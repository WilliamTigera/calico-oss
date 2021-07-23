// +build !windows

// Copyright (c) 2018-2021 Tigera, Inc. All rights reserved.

package collector

import (
	"fmt"
	net2 "net"
	"strings"
	"testing"
	"time"

	kapiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/proxy"

	v3 "github.com/projectcalico/tigera/api/pkg/apis/projectcalico/v3"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/net"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/projectcalico/felix/calc"
	"github.com/projectcalico/felix/proto"
	"github.com/projectcalico/felix/rules"

	"github.com/tigera/nfnetlink"
	"github.com/tigera/nfnetlink/nfnl"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	ipv4       = 0x800
	proto_icmp = 1
	proto_tcp  = 6
	proto_udp  = 17
)

var (
	localIp1Str  = "10.0.0.1"
	localIp1     = ipStrTo16Byte(localIp1Str)
	localIp2Str  = "10.0.0.2"
	localIp2     = ipStrTo16Byte(localIp2Str)
	remoteIp1Str = "20.0.0.1"
	remoteIp1    = ipStrTo16Byte(remoteIp1Str)
	remoteIp2Str = "20.0.0.2"
	remoteIp2    = ipStrTo16Byte(remoteIp2Str)
	localIp1DNAT = ipStrTo16Byte("192.168.0.1")
	localIp2DNAT = ipStrTo16Byte("192.168.0.2")
	publicIP1Str = "1.0.0.1"
	publicIP2Str = "2.0.0.2"
	netSetIp1Str = "8.8.8.8"
	netSetIp1    = ipStrTo16Byte(netSetIp1Str)
)

var (
	srcPort     = 54123
	dstPort     = 80
	dstPortDNAT = 8080
)

var (
	localWlEPKey1 = model.WorkloadEndpointKey{
		Hostname:       "localhost",
		OrchestratorID: "orchestrator",
		WorkloadID:     "localworkloadid1",
		EndpointID:     "localepid1",
	}

	localWlEPKey2 = model.WorkloadEndpointKey{
		Hostname:       "localhost",
		OrchestratorID: "orchestrator",
		WorkloadID:     "localworkloadid2",
		EndpointID:     "localepid2",
	}

	localHostEpKey1 = model.HostEndpointKey{
		Hostname:   "localhost",
		EndpointID: "eth1",
	}

	remoteWlEpKey1 = model.WorkloadEndpointKey{
		OrchestratorID: "orchestrator",
		WorkloadID:     "remoteworkloadid1",
		EndpointID:     "remoteepid1",
	}
	remoteWlEpKey2 = model.WorkloadEndpointKey{
		OrchestratorID: "orchestrator",
		WorkloadID:     "remoteworkloadid2",
		EndpointID:     "remoteepid2",
	}

	remoteHostEpKey1 = model.HostEndpointKey{
		Hostname:   "remotehost",
		EndpointID: "eth1",
	}

	localWlEp1 = &model.WorkloadEndpoint{
		State:    "active",
		Name:     "cali1",
		Mac:      mustParseMac("01:02:03:04:05:06"),
		IPv4Nets: []net.IPNet{mustParseNet("10.0.0.1/32")},
		Labels: map[string]string{
			"id": "local-ep-1",
		},
	}
	localWlEp2 = &model.WorkloadEndpoint{
		State:    "active",
		Name:     "cali2",
		Mac:      mustParseMac("01:02:03:04:05:07"),
		IPv4Nets: []net.IPNet{mustParseNet("10.0.0.2/32")},
		Labels: map[string]string{
			"id": "local-ep-2",
		},
	}
	localHostEp1 = &model.HostEndpoint{
		Name:              "eth1",
		ExpectedIPv4Addrs: []net.IP{mustParseIP("10.0.0.1")},
		Labels: map[string]string{
			"id": "loc-ep-1",
		},
	}
	remoteWlEp1 = &model.WorkloadEndpoint{
		State:    "active",
		Name:     "cali3",
		Mac:      mustParseMac("02:02:03:04:05:06"),
		IPv4Nets: []net.IPNet{mustParseNet("20.0.0.1/32")},
		Labels: map[string]string{
			"id": "remote-ep-1",
		},
	}
	remoteWlEp2 = &model.WorkloadEndpoint{
		State:    "active",
		Name:     "cali4",
		Mac:      mustParseMac("02:03:03:04:05:06"),
		IPv4Nets: []net.IPNet{mustParseNet("20.0.0.2/32")},
		Labels: map[string]string{
			"id": "remote-ep-2",
		},
	}
	remoteHostEp1 = &model.HostEndpoint{
		Name:              "eth1",
		ExpectedIPv4Addrs: []net.IP{mustParseIP("20.0.0.1")},
		Labels: map[string]string{
			"id": "rem-ep-1",
		},
	}
	localEd1 = &calc.EndpointData{
		Key:      localWlEPKey1,
		Endpoint: localWlEp1,
		IsLocal:  true,
		Ingress: &calc.MatchData{
			PolicyMatches: map[calc.PolicyID]int{
				calc.PolicyID{Name: "policy1", Tier: "default"}: 0,
				calc.PolicyID{Name: "policy2", Tier: "default"}: 0,
			},
			TierData: map[string]*calc.TierData{
				"default": {
					ImplicitDropRuleID: calc.NewRuleID("default", "policy2", "", calc.RuleIDIndexImplicitDrop,
						rules.RuleDirIngress, rules.RuleActionDeny),
					EndOfTierMatchIndex: 0,
				},
			},
			ProfileMatchIndex: 0,
		},
		Egress: &calc.MatchData{
			PolicyMatches: map[calc.PolicyID]int{
				calc.PolicyID{Name: "policy1", Tier: "default"}: 0,
				calc.PolicyID{Name: "policy2", Tier: "default"}: 0,
			},
			TierData: map[string]*calc.TierData{
				"default": {
					ImplicitDropRuleID: calc.NewRuleID("default", "policy2", "", calc.RuleIDIndexImplicitDrop,
						rules.RuleDirIngress, rules.RuleActionDeny),
					EndOfTierMatchIndex: 0,
				},
			},
			ProfileMatchIndex: 0,
		},
	}
	localEd2 = &calc.EndpointData{
		Key:      localWlEPKey2,
		Endpoint: localWlEp2,
		IsLocal:  true,
		Ingress: &calc.MatchData{
			PolicyMatches: map[calc.PolicyID]int{
				calc.PolicyID{Name: "policy1", Tier: "default"}: 0,
				calc.PolicyID{Name: "policy2", Tier: "default"}: 0,
			},
			TierData: map[string]*calc.TierData{
				"default": {
					ImplicitDropRuleID: calc.NewRuleID("default", "policy2", "", calc.RuleIDIndexImplicitDrop,
						rules.RuleDirIngress, rules.RuleActionDeny),
					EndOfTierMatchIndex: 0,
				},
			},
			ProfileMatchIndex: 0,
		},
		Egress: &calc.MatchData{
			PolicyMatches: map[calc.PolicyID]int{
				calc.PolicyID{Name: "policy1", Tier: "default"}: 0,
				calc.PolicyID{Name: "policy2", Tier: "default"}: 0,
			},
			TierData: map[string]*calc.TierData{
				"default": {
					ImplicitDropRuleID: calc.NewRuleID("default", "policy2", "", calc.RuleIDIndexImplicitDrop,
						rules.RuleDirIngress, rules.RuleActionDeny),
					EndOfTierMatchIndex: 0,
				},
			},
			ProfileMatchIndex: 0,
		},
	}
	remoteEd1 = &calc.EndpointData{
		Key:      remoteWlEpKey1,
		Endpoint: remoteWlEp1,
		IsLocal:  false,
	}
	remoteEd2 = &calc.EndpointData{
		Key:      remoteWlEpKey2,
		Endpoint: remoteWlEp2,
		IsLocal:  false,
	}
	localHostEd1 = &calc.EndpointData{
		Key:      localHostEpKey1,
		Endpoint: localHostEp1,
		IsLocal:  true,
		Ingress: &calc.MatchData{
			PolicyMatches: map[calc.PolicyID]int{
				calc.PolicyID{Name: "policy1", Tier: "default"}: 0,
				calc.PolicyID{Name: "policy2", Tier: "default"}: 0,
			},
			TierData: map[string]*calc.TierData{
				"default": {
					ImplicitDropRuleID: calc.NewRuleID("default", "policy2", "", calc.RuleIDIndexImplicitDrop,
						rules.RuleDirIngress, rules.RuleActionDeny),
					EndOfTierMatchIndex: 0,
				},
			},
			ProfileMatchIndex: 0,
		},
		Egress: &calc.MatchData{
			PolicyMatches: map[calc.PolicyID]int{
				calc.PolicyID{Name: "policy1", Tier: "default"}: 0,
				calc.PolicyID{Name: "policy2", Tier: "default"}: 0,
			},
			TierData: map[string]*calc.TierData{
				"default": {
					ImplicitDropRuleID: calc.NewRuleID("default", "policy2", "", calc.RuleIDIndexImplicitDrop,
						rules.RuleDirIngress, rules.RuleActionDeny),
					EndOfTierMatchIndex: 0,
				},
			},
			ProfileMatchIndex: 0,
		},
	}
	remoteHostEd1 = &calc.EndpointData{
		Key:      remoteHostEpKey1,
		Endpoint: remoteHostEp1,
		IsLocal:  false,
	}

	netSetKey1 = model.NetworkSetKey{
		Name: "dns-servers",
	}
	netSet1 = model.NetworkSet{
		Nets:   []net.IPNet{mustParseNet(netSetIp1Str + "/32")},
		Labels: map[string]string{"public": "true"},
	}

	svcKey1 = model.ResourceKey{
		Name:      "test-svc",
		Namespace: "test-namespace",
		Kind:      v3.KindK8sService,
	}
	svc1 = kapiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-namespace"},
		Spec: kapiv1.ServiceSpec{
			ClusterIP: "10.10.10.10",
			Ports: []kapiv1.ServicePort{
				{
					Name:       "nginx",
					Port:       80,
					TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "nginx"},
					Protocol:   kapiv1.ProtocolTCP,
				},
			},
		},
	}

	proc1 = ProcessInfo{
		Tuple: Tuple{
			src:   remoteIp1,
			dst:   localIp1,
			proto: proto_tcp,
			l4Src: srcPort,
			l4Dst: dstPort,
		},
		ProcessData: ProcessData{
			Name: "test-process",
			Pid:  1234,
		},
	}
)

// Nflog prefix test parameters
var (
	defTierAllowIngressNFLOGPrefix   = [64]byte{'A', 'P', 'I', '0', '|', 'd', 'e', 'f', 'a', 'u', 'l', 't', '.', 'p', 'o', 'l', 'i', 'c', 'y', '1'}
	defTierAllowEgressNFLOGPrefix    = [64]byte{'A', 'P', 'E', '0', '|', 'd', 'e', 'f', 'a', 'u', 'l', 't', '.', 'p', 'o', 'l', 'i', 'c', 'y', '1'}
	defTierDenyIngressNFLOGPrefix    = [64]byte{'D', 'P', 'I', '0', '|', 'd', 'e', 'f', 'a', 'u', 'l', 't', '.', 'p', 'o', 'l', 'i', 'c', 'y', '2'}
	defTierPolicy1AllowIngressRuleID = &calc.RuleID{
		PolicyID: calc.PolicyID{
			Tier:      "default",
			Name:      "policy1",
			Namespace: "",
		},
		Index:     0,
		IndexStr:  "0",
		Action:    rules.RuleActionAllow,
		Direction: rules.RuleDirIngress,
	}
	defTierPolicy1AllowEgressRuleID = &calc.RuleID{
		PolicyID: calc.PolicyID{
			Tier:      "default",
			Name:      "policy1",
			Namespace: "",
		},
		Index:     0,
		IndexStr:  "0",
		Action:    rules.RuleActionAllow,
		Direction: rules.RuleDirEgress,
	}
	defTierPolicy2DenyIngressRuleID = &calc.RuleID{
		PolicyID: calc.PolicyID{
			Tier:      "default",
			Name:      "policy2",
			Namespace: "",
		},
		Index:     0,
		IndexStr:  "0",
		Action:    rules.RuleActionDeny,
		Direction: rules.RuleDirIngress,
	}
)

var ingressPktAllow = &nfnetlink.NflogPacketAggregate{
	Prefixes: []nfnetlink.NflogPrefix{
		{
			Prefix:  defTierAllowIngressNFLOGPrefix,
			Len:     20,
			Bytes:   100,
			Packets: 1,
		},
	},
	Tuple: nfnetlink.NflogPacketTuple{
		Src:   remoteIp1,
		Dst:   localIp1,
		Proto: proto_tcp,
		L4Src: nfnetlink.NflogL4Info{Port: srcPort},
		L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
	},
}

var ingressPktAllowTuple = NewTuple(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)

var egressPktAllow = &nfnetlink.NflogPacketAggregate{
	Prefixes: []nfnetlink.NflogPrefix{
		{
			Prefix:  defTierAllowEgressNFLOGPrefix,
			Len:     20,
			Bytes:   100,
			Packets: 1,
		},
	},
	Tuple: nfnetlink.NflogPacketTuple{
		Src:   localIp1,
		Dst:   remoteIp1,
		Proto: proto_udp,
		L4Src: nfnetlink.NflogL4Info{Port: srcPort},
		L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
	},
}
var egressPktAllowTuple = NewTuple(localIp1, remoteIp1, proto_udp, srcPort, dstPort)

var ingressPktDeny = &nfnetlink.NflogPacketAggregate{
	Prefixes: []nfnetlink.NflogPrefix{
		{
			Prefix:  defTierDenyIngressNFLOGPrefix,
			Len:     20,
			Bytes:   100,
			Packets: 1,
		},
	},
	Tuple: nfnetlink.NflogPacketTuple{
		Src:   remoteIp1,
		Dst:   localIp1,
		Proto: proto_tcp,
		L4Src: nfnetlink.NflogL4Info{Port: srcPort},
		L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
	},
}
var ingressPktDenyTuple = NewTuple(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)

var localPktIngress = &nfnetlink.NflogPacketAggregate{
	Prefixes: []nfnetlink.NflogPrefix{
		{
			Prefix:  defTierDenyIngressNFLOGPrefix,
			Len:     22,
			Bytes:   100,
			Packets: 1,
		},
	},
	Tuple: nfnetlink.NflogPacketTuple{
		Src:   localIp1,
		Dst:   localIp2,
		Proto: proto_tcp,
		L4Src: nfnetlink.NflogL4Info{Port: srcPort},
		L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
	},
}
var localPktIngressWithDNAT = &nfnetlink.NflogPacketAggregate{
	Prefixes: []nfnetlink.NflogPrefix{
		{
			Prefix:  defTierDenyIngressNFLOGPrefix,
			Len:     22,
			Bytes:   100,
			Packets: 1,
		},
	},
	Tuple: nfnetlink.NflogPacketTuple{
		Src:   localIp1,
		Dst:   localIp2,
		Proto: proto_tcp,
		L4Src: nfnetlink.NflogL4Info{Port: srcPort},
		L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
	},
	OriginalTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        localIp2DNAT,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPortDNAT},
	},
	IsDNAT: true,
}

var localPktEgress = &nfnetlink.NflogPacketAggregate{
	Prefixes: []nfnetlink.NflogPrefix{
		{
			Prefix:  defTierAllowEgressNFLOGPrefix,
			Len:     20,
			Bytes:   100,
			Packets: 1,
		},
	},
	Tuple: nfnetlink.NflogPacketTuple{
		Src:   localIp1,
		Dst:   localIp2,
		Proto: proto_tcp,
		L4Src: nfnetlink.NflogL4Info{Port: srcPort},
		L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
	},
}

var _ = Describe("NFLOG Datasource", func() {
	Describe("NFLOG Incoming Packets", func() {
		// Inject info nflogChan
		var c *collector
		var lm *calc.LookupsCache
		var nflogReader *NFLogReader
		conf := &Config{
			StatsDumpFilePath:            "/tmp/qwerty",
			AgeTimeout:                   time.Duration(10) * time.Second,
			InitialReportingDelay:        time.Duration(5) * time.Second,
			ExportingInterval:            time.Duration(1) * time.Second,
			MaxOriginalSourceIPsIncluded: 5,
		}
		rm := NewReporterManager()
		BeforeEach(func() {
			epMap := map[[16]byte]*calc.EndpointData{
				localIp1:  localEd1,
				localIp2:  localEd2,
				remoteIp1: remoteEd1,
			}
			nflogMap := map[[64]byte]*calc.RuleID{}

			for _, rid := range []*calc.RuleID{defTierPolicy1AllowEgressRuleID, defTierPolicy1AllowIngressRuleID, defTierPolicy2DenyIngressRuleID} {
				nflogMap[policyIDStrToRuleIDParts(rid)] = rid
			}

			lm = newMockLookupsCache(epMap, nflogMap, nil, nil)
			nflogReader = NewNFLogReader(lm, 0, 0, 0, false)
			nflogReader.Start()
			c = newCollector(lm, rm, conf).(*collector)
			c.SetPacketInfoReader(nflogReader)
			c.SetConntrackInfoReader(dummyConntrackInfoReader{})
			go c.Start()
		})
		AfterEach(func() {
			nflogReader.Stop()
		})
		Describe("Test local destination", func() {
			It("should receive a single stat update with allow ruleid trace", func() {
				t := NewTuple(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
				nflogReader.nfIngressC <- ingressPktAllow
				Eventually(c.epStats).Should(HaveKey(*t))
			})
		})
		Describe("Test local to local", func() {
			It("should receive a single stat update with deny ruleid trace", func() {
				t := NewTuple(localIp1, localIp2, proto_tcp, srcPort, dstPort)
				nflogReader.nfIngressC <- localPktIngress
				Eventually(c.epStats).Should(HaveKey(*t))
			})
		})
	})
})

// Entry remoteIp1:srcPort -> localIp1:dstPort
var inCtEntry = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        remoteIp1,
		Dst:        localIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPort},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        remoteIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: srcPort},
	},
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_ESTABLISHED},
}

func convertCtEntry(e nfnetlink.CtEntry) ConntrackInfo {
	i, _ := convertCtEntryToConntrackInfo(e)
	return i
}

var alpEntryHTTPReqAllowed = 12
var alpEntryHTTPReqDenied = 130
var inALPEntry = proto.DataplaneStats{
	SrcIp:   remoteIp1Str,
	DstIp:   localIp1Str,
	SrcPort: int32(srcPort),
	DstPort: int32(dstPort),
	Protocol: &proto.Protocol{
		NumberOrName: &proto.Protocol_Number{proto_tcp},
	},
	Stats: []*proto.Statistic{
		{
			Direction:  proto.Statistic_IN,
			Relativity: proto.Statistic_DELTA,
			Kind:       proto.Statistic_HTTP_REQUESTS,
			Action:     proto.Action_ALLOWED,
			Value:      int64(alpEntryHTTPReqAllowed),
		},
		{
			Direction:  proto.Statistic_IN,
			Relativity: proto.Statistic_DELTA,
			Kind:       proto.Statistic_HTTP_REQUESTS,
			Action:     proto.Action_DENIED,
			Value:      int64(alpEntryHTTPReqDenied),
		},
	},
}

var dpStatsHTTPDataValue = 23
var dpStatsEntryWithFwdFor = proto.DataplaneStats{
	SrcIp:   remoteIp1Str,
	DstIp:   localIp1Str,
	SrcPort: int32(srcPort),
	DstPort: int32(dstPort),
	Protocol: &proto.Protocol{
		NumberOrName: &proto.Protocol_Number{proto_tcp},
	},
	Stats: []*proto.Statistic{
		{
			Direction:  proto.Statistic_IN,
			Relativity: proto.Statistic_DELTA,
			Kind:       proto.Statistic_INGRESS_DATA,
			Action:     proto.Action_ALLOWED,
			Value:      int64(dpStatsHTTPDataValue),
		},
	},
	HttpData: []*proto.HTTPData{
		{
			XForwardedFor: publicIP1Str,
		},
	},
}

var outCtEntry = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        remoteIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPort},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        remoteIp1,
		Dst:        localIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: srcPort},
	},
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_ESTABLISHED},
}

var localCtEntry = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        localIp2,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPort},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        localIp2,
		Dst:        localIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: srcPort},
	},
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_ESTABLISHED},
}

// DNAT Conntrack Entries
// DNAT from localIp1DNAT:dstPortDNAT --> localIp1:dstPort
var inCtEntryWithDNAT = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        remoteIp1,
		Dst:        localIp1DNAT,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPortDNAT},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        remoteIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: srcPort},
	},
	Status:           nfnl.IPS_DST_NAT,
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_ESTABLISHED},
}

// DNAT from localIp2DNAT:dstPortDNAT --> localIp2:dstPort
var localCtEntryWithDNAT = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        localIp2DNAT,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPortDNAT},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        localIp2,
		Dst:        localIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: srcPort},
	},
	Status:           nfnl.IPS_DST_NAT,
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_ESTABLISHED},
}

var _ = Describe("Conntrack Datasource", func() {
	var c *collector
	var ciReaderSenderChan chan []ConntrackInfo
	// var piReaderInfoSenderChan chan PacketInfo
	var lm *calc.LookupsCache
	var epMapDelete map[[16]byte]*calc.EndpointData
	var nflogReader *NFLogReader
	conf := &Config{
		StatsDumpFilePath:            "/tmp/qwerty",
		AgeTimeout:                   time.Duration(10) * time.Second,
		InitialReportingDelay:        time.Duration(5) * time.Second,
		ExportingInterval:            time.Duration(1) * time.Second,
		MaxOriginalSourceIPsIncluded: 5,
	}
	rm := NewReporterManager()
	BeforeEach(func() {
		epMap := map[[16]byte]*calc.EndpointData{
			localIp1:  localEd1,
			localIp2:  localEd2,
			remoteIp1: remoteEd1,
		}
		epMapDelete = map[[16]byte]*calc.EndpointData{
			localIp1:  nil,
			localIp2:  nil,
			remoteIp1: nil,
		}

		nflogMap := map[[64]byte]*calc.RuleID{}

		for _, rid := range []*calc.RuleID{defTierPolicy1AllowEgressRuleID, defTierPolicy1AllowIngressRuleID, defTierPolicy2DenyIngressRuleID} {
			nflogMap[policyIDStrToRuleIDParts(rid)] = rid
		}

		lm = newMockLookupsCache(epMap, nflogMap, nil, nil)
		nflogReader = NewNFLogReader(lm, 0, 0, 0, false)
		c = newCollector(lm, rm, conf).(*collector)

		c.SetPacketInfoReader(nflogReader)

		ciReaderSenderChan = make(chan []ConntrackInfo, 1)
		c.SetConntrackInfoReader(dummyConntrackInfoReader{
			MockSenderChannel: ciReaderSenderChan,
		})

		c.Start()
	})

	Describe("Test local destination", func() {
		It("should create a single entry in inbound direction", func() {
			t := NewTuple(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)

			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntry)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			data := c.epStats[*t]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Bytes)))
		})
		It("should handle destination becoming non-local by removing entry on next update", func() {
			t := NewTuple(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntry)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Flag the data as reported, remove endpoints from mock data and send in CT entry again.
			data := c.epStats[*t]
			data.reported = true
			lm.SetMockData(epMapDelete, nil, nil, nil)
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntry)}
			Eventually(c.epStats, "500ms", "100ms").ShouldNot(HaveKey(*t))
		})
	})
	Describe("Test local source", func() {
		It("should create a single entry with outbound direction", func() {
			t := NewTuple(localIp1, remoteIp1, proto_tcp, srcPort, dstPort)

			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(outCtEntry)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			data := c.epStats[*t]

			Expect(data.ConntrackPacketsCounter()).Should(Equal(*NewCounter(outCtEntry.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*NewCounter(outCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(outCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(outCtEntry.ReplyCounters.Bytes)))
		})
		It("should handle source becoming non-local by removing entry on next update", func() {
			t := NewTuple(localIp1, remoteIp1, proto_tcp, srcPort, dstPort)
			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(outCtEntry)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Flag the data as reported, remove endpoints from mock data and send in CT entry again.
			data := c.epStats[*t]
			data.reported = true
			lm.SetMockData(epMapDelete, nil, nil, nil)
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(outCtEntry)}
			Eventually(c.epStats, "500ms", "100ms").ShouldNot(HaveKey(*t))
		})
	})
	Describe("Test local source to local destination", func() {
		It("should create a single entry with 'local' direction", func() {
			t1 := NewTuple(localIp1, localIp2, proto_tcp, srcPort, dstPort)

			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(localCtEntry)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t1))

			data := c.epStats[*t1]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*NewCounter(localCtEntry.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*NewCounter(localCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(localCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(localCtEntry.ReplyCounters.Bytes)))
		})
	})
	Describe("Test local destination with DNAT", func() {
		It("should create a single entry with inbound connection direction and with correct tuple extracted", func() {
			t := NewTuple(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)

			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntryWithDNAT)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			data := c.epStats[*t]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*NewCounter(inCtEntryWithDNAT.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*NewCounter(inCtEntryWithDNAT.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(inCtEntryWithDNAT.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(inCtEntryWithDNAT.ReplyCounters.Bytes)))
		})
	})
	Describe("Test local source to local destination with DNAT", func() {
		It("should create a single entry with 'local' connection direction and with correct tuple extracted", func() {
			t1 := NewTuple(localIp1, localIp2, proto_tcp, srcPort, dstPort)
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(localCtEntryWithDNAT)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey((Equal(*t1))))
			data := c.epStats[*t1]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*NewCounter(localCtEntryWithDNAT.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*NewCounter(localCtEntryWithDNAT.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(localCtEntryWithDNAT.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(localCtEntryWithDNAT.ReplyCounters.Bytes)))
		})
	})
	Describe("Test conntrack TCP Protoinfo State", func() {
		It("Handle TCP conntrack entries with TCP state TIME_WAIT after NFLOGs gathered", func() {
			By("handling a conntrack update to start tracking stats for tuple")
			t := NewTuple(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntry)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			data := c.epStats[*t]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Bytes)))

			By("handling a conntrack update with updated counters")
			inCtEntryUpdatedCounters := inCtEntry
			inCtEntryUpdatedCounters.OriginalCounters.Packets = inCtEntry.OriginalCounters.Packets + 1
			inCtEntryUpdatedCounters.OriginalCounters.Bytes = inCtEntry.OriginalCounters.Bytes + 10
			inCtEntryUpdatedCounters.ReplyCounters.Packets = inCtEntry.ReplyCounters.Packets + 2
			inCtEntryUpdatedCounters.ReplyCounters.Bytes = inCtEntry.ReplyCounters.Bytes + 50
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntryUpdatedCounters)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			// know update is complete
			Eventually(func() Counter {
				return c.epStats[*t].ConntrackPacketsCounter()
			}, "500ms", "100ms").Should(Equal(*NewCounter(inCtEntryUpdatedCounters.OriginalCounters.Packets)))

			data = c.epStats[*t]
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*NewCounter(inCtEntryUpdatedCounters.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(inCtEntryUpdatedCounters.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(inCtEntryUpdatedCounters.ReplyCounters.Bytes)))

			By("handling a conntrack update with TCP CLOSE_WAIT")
			inCtEntryStateCloseWait := inCtEntryUpdatedCounters
			inCtEntryStateCloseWait.ProtoInfo.State = nfnl.TCP_CONNTRACK_CLOSE_WAIT
			inCtEntryStateCloseWait.ReplyCounters.Packets = inCtEntryUpdatedCounters.ReplyCounters.Packets + 1
			inCtEntryStateCloseWait.ReplyCounters.Bytes = inCtEntryUpdatedCounters.ReplyCounters.Bytes + 10
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntryStateCloseWait)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			// know update is complete
			Eventually(func() Counter {
				return c.epStats[*t].ConntrackPacketsCounterReverse()
			}, "500ms", "100ms").Should(Equal(*NewCounter(inCtEntryStateCloseWait.ReplyCounters.Packets)))

			data = c.epStats[*t]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*NewCounter(inCtEntryStateCloseWait.OriginalCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(inCtEntryStateCloseWait.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(inCtEntryStateCloseWait.ReplyCounters.Bytes)))

			By("handling an nflog update for destination matching on policy - all policy info is now gathered",
				func() {
					pktinfo := nflogReader.convertNflogPkt(rules.RuleDirIngress, ingressPktAllow)
					c.applyPacketInfo(pktinfo)
				},
			)

			By("handling a conntrack update with TCP TIME_WAIT")
			inCtEntryStateTimeWait := inCtEntry
			inCtEntryStateTimeWait.ProtoInfo.State = nfnl.TCP_CONNTRACK_TIME_WAIT
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntryStateTimeWait)}
			Eventually(c.epStats, "500ms", "100ms").ShouldNot(HaveKey(*t))
		})
		It("Handle TCP conntrack entries with TCP state TIME_WAIT before NFLOGs gathered", func() {
			By("handling a conntrack update to start tracking stats for tuple")
			t := NewTuple(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntry)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// know update is complete
			Eventually(func() Counter {
				return c.epStats[*t].ConntrackPacketsCounter()
			}, "500ms", "100ms").Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Packets)))
			data := c.epStats[*t]

			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Bytes)))

			By("handling a conntrack update with updated counters")
			inCtEntryUpdatedCounters := inCtEntry
			inCtEntryUpdatedCounters.OriginalCounters.Packets = inCtEntry.OriginalCounters.Packets + 1
			inCtEntryUpdatedCounters.OriginalCounters.Bytes = inCtEntry.OriginalCounters.Bytes + 10
			inCtEntryUpdatedCounters.ReplyCounters.Packets = inCtEntry.ReplyCounters.Packets + 2
			inCtEntryUpdatedCounters.ReplyCounters.Bytes = inCtEntry.ReplyCounters.Bytes + 50
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntryUpdatedCounters)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// know update is complete
			Eventually(func() Counter {
				return c.epStats[*t].ConntrackPacketsCounter()
			}, "500ms", "100ms").Should(Equal(*NewCounter(inCtEntryUpdatedCounters.OriginalCounters.Packets)))
			data = c.epStats[*t]

			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*NewCounter(inCtEntryUpdatedCounters.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(inCtEntryUpdatedCounters.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(inCtEntryUpdatedCounters.ReplyCounters.Bytes)))

			By("handling a conntrack update with TCP CLOSE_WAIT")
			inCtEntryStateCloseWait := inCtEntryUpdatedCounters
			inCtEntryStateCloseWait.ProtoInfo.State = nfnl.TCP_CONNTRACK_CLOSE_WAIT
			inCtEntryStateCloseWait.ReplyCounters.Packets = inCtEntryUpdatedCounters.ReplyCounters.Packets + 1
			inCtEntryStateCloseWait.ReplyCounters.Bytes = inCtEntryUpdatedCounters.ReplyCounters.Bytes + 10
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntryStateCloseWait)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// know update is complete
			Eventually(func() Counter {
				return c.epStats[*t].ConntrackPacketsCounterReverse()
			}, "500ms", "100ms").Should(Equal(*NewCounter(inCtEntryStateCloseWait.ReplyCounters.Packets)))
			data = c.epStats[*t]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*NewCounter(inCtEntryStateCloseWait.OriginalCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(inCtEntryStateCloseWait.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(inCtEntryStateCloseWait.ReplyCounters.Bytes)))

			By("handling a conntrack update with TCP TIME_WAIT")
			inCtEntryStateTimeWait := inCtEntry
			inCtEntryStateTimeWait.ProtoInfo.State = nfnl.TCP_CONNTRACK_TIME_WAIT
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntryStateTimeWait)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			By("handling an nflog update for destination matching on policy - all policy info is now gathered",
				func() {
					pktinfo := nflogReader.convertNflogPkt(rules.RuleDirIngress, ingressPktAllow)
					c.applyPacketInfo(pktinfo)
				},
			)
			Eventually(c.epStats, "500ms", "100ms").ShouldNot(HaveKey(*t))
		})
	})

	Describe("Test data race", func() {
		It("getDataAndUpdateEndpoints does not cause a data race contention  with deleteDataFromEpStats after deleteDataFromEpStats removes it from epstats", func() {
			existingTuple := NewTuple(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			testData := c.getDataAndUpdateEndpoints(*existingTuple, false)

			newTuple := NewTuple(localIp1, localIp2, proto_tcp, srcPort, dstPort)

			var resultantNewTupleData *Data

			time.AfterFunc(2*time.Second, func() {
				c.deleteDataFromEpStats(testData)
			})

			// ok Get is a little after feedupdate because feedupdate has some preprocesssing
			// before ti accesses flowstore
			time.AfterFunc(2*time.Second+10*time.Millisecond, func() {
				resultantNewTupleData = c.getDataAndUpdateEndpoints(*newTuple, false)
			})

			time.Sleep(3 * time.Second)

			Expect(c.epStats).ShouldNot(HaveKey(*existingTuple))
			Expect(c.epStats).Should(HaveKey(*newTuple))
			Expect(resultantNewTupleData).ToNot(Equal(nil))
		})
	})

	Describe("Test pre-DNAT handling", func() {
		It("handle pre-DNAT info on conntrack", func() {
			By("handling a conntrack update to start tracking stats for tuple (w/ DNAT)")
			t := NewTuple(localIp1, localIp2, proto_tcp, srcPort, dstPort)
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(localCtEntryWithDNAT)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Flagging as expired will attempt to expire the data when NFLOGs and service info are gathered.
			By("flagging the data as expired")
			data := c.epStats[*t]
			data.expired = true
			Expect(data.isDNAT).Should(BeTrue())

			By("handling nflog updates for destination matching on policy - all policy info is now gathered, but no service")
			c.applyPacketInfo(nflogReader.convertNflogPkt(rules.RuleDirIngress, localPktIngress))
			c.applyPacketInfo(nflogReader.convertNflogPkt(rules.RuleDirEgress, localPktEgress))
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			By("creating a matching service for the pre-DNAT cluster IP and port")
			lm.SetMockData(nil, nil, nil, map[model.ResourceKey]*kapiv1.Service{
				{Kind: v3.KindK8sService, Name: "svc", Namespace: "default"}: {Spec: kapiv1.ServiceSpec{
					Ports: []kapiv1.ServicePort{{
						Name:     "test",
						Protocol: kapiv1.ProtocolTCP,
						Port:     int32(dstPortDNAT),
					}},
					ClusterIP: "192.168.0.2",
				},
				},
			})

			By("handling another nflog update for destination matching on policy - should rematch and expire the entry")
			c.applyPacketInfo(nflogReader.convertNflogPkt(rules.RuleDirIngress, localPktIngress))
			Expect(c.epStats).ShouldNot(HaveKey(*t))
		})
		It("handle pre-DNAT info on nflog update", func() {
			By("handling egress nflog updates for destination matching on policy - this contains pre-DNAT info")
			t := NewTuple(localIp1, localIp2, proto_tcp, srcPort, dstPort)
			c.applyPacketInfo(nflogReader.convertNflogPkt(rules.RuleDirIngress, localPktIngressWithDNAT))

			// Flagging as expired will attempt to expire the data when NFLOGs and service info are gathered.
			By("flagging the data as expired")
			data := c.epStats[*t]
			data.expired = true
			Expect(data.isDNAT).Should(BeTrue())

			By("handling ingree nflog updates for destination matching on policy - all policy info is now gathered, but no service")
			c.applyPacketInfo(nflogReader.convertNflogPkt(rules.RuleDirEgress, localPktEgress))
			Expect(c.epStats).Should(HaveKey(*t))

			By("creating a matching service for the pre-DNAT cluster IP and port")
			lm.SetMockData(nil, nil, nil, map[model.ResourceKey]*kapiv1.Service{
				{Kind: v3.KindK8sService, Name: "svc", Namespace: "default"}: {Spec: kapiv1.ServiceSpec{
					Ports: []kapiv1.ServicePort{{
						Name:     "test",
						Protocol: kapiv1.ProtocolTCP,
						Port:     int32(dstPortDNAT),
					}},
					ClusterIP: "192.168.0.2",
				},
				},
			})

			By("handling another nflog update for destination matching on policy - should rematch and expire the entry")
			c.applyPacketInfo(nflogReader.convertNflogPkt(rules.RuleDirIngress, localPktIngress))
			Expect(c.epStats).ShouldNot(HaveKey(*t))
		})
	})
	Describe("Test local destination combined with ALP stats", func() {
		It("should create a single entry in inbound direction", func() {
			By("Sending a conntrack update and a dataplane stats update and checking for combined values")
			t := NewTuple(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			c.convertDataplaneStatsAndApplyUpdate(&inALPEntry)
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntry)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// know update is complete
			Eventually(func() Counter {
				return c.epStats[*t].ConntrackPacketsCounter()
			}, "500ms", "100ms").Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Packets)))

			data := c.epStats[*t]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Bytes)))
			Expect(data.HTTPRequestsAllowed()).Should(Equal(*NewCounter(alpEntryHTTPReqAllowed)))
			Expect(data.HTTPRequestsDenied()).Should(Equal(*NewCounter(alpEntryHTTPReqDenied)))

			By("Sending in another dataplane stats update and check for incremented counter")
			c.convertDataplaneStatsAndApplyUpdate(&inALPEntry)
			Expect(data.HTTPRequestsAllowed()).Should(Equal(*NewCounter(2 * alpEntryHTTPReqAllowed)))
			Expect(data.HTTPRequestsDenied()).Should(Equal(*NewCounter(2 * alpEntryHTTPReqDenied)))
		})
	})
	Describe("Test DataplaneStat with HTTPData", func() {
		It("should process DataplaneStat update with X-Forwarded-For HTTP Data", func() {
			By("Sending a conntrack update and a dataplane stats update and checking for combined values")
			t := NewTuple(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			expectedOrigSourceIPs := []net2.IP{net2.ParseIP(publicIP1Str)}
			c.convertDataplaneStatsAndApplyUpdate(&dpStatsEntryWithFwdFor)
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntry)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// know update is complete
			Eventually(func() Counter {
				return c.epStats[*t].ConntrackPacketsCounterReverse()
			}, "500ms", "100ms").Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Packets)))
			data := c.epStats[*t]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Bytes)))
			Expect(data.OriginalSourceIps()).Should(ConsistOf(expectedOrigSourceIPs))
			Expect(data.NumUniqueOriginalSourceIPs()).Should(Equal(dpStatsHTTPDataValue))

			By("Sending in another dataplane stats update and check for updated tracked data")
			updatedDpStatsEntryWithFwdFor := dpStatsEntryWithFwdFor
			updatedDpStatsEntryWithFwdFor.HttpData = []*proto.HTTPData{
				{
					XForwardedFor: publicIP1Str,
				},
				{
					XForwardedFor: publicIP2Str,
				},
			}
			expectedOrigSourceIPs = []net2.IP{net2.ParseIP(publicIP1Str), net2.ParseIP(publicIP2Str)}
			c.convertDataplaneStatsAndApplyUpdate(&updatedDpStatsEntryWithFwdFor)
			Expect(data.OriginalSourceIps()).Should(ConsistOf(expectedOrigSourceIPs))
			Expect(data.NumUniqueOriginalSourceIPs()).Should(Equal(2*dpStatsHTTPDataValue - 1))

			By("Sending in another dataplane stats update with only counts and check for updated tracked data")
			updatedDpStatsEntryWithOnlyHttpStats := dpStatsEntryWithFwdFor
			updatedDpStatsEntryWithOnlyHttpStats.HttpData = []*proto.HTTPData{}
			c.convertDataplaneStatsAndApplyUpdate(&updatedDpStatsEntryWithOnlyHttpStats)
			Expect(data.OriginalSourceIps()).Should(ConsistOf(expectedOrigSourceIPs))
			Expect(data.NumUniqueOriginalSourceIPs()).Should(Equal(3*dpStatsHTTPDataValue - 1))
		})
		It("should process DataplaneStat update with X-Real-IP HTTP Data", func() {
			By("Sending a conntrack update and a dataplane stats update and checking for combined values")
			t := NewTuple(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			expectedOrigSourceIPs := []net2.IP{net2.ParseIP(publicIP1Str)}
			dpStatsEntryWithRealIP := dpStatsEntryWithFwdFor
			dpStatsEntryWithRealIP.HttpData = []*proto.HTTPData{
				{
					XRealIp: publicIP1Str,
				},
			}
			c.convertDataplaneStatsAndApplyUpdate(&dpStatsEntryWithRealIP)
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntry)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			// know update is complete
			Eventually(func() Counter {
				return c.epStats[*t].ConntrackPacketsCounter()
			}, "500ms", "100ms").Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Packets)))
			data := c.epStats[*t]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Bytes)))
			Expect(data.OriginalSourceIps()).Should(ConsistOf(expectedOrigSourceIPs))
			Expect(data.NumUniqueOriginalSourceIPs()).Should(Equal(dpStatsHTTPDataValue))

			By("Sending a dataplane stats update with x-real-ip and check for updated tracked data")
			updatedDpStatsEntryWithRealIP := dpStatsEntryWithRealIP
			updatedDpStatsEntryWithRealIP.HttpData = []*proto.HTTPData{
				{
					XRealIp: publicIP1Str,
				},
				{
					XRealIp: publicIP2Str,
				},
			}
			expectedOrigSourceIPs = []net2.IP{net2.ParseIP(publicIP1Str), net2.ParseIP(publicIP2Str)}
			c.convertDataplaneStatsAndApplyUpdate(&updatedDpStatsEntryWithRealIP)
			Expect(data.OriginalSourceIps()).Should(ConsistOf(expectedOrigSourceIPs))
			Expect(data.NumUniqueOriginalSourceIPs()).Should(Equal(2*dpStatsHTTPDataValue - 1))

			By("Sending in another dataplane stats update with only counts and check for updated tracked data")
			updatedDpStatsEntryWithOnlyHttpStats := dpStatsEntryWithRealIP
			updatedDpStatsEntryWithOnlyHttpStats.HttpData = []*proto.HTTPData{}
			c.convertDataplaneStatsAndApplyUpdate(&updatedDpStatsEntryWithOnlyHttpStats)
			Expect(data.OriginalSourceIps()).Should(ConsistOf(expectedOrigSourceIPs))
			Expect(data.NumUniqueOriginalSourceIPs()).Should(Equal(3*dpStatsHTTPDataValue - 1))
		})
		It("should process DataplaneStat update with X-Real-IP and X-Forwarded-For HTTP Data", func() {
			By("Sending a conntrack update and a dataplane stats update and checking for combined values")
			t := NewTuple(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			expectedOrigSourceIPs := []net2.IP{net2.ParseIP(publicIP1Str)}
			c.convertDataplaneStatsAndApplyUpdate(&dpStatsEntryWithFwdFor)
			ciReaderSenderChan <- []ConntrackInfo{convertCtEntry(inCtEntry)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			// know update is complete
			Eventually(func() Counter {
				return c.epStats[*t].ConntrackPacketsCounter()
			}, "500ms", "100ms").Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Packets)))
			data := c.epStats[*t]
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*NewCounter(inCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*NewCounter(inCtEntry.ReplyCounters.Bytes)))
			Expect(data.OriginalSourceIps()).Should(ConsistOf(expectedOrigSourceIPs))
			Expect(data.NumUniqueOriginalSourceIPs()).Should(Equal(dpStatsHTTPDataValue))

			By("Sending in another dataplane stats update and check for updated tracked data")
			updatedDpStatsEntryWithFwdForAndRealIP := dpStatsEntryWithFwdFor
			updatedDpStatsEntryWithFwdForAndRealIP.HttpData = []*proto.HTTPData{
				{
					XForwardedFor: publicIP1Str,
					XRealIp:       publicIP1Str,
				},
				{
					XRealIp: publicIP2Str,
				},
			}
			expectedOrigSourceIPs = []net2.IP{net2.ParseIP(publicIP1Str), net2.ParseIP(publicIP2Str)}
			c.convertDataplaneStatsAndApplyUpdate(&updatedDpStatsEntryWithFwdForAndRealIP)
			Expect(data.OriginalSourceIps()).Should(ConsistOf(expectedOrigSourceIPs))
			// We subtract 1 because the second update contains an overlapping IP that is accounted for.
			Expect(data.NumUniqueOriginalSourceIPs()).Should(Equal(2*dpStatsHTTPDataValue - 1))
		})
	})
})

func policyIDStrToRuleIDParts(r *calc.RuleID) [64]byte {
	var (
		name  string
		byt64 [64]byte
	)

	if r.Namespace != "" {
		if strings.HasPrefix(r.Name, "knp.default.") {
			name = fmt.Sprintf("%s/%s", r.Namespace, r.Name)
		} else {
			name = fmt.Sprintf("%s/%s.%s", r.Namespace, r.Tier, r.Name)
		}
	} else {
		name = fmt.Sprintf("%s.%s", r.Tier, r.Name)
	}

	prefix := rules.CalculateNFLOGPrefixStr(r.Action, rules.RuleOwnerTypePolicy, r.Direction, r.Index, name)
	copy(byt64[:], []byte(prefix))
	return byt64
}

var _ = Describe("Reporting Metrics", func() {
	var c *collector
	var nflogReader *NFLogReader
	const (
		ageTimeout        = time.Duration(3) * time.Second
		reportingDelay    = time.Duration(2) * time.Second
		exportingInterval = time.Duration(1) * time.Second
	)
	conf := &Config{
		StatsDumpFilePath:            "/tmp/qwerty",
		AgeTimeout:                   ageTimeout,
		InitialReportingDelay:        reportingDelay,
		ExportingInterval:            exportingInterval,
		MaxOriginalSourceIPsIncluded: 5,
	}
	rm := NewReporterManager()
	mockReporter := newMockReporter()
	rm.RegisterMetricsReporter(mockReporter)
	BeforeEach(func() {
		epMap := map[[16]byte]*calc.EndpointData{
			localIp1:  localEd1,
			localIp2:  localEd2,
			remoteIp1: remoteEd1,
		}

		nflogMap := map[[64]byte]*calc.RuleID{}

		for _, rid := range []*calc.RuleID{defTierPolicy1AllowEgressRuleID, defTierPolicy1AllowIngressRuleID, defTierPolicy2DenyIngressRuleID} {
			nflogMap[policyIDStrToRuleIDParts(rid)] = rid
		}

		lm := newMockLookupsCache(epMap, nflogMap, nil, nil)
		rm.Start()
		nflogReader = NewNFLogReader(lm, 0, 0, 0, false)
		nflogReader.Start()
		c = newCollector(lm, rm, conf).(*collector)
		c.SetPacketInfoReader(nflogReader)
		c.SetConntrackInfoReader(dummyConntrackInfoReader{})
	})
	AfterEach(func() {
		nflogReader.Stop()
	})
	Context("Without process info enabled", func() {
		BeforeEach(func() {
			go c.Start()
		})
		Describe("Report Denied Packets", func() {
			BeforeEach(func() {
				nflogReader.nfIngressC <- ingressPktDeny
			})
			Context("reporting tick", func() {
				It("should receive metric", func() {
					tmu := testMetricUpdate{
						updateType:   UpdateTypeReport,
						tuple:        *ingressPktDenyTuple,
						srcEp:        remoteEd1,
						dstEp:        localEd1,
						ruleIDs:      []*calc.RuleID{defTierPolicy2DenyIngressRuleID},
						isConnection: false,
					}
					Eventually(mockReporter.reportChan, reportingDelay*2).Should(Receive(Equal(tmu)))
				})
			})
		})
		Describe("Report Allowed Packets (ingress)", func() {
			BeforeEach(func() {
				nflogReader.nfIngressC <- ingressPktAllow
			})
			Context("reporting tick", func() {
				It("should receive metric", func() {
					tmu := testMetricUpdate{
						updateType:   UpdateTypeReport,
						tuple:        *ingressPktAllowTuple,
						srcEp:        remoteEd1,
						dstEp:        localEd1,
						ruleIDs:      []*calc.RuleID{defTierPolicy1AllowIngressRuleID},
						isConnection: false,
					}
					Eventually(mockReporter.reportChan, reportingDelay*2).Should(Receive(Equal(tmu)))
				})
			})
		})
		Describe("Report Packets that switch from deny to allow", func() {
			BeforeEach(func() {
				nflogReader.nfIngressC <- ingressPktDeny
				time.Sleep(time.Duration(500) * time.Millisecond)
				nflogReader.nfIngressC <- ingressPktAllow
			})
			Context("reporting tick", func() {
				It("should receive metric", func() {
					tmu := testMetricUpdate{
						updateType:   UpdateTypeReport,
						tuple:        *ingressPktAllowTuple,
						srcEp:        remoteEd1,
						dstEp:        localEd1,
						ruleIDs:      []*calc.RuleID{defTierPolicy1AllowIngressRuleID},
						isConnection: false,
					}
					Eventually(mockReporter.reportChan, reportingDelay*2).Should(Receive(Equal(tmu)))
				})
			})
		})
		Describe("Report Allowed Packets (egress)", func() {
			BeforeEach(func() {
				nflogReader.nfEgressC <- egressPktAllow
			})
			Context("reporting tick", func() {
				It("should receive metric", func() {
					tmu := testMetricUpdate{
						updateType:   UpdateTypeReport,
						tuple:        *egressPktAllowTuple,
						srcEp:        localEd1,
						dstEp:        remoteEd1,
						ruleIDs:      []*calc.RuleID{defTierPolicy1AllowEgressRuleID},
						isConnection: false,
					}
					Eventually(mockReporter.reportChan, reportingDelay*2).Should(Receive(Equal(tmu)))
				})
			})
		})
		Context("With HTTP Data", func() {
			Describe("Report Allowed Packets (ingress)", func() {
				It("should receive metric", func() {
					By("Sending a NFLOG packet update")
					nflogReader.nfIngressC <- ingressPktAllow
					tmuIngress := testMetricUpdate{
						updateType:   UpdateTypeReport,
						tuple:        *ingressPktAllowTuple,
						srcEp:        remoteEd1,
						dstEp:        localEd1,
						ruleIDs:      []*calc.RuleID{defTierPolicy1AllowIngressRuleID},
						isConnection: false,
					}
					Eventually(mockReporter.reportChan, reportingDelay*2).Should(Receive(Equal(tmuIngress)))
					By("Sending a dataplane stats update with HTTP Data")
					c.ds <- &dpStatsEntryWithFwdFor
					tmuOrigIP := testMetricUpdate{
						updateType:    UpdateTypeReport,
						tuple:         *ingressPktAllowTuple,
						srcEp:         remoteEd1,
						dstEp:         localEd1,
						ruleIDs:       []*calc.RuleID{defTierPolicy1AllowIngressRuleID},
						origSourceIPs: NewBoundedSetFromSliceWithTotalCount(c.config.MaxOriginalSourceIPsIncluded, []net2.IP{net2.ParseIP(publicIP1Str)}, dpStatsHTTPDataValue),
						isConnection:  true,
					}
					Eventually(mockReporter.reportChan, reportingDelay*2).Should(Receive(Equal(tmuOrigIP)))
				})
			})
			Describe("Report HTTP Data only", func() {
				unknownRuleID := calc.NewRuleID(calc.UnknownStr, calc.UnknownStr, calc.UnknownStr, calc.RuleIDIndexUnknown, rules.RuleDirIngress, rules.RuleActionAllow)
				It("should receive metric", func() {
					By("Sending a dataplane stats update with HTTP Data")
					c.ds <- &dpStatsEntryWithFwdFor
					tmuOrigIP := testMetricUpdate{
						updateType:    UpdateTypeReport,
						tuple:         *ingressPktAllowTuple,
						srcEp:         remoteEd1,
						dstEp:         localEd1,
						ruleIDs:       nil,
						origSourceIPs: NewBoundedSetFromSliceWithTotalCount(c.config.MaxOriginalSourceIPsIncluded, []net2.IP{net2.ParseIP(publicIP1Str)}, dpStatsHTTPDataValue),
						unknownRuleID: unknownRuleID,
						isConnection:  true,
					}
					Eventually(mockReporter.reportChan, reportingDelay*2).Should(Receive(Equal(tmuOrigIP)))
				})
			})
		})
	})
	Context("With process info enabled", func() {
		BeforeEach(func() {
			c.SetProcessInfoCache(mockProcessCache{})
			go c.Start()
		})
		Describe("Report Allowed Packets (ingress) with process info", func() {
			BeforeEach(func() {
				nflogReader.nfIngressC <- ingressPktAllow
			})
			Context("reporting tick", func() {
				It("should receive metric", func() {
					tmu := testMetricUpdate{
						updateType:   UpdateTypeReport,
						tuple:        *ingressPktAllowTuple,
						srcEp:        remoteEd1,
						dstEp:        localEd1,
						ruleIDs:      []*calc.RuleID{defTierPolicy1AllowIngressRuleID},
						isConnection: false,
						processName:  "test-process",
						processID:    1234,
					}
					Eventually(mockReporter.reportChan, reportingDelay*2).Should(Receive(Equal(tmu)))
				})
			})
		})
	})
})

type mockDNSReporter struct {
	updates []DNSUpdate
}

func (c *mockDNSReporter) Start() {}

func (c *mockDNSReporter) Log(update DNSUpdate) error {
	c.updates = append(c.updates, update)
	return nil
}

var _ = Describe("DNS logging", func() {
	var c *collector
	var nflogReader *NFLogReader
	var r *mockDNSReporter
	BeforeEach(func() {
		epMap := map[[16]byte]*calc.EndpointData{
			localIp1:  localEd1,
			localIp2:  localEd2,
			remoteIp1: remoteEd1,
		}
		nflogMap := map[[64]byte]*calc.RuleID{}
		lm := newMockLookupsCache(epMap, nflogMap, map[model.NetworkSetKey]*model.NetworkSet{netSetKey1: &netSet1}, nil)
		nflogReader = NewNFLogReader(lm, 0, 0, 0, false)
		c = newCollector(lm, nil, &Config{
			AgeTimeout:            time.Duration(10) * time.Second,
			InitialReportingDelay: time.Duration(5) * time.Second,
			ExportingInterval:     time.Duration(1) * time.Second,
		}).(*collector)
		c.SetPacketInfoReader(nflogReader)
		c.SetConntrackInfoReader(dummyConntrackInfoReader{})
		r = &mockDNSReporter{}
		c.SetDNSLogReporter(r)
	})
	It("should get client and server endpoint data", func() {
		c.LogDNS(net2.ParseIP(netSetIp1Str), net2.ParseIP(localIp1Str), nil, nil)
		Expect(r.updates).To(HaveLen(1))
		update := r.updates[0]
		Expect(update.ClientEP).NotTo(BeNil())
		Expect(update.ClientEP.Endpoint).To(BeAssignableToTypeOf(&model.WorkloadEndpoint{}))
		Expect(*(update.ClientEP.Endpoint.(*model.WorkloadEndpoint))).To(Equal(*localWlEp1))
		Expect(update.ServerEP).NotTo(BeNil())
		Expect(update.ServerEP.Networkset).To(BeAssignableToTypeOf(&model.NetworkSet{}))
		Expect(*(update.ServerEP.Networkset.(*model.NetworkSet))).To(Equal(netSet1))
	})
})

func newMockLookupsCache(
	em map[[16]byte]*calc.EndpointData,
	nm map[[64]byte]*calc.RuleID,
	ns map[model.NetworkSetKey]*model.NetworkSet,
	svcs map[model.ResourceKey]*kapiv1.Service,
) *calc.LookupsCache {
	l := calc.NewLookupsCache()
	l.SetMockData(em, nm, ns, svcs)
	return l
}

type mockL7Reporter struct {
	updates []L7Update
}

func (c *mockL7Reporter) Start() {}

func (c *mockL7Reporter) Log(update L7Update) error {
	c.updates = append(c.updates, update)
	return nil
}

var _ = Describe("L7 logging", func() {
	var c *collector
	var r *mockL7Reporter
	var hd *proto.HTTPData
	var d *Data
	var t Tuple
	var hdsvc *proto.HTTPData
	var hdsvcip *proto.HTTPData
	var hdsvcnoport *proto.HTTPData
	BeforeEach(func() {
		epMap := map[[16]byte]*calc.EndpointData{
			localIp1:  localEd1,
			localIp2:  localEd2,
			remoteIp1: remoteEd1,
		}
		nflogMap := map[[64]byte]*calc.RuleID{}
		nsMap := map[model.NetworkSetKey]*model.NetworkSet{netSetKey1: &netSet1}
		svcMap := map[model.ResourceKey]*kapiv1.Service{svcKey1: &svc1}
		lm := newMockLookupsCache(epMap, nflogMap, nsMap, svcMap)
		c = newCollector(lm, nil, &Config{
			AgeTimeout:            time.Duration(10) * time.Second,
			InitialReportingDelay: time.Duration(5) * time.Second,
			ExportingInterval:     time.Duration(1) * time.Second,
		}).(*collector)
		r = &mockL7Reporter{}
		c.SetL7LogReporter(r)
		hd = &proto.HTTPData{
			Duration:      int32(10),
			ResponseCode:  int32(200),
			BytesSent:     int32(40),
			BytesReceived: int32(60),
			UserAgent:     "firefox",
			RequestPath:   "/test/path",
			RequestMethod: "GET",
			Type:          "http/1.1",
			Count:         int32(1),
			Domain:        "www.test.com",
			DurationMax:   int32(12),
		}

		hdsvc = &proto.HTTPData{
			Duration:      int32(10),
			ResponseCode:  int32(200),
			BytesSent:     int32(40),
			BytesReceived: int32(60),
			UserAgent:     "firefox",
			RequestPath:   "/test/path",
			RequestMethod: "GET",
			Type:          "http/1.1",
			Count:         int32(1),
			Domain:        "test-svc.test-namespace.svc.cluster.local:80",
			DurationMax:   int32(12),
		}

		hdsvcip = &proto.HTTPData{
			Duration:      int32(10),
			ResponseCode:  int32(200),
			BytesSent:     int32(40),
			BytesReceived: int32(60),
			UserAgent:     "firefox",
			RequestPath:   "/test/path",
			RequestMethod: "GET",
			Type:          "http/1.1",
			Count:         int32(1),
			Domain:        "10.10.10.10:80",
			DurationMax:   int32(12),
		}

		hdsvcnoport = &proto.HTTPData{
			Duration:      int32(10),
			ResponseCode:  int32(200),
			BytesSent:     int32(40),
			BytesReceived: int32(60),
			UserAgent:     "firefox",
			RequestPath:   "/test/path",
			RequestMethod: "GET",
			Type:          "http/1.1",
			Count:         int32(1),
			Domain:        "test-svc.test-namespace.svc.cluster.local",
			DurationMax:   int32(12),
		}

		t = *NewTuple(remoteIp1, remoteIp2, proto_tcp, srcPort, dstPort)
		d = NewData(t, remoteEd1, remoteEd2, 0)
		d.dstSvc = proxy.ServicePortName{
			Port: "test-port",
			NamespacedName: types.NamespacedName{
				Name:      svcKey1.Name,
				Namespace: svcKey1.Namespace,
			},
		}
	})

	It("should get client and server endpoint data", func() {
		c.LogL7(hd, d, t, 1)
		Expect(r.updates).To(HaveLen(1))
		update := r.updates[0]
		Expect(update.Tuple).To(Equal(t))
		Expect(update.SrcEp).NotTo(BeNil())
		Expect(update.SrcEp).To(Equal(remoteEd1))
		Expect(update.DstEp).NotTo(BeNil())
		Expect(update.DstEp).To(Equal(remoteEd2))
		Expect(update.Duration).To(Equal(10))
		Expect(update.DurationMax).To(Equal(12))
		Expect(update.BytesReceived).To(Equal(60))
		Expect(update.BytesSent).To(Equal(40))
		Expect(update.ResponseCode).To(Equal("200"))
		Expect(update.Method).To(Equal("GET"))
		Expect(update.Path).To(Equal("/test/path"))
		Expect(update.UserAgent).To(Equal("firefox"))
		Expect(update.Type).To(Equal("http/1.1"))
		Expect(update.Count).To(Equal(1))
		Expect(update.Domain).To(Equal("www.test.com"))
		Expect(update.ServiceName).To(Equal(""))
		Expect(update.ServiceNamespace).To(Equal(""))
		Expect(update.ServicePortName).To(Equal(""))
		Expect(update.ServicePortNum).To(Equal(0))
	})

	It("should properly return kubernetes service names", func() {
		c.LogL7(hdsvc, d, t, 1)
		Expect(r.updates).To(HaveLen(1))
		update := r.updates[0]
		Expect(update.Tuple).To(Equal(t))
		Expect(update.SrcEp).NotTo(BeNil())
		Expect(update.SrcEp).To(Equal(remoteEd1))
		Expect(update.DstEp).NotTo(BeNil())
		Expect(update.DstEp).To(Equal(remoteEd2))
		Expect(update.Duration).To(Equal(10))
		Expect(update.DurationMax).To(Equal(12))
		Expect(update.BytesReceived).To(Equal(60))
		Expect(update.BytesSent).To(Equal(40))
		Expect(update.ResponseCode).To(Equal("200"))
		Expect(update.Method).To(Equal("GET"))
		Expect(update.Path).To(Equal("/test/path"))
		Expect(update.UserAgent).To(Equal("firefox"))
		Expect(update.Type).To(Equal("http/1.1"))
		Expect(update.Count).To(Equal(1))
		Expect(update.Domain).To(Equal("test-svc.test-namespace.svc.cluster.local:80"))
		Expect(update.ServiceName).To(Equal("test-svc"))
		Expect(update.ServiceNamespace).To(Equal("test-namespace"))
		// from service cache
		Expect(update.ServicePortName).To(Equal("nginx"))
		Expect(update.ServicePortNum).To(Equal(80))
	})

	It("should properly look up service names by cluster IP and store update based on the value stored in ServiceCache", func() {
		c.LogL7(hdsvcip, d, t, 1)
		Expect(r.updates).To(HaveLen(1))
		update := r.updates[0]
		Expect(update.Tuple).To(Equal(t))
		Expect(update.SrcEp).NotTo(BeNil())
		Expect(update.SrcEp).To(Equal(remoteEd1))
		Expect(update.DstEp).NotTo(BeNil())
		Expect(update.DstEp).To(Equal(remoteEd2))
		Expect(update.Duration).To(Equal(10))
		Expect(update.DurationMax).To(Equal(12))
		Expect(update.BytesReceived).To(Equal(60))
		Expect(update.BytesSent).To(Equal(40))
		Expect(update.ResponseCode).To(Equal("200"))
		Expect(update.Method).To(Equal("GET"))
		Expect(update.Path).To(Equal("/test/path"))
		Expect(update.UserAgent).To(Equal("firefox"))
		Expect(update.Type).To(Equal("http/1.1"))
		Expect(update.Count).To(Equal(1))
		Expect(update.Domain).To(Equal("10.10.10.10:80"))
		Expect(update.ServiceName).To(Equal("test-svc"))
		Expect(update.ServiceNamespace).To(Equal("test-namespace"))
		// from service cache
		Expect(update.ServicePortName).To(Equal("nginx"))
		Expect(update.ServicePortNum).To(Equal(80))
	})

	It("should properly return kubernetes service names and fill out the protocol default port when not specified", func() {
		c.LogL7(hdsvcnoport, d, t, 1)
		Expect(r.updates).To(HaveLen(1))
		update := r.updates[0]
		Expect(update.Tuple).To(Equal(t))
		Expect(update.SrcEp).NotTo(BeNil())
		Expect(update.SrcEp).To(Equal(remoteEd1))
		Expect(update.DstEp).NotTo(BeNil())
		Expect(update.DstEp).To(Equal(remoteEd2))
		Expect(update.Duration).To(Equal(10))
		Expect(update.DurationMax).To(Equal(12))
		Expect(update.BytesReceived).To(Equal(60))
		Expect(update.BytesSent).To(Equal(40))
		Expect(update.ResponseCode).To(Equal("200"))
		Expect(update.Method).To(Equal("GET"))
		Expect(update.Path).To(Equal("/test/path"))
		Expect(update.UserAgent).To(Equal("firefox"))
		Expect(update.Type).To(Equal("http/1.1"))
		Expect(update.Count).To(Equal(1))
		Expect(update.Domain).To(Equal("test-svc.test-namespace.svc.cluster.local"))
		Expect(update.ServiceName).To(Equal("test-svc"))
		Expect(update.ServiceNamespace).To(Equal("test-namespace"))
		// from service cache
		Expect(update.ServicePortName).To(Equal("nginx"))
		Expect(update.ServicePortNum).To(Equal(80))
	})

	It("should handle empty HTTP data (overflow logs)", func() {
		emptyHD := &proto.HTTPData{}
		c.LogL7(emptyHD, d, t, 100)
		Expect(r.updates).To(HaveLen(1))
		update := r.updates[0]
		Expect(update.Tuple).To(Equal(t))
		Expect(update.SrcEp).NotTo(BeNil())
		Expect(update.SrcEp).To(Equal(remoteEd1))
		Expect(update.DstEp).NotTo(BeNil())
		Expect(update.DstEp).To(Equal(remoteEd2))
		Expect(update.Duration).To(Equal(0))
		Expect(update.DurationMax).To(Equal(0))
		Expect(update.BytesReceived).To(Equal(0))
		Expect(update.BytesSent).To(Equal(0))
		Expect(update.ResponseCode).To(Equal(""))
		Expect(update.Method).To(Equal(""))
		Expect(update.Path).To(Equal(""))
		Expect(update.UserAgent).To(Equal(""))
		Expect(update.Type).To(Equal(""))
		Expect(update.Count).To(Equal(100))
		Expect(update.Domain).To(Equal(""))
		Expect(update.ServiceName).To(Equal(""))
		Expect(update.ServiceNamespace).To(Equal(""))
		Expect(update.ServicePortNum).To(Equal(0))
	})
})

// Define a separate metric type that doesn't include the actual stats.  We use this
// for simpler comparisons.
type testMetricUpdate struct {
	updateType UpdateType

	// Tuple key
	tuple Tuple

	origSourceIPs *boundedSet

	// Endpoint information.
	srcEp *calc.EndpointData
	dstEp *calc.EndpointData

	// Rules identification
	ruleIDs []*calc.RuleID

	// Sometimes we may need to send updates without having all the rules
	// in place. This field will help aggregators determine if they need
	// to handle this update or not. Typically this is used when we receive
	// HTTP Data updates after the connection itself has closed.
	unknownRuleID *calc.RuleID

	// isConnection is true if this update is from an active connection (i.e. a conntrack
	// update compared to an NFLOG update).
	isConnection bool

	// Process information
	processName string
	processID   int
}

// Create a mockReporter that acts as a pass-thru of the updates.
type mockReporter struct {
	reportChan chan testMetricUpdate
}

func newMockReporter() *mockReporter {
	return &mockReporter{
		reportChan: make(chan testMetricUpdate),
	}
}

func (mr *mockReporter) Start() {
	// Do nothing. We are a mock anyway.
}

func (mr *mockReporter) Report(mu MetricUpdate) error {
	mr.reportChan <- testMetricUpdate{
		updateType:    UpdateTypeReport,
		tuple:         mu.tuple,
		srcEp:         mu.srcEp,
		dstEp:         mu.dstEp,
		ruleIDs:       mu.ruleIDs,
		unknownRuleID: mu.unknownRuleID,
		origSourceIPs: mu.origSourceIPs,
		isConnection:  mu.isConnection,
		processName:   mu.processName,
		processID:     mu.processID,
	}
	return nil
}

func mustParseIP(s string) net.IP {
	ip := net2.ParseIP(s)
	return net.IP{ip}
}

func mustParseMac(m string) *net.MAC {
	hwAddr, err := net2.ParseMAC(m)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse MAC: %v; %v", m, err))
	}
	return &net.MAC{hwAddr}
}

func mustParseNet(n string) net.IPNet {
	_, cidr, err := net.ParseCIDR(n)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse CIDR %v; %v", n, err))
	}
	return *cidr
}

func BenchmarkNflogPktToStat(b *testing.B) {
	epMap := map[[16]byte]*calc.EndpointData{
		localIp1:  localEd1,
		localIp2:  localEd2,
		remoteIp1: remoteEd1,
	}

	nflogMap := map[[64]byte]*calc.RuleID{}

	for _, rid := range []*calc.RuleID{defTierPolicy1AllowEgressRuleID, defTierPolicy1AllowIngressRuleID, defTierPolicy2DenyIngressRuleID} {
		nflogMap[policyIDStrToRuleIDParts(rid)] = rid
	}

	conf := &Config{
		StatsDumpFilePath:            "/tmp/qwerty",
		AgeTimeout:                   time.Duration(10) * time.Second,
		InitialReportingDelay:        time.Duration(5) * time.Second,
		ExportingInterval:            time.Duration(1) * time.Second,
		MaxOriginalSourceIPsIncluded: 5,
	}
	rm := NewReporterManager()
	lm := newMockLookupsCache(epMap, nflogMap, nil, nil)
	nflogReader := NewNFLogReader(lm, 0, 0, 0, false)
	c := newCollector(lm, rm, conf).(*collector)
	c.SetPacketInfoReader(nflogReader)
	c.SetConntrackInfoReader(dummyConntrackInfoReader{})
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		pktinfo := nflogReader.convertNflogPkt(rules.RuleDirIngress, ingressPktAllow)
		c.applyPacketInfo(pktinfo)
	}
}

func BenchmarkApplyStatUpdate(b *testing.B) {
	epMap := map[[16]byte]*calc.EndpointData{
		localIp1:  localEd1,
		localIp2:  localEd2,
		remoteIp1: remoteEd1,
	}

	nflogMap := map[[64]byte]*calc.RuleID{}
	for _, rid := range []*calc.RuleID{defTierPolicy1AllowEgressRuleID, defTierPolicy1AllowIngressRuleID, defTierPolicy2DenyIngressRuleID} {
		nflogMap[policyIDStrToRuleIDParts(rid)] = rid
	}

	conf := &Config{
		StatsDumpFilePath:            "/tmp/qwerty",
		AgeTimeout:                   time.Duration(10) * time.Second,
		InitialReportingDelay:        time.Duration(5) * time.Second,
		ExportingInterval:            time.Duration(1) * time.Second,
		MaxOriginalSourceIPsIncluded: 5,
	}
	rm := NewReporterManager()
	lm := newMockLookupsCache(epMap, nflogMap, nil, nil)
	nflogReader := NewNFLogReader(lm, 0, 0, 0, false)
	c := newCollector(lm, rm, conf).(*collector)
	c.SetPacketInfoReader(nflogReader)
	c.SetConntrackInfoReader(dummyConntrackInfoReader{})
	var tuples []Tuple
	MaxSrcPort := 1000
	MaxDstPort := 1000
	for sp := 1; sp < MaxSrcPort; sp++ {
		for dp := 1; dp < MaxDstPort; dp++ {
			t := NewTuple(localIp1, localIp2, proto_tcp, sp, dp)
			tuples = append(tuples, *t)
		}
	}
	var rids []*calc.RuleID
	MaxEntries := 10000
	for i := 0; i < MaxEntries; i++ {
		rid := defTierPolicy1AllowIngressRuleID
		rids = append(rids, rid)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		for i := 0; i < MaxEntries; i++ {
			data := NewData(tuples[i], localEd1, remoteEd1, 100)
			c.applyNflogStatUpdate(data, rids[i], 0, 1, 2)
		}
	}
}

type dummyConntrackInfoReader struct {
	MockSenderChannel chan []ConntrackInfo
}

func (d dummyConntrackInfoReader) Start() error { return nil }
func (d dummyConntrackInfoReader) ConntrackInfoChan() <-chan []ConntrackInfo {
	return d.MockSenderChannel
}

type mockProcessCache struct{}

func (mockProcessCache) Start() error { return nil }
func (mockProcessCache) Stop()        {}
func (mockProcessCache) Lookup(tuple Tuple, dir TrafficDirection) (ProcessInfo, bool) {
	if dir == TrafficDirInbound {
		return proc1, true
	}
	return ProcessInfo{}, false
}
func (mockProcessCache) Update(tuple Tuple, dirty bool) {}
