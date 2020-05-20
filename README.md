# cgmon

cgmon uses netlink with the inet_diag protocol to sample congestion related
statistics from the Linux kernel.

Feel free to report any problems or feature requests as issues.

1. [Features](#features)
2. [Installation](#installation)
3. [Quick Start](#quick-start)
4. [Sample Results and Discussion](#sample-results-and-discussion)
5. [Understanding the cgmon Pipeline](#understanding-the-cgmon-pipeline)
6. [Metrics](#metrics)
7. [Todo](#todo)

## Features

- records:
  - RTT (w/ minimum from kernel and observed)
  - send cwnd
  - retransmits
  - bytes acked
  - delivered (acked segments) and delivered_ce (acked with ECE)
  - pacing rate (w/ maximum observed)
  - TCP option flags including ECN, ECN seen, SACK and timestamp support
- calculates:
  - RTT [seven number summary](https://en.wikipedia.org/wiki/Seven-number_summary)
  - correlation coefficients (weighted using time between samples) for:
    - RTT to cwnd
    - retransmits to cwnd (needs work)
    - pacing rate to cwnd
- outputs JSON to stdout or files with support for:
  - file rotation by size, time interval or both
  - on-the-fly gzip compression
- technical:
  - netlink interaction in C for fast message processing
  - generates netlink inet_diag filter bytecodes for kernel space port filtering
  - five-stage pipeline for concurrent processing of samples and results
  - flow tracker with restrictions for max flow count and min flow samples
  - embedded HTTP server shows basic internal metrics
  - basic logging with syslog support

## Installation

Install instructions:

1. [Install Go](https://golang.org/dl/).
2. Download cgmon: `go get -u github.com/heistp/cgmon`
3. Build cgmon: `go build` (from `cgmon` directory, by default
   `~/go/src/github.com/heistp/cgmon`)
4. Put `cgmon` somewhere on your PATH (or specify an absolute path)
5. Run `cgmon -h` for usage

A few more (hopefully unnecessary) notes:

- The C code doesn't depend on any external libs, and the Go code only depends
  on [gonum](https://github.com/gonum) for statistics, which should be pulled
  down automatically by `go get`. `go build` compiles the C code automatically
  (obviously, a C compiler must be installed).
- `cgmon` was developed on Go version 1.13, but also tested on Go 1.12.  It may
  or may not build and work with earlier versions.  The C code doesn't depend on
  any external libs, and the Go code depends only on
  [gonum](https://github.com/gonum) for statistics, which should be pulled down
  automatically by `go get`.
- It is also possible instead of step 3 to do `go install
  github.com/heistp/cgmon`, which will place the binary in `~/go/bin` by
  default. The method I included just offers more control over where the binary goes.
- Because `cgmon` uses `cgo`, it is recommended to run the executable on the
  same version of Linux it was built on. However, there is some flexibility
  here. For example, it's possible to build on kernel 5.1 and deploy on kernel
  4.15, and vice-versa. I had to make a few small changes on the C side in order
  for this to be possible.

## Quick Start

Monitor flow data sent from a web server to clients (up to 100 flows):

```
$ mkdir output
$ cgmon -netlink-sport 80,443 -tracker-max-flows 100 -writer-dir output -writer-rotate-size 10M -run-interval 100ms
```

Note that `-tracker-max-flows` requires a programmatic restriction in user space.
One may instead use the more efficient kernel space port filtering and restrict
the number of flows using a range of ephemeral ports:

```
$ cgmon -netlink-sport 80,443 -netlink-dport 2000-3000,45000-55000 -writer-dir output -writer-rotate-size 10M -run-interval 100ms
```

*Note:* It may be useful to choose a range of ephemeral ports that minimizes
bias in the results for different OSs, although that may be difficult to do
(see [Ephemeral Port](https://en.wikipedia.org/wiki/Ephemeral_port)).

## Sample Results and Discussion

### local iperf3, client using WiFi, pfifo_fast qdisc

The following result is from a 10 second iperf3 flow in a LAN environment that
traverses a WiFi AP with a 100Mbit Ethernet adapter. ECN is enabled. The first
flow is the data flow and the second the control flow:

```
$ ./cgmon -netlink-dport 5201 -run-interval 20ms
{
	"ID": {
		"SrcIP": "192.168.0.36",
		"SrcPort": 37888,
		"DstIP": "192.168.0.251",
		"DstPort": 5201,
		"TstampStartNs": 458994210275544
	},
	"StartTime": "2019-10-28T16:53:07.354605093+01:00",
	"EndTime": "2019-10-28T16:53:17.393685284+01:00",
	"Duration": 10019850798,
	"Samples": 502,
	"SamplesDeduped": 0,
	"Partial": false,
	"Timestamps": true,
	"SACK": true,
	"ECN": true,
	"ECNSeen": false,
	"MinRTTKernelms": 0.939,
	"MinRTTObservedms": 1.103,
	"MaxPacingRateObservedMbps": 230.997504,
	"RTTSevenNumSum": [
		8.350397274939079,
		31.354482239224936,
		34.71255180531762,
		37.213925228004214,
		39.91222578638429,
		41.90972090013958,
		43.45714055969314
	],
	"CorrRTTCwnd": 0.8349523415594449,
	"CorrRetransCwnd": -0.007016178284874477,
	"CorrPacingCwnd": -0.243851449275133,
	"TotalRetransmits": 26,
	"BytesAcked": 117537094,
	"SendThroughputMbps": 93.663632
}
{
	"ID": {
		"SrcIP": "192.168.0.36",
		"SrcPort": 37886,
		"DstIP": "192.168.0.251",
		"DstPort": 5201,
		"TstampStartNs": 458994149636701
	},
	"StartTime": "2019-10-28T16:53:07.294015903+01:00",
	"EndTime": "2019-10-28T16:53:17.494960874+01:00",
	"Duration": 10181185635,
	"Samples": 4,
	"SamplesDeduped": 506,
	"Partial": false,
	"Timestamps": true,
	"SACK": true,
	"ECN": true,
	"ECNSeen": true,
	"MinRTTKernelms": 1.047,
	"MinRTTObservedms": 3.065,
	"MaxPacingRateObservedMbps": 75.585824,
	"RTTSevenNumSum": [
		7.143609008333342,
		7.481103697966614,
		8.252520131414094,
		9.457858308675783,
		10.663196485937473,
		11.434612919384953,
		11.772107609018226
	],
	"CorrRTTCwnd": -2,
	"CorrRetransCwnd": -2,
	"CorrPacingCwnd": -2,
	"BytesAcked": 148,
	"SendThroughputMbps": 0.000112
}
2019/10/28 16:53:21 successful termination
```

Note above that valid correlations are a floating point number from from -1 to
1, where -1 is a total negative linear correlation, 1 is a total positive linear
correlation and 0 is no linear correlation.

Here, we see a strong correlation between RTT and cwnd.

The special value -2 is used when cerrelations is undefined, which often happens
when at least one of the two measured variables has values that are all the
same, as happens here with the control connection. The special value -3 is used
when there is only one sample for the flow. At least two are required, but many
more are needed for useful correlations and seven number summaries.

Note that `MinRTTKernelms` is typically somewhat less than `MinRTTObservedms`,
because the kernel may see lower minimums between samples.

### local iperf3, client using WiFi, cake qdisc

The following result is similar to the previous one, only we use `cake bandwidth
90Mbit` as the root cake, to control the bottleneck. The control flow is
omitted here.

```
$ ./cgmon -run-interval 20ms -netlink-dport 5201
{
	"ID": {
		"SrcIP": "192.168.0.36",
		"SrcPort": 38618,
		"DstIP": "192.168.0.251",
		"DstPort": 5201,
		"TstampStartNs": 462392255023108
	},
	"StartTime": "2019-10-28T17:49:45.399383126+01:00",
	"EndTime": "2019-10-28T17:49:55.418942467+01:00",
	"Duration": 9999593456,
	"Samples": 501,
	"SamplesDeduped": 0,
	"Partial": false,
	"Timestamps": true,
	"SACK": true,
	"ECN": true,
	"ECNSeen": false,
	"MinRTTKernelms": 1.059,
	"MinRTTObservedms": 1.059,
	"MaxPacingRateKernelMbps": 0,
	"MaxPacingRateObservedMbps": 281.5236,
	"RTTSevenNumSum": [
		5.585468531346944,
		6.063150572751023,
		6.4114634267300135,
		6.797766347400113,
		7.216781398312581,
		7.739435280434424,
		9.630185886870049
	],
	"CorrRTTCwnd": 0.8214931181780514,
	"CorrRetransCwnd": -2,
	"CorrPacingCwnd": 0.7037850374556942,
	"BytesAcked": 192.1687774,
	"TotalRetransmits": 0,
	"SendThroughputMbps": 85.215536
}
```

In the results above we see that Cake has reduced the RTTs considerably. 

Counter-intuitively, the number of retransmits is 0 when Cake is used, but this
is because the bottleneck is local, so the congestion signal is handled locally
and doesn't lead to packet loss. Correspondingly, this is probably why `ECNSeen`
remains false.

### local iperf3, client using WiFi, cake qdisc, ECN disabled

This example is the same as the previous but with ECN disabled.

```
{
	"ID": {
		"SrcIP": "192.168.0.36",
		"SrcPort": 38622,
		"DstIP": "192.168.0.251",
		"DstPort": 5201,
		"TstampStartNs": 462758616240100
	},
	"StartTime": "2019-10-28T17:55:51.760564527+01:00",
	"EndTime": "2019-10-28T17:56:01.760384379+01:00",
	"Duration": 9979984038,
	"Samples": 500,
	"SamplesDeduped": 0,
	"Partial": false,
	"Timestamps": true,
	"SACK": true,
	"ECN": false,
	"ECNSeen": false,
	"MinRTTKernelms": 0.963,
	"MinRTTObservedms": 1.464,
	"MaxPacingRateKernelMbps": 0,
	"MaxPacingRateObservedMbps": 445.90992,
	"RTTSevenNumSum": [
		6.021326873127832,
		6.546019721356404,
		6.957456546946132,
		7.585005455499874,
		8.365116056164712,
		9.228,
		10.791006956580107
	],
	"CorrRTTCwnd": 0.8643065647751361,
	"CorrRetransCwnd": -2,
	"CorrPacingCwnd": 0.9211551415981412,
	"BytesAcked": 106609038,
	"TotalRetransmits": 0,
	"SendThroughputMbps": 85.28876
}
```

Above we can see slightly higher RTTs than when ECN was enabled, however, this
may just be due to ordinary run-to-run variations.

### fast.com upload, client using WiFi

The following result is obtained with a single flow upload to fast.com, with
WiFi to the LAN and point-to-point WiFi to the Internet. 

```
{
	"ID": {
		"SrcIP": "192.168.0.36",
		"SrcPort": 34220,
		"DstIP": "45.57.63.136",
		"DstPort": 443,
		"TstampStartNs": 459405166460634
	},
	"StartTime": "2019-10-28T16:59:58.31082909+01:00",
	"EndTime": "2019-10-28T17:00:09.630184339+01:00",
	"Duration": 11299659636,
	"Samples": 486,
	"SamplesDeduped": 80,
	"Partial": false,
	"Timestamps": true,
	"SACK": true,
	"ECN": false,
	"ECNSeen": false,
	"MinRTTKernelms": 114.357,
	"MinRTTObservedms": 115.981,
	"MaxPacingRateObservedMbps": 85.09632,
	"RTTSevenNumSum": [
		116.14561386751598,
		121.0346611324477,
		123.19184631978811,
		127.21317449586843,
		134.73615345282636,
		146.60332301044662,
		182.84273108395004
	],
	"CorrRTTCwnd": 0.10682659055034016,
	"CorrRetransCwnd": 0.1576458198506397,
	"CorrPacingCwnd": 0.9573668518954076,
	"BytesAcked": 39152316,
	"SendThroughputMbps": 27.671056
}
```
Above, unlike with the local iperf3 results, we see a strong positive
correlation between the pacing rate and cwnd. Observed RTTs indicate a
relatively distant server. Note that this result was obtained before the
`TotalRetransmits` field was added.

### speedtest.net upload, client using WiFi

The following result is one of the flows obtained with a six flow upload to
speedtest.net, with the same WiFi to the LAN and point-to-point WiFi to the
Internet as above.

```
{
	"ID": {
		"SrcIP": "192.168.0.36",
		"SrcPort": 60442,
		"DstIP": "78.110.213.94",
		"DstPort": 8080,
		"TstampStartNs": 467355155129500
	},
	"StartTime": "2019-10-28T19:12:28.299452444+01:00",
	"EndTime": "2019-10-28T19:12:42.639426383+01:00",
	"Duration": 14320360367,
	"Samples": 690,
	"SamplesDeduped": 27,
	"Partial": false,
	"Timestamps": true,
	"SACK": true,
	"ECN": false,
	"ECNSeen": false,
	"MinRTTKernelms": 28.224,
	"MinRTTObservedms": 35.291,
	"MaxPacingRateKernelMbps": 0,
	"MaxPacingRateObservedMbps": 31.010352,
	"RTTSevenNumSum": [
		47.9202720084492,
		55.14487204449798,
		68.55835629507412,
		94.40987831474314,
		118.76957427564423,
		144.8933139700737,
		195.9231720619709
	],
	"CorrRTTCwnd": 0.7581565492594917,
	"CorrRetransCwnd": -0.08431873245068176,
	"CorrPacingCwnd": 0.4714998438274179,
	"TotalRetransmits": 19,
	"BytesAcked": 17260163,
	"SendThroughputMbps": 9.629112
}
```

### dslreports.com/speedtest upload, client using WiFi

The following result is one of the flows obtained with a six flow upload to
dslreports.com/speedtest, with the same WiFi to the LAN and point-to-point WiFi
to the Internet as above.

```
{
	"ID": {
		"SrcIP": "192.168.0.36",
		"SrcPort": 51258,
		"DstIP": "83.150.0.50",
		"DstPort": 80,
		"TstampStartNs": 468333777348377
	},
	"StartTime": "2019-10-28T19:28:46.921685451+01:00",
	"EndTime": "2019-10-28T19:29:06.581693373+01:00",
	"Duration": 19640424062,
	"Samples": 916,
	"SamplesDeduped": 67,
	"Partial": false,
	"Timestamps": true,
	"SACK": true,
	"ECN": false,
	"ECNSeen": false,
	"MinRTTKernelms": 28.788,
	"MinRTTObservedms": 28.788,
	"MaxPacingRateKernelMbps": 0,
	"MaxPacingRateObservedMbps": 57.377824,
	"RTTSevenNumSum": [
		45.25141038453186,
		58.673677737588854,
		74.56558053646026,
		100.82250884594792,
		165.31075404665071,
		231.60734800305067,
		280.30150874494154
	],
	"CorrRTTCwnd": -0.4962751151147407,
	"CorrRetransCwnd": -0.003392663114451414,
	"CorrPacingCwnd": 0.2224423911899571,
	"TotalRetransmits": 159,
	"BytesAcked": 27675921,
	"SendThroughputMbps": 11.261808
}
```

A higher number of retransmits can be observed in this flow, and there is
interestingly a near 0 correlation between retransmits and cwnd, indicating that
there either is no linear relationship here, or the calculation of this
correlation is incorrect. Further research is required.

### Upload to first hop ISP backhaul router, client using WiFi

The following result is one of the flows obtained with a three flow upload to
an ISP backhaul router, with the CPE router running htb+fq_codel at 30Mbit and
the client with ECN enabled, giving us a bottleneck with ECN marking.

```
{
	"ID": {
		"SrcIP": "192.168.0.36",
		"SrcPort": 60844,
		"DstIP": "10.101.31.1",
		"DstPort": 443,
		"TstampStartNs": 470853501245831
	},
	"StartTime": "2019-10-28T20:10:46.645572345+01:00",
	"EndTime": "2019-10-28T20:10:57.205495469+01:00",
	"Duration": 10540082127,
	"Samples": 517,
	"SamplesDeduped": 11,
	"Partial": false,
	"Timestamps": true,
	"SACK": true,
	"ECN": true,
	"ECNSeen": true,
	"MinRTTKernelms": 7.644,
	"MinRTTObservedms": 8.205,
	"MaxPacingRateKernelMbps": 0,
	"MaxPacingRateObservedMbps": 28.23644,
	"RTTSevenNumSum": [
		16.759601619937417,
		19.272779582032157,
		21.475669203845477,
		23.921183497302305,
		27.042406773840277,
		31.366437269199125,
		38.26928438746498
	],
	"CorrRTTCwnd": -0.24309002650910047,
	"CorrRetransCwnd": -2,
	"CorrPacingCwnd": 0.26922752220444324,
	"TotalRetransmits": 0,
	"BytesAcked": 11524327,
	"Delivered": 7962,
	"DeliveredCE": 597,
	"ThroughputMbps": 8.730608
}
```

Above we can see that ECN has kept the number of retransmits to 0. Note that new
counters for `Delivered` and `DeliveredCE` (segments that are acked with the ECE flag,
indicating CE was marked somewhere on the upstream) have been added here.

## Understanding the cgmon Pipeline

Results are obtained and processed using a five-stage pipeline where each stage
may be executed concurrently. The unit of work for the pipeline is one sample
from netlink containing sample data for multiple flows. It helps to know what
each named stage does in order to understand the command line flags and other
terminology:

*Sampler (Netlink)* → *Converter* → *Tracker* → *Analyzer* → *Writer*

- *Sampler (Netlink):* Uses AF_NETLINK sockets with the INET_DIAG protocol to make
  inet_diag requests to get the statistics from kernel space. This is done in C,
  and is the same way the `ss` utility accesses its statistics. The rest of the
  stages use Go.
- *Converter:* Copies the sample data containing C structs to Go structs. This
  is the main area of overhead for Go's interaction with netlink, and in practice
  is typically less than 1% of the total pipeline processing time.
- *Tracker:* Keeps track of flows, which are identified by their 5-tuple. Flows
  are considered ended when the first sample from Netlink contains no data for
  that flow. Sample data is de-duplicated here, so that if nothing but the
  timestamp has changed since the previous sample, the sample data for that flow
  is not retained. This stage is where the programmatic maximum number of flows
  is enforced, as well as a minimum number of samples to allow flows to pass to
  the *Analyzer* stage.
- *Analyzer:* Performs statistical analysis on ended flows.
- *Writer:* Encodes the results of analysis to JSON and writes it to stdout or a
  file. Output may be compressed, and output files may be rotated either by size
  or on a time interval.

## Metrics

`cgmon` keeps track of a few internal performance metrics. These may be accessed
in one of two ways:

1. Use the `-run-http-server` command line flag to specify a listen address for
   the embedded, single page http server (e.g. `-run-http-server :8080`).
2. Send the `cgmon` process a `SIGUSR1` signal, which will dump the metrics to
   the logger. Note: sending `SIGUSR2` additionally runs the garbage collector
   for debugging purposes, although this is ordinarily not needed as it's run
   automatically.

## Todo

- Refine statistics
- IPv6 support
- Output post-sampler results in an intermediate binary format
- Aggregation of results across different timescales
- Refactor and improve metrics
- Use audit subsystem to detect socket creation and poll immediately?
- Improve performance or reduce garbage, if needed
- Add spearman's rank correlation coefficient
- Stop converting snd_cwnd to bytes
- Discard first data points instead of using medians
- Re-add delivered and delivered_ce but allow compiling on 4.15
