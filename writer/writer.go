package writer

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heistp/cgmon/analyzer"
	"github.com/heistp/cgmon/metrics"
)

type Config struct {
	Dir              string
	File             string
	CompressionLevel int
	Flush            bool
	RotateInterval   time.Duration
	RotateSize       uint64
	Partial          bool
	Log              bool
}

type Writer struct {
	Config
	metrics *metrics.Metrics
	enc     *json.Encoder
	writer  flushWriter
	sync.Mutex
}

func Open(cfg Config, m *metrics.Metrics) (w *Writer, err error) {
	var writer flushWriter
	if cfg.Dir != "" {
		// compressed: fileWriter -> gzip -> countWriter -> buf -> file
		if writer, err = newFileWriter(&cfg); err != nil {
			return
		}
	} else {
		if cfg.Log {
			log.Printf("writer using stdout")
		}
		writer = bufio.NewWriter(os.Stdout)
	}

	var enc *json.Encoder
	if enc = json.NewEncoder(writer); err != nil {
		return
	}
	enc.SetIndent("", "\t")

	w = &Writer{
		cfg,
		m,
		enc,
		writer,
		sync.Mutex{},
	}

	return
}

func (w *Writer) Write(ss []*analyzer.FlowStats) (err error) {
	w.Lock()
	defer w.Unlock()

	if len(ss) == 0 {
		return
	}

	t0 := time.Now()

	for _, s := range ss {
		if w.Partial || !s.Partial {
			if err = w.enc.Encode(s); err != nil {
				return
			}
		}
	}

	if w.Flush {
		w.writer.Flush()
	}

	el := time.Since(t0)
	w.metrics.PushWriter(el)

	if w.Log {
		log.Printf("writer time=%s flows=%d", el, len(ss))
	}

	return
}

func (w *Writer) Close() (err error) {
	w.Lock()
	defer w.Unlock()

	if c, ok := w.writer.(io.Closer); ok {
		err = c.Close()
	} else if f, ok := w.writer.(flushWriter); ok {
		err = f.Flush()
	}

	return
}

// flushWriter is a Writer that can flush, and is implemented by both fileWriter
// and naturally by bufio.Writer.
type flushWriter interface {
	io.Writer

	Flush() error
}

// fileWriter is an io.Writer with file rotation support.
type fileWriter struct {
	*Config
	path       string
	file       *os.File
	bfw        *bufio.Writer
	writer     flushWriter
	cw         *countWriter
	lastRotate time.Time
}

func newFileWriter(cfg *Config) (w *fileWriter, err error) {
	var di os.FileInfo
	var path string
	if di, err = os.Stat(cfg.Dir); err != nil {
		return
	}
	if !di.IsDir() {
		err = fmt.Errorf("writer directory '%s' not a directory", cfg.Dir)
		return
	}

	path = filepath.Join(cfg.Dir, cfg.File)

	w = &fileWriter{
		cfg,
		path,
		nil,
		nil,
		nil,
		nil,
		time.Time{},
	}

	if err = w.open(false); err != nil {
		return
	}

	err = w.maybeRotate()

	return
}

func (w *fileWriter) Write(p []byte) (n int, err error) {
	if n, err = w.writer.Write(p); err != nil {
		return
	}

	if w.lastRotate.IsZero() { // set last rotate time on first write
		w.lastRotate = time.Now()
	}
	err = w.maybeRotate()

	return
}

func (w *fileWriter) Flush() (err error) {
	if f, ok := w.writer.(flushWriter); ok {
		if err = f.Flush(); err != nil {
			return err
		}
	}
	if w.writer != w.bfw {
		err = w.bfw.Flush()
	}
	return
}

func (w *fileWriter) Close() (err error) {
	if c, ok := w.writer.(io.Closer); ok {
		if err = c.Close(); err != nil {
			log.Printf("file writer error on close: %s", err)
		}
	}

	if err = w.bfw.Flush(); err != nil {
		log.Printf("bufio writer error on flush: %s", err)
	}

	if err = w.file.Close(); err != nil {
		log.Printf("file writer error closing underlying file: %s", err)
	}

	return
}

func (w *fileWriter) open(quiet bool) (err error) {
	if !quiet && w.Log {
		log.Printf("writer opening output file %s", w.path)
	}

	var fi os.FileInfo
	var sz uint64
	if fi, err = os.Stat(w.path); err == nil {
		sz = uint64(fi.Size())
	} else if !os.IsNotExist(err) {
		return
	}

	if w.file, err = os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err != nil {
		return
	}

	w.bfw = bufio.NewWriter(w.file)
	w.cw = &countWriter{w.bfw, sz}

	if filepath.Ext(w.File) == ".gz" {
		if !quiet && w.Log {
			log.Printf("writer using gzip compression level %d", w.CompressionLevel)
		}
		if w.writer, err = gzip.NewWriterLevel(w.cw, w.CompressionLevel); err != nil {
			return
		}
	} else {
		w.writer = w.cw
	}

	return
}

func (w *fileWriter) maybeRotate() (err error) {
	if w.RotateSize > 0 && w.cw.count >= w.RotateSize {
		if w.Log {
			log.Printf("writer rotating %s at %d bytes", w.path, w.cw.count)
		}
		err = w.rotate()
	} else if w.RotateInterval > 0 && !w.lastRotate.IsZero() {
		s := time.Since(w.lastRotate)
		if s > w.RotateInterval {
			if w.cw.count == 0 { // reset interval and don't rotate if no data
				w.lastRotate = time.Now()
				return
			}
			if w.Log {
				log.Printf("writer rotating %s after %s", w.path, s)
			}
			err = w.rotate()
		}
	}

	return
}

func (w *fileWriter) rotate() (err error) {
	if c, ok := w.writer.(io.Closer); ok {
		if err = c.Close(); err != nil {
			return err
		}
	}

	if err = w.bfw.Flush(); err != nil {
		return err
	}

	if err = w.file.Close(); err != nil {
		return
	}

	var np string
	for i := 1; ; i++ {
		var gz bool
		np, gz = w.rotatedFilename(i)
		var np2 string
		if gz {
			np2 = strings.TrimSuffix(np, ".gz")
		} else {
			np2 = np + ".gz"
		}
		_, npe := os.Stat(np)
		_, np2e := os.Stat(np2)
		if os.IsNotExist(npe) && os.IsNotExist(np2e) {
			break
		}
	}

	if w.Log {
		log.Printf("renaming %s to %s", w.path, np)
	}

	if err = os.Rename(w.path, np); err != nil {
		return
	}

	err = w.open(true)

	if w.RotateInterval > 0 {
		w.lastRotate = time.Now()
	}

	return
}

func (w *fileWriter) rotatedFilename(n int) (rp string, gz bool) {
	var ext string
	var ext2 string

	rp = w.path
	ext = filepath.Ext(rp)
	rp = strings.TrimSuffix(rp, ext)
	if ext == ".gz" {
		ext2 = filepath.Ext(rp)
		rp = strings.TrimSuffix(rp, ext2)
		gz = true
	}
	rp += "."
	rp += strconv.Itoa(n)
	rp += ext2
	rp += ext

	return
}

type countWriter struct {
	uw    io.Writer
	count uint64
}

func (c *countWriter) Write(p []byte) (n int, err error) {
	n, err = c.uw.Write(p)
	c.count += uint64(n)
	return
}

func (c *countWriter) Flush() (err error) {
	if f, ok := c.uw.(flushWriter); ok {
		err = f.Flush()
	}
	return
}
