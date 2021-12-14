// Project Calico BPF dataplane programs.
// Copyright (c) 2021 Tigera, Inc. All rights reserved.
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

#ifndef __CALI_EVETNS_H__
#define __CALI_EVETNS_H__

#include "bpf.h"
#include "perf.h"
#include "sock.h"
#include "jump.h"
#include <linux/bpf_perf_event.h>
#include "events_type.h"

struct event_tcp_stats {
	struct perf_event_header hdr;
	__u8 saddr[16];
	__u8 daddr[16];
	__u16 sport;
	__u16 dport;
	__u32 snd_cwnd;
	__u32 srtt_us;
	__u32 rtt_min;
	__u32 mss_cache;
	__u32 total_retrans;
	__u32 lost_out;
	__u32 icsk_retransmits;
};

static CALI_BPF_INLINE void event_tcp_stats (struct __sk_buff *skb, struct event_tcp_stats *event) {
	int err = perf_commit_event(skb, event, sizeof(struct event_tcp_stats));
	if (err != 0) {
		CALI_DEBUG("tcp stats: perf_commit_event returns %d\n", err);
	}
}

static CALI_BPF_INLINE void event_flow_log(struct __sk_buff *skb, struct cali_tc_state *state)
{
	state->eventhdr.type = EVENT_POLICY_VERDICT,
	state->eventhdr.len = offsetof(struct cali_tc_state, rule_ids) + sizeof(__u64) * MAX_RULE_IDS;

	/* Due to stack space limitations, the begining of the state is structured as the
	 * event and so we can send the data straight without copying in BPF.
	 */
	int err = perf_commit_event(skb, state, state->eventhdr.len);

	if (err != 0) {
		CALI_DEBUG("event_flow_log: perf_commit_event returns %d\n", err);
	}
}

#endif /* __CALI_EVETNS_H__ */
