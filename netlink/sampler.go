package netlink

/*
#cgo CFLAGS: -O2 -Wall

#include <stdbool.h>
#include "nl_diag.h"

extern bool eq_op_support;
*/
import "C"

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/heistp/cgmon/metrics"
	"github.com/heistp/cgmon/sampler"
)

var initMutex = sync.Mutex{}

var netlinkInitialized = false

// Config contains the netlink client configuration.
type Config struct {
	ReadBufSize         int           // size of userspace read buffer (>32K no benefit in kernels 4.9-5.2, at least)
	ReceiveBufSize      int           // socket receive buffer size
	ReceiveBufSizeForce int           // force socket receive buffer size (requires CAP_NET_ADMIN or root)
	SrcPorts            []uint16      // source (local) ports for kernel to filter by
	DstPorts            []uint16      // dest (remote) ports for kernel to filter by
	ReceiveTimeout      time.Duration // socket receive timeout
	Log                 bool          // if true enable logging
}

type Metrics struct {
	SampleTimes  metrics.DurationStats
	ConvertTimes metrics.DurationStats
	sync.RWMutex
}

func (m *Metrics) recordSampleTime(d time.Duration) {
	m.Lock()
	defer m.Unlock()
	m.SampleTimes.Push(d)
}

func (m *Metrics) recordConvertTime(d time.Duration) {
	m.Lock()
	defer m.Unlock()
	m.ConvertTimes.Push(d)
}

type Sampler struct {
	Config
	metrics   Metrics
	session   *C.struct_nl_session
	resultsCh chan *Result
	samplesCh chan []sampler.Sample
	sync.Mutex
}

func NewSampler(cfg Config) *Sampler {
	initMutex.Lock()
	defer initMutex.Unlock()
	if !netlinkInitialized {
		nlInit(cfg.Log)
		netlinkInitialized = true
	}

	return &Sampler{cfg,
		Metrics{},
		nil,
		make(chan *Result, 32),
		make(chan []sampler.Sample, 32),
		sync.Mutex{},
	}
}

func (s *Sampler) Sample() (r sampler.Result, err error) {
	s.Lock()
	defer s.Unlock()

	t0 := time.Now()

	if err = s.nlOpen(); err != nil {
		return
	}

	var nr *Result
	if nr, err = s.nlSample(); err != nil {
		s.nlClose()
		return
	}

	el := time.Since(t0)
	s.metrics.recordSampleTime(el)

	if s.Log {
		ss := nr.sampleStats()
		log.Printf("netlink sample time=%s samples=%d msgs=%d msgslen=%d",
			el, ss.samples, ss.msgs, ss.msgsLen)
	}

	r = nr

	return
}

func (s *Sampler) RecycleResult(r sampler.Result) {
	select {
	case s.resultsCh <- r.(*Result):
	default:
	}
}

func (s *Sampler) RecycleSamples(ss []sampler.Sample) {
	select {
	case s.samplesCh <- ss:
	default:
	}
}

func (s *Sampler) Metrics() (m Metrics) {
	s.metrics.RLock()
	defer s.metrics.RUnlock()
	m = s.metrics
	return
}

func (s *Sampler) Close() error {
	s.Lock()
	defer s.Unlock()

	return s.nlClose()
}

func (s *Sampler) nlOpen() (err error) {
	if s.session == nil {
		sp, spl := ushortArray(s.SrcPorts)
		dp, dpl := ushortArray(s.DstPorts)
		nc := &C.struct_nl_config{
			read_bufsize:      C.int(s.ReadBufSize),
			rcv_bufsize:       C.int(s.ReceiveBufSize),
			rcv_bufsize_force: C.int(s.ReceiveBufSizeForce),
			rcv_timeout_ms:    C.int(int64(s.ReceiveTimeout) / 1e6),
		}

		if _, err = C.nl_open(nc, sp, spl, dp, dpl, &s.session); err != nil {
			return
		}
		if s.Log {
			log.Printf("opened netlink socket, SO_RCVBUF=%d", s.session.rcv_bufsize)
		}
	}
	return
}

func (s *Sampler) nlSample() (r *Result, err error) {
	// check for recycled result
	select {
	case r = <-s.resultsCh:
	default:
		if s.Log {
			log.Printf("allocating new netlink result buffer")
		}
		r = &Result{log: s.Log, samplesCh: s.samplesCh, metrics: &s.metrics}
	}

	_, err = C.nl_sample(s.session, &r.samples, &r.samplesCap, &r.stats)
	return
}

func (s *Sampler) nlClose() (err error) {
	if s.session != nil {
		_, err = C.nl_close(s.session)
		s.session = nil
	}
	return
}

func ushortArray(a []uint16) (p *C.ushort, l C.int) {
	if l = C.int(len(a)); l > 0 {
		p = (*C.ushort)(&a[0])
	}
	return
}

func nlInit(logEnabled bool) {
	var stat string
	if i, err := C.nl_init(); err != nil {
		if i == -2 {
			stat = fmt.Sprintf("unable to determine OS version (%s)", err)
		} else if logEnabled {
			log.Fatalf("error during netlink initialization (%s)", err)
		}
	} else {
		stat = "initialized"
	}

	if logEnabled {
		var s string
		if C.eq_op_support {
			s = "supported"
		} else {
			s = "not supported, using ge&le"
		}
		log.Printf("netlink %s, port equality kernel filter op %s", stat, s)
	}
}
