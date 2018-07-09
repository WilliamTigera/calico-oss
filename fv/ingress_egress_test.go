// +build fvtests

// Copyright (c) 2017-2018 Tigera, Inc. All rights reserved.
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

package fv_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/projectcalico/felix/collector"
	"github.com/projectcalico/felix/fv/containers"
	"github.com/projectcalico/felix/fv/infrastructure"
	"github.com/projectcalico/felix/fv/metrics"
	"github.com/projectcalico/felix/fv/utils"
	"github.com/projectcalico/felix/fv/workload"
	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
)

// So that we can say 'HaveConnectivityTo' without the 'workload.' prefix...
var HaveConnectivityTo = workload.HaveConnectivityTo

var _ = Context("with initialized Felix, etcd datastore, 3 workloads", func() {

	var (
		etcd   *containers.Container
		felix  *infrastructure.Felix
		client client.Interface
		w      [3]*workload.Workload
	)

	BeforeEach(func() {
		opts := infrastructure.DefaultTopologyOptions()
		opts.EnableCloudWatchLogs()
		felix, etcd, client = infrastructure.StartSingleNodeEtcdTopology(opts)

		// Install a default profile that allows workloads with this profile to talk to each
		// other, in the absence of any Policy.
		defaultProfile := api.NewProfile()
		defaultProfile.Name = "default"
		defaultProfile.Spec.LabelsToApply = map[string]string{"default": ""}
		defaultProfile.Spec.Egress = []api.Rule{{Action: api.Allow}}
		defaultProfile.Spec.Ingress = []api.Rule{{
			Action: api.Allow,
			Source: api.EntityRule{Selector: "default == ''"},
		}}
		_, err := client.Profiles().Create(utils.Ctx, defaultProfile, utils.NoOptions)
		Expect(err).NotTo(HaveOccurred())

		// Create three workloads, using that profile.
		for ii := range w {
			iiStr := strconv.Itoa(ii)
			w[ii] = workload.Run(felix, "w"+iiStr, "default", "10.65.0.1"+iiStr, "8055", "tcp")
			w[ii].Configure(client)
		}
	})

	AfterEach(func() {

		if CurrentGinkgoTestDescription().Failed {
			felix.Exec("iptables-save", "-c")
			felix.Exec("ip", "r")
		}

		for ii := range w {
			w[ii].Stop()
		}
		felix.Stop()

		if CurrentGinkgoTestDescription().Failed {
			etcd.Exec("etcdctl", "ls", "--recursive", "/")
		}
		etcd.Stop()
	})

	It("full connectivity to and from workload 0", func() {
		Expect(w[1]).To(HaveConnectivityTo(w[0]))
		Expect(w[2]).To(HaveConnectivityTo(w[0]))
		Expect(w[0]).To(HaveConnectivityTo(w[1]))
		Expect(w[0]).To(HaveConnectivityTo(w[2]))

		// Expect flow logs that describe those 4 different flows starting and
		// completing.  Note that each flow is reported twice, for egress from the
		// source workload and for ingress to the destination workload.
		//
		// Timing-wise, there is an interplay between
		//
		// (a) when the conntrack state for a flow times out (120s)
		//
		// (b) how long after that Felix expires the flow (up to 10s)
		//
		// (c) when Felix decides to generate a round of flow logs (every 120s,
		// but possibly offset from the previous timings).
		//
		// Therefore we could see 8 flow logs each saying that its flow both
		// started and completed; or we could see 16 flow logs of which 8 say that
		// the flow started, and the other 8 say that it completed.  The following
		// code is written so as to allow for that variation.
		//
		// FIXME: This test currently fails, most of the time, because (1) we
		// usually get the 16 flow logs case, where flow start and completion are
		// reported separately, and (2) the completion flow logs also -
		// incorrectly - report the flow as having started within the same time
		// interval.
		Eventually(func() error {
			expectedKeys := map[string]bool{
				"start-w0-idx1--w2-idx5--in":  true,
				"start-w0-idx1--w2-idx5--out": true,
				"start-w2-idx5--w0-idx1--in":  true,
				"start-w2-idx5--w0-idx1--out": true,
				"start-w0-idx1--w1-idx3--in":  true,
				"start-w0-idx1--w1-idx3--out": true,
				"start-w1-idx3--w0-idx1--in":  true,
				"start-w1-idx3--w0-idx1--out": true,
				"end-w0-idx1--w2-idx5--in":    true,
				"end-w0-idx1--w2-idx5--out":   true,
				"end-w2-idx5--w0-idx1--in":    true,
				"end-w2-idx5--w0-idx1--out":   true,
				"end-w0-idx1--w1-idx3--in":    true,
				"end-w0-idx1--w1-idx3--out":   true,
				"end-w1-idx3--w0-idx1--in":    true,
				"end-w1-idx3--w0-idx1--out":   true,
			}
			logs, err := felix.ReadCloudWatchLogs()
			if err != nil {
				return err
			}
			for _, log := range logs {
				fl := &collector.FlowLog{}
				fl.Deserialize(log.Message)
				dir := "in"
				if fl.Direction == collector.FlowLogDirectionOut {
					dir = "out"
				}
				key := fl.SrcMeta.Name + "--" + fl.DstMeta.Name + "--" + dir
				if fl.NumFlowsStarted == 1 {
					if _, ok := expectedKeys["start-"+key]; ok {
						// Expected flow log seen.
						delete(expectedKeys, "start-"+key)
					} else {
						// Unexpected flow log.
						return errors.New(fmt.Sprintf("Unexpected flow log: %v", fl))
					}
				}
				if fl.NumFlowsCompleted == 1 {
					if _, ok := expectedKeys["end-"+key]; ok {
						// Expected flow log seen.
						delete(expectedKeys, "end-"+key)
					} else {
						// Unexpected flow log.
						return errors.New(fmt.Sprintf("Unexpected flow log: %v", fl))
					}
				}
			}
			if len(expectedKeys) != 0 {
				return errors.New(fmt.Sprintf("Expected flow logs not seen: %v", expectedKeys))
			}
			return nil
		}, "300s", "15s").ShouldNot(HaveOccurred())
	})

	Context("with ingress-only restriction for workload 0", func() {

		BeforeEach(func() {
			policy := api.NewNetworkPolicy()
			policy.Namespace = "fv"
			policy.Name = "policy-1"
			allowFromW1 := api.Rule{
				Action: api.Allow,
				Source: api.EntityRule{
					Selector: w[1].NameSelector(),
				},
			}
			policy.Spec.Ingress = []api.Rule{allowFromW1}
			policy.Spec.Selector = w[0].NameSelector()
			_, err := client.NetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
			Expect(err).NotTo(HaveOccurred())
		})

		It("only w1 can connect into w0, but egress from w0 is unrestricted", func() {
			Eventually(w[2], "10s", "1s").ShouldNot(HaveConnectivityTo(w[0]))
			Expect(w[1]).To(HaveConnectivityTo(w[0]))
			Expect(w[0]).To(HaveConnectivityTo(w[1]))
			Expect(w[0]).To(HaveConnectivityTo(w[1]))
		})
	})

	Context("with egress-only restriction for workload 0", func() {

		BeforeEach(func() {
			policy := api.NewNetworkPolicy()
			policy.Namespace = "fv"
			policy.Name = "policy-1"
			allowToW1 := api.Rule{
				Action: api.Allow,
				Destination: api.EntityRule{
					Selector: w[1].NameSelector(),
				},
			}
			policy.Spec.Egress = []api.Rule{allowToW1}
			policy.Spec.Selector = w[0].NameSelector()
			_, err := client.NetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
			Expect(err).NotTo(HaveOccurred())
		})

		It("ingress to w0 is unrestricted, but w0 can only connect out to w1", func() {
			Eventually(w[0], "10s", "1s").ShouldNot(HaveConnectivityTo(w[2]))
			Expect(w[1]).To(HaveConnectivityTo(w[0]))
			Expect(w[2]).To(HaveConnectivityTo(w[0]))
			Expect(w[0]).To(HaveConnectivityTo(w[1]))
		})
	})

	Context("with ingress rules and types [ingress,egress]", func() {

		BeforeEach(func() {
			policy := api.NewNetworkPolicy()
			policy.Namespace = "fv"
			policy.Name = "policy-1"
			allowFromW1 := api.Rule{
				Action: api.Allow,
				Source: api.EntityRule{
					Selector: w[1].NameSelector(),
				},
			}
			policy.Spec.Ingress = []api.Rule{allowFromW1}
			policy.Spec.Selector = w[0].NameSelector()
			policy.Spec.Types = []api.PolicyType{api.PolicyTypeIngress, api.PolicyTypeEgress}
			_, err := client.NetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
			Expect(err).NotTo(HaveOccurred())
		})

		It("only w1 can connect into w0, and all egress from w0 is denied", func() {
			Eventually(w[2], "10s", "1s").ShouldNot(HaveConnectivityTo(w[0]))
			Expect(w[1]).To(HaveConnectivityTo(w[0]))
			Expect(w[0]).NotTo(HaveConnectivityTo(w[1]))
			Expect(w[0]).NotTo(HaveConnectivityTo(w[2]))
		})
	})

	Context("with an egress deny rule", func() {
		var policy *api.NetworkPolicy

		BeforeEach(func() {
			policy = api.NewNetworkPolicy()
			policy.Namespace = "fv"
			policy.Name = "policy-1"
			allowFromW1 := api.Rule{
				Action: api.Allow,
				Source: api.EntityRule{
					Selector: w[1].NameSelector(),
				},
			}
			policy.Spec.Ingress = []api.Rule{allowFromW1}
			policy.Spec.Egress = []api.Rule{{Action: api.Deny}}
			policy.Spec.Selector = w[0].NameSelector()
		})

		JustBeforeEach(func() {
			_, err := client.NetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
			Expect(err).NotTo(HaveOccurred())
		})

		Describe("and types [ingress] (i.e. disabling the egress rule)", func() {
			BeforeEach(func() {
				policy.Spec.Types = []api.PolicyType{api.PolicyTypeIngress}
			})

			It("only w1 can connect into w0, and all egress from w0 is allowed", func() {
				Eventually(w[2], "10s", "1s").ShouldNot(HaveConnectivityTo(w[0]))
				Expect(w[1]).To(HaveConnectivityTo(w[0]))
				Expect(w[0]).To(HaveConnectivityTo(w[1]))
				Expect(w[0]).To(HaveConnectivityTo(w[2]))
			})
		})

		Describe("and types [ingress, egress]", func() {
			BeforeEach(func() {
				policy.Spec.Types = []api.PolicyType{api.PolicyTypeIngress, api.PolicyTypeEgress}
			})

			It("only w1 can connect into w0, and all egress from w0 is blocked", func() {
				Eventually(w[2], "10s", "1s").ShouldNot(HaveConnectivityTo(w[0]))
				Expect(w[1]).To(HaveConnectivityTo(w[0]))
				Expect(w[0]).NotTo(HaveConnectivityTo(w[1]))
				Expect(w[0]).NotTo(HaveConnectivityTo(w[2]))
			})
		})
	})
})

var _ = Context("with Typha and Felix-Typha TLS", func() {

	var (
		etcd   *containers.Container
		felix  *infrastructure.Felix
		client client.Interface
		w      [3]*workload.Workload
	)

	BeforeEach(func() {
		options := infrastructure.DefaultTopologyOptions()
		options.WithTypha = true
		options.WithFelixTyphaTLS = true
		felix, etcd, client = infrastructure.StartSingleNodeEtcdTopology(options)

		// Install a default profile that allows workloads with this profile to talk to each
		// other, in the absence of any Policy.
		defaultProfile := api.NewProfile()
		defaultProfile.Name = "default"
		defaultProfile.Spec.LabelsToApply = map[string]string{"default": ""}
		defaultProfile.Spec.Egress = []api.Rule{{Action: api.Allow}}
		defaultProfile.Spec.Ingress = []api.Rule{{
			Action: api.Allow,
			Source: api.EntityRule{Selector: "default == ''"},
		}}
		_, err := client.Profiles().Create(utils.Ctx, defaultProfile, utils.NoOptions)
		Expect(err).NotTo(HaveOccurred())

		// Create three workloads, using that profile.
		for ii := range w {
			iiStr := strconv.Itoa(ii)
			w[ii] = workload.Run(felix, "w"+iiStr, "default", "10.65.0.1"+iiStr, "8055", "tcp")
			w[ii].Configure(client)
		}
	})

	AfterEach(func() {

		if CurrentGinkgoTestDescription().Failed {
			felix.Exec("iptables-save", "-c")
			felix.Exec("ip", "r")
		}

		for ii := range w {
			w[ii].Stop()
		}
		felix.Stop()

		if CurrentGinkgoTestDescription().Failed {
			etcd.Exec("etcdctl", "ls", "--recursive", "/")
		}
		etcd.Stop()
	})

	It("full connectivity to and from workload 0", func() {
		Expect(w[1]).To(HaveConnectivityTo(w[0]))
		Expect(w[2]).To(HaveConnectivityTo(w[0]))
		Expect(w[0]).To(HaveConnectivityTo(w[1]))
		Expect(w[0]).To(HaveConnectivityTo(w[2]))
	})

	Context("with ingress-only restriction for workload 0", func() {

		BeforeEach(func() {
			policy := api.NewNetworkPolicy()
			policy.Namespace = "fv"
			policy.Name = "policy-1"
			allowFromW1 := api.Rule{
				Action: api.Allow,
				Source: api.EntityRule{
					Selector: w[1].NameSelector(),
				},
			}
			policy.Spec.Ingress = []api.Rule{allowFromW1}
			policy.Spec.Selector = w[0].NameSelector()
			_, err := client.NetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
			Expect(err).NotTo(HaveOccurred())
		})

		It("only w1 can connect into w0, but egress from w0 is unrestricted", func() {
			Eventually(w[2], "10s", "1s").ShouldNot(HaveConnectivityTo(w[0]))
			Expect(w[1]).To(HaveConnectivityTo(w[0]))
			Expect(w[0]).To(HaveConnectivityTo(w[1]))
			Expect(w[0]).To(HaveConnectivityTo(w[1]))
		})
	})
})

var _ = Context("with TLS-secured Prometheus ports", func() {

	var (
		etcd    *containers.Container
		felix   *infrastructure.Felix
		client  client.Interface
		w       [3]*workload.Workload
		options infrastructure.TopologyOptions
	)

	BeforeEach(func() {
		options = infrastructure.DefaultTopologyOptions()
		options.WithTypha = true
		options.WithPrometheusPortTLS = true
		felix, etcd, client = infrastructure.StartSingleNodeEtcdTopology(options)

		// Install a default profile that allows workloads with this profile to talk to each
		// other, in the absence of any Policy.
		defaultProfile := api.NewProfile()
		defaultProfile.Name = "default"
		defaultProfile.Spec.LabelsToApply = map[string]string{"default": ""}
		defaultProfile.Spec.Egress = []api.Rule{{Action: api.Allow}}
		defaultProfile.Spec.Ingress = []api.Rule{{
			Action: api.Allow,
			Source: api.EntityRule{Selector: "default == ''"},
		}}
		_, err := client.Profiles().Create(utils.Ctx, defaultProfile, utils.NoOptions)
		Expect(err).NotTo(HaveOccurred())

		// Create three workloads, using that profile.
		for ii := range w {
			iiStr := strconv.Itoa(ii)
			w[ii] = workload.Run(felix, "w"+iiStr, "default", "10.65.0.1"+iiStr, "8055", "tcp")
			w[ii].Configure(client)
		}
	})

	AfterEach(func() {

		if CurrentGinkgoTestDescription().Failed {
			felix.Exec("iptables-save", "-c")
			felix.Exec("ip", "r")
		}

		for ii := range w {
			w[ii].Stop()
		}
		felix.Stop()

		if CurrentGinkgoTestDescription().Failed {
			etcd.Exec("etcdctl", "ls", "--recursive", "/")
		}
		etcd.Stop()
	})

	It("full connectivity to and from workload 0", func() {
		Expect(w[1]).To(HaveConnectivityTo(w[0]))
		Expect(w[2]).To(HaveConnectivityTo(w[0]))
		Expect(w[0]).To(HaveConnectivityTo(w[1]))
		Expect(w[0]).To(HaveConnectivityTo(w[2]))
	})

	testAccess := func(tester func(caFile, certFile, keyFile string) error) func(certKeyName string, canAccess bool) func() {
		return func(certKeyName string, canAccess bool) func() {
			return func() {
				var caFile, certFile, keyFile string
				if certKeyName != "" {
					caFile = filepath.Join(infrastructure.CertDir, "ca.crt")
					certFile = filepath.Join(infrastructure.CertDir, certKeyName+".crt")
					keyFile = filepath.Join(infrastructure.CertDir, certKeyName+".key")
				}
				err := tester(caFile, certFile, keyFile)
				if canAccess {
					Expect(err).NotTo(HaveOccurred())
				} else {
					Expect(err).To(HaveOccurred())
				}
			}
		}
	}

	testFelixReporter := testAccess(func(caFile, certFile, keyFile string) error {
		_, err := metrics.GetMetric(felix.IP, 9092, "calico_denied_packets", caFile, certFile, keyFile)
		return err
	})

	testFelixMetrics := testAccess(func(caFile, certFile, keyFile string) error {
		_, err := metrics.GetMetric(felix.IP, 9091, "felix_host", caFile, certFile, keyFile)
		return err
	})

	testTyphaMetrics := testAccess(func(caFile, certFile, keyFile string) error {
		_, err := metrics.GetMetric(felix.TyphaIP, 9093, "typha_connections_active", caFile, certFile, keyFile)
		return err
	})

	It("should not be able to access Felix Reporter port over http", testFelixReporter("", false))

	It("should not be able to access Felix Metrics port over http", testFelixMetrics("", false))

	It("should not be able to access Typha Metrics port over http", testTyphaMetrics("", false))

	It("should not be able to access Felix Reporter port with untrusted cert", testFelixReporter("client-untrusted", false))

	It("should not be able to access Felix Metrics port with untrusted cert", testFelixMetrics("client-untrusted", false))

	It("should not be able to access Typha Metrics port with untrusted cert", testTyphaMetrics("client-untrusted", false))

	It("should be able to access Felix Reporter port with trusted cert", testFelixReporter("client", true))

	It("should be able to access Felix Metrics port with trusted cert", testFelixMetrics("client", true))

	It("should be able to access Typha Metrics port with trusted cert", testTyphaMetrics("client", true))
})
