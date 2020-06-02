// Copyright (c) 2019-2020 Tigera, Inc. All rights reserved.
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

package daemon

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	lclient "github.com/tigera/licensing/client"
	"github.com/tigera/licensing/client/features"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/projectcalico/felix/config"
	"github.com/projectcalico/libcalico-go/lib/set"
)

// Dummy license checker for the tests.
type dlc struct {
	status   lclient.LicenseStatus
	features map[string]bool
}

func (d dlc) GetFeatureStatus(feature string) bool {
	switch d.status {
	case lclient.Valid, lclient.InGracePeriod:
		return d.features[feature]
	}
	return false
}

func (d dlc) GetLicenseStatus() lclient.LicenseStatus {
	return d.status
}

var _ = Describe("FelixDaemon license checks", func() {

	var cfg *config.Config

	BeforeEach(func() {
		// Create a config resource with all of the licensed features.
		cfg = config.New()
		cfg.UpdateFrom(map[string]string{
			"IPSecMode":                         "PSK",
			"IPSecAllowUnsecuredTraffic":        "false",
			"PrometheusReporterEnabled":         "true",
			"DropActionOverride":                "ACCEPT",
			"CloudWatchLogsReporterEnabled":     "true",
			"CloudWatchMetricsReporterEnabled":  "true",
			"CloudWatchNodeHealthStatusEnabled": "true",
			"FlowLogsFileEnabled":               "true",
		}, config.DatastoreGlobal)

		Expect(cfg.IPSecMode).To(Equal("PSK"))
		Expect(cfg.IPSecAllowUnsecuredTraffic).To(BeFalse())
		Expect(cfg.PrometheusReporterEnabled).To(BeTrue())
		Expect(cfg.DropActionOverride).To(Equal("ACCEPT"))
		Expect(cfg.CloudWatchLogsReporterEnabled).To(BeTrue())
		Expect(cfg.CloudWatchMetricsReporterEnabled).To(BeTrue())
		Expect(cfg.CloudWatchNodeHealthStatusEnabled).To(BeTrue())
		Expect(cfg.FlowLogsFileEnabled).To(BeTrue())
	})

	It("Should reset all values if there is no license", func() {
		removeUnlicensedFeaturesFromConfig(cfg, dlc{
			status: lclient.NoLicenseLoaded,
		})
		Expect(cfg.IPSecMode).To(Equal(""))
		Expect(cfg.PrometheusReporterEnabled).To(BeFalse())
		Expect(cfg.DropActionOverride).To(Equal("DROP"))
		Expect(cfg.CloudWatchLogsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchMetricsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchNodeHealthStatusEnabled).To(BeFalse())
		Expect(cfg.FlowLogsFileEnabled).To(BeFalse())
	})

	It("Should allow IPSec insecure if IPSec feature is in grace period", func() {
		removeUnlicensedFeaturesFromConfig(cfg, dlc{
			status: lclient.InGracePeriod,
			features: map[string]bool{
				features.IPSec: true,
			},
		})
		Expect(cfg.IPSecMode).To(Equal("PSK"))
		Expect(cfg.IPSecAllowUnsecuredTraffic).To(BeTrue())
		Expect(cfg.PrometheusReporterEnabled).To(BeFalse())
		Expect(cfg.DropActionOverride).To(Equal("DROP"))
		Expect(cfg.CloudWatchLogsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchMetricsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchNodeHealthStatusEnabled).To(BeFalse())
		Expect(cfg.FlowLogsFileEnabled).To(BeFalse())
	})

	It("Should leave IPSec settings unchanged if IPSec license is valid", func() {
		removeUnlicensedFeaturesFromConfig(cfg, dlc{
			status: lclient.Valid,
			features: map[string]bool{
				features.IPSec: true,
			},
		})
		Expect(cfg.IPSecMode).To(Equal("PSK"))
		Expect(cfg.IPSecAllowUnsecuredTraffic).To(BeFalse())
		Expect(cfg.PrometheusReporterEnabled).To(BeFalse())
		Expect(cfg.DropActionOverride).To(Equal("DROP"))
		Expect(cfg.CloudWatchLogsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchMetricsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchNodeHealthStatusEnabled).To(BeFalse())
		Expect(cfg.FlowLogsFileEnabled).To(BeFalse())
	})

	It("Should leave Prometheus setting unchanged if PrometheusMetrics license is valid", func() {
		removeUnlicensedFeaturesFromConfig(cfg, dlc{
			status: lclient.Valid,
			features: map[string]bool{
				features.PrometheusMetrics: true,
			},
		})
		Expect(cfg.IPSecMode).To(Equal(""))
		Expect(cfg.PrometheusReporterEnabled).To(BeTrue())
		Expect(cfg.DropActionOverride).To(Equal("DROP"))
		Expect(cfg.CloudWatchLogsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchMetricsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchNodeHealthStatusEnabled).To(BeFalse())
		Expect(cfg.FlowLogsFileEnabled).To(BeFalse())
	})

	It("Should leave DropActionOverride setting unchanged if DropActionOverride license is valid", func() {
		removeUnlicensedFeaturesFromConfig(cfg, dlc{
			status: lclient.Valid,
			features: map[string]bool{
				features.DropActionOverride: true,
			},
		})
		Expect(cfg.IPSecMode).To(Equal(""))
		Expect(cfg.PrometheusReporterEnabled).To(BeFalse())
		Expect(cfg.DropActionOverride).To(Equal("ACCEPT"))
		Expect(cfg.CloudWatchLogsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchMetricsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchNodeHealthStatusEnabled).To(BeFalse())
		Expect(cfg.FlowLogsFileEnabled).To(BeFalse())
	})

	It("Should leave AWSCloudwatchFlowLogs setting unchanged if AWSCloudwatchFlowLogs license is valid", func() {
		removeUnlicensedFeaturesFromConfig(cfg, dlc{
			status: lclient.Valid,
			features: map[string]bool{
				features.AWSCloudwatchFlowLogs: true,
			},
		})
		Expect(cfg.IPSecMode).To(Equal(""))
		Expect(cfg.PrometheusReporterEnabled).To(BeFalse())
		Expect(cfg.DropActionOverride).To(Equal("DROP"))
		Expect(cfg.CloudWatchLogsReporterEnabled).To(BeTrue())
		Expect(cfg.CloudWatchMetricsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchNodeHealthStatusEnabled).To(BeFalse())
		Expect(cfg.FlowLogsFileEnabled).To(BeFalse())
	})

	It("Should leave AWSCloudwatchMetrics setting unchanged if AWSCloudwatchMetrics license is valid", func() {
		removeUnlicensedFeaturesFromConfig(cfg, dlc{
			status: lclient.Valid,
			features: map[string]bool{
				features.AWSCloudwatchMetrics: true,
			},
		})
		Expect(cfg.IPSecMode).To(Equal(""))
		Expect(cfg.PrometheusReporterEnabled).To(BeFalse())
		Expect(cfg.DropActionOverride).To(Equal("DROP"))
		Expect(cfg.CloudWatchLogsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchMetricsReporterEnabled).To(BeTrue())
		Expect(cfg.CloudWatchNodeHealthStatusEnabled).To(BeTrue())
		Expect(cfg.FlowLogsFileEnabled).To(BeFalse())
	})

	It("Should leave FileOutputFlowLogs setting unchanged if FileOutputFlowLogs license is valid", func() {
		removeUnlicensedFeaturesFromConfig(cfg, dlc{
			status: lclient.Valid,
			features: map[string]bool{
				features.FileOutputFlowLogs: true,
			},
		})
		Expect(cfg.IPSecMode).To(Equal(""))
		Expect(cfg.PrometheusReporterEnabled).To(BeFalse())
		Expect(cfg.DropActionOverride).To(Equal("DROP"))
		Expect(cfg.CloudWatchLogsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchMetricsReporterEnabled).To(BeFalse())
		Expect(cfg.CloudWatchNodeHealthStatusEnabled).To(BeFalse())
		Expect(cfg.FlowLogsFileEnabled).To(BeTrue())
	})
})

var _ = Describe("Typha address discovery", func() {
	var (
		configParams *config.Config
		endpoints    *v1.Endpoints
		k8sClient    *fake.Clientset
	)

	refreshClient := func() {
		k8sClient = fake.NewSimpleClientset(endpoints)
	}

	BeforeEach(func() {
		configParams = config.New()
		_, err := configParams.UpdateFrom(map[string]string{
			"TyphaK8sServiceName": "calico-typha-service",
		}, config.EnvironmentVariable)
		Expect(err).NotTo(HaveOccurred())

		endpoints = &v1.Endpoints{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Endpoints",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "calico-typha-service",
				Namespace: "kube-system",
			},
			Subsets: []v1.EndpointSubset{
				{
					Addresses: []v1.EndpointAddress{
						{IP: "10.0.0.4"},
					},
					NotReadyAddresses: []v1.EndpointAddress{},
					Ports: []v1.EndpointPort{
						{Name: "calico-typha-v2", Port: 8157, Protocol: v1.ProtocolUDP},
					},
				},
				{
					Addresses: []v1.EndpointAddress{
						{IP: "10.0.0.2"},
					},
					NotReadyAddresses: []v1.EndpointAddress{
						{IP: "10.0.0.5"},
					},
					Ports: []v1.EndpointPort{
						{Name: "calico-typha-v2", Port: 8157, Protocol: v1.ProtocolUDP},
						{Name: "calico-typha", Port: 8156, Protocol: v1.ProtocolTCP},
					},
				},
			},
		}

		refreshClient()
	})

	It("should return address if configured", func() {
		configParams.TyphaAddr = "10.0.0.1:8080"
		typhaAddr, err := discoverTyphaAddr(configParams, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(typhaAddr).To(Equal("10.0.0.1:8080"))
	})

	It("should return nothing if no service name", func() {
		configParams.TyphaK8sServiceName = ""
		typhaAddr, err := discoverTyphaAddr(configParams, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(typhaAddr).To(Equal(""))
	})

	It("should return IP from endpoints", func() {
		typhaAddr, err := discoverTyphaAddr(configParams, k8sClient)
		Expect(err).NotTo(HaveOccurred())
		Expect(typhaAddr).To(Equal("10.0.0.2:8156"))
	})

	It("should bracket an IPv6 Typha address", func() {
		endpoints.Subsets[1].Addresses[0].IP = "fd5f:65af::2"
		refreshClient()
		typhaAddr, err := discoverTyphaAddr(configParams, k8sClient)
		Expect(err).NotTo(HaveOccurred())
		Expect(typhaAddr).To(Equal("[fd5f:65af::2]:8156"))
	})

	It("should error if no Typhas", func() {
		endpoints.Subsets = nil
		refreshClient()
		_, err := discoverTyphaAddr(configParams, k8sClient)
		Expect(err).To(HaveOccurred())
	})

	It("should choose random Typhas", func() {
		seenAddresses := set.New()
		expected := set.From("10.0.0.2:8156", "10.0.0.6:8156")
		endpoints.Subsets[1].Addresses = append(endpoints.Subsets[1].Addresses, v1.EndpointAddress{IP: "10.0.0.6"})
		refreshClient()

		for i := 0; i < 32; i++ {
			addr, err := discoverTyphaAddr(configParams, k8sClient)
			Expect(err).NotTo(HaveOccurred())
			seenAddresses.Add(addr)
			if seenAddresses.ContainsAll(expected) {
				return
			}
		}
		Fail(fmt.Sprintf("Didn't get expected values; got %v", seenAddresses))
	})
})
