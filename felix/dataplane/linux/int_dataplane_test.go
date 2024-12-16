// Copyright (c) 2016-2024 Tigera, Inc. All rights reserved.

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

package intdataplane_test

import (
	"net"
	"regexp"
	"time"

	"github.com/google/gopacket/layers"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/projectcalico/calico/felix/capture"
	"github.com/projectcalico/calico/felix/collector"
	"github.com/projectcalico/calico/felix/collector/types"
	"github.com/projectcalico/calico/felix/collector/types/tuple"
	"github.com/projectcalico/calico/felix/config"
	dpipsets "github.com/projectcalico/calico/felix/dataplane/ipsets"
	intdataplane "github.com/projectcalico/calico/felix/dataplane/linux"
	"github.com/projectcalico/calico/felix/idalloc"
	"github.com/projectcalico/calico/felix/ifacemonitor"
	"github.com/projectcalico/calico/felix/ipsets"
	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/felix/rules"
	"github.com/projectcalico/calico/felix/wireguard"
	"github.com/projectcalico/calico/libcalico-go/lib/health"
)

type mockCollector struct{}

func (_ *mockCollector) ReportingChannel() chan<- *proto.DataplaneStats { return nil }

func (_ *mockCollector) Start() error { return nil }

func (_ *mockCollector) LogDNS(net.IP, net.IP, *layers.DNS, *time.Duration) {}

func (_ *mockCollector) SetDNSLogReporter(types.Reporter) {}

func (_ *mockCollector) LogL7(*proto.HTTPData, *collector.Data, tuple.Tuple, int) {
}

func (_ *mockCollector) SetL7LogReporter(types.Reporter) {}

func (_ *mockCollector) RegisterMetricsReporter(types.Reporter) {}

func (_ *mockCollector) SetDataplaneInfoReader(collector.DataplaneInfoReader) {}

func (_ *mockCollector) SetPacketInfoReader(collector.PacketInfoReader) {}

func (_ *mockCollector) SetConntrackInfoReader(collector.ConntrackInfoReader) {}

func (_ *mockCollector) SetProcessInfoCache(collector.ProcessInfoCache) {}

func (_ *mockCollector) SetDomainLookup(collector.EgressDomainCache) {}

func (_ *mockCollector) AddNewDomainDataplaneToIpSetsManager(ipsets.IPFamily, *dpipsets.IPSetsManager) {
}

var _ = Describe("Constructor test", func() {
	var configParams *config.Config
	var dpConfig intdataplane.Config
	var healthAggregator *health.HealthAggregator
	var col collector.Collector
	kubernetesProvider := config.ProviderNone
	routeSource := "CalicoIPAM"
	var wireguardEncryptHostTraffic bool

	JustBeforeEach(func() {
		configParams = config.New()
		_, err := configParams.UpdateFrom(map[string]string{"InterfaceExclude": "/^kube.*/,/veth/,eth2"}, config.EnvironmentVariable)
		Expect(err).NotTo(HaveOccurred())
		dpConfig = intdataplane.Config{
			KubeClientSet:      fake.NewSimpleClientset(),
			FloatingIPsEnabled: true,
			IfaceMonitorConfig: ifacemonitor.Config{
				InterfaceExcludes: configParams.InterfaceExclude,
				ResyncInterval:    configParams.RouteRefreshInterval,
			},
			RulesConfig: rules.Config{
				WorkloadIfacePrefixes: configParams.InterfacePrefixes(),

				IPSetConfigV4: ipsets.NewIPVersionConfig(
					ipsets.IPFamilyV4,
					rules.IPSetNamePrefix,
					rules.AllHistoricIPSetNamePrefixes,
					rules.LegacyV4IPSetNames,
				),
				IPSetConfigV6: ipsets.NewIPVersionConfig(
					ipsets.IPFamilyV6,
					rules.IPSetNamePrefix,
					rules.AllHistoricIPSetNamePrefixes,
					nil,
				),

				DNSPolicyNfqueueID: 100,

				OpenStackSpecialCasesEnabled: configParams.OpenstackActive(),
				OpenStackMetadataIP:          net.ParseIP(configParams.MetadataAddr),
				OpenStackMetadataPort:        uint16(configParams.MetadataPort),

				MarkAccept:               0x1000000,
				MarkPass:                 0x2000000,
				MarkScratch0:             0x4000000,
				MarkScratch1:             0x8000000,
				MarkDrop:                 0x0800000,
				MarkIPsec:                0x0400000,
				MarkEgress:               0x0200000,
				MarkEndpoint:             0x000ff00,
				MarkDNSPolicy:            0x00001,
				MarkSkipDNSPolicyNfqueue: 0x400000,

				IPIPEnabled:       configParams.Encapsulation.IPIPEnabled,
				IPIPTunnelAddress: configParams.IpInIpTunnelAddr,

				ActionOnDrop:         configParams.DropActionOverride,
				EndpointToHostAction: configParams.DefaultEndpointToHostAction,
				FilterAllowAction:    configParams.IptablesFilterAllowAction,
				MangleAllowAction:    configParams.IptablesMangleAllowAction,
				FilterDenyAction:     configParams.IptablesFilterDenyAction,
			},
			DisableDNSPolicyPacketProcessor: true,
			IPIPMTU:                         configParams.IpInIpMtu,
			HealthAggregator:                healthAggregator,
			Collector:                       col,

			MTUIfacePattern: regexp.MustCompile(".*"),

			LookPathOverride: func(file string) (string, error) {
				return file, nil
			},

			PacketCapture: capture.Config{
				Directory: "/tmp",
			},

			RouteTableManager: idalloc.NewIndexAllocator(
				[]idalloc.IndexRange{{Min: 1, Max: 255}},
				[]idalloc.IndexRange{{Min: 253, Max: 255}}),

			KubernetesProvider: kubernetesProvider,
			RouteSource:        routeSource,
			Wireguard: wireguard.Config{
				EncryptHostTraffic: wireguardEncryptHostTraffic,
			},

			AWSSecondaryIPSupport: v3.AWSSecondaryIPEnabled,
		}
	})

	It("should be constructable", func() {
		var dp = intdataplane.NewIntDataplaneDriver(dpConfig, nil)
		defer dp.Stop()
		Expect(dp).ToNot(BeNil())
	})

	Context("with health aggregator", func() {
		BeforeEach(func() {
			healthAggregator = health.NewHealthAggregator()
		})

		It("should be constructable", func() {
			var dp = intdataplane.NewIntDataplaneDriver(dpConfig, nil)
			defer dp.Stop()
			Expect(dp).ToNot(BeNil())
		})
	})

	Context("with collector", func() {

		BeforeEach(func() {
			col = &mockCollector{}
		})

		It("should be constructable", func() {
			var dp = intdataplane.NewIntDataplaneDriver(dpConfig, nil)
			defer dp.Stop()
			Expect(dp).ToNot(BeNil())
		})
	})

	Context("with Wireguard on AKS", func() {
		BeforeEach(func() {
			kubernetesProvider = config.ProviderAKS
			routeSource = "WorkloadIPs"
			wireguardEncryptHostTraffic = true
		})

		It("should set the correct MTU", func() {
			intdataplane.ConfigureDefaultMTUs(1500, &dpConfig)
			Expect(dpConfig.Wireguard.MTU).To(Equal(1340))
		})
	})

	Context("with Wireguard on non-managed provider", func() {
		BeforeEach(func() {
			kubernetesProvider = config.ProviderNone
			routeSource = "CalicoIPAM"
		})

		It("should set the correct MTU", func() {
			intdataplane.ConfigureDefaultMTUs(1500, &dpConfig)
			Expect(dpConfig.Wireguard.MTU).To(Equal(1440))
		})
	})
})
