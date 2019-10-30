package analyzer

import (
	"log"
	"math"
	"net"
	"sort"
	"time"

	"github.com/heistp/cgmon/linux"
	"github.com/heistp/cgmon/metrics"
	"github.com/heistp/cgmon/sampler"
	"github.com/heistp/cgmon/tracker"
	"gonum.org/v1/gonum/stat"
)

// snsPcts  are the seven-number summary percentiles
var snsPcts = [7]float64{0.02, 0.09, 0.25, 0.5, 0.75, 0.91, 0.98}

const CORR_UNDEFINED = -2

const CORR_INSUFFICIENT_SAMPLES = -3

const debug = false

// An ID uniquely identifies flows within program execution. A monotonic
// timestamp from the first sample is added to distinguish between flows with
// the same 5-tuple.
type ID struct {
	SrcIP         net.IP
	SrcPort       uint16
	DstIP         net.IP
	DstPort       uint16
	TstampStartNs uint64
}

// A FlowStats contains the data and statistics that are saved to the output.
type FlowStats struct {
	ID                        ID            // flow ID
	StartTime                 time.Time     // start time
	EndTime                   time.Time     // end time
	Duration                  time.Duration // duration from first to last sample
	Samples                   int           // number of unique samples
	SamplesDeduped            int           // number of samples de-duped
	Partial                   bool          // true if flow was pre-existing or had no last sample on shutdown
	Timestamps                bool          // true if flow had timestamps enabled (TCPI_OPT_TIMESTAMPS)
	SACK                      bool          // true if flow had SACK enabled (TCPI_OPT_SACK)
	ECN                       bool          // true if flow had ECN enabled (TCPI_OPT_ECN)
	ECNSeen                   bool          // true if at least one packet _received_ with ECT (TCPI_OPT_ECN_SEEN)
	MinRTTKernelms            float64       // minimum RTT as tracked by the kernel, in milliseconds
	MinRTTObservedms          float64       // minimum RTT in the observed samples
	MaxPacingRateKernelMbps   float64       // maximum pacing rate as tracked by the kernel, in Mbps
	MaxPacingRateObservedMbps float64       // maximum pacing rate in the observed samples
	RTTSevenNumSum            [7]float64    // RTT seven number summary
	CorrRTTCwnd               float64       // correlation between RTT and cwnd
	CorrRetransCwnd           float64       // correlation between retransmit rate and cwnd
	CorrPacingCwnd            float64       // correlation between pacing rate and cwnd
	TotalRetransmits          uint32        // the value of tcpi_total_retrans from the kernel on the last sample
	BytesAcked                uint64        // bytes acked
	// delivery stats only available in 4.18 and later
	//Delivered                 uint32        // packets delivered
	//DeliveredCE               uint32        // packets delivered and acked with ECE
	SendThroughputMbps float64 // mean send throughput in Mbps
}

type Config struct {
	SamplerInterval        time.Duration     // sampler interval (for quantile and correlation weights)
	CumulantKind           stat.CumulantKind // cumulant for quantile calculations
	UnweightedCorrelations bool              // if true, correlations are unweighted
	UnweightedQuantiles    bool              // if true, quantiles are unweighted
	AdjustedCC1            bool              // if true, use adjusted correlation r_adj = r * (1 + (1-r^2)/2n)
	AdjustedCC2            bool              // if true, use adjusted correlation r_adj = sqrt(1 - ((1-r^2)*(n-1)) / (n-2))
	Log                    bool              // if true, logging is enabled
}

type Analyzer struct {
	Config
	metrics *metrics.Metrics
}

func NewAnalyzer(cfg Config, m *metrics.Metrics) *Analyzer {
	return &Analyzer{cfg, m}
}

func (a *Analyzer) Analyze(fs []*tracker.Flow) (s []*FlowStats) {
	if len(fs) == 0 {
		return
	}

	t0 := time.Now()

	s = make([]*FlowStats, len(fs))
	fa := &flow{Config: &a.Config}

	for i := 0; i < len(fs); i++ {
		fa.Flow = fs[i]
		s[i] = fa.analyze()
	}

	el := time.Since(t0)
	a.metrics.PushAnalyzer(el)

	if a.Log {
		log.Printf("analyzer time=%s flows=%d", el, len(fs))
	}

	return
}

type flow struct {
	*Config
	*tracker.Flow
}

func (f *flow) analyze() (s *FlowStats) {
	s = &FlowStats{}
	s.ID = f.convertID()
	s.StartTime = f.StartTime
	s.EndTime = f.EndTime
	s.Duration = f.duration()
	s.Samples = len(f.Data)
	s.SamplesDeduped = f.SamplesDeduped
	s.Partial = f.Partial
	s.Timestamps = f.optSeen(linux.TCPI_OPT_TIMESTAMPS)
	s.SACK = f.optSeen(linux.TCPI_OPT_SACK)
	s.ECN = f.optSeen(linux.TCPI_OPT_ECN)
	s.ECNSeen = f.optSeen(linux.TCPI_OPT_ECN_SEEN)
	s.MinRTTKernelms = usToMs(f.minRTTKernel())
	s.MinRTTObservedms = usToMs(f.minRTTObserved())
	//s.MaxPacingRateKernelMbps = bytesPSToMbps(f.maxPacingRateKernel())
	s.MaxPacingRateObservedMbps = bytesPSToMbps(f.maxPacingRateObserved())
	rtts := f.rtts()
	s.RTTSevenNumSum = f.sevenNumSum(rtts)
	cwnds := f.cwnds()
	if s.Samples > 1 {
		var w []float64
		if !f.UnweightedCorrelations {
			w = f.sampleWeights()
		} else {
			w = nil
		}

		if debug {
			log.Printf("correlate rtts %v to cwnds %v", rtts, cwnds)
		}
		s.CorrRTTCwnd = stat.Correlation(rtts, cwnds, w)
		if isUndefined(s.CorrRTTCwnd) {
			s.CorrRTTCwnd = CORR_UNDEFINED
		} else {
			s.CorrRTTCwnd = f.adjustCorrelation(s.CorrRTTCwnd)
		}

		rtps := f.retransPerSec()
		if debug {
			log.Printf("correlate retransmits %v to cwnds %v", rtps, cwnds)
		}
		s.CorrRetransCwnd = stat.Correlation(rtps, cwnds, w)
		if isUndefined(s.CorrRetransCwnd) {
			s.CorrRetransCwnd = CORR_UNDEFINED
		} else {
			s.CorrRetransCwnd = f.adjustCorrelation(s.CorrRetransCwnd)
		}

		pcng := f.pacing()
		if debug {
			log.Printf("correlate pacing %v to cwnds %v", pcng, cwnds)
		}
		s.CorrPacingCwnd = stat.Correlation(pcng, cwnds, w)
		if isUndefined(s.CorrPacingCwnd) {
			s.CorrPacingCwnd = CORR_UNDEFINED
		} else {
			s.CorrPacingCwnd = f.adjustCorrelation(s.CorrPacingCwnd)
		}
	} else {
		s.CorrRTTCwnd = CORR_INSUFFICIENT_SAMPLES
		s.CorrRetransCwnd = CORR_INSUFFICIENT_SAMPLES
		s.CorrPacingCwnd = CORR_INSUFFICIENT_SAMPLES
	}
	s.TotalRetransmits = f.lastData().TotalRetransmits
	s.BytesAcked = f.lastData().BytesAcked
	//s.Delivered = f.lastData().Delivered
	//s.DeliveredCE = f.lastData().DeliveredCE
	s.SendThroughputMbps = bytesPSToMbps(1000000000 * s.BytesAcked /
		uint64(s.EndTime.Sub(s.StartTime)))
	return
}

func (f *flow) convertID() (id ID) {
	id.SrcIP = net.IP(f.ID.SrcIP[:])
	id.SrcPort = f.ID.SrcPort
	id.DstIP = net.IP(f.ID.DstIP[:])
	id.DstPort = f.ID.DstPort
	id.TstampStartNs = f.firstData().TstampNs
	return
}

func (f *flow) duration() time.Duration {
	return time.Duration(f.EndTstampNs - f.firstData().TstampNs)
}

func (f *flow) firstData() *sampler.Data {
	return &f.Data[0]
}

func (f *flow) lastData() *sampler.Data {
	return &f.Data[len(f.Data)-1]
}

func (f *flow) optSeen(opt uint8) (b bool) {
	for _, d := range f.Data {
		if d.Options&opt != 0 {
			b = true
			break
		}
	}
	return
}

func (f *flow) minRTTKernel() (min uint32) {
	min = f.lastData().MinRTTus
	return
}

func (f *flow) minRTTObserved() (min uint32) {
	min = f.Data[0].RTTus
	for i := 1; i < len(f.Data); i++ {
		if f.Data[i].RTTus < min {
			min = f.Data[i].RTTus
		}
	}
	return
}

/*
func (f *flow) maxPacingRateKernel() (max uint64) {
	max = f.lastData().MaxPacingRateBps
	return
}
*/

func (f *flow) maxPacingRateObserved() (max uint64) {
	max = f.Data[0].PacingRateBps
	for i := 1; i < len(f.Data); i++ {
		if f.Data[i].PacingRateBps > max {
			max = f.Data[i].PacingRateBps
		}
	}
	return
}

func (f *flow) rtts() (r []float64) {
	r = make([]float64, len(f.Data))
	for i := 0; i < len(f.Data); i++ {
		r[i] = usToMs(f.Data[i].RTTus)
	}
	return
}

func (f *flow) cwnds() (w []float64) {
	w = make([]float64, len(f.Data))
	for i := 0; i < len(f.Data); i++ {
		w[i] = float64(f.Data[i].SndCwndBytes)
	}
	return
}

func (f *flow) retransPerSec() (r []float64) {
	r = make([]float64, len(f.Data))
	for i := 1; i < len(f.Data); i++ {
		deltaSec := float64(f.Data[i].TstampNs-f.Data[i-1].TstampNs) / 1000000000
		retrans := f.Data[i].TotalRetransmits - f.Data[i-1].TotalRetransmits
		r[i] = float64(retrans) / deltaSec
	}
	return
}

func (f *flow) pacing() (p []float64) {
	p = make([]float64, len(f.Data))
	for i := 0; i < len(f.Data); i++ {
		p[i] = float64(f.Data[i].PacingRateBps)
	}
	return
}

func (f *flow) sevenNumSum(d []float64) (s [7]float64) {
	var w []float64
	if f.UnweightedQuantiles {
		sort.Float64s(d)
	} else {
		w = f.sampleWeights()
		t := transformFromSlices(d, w)
		sort.Sort(t)
		t.transformToSlices(d, w)
	}
	if debug {
		log.Printf("sevenNumSum d=%v, w=%v, dlen=%d, wlen=%d", d, w, len(d), len(w))
	}
	for i := 0; i < 7; i++ {
		s[i] = stat.Quantile(snsPcts[i], f.CumulantKind, d, w)
	}
	return
}

func (f *flow) sampleWeights() (w []float64) {
	w = make([]float64, len(f.Data))
	for i := 1; i < len(f.Data); i++ {
		w[i] = float64(f.Data[i].TstampNs-f.Data[i-1].TstampNs) / float64(f.SamplerInterval)
	}
	if len(w) > 1 {
		// 0th weight is median of following weights
		w0 := make([]float64, len(w)-1)
		copy(w0, w[1:])
		sort.Float64s(w0)
		w[0] = stat.Quantile(0.5, stat.LinInterp, w0, nil)
	}
	return
}

func (f *flow) adjustCorrelation(r float64) (radj float64) {
	if f.AdjustedCC1 {
		n := float64(len(f.Data))
		radj = r * (1 + (1-r*r)/2*n)
	} else if f.AdjustedCC2 {
		n := float64(len(f.Data))
		if n > 2 {
			radj = math.Sqrt(1 - ((1-r*r)*(n-1))/(n-2))
		}
	} else {
		radj = r
	}
	return
}

func usToMs(us uint32) float64 {
	return float64(us) / 1000
}

func usSliceToMs(us []uint32) (ms []float64) {
	ms = make([]float64, len(us))
	for i := 0; i < len(us); i++ {
		ms[i] = usToMs(us[i])
	}
	return
}

func bytesPSToMbps(byps uint64) float64 {
	return float64(byps) * 8 / 1000000
}

type tuple struct {
	a float64
	b float64
}

type tuples []tuple

func transformFromSlices(a, b []float64) (t tuples) {
	t = tuples(make([]tuple, len(a)))
	for i := 0; i < len(a); i++ {
		t[i] = tuple{a[i], b[i]}
	}
	return
}

func (t tuples) transformToSlices(a, b []float64) {
	for i := 0; i < len(t); i++ {
		a[i], b[i] = t[i].a, t[i].b
	}
	return
}

func (t tuples) Len() int {
	return len(t)
}

func (t tuples) Less(i, j int) bool {
	return t[i].a < t[j].a
}

func (t tuples) Swap(i, j int) {
	t[i], t[j] = t[j], t[i]
}

func isUndefined(f float64) bool {
	return math.IsNaN(f) || math.IsInf(f, 0)
}
