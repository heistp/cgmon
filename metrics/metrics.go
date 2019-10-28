package metrics

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

type Metrics struct {
	StartTime       time.Time
	NetlinkTimes    DurationStats
	ConversionTimes DurationStats
	TrackerTimes    DurationStats
	AnalyzerTimes   DurationStats
	WriterTimes     DurationStats

	TrackedFlows int
	EndedFlows   uint64

	PriorTrackerTime time.Time
	PriorEndedFlows  uint64
	InstChurnRate    float64
	sync.RWMutex
}

func NewMetrics() *Metrics {
	return &Metrics{
		time.Now(),
		DurationStats{},
		DurationStats{},
		DurationStats{},
		DurationStats{},
		DurationStats{},
		0,
		0,
		time.Now(),
		0,
		0,
		sync.RWMutex{},
	}
}

func (m *Metrics) PushNetlink(d time.Duration) {
	m.Lock()
	defer m.Unlock()
	m.NetlinkTimes.push(d)
}

func (m *Metrics) PushConversion(d time.Duration) {
	m.Lock()
	defer m.Unlock()
	m.ConversionTimes.push(d)
}

func (m *Metrics) PushTracker(d time.Duration, tf int, ef int) {
	m.Lock()
	defer m.Unlock()
	now := time.Now()
	m.TrackerTimes.push(d)
	m.TrackedFlows = tf
	m.EndedFlows += uint64(ef)
	m.InstChurnRate = (float64(m.EndedFlows) - float64(m.PriorEndedFlows)) /
		float64(now.Sub(m.PriorTrackerTime).Seconds())
	m.PriorEndedFlows = m.EndedFlows
	m.PriorTrackerTime = now
}

func (m *Metrics) PushAnalyzer(d time.Duration) {
	m.Lock()
	defer m.Unlock()
	m.AnalyzerTimes.push(d)
}

func (m *Metrics) PushWriter(d time.Duration) {
	m.Lock()
	defer m.Unlock()
	m.WriterTimes.push(d)
}

func (m *Metrics) ChurnRate() float64 {
	return float64(m.EndedFlows) / float64(time.Since(m.StartTime).Seconds())
}

func (m *Metrics) String() (s string) {
	sb := &strings.Builder{}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	w := tabwriter.NewWriter(sb, 0, 0, 2, ' ', 0)

	m.RLock()
	mc := *m
	m.RUnlock()

	fmt.Fprintf(w, "Tracking %d flows\n\n", mc.TrackedFlows)

	fmt.Fprintf(w, "Churn rate (flows/sec):\n")
	fmt.Fprintf(w, "-----------------------\n\n")
	fmt.Fprintf(w, "Instantaneous\t%.2f\n", mc.InstChurnRate)
	fmt.Fprintf(w, "Mean\t%.2f\n", mc.ChurnRate())
	fmt.Fprintf(w, "\n")

	nt := mc.NetlinkTimes
	ct := mc.ConversionTimes
	tt := mc.TrackerTimes
	at := mc.AnalyzerTimes
	wt := mc.WriterTimes
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

func us(d time.Duration) int64 {
	return int64(d) / 1e3
}

// DurationStats keeps basic time.Duration statistics. Welford's method is used
// to keep a running mean and standard deviation.
type DurationStats struct {
	Total time.Duration
	N     uint
	Min   time.Duration
	Max   time.Duration
	m     float64
	s     float64
	mean  float64
}

func (s *DurationStats) push(d time.Duration) {
	if s.N == 0 {
		s.Min = d
		s.Max = d
		s.Total = d
	} else {
		if d < s.Min {
			s.Min = d
		}
		if d > s.Max {
			s.Max = d
		}
		s.Total += d
	}
	s.N++
	om := s.mean
	fd := float64(d)
	s.mean += (fd - om) / float64(s.N)
	s.s += (fd - om) * (fd - s.mean)
}

func (s *DurationStats) IsZero() bool {
	return s.N == 0
}

func (s *DurationStats) Mean() time.Duration {
	return time.Duration(s.mean)
}

func (s *DurationStats) Variance() float64 {
	if s.N > 1 {
		return s.s / float64(s.N-1)
	}
	return 0.0
}

func (s *DurationStats) Stddev() time.Duration {
	return time.Duration(math.Sqrt(s.Variance()))
}
