// Project Calico BPF dataplane programs.
// Copyright (c) 2020-2021 Tigera, Inc. All rights reserved.
//
// This program is free software; you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation; either version 2 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License along
// with this program; if not, write to the Free Software Foundation, Inc.,
// 51 Franklin Street, Fifth Floor, Boston, MA 02110-1301 USA.

#ifndef __CALI_CONNTRACK_TYPES_H__
#define __CALI_CONNTRACK_TYPES_H__

// Connection tracking.

struct calico_ct_key {
	__u32 protocol;
	__be32 addr_a, addr_b; // NBO
	__u16 port_a, port_b; // HBO
};

enum cali_ct_type {
	CALI_CT_TYPE_NORMAL	= 0x00, /* Non-NATted entry. */
	CALI_CT_TYPE_NAT_FWD	= 0x01, /* Forward entry for a DNATted flow, keyed on orig src/dst.
					 * Points to the reverse entry.
					 */
	CALI_CT_TYPE_NAT_REV	= 0x02, /* "Reverse" entry for a NATted flow, contains NAT +
					 * tracking information.
					 */
};

#define CALI_CT_FLAG_NAT_OUT	(1 << 0)
#define CALI_CT_FLAG_DSR_FWD	(1 << 1) /* marks entry into the tunnel on the fwd node when dsr */
#define CALI_CT_FLAG_NP_FWD	(1 << 2) /* marks entry into the tunnel on the fwd node */
#define CALI_CT_FLAG_SKIP_FIB	(1 << 3) /* marks traffic that should pass through host IP stack */
#define CALI_CT_FLAG_TRUST_DNS	(1 << 4) /* marks connection to a trusted DNS server */

struct calico_ct_leg {
	__u32 seqno;

	__u32 syn_seen:1;
	__u32 ack_seen:1;
	__u32 fin_seen:1;
	__u32 rst_seen:1;

	__u32 whitelisted:1;

	__u32 opener:1;

	__u32 ifindex; /* where the packet entered the system from */
};

#define CT_INVALID_IFINDEX	0
struct calico_ct_value {
	__u64 created;
	__u64 last_seen; // 8
	__u8 type;		 // 16
	__u8 flags;

	// Important to use explicit padding, otherwise the compiler can decide
	// not to zero the padding bytes, which upsets the verifier.  Worse than
	// that, debug logging often prevents such optimisation resulting in
	// failures when debug logging is compiled out only :-).
	__u8 pad0[6];
	union {
		// CALI_CT_TYPE_NORMAL and CALI_CT_TYPE_NAT_REV.
		struct {
			struct calico_ct_leg a_to_b; // 24
			struct calico_ct_leg b_to_a; // 36

			// CALI_CT_TYPE_NAT_REV
			__u32 orig_ip;                     // 44
			__u16 orig_port;                   // 48
			__u8 pad1[2];                      // 50
			__u32 tun_ip;                      // 52
			__u32 pad3;                        // 56
		};

		// CALI_CT_TYPE_NAT_FWD; key for the CALI_CT_TYPE_NAT_REV entry.
		struct {
			struct calico_ct_key nat_rev_key;  // 24
			__u8 pad2[8];
		};
	};
};

#define CT_CREATE_NORMAL	0
#define CT_CREATE_NAT		1
#define CT_CREATE_NAT_FWD	2

struct ct_ctx {
	struct __sk_buff *skb;
	__u8 proto;
	__be32 src;
	__be32 orig_dst;
	__be32 dst;
	__u16 sport;
	__u16 dport;
	__u16 orig_dport;
	struct tcphdr *tcp;
	__be32 tun_ip; /* is set when the packet arrive through the NP tunnel.
			* It is also set on the first node when we create the
			* initial CT entry for the tunneled traffic. */
	__u8 flags;
};

CALI_MAP(cali_v4_ct, 2,
		BPF_MAP_TYPE_HASH,
		struct calico_ct_key, struct calico_ct_value,
		512000, BPF_F_NO_PREALLOC, MAP_PIN_GLOBAL)

enum calico_ct_result_type {
	/* CALI_CT_NEW means that the packet is not part of a known conntrack flow.
	 * TCP SYN packets are always treated as NEW so they always go through policy. */
	CALI_CT_NEW,
	/* CALI_CT_MID_FLOW_MISS indicates that the packet is known to be of a type that
	 * cannot be the start of a flow but it also has no matching conntrack entry.  For
	 * example, a TCP packet without SYN set. */
	CALI_CT_MID_FLOW_MISS,
	/* CALI_CT_ESTABLISHED indicates the packet is part of a known flow, approved at "this"
	 * side.  I.e. it's safe to let this packet through _this_ program.  If a packet is
	 * ESTABLISHED but not ESTABLISHED_BYPASS then it has only been approved by _this_
	 * program, but downstream programs still need to have their say. For example, if this
	 * is a workload egress program then it implements egress policy for one workload. If
	 * that workload communicates with another workload on the same host then the packet
	 * needs to be approved by the ingress policy program attached to the other workload. */
	CALI_CT_ESTABLISHED,
	/* CALI_CT_ESTABLISHED_BYPASS indicates the packet is part of a known flow and *both*
	 * legs of the conntrack entry have been approved.  Hence it is safe to set the bypass
	 * mark bit on the traffic so that any downstream BPF programs let the packet through
	 * automatically. */
	CALI_CT_ESTABLISHED_BYPASS,
	/* CALI_CT_ESTABLISHED_SNAT means the packet is a response packet on a NATted flow;
	 * hence the packet needs to be SNATted. The new src IP and port are returned in
	 * result.nat_ip and result.nat_port. */
	CALI_CT_ESTABLISHED_SNAT,
	/* CALI_CT_ESTABLISHED_DNAT means the packet is a request packet on a NATted flow;
	 * hence the packet needs to be DNATted. The new dst IP and port are returned in
	 * result.nat_ip and result.nat_port. */
	CALI_CT_ESTABLISHED_DNAT,
	/* CALI_CT_INVALID is returned for packets that cannot be parsed (e.g. invalid ICMP response)
	 * or for packet that have a conntrack entry that is only approved by the other leg
	 * (indicating that policy on this leg failed to allow the packet). */
	CALI_CT_INVALID,
};

#define CALI_CT_RELATED		(1 << 8)
#define CALI_CT_RPF_FAILED	(1 << 9)
#define CALI_CT_TUN_SRC_CHANGED	(1 << 10)

#define ct_result_rc(rc)		((rc) & 0xff)
#define ct_result_flags(rc)		((rc) & ~0xff)
#define ct_result_set_rc(val, rc)	((val) = ct_result_flags(val) | (rc))
#define ct_result_set_flag(val, flags)	((val) |= (flags))

#define ct_result_is_related(rc)	((rc) & CALI_CT_RELATED)
#define ct_result_rpf_failed(rc)	((rc) & CALI_CT_RPF_FAILED)
#define ct_result_tun_src_changed(rc)	((rc) & CALI_CT_TUN_SRC_CHANGED)

struct calico_ct_result {
	__s16 rc;
	__u16 flags;
	__be32 nat_ip;
	__u32 nat_port;
	__be32 tun_ip;
	__u32 ifindex_fwd; /* if set, the ifindex where the packet should be forwarded */
};

#endif /* __CALI_CONNTRAC_TYPESK_H__ */
