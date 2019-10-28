package netlink

/*
#cgo CFLAGS: -O2 -Wall

#include "nl_diag.h"
*/
import "C"
import (
	"log"
	"time"
	"unsafe"

	"github.com/heistp/cgmon/metrics"
	"github.com/heistp/cgmon/sampler"
)

// Result holds the results from a one netlink inet_diag call.
type Result struct {
	samples    *C.struct_nl_sample
	samplesCap C.int
	stats      C.struct_nl_sample_stats
	log        bool
	samplesCh  chan []sampler.Sample
	metrics    *metrics.Metrics
}

func (r *Result) Samples() (ss []sampler.Sample) {
	t0 := time.Now()

	cs := r.nlSamplesSlice()
	ss = r.samplesSlice(len(cs))
	for i, s := range cs {
		ss[i] = sampler.Sample{
			sampler.ID{
				byteArray4(s.saddr),
				uint16(s.sport),
				byteArray4(s.daddr),
				uint16(s.dport),
			},
			sampler.Data{
				uint64(s.tstamp_ns),
				uint8(s.options),
				uint32(s.rtt_us),
				uint32(s.min_rtt_us),
				uint32(s.snd_cwnd_bytes),
				uint64(s.pacing_rate_Bps),
				//uint64(s.max_pacing_rate_Bps),
				uint32(s.total_retrans),
				uint32(s.delivered),
				uint32(s.delivered_ce),
				uint64(s.bytes_acked),
			},
		}
	}

	el := time.Since(t0)
	r.metrics.PushConversion(el)

	if r.log {
		log.Printf("conversion time=%s samples=%d", el, len(ss))
	}

	return ss
}

func (r *Result) sampleStats() (s sampleStats) {
	s.samples = int(r.stats.samples)
	s.msgs = int(r.stats.msgs)
	s.msgsLen = int(r.stats.msgslen)
	return
}

func (r *Result) nlSamplesSlice() []C.struct_nl_sample {
	if r.samples == nil {
		return []C.struct_nl_sample{}
	}
	n := r.stats.samples
	return (*[1 << 30]C.struct_nl_sample)(unsafe.Pointer(r.samples))[:n:n]
}

func (r *Result) samplesSlice(l int) (ss []sampler.Sample) {
	// check for recycled result
	select {
	case ss = <-r.samplesCh:
	default:
	}

	if ss == nil || cap(ss) < l {
		c := l * 2
		if r.log {
			log.Printf("allocating new samples buffer len %d", c)
		}
		ss = make([]sampler.Sample, l, c)
	} else {
		ss = ss[:l]
	}

	return
}

func byteArray4(c [4]C.uchar) (b [4]byte) {
	b[0] = byte(c[0])
	b[1] = byte(c[1])
	b[2] = byte(c[2])
	b[3] = byte(c[3])
	return b
}

// sampleStats contains the stats for a netlink sample call.
type sampleStats struct {
	samples int
	msgs    int
	msgsLen int
}
