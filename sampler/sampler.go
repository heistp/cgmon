package sampler

// A Config contains the sampler configuration.
type Config struct {
	Log bool
}

// An ID uniquely identifies samples within a given sampler run.
type ID struct {
	SrcIP   [4]byte // source (local) IP address
	SrcPort uint16  // source (local) port
	DstIP   [4]byte // dest (remote) IP address
	DstPort uint16  // dest (remote) port
}

// A Data contains the sampled values for a flow.
type Data struct {
	TstampNs         uint64 // monotonic nsec receive timestamp
	Options          uint8  // TCP options (TCPI_OPT_* in linux/tcp.h)
	RTTus            uint32 // TCP RTT in microseconds
	MinRTTus         uint32 // min TCP RTT in microseconds
	SndCwndBytes     uint32 // TCP cwnd in bytes
	PacingRateBps    uint64 // TCP pacing rate in bytes / second
	TotalRetransmits uint32 // total retransmit counter
	// delivery stats only available in 4.18 and later
	//Delivered        uint32 // total delivered packets
	//DeliveredCE      uint32 // total delivered packets acked with ECE
	BytesAcked uint64 // bytes acked
}

// EquivalentTo returns true if all fields excluding the timestamp are the same
// as the given data.
func (d *Data) EquivalentTo(d1 *Data) bool {
	return d.RTTus == d1.RTTus &&
		d.BytesAcked == d1.BytesAcked &&
		d.PacingRateBps == d1.PacingRateBps &&
		d.TotalRetransmits == d1.TotalRetransmits &&
		d.SndCwndBytes == d1.SndCwndBytes &&
		d.MinRTTus == d1.MinRTTus
	//d.MaxPacingRateBps == d1.MaxPacingRateBps
}

// A Sample contains a sample ID and its data.
type Sample struct {
	ID
	Data
}

// Sampler is the interface that wraps the Sample method.
//
// Samples returns a nil result if it has no more samples to return.
// The separate Result interface allows the conversion to a flow.Sample
// slice to occur concurrently.
type Sampler interface {
	Sample() (Result, error)
}

// Result is the interface that wraps the Samples method.
type Result interface {
	Samples() []Sample
}

// ResultRecycler is the interface that wraps the RecycleResult method. It should
// be implemented by samplers that can reuse previously allocated Results.
type ResultRecycler interface {
	RecycleResult(Result)
}

// SamplesRecycler is the interface that wraps the RecycleSamples method. It should
// be implemented by samplers that can reuse previously allocated Samples.
type SamplesRecycler interface {
	RecycleSamples([]Sample)
}

// Closer is the interface that wraps the Close method.
type Closer interface {
	Close() error
}
