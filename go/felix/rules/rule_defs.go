// Copyright (c) 2016 Tigera, Inc. All rights reserved.
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

package rules

import (
	"github.com/projectcalico/felix/go/felix/ipsets"
	"github.com/projectcalico/felix/go/felix/iptables"
	"github.com/projectcalico/felix/go/felix/proto"
	"net"
)

const (
	ChainNamePrefix = "cali"
	IPSetNamePrefix = "cali"

	FilterInputChainName   = ChainNamePrefix + "-INPUT"
	FilterForwardChainName = ChainNamePrefix + "-FORWARD"
	FilterOutputChainName  = ChainNamePrefix + "-OUTPUT"

	NATPreroutingChainName = ChainNamePrefix + "-PREROUTING"

	PolicyInboundPfx  = ChainNamePrefix + "pi-"
	PolicyOutboundPfx = ChainNamePrefix + "po-"

	DispatchToWorkloadEndpoint   = ChainNamePrefix + "-to-wl-endpoint"
	DispatchFromWorkloadEndpoint = ChainNamePrefix + "-from-wl-endpoint"

	DispatchToHostEndpoint   = ChainNamePrefix + "-to-host-endpoint"
	DispatchFromHostEndpoint = ChainNamePrefix + "-from-host-endpoint"

	WorkloadToEndpointPfx   = ChainNamePrefix + "tw-"
	WorkloadFromEndpointPfx = ChainNamePrefix + "fw-"

	HostToEndpointPfx   = ChainNamePrefix + "th-"
	HostFromEndpointPfx = ChainNamePrefix + "fh-"

	RuleHashPrefix = "cali:"

	// HistoricNATRuleInsertRegex is a regex pattern to match to match
	// special-case rules inserted by old versions of felix.  Specifically,
	// Python felix used to insert a masquerade rule directly into the
	// POSTROUTING chain.
	//
	// Note: this regex depends on the output format of iptables-save so,
	// where possible, it's best to match only on part of the rule that
	// we're sure can't change (such as the ipset name in the masquerade
	// rule).
	HistoricInsertedNATRuleRegex =
		`-A POSTROUTING .* felix-masq-ipam-pools .*|` +
		`-A POSTROUTING -o tunl0 -m addrtype ! --src-type LOCAL --limit-iface-out -m addrtype --src-type LOCAL -j MASQUERADE`
)

var (
	// AllHistoricChainNamePrefixes lists all the prefixes that we've used for chains.  Keeping
	// track of the old names lets us clean them up.
	AllHistoricChainNamePrefixes = []string{"felix-", "cali"}
	// AllHistoricIPSetNamePrefixes, similarly contains all the prefixes we've ever used for IP
	// sets.
	AllHistoricIPSetNamePrefixes = []string{"felix-", "cali"}
	// LegacyV4IPSetNames contains some extra IP set names that were used in older versions of
	// Felix and don't fit our versioned pattern.
	LegacyV4IPSetNames = []string{"felix-masq-ipam-pools", "felix-all-ipam-pools"}
)

type RuleRenderer interface {
	StaticFilterTableChains() []*iptables.Chain
	StaticNATTableChains(ipVersion uint8) []*iptables.Chain

	WorkloadDispatchChains(map[proto.WorkloadEndpointID]*proto.WorkloadEndpoint) []*iptables.Chain
	WorkloadEndpointToIptablesChains(epID *proto.WorkloadEndpointID, endpoint *proto.WorkloadEndpoint) []*iptables.Chain

	HostDispatchChains(map[proto.HostEndpointID]*proto.HostEndpoint) []*iptables.Chain
	HostEndpointToIptablesChains(epID *proto.HostEndpointID, endpoint *proto.HostEndpoint) []*iptables.Chain

	PolicyToIptablesChains(policyID *proto.PolicyID, policy *proto.Policy, ipVersion uint8) []*iptables.Chain
	ProfileToIptablesChains(policyID *proto.ProfileID, policy *proto.Profile, ipVersion uint8) []*iptables.Chain
}

type ruleRenderer struct {
	Config
}

type Config struct {
	IPSetConfigV4 *ipsets.IPVersionConfig
	IPSetConfigV6 *ipsets.IPVersionConfig

	WorkloadIfacePrefixes []string

	IptablesMarkAccept    uint32
	IptablesMarkNextTier  uint32
	IptablesMarkDrop      uint32
	IptablesMarkEndpoints uint32

	WhitelistDHCPToHost   bool
	OpenStackMetadataIP   net.IP
	OpenStackMetadataPort uint16
}

func NewRenderer(config Config) RuleRenderer {
	return &ruleRenderer{config}
}
