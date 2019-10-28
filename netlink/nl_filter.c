#include <stdlib.h>
#include <stdbool.h>
#include <linux/inet_diag.h>
#include <linux/version.h>
#include "nl_filter.h"

// these ops are not defined prior to 4.16
#ifndef INET_DIAG_BC_S_EQ
#define INET_DIAG_BC_S_EQ INET_DIAG_BC_MARK_COND + 1
#endif

#ifndef INET_DIAG_BC_D_EQ
#define INET_DIAG_BC_D_EQ INET_DIAG_BC_S_EQ + 1
#endif

extern bool eq_op_support;

// pfops_count calculates the number of inet_diag filter ops needed to
// filter the specified ports.
int pfops_count(uint16_t ports[], int len) {
	int i;
	int l = 0;

	if (len == 0)
		return 0;

	// 2 ops for equality, 4 for range, plus jmp for logical or
	for (i = 0; i < len; i += 2) {
		if (eq_op_support && ports[i] == ports[i+1])
			l += 3;
		else
			l += 5;
	}
	l--; // last op has no jmp

	return l;
}

// pfops writes an OR'd filter for the specified ports.
// rops is the remaining number of ops, used if all conditions are false.
void pfops(uint16_t ports[], int len, bool dest, int rops,
		struct inet_diag_bc_op **oop) {
	const int opsz = sizeof(struct inet_diag_bc_op);
	struct inet_diag_bc_op *op = *oop;
	struct inet_diag_bc_op *opend = op + pfops_count(ports, len);
	bool last;
	int i;

	for (i = 0; i < len; i += 2) {
		last = (i == len - 2);

		if (eq_op_support && ports[i] == ports[i+1]) {
			op->code = dest ? INET_DIAG_BC_D_EQ : INET_DIAG_BC_S_EQ;
			op->yes = 2 * opsz;
			op->no = ((last ? rops : 0) + 3) * opsz;
			op++;
			op->no = ports[i];
			op++;
		} else {
			op->code = dest ? INET_DIAG_BC_D_GE : INET_DIAG_BC_S_GE;
			op->yes = 2 * opsz;
			op->no = ((last ? rops : 0) + 5) * opsz;
			op++;
			op->no = ports[i];
			op++;
			op->code = dest ? INET_DIAG_BC_D_LE : INET_DIAG_BC_S_LE;
			op->yes = 2 * opsz;
			op->no = ((last ? rops : 0) + 3) * opsz;
			op++;
			op->no = ports[i+1];
			op++;
		}

		if (!last) {
			op->code = INET_DIAG_BC_JMP;
			op->yes = opsz;
			op->no = (opend - op) * opsz;
			op++;
		}
	}

	*oop = op;
}

// nl_port_filter creates an inet_diag filter to filter by a list of port ranges.
int nl_port_filter(uint16_t sports[], int splen, uint16_t dports[], int dplen,
		struct inet_diag_bc_op **filter) {
	struct inet_diag_bc_op *op;
	int flen;
	int sops = pfops_count(sports, splen);
	int dops = pfops_count(dports, dplen);

	if (splen == 0 && dplen == 0) {
		*filter = NULL;
		return 0;
	}

	flen = (sops + dops) * sizeof(struct inet_diag_bc_op);
	if ((*filter = calloc(1, flen)) == NULL)
		return -1;

	op = *filter; 
	pfops(sports, splen, false, dops, &op);
	pfops(dports, dplen, true, 0, &op);

	return flen;
}
