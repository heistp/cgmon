package main

import (
	"flag"
	"fmt"
	"log"
	"log/syslog"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/heistp/cgmon/analyzer"
	"github.com/heistp/cgmon/netlink"
	"github.com/heistp/cgmon/prof"
	"github.com/heistp/cgmon/tracker"
	"github.com/heistp/cgmon/writer"
	"gonum.org/v1/gonum/stat"
)

// Defaults.
const (
	DEFAULT_ANALYZER_ADJUSTED_CORRELATION_1  = false
	DEFAULT_ANALYZER_ADJUSTED_CORRELATION_2  = false
	DEFAULT_ANALYZER_CUMULANT_KIND           = "lininterp"
	DEFAULT_ANALYZER_UNWEIGHTED_CORRELATIONS = false
	DEFAULT_ANALYZER_UNWEIGHTED_QUANTILES    = false
	DEFAULT_LOG_ALL                          = false
	DEFAULT_LOG_ANALYZER                     = false
	DEFAULT_LOG_NETLINK                      = false
	DEFAULT_LOG_SYSLOG                       = false
	DEFAULT_LOG_TRACKER                      = false
	DEFAULT_LOG_WRITER                       = false
	DEFAULT_NETLINK_DPORT                    = ""
	DEFAULT_NETLINK_READ_BUFSIZE             = 32 * 1024
	DEFAULT_NETLINK_RECEIVE_BUFSIZE          = 0
	DEFAULT_NETLINK_RECEIVE_BUFSIZE_FORCE    = 0
	DEFAULT_NETLINK_RECEIVE_TIMEOUT          = 1 * time.Second
	DEFAULT_NETLINK_SPORT                    = ""
	DEFAULT_RUN_DURATION                     = time.Duration(0)
	DEFAULT_RUN_ERROR_DELAY                  = 1 * time.Second
	DEFAULT_RUN_HTTP_SERVER                  = ""
	DEFAULT_RUN_INTERVAL                     = 1 * time.Second
	DEFAULT_RUN_MAX_ERRORS                   = 5
	DEFAULT_RUN_SERIAL                       = false
	DEFAULT_RUN_SHUTDOWN_TIMEOUT             = 15 * time.Second
	DEFAULT_TRACKER_MAX_FLOWS                = 0
	DEFAULT_TRACKER_MIN_SAMPLES              = 0
	DEFAULT_WRITER_COMPRESSION_LEVEL         = 9
	DEFAULT_WRITER_DIR                       = ""
	DEFAULT_WRITER_FLUSH                     = false
	DEFAULT_WRITER_PARTIAL                   = false
	DEFAULT_WRITER_ROTATE_INTERVAL           = 15 * time.Minute
	DEFAULT_WRITER_ROTATE_SIZE               = ""
)

func main() {
	var err error

	// start profiling, if enabled in build
	if prof.ProfileEnabled {
		defer prof.StartProfile("./cgmon.pprof").Stop()
	}

	var hostname string
	var defaultWriterFile string
	if hostname, err = os.Hostname(); err != nil {
		defaultWriterFile = "cgmon.json.gz"
	} else {
		defaultWriterFile = "cgmon-" + hostname + ".json.gz"
	}

	var ac1 = flag.Bool("analyzer-adjusted-correlation-1", DEFAULT_ANALYZER_ADJUSTED_CORRELATION_1,
		"use adjusted correlation coefficient r_adj = r * (1 + (1-r*r)/2*n) (Wikipedia PCC)")
	var ac2 = flag.Bool("analyzer-adjusted-correlation-2", DEFAULT_ANALYZER_ADJUSTED_CORRELATION_2,
		"use adjusted correlation coefficient r_adj = sqrt(1 - ((1-r*r)*(n-1))/(n-2)) (only applied with more than 2 samples)")
	var ack = flag.String("analyzer-cumulant-kind", DEFAULT_ANALYZER_CUMULANT_KIND,
		"for seven number summaries, empirical: use only measured values, lininterp: do linear interpolation")
	var auc = flag.Bool("analyzer-unweighted-correlations",
		DEFAULT_ANALYZER_UNWEIGHTED_CORRELATIONS,
		"do not use weights for correlation stats (otherwise use time between samples)")
	var auq = flag.Bool("analyzer-unweighted-quantiles",
		DEFAULT_ANALYZER_UNWEIGHTED_QUANTILES,
		"do not use weights for quantiles needed for seven number summaries (otherwise use time between samples)")
	var lal = flag.Bool("log-all", DEFAULT_LOG_ALL, "enable all logging")
	var lga = flag.Bool("log-analyzer", DEFAULT_LOG_ANALYZER, "enable analyzer logging")
	var lgn = flag.Bool("log-netlink", DEFAULT_LOG_NETLINK, "enable netlink logging")
	var lgy = flag.Bool("log-syslog", DEFAULT_LOG_SYSLOG, "send logging to syslog")
	var lgt = flag.Bool("log-tracker", DEFAULT_LOG_TRACKER, "enable tracker logging")
	var lgw = flag.Bool("log-writer", DEFAULT_LOG_WRITER, "enable writer logging")
	var ndp = flag.String("netlink-dport", DEFAULT_NETLINK_DPORT,
		"kernel space filter on dest (peer) port ranges (format: a,b-c)")
	var nrb = flag.Int("netlink-read-bufsize", DEFAULT_NETLINK_READ_BUFSIZE,
		"netlink receive buffer size (>32K no benefit at least in kernels 4.9-5.2)")
	var nsb = flag.Int("netlink-receive-bufsize", DEFAULT_NETLINK_RECEIVE_BUFSIZE,
		"netlink socket receive buffer size")
	var nsbf = flag.Int("netlink-receive-bufsize-force", DEFAULT_NETLINK_RECEIVE_BUFSIZE_FORCE,
		"netlink socket force receive buffer size, requires CAP_NET_ADMIN or root")
	var nrt = flag.Duration("netlink-receive-timeout", DEFAULT_NETLINK_RECEIVE_TIMEOUT,
		"netlink socket receive timeout")
	var nsp = flag.String("netlink-sport", DEFAULT_NETLINK_SPORT,
		"kernel space filter on source (local) port ranges (format: a,b-c)")
	var rdr = flag.Duration("run-duration", DEFAULT_RUN_DURATION,
		"run duration (units required, default unlimited)")
	var red = flag.Duration("run-error-delay", DEFAULT_RUN_ERROR_DELAY,
		"initial exponential backoff wait time after sample error occurs")
	var riv = flag.Duration("run-interval", DEFAULT_RUN_INTERVAL, "sample interval (units required)")
	var rme = flag.Int("run-max-errors", DEFAULT_RUN_MAX_ERRORS,
		"maximum number of consective sample errors before exit occurs")
	var rsr = flag.Bool("run-serial", DEFAULT_RUN_SERIAL,
		"execute pipeline in one, instead of multiple goroutines (threads)")
	var rhs = flag.String("run-http-server", DEFAULT_RUN_HTTP_SERVER,
		"listen host/port of http server for metrics (e.g. :8080 or localhost:8080)")
	var rst = flag.Duration("run-shutdown-timeout", DEFAULT_RUN_SHUTDOWN_TIMEOUT,
		"time to wait after signal for completion of shutdown")
	var tmf = flag.Int("tracker-max-flows", DEFAULT_TRACKER_MAX_FLOWS,
		"programmatic limit on max number of active flows (flows still sampled and tracked)")
	var tms = flag.Int("tracker-min-samples", DEFAULT_TRACKER_MIN_SAMPLES,
		"programmatic limit on minimum number of samples required to return ended flows for further processing")
	var wcl = flag.Int("writer-compression-level", DEFAULT_WRITER_COMPRESSION_LEVEL,
		"gzip compression level to use (1 to 9 where 9 is best compression)")
	var wdr = flag.String("writer-dir", DEFAULT_WRITER_DIR,
		"write output to files in this directory (if unset, write to stdout)")
	var wfi = flag.String("writer-file", defaultWriterFile,
		"output filename (extension .gz means use compression, suggested extension .json or json.gz)")
	var wfl = flag.Bool("writer-flush", DEFAULT_WRITER_FLUSH,
		"flush after every group of results is written (may degrade compression)")
	var wri = flag.Duration("writer-rotate-interval", DEFAULT_WRITER_ROTATE_INTERVAL,
		"approximate interval on which to rotate output files (units required, e.g. 30s, 15m, 1h)")
	var wrs = flag.String("writer-rotate-size", DEFAULT_WRITER_ROTATE_SIZE,
		"approximate output file size to trigger rotation (suffixes K, M and G supported)")
	var wpl = flag.Bool("writer-partial", DEFAULT_WRITER_PARTIAL,
		"write flow results that are missing samples (cross startup or shutdown boundaries)")
	var ver = flag.Bool("version", false, "show version number")
	flag.Parse()

	if *ver {
		fmt.Printf("%s version %s\n", os.Args[0], VERSION)
		os.Exit(0)
	}

	if *lal {
		*lga = true
		*lgn = true
		*lgt = true
		*lgw = true
	}

	if *lgy {
		var w *syslog.Writer
		if w, err = syslog.New(syslog.LOG_NOTICE, "cgmon"); err != nil {
			log.Fatalf("unable to open syslog (%s)", err)
		}
		log.Println("sending logging to syslog")
		log.SetOutput(w)
	}

	var sports []uint16
	if *nsp != "" {
		if sports, err = parsePortRanges(*nsp); err != nil {
			log.Fatalf("invalid source port range %s (%s)", *nsp, err)
		}
	}
	var dports []uint16
	if *ndp != "" {
		if dports, err = parsePortRanges(*ndp); err != nil {
			log.Fatalf("invalid dest port range %s (%s)", *ndp, err)
		}
	}

	var ackind stat.CumulantKind
	if *ack == "empirical" {
		ackind = stat.Empirical
	} else if *ack == "lininterp" {
		ackind = stat.LinInterp
	} else {
		log.Fatalf("unrecognized cumulant kind: %s", *ack)
	}

	var rotateSize uint64
	if *wrs != "" {
		m := uint64(1)
		if strings.HasSuffix(*wrs, "K") {
			m = 1024
			*wrs = strings.TrimSuffix(*wrs, "K")
		} else if strings.HasSuffix(*wrs, "M") {
			m = 1024 * 1024
			*wrs = strings.TrimSuffix(*wrs, "M")
		} else if strings.HasSuffix(*wrs, "G") {
			m = 1024 * 1024 * 1024
			*wrs = strings.TrimSuffix(*wrs, "G")
		} else {
			m = 1
		}
		if rotateSize, err = strconv.ParseUint(*wrs, 10, 64); err != nil {
			log.Fatalf("unable to parse writer rotate size: %s", *wrs)
		}
		rotateSize *= m
	}

	if *wcl < 1 || *wcl > 9 {
		log.Fatalf("invalid compression level %d, must be 1-9", *wcl)
	}

	if *ac1 && *ac2 {
		log.Fatalf("multiple adjusted correlations may not be used at the same time")
	}

	cfg := &Config{
		netlink.Config{
			*nrb,
			*nsb,
			*nsbf,
			sports,
			dports,
			*nrt,
			*lgn,
		},
		tracker.Config{
			*tmf,
			*tms,
			*lgt,
		},
		analyzer.Config{
			*riv,
			ackind,
			*auc,
			*auq,
			*ac1,
			*ac2,
			*lga,
		},
		writer.Config{
			*wdr,
			*wfi,
			*wcl,
			*wfl,
			*wri,
			rotateSize,
			*wpl,
			*lgw,
		},
		*rsr,
		*rhs,
		*riv,
		*rdr,
		*rme,
		*red,
		*rst,
	}

	log.Printf("cgmon version %s started", VERSION)

	run(cfg)
}

func run(cfg *Config) {
	var a *App
	var err error

	if a, err = NewApp(cfg); err != nil {
		log.Fatalf("initialization failed (%s)", err)
	}

	done := make(chan bool, 2)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1, syscall.SIGUSR2)
	go func() {
		defer func() {
			done <- true
		}()
		printMetrics := func() {
			log.Printf("reading metrics\n" + a.DumpMetrics())
		}
		for {
			sig := <-sigs
			log.Println("received signal:", sig)
			if sig == syscall.SIGUSR1 {
				printMetrics()
			} else if sig == syscall.SIGUSR2 {
				log.Println("running full GC")
				runtime.GC()
				printMetrics()
			} else {
				if err := a.Stop(); err != nil {
					log.Printf("error on stop (%s)", err)
				}
				break
			}
		}
	}()

	go func() {
		defer func() {
			done <- true
		}()
		if err := a.Run(); err != nil {
			log.Fatalf("run failed (%s)", err)
		} else {
			log.Println("successful termination")
		}
	}()

	<-done
}

// parsePortRanges takes a comma separated list of dash separated ranges and
// returns those ranges as tuples flattened inrt a slice.
func parsePortRanges(s string) (ranges []uint16, err error) {
	for _, cp := range strings.Split(s, ",") {
		var lo, hi int
		dp := strings.Split(cp, "-")
		if lo, err = strconv.Atoi(dp[0]); err != nil {
			return
		}
		if len(dp) > 1 {
			if hi, err = strconv.Atoi(dp[1]); err != nil {
				return
			}
		} else {
			hi = lo
		}
		ranges = append(ranges, uint16(lo), uint16(hi))
	}
	return
}
