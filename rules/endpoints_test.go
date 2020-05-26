// Copyright (c) 2017-2020 Tigera, Inc. All rights reserved.
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

package rules_test

import (
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/projectcalico/felix/ipsets"
	. "github.com/projectcalico/felix/iptables"
	"github.com/projectcalico/felix/proto"
	. "github.com/projectcalico/felix/rules"
)

var _ = Describe("Endpoints", func() {
	const (
		ProtoUDP  = 17
		ProtoIPIP = 4
		VXLANPort = 4789
		VXLANVNI  = 4096
	)

	for _, trueOrFalse := range []bool{true, false} {
		kubeIPVSEnabled := trueOrFalse
		var rrConfigNormalMangleReturn = Config{
			IPIPEnabled:                 true,
			IPIPTunnelAddress:           nil,
			IPSetConfigV4:               ipsets.NewIPVersionConfig(ipsets.IPFamilyV4, "cali", nil, nil),
			IPSetConfigV6:               ipsets.NewIPVersionConfig(ipsets.IPFamilyV6, "cali", nil, nil),
			IptablesMarkEgress:          0x4,
			IptablesMarkAccept:          0x8,
			IptablesMarkPass:            0x10,
			IptablesMarkScratch0:        0x20,
			IptablesMarkScratch1:        0x40,
			IptablesMarkDrop:            0x80,
			IptablesMarkEndpoint:        0xff00,
			IptablesMarkNonCaliEndpoint: 0x0100,
			KubeIPVSSupportEnabled:      kubeIPVSEnabled,
			IptablesMangleAllowAction:   "RETURN",
			VXLANPort:                   4789,
			VXLANVNI:                    4096,
		}

		var rrConfigConntrackDisabledReturnAction = Config{
			IPIPEnabled:                 true,
			IPIPTunnelAddress:           nil,
			IPSetConfigV4:               ipsets.NewIPVersionConfig(ipsets.IPFamilyV4, "cali", nil, nil),
			IPSetConfigV6:               ipsets.NewIPVersionConfig(ipsets.IPFamilyV6, "cali", nil, nil),
			IptablesMarkEgress:          0x4,
			IptablesMarkAccept:          0x8,
			IptablesMarkPass:            0x10,
			IptablesMarkScratch0:        0x20,
			IptablesMarkScratch1:        0x40,
			IptablesMarkDrop:            0x80,
			IptablesMarkEndpoint:        0xff00,
			IptablesMarkNonCaliEndpoint: 0x0100,
			KubeIPVSSupportEnabled:      kubeIPVSEnabled,
			DisableConntrackInvalid:     true,
			IptablesFilterAllowAction:   "RETURN",
			VXLANPort:                   4789,
			VXLANVNI:                    4096,
		}

		var renderer RuleRenderer
		var epMarkMapper EndpointMarkMapper

		dropVXLANRule := Rule{
			Match: Match().ProtocolNum(ProtoUDP).
				DestPorts(uint16(VXLANPort)),
			Action:  DropAction{},
			Comment: []string{"Drop VXLAN encapped packets originating in pods"},
		}
		dropIPIPRule := Rule{
			Match:   Match().ProtocolNum(ProtoIPIP),
			Action:  DropAction{},
			Comment: []string{"Drop IPinIP encapped packets originating in pods"},
		}

		Context("with normal config", func() {
			BeforeEach(func() {
				renderer = NewRenderer(rrConfigNormalMangleReturn)
				epMarkMapper = NewEndpointMarkMapper(rrConfigNormalMangleReturn.IptablesMarkEndpoint,
					rrConfigNormalMangleReturn.IptablesMarkNonCaliEndpoint)
			})

			It("should render a minimal workload endpoint", func() {
				Expect(renderer.WorkloadEndpointToIptablesChains(
					"cali1234", epMarkMapper,
					true,
					nil,
					nil,
					NotAnEgressGateway)).To(Equal(trimSMChain(kubeIPVSEnabled, []*Chain{
					{
						Name: "cali-tw-cali1234",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},
							{Match: Match().ConntrackState("INVALID"),
								Action: DropAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},
							{Action: NflogAction{Group: 1, Prefix: "DRI"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-fw-cali1234",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},
							{Match: Match().ConntrackState("INVALID"),
								Action: DropAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},
							dropVXLANRule,
							dropIPIPRule,
							{Action: NflogAction{Group: 2, Prefix: "DRE"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-sm-cali1234",
						Rules: []Rule{
							{Action: SetMaskedMarkAction{Mark: 0xd400, Mask: 0xff00}},
						},
					},
				})))
			})

			It("should render a disabled workload endpoint", func() {
				Expect(renderer.WorkloadEndpointToIptablesChains(
					"cali1234", epMarkMapper,
					false,
					nil,
					nil,
					NotAnEgressGateway,
				)).To(Equal(trimSMChain(kubeIPVSEnabled, []*Chain{
					{
						Name: "cali-tw-cali1234",
						Rules: []Rule{
							{Action: DropAction{},
								Comment: []string{"Endpoint admin disabled"}},
						},
					},
					{
						Name: "cali-fw-cali1234",
						Rules: []Rule{
							{Action: DropAction{},
								Comment: []string{"Endpoint admin disabled"}},
						},
					},
					{
						Name: "cali-sm-cali1234",
						Rules: []Rule{
							{Action: SetMaskedMarkAction{Mark: 0xd400, Mask: 0xff00}},
						},
					},
				})))
			})

			It("should render a fully-loaded workload endpoint", func() {
				Expect(renderer.WorkloadEndpointToIptablesChains(
					"cali1234",
					epMarkMapper,
					true,
					[]*proto.TierInfo{{
						Name:            "default",
						IngressPolicies: []string{"ai", "bi"},
						EgressPolicies:  []string{"ae", "be"},
					}},
					[]string{"prof1", "prof2"},
					NotAnEgressGateway,
				)).To(Equal(trimSMChain(kubeIPVSEnabled, []*Chain{
					{
						Name: "cali-tw-cali1234",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},
							{Match: Match().ConntrackState("INVALID"),
								Action: DropAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/ai"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/bi"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: NflogAction{Group: 1, Prefix: "DPI|default"}},
							{Match: Match().MarkClear(0x10),
								Action:  DropAction{},
								Comment: []string{"Drop if no policies passed packet"}},

							{Action: JumpAction{Target: "cali-pri-prof1"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},
							{Action: JumpAction{Target: "cali-pri-prof2"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},

							{Action: NflogAction{Group: 1, Prefix: "DRI"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-fw-cali1234",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},
							{Match: Match().ConntrackState("INVALID"),
								Action: DropAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},
							dropVXLANRule,
							dropIPIPRule,

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-po-default/ae"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-po-default/be"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: NflogAction{Group: 2, Prefix: "DPE|default"}},
							{Match: Match().MarkClear(0x10),
								Action:  DropAction{},
								Comment: []string{"Drop if no policies passed packet"}},

							{Action: JumpAction{Target: "cali-pro-prof1"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},
							{Action: JumpAction{Target: "cali-pro-prof2"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},

							{Action: NflogAction{Group: 2, Prefix: "DRE"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-sm-cali1234",
						Rules: []Rule{
							{Action: SetMaskedMarkAction{Mark: 0xd400, Mask: 0xff00}},
						},
					},
				})))
			})

			It("should render a fully-loaded workload endpoint - one staged policy, one enforced", func() {
				Expect(renderer.WorkloadEndpointToIptablesChains(
					"cali1234",
					epMarkMapper,
					true,
					[]*proto.TierInfo{{
						Name:            "default",
						IngressPolicies: []string{"staged:ai", "bi"},
						EgressPolicies:  []string{"ae", "staged:be"},
					}},
					[]string{"prof1", "prof2"},
					NotAnEgressGateway,
				)).To(Equal(trimSMChain(kubeIPVSEnabled, []*Chain{
					{
						Name: "cali-tw-cali1234",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},
							{Match: Match().ConntrackState("INVALID"),
								Action: DropAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/staged:ai"}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/bi"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: NflogAction{Group: 1, Prefix: "DPI|default"}},
							{Match: Match().MarkClear(0x10),
								Action:  DropAction{},
								Comment: []string{"Drop if no policies passed packet"}},

							{Action: JumpAction{Target: "cali-pri-prof1"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},
							{Action: JumpAction{Target: "cali-pri-prof2"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},

							{Action: NflogAction{Group: 1, Prefix: "DRI"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-fw-cali1234",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},
							{Match: Match().ConntrackState("INVALID"),
								Action: DropAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},
							dropVXLANRule,
							dropIPIPRule,

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-po-default/ae"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-po-default/staged:be"}},
							{Match: Match().MarkClear(0x10),
								Action: NflogAction{Group: 2, Prefix: "DPE|default"}},
							{Match: Match().MarkClear(0x10),
								Action:  DropAction{},
								Comment: []string{"Drop if no policies passed packet"}},

							{Action: JumpAction{Target: "cali-pro-prof1"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},
							{Action: JumpAction{Target: "cali-pro-prof2"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},

							{Action: NflogAction{Group: 2, Prefix: "DRE"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-sm-cali1234",
						Rules: []Rule{
							{Action: SetMaskedMarkAction{Mark: 0xd400, Mask: 0xff00}},
						},
					},
				})))
			})

			It("should render a fully-loaded workload endpoint - both staged, end-of-tier action is pass", func() {
				Expect(renderer.WorkloadEndpointToIptablesChains(
					"cali1234",
					epMarkMapper,
					true,
					[]*proto.TierInfo{{
						Name:            "default",
						IngressPolicies: []string{"staged:ai", "staged:bi"},
						EgressPolicies:  []string{"staged:ae", "staged:be"},
					}},
					[]string{"prof1", "prof2"},
					NotAnEgressGateway,
				)).To(Equal(trimSMChain(kubeIPVSEnabled, []*Chain{
					{
						Name: "cali-tw-cali1234",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},
							{Match: Match().ConntrackState("INVALID"),
								Action: DropAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/staged:ai"}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/staged:bi"}},
							{Match: Match().MarkClear(0x10),
								Action: NflogAction{Group: 1, Prefix: "PPI|default"}},

							{Action: JumpAction{Target: "cali-pri-prof1"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},
							{Action: JumpAction{Target: "cali-pri-prof2"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},

							{Action: NflogAction{Group: 1, Prefix: "DRI"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-fw-cali1234",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},
							{Match: Match().ConntrackState("INVALID"),
								Action: DropAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},
							dropVXLANRule,
							dropIPIPRule,

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-po-default/staged:ae"}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-po-default/staged:be"}},
							{Match: Match().MarkClear(0x10),
								Action: NflogAction{Group: 2, Prefix: "PPE|default"}},

							{Action: JumpAction{Target: "cali-pro-prof1"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},
							{Action: JumpAction{Target: "cali-pro-prof2"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},

							{Action: NflogAction{Group: 2, Prefix: "DRE"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-sm-cali1234",
						Rules: []Rule{
							{Action: SetMaskedMarkAction{Mark: 0xd400, Mask: 0xff00}},
						},
					},
				})))
			})

			It("should render a host endpoint", func() {
				Expect(renderer.HostEndpointToFilterChains("eth0",
					[]*proto.TierInfo{{
						Name:            "default",
						IngressPolicies: []string{"ai", "bi"},
						EgressPolicies:  []string{"ae", "be"},
					}},
					[]*proto.TierInfo{{
						Name:            "default",
						IngressPolicies: []string{"afi", "bfi"},
						EgressPolicies:  []string{"afe", "bfe"},
					}},
					epMarkMapper,
					[]string{"prof1", "prof2"},
				)).To(Equal(trimSMChain(kubeIPVSEnabled, []*Chain{
					{
						Name: "cali-th-eth0",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},
							{Match: Match().ConntrackState("INVALID"),
								Action: DropAction{}},

							// Host endpoints get extra failsafe rules.
							{Action: JumpAction{Target: "cali-failsafe-out"}},

							{Action: ClearMarkAction{Mark: 0x88}},

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-po-default/ae"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-po-default/be"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: NflogAction{Group: 2, Prefix: "DPE|default"}},
							{Match: Match().MarkClear(0x10),
								Action:  DropAction{},
								Comment: []string{"Drop if no policies passed packet"}},

							{Action: JumpAction{Target: "cali-pro-prof1"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},
							{Action: JumpAction{Target: "cali-pro-prof2"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},

							{Action: NflogAction{Group: 2, Prefix: "DRE"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-fh-eth0",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},
							{Match: Match().ConntrackState("INVALID"),
								Action: DropAction{}},

							// Host endpoints get extra failsafe rules.
							{Action: JumpAction{Target: "cali-failsafe-in"}},

							{Action: ClearMarkAction{Mark: 0x88}},

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/ai"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/bi"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: NflogAction{Group: 1, Prefix: "DPI|default"}},
							{Match: Match().MarkClear(0x10),
								Action:  DropAction{},
								Comment: []string{"Drop if no policies passed packet"}},

							{Action: JumpAction{Target: "cali-pri-prof1"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},
							{Action: JumpAction{Target: "cali-pri-prof2"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},

							{Action: NflogAction{Group: 1, Prefix: "DRI"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-thfw-eth0",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},
							{Match: Match().ConntrackState("INVALID"),
								Action: DropAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-po-default/afe"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-po-default/bfe"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: NflogAction{Group: 2, Prefix: "DPE|default"}},
							{Match: Match().MarkClear(0x10),
								Action:  DropAction{},
								Comment: []string{"Drop if no policies passed packet"}},
						},
					},
					{
						Name: "cali-fhfw-eth0",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},
							{Match: Match().ConntrackState("INVALID"),
								Action: DropAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/afi"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/bfi"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: NflogAction{Group: 1, Prefix: "DPI|default"}},
							{Match: Match().MarkClear(0x10),
								Action:  DropAction{},
								Comment: []string{"Drop if no policies passed packet"}},
						},
					},
					{
						Name: "cali-sm-eth0",
						Rules: []Rule{
							{Action: SetMaskedMarkAction{Mark: 0xa200, Mask: 0xff00}},
						},
					},
				})))
			})

			It("should render host endpoint raw chains with untracked policies", func() {
				Expect(renderer.HostEndpointToRawChains("eth0",
					[]*proto.TierInfo{{
						Name:            "default",
						IngressPolicies: []string{"c"},
						EgressPolicies:  []string{"c"},
					}},
				)).To(Equal([]*Chain{
					{
						Name: "cali-th-eth0",
						Rules: []Rule{
							// Host endpoints get extra failsafe rules.
							{Action: JumpAction{Target: "cali-failsafe-out"}},

							{Action: ClearMarkAction{Mark: 0x88}},

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-po-default/c"}},
							// Extra NOTRACK action before returning in raw table.
							{Match: Match().MarkSingleBitSet(0x8),
								Action: NoTrackAction{}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},

							// No drop actions or profiles in raw table.
						},
					},
					{
						Name: "cali-fh-eth0",
						Rules: []Rule{
							// Host endpoints get extra failsafe rules.
							{Action: JumpAction{Target: "cali-failsafe-in"}},

							{Action: ClearMarkAction{Mark: 0x88}},

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/c"}},
							// Extra NOTRACK action before returning in raw table.
							{Match: Match().MarkSingleBitSet(0x8),
								Action: NoTrackAction{}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},

							// No drop actions or profiles in raw table.
						},
					},
				}))
			})

			It("should render host endpoint mangle chains with pre-DNAT policies", func() {
				Expect(renderer.HostEndpointToMangleChains(
					"eth0",
					[]*proto.TierInfo{{
						Name:            "default",
						IngressPolicies: []string{"c"},
					}},
				)).To(Equal([]*Chain{
					{
						Name: "cali-fh-eth0",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: SetMarkAction{Mark: 0x8}},
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: ReturnAction{}},
							{Match: Match().ConntrackState("INVALID"),
								Action: DropAction{}},

							// Host endpoints get extra failsafe rules.
							{Action: JumpAction{Target: "cali-failsafe-in"}},

							{Action: ClearMarkAction{Mark: 0x88}},

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/c"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},

							// No drop actions or profiles in raw table.
						},
					},
				}))
			})

			It("should render an egress gateway workload endpoint", func() {
				Expect(renderer.WorkloadEndpointToIptablesChains(
					"cali1234",
					epMarkMapper,
					true,
					[]*proto.TierInfo{{
						Name:            "default",
						IngressPolicies: []string{"ai", "bi"},
						EgressPolicies:  []string{"ae", "be"},
					}},
					[]string{"prof1", "prof2"},
					IsAnEgressGateway,
				)).To(Equal(trimSMChain(kubeIPVSEnabled, []*Chain{
					{
						Name: "cali-tw-cali1234",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/ai"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/bi"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: NflogAction{Group: 1, Prefix: "DPI|default"}},
							{Match: Match().MarkClear(0x10),
								Action:  DropAction{},
								Comment: []string{"Drop if no policies passed packet"}},

							{Action: JumpAction{Target: "cali-pri-prof1"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},
							{Action: JumpAction{Target: "cali-pri-prof2"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},

							{Action: NflogAction{Group: 1, Prefix: "DRI"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-fw-cali1234",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},
							dropVXLANRule,
							dropIPIPRule,

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-po-default/ae"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-po-default/be"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},
							{Match: Match().MarkClear(0x10),
								Action: NflogAction{Group: 2, Prefix: "DPE|default"}},
							{Match: Match().MarkClear(0x10),
								Action:  DropAction{},
								Comment: []string{"Drop if no policies passed packet"}},

							{Action: JumpAction{Target: "cali-pro-prof1"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},
							{Action: JumpAction{Target: "cali-pro-prof2"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if profile accepted"}},

							{Action: NflogAction{Group: 2, Prefix: "DRE"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-sm-cali1234",
						Rules: []Rule{
							{Action: SetMaskedMarkAction{Mark: 0xd400, Mask: 0xff00}},
						},
					},
				})))
			})
		})

		Describe("with ctstate=INVALID disabled", func() {
			BeforeEach(func() {
				renderer = NewRenderer(rrConfigConntrackDisabledReturnAction)
				epMarkMapper = NewEndpointMarkMapper(rrConfigConntrackDisabledReturnAction.IptablesMarkEndpoint,
					rrConfigConntrackDisabledReturnAction.IptablesMarkNonCaliEndpoint)
			})

			It("should render a minimal workload endpoint", func() {
				Expect(renderer.WorkloadEndpointToIptablesChains(
					"cali1234",
					epMarkMapper,
					true,
					nil,
					nil,
					NotAnEgressGateway,
				)).To(Equal(trimSMChain(kubeIPVSEnabled, []*Chain{
					{
						Name: "cali-tw-cali1234",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: SetMarkAction{Mark: 0x8}},
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: ReturnAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},

							{Action: NflogAction{Group: 1, Prefix: "DRI"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-fw-cali1234",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: SetMarkAction{Mark: 0x8}},
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: ReturnAction{}},

							{Action: ClearMarkAction{Mark: 0x88}},
							dropVXLANRule,
							dropIPIPRule,

							{Action: NflogAction{Group: 2, Prefix: "DRE"}},
							{Action: DropAction{},
								Comment: []string{"Drop if no profiles matched"}},
						},
					},
					{
						Name: "cali-sm-cali1234",
						Rules: []Rule{
							{Action: SetMaskedMarkAction{Mark: 0xd400, Mask: 0xff00}},
						},
					},
				})))
			})

			It("should render host endpoint mangle chains with pre-DNAT policies", func() {
				Expect(renderer.HostEndpointToMangleChains(
					"eth0",
					[]*proto.TierInfo{{
						Name:            "default",
						IngressPolicies: []string{"c"},
					}},
				)).To(Equal([]*Chain{
					{
						Name: "cali-fh-eth0",
						Rules: []Rule{
							// conntrack rules.
							{Match: Match().ConntrackState("RELATED,ESTABLISHED"),
								Action: AcceptAction{}},

							// Host endpoints get extra failsafe rules.
							{Action: JumpAction{Target: "cali-failsafe-in"}},

							{Action: ClearMarkAction{Mark: 0x88}},

							{Comment: []string{"Start of tier default"},
								Action: ClearMarkAction{Mark: 0x10}},
							{Match: Match().MarkClear(0x10),
								Action: JumpAction{Target: "cali-pi-default/c"}},
							{Match: Match().MarkSingleBitSet(0x8),
								Action:  ReturnAction{},
								Comment: []string{"Return if policy accepted"}},

							// No drop actions or profiles in raw table.
						},
					},
				}))
			})
		})
	}
})

func trimSMChain(ipvsEnable bool, chains []*Chain) []*Chain {
	result := []*Chain{}
	for _, chain := range chains {
		if !ipvsEnable && strings.HasPrefix(chain.Name, "cali-sm") {
			continue
		}
		result = append(result, chain)
	}

	return result
}
