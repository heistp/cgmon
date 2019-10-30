#ifndef _NL_DIAG_H_
#define _NL_DIAG_H_

#include <stdint.h>
#include <netinet/in.h>

struct nl_config {
	int read_bufsize;
	int rcv_bufsize;
	int rcv_bufsize_force;
	int rcv_timeout_ms;
};

struct nl_session {
	int fd;
	int read_bufsize;
	int rcv_bufsize;
	struct inet_diag_bc_op *filter;
	int filter_len;
};

struct nl_sample {
	uint64_t tstamp_ns;           // monotonic nanosecond timestamp on sample receipt
	uint8_t saddr[4];             // source (local) IP address
	uint16_t sport;               // source (local) port
	uint8_t daddr[4];             // dest (remote) IP address
	uint16_t dport;               // dest (remote) port
	uint8_t options;              // TCP options (TCPI_OPT_* in linux/tcp.h)
	uint32_t rtt_us;              // TCP round-trip time in usec
	uint32_t min_rtt_us;          // min TCP round-trip time in usec
	uint32_t snd_cwnd_bytes;      // TCP send cwnd in bytes
	uint64_t pacing_rate_Bps;     // TCP pacing rate in bytes/sec
	uint32_t total_retrans;       // TCP total retransmits
	// delivery stats only available in 4.18 and later
	//uint32_t delivered;           // TCP delivered packets
	//uint32_t delivered_ce;        // TCP CE on delivered packets (ECE received)
	uint64_t bytes_acked;         // TCP bytes acked
};

struct nl_sample_stats {
	int samples; // number of samples in call
	int msgs;    // number of netlink messages returned
	int msgslen; // total length of all netlink messages
};

int nl_init();

int nl_open(struct nl_config *cfg, uint16_t *sports, int splen,
		uint16_t *dports, int dplen, struct nl_session **nls);

int nl_sample(struct nl_session *nls, struct nl_sample **samples,
		int *samples_cap, struct nl_sample_stats *stats);

int nl_close(struct nl_session *nls);

#endif // _NL_DIAG_H_
