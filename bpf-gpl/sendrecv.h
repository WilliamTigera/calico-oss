// Project Calico BPF dataplane programs.
// Copyright (c) 2020 Tigera, Inc. All rights reserved.
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

#ifndef __SENDRECV_H__
#define __SENDRECV_H__

struct sendrecv4_key {
	uint64_t cookie;
	uint32_t ip;
	uint32_t port; /* because bpf_sock_addr uses 32bit and we would need padding */
};

struct sendrecv4_val {
	uint32_t ip;
	uint32_t port; /* because bpf_sock_addr uses 32bit and we would need padding */
};

CALI_MAP_V1(cali_v4_srmsg,
		BPF_MAP_TYPE_LRU_HASH,
		struct sendrecv4_key, struct sendrecv4_val,
		510000, 0, MAP_PIN_GLOBAL)

static CALI_BPF_INLINE uint16_t ctx_port_to_host(__u32 port)
{
	return be32_to_host(port) >> 16;
}

static CALI_BPF_INLINE __u32 host_to_ctx_port(uint16_t port)
{
	return host_to_be32(((uint32_t)port) << 16);
}

#endif /* __SENDRECV_H__ */
