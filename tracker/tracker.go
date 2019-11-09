package tracker

import (
	"log"
	"sync"
	"time"

	"github.com/heistp/cgmon/metrics"
	"github.com/heistp/cgmon/sampler"
)

// A Config contains the tracker configuration.
type Config struct {
	MaxFlows   int  // maximum number of active (non-filtered) flows allowed at a time
	MinSamples int  // minimum number of samples required to return ended flows for further processing
	Log        bool // if true, logging is enabled
}

// A Flow contains the data needed by the tracker for one flow.
type Flow struct {
	ID             sampler.ID     // flow ID
	Data           []sampler.Data // flow data
	StartTime      time.Time      // start time (use only when wall time needed, otherwise use monotonic timestamps in Data)
	EndTime        time.Time      // end time (use only when wall time needed, otherwise use monotonic timestamps in Data)
	Filtered       bool           // true if flow will be tracked but data not recorded/returned
	Sampled        bool           // true if flow was sampled during current track operation
	PreExisting    bool           // true if flow already existed on startup
	Partial        bool           // true if flow was pre-existing or no final sample was seen
	SamplesDeduped int            // number of samples de-duped
	EndTstampNs    uint64         // monotonic nsec time of last sample, even if it was de-duped
}

type Metrics struct {
	StartTime        time.Time
	TrackTimes       metrics.DurationStats
	TrackedFlows     int
	PriorEndedFlows  uint64
	PriorTrackerTime time.Time
	EndedFlows       uint64
	InstChurnRate    float64
	sync.RWMutex
}

func (m *Metrics) record(now time.Time, elapsed time.Duration,
	tracked, ended int) {
	m.Lock()
	defer m.Unlock()

	if m.StartTime.IsZero() {
		m.StartTime = time.Now()
	}

	m.TrackTimes.Push(elapsed)
	m.TrackedFlows = tracked
	m.EndedFlows += uint64(ended)
	m.InstChurnRate = (float64(m.EndedFlows) - float64(m.PriorEndedFlows)) /
		float64(now.Sub(m.PriorTrackerTime).Seconds())
	m.PriorEndedFlows = m.EndedFlows
	m.PriorTrackerTime = now
}

func (m *Metrics) ChurnRate() float64 {
	return float64(m.EndedFlows) / float64(time.Since(m.StartTime).Seconds())
}

type Tracker struct {
	Config
	metrics    Metrics
	flows      map[sampler.ID]*Flow
	firstTrack bool
}

func NewTracker(cfg Config) (t *Tracker) {
	t = &Tracker{cfg,
		Metrics{},
		make(map[sampler.ID]*Flow),
		true,
	}
	return
}

// Track tracks flows by adding samples to non-filtered flows, deleting any
// ended flows (those with missing samples), and returning any ended flows
// that pass the tracker's configured constraints.
//
// As an optimization, when enforcing filtering rules, we could allow some new
// flows to start in the same track operation that other flows end, but for now we
// don't do so to avoid the added complexity.
func (t *Tracker) Track(ss []sampler.Sample) (ended []*Flow) {
	t0 := time.Now()
	ts := &trackStats{}

	t.update(ss, t0, ts)
	ended = t.cleanup(t0, ts)

	ts.Ended = len(ended)

	if t.firstTrack {
		t.firstTrack = false
	}

	el := time.Since(t0)
	t.metrics.record(t0, el, len(t.flows), ts.Ended)

	if t.Log {
		log.Printf("tracker time=%s new=%d filtered=%d updated=%d deduped=%d ended=%d deleted=%d",
			el, ts.New, ts.Filtered, ts.Updated, ts.Deduped, ts.Ended, ts.Deleted)
	}

	return
}

func (t *Tracker) Metrics() (m Metrics) {
	t.metrics.RLock()
	defer t.metrics.RUnlock()
	m = t.metrics
	return
}

// update adds new and updates existing flows.
func (t *Tracker) update(ss []sampler.Sample, now time.Time, ts *trackStats) {
	for _, s := range ss {
		var f *Flow
		var ok bool
		if f, ok = t.flows[s.ID]; !ok { // new flow
			filtered := t.MaxFlows > 0 && len(t.flows)+1 > t.MaxFlows
			var data []sampler.Data
			if !filtered {
				data = make([]sampler.Data, 0, 16)
				data = append(data, s.Data)
			}
			f = &Flow{s.ID,
				data,
				now,
				time.Time{},
				filtered,
				true,
				t.firstTrack,
				true,
				0,
				s.Data.TstampNs,
			}
			t.flows[s.ID] = f
			if filtered {
				ts.Filtered++
			} else {
				ts.New++
			}
		} else { // existing flow
			f.Sampled = true
			if !f.Filtered {
				f.EndTstampNs = s.Data.TstampNs
				if f.Data[len(f.Data)-1].EquivalentTo(&s.Data) {
					// de-duplicate existing flow
					f.SamplesDeduped++
					ts.Deduped++
					continue
				}
				f.Data = append(f.Data, s.Data)
				ts.Updated++
			}
		}
	}
}

// cleanup cleans up after tracked flows that were not sampled. Filtered flows are
// deleted but not returned as ended.
func (t *Tracker) cleanup(now time.Time, ts *trackStats) (ended []*Flow) {
	var deleted []sampler.ID

	for _, v := range t.flows {
		if !v.Sampled {
			v.Partial = v.PreExisting
			v.EndTime = now
			if !v.Filtered {
				if t.MinSamples > 0 && len(v.Data) < t.MinSamples {
					v.Filtered = true
				} else {
					ended = append(ended, v)
				}
			}
			deleted = append(deleted, v.ID) // delete must occur outside range loop
		} else {
			v.Sampled = false // prepare for next track
		}
	}

	for _, id := range deleted {
		delete(t.flows, id)
	}
	ts.Deleted = len(deleted)

	return
}

type trackStats struct {
	New      int
	Filtered int
	Updated  int
	Deduped  int
	Ended    int
	Deleted  int
}
