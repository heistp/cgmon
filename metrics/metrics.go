package metrics

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

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

func (s *DurationStats) Push(d time.Duration) {
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

// DurationHistogram is a histogram divided into N adjacent
// configurable ranges.
type DurationHistogram struct {
	counts     [][]int
	starts     []time.Duration
	ends       []time.Duration
	steps      []time.Duration
	N          int
	OutOfRange int
	sync.RWMutex
}

func NewDurationHistogram(steps, ends []time.Duration) (h DurationHistogram) {
	if len(steps) != len(ends) {
		panic("duration histogram steps and ends must be same size")
	}

	h.steps = steps
	h.ends = ends

	h.counts = make([][]int, len(ends))
	h.starts = make([]time.Duration, len(ends))
	for i := 0; i < len(ends); i++ {
		var start time.Duration
		if i > 0 {
			start = ends[i-1]
		}
		bins := (ends[i] - start) / steps[i]
		h.counts[i] = make([]int, bins)
		h.starts[i] = start
	}

	return
}

// Push adds the duration to the histogram.
func (h *DurationHistogram) Push(d time.Duration) {
	h.Lock()
	defer h.Unlock()

	if d < 0 || d >= h.ends[len(h.ends)-1] {
		h.OutOfRange++
		return
	}

	for i := 0; i < len(h.ends); i++ {
		if d >= h.starts[i] && d < h.ends[i] {
			base := d - h.starts[i]
			h.counts[i][base/h.steps[i]]++
			h.N++
			break
		}
	}

	return
}

// String returns the histogram with the maximum specified width, and
// a percentage of values that are out of range.
func (h *DurationHistogram) String(width int) (s string, oorp float64) {
	h.RLock()
	h.RUnlock()

	sb := &strings.Builder{}
	w := tabwriter.NewWriter(sb, 0, 0, 2, ' ', 0)

	for i := 0; i < len(h.counts); i++ {
		min, max, tot, fps := h.rangeStats(i)
		tick := float64(1)
		if max > width {
			tick = float64(max) / float64(width)
		}

		fmt.Fprintf(w, "\n")
		fmt.Fprintf(w, "---- %s-%s (width=%s), step=%s, min/max/total=%d/%d/%d flows, 1 tick=%.1f flows, density=%.1f flows/sec ----\n",
			h.starts[i], h.ends[i], h.ends[i]-h.starts[i],
			h.steps[i], min, max, tot, tick, fps)
		fmt.Fprintf(w, "Range\tFlows\tGraph\n")
		for j := 0; j < len(h.counts[i]); j++ {
			dj := time.Duration(j)
			fmt.Fprintf(w, "%s-%s\t",
				h.starts[i]+dj*h.steps[i],
				h.starts[i]+(dj+1)*h.steps[i])
			fmt.Fprintf(w, "%d\t", h.counts[i][j])
			t := float64(h.counts[i][j]) / tick
			for l := 0; l < int(t); l++ {
				fmt.Fprintf(w, "#")
			}
			fmt.Fprintf(w, "\n")
		}
	}

	w.Flush()
	s = sb.String()

	oorp = 100 * float64(h.OutOfRange) / (float64(h.OutOfRange) + float64(h.N))

	return
}

func (h *DurationHistogram) rangeStats(rangeIndex int) (min, max, tot int, fps float64) {
	for j := 0; j < len(h.counts[rangeIndex]); j++ {
		c := h.counts[rangeIndex][j]
		if j == 0 || c < min {
			min = c
		}
		if c > max {
			max = c
		}
		tot += c
	}

	fps = float64(tot) / (time.Duration(h.ends[rangeIndex] - h.starts[rangeIndex]).Seconds())

	return
}
