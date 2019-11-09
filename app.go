package main

import (
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/heistp/cgmon/analyzer"
	"github.com/heistp/cgmon/netlink"
	"github.com/heistp/cgmon/sampler"
	"github.com/heistp/cgmon/tracker"
	"github.com/heistp/cgmon/writer"
)

// Notes/Questions:
// - The correlation between retransmits and cwnd always appears to be around 0,
//   leading me to believe this it isn't done the right way or what we want.
// - Ok calculation for snd_cwnd_bytes?
// - There may not be enough data on shorter flows to calculate statistically
//   significant correlations, so we may need to use -tracker-min-samples
// - Should I rename Src/Dst to Local/Remote? Src/Dst used in tcphdr. Can
//   I tell if it's a client or server socket from tcphdr or tcp_info?

type Config struct {
	Netlink     netlink.Config  // netlink config
	Tracker     tracker.Config  // tracker config
	Analyzer    analyzer.Config // analyzer config
	Writer      writer.Config   // writer config
	Serial      bool            // if true, execute pipe in one goroutine
	HTTPAddr    string          // listen address of metrics server
	Interval    time.Duration   // time between sample calls
	Duration    time.Duration   // limit on run time
	MaxErrors   int             // maximum consecutive errors
	ErrorDelay  time.Duration   // initial exponential backoff time between errors
	StopTimeout time.Duration   // time to wait on stop request
}

type App struct {
	*Config
	sampler  sampler.Sampler
	tracker  *tracker.Tracker
	analyzer *analyzer.Analyzer
	writer   *writer.Writer
	errs     int
	dur      <-chan time.Time
	stop     chan bool
	done     chan bool
	rc       chan sampler.Result
	sc       chan []sampler.Sample
	fc       chan []*tracker.Flow
	fsc      chan []*analyzer.FlowStats
	errc     chan error
}

func NewApp(cfg *Config) (a *App, err error) {
	var w *writer.Writer
	if w, err = writer.Open(cfg.Writer); err != nil {
		return
	}

	a = &App{cfg,
		netlink.NewSampler(cfg.Netlink),
		tracker.NewTracker(cfg.Tracker),
		analyzer.NewAnalyzer(cfg.Analyzer),
		w,
		0,
		make(<-chan time.Time),
		make(chan bool),
		make(chan bool),
		make(chan sampler.Result, 128),
		make(chan []sampler.Sample, 256),
		make(chan []*tracker.Flow, 256),
		make(chan []*analyzer.FlowStats, 1024),
		make(chan error, 1),
	}

	return
}

func (a *App) Run() (err error) {
	defer close(a.done)
	defer func() {
		if e := a.writer.Close(); e != nil {
			log.Printf("error closing writer (%s)", e)
		}
	}()
	defer func() {
		if c, ok := a.sampler.(sampler.Closer); ok {
			if e := c.Close(); e != nil {
				log.Printf("error closing sampler (%s)", e)
			}
		}
	}()

	if a.HTTPAddr != "" {
		go a.httpServer()
	}

	if !a.Serial {
		go a.convert()
		go a.track()
		go a.analyze()
		go a.write()
	}

	if a.Duration > 0 {
		a.dur = time.After(a.Duration)
	}

	stopped := false
Outer:
	for !stopped {
		if a.errs >= a.MaxErrors {
			err = fmt.Errorf("aborted after %d consecutive errors", a.errs)
			break
		} else if a.errs > 0 {
			if stopped, err = a.waitOnError(); stopped || err != nil {
				break
			}
		}

		tck := time.NewTicker(a.Interval)
		for !stopped {
			if stopped, err = a.wait(tck.C); stopped || err != nil {
				break
			}

			var r sampler.Result
			if r, err = a.sampler.Sample(); err != nil {
				a.errs++
				log.Printf("error[%d] getting sample (%s)", a.errs, err)
				break
			}
			a.errs = 0

			if r == nil {
				log.Printf("stopping due to nil sampler result")
				break Outer
			}

			if a.Serial {
				if err = a.processSerial(r); err != nil {
					break Outer
				}
			} else {
				a.rc <- r
			}
		}
	}

	if !a.Serial {
		log.Println("shutting down pipeline")
		close(a.rc)
		if e := <-a.errc; e != nil {
			log.Printf("pipeline error during close (%s)", err)
			if err == nil {
				err = e
			}
		}
	}

	return
}

func (a *App) netlinkSampler() (s *netlink.Sampler) {
	var ok bool
	if s, ok = a.sampler.(*netlink.Sampler); !ok {
		panic("sampler isn't netlink")
	}
	return
}

func (a *App) DumpMetrics() (s string) {
	sb := &strings.Builder{}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	nm := a.netlinkSampler().Metrics()
	tm := a.tracker.Metrics()
	am := a.analyzer.Metrics()
	wm := a.writer.Metrics()

	w := tabwriter.NewWriter(sb, 0, 0, 2, ' ', 0)

	fmt.Fprintf(w, "Tracking %d flows\n\n", tm.TrackedFlows)

	fmt.Fprintf(w, "Churn rate (flows/sec):\n")
	fmt.Fprintf(w, "-----------------------\n\n")
	fmt.Fprintf(w, "Instantaneous\t%.2f\n", tm.InstChurnRate)
	fmt.Fprintf(w, "Mean\t%.2f\n", tm.ChurnRate())
	fmt.Fprintf(w, "\n")

	nt := nm.SampleTimes
	ct := nm.ConvertTimes
	tt := tm.TrackTimes
	at := am.AnalyzeTimes
	wt := wm.WriteTimes
	fmt.Fprintf(w, "Pipeline Stage Times (in μs):\n")
	fmt.Fprintf(w, "-----------------------------\n\n")
	fmt.Fprintf(w, "Stage\tCalls\tMin\tMean\tMax\tStddev\n")
	fmt.Fprintf(w, "Netlink\t%d\t%d\t%d\t%d\t%d\n",
		nt.N, us(nt.Min), us(nt.Mean()), us(nt.Max), us(nt.Stddev()))
	fmt.Fprintf(w, "Conversion\t%d\t%d\t%d\t%d\t%d\n",
		ct.N, us(ct.Min), us(ct.Mean()), us(ct.Max), us(ct.Stddev()))
	fmt.Fprintf(w, "Tracker\t%d\t%d\t%d\t%d\t%d\n",
		tt.N, us(tt.Min), us(tt.Mean()), us(tt.Max), us(tt.Stddev()))
	fmt.Fprintf(w, "Analyzer\t%d\t%d\t%d\t%d\t%d\n",
		at.N, us(at.Min), us(at.Mean()), us(at.Max), us(at.Stddev()))
	fmt.Fprintf(w, "Writer\t%d\t%d\t%d\t%d\t%d\n",
		wt.N, us(wt.Min), us(wt.Mean()), us(wt.Max), us(wt.Stddev()))
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "Memory Stats:\n")
	fmt.Fprintf(w, "-------------\n\n")
	fmt.Fprintf(w, "Heap alloc objects\t%d\n", ms.HeapAlloc)
	fmt.Fprintf(w, "Heap total objects\t%d\n", ms.TotalAlloc)
	fmt.Fprintf(w, "Sys (OS virt size)\t%d\n", ms.Sys)
	fmt.Fprintf(w, "Mallocs\t%d\n", ms.Mallocs)
	fmt.Fprintf(w, "Frees\t%d\n", ms.Frees)
	fmt.Fprintf(w, "Live objects\t%d\n", ms.Mallocs-ms.Frees)
	w.Flush()

	s = sb.String()
	return
}

func (a *App) Stop() (err error) {
	log.Printf("stopping (waiting up to %s for stop)", a.StopTimeout)
	close(a.stop)
	select {
	case <-a.done:
	case <-time.After(a.StopTimeout):
		err = fmt.Errorf("wait for stop timed out")
	}
	return
}

func (a *App) processSerial(r sampler.Result) (err error) {
	s := r.Samples()

	if rr, ok := a.sampler.(sampler.ResultRecycler); ok {
		rr.RecycleResult(r)
	}

	ef := a.tracker.Track(s)

	if sr, ok := a.sampler.(sampler.SamplesRecycler); ok {
		sr.RecycleSamples(s)
	}

	fs := a.analyzer.Analyze(ef)

	err = a.writer.Write(fs)

	return
}

func (a *App) convert() {
	defer close(a.sc)
	for r := range a.rc {
		a.sc <- r.Samples()

		if rr, ok := a.sampler.(sampler.ResultRecycler); ok {
			rr.RecycleResult(r)
		}
	}
}

func (a *App) track() {
	defer close(a.fc)
	for s := range a.sc {
		a.fc <- a.tracker.Track(s)

		if sr, ok := a.sampler.(sampler.SamplesRecycler); ok {
			sr.RecycleSamples(s)
		}
	}
}

func (a *App) analyze() {
	defer close(a.fsc)
	for f := range a.fc {
		a.fsc <- a.analyzer.Analyze(f)
	}
}

func (a *App) write() {
	defer close(a.errc)
	for fs := range a.fsc {
		if err := a.writer.Write(fs); err != nil {
			a.errc <- err
			break
		}
	}
}

func (a *App) waitOnError() (stopped bool, err error) {
	d := a.ErrorDelay << uint(a.errs-1)
	log.Printf("waiting %s", d)
	stopped, err = a.wait(time.After(d))
	return
}

func (a *App) wait(ch <-chan time.Time) (stopped bool, err error) {
	stopped = true
	select {
	case <-a.stop:
	case <-a.dur:
		log.Printf("stopping after duration %s", a.Duration)
	case err = <-a.errc:
		log.Printf("pipeline error (%s)", err)
	case <-ch:
		stopped = false
	}
	return
}

func (a *App) httpServer() {
	http.Handle("/", newRootHandler(a))
	http.Handle("/flow-duration-histogram", &flowDurationHistogramHandler{a.analyzer})
	log.Printf("starting http server on %s", a.HTTPAddr)
	if err := http.ListenAndServe(a.HTTPAddr, nil); err != nil {
		log.Printf("http server exiting due to error (%s)", err)
	}
}

func us(d time.Duration) int64 {
	return int64(d) / 1e3
}
