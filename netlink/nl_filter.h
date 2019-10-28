#ifndef _NL_FILTER_H_
#define _NL_FILTER_H_

#include <stdint.h>

int nl_port_filter(uint16_t sports[], int splen, uint16_t dports[], int dplen,
		struct inet_diag_bc_op **filter);

#endif // _NL_FILTER_H_
