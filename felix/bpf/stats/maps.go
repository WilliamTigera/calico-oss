//go:build !windows
// +build !windows

// Copyright (c) 2021 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package stats

import (
	"github.com/projectcalico/calico/felix/bpf"
)

const keySize = 36
const keyValueSize = 8

var SocketStatsMapParameters = bpf.MapParameters{
	Filename:   "/sys/fs/bpf/tc/globals/cali_sstats",
	Type:       "lru_hash",
	KeySize:    keySize,
	ValueSize:  keyValueSize,
	MaxEntries: 511000,
	Name:       "cali_sstats",
	Version:    2,
}

func SocketStatsMap(mc *bpf.MapContext) bpf.Map {
	return mc.NewPinnedMap(SocketStatsMapParameters)
}
