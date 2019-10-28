#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdbool.h>
#include <unistd.h>
#include <time.h>
#include <errno.h>
#include <sys/types.h>
#include <sys/socket.h>
#include <sys/utsname.h>
#include <linux/netlink.h>
#include <linux/rtnetlink.h>
#include <netinet/in.h>
#include <linux/tcp.h>
#include <linux/sock_diag.h>
#include <linux/inet_diag.h>
#include <arpa/inet.h>
#include "nl_diag.h"
#include "nl_filter.h"

// kernel tcp states (net/tcp_states.h)
enum {
	TCP_ESTABLISHED = 1,
	TCP_SYN_SENT,
	TCP_SYN_RECV,
	TCP_FIN_WAIT1,
	TCP_FIN_WAIT2,
	TCP_TIME_WAIT,
	TCP_CLOSE,
	TCP_CLOSE_WAIT,
	TCP_LAST_ACK,
	TCP_LISTEN,
	TCP_CLOSING,
	TCP_NEW_SYN_RECV,
	TCP_MAX_STATES
};

// 12 states with the first state in position 1, so 13 bit mask.
#define TCP_ALL_STATES_MASK 0x1FFF

// how many samples to add with each array growth
#define GROW_SAMPLES_INCREMENT 4096

// eq_op_support is true if port equality filter op is supported (set in nl_init)
bool eq_op_support;

// nl_init initializes this netlink library.
int nl_init() {
	struct utsname un;
	unsigned maj, min, rel;

	if (uname(&un) == -1 ||
		sscanf(un.release, "%u.%u.%u", &maj, &min, &rel) != 3) {
		eq_op_support = false;
		return -2;
	}

	eq_op_support = (maj > 4 || (maj == 4 && min >= 16));

	return 0;
}

// ms_timeval converts an int milliseconds to a timeval struct.
void ms_timeval(int ms, struct timeval *tv) {
	tv->tv_sec = ms / 1000;
	tv->tv_usec = (ms % 1000) * 1000;
}

// setsockopts sets socket options on the netlink socket.
int setsockopts(int fd, struct nl_config *cfg) {
	struct timeval tv;
	ms_timeval(cfg->rcv_timeout_ms, &tv);

	if (setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, (const char*)&tv, sizeof tv) == -1)
		return -1;

	if (cfg->rcv_bufsize > 0 &&
			setsockopt(fd, SOL_SOCKET, SO_RCVBUF, &cfg->rcv_bufsize,
			sizeof(int)) == -1)
		return -1;

	if (cfg->rcv_bufsize_force > 0 &&
			setsockopt(fd, SOL_SOCKET, SO_RCVBUFFORCE,
			&cfg->rcv_bufsize_force, sizeof(int)) == -1)
		return -1;

	return 0;
}

// nl_open opens a netlink session.
int nl_open(struct nl_config *cfg, uint16_t *sports, int splen,
		uint16_t *dports, int dplen, struct nl_session **nls) {
	int fd;
	struct nl_session *s;
	socklen_t rbsz = sizeof(s->rcv_bufsize);

	s = calloc(1, sizeof(*s));
	if (!s)
		goto err_nls;

	if ((fd = socket(AF_NETLINK, SOCK_DGRAM, NETLINK_INET_DIAG)) == -1)
		goto err_sock;

	if (setsockopts(fd, cfg) == -1)
		goto err_sock;

	if (getsockopt(fd, SOL_SOCKET, SO_RCVBUF, &s->rcv_bufsize, &rbsz) == -1)
		goto err_sockopt;

	s->fd = fd;
	s->read_bufsize = cfg->read_bufsize;
	s->filter_len = nl_port_filter(sports, splen, dports, dplen, &s->filter);
	if (s->filter_len == -1)
		goto err_filter;

	*nls = s;

	return 0;

err_filter:
err_sockopt:
	close(s->fd);
err_sock:
	free(s);
err_nls:
	return -1;
}

// nl_close closes and deallocates a netlink session.
int nl_close(struct nl_session *nls) {
	int ret;

	ret = close(nls->fd);
	free(nls->filter);
	free(nls);

	return ret;
}

// send_inet_diag sends one inet_diag request and returns the result from sendmsg.
int send_inet_diag(struct nl_session *nls) {
	struct msghdr msg;
	struct nlmsghdr h;
	struct inet_diag_req_v2 conn_req;
	struct sockaddr_nl sa;
	struct iovec iov[4];
	struct rtattr rta;

	memset(&msg, 0, sizeof(msg));
	memset(&sa, 0, sizeof(sa));
	memset(&h, 0, sizeof(h));
	memset(&conn_req, 0, sizeof(conn_req));

	sa.nl_family = AF_NETLINK;
	conn_req.sdiag_family = AF_INET;
	conn_req.sdiag_protocol = IPPROTO_TCP;

	//conn_req.idiag_states = TCP_ALL_STATES_MASK & 
	//	~((1 << TCP_SYN_RECV) | (1 << TCP_TIME_WAIT) | (1 << TCP_CLOSE));
	conn_req.idiag_states = (1 << TCP_ESTABLISHED);

	// request tcp_info, further possibilities in inet_diag.h
	conn_req.idiag_ext |= (1 << (INET_DIAG_INFO - 1));

	h.nlmsg_len = NLMSG_LENGTH(sizeof(conn_req));
	h.nlmsg_flags = NLM_F_DUMP | NLM_F_REQUEST;
	// remove NLM_F_DUMP above and specify src and dst ports and addrs on
	// conn_req.id to request a single socket.
	// conn_req.id.idiag_dport=htons(443);

	h.nlmsg_type = SOCK_DIAG_BY_FAMILY;
	iov[0].iov_base = (void*) &h;
	iov[0].iov_len = sizeof(h);
	iov[1].iov_base = (void*) &conn_req;
	iov[1].iov_len = sizeof(conn_req);

	// maybe add the filter
	if (nls->filter) {
		memset(&rta, 0, sizeof(rta));
		rta.rta_type = INET_DIAG_REQ_BYTECODE;
		rta.rta_len = RTA_LENGTH(nls->filter_len);
		iov[2] = (struct iovec){&rta, sizeof(rta)};
		iov[3] = (struct iovec){nls->filter, nls->filter_len};
		h.nlmsg_len += rta.rta_len;
	}

	// prepare and send message
	msg.msg_name = (void*) &sa;
	msg.msg_namelen = sizeof(sa);
	msg.msg_iov = iov;
	msg.msg_iovlen = (!nls->filter ? 2 : 4);

	return sendmsg(nls->fd, &msg, 0);
}

// grow increases the size of the samples array.
struct nl_sample* grow(struct nl_sample **s, int *scap) {
	*scap += GROW_SAMPLES_INCREMENT;
	*s = realloc(*s, *scap * sizeof(**s));
	return *s;
}

// parse reads one message and append samples for each embedded tcp_info.
void parse(struct inet_diag_msg *msg, int rtalen, uint64_t tstamp_ns,
		struct nl_sample **samples, int *samples_cap, int *nsamples) {
	struct rtattr *attr;
	struct tcp_info *tcpi;
	struct nl_sample *s = *samples;
	int ns = *nsamples;

	attr = (struct rtattr*) (msg+1);

	while (RTA_OK(attr, rtalen)) {
		if(attr->rta_type == INET_DIAG_INFO){
			tcpi = (struct tcp_info*) RTA_DATA(attr);

			if (ns + 1 > *samples_cap)
				s = grow(samples, samples_cap);

			s[ns] = (struct nl_sample) {
				tstamp_ns,
				{0},
				ntohs(msg->id.idiag_sport),
				{0},
				ntohs(msg->id.idiag_dport),
				tcpi->tcpi_options,
				tcpi->tcpi_rtt,
				tcpi->tcpi_min_rtt,
				tcpi->tcpi_snd_cwnd * tcpi->tcpi_snd_mss,
				tcpi->tcpi_pacing_rate,
				//tcpi->tcpi_max_pacing_rate,
				tcpi->tcpi_total_retrans,
				tcpi->tcpi_delivered,
				tcpi->tcpi_delivered_ce,
				tcpi->tcpi_bytes_acked,
			};
			// len for IPv6: msg->idiag_family == AF_INET ? 4 : 16
			memcpy(s[ns].saddr, msg->id.idiag_src, 4);
			memcpy(s[ns].daddr, msg->id.idiag_dst, 4);

			ns++;
		}
		attr = RTA_NEXT(attr, rtalen); 
	}

	*samples = s;
	*nsamples = ns;
}

// tstamp_nanos returns the time in nanoseconds from the monotonic clock.
inline uint64_t tstamp_nanos() {
	struct timespec ts;

	// no error checking- if this call fails we've got other problems
	clock_gettime(CLOCK_MONOTONIC, &ts);

	return ((uint64_t)ts.tv_sec * 1000000000) + ts.tv_nsec;
}

// nl_sample sends an inet_diag request and writes the results into the
// samples array, growing it as necessary.
int nl_sample(struct nl_session *nls, struct nl_sample **samples,
		int *samples_cap, struct nl_sample_stats *stats) {
	int n, rtalen;
	int msgs = 0, msgslen = 0, nsamples = 0;
	struct nlmsghdr *h;
	uint8_t read_buf[nls->read_bufsize];
	struct inet_diag_msg *msg;
	struct nlmsgerr *err;
	uint64_t ts;

	// send request
	if (send_inet_diag(nls) < 0)
		return -1;

	// read until message with NLMSG_DONE is received
	while (1) {
		if ((n = recv(nls->fd, read_buf, sizeof(read_buf), 0)) == -1)
			return -1;

		ts = tstamp_nanos();
		msgs++;
		msgslen += n;

		h = (struct nlmsghdr*) read_buf;
		while (NLMSG_OK(h, n)) {
			if(h->nlmsg_type == NLMSG_DONE) {
				stats->samples = nsamples;
				stats->msgs = msgs;
				stats->msgslen = msgslen;
				return 0;
			}

			if(h->nlmsg_type == NLMSG_ERROR) {
				err = (struct nlmsgerr*)NLMSG_DATA(h);
				if (h->nlmsg_len < NLMSG_LENGTH(sizeof(struct nlmsgerr)))
					errno = ENODATA;
				else
					errno = -err->error;
				return -1;
			}

			msg = (struct inet_diag_msg*) NLMSG_DATA(h);
			rtalen = h->nlmsg_len - NLMSG_LENGTH(sizeof(*msg));
			if (rtalen > 0)
				parse(msg, rtalen, ts, samples, samples_cap, &nsamples);

			h = NLMSG_NEXT(h, n); 
		}
	}
}
