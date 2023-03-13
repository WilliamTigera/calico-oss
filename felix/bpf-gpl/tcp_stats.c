// Project Calico BPF dataplane programs.
// Copyright (c) 2020-2023 Tigera, Inc. All rights reserved.
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

#include "events.h"
#include "tcp_stats.h"
#include "socket_lookup.h"
#include "globals.h"

const volatile struct cali_tc_globals __globals;
SEC("classifier/tc/calico_tcp_stats")
int calico_tcp_stats(struct __sk_buff *skb)
{
	struct cali_tc_ctx ctx = {
		.skb = skb,
	};
	if (!skb_refresh_validate_ptrs(&ctx, TCP_SIZE)) {
		if ((ip_hdr(&ctx)->ihl == 5) && (ip_hdr(&ctx)->protocol == IPPROTO_TCP)) {
			socket_lookup(&ctx);
		}
	}

	return TC_ACT_UNSPEC;
}
