// Copyright (c) 2020 Tigera, Inc. All rights reserved.
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

// +build fvtests

package fv_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	"github.com/projectcalico/libcalico-go/lib/apiconfig"
	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/ipam"
	cnet "github.com/projectcalico/libcalico-go/lib/net"
	"github.com/projectcalico/libcalico-go/lib/numorstring"
	options2 "github.com/projectcalico/libcalico-go/lib/options"

	"github.com/projectcalico/felix/bpf"
	"github.com/projectcalico/felix/bpf/conntrack"
	"github.com/projectcalico/felix/bpf/nat"
	. "github.com/projectcalico/felix/fv/connectivity"
	"github.com/projectcalico/felix/fv/containers"
	"github.com/projectcalico/felix/fv/infrastructure"
	"github.com/projectcalico/felix/fv/utils"
	"github.com/projectcalico/felix/fv/workload"
)

// We run with and without connection-time load balancing for a couple of reasons:
// - We can only test the non-connection time NAT logic (and node ports) with it disabled.
// - Since the connection time program applies to the whole host, the different felix nodes actually share the
//   connection-time program.  This is a bit of a broken test but it's better than nothing since all felix nodes
//   should be programming the same NAT mappings.
var _ = describeBPFTests(withProto("tcp"), withConnTimeLoadBalancingEnabled(), withNonProtocolDependentTests())
var _ = describeBPFTests(withProto("udp"), withConnTimeLoadBalancingEnabled())
var _ = describeBPFTests(withProto("udp"), withConnTimeLoadBalancingEnabled(), withUDPUnConnected())
var _ = describeBPFTests(withProto("tcp"))
var _ = describeBPFTests(withProto("udp"))
var _ = describeBPFTests(withProto("udp"), withUDPUnConnected())
var _ = describeBPFTests(withProto("udp"), withUDPConnectedRecvMsg(), withConnTimeLoadBalancingEnabled())
var _ = describeBPFTests(withTunnel("ipip"), withProto("tcp"), withConnTimeLoadBalancingEnabled())
var _ = describeBPFTests(withTunnel("ipip"), withProto("udp"), withConnTimeLoadBalancingEnabled())
var _ = describeBPFTests(withTunnel("ipip"), withProto("tcp"))
var _ = describeBPFTests(withTunnel("ipip"), withProto("udp"))
var _ = describeBPFTests(withProto("tcp"), withDSR())
var _ = describeBPFTests(withProto("udp"), withDSR())
var _ = describeBPFTests(withTunnel("ipip"), withProto("tcp"), withDSR())
var _ = describeBPFTests(withTunnel("ipip"), withProto("udp"), withDSR())

// Run a stripe of tests with BPF logging disabled since the compiler tends to optimise the code differently
// with debug disabled and that can lead to verifier issues.
var _ = describeBPFTests(withProto("tcp"),
	withConnTimeLoadBalancingEnabled(),
	withBPFLogLevel("info"))

type bpfTestOptions struct {
	connTimeEnabled bool
	protocol        string
	udpUnConnected  bool
	bpfLogLevel     string
	tunnel          string
	dsr             bool
	udpConnRecvMsg  bool
	nonProtoTests   bool
}

type bpfTestOpt func(opts *bpfTestOptions)

func withProto(proto string) bpfTestOpt {
	return func(opts *bpfTestOptions) {
		opts.protocol = proto
	}
}

func withConnTimeLoadBalancingEnabled() bpfTestOpt {
	return func(opts *bpfTestOptions) {
		opts.connTimeEnabled = true
	}
}

func withNonProtocolDependentTests() bpfTestOpt {
	return func(opts *bpfTestOptions) {
		opts.nonProtoTests = true
	}
}

func withBPFLogLevel(level string) bpfTestOpt {
	return func(opts *bpfTestOptions) {
		opts.bpfLogLevel = level
	}
}

func withTunnel(tunnel string) bpfTestOpt {
	return func(opts *bpfTestOptions) {
		opts.tunnel = tunnel
	}
}

func withUDPUnConnected() bpfTestOpt {
	return func(opts *bpfTestOptions) {
		opts.udpUnConnected = true
	}
}

func withDSR() bpfTestOpt {
	return func(opts *bpfTestOptions) {
		opts.dsr = true
	}
}

func withUDPConnectedRecvMsg() bpfTestOpt {
	return func(opts *bpfTestOptions) {
		opts.udpConnRecvMsg = true
	}
}

const expectedRouteDump = `10.65.0.0/16: remote in-pool nat-out
10.65.0.2/32: local workload in-pool nat-out idx -
10.65.0.3/32: local workload in-pool nat-out idx -
10.65.0.4/32: local workload in-pool nat-out idx -
10.65.1.0/26: remote workload in-pool nat-out nh FELIX_1
10.65.2.0/26: remote workload in-pool nat-out nh FELIX_2
FELIX_0/32: local host idx -
FELIX_1/32: remote host
FELIX_2/32: remote host`

const expectedRouteDumpIPIP = `10.65.0.0/16: remote in-pool nat-out
10.65.0.1/32: local host
10.65.0.2/32: local workload in-pool nat-out idx -
10.65.0.3/32: local workload in-pool nat-out idx -
10.65.0.4/32: local workload in-pool nat-out idx -
10.65.1.0/26: remote workload in-pool nat-out nh FELIX_1
10.65.2.0/26: remote workload in-pool nat-out nh FELIX_2
FELIX_0/32: local host idx -
FELIX_1/32: remote host
FELIX_2/32: remote host`

const extIP = "10.1.2.3"

func describeBPFTests(opts ...bpfTestOpt) bool {
	testOpts := bpfTestOptions{
		bpfLogLevel: "debug",
		tunnel:      "none",
	}
	for _, o := range opts {
		o(&testOpts)
	}

	protoExt := ""
	if testOpts.udpUnConnected {
		protoExt = "-unconnected"
	}
	if testOpts.udpConnRecvMsg {
		protoExt = "-conn-recvmsg"
	}

	desc := fmt.Sprintf("_BPF_ _BPF-SAFE_ BPF tests (%s%s, ct=%v, log=%s, tunnel=%s, dsr=%v)",
		testOpts.protocol, protoExt, testOpts.connTimeEnabled,
		testOpts.bpfLogLevel, testOpts.tunnel, testOpts.dsr,
	)

	return infrastructure.DatastoreDescribe(desc, []apiconfig.DatastoreType{apiconfig.Kubernetes}, func(getInfra infrastructure.InfraFactory) {

		var (
			infra          infrastructure.DatastoreInfra
			felixes        []*infrastructure.Felix
			calicoClient   client.Interface
			cc             *Checker
			externalClient *containers.Container
			deadWorkload   *workload.Workload
			bpfLog         *containers.Container
			options        infrastructure.TopologyOptions
			numericProto   uint8
			expectedRoutes string
		)

		switch testOpts.protocol {
		case "tcp":
			numericProto = 6
		case "udp":
			numericProto = 17
		default:
			Fail("bad protocol option")
		}

		BeforeEach(func() {
			if os.Getenv("FELIX_FV_ENABLE_BPF") != "true" {
				Skip("Skipping BPF test in non-BPF run.")
			}
			bpfLog = containers.Run("bpf-log", containers.RunOpts{AutoRemove: true}, "--privileged",
				"calico/bpftool:v5.3-amd64", "/bpftool", "prog", "tracelog")
			infra = getInfra()

			cc = &Checker{
				CheckSNAT: true,
			}
			cc.Protocol = testOpts.protocol
			if testOpts.protocol == "udp" && testOpts.udpUnConnected {
				cc.Protocol += "-noconn"
			}
			if testOpts.protocol == "udp" && testOpts.udpConnRecvMsg {
				cc.Protocol += "-recvmsg"
			}

			options = infrastructure.DefaultTopologyOptions()
			options.FelixLogSeverity = "debug"
			options.NATOutgoingEnabled = true
			switch testOpts.tunnel {
			case "none":
				options.IPIPEnabled = false
				options.IPIPRoutesEnabled = false
				expectedRoutes = expectedRouteDump
			case "ipip":
				options.IPIPEnabled = true
				options.IPIPRoutesEnabled = true
				expectedRoutes = expectedRouteDumpIPIP
			default:
				Fail("bad tunnel option")
			}
			options.ExtraEnvVars["FELIX_BPFConnectTimeLoadBalancingEnabled"] = fmt.Sprint(testOpts.connTimeEnabled)
			options.ExtraEnvVars["FELIX_BPFLogLevel"] = fmt.Sprint(testOpts.bpfLogLevel)
			if testOpts.dsr {
				options.ExtraEnvVars["FELIX_BPFExternalServiceMode"] = "dsr"
			}
		})

		JustAfterEach(func() {
			if CurrentGinkgoTestDescription().Failed {
				currBpfsvcs, currBpfeps := dumpNATmaps(felixes)

				for i, felix := range felixes {
					felix.Exec("iptables-save", "-c")
					felix.Exec("ip", "r")
					felix.Exec("ip", "route", "show", "cached")
					felix.Exec("calico-bpf", "ipsets", "dump")
					felix.Exec("calico-bpf", "routes", "dump")
					felix.Exec("calico-bpf", "nat", "dump")
					felix.Exec("calico-bpf", "conntrack", "dump")
					log.Infof("[%d]FrontendMap: %+v", i, currBpfsvcs[i])
					log.Infof("[%d]NATBackend: %+v", i, currBpfeps[i])
					log.Infof("[%d]SendRecvMap: %+v", i, dumpSendRecvMap(felix))
				}
				externalClient.Exec("ip", "route", "show", "cached")
			}
		})

		AfterEach(func() {
			log.Info("AfterEach starting")
			for _, f := range felixes {
				f.Exec("calico-bpf", "connect-time", "clean")
				f.Stop()
			}
			infra.Stop()
			externalClient.Stop()
			bpfLog.Stop()
			log.Info("AfterEach done")
		})

		createPolicy := func(policy *api.GlobalNetworkPolicy) *api.GlobalNetworkPolicy {
			log.WithField("policy", dumpResource(policy)).Info("Creating policy")
			policy, err := calicoClient.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
			Expect(err).NotTo(HaveOccurred())
			return policy
		}

		updatePolicy := func(policy *api.GlobalNetworkPolicy) *api.GlobalNetworkPolicy {
			log.WithField("policy", dumpResource(policy)).Info("Updating policy")
			policy, err := calicoClient.GlobalNetworkPolicies().Update(utils.Ctx, policy, utils.NoOptions)
			Expect(err).NotTo(HaveOccurred())
			return policy
		}
		_ = updatePolicy

		Describe("with a single node and an allow-all policy", func() {
			var (
				hostW *workload.Workload
				w     [2]*workload.Workload
			)

			if !testOpts.connTimeEnabled {
				// These tests don't depend on NAT.
				return
			}

			JustBeforeEach(func() {
				felixes, calicoClient = infrastructure.StartNNodeTopology(1, options, infra)

				hostW = workload.Run(
					felixes[0],
					"host",
					"default",
					felixes[0].IP, // Same IP as felix means "run in the host's namespace"
					"8055",
					testOpts.protocol)

				// Start a couple of workloads so we can check workload-to-workload and workload-to-host.
				for i := 0; i < 2; i++ {
					wIP := fmt.Sprintf("10.65.0.%d", i+2)
					w[i] = workload.Run(felixes[0], fmt.Sprintf("w%d", i), "default", wIP, "8055", testOpts.protocol)
					w[i].WorkloadEndpoint.Labels = map[string]string{"name": w[i].Name}
					w[i].ConfigureInDatastore(infra)
				}

				err := infra.AddDefaultDeny()
				Expect(err).NotTo(HaveOccurred())

				pol := api.NewGlobalNetworkPolicy()
				pol.Namespace = "fv"
				pol.Name = "policy-1"
				pol.Spec.Ingress = []api.Rule{{Action: "Allow"}}
				pol.Spec.Egress = []api.Rule{{Action: "Allow"}}
				pol.Spec.Selector = "all()"

				pol = createPolicy(pol)
			})

			Describe("with DefaultEndpointToHostAction=DROP", func() {
				BeforeEach(func() {
					options.ExtraEnvVars["FELIX_DefaultEndpointToHostAction"] = "DROP"
				})
				It("should only allow traffic from workload to workload", func() {
					cc.ExpectSome(w[0], w[1])
					cc.ExpectSome(w[1], w[0])
					cc.ExpectNone(w[1], hostW)
					cc.ExpectSome(hostW, w[0])
					cc.CheckConnectivity()
				})
			})

			getMapIDByPath := func(felix *infrastructure.Felix, filename string) (int, error) {
				out, err := felix.ExecOutput("bpftool", "map", "show", "pinned", filename, "-j")
				if err != nil {
					return 0, err
				}
				var mapMeta struct {
					ID    int    `json:"id"`
					Error string `json:"error"`
				}
				err = json.Unmarshal([]byte(out), &mapMeta)
				if err != nil {
					return 0, err
				}
				if mapMeta.Error != "" {
					return 0, errors.New(mapMeta.Error)
				}
				return mapMeta.ID, nil
			}

			mustGetMapIDByPath := func(felix *infrastructure.Felix, filename string) int {
				var mapID int
				Eventually(func() error {
					var err error
					mapID, err = getMapIDByPath(felix, filename)
					return err
				}, "5s").ShouldNot(HaveOccurred())
				return mapID
			}

			Describe("with DefaultEndpointToHostAction=ACCEPT", func() {
				BeforeEach(func() {
					options.ExtraEnvVars["FELIX_DefaultEndpointToHostAction"] = "ACCEPT"
				})
				It("should traffic from workload to workload and to/from host", func() {
					cc.ExpectSome(w[0], w[1])
					cc.ExpectSome(w[1], w[0])
					cc.ExpectSome(w[1], hostW)
					cc.ExpectSome(hostW, w[0])
					cc.CheckConnectivity()
				})
			})

			if testOpts.protocol != "udp" { // No need to run these tests per-protocol.

				mapPath := conntrack.Map(&bpf.MapContext{}).Path()

				Describe("with map repinning enabled", func() {
					BeforeEach(func() {
						options.ExtraEnvVars["FELIX_DebugBPFMapRepinEnabled"] = "true"
					})

					It("should repin maps", func() {
						// Wait for the first felix to create its maps.
						mapID := mustGetMapIDByPath(felixes[0], mapPath)

						// Now, start a completely independent felix, which will get its own bpffs.  It should re-pin the
						// maps, picking up the ones from the first felix.
						extraFelix, _ := infrastructure.StartSingleNodeTopology(options, infra)
						defer extraFelix.Stop()

						secondMapID := mustGetMapIDByPath(extraFelix, mapPath)
						Expect(mapID).NotTo(BeNumerically("==", 0))
						Expect(mapID).To(BeNumerically("==", secondMapID))
					})
				})

				Describe("with map repinning disabled", func() {
					It("should repin maps", func() {
						// Wait for the first felix to create its maps.
						mapID := mustGetMapIDByPath(felixes[0], mapPath)

						// Now, start a completely independent felix, which will get its own bpffs.  It should make its own
						// maps.
						extraFelix, _ := infrastructure.StartSingleNodeTopology(options, infra)
						defer extraFelix.Stop()

						secondMapID := mustGetMapIDByPath(extraFelix, mapPath)
						Expect(mapID).NotTo(BeNumerically("==", 0))
						Expect(mapID).NotTo(BeNumerically("==", secondMapID))
					})
				})
			}

			if testOpts.nonProtoTests {
				// We can only test that felix _sets_ this because the flag is one-way and cannot be unset.
				It("should enable the kernel.unprivileged_bpf_disabled sysctl", func() {
					Eventually(func() string {
						out, err := felixes[0].ExecOutput("sysctl", "kernel.unprivileged_bpf_disabled")
						if err != nil {
							log.WithError(err).Error("Failed to run sysctl")
						}
						return out
					}).Should(ContainSubstring("kernel.unprivileged_bpf_disabled = 1"))
				})
			}
		})

		const numNodes = 3

		Describe(fmt.Sprintf("with a %d node cluster", numNodes), func() {
			var (
				w     [numNodes][2]*workload.Workload
				hostW [numNodes]*workload.Workload
			)

			BeforeEach(func() {
				felixes, calicoClient = infrastructure.StartNNodeTopology(numNodes, options, infra)

				addWorkload := func(run bool, ii, wi, port int, labels map[string]string) *workload.Workload {
					if labels == nil {
						labels = make(map[string]string)
					}

					wIP := fmt.Sprintf("10.65.%d.%d", ii, wi+2)
					wName := fmt.Sprintf("w%d%d", ii, wi)

					w := workload.New(felixes[ii], wName, "default",
						wIP, strconv.Itoa(port), testOpts.protocol)
					if run {
						w.Start()
					}

					labels["name"] = w.Name
					labels["workload"] = "regular"

					w.WorkloadEndpoint.Labels = labels
					w.ConfigureInDatastore(infra)
					// Assign the workload's IP in IPAM, this will trigger calculation of routes.
					err := calicoClient.IPAM().AssignIP(context.Background(), ipam.AssignIPArgs{
						IP:       cnet.MustParseIP(wIP),
						HandleID: &w.Name,
						Attrs: map[string]string{
							ipam.AttributeNode: felixes[ii].Hostname,
						},
						Hostname: felixes[ii].Hostname,
					})
					Expect(err).NotTo(HaveOccurred())

					return w
				}

				// Start a host networked workload on each host for connectivity checks.
				for ii := range felixes {
					// We tell each host-networked workload to open:
					// TODO: Copied from another test
					// - its normal (uninteresting) port, 8055
					// - port 2379, which is both an inbound and an outbound failsafe port
					// - port 22, which is an inbound failsafe port.
					// This allows us to test the interaction between do-not-track policy and failsafe
					// ports.
					hostW[ii] = workload.Run(
						felixes[ii],
						fmt.Sprintf("host%d", ii),
						"default",
						felixes[ii].IP, // Same IP as felix means "run in the host's namespace"
						"8055",
						testOpts.protocol)

					hostW[ii].WorkloadEndpoint.Labels = map[string]string{"name": hostW[ii].Name}
					hostW[ii].ConfigureInDatastore(infra)

					// Two workloads on each host so we can check the same host and other host cases.
					w[ii][0] = addWorkload(true, ii, 0, 8055, map[string]string{"port": "8055"})
					w[ii][1] = addWorkload(true, ii, 1, 8056, nil)
				}

				// Create a workload on node 0 that does not run, but we can use it to set up paths
				deadWorkload = addWorkload(false, 0, 2, 8057, nil)

				// We will use this container to model an external client trying to connect into
				// workloads on a host.  Create a route in the container for the workload CIDR.
				// TODO: Copied from another test
				externalClient = containers.Run("external-client",
					containers.RunOpts{AutoRemove: true},
					"--privileged", // So that we can add routes inside the container.
					utils.Config.BusyboxImage,
					"/bin/sh", "-c", "sleep 1000")
				_ = externalClient

				err := infra.AddDefaultDeny()
				Expect(err).NotTo(HaveOccurred())
			})

			It("should have correct routes", func() {
				dumpRoutes := func() string {
					out, err := felixes[0].ExecOutput("calico-bpf", "routes", "dump")
					if err != nil {
						return fmt.Sprint(err)
					}

					lines := strings.Split(out, "\n")
					var filteredLines []string
					idxRE := regexp.MustCompile(`idx \d+`)
					for _, l := range lines {
						l = strings.TrimLeft(l, " ")
						if len(l) == 0 {
							continue
						}
						l = strings.ReplaceAll(l, felixes[0].IP, "FELIX_0")
						l = strings.ReplaceAll(l, felixes[1].IP, "FELIX_1")
						l = strings.ReplaceAll(l, felixes[2].IP, "FELIX_2")
						l = idxRE.ReplaceAllLiteralString(l, "idx -")
						filteredLines = append(filteredLines, l)
					}
					sort.Strings(filteredLines)
					return strings.Join(filteredLines, "\n")
				}
				Eventually(dumpRoutes).Should(Equal(expectedRoutes))
			})

			It("should only allow traffic from the local host by default", func() {
				// Same host, other workload.
				cc.ExpectNone(w[0][0], w[0][1])
				cc.ExpectNone(w[0][1], w[0][0])
				// Workloads on other host.
				cc.ExpectNone(w[0][0], w[1][0])
				cc.ExpectNone(w[1][0], w[0][0])
				// Hosts.
				cc.ExpectSome(felixes[0], w[0][0])
				cc.ExpectNone(felixes[1], w[0][0])
				cc.CheckConnectivity()
			})

			Context("with a policy allowing ingress to w[0][0] from all regular workloads", func() {
				var (
					pol       *api.GlobalNetworkPolicy
					k8sClient *kubernetes.Clientset
				)

				BeforeEach(func() {
					pol = api.NewGlobalNetworkPolicy()
					pol.Namespace = "fv"
					pol.Name = "policy-1"
					pol.Spec.Ingress = []api.Rule{
						{
							Action: "Allow",
							Source: api.EntityRule{
								Selector: "workload=='regular'",
							},
						},
					}
					pol.Spec.Egress = []api.Rule{
						{
							Action: "Allow",
							Source: api.EntityRule{
								Selector: "workload=='regular'",
							},
						},
					}
					pol.Spec.Selector = "workload=='regular'"

					pol = createPolicy(pol)

					k8sClient = infra.(*infrastructure.K8sDatastoreInfra).K8sClient
					_ = k8sClient
				})

				It("should handle NAT outgoing", func() {
					By("SNATting outgoing traffic with the flag set")
					cc.ExpectSNAT(w[0][0], felixes[0].IP, hostW[1])
					cc.CheckConnectivity()

					if testOpts.tunnel == "none" {
						By("Leaving traffic alone with the flag clear")
						pool, err := calicoClient.IPPools().Get(context.TODO(), "test-pool", options2.GetOptions{})
						Expect(err).NotTo(HaveOccurred())
						pool.Spec.NATOutgoing = false
						pool, err = calicoClient.IPPools().Update(context.TODO(), pool, options2.SetOptions{})
						Expect(err).NotTo(HaveOccurred())
						cc.ResetExpectations()
						cc.ExpectSNAT(w[0][0], w[0][0].IP, hostW[1])
						cc.CheckConnectivity()

						By("SNATting again with the flag set")
						pool.Spec.NATOutgoing = true
						pool, err = calicoClient.IPPools().Update(context.TODO(), pool, options2.SetOptions{})
						Expect(err).NotTo(HaveOccurred())
						cc.ResetExpectations()
						cc.ExpectSNAT(w[0][0], felixes[0].IP, hostW[1])
						cc.CheckConnectivity()
					}
				})

				It("connectivity from all workloads via workload 0's main IP", func() {
					cc.ExpectSome(w[0][1], w[0][0])
					cc.ExpectSome(w[1][0], w[0][0])
					cc.ExpectSome(w[1][1], w[0][0])
					cc.CheckConnectivity()
				})

				It("should not be able to spoof IP", func() {
					if testOpts.protocol != "udp" {
						return
					}

					By("allowing any traffic", func() {
						pol.Spec.Ingress = []api.Rule{
							{
								Action: "Allow",
								Source: api.EntityRule{
									Nets: []string{
										"0.0.0.0/0",
									},
								},
							},
						}
						pol = updatePolicy(pol)

						cc.ExpectSome(w[1][0], w[0][0])
						cc.ExpectSome(w[1][1], w[0][0])
						cc.CheckConnectivity()
					})

					By("testing that packet sent by another workload is dropped", func() {
						tcpdump := w[0][0].AttachTCPDump()
						tcpdump.SetLogEnabled(true)
						matcher := fmt.Sprintf("IP %s\\.30444 > %s\\.30444: UDP", w[1][0].IP, w[0][0].IP)
						tcpdump.AddMatcher("UDP-30444", regexp.MustCompile(matcher))
						tcpdump.Start(testOpts.protocol, "port", "30444", "or", "port", "30445")
						defer tcpdump.Stop()

						// send a packet from the correct workload to create a conntrack entry
						_, err := w[1][0].RunCmd("/pktgen", w[1][0].IP, w[0][0].IP, "udp",
							"--port-src", "30444", "--port-dst", "30444")
						Expect(err).NotTo(HaveOccurred())

						// We must eventually see the packet at the target
						Eventually(func() int { return tcpdump.MatchCount("UDP-30444") }).
							Should(BeNumerically("==", 1), matcher)

						// Send a spoofed packet from a different pod. Since we hit the
						// conntrack we would not do the WEP only RPF check.
						_, err = w[1][1].RunCmd("/pktgen", w[1][0].IP, w[0][0].IP, "udp",
							"--port-src", "30444", "--port-dst", "30444")
						Expect(err).NotTo(HaveOccurred())

						// Since the packet will get dropped, we would not see it at the dest.
						// So we send another good packet from the spoofing workload, that we
						// will see at the dest.
						matcher2 := fmt.Sprintf("IP %s\\.30445 > %s\\.30445: UDP", w[1][1].IP, w[0][0].IP)
						tcpdump.AddMatcher("UDP-30445", regexp.MustCompile(matcher2))

						_, err = w[1][1].RunCmd("/pktgen", w[1][1].IP, w[0][0].IP, "udp",
							"--port-src", "30445", "--port-dst", "30445")
						Expect(err).NotTo(HaveOccurred())

						// Wait for the good packet from the bad workload
						Eventually(func() int { return tcpdump.MatchCount("UDP-30445") }).
							Should(BeNumerically("==", 1), matcher2)

						// Check that we have not seen the spoofed packet. If there was not
						// packet reordering, which in out setup is guaranteed not to happen,
						// we know that the spoofed packet was dropped.
						Expect(tcpdump.MatchCount("UDP-30444")).To(BeNumerically("==", 1), matcher)
					})

					var eth20, eth30 *workload.Workload

					defer func() {
						if eth20 != nil {
							eth20.Stop()
						}
						if eth30 != nil {
							eth30.Stop()
						}
					}()

					fakeWorkloadIP := "10.65.15.15"

					By("setting up node's fake external ifaces", func() {
						// We name the ifaces ethXY since such ifaces are
						// treated by felix as external to the node
						//
						// Using a test-workload creates the namespaces and the
						// interfaces to emulate the host NICs

						eth20 = &workload.Workload{
							Name:          "eth20",
							C:             felixes[1].Container,
							IP:            "192.168.20.1",
							Ports:         "57005", // 0xdead
							Protocol:      testOpts.protocol,
							InterfaceName: "eth20",
						}
						eth20.Start()

						// assign address to eth20 and add route to the .20 network
						felixes[1].Exec("ip", "route", "add", "192.168.20.0/24", "dev", "eth20")
						felixes[1].Exec("ip", "addr", "add", "10.0.0.20/32", "dev", "eth20")
						_, err := eth20.RunCmd("ip", "route", "add", "10.0.0.20/32", "dev", "eth0")
						Expect(err).NotTo(HaveOccurred())
						// Add a route to the test workload to the fake external
						// client emulated by the test-workload
						_, err = eth20.RunCmd("ip", "route", "add", w[1][1].IP+"/32", "via", "10.0.0.20")
						Expect(err).NotTo(HaveOccurred())

						eth30 = &workload.Workload{
							Name:          "eth30",
							C:             felixes[1].Container,
							IP:            "192.168.30.1",
							Ports:         "57005", // 0xdead
							Protocol:      testOpts.protocol,
							InterfaceName: "eth30",
						}
						eth30.Start()

						// assign address to eth30 and add route to the .30 network
						felixes[1].Exec("ip", "route", "add", "192.168.30.0/24", "dev", "eth30")
						felixes[1].Exec("ip", "addr", "add", "10.0.0.30/32", "dev", "eth30")
						_, err = eth30.RunCmd("ip", "route", "add", "10.0.0.30/32", "dev", "eth0")
						Expect(err).NotTo(HaveOccurred())
						// Add a route to the test workload to the fake external
						// client emulated by the test-workload
						_, err = eth30.RunCmd("ip", "route", "add", w[1][1].IP+"/32", "via", "10.0.0.30")
						Expect(err).NotTo(HaveOccurred())

						// Make sure that networking with the .20 and .30 networks works
						cc.ResetExpectations()
						cc.ExpectSome(w[1][1], TargetIP(eth20.IP), 0xdead)
						cc.ExpectSome(w[1][1], TargetIP(eth30.IP), 0xdead)
						cc.CheckConnectivity()
					})

					By("testing that external traffic updates the RPF check if routing changes", func() {
						// set the route to the fake workload to .20 network
						felixes[1].Exec("ip", "route", "add", fakeWorkloadIP+"/32", "dev", "eth20")

						tcpdump := w[1][1].AttachTCPDump()
						tcpdump.SetLogEnabled(true)
						matcher := fmt.Sprintf("IP %s\\.30446 > %s\\.30446: UDP", fakeWorkloadIP, w[1][1].IP)
						tcpdump.AddMatcher("UDP-30446", regexp.MustCompile(matcher))
						tcpdump.Start()
						defer tcpdump.Stop()

						_, err := eth20.RunCmd("/pktgen", fakeWorkloadIP, w[1][1].IP, "udp",
							"--port-src", "30446", "--port-dst", "30446")
						Expect(err).NotTo(HaveOccurred())

						// Expect to receive the packet from the .20 as the routing is correct
						Eventually(func() int { return tcpdump.MatchCount("UDP-30446") }).
							Should(BeNumerically("==", 1), matcher)

						ctBefore := dumpCTMap(felixes[1])

						k := conntrack.NewKey(17, net.ParseIP(w[1][1].IP).To4(), 30446,
							net.ParseIP(fakeWorkloadIP).To4(), 30446)
						Expect(ctBefore).To(HaveKey(k))

						// XXX Since the same code is used to do the drop of spoofed
						// packet between pods, we do not repeat it here as it is not 100%
						// bulletproof.
						//
						// We should perhaps compare the iptables counter and see if the
						// packet was dropped by the RPF check.

						// Change the routing to be from the .30
						felixes[1].Exec("ip", "route", "del", fakeWorkloadIP+"/32", "dev", "eth20")
						felixes[1].Exec("ip", "route", "add", fakeWorkloadIP+"/32", "dev", "eth30")

						_, err = eth30.RunCmd("/pktgen", fakeWorkloadIP, w[1][1].IP, "udp",
							"--port-src", "30446", "--port-dst", "30446")
						Expect(err).NotTo(HaveOccurred())

						// Expect the packet from the .30 to make it through as RPF will
						// allow it and we will update the expected interface
						Eventually(func() int { return tcpdump.MatchCount("UDP-30446") }).
							Should(BeNumerically("==", 2), matcher)

						ctAfter := dumpCTMap(felixes[1])
						Expect(ctAfter).To(HaveKey(k))

						// Ifindex must have changed
						// B2A because of IPA > IPB - deterministic
						Expect(ctBefore[k].Data().B2A.Ifindex).NotTo(BeNumerically("==", 0))
						Expect(ctAfter[k].Data().B2A.Ifindex).NotTo(BeNumerically("==", 0))
						Expect(ctBefore[k].Data().B2A.Ifindex).
							NotTo(BeNumerically("==", ctAfter[k].Data().B2A.Ifindex))
					})
				})

				Describe("Test Load balancer service with external IP", func() {
					srcIPRange := []string{}
					externalIP := []string{extIP}
					testSvcName := "test-lb-service-extip"
					tgtPort := 8055
					var testSvc *v1.Service
					var ip []string
					var port uint16
					BeforeEach(func() {
						if testOpts.connTimeEnabled {
							Skip("FIXME externalClient also does conntime balancing")
						}
						externalClient.EnsureBinary("test-connection")
						externalClient.Exec("ip", "route", "add", extIP, "via", felixes[0].IP)
						testSvc = k8sCreateLBServiceWithEndPoints(k8sClient, testSvcName, "10.101.0.10", w[0][0], 80, tgtPort,
							testOpts.protocol, externalIP, srcIPRange)
						// when we point Load Balancer to a node in GCE it adds local routes to the external IP on the hosts.
						// Similarity add local routes for externalIP on felixes[0], felixes[1]
						felixes[1].Exec("ip", "route", "add", "local", extIP, "dev", "eth0")
						felixes[0].Exec("ip", "route", "add", "local", extIP, "dev", "eth0")
						ip = testSvc.Spec.ExternalIPs
						port = uint16(testSvc.Spec.Ports[0].Port)
						pol.Spec.Ingress = []api.Rule{
							{
								Action: "Allow",
								Source: api.EntityRule{
									Nets: []string{
										externalClient.IP + "/32",
										w[0][1].IP + "/32",
										w[1][0].IP + "/32",
										w[1][1].IP + "/32",
									},
								},
							},
						}
						pol = updatePolicy(pol)
					})

					It("should have connectivity from workloads[1][0],[1][1], [0][1] and external client via external IP to workload 0", func() {
						cc.ExpectSome(w[1][0], TargetIP(ip[0]), port)
						cc.ExpectSome(w[1][1], TargetIP(ip[0]), port)
						cc.ExpectSome(w[0][1], TargetIP(ip[0]), port)
						cc.ExpectSome(externalClient, TargetIP(ip[0]), port)
						cc.CheckConnectivity()
					})
				})

				Context("Test load balancer service with src ranges", func() {
					var testSvc *v1.Service
					tgtPort := 8055
					externalIP := []string{extIP}
					srcIPRange := []string{"10.65.1.3/24"}
					testSvcName := "test-lb-service-extip"
					var ip []string
					var port uint16
					BeforeEach(func() {
						testSvc = k8sCreateLBServiceWithEndPoints(k8sClient, testSvcName, "10.101.0.10", w[0][0], 80, tgtPort,
							testOpts.protocol, externalIP, srcIPRange)
						felixes[1].Exec("ip", "route", "add", "local", extIP, "dev", "eth0")
						felixes[0].Exec("ip", "route", "add", "local", extIP, "dev", "eth0")
						ip = testSvc.Spec.ExternalIPs
						port = uint16(testSvc.Spec.Ports[0].Port)
					})
					It("should have connectivity from workloads[1][0],[1][1] via external IP to workload 0", func() {
						cc.ExpectSome(w[1][0], TargetIP(ip[0]), port)
						cc.ExpectSome(w[1][1], TargetIP(ip[0]), port)
						cc.ExpectNone(w[0][1], TargetIP(ip[0]), port)
						cc.CheckConnectivity()
					})
				})

				Describe("Test load balancer service with external Client,src ranges", func() {
					var testSvc *v1.Service
					tgtPort := 8055
					externalIP := []string{extIP}
					testSvcName := "test-lb-service-extip"
					var ip []string
					var port uint16
					var srcIPRange []string
					BeforeEach(func() {
						if testOpts.connTimeEnabled {
							Skip("FIXME externalClient also does conntime balancing")
						}
						externalClient.Exec("ip", "route", "add", extIP, "via", felixes[0].IP)
						externalClient.EnsureBinary("test-connection")
						pol.Spec.Ingress = []api.Rule{
							{
								Action: "Allow",
								Source: api.EntityRule{
									Nets: []string{
										externalClient.IP + "/32",
									},
								},
							},
						}
						pol = updatePolicy(pol)
						felixes[1].Exec("ip", "route", "add", "local", extIP, "dev", "eth0")
						felixes[0].Exec("ip", "route", "add", "local", extIP, "dev", "eth0")
						srcIPRange = []string{"10.65.1.3/24"}
					})
					Context("Test LB-service with external Client's IP not in src range", func() {
						BeforeEach(func() {
							testSvc = k8sCreateLBServiceWithEndPoints(k8sClient, testSvcName, "10.101.0.10", w[0][0], 80, tgtPort,
								testOpts.protocol, externalIP, srcIPRange)
							ip = testSvc.Spec.ExternalIPs
							port = uint16(testSvc.Spec.Ports[0].Port)
						})
						It("should not have connectivity from external Client via external IP to workload 0", func() {
							cc.ExpectNone(externalClient, TargetIP(ip[0]), port)
							cc.CheckConnectivity()
						})
					})
					Context("Test LB-service with external Client's IP in src range", func() {
						BeforeEach(func() {
							srcIPRange = []string{externalClient.IP + "/32"}
							testSvc = k8sCreateLBServiceWithEndPoints(k8sClient, testSvcName, "10.101.0.10", w[0][0], 80, tgtPort,
								testOpts.protocol, externalIP, srcIPRange)
							ip = testSvc.Spec.ExternalIPs
							port = uint16(testSvc.Spec.Ports[0].Port)
						})
						It("should have connectivity from external Client via external IP to workload 0", func() {
							cc.ExpectSome(externalClient, TargetIP(ip[0]), port)
							cc.CheckConnectivity()
						})
					})
				})
				Context("with test-service configured 10.101.0.10:80 -> w[0][0].IP:8055", func() {
					var (
						testSvc          *v1.Service
						testSvcNamespace string
					)

					testSvcName := "test-service"
					tgtPort := 8055

					BeforeEach(func() {
						testSvc = k8sService(testSvcName, "10.101.0.10", w[0][0], 80, tgtPort, 0, testOpts.protocol)
						testSvcNamespace = testSvc.ObjectMeta.Namespace
						_, err := k8sClient.CoreV1().Services(testSvcNamespace).Create(testSvc)
						Expect(err).NotTo(HaveOccurred())
						Eventually(k8sGetEpsForServiceFunc(k8sClient, testSvc), "10s").Should(HaveLen(1),
							"Service endpoints didn't get created? Is controller-manager happy?")
					})

					It("should have connectivity from all workloads via a service to workload 0", func() {
						ip := testSvc.Spec.ClusterIP
						port := uint16(testSvc.Spec.Ports[0].Port)

						cc.ExpectSome(w[0][1], TargetIP(ip), port)
						cc.ExpectSome(w[1][0], TargetIP(ip), port)
						cc.ExpectSome(w[1][1], TargetIP(ip), port)
						cc.CheckConnectivity()
					})

					if testOpts.connTimeEnabled {
						It("workload should have connectivity to self via a service", func() {
							ip := testSvc.Spec.ClusterIP
							port := uint16(testSvc.Spec.Ports[0].Port)

							cc.ExpectSome(w[0][0], TargetIP(ip), port)
							cc.CheckConnectivity()
						})

						It("should only have connectivity from the local host via a service to workload 0", func() {
							// Local host is always white-listed (for kubelet health checks).
							ip := testSvc.Spec.ClusterIP
							port := uint16(testSvc.Spec.Ports[0].Port)

							cc.ExpectSome(felixes[0], TargetIP(ip), port)
							cc.ExpectNone(felixes[1], TargetIP(ip), port)
							cc.CheckConnectivity()
						})
					} else {
						It("should not have connectivity from the local host via a service to workload 0", func() {
							// Local host is always white-listed (for kubelet health checks).
							ip := testSvc.Spec.ClusterIP
							port := uint16(testSvc.Spec.Ports[0].Port)

							cc.ExpectNone(felixes[0], TargetIP(ip), port)
							cc.ExpectNone(felixes[1], TargetIP(ip), port)
							cc.CheckConnectivity()
						})
					}

					if testOpts.connTimeEnabled {
						Describe("after updating the policy to allow traffic from hosts", func() {
							BeforeEach(func() {
								pol.Spec.Ingress = []api.Rule{
									{
										Action: "Allow",
										Source: api.EntityRule{
											Nets: []string{
												felixes[0].IP + "/32",
												felixes[1].IP + "/32",
											},
										},
									},
								}
								switch testOpts.tunnel {
								case "ipip":
									pol.Spec.Ingress[0].Source.Nets = append(pol.Spec.Ingress[0].Source.Nets,
										felixes[0].ExpectedIPIPTunnelAddr+"/32",
										felixes[1].ExpectedIPIPTunnelAddr+"/32",
									)
								}
								pol = updatePolicy(pol)
							})

							It("should have connectivity from the hosts via a service to workload 0", func() {
								ip := testSvc.Spec.ClusterIP
								port := uint16(testSvc.Spec.Ports[0].Port)

								cc.ExpectSome(felixes[0], TargetIP(ip), port)
								cc.ExpectSome(felixes[1], TargetIP(ip), port)
								cc.ExpectNone(w[0][1], TargetIP(ip), port)
								cc.ExpectNone(w[1][0], TargetIP(ip), port)
								cc.CheckConnectivity()
							})
						})
					}

					It("should create sane conntrack entries and clean them up", func() {
						By("Generating some traffic")
						ip := testSvc.Spec.ClusterIP
						port := uint16(testSvc.Spec.Ports[0].Port)

						cc.ExpectSome(w[0][1], TargetIP(ip), port)
						cc.ExpectSome(w[1][0], TargetIP(ip), port)
						cc.CheckConnectivity()

						By("Checking timestamps on conntrack entries are sane")
						// This test verifies that we correctly interpret conntrack entry timestamps by reading them back
						// and checking that they're (a) in the past and (b) sensibly recent.
						ctDump, err := felixes[0].ExecOutput("calico-bpf", "conntrack", "dump")
						Expect(err).NotTo(HaveOccurred())
						re := regexp.MustCompile(`LastSeen:\s*(\d+)`)
						matches := re.FindAllStringSubmatch(ctDump, -1)
						Expect(matches).ToNot(BeEmpty(), "didn't find any conntrack entries")
						for _, match := range matches {
							lastSeenNanos, err := strconv.ParseInt(match[1], 10, 64)
							Expect(err).NotTo(HaveOccurred())
							nowNanos := bpf.KTimeNanos()
							age := time.Duration(nowNanos - lastSeenNanos)
							Expect(age).To(BeNumerically(">", 0))
							Expect(age).To(BeNumerically("<", 60*time.Second))
						}

						By("Checking conntrack entries are cleaned up")
						// We have UTs that check that all kinds of entries eventually get cleaned up.  This
						// test is mainly to check that the cleanup code actually runs and is able to actually delete
						// entries.
						numWl0ConntrackEntries := func() int {
							ctDump, err := felixes[0].ExecOutput("calico-bpf", "conntrack", "dump")
							Expect(err).NotTo(HaveOccurred())
							return strings.Count(ctDump, w[0][0].IP)
						}

						startingCTEntries := numWl0ConntrackEntries()
						Expect(startingCTEntries).To(BeNumerically(">", 0))

						// TODO reduce timeouts just for this test.
						Eventually(numWl0ConntrackEntries, "180s", "5s").Should(BeNumerically("<", startingCTEntries))
					})

					Context("with test-service port updated", func() {

						var (
							testSvcUpdated      *v1.Service
							natBackBeforeUpdate []nat.BackendMapMem
							natBeforeUpdate     []nat.MapMem
						)

						BeforeEach(func() {
							ip := testSvc.Spec.ClusterIP
							portOld := uint16(testSvc.Spec.Ports[0].Port)
							ipv4 := net.ParseIP(ip)
							oldK := nat.NewNATKey(ipv4, portOld, numericProto)

							// Wait for the NAT maps to converge...
							log.Info("Waiting for NAT maps to converge...")
							startTime := time.Now()
							for {
								if time.Since(startTime) > 5*time.Second {
									Fail("NAT maps failed to converge")
								}
								natBeforeUpdate, natBackBeforeUpdate = dumpNATmaps(felixes)
								for i, m := range natBeforeUpdate {
									if natV, ok := m[oldK]; !ok {
										goto retry
									} else {
										bckCnt := natV.Count()
										if bckCnt != 1 {
											log.Debugf("Expected single backend, not %d; retrying...", bckCnt)
											goto retry
										}
										bckID := natV.ID()
										bckK := nat.NewNATBackendKey(bckID, 0)
										if _, ok := natBackBeforeUpdate[i][bckK]; !ok {
											log.Debugf("Backend not found %v; retrying...", bckK)
											goto retry
										}
									}
								}

								break
							retry:
								time.Sleep(100 * time.Millisecond)
							}
							log.Info("NAT maps converged.")

							testSvcUpdated = k8sService(testSvcName, "10.101.0.10", w[0][0], 88, 8055, 0, testOpts.protocol)

							svc, err := k8sClient.CoreV1().
								Services(testSvcNamespace).
								Get(testSvcName, metav1.GetOptions{})

							testSvcUpdated.ObjectMeta.ResourceVersion = svc.ObjectMeta.ResourceVersion

							_, err = k8sClient.CoreV1().Services(testSvcNamespace).Update(testSvcUpdated)
							Expect(err).NotTo(HaveOccurred())
							Eventually(k8sGetEpsForServiceFunc(k8sClient, testSvc), "10s").Should(HaveLen(1),
								"Service endpoints didn't get created? Is controller-manager happy?")
						})

						It("should have connectivity from all workloads via the new port", func() {
							ip := testSvcUpdated.Spec.ClusterIP
							port := uint16(testSvcUpdated.Spec.Ports[0].Port)

							cc.ExpectSome(w[0][1], TargetIP(ip), port)
							cc.ExpectSome(w[1][0], TargetIP(ip), port)
							cc.ExpectSome(w[1][1], TargetIP(ip), port)
							cc.CheckConnectivity()
						})

						It("should not have connectivity from all workloads via the old port", func() {
							ip := testSvc.Spec.ClusterIP
							port := uint16(testSvc.Spec.Ports[0].Port)

							cc.ExpectNone(w[0][1], TargetIP(ip), port)
							cc.ExpectNone(w[1][0], TargetIP(ip), port)
							cc.ExpectNone(w[1][1], TargetIP(ip), port)
							cc.CheckConnectivity()

							natmaps, natbacks := dumpNATmaps(felixes)
							ipv4 := net.ParseIP(ip)
							portOld := uint16(testSvc.Spec.Ports[0].Port)
							oldK := nat.NewNATKey(ipv4, portOld, numericProto)
							portNew := uint16(testSvcUpdated.Spec.Ports[0].Port)
							natK := nat.NewNATKey(ipv4, portNew, numericProto)

							for i := range felixes {
								Expect(natmaps[i]).To(HaveKey(natK))
								Expect(natmaps[i]).NotTo(HaveKey(nat.NewNATKey(ipv4, portOld, numericProto)))

								Expect(natBeforeUpdate[i]).To(HaveKey(oldK))
								oldV := natBeforeUpdate[i][oldK]

								natV := natmaps[i][natK]
								bckCnt := natV.Count()
								bckID := natV.ID()

								log.WithField("backCnt", bckCnt).Debug("Backend count.")
								for ord := uint32(0); ord < uint32(bckCnt); ord++ {
									bckK := nat.NewNATBackendKey(bckID, ord)
									oldBckK := nat.NewNATBackendKey(oldV.ID(), ord)
									Expect(natbacks[i]).To(HaveKey(bckK))
									Expect(natBackBeforeUpdate[i]).To(HaveKey(oldBckK))
									Expect(natBackBeforeUpdate[i][oldBckK]).To(Equal(natbacks[i][bckK]))
								}

							}
						})

						It("after removing service, should not have connectivity from workloads via a service to workload 0", func() {
							ip := testSvcUpdated.Spec.ClusterIP
							port := uint16(testSvcUpdated.Spec.Ports[0].Port)
							natK := nat.NewNATKey(net.ParseIP(ip), port, numericProto)
							var prevBpfsvcs []nat.MapMem
							Eventually(func() bool {
								prevBpfsvcs, _ = dumpNATmaps(felixes)
								for _, m := range prevBpfsvcs {
									if _, ok := m[natK]; !ok {
										return false
									}
								}
								return true
							}, "5s").Should(BeTrue(), "service NAT key didn't show up")

							err := k8sClient.CoreV1().
								Services(testSvcNamespace).
								Delete(testSvcName, &metav1.DeleteOptions{})
							Expect(err).NotTo(HaveOccurred())
							Eventually(k8sGetEpsForServiceFunc(k8sClient, testSvc), "10s").Should(HaveLen(0))

							cc.ExpectNone(w[0][1], TargetIP(ip), port)
							cc.ExpectNone(w[1][0], TargetIP(ip), port)
							cc.ExpectNone(w[1][1], TargetIP(ip), port)
							cc.CheckConnectivity()

							for i, f := range felixes {
								natV := prevBpfsvcs[i][natK]
								bckCnt := natV.Count()
								bckID := natV.ID()

								Eventually(func() bool {
									svcs := dumpNATMap(f)
									eps := dumpEPMap(f)

									if _, ok := svcs[natK]; ok {
										return false
									}

									for ord := uint32(0); ord < uint32(bckCnt); ord++ {
										bckK := nat.NewNATBackendKey(bckID, ord)
										if _, ok := eps[bckK]; ok {
											return false
										}
									}

									return true
								}, "5s").Should(BeTrue(), "service NAT key wasn't removed correctly")
							}
						})
					})
				})

				Context("with test-service configured 10.101.0.10:80 -> w[*][0].IP:8055 and affinity", func() {
					var (
						testSvc          *v1.Service
						testSvcNamespace string
					)

					testSvcName := "test-service"

					BeforeEach(func() {
						testSvc = k8sService(testSvcName, "10.101.0.10", w[0][0], 80, 8055, 0, testOpts.protocol)
						testSvcNamespace = testSvc.ObjectMeta.Namespace
						// select all pods with port 8055
						testSvc.Spec.Selector = map[string]string{"port": "8055"}
						testSvc.Spec.SessionAffinity = "ClientIP"
						_, err := k8sClient.CoreV1().Services(testSvcNamespace).Create(testSvc)
						Expect(err).NotTo(HaveOccurred())
						Eventually(k8sGetEpsForServiceFunc(k8sClient, testSvc), "10s").Should(HaveLen(1),
							"Service endpoints didn't get created? Is controller-manager happy?")
					})

					It("should have connectivity from a workload to a service with multiple backends", func() {
						ip := testSvc.Spec.ClusterIP
						port := uint16(testSvc.Spec.Ports[0].Port)

						cc.ExpectSome(w[1][1], TargetIP(ip), port)
						cc.ExpectSome(w[1][1], TargetIP(ip), port)
						cc.ExpectSome(w[1][1], TargetIP(ip), port)
						cc.CheckConnectivity()

						if !testOpts.connTimeEnabled {
							// FIXME we can only do the test with regular NAT as
							// cgroup shares one random affinity map
							aff := dumpAffMap(felixes[1])
							Expect(aff).To(HaveLen(1))
						}
					})
				})

				npPort := uint16(30333)

				nodePortsTest := func(localOnly bool) {
					var (
						testSvc          *v1.Service
						testSvcNamespace string
					)

					testSvcName := "test-service"

					BeforeEach(func() {
						k8sClient := infra.(*infrastructure.K8sDatastoreInfra).K8sClient
						testSvc = k8sService(testSvcName, "10.101.0.10",
							w[0][0], 80, 8055, int32(npPort), testOpts.protocol)
						if localOnly {
							testSvc.Spec.ExternalTrafficPolicy = "Local"
						}
						testSvcNamespace = testSvc.ObjectMeta.Namespace
						_, err := k8sClient.CoreV1().Services(testSvcNamespace).Create(testSvc)
						Expect(err).NotTo(HaveOccurred())
						Eventually(k8sGetEpsForServiceFunc(k8sClient, testSvc), "10s").Should(HaveLen(1),
							"Service endpoints didn't get created? Is controller-manager happy?")
					})

					It("should have connectivity from all workloads via a service to workload 0", func() {
						clusterIP := testSvc.Spec.ClusterIP
						port := uint16(testSvc.Spec.Ports[0].Port)

						cc.ExpectSome(w[0][1], TargetIP(clusterIP), port)
						cc.ExpectSome(w[1][0], TargetIP(clusterIP), port)
						cc.ExpectSome(w[1][1], TargetIP(clusterIP), port)
						cc.CheckConnectivity()
					})

					if localOnly {
						It("should not have connectivity from all workloads via a nodeport to non-local workload 0", func() {
							node0IP := felixes[0].IP
							node1IP := felixes[1].IP
							// Via remote nodeport, should fail.
							cc.ExpectNone(w[0][1], TargetIP(node1IP), npPort)
							cc.ExpectNone(w[1][0], TargetIP(node1IP), npPort)
							cc.ExpectNone(w[1][1], TargetIP(node1IP), npPort)
							// Include a check that goes via the local nodeport to make sure the dataplane has converged.
							cc.ExpectSome(w[0][1], TargetIP(node0IP), npPort)
							cc.CheckConnectivity()
						})
					} else {
						It("should have connectivity from all workloads via a nodeport to workload 0", func() {
							node1IP := felixes[1].IP

							cc.ExpectSome(w[0][1], TargetIP(node1IP), npPort)
							cc.ExpectSome(w[1][0], TargetIP(node1IP), npPort)
							cc.ExpectSome(w[1][1], TargetIP(node1IP), npPort)
							cc.CheckConnectivity()
						})
					}

					if !localOnly {
						It("should have connectivity from a workload via a nodeport on another node to workload 0", func() {
							ip := felixes[1].IP

							cc.ExpectSome(w[2][1], TargetIP(ip), npPort)
							cc.CheckConnectivity()

						})
					}

					It("workload should have connectivity to self via local/remote node", func() {
						if !testOpts.connTimeEnabled {
							Skip("FIXME pod cannot connect to self without connect time lb")
						}
						cc.ExpectSome(w[0][0], TargetIP(felixes[1].IP), npPort)
						cc.ExpectSome(w[0][0], TargetIP(felixes[0].IP), npPort)
						cc.CheckConnectivity()
					})

					It("should not have connectivity from external to w[0] via local/remote node", func() {
						cc.ExpectNone(externalClient, TargetIP(felixes[1].IP), npPort)
						cc.ExpectNone(externalClient, TargetIP(felixes[0].IP), npPort)
						// Include a check that goes via the local nodeport to make sure the dataplane has converged.
						cc.ExpectSome(w[0][1], TargetIP(felixes[0].IP), npPort)
						cc.CheckConnectivity()
					})

					Describe("after updating the policy to allow traffic from externalClient", func() {
						BeforeEach(func() {
							pol.Spec.Ingress = []api.Rule{
								{
									Action: "Allow",
									Source: api.EntityRule{
										Nets: []string{
											externalClient.IP + "/32",
										},
									},
								},
							}
							pol = updatePolicy(pol)
						})

						if localOnly {
							It("should not have connectivity from external to w[0] via node1->node0 fwd", func() {
								if testOpts.connTimeEnabled {
									Skip("FIXME externalClient also does conntime balancing")
								}

								cc.ExpectNone(externalClient, TargetIP(felixes[1].IP), npPort)
								// Include a check that goes via the nodeport with a local backing pod to make sure the dataplane has converged.
								cc.ExpectSome(externalClient, TargetIP(felixes[0].IP), npPort)
								cc.CheckConnectivity()
							})
						} else {
							It("should have connectivity from external to w[0] via node1->node0 fwd", func() {
								if testOpts.connTimeEnabled {
									Skip("FIXME externalClient also does conntime balancing")
								}

								cc.ExpectSome(externalClient, TargetIP(felixes[1].IP), npPort)
								cc.CheckConnectivity()
							})

							It("should have connectivity from external to w[0] via node1IP2 -> nodeIP1 -> node0 fwd", func() {
								// 192.168.20.1              +----------|---------+
								//      |                    |          |         |
								//      v                    |          |         V
								//    eth20                 eth0        |       eth0
								//  10.0.0.20:30333 --> felixes[1].IP   |   felixes[0].IP
								//                                      |        |
								//                                      |        V
								//                                      |     caliXYZ
								//                                      |    w[0][0].IP:8055
								//                                      |
								//                node1                 |      node0

								if testOpts.dsr {
									return
									// When DSR is enabled, we need to have away how to pass the
									// original traffic back.
									//
									// felixes[0].Exec("ip", "route", "add", "192.168.20.0/24", "via", felixes[1].IP)
									//
									// This does not work since the other node would treat it as
									// DNAT due to the existing CT entries and NodePort traffix
									// otherwise :-/
								}

								if testOpts.connTimeEnabled {
									Skip("FIXME externalClient also does conntime balancing")
								}

								var eth20 *workload.Workload

								defer func() {
									if eth20 != nil {
										eth20.Stop()
									}
								}()

								By("setting up node's fake external iface", func() {
									// We name the iface eth20 since such ifaces are
									// treated by felix as external to the node
									//
									// Using a test-workload creates the namespaces and the
									// interfaces to emulate the host NICs

									eth20 = &workload.Workload{
										Name:          "eth20",
										C:             felixes[1].Container,
										IP:            "192.168.20.1",
										Ports:         "57005", // 0xdead
										Protocol:      testOpts.protocol,
										InterfaceName: "eth20",
									}
									eth20.Start()

									// assign address to eth20 and add route to the .20 network
									felixes[1].Exec("ip", "route", "add", "192.168.20.0/24", "dev", "eth20")
									felixes[1].Exec("ip", "addr", "add", "10.0.0.20/32", "dev", "eth20")
									_, err := eth20.RunCmd("ip", "route", "add", "10.0.0.20/32", "dev", "eth0")
									Expect(err).NotTo(HaveOccurred())
									// Add a route to felix[1] to be able to reach the nodeport
									_, err = eth20.RunCmd("ip", "route", "add", felixes[1].IP+"/32", "via", "10.0.0.20")
									Expect(err).NotTo(HaveOccurred())
									// This multi-NIC scenario works only if the kernel's RPF check
									// is not strict so we need to override it for the test and must
									// be set properly when product is deployed. We reply on
									// iptables to do require check for us.
									felixes[1].Exec("sysctl", "-w", "net.ipv4.conf.eth0.rp_filter=2")
									felixes[1].Exec("sysctl", "-w", "net.ipv4.conf.eth20.rp_filter=2")
								})

								By("setting up routes to .20 net on dest node to trigger RPF check", func() {
									// set up a dummy interface just for the routing purpose
									felixes[0].Exec("ip", "link", "add", "dummy1", "type", "dummy")
									felixes[0].Exec("ip", "link", "set", "dummy1", "up")
									// set up route to the .20 net through the dummy iface. This
									// makes the .20 a universaly reachable external world from the
									// internal/private eth0 network
									felixes[0].Exec("ip", "route", "add", "192.168.20.0/24", "dev", "dummy1")
									// This multi-NIC scenario works only if the kernel's RPF check
									// is not strict so we need to override it for the test and must
									// be set properly when product is deployed. We reply on
									// iptables to do require check for us.
									felixes[0].Exec("sysctl", "-w", "net.ipv4.conf.eth0.rp_filter=2")
									felixes[0].Exec("sysctl", "-w", "net.ipv4.conf.dummy1.rp_filter=2")
								})

								By("Allowing traffic from the eth20 network", func() {
									pol.Spec.Ingress = []api.Rule{
										{
											Action: "Allow",
											Source: api.EntityRule{
												Nets: []string{
													eth20.IP + "/32",
												},
											},
										},
									}
									pol = updatePolicy(pol)
								})

								By("Checking that there is connectivity from eth20 network", func() {

									cc.ExpectSome(eth20, TargetIP(felixes[1].IP), npPort)
									cc.CheckConnectivity()
								})
							})

							if testOpts.protocol == "tcp" && !testOpts.connTimeEnabled {

								const (
									npEncapOverhead = 50
									hostIfaceMTU    = 1500
									podIfaceMTU     = 1410
									sendLen         = hostIfaceMTU
									recvLen         = podIfaceMTU - npEncapOverhead
								)

								Context("with TCP, tx/rx close to MTU size on NP via node1->node0 ", func() {

									negative := ""
									adjusteMTU := podIfaceMTU - npEncapOverhead
									if testOpts.dsr {
										negative = "not "
										adjusteMTU = 0
									}

									It("should "+negative+"adjust MTU on workload side", func() {
										// force non-GSO packets when workload replies
										_, err := w[0][0].RunCmd("ethtool", "-K", "eth0", "gso", "off")
										Expect(err).NotTo(HaveOccurred())
										_, err = w[0][0].RunCmd("ethtool", "-K", "eth0", "tso", "off")
										Expect(err).NotTo(HaveOccurred())

										pmtu, err := w[0][0].PathMTU(externalClient.IP)
										Expect(err).NotTo(HaveOccurred())
										Expect(pmtu).To(Equal(0)) // nothing specific for this path yet

										port := []uint16{npPort}
										cc.ExpectConnectivity(externalClient, TargetIP(felixes[1].IP), port,
											ExpectWithSendLen(sendLen),
											ExpectWithRecvLen(recvLen),
											ExpectWithClientAdjustedMTU(hostIfaceMTU, hostIfaceMTU),
										)
										cc.CheckConnectivity()

										pmtu, err = w[0][0].PathMTU(externalClient.IP)
										Expect(err).NotTo(HaveOccurred())
										Expect(pmtu).To(Equal(adjusteMTU))
									})

									It("should not adjust MTU on client side if GRO off on nodes", func() {
										// force non-GSO packets on node ingress
										err := felixes[1].ExecMayFail("ethtool", "-K", "eth0", "gro", "off")
										Expect(err).NotTo(HaveOccurred())

										port := []uint16{npPort}
										cc.ExpectConnectivity(externalClient, TargetIP(felixes[1].IP), port,
											ExpectWithSendLen(sendLen),
											ExpectWithRecvLen(recvLen),
											ExpectWithClientAdjustedMTU(hostIfaceMTU, hostIfaceMTU),
										)
										cc.CheckConnectivity()
									})
								})
							}
						}

						It("should have connectivity from external to w[0] via node0", func() {
							if testOpts.connTimeEnabled {
								Skip("FIXME externalClient also does conntime balancing")
							}

							log.WithFields(log.Fields{
								"externalClientIP": externalClient.IP,
								"nodePortIP":       felixes[1].IP,
							}).Infof("external->nodeport connection")

							cc.ExpectSome(externalClient, TargetIP(felixes[0].IP), npPort)
							cc.CheckConnectivity()
						})
					})
				}

				Context("with test-service being a nodeport @ "+strconv.Itoa(int(npPort)), func() {
					nodePortsTest(false)
				})

				// FIXME connect time shares the same NAT table and it is a lottery which one it gets
				if !testOpts.connTimeEnabled {
					Context("with test-service being a nodeport @ "+strconv.Itoa(int(npPort))+
						" ExternalTrafficPolicy=local", func() {
						nodePortsTest(true)
					})
				}

				Context("with icmp blocked from workloads, external client", func() {
					var (
						testSvc          *v1.Service
						testSvcNamespace string
					)

					testSvcName := "test-service"

					BeforeEach(func() {
						icmpProto := numorstring.ProtocolFromString("icmp")
						pol.Spec.Ingress = []api.Rule{
							{
								Action: "Allow",
								Source: api.EntityRule{
									Nets: []string{"0.0.0.0/0"},
								},
							},
						}
						pol.Spec.Egress = []api.Rule{
							{
								Action: "Allow",
								Source: api.EntityRule{
									Nets: []string{"0.0.0.0/0"},
								},
							},
							{
								Action:   "Deny",
								Protocol: &icmpProto,
							},
						}
						pol = updatePolicy(pol)
					})

					var tgtPort int
					var tgtWorkload *workload.Workload

					JustBeforeEach(func() {
						k8sClient := infra.(*infrastructure.K8sDatastoreInfra).K8sClient
						testSvc = k8sService(testSvcName, "10.101.0.10",
							tgtWorkload, 80, tgtPort, int32(npPort), testOpts.protocol)
						testSvcNamespace = testSvc.ObjectMeta.Namespace
						_, err := k8sClient.CoreV1().Services(testSvcNamespace).Create(testSvc)
						Expect(err).NotTo(HaveOccurred())
						Eventually(k8sGetEpsForServiceFunc(k8sClient, testSvc), "10s").Should(HaveLen(1),
							"Service endpoints didn't get created? Is controller-manager happy?")

						// sync with NAT table being applied
						natFtKey := nat.NewNATKey(net.ParseIP(felixes[1].IP), npPort, numericProto)
						Eventually(func() bool {
							m := dumpNATMap(felixes[1])
							v, ok := m[natFtKey]
							return ok && v.Count() > 0
						}, 5*time.Second).Should(BeTrue())

						// Sync with policy
						cc.ExpectSome(w[1][0], w[0][0])
						cc.CheckConnectivity()
					})

					Describe("with dead workload", func() {
						BeforeEach(func() {
							tgtPort = 8057
							tgtWorkload = deadWorkload
						})

						It("should get host unreachable from nodeport via node1->node0 fwd", func() {
							if testOpts.connTimeEnabled {
								Skip("FIXME externalClient also does conntime balancing")
							}

							err := felixes[0].ExecMayFail("ip", "route", "add", "unreachable", deadWorkload.IP)
							Expect(err).NotTo(HaveOccurred())

							tcpdump := externalClient.AttachTCPDump("any")
							tcpdump.SetLogEnabled(true)
							matcher := fmt.Sprintf("IP %s > %s: ICMP host %s unreachable",
								felixes[1].IP, externalClient.IP, felixes[1].IP)
							tcpdump.AddMatcher("ICMP", regexp.MustCompile(matcher))
							tcpdump.Start(testOpts.protocol, "port", strconv.Itoa(int(npPort)), "or", "icmp")
							defer tcpdump.Stop()

							cc.ExpectNone(externalClient, TargetIP(felixes[1].IP), npPort)
							cc.CheckConnectivity()

							Eventually(func() int { return tcpdump.MatchCount("ICMP") }).
								Should(BeNumerically(">", 0), matcher)
						})
					})

					Describe("with wrong target port", func() {
						// TCP would send RST instead of ICMP, it is enough to test one way of
						// triggering the ICMP message
						if testOpts.protocol != "udp" {
							return
						}

						BeforeEach(func() {
							tgtPort = 0xdead
							tgtWorkload = w[0][0]
						})

						It("should get port unreachable via node1->node0 fwd", func() {
							if testOpts.connTimeEnabled {
								Skip("FIXME externalClient also does conntime balancing")
							}

							tcpdump := externalClient.AttachTCPDump("any")
							tcpdump.SetLogEnabled(true)
							matcher := fmt.Sprintf("IP %s > %s: ICMP %s udp port %d unreachable",
								felixes[1].IP, externalClient.IP, felixes[1].IP, npPort)
							tcpdump.AddMatcher("ICMP", regexp.MustCompile(matcher))
							tcpdump.Start(testOpts.protocol, "port", strconv.Itoa(int(npPort)), "or", "icmp")
							defer tcpdump.Stop()

							cc.ExpectNone(externalClient, TargetIP(felixes[1].IP), npPort)
							cc.CheckConnectivity()
							Eventually(func() int { return tcpdump.MatchCount("ICMP") }).
								Should(BeNumerically(">", 0), matcher)
						})

						It("should get port unreachable workload to workload", func() {
							tcpdump := w[1][1].AttachTCPDump()
							tcpdump.SetLogEnabled(true)
							matcher := fmt.Sprintf("IP %s > %s: ICMP %s udp port %d unreachable",
								tgtWorkload.IP, w[1][1].IP, tgtWorkload.IP, tgtPort)
							tcpdump.AddMatcher("ICMP", regexp.MustCompile(matcher))
							tcpdump.Start(testOpts.protocol, "port", strconv.Itoa(tgtPort), "or", "icmp")
							defer tcpdump.Stop()

							cc.ExpectNone(w[1][1], TargetIP(tgtWorkload.IP), uint16(tgtPort))
							cc.CheckConnectivity()
							Eventually(func() int { return tcpdump.MatchCount("ICMP") }).
								Should(BeNumerically(">", 0), matcher)
						})

						It("should get port unreachable workload to workload through NP", func() {
							tcpdump := w[1][1].AttachTCPDump()
							tcpdump.SetLogEnabled(true)

							var matcher string

							if testOpts.connTimeEnabled {
								matcher = fmt.Sprintf("IP %s > %s: ICMP %s udp port %d unreachable",
									tgtWorkload.IP, w[1][1].IP, w[0][0].IP, tgtPort)
								tcpdump.AddMatcher("ICMP", regexp.MustCompile(matcher))
								tcpdump.Start(testOpts.protocol, "port", strconv.Itoa(tgtPort), "or", "icmp")
							} else {
								matcher = fmt.Sprintf("IP %s > %s: ICMP %s udp port %d unreachable",
									tgtWorkload.IP, w[1][1].IP, felixes[1].IP, npPort)
								tcpdump.AddMatcher("ICMP", regexp.MustCompile(matcher))
								tcpdump.Start(testOpts.protocol, "port", strconv.Itoa(int(npPort)), "or", "icmp")
							}
							defer tcpdump.Stop()

							cc.ExpectNone(w[1][1], TargetIP(felixes[1].IP), npPort)
							cc.CheckConnectivity()
							Eventually(func() int { return tcpdump.MatchCount("ICMP") }).
								Should(BeNumerically(">", 0), matcher)
						})
					})
				})
			})
		})
	})
}

func typeMetaV1(kind string) metav1.TypeMeta {
	return metav1.TypeMeta{
		Kind:       kind,
		APIVersion: "v1",
	}
}

func objectMetaV1(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: "default",
	}
}

func dumpNATmaps(felixes []*infrastructure.Felix) ([]nat.MapMem, []nat.BackendMapMem) {
	bpfsvcs := make([]nat.MapMem, len(felixes))
	bpfeps := make([]nat.BackendMapMem, len(felixes))

	for i, felix := range felixes {
		bpfsvcs[i], bpfeps[i] = dumpNATMaps(felix)
	}

	return bpfsvcs, bpfeps
}

func dumpNATMaps(felix *infrastructure.Felix) (nat.MapMem, nat.BackendMapMem) {
	return dumpNATMap(felix), dumpEPMap(felix)
}

func dumpBPFMap(felix *infrastructure.Felix, m bpf.Map, iter bpf.MapIter) {
	// Wait for the map to exist before trying to access it.  Otherwise, we
	// might fail a test that was retrying this dump anyway.
	Eventually(func() bool {
		return felix.FileExists(m.Path())
	}).Should(BeTrue(), fmt.Sprintf("dumpBPFMap: map %s didn't show up inside container", m.Path()))
	cmd, err := bpf.DumpMapCmd(m)
	Expect(err).NotTo(HaveOccurred(), "Failed to get BPF map dump command: "+m.Path())
	log.WithField("cmd", cmd).Debug("dumpBPFMap")
	out, err := felix.ExecOutput(cmd...)
	Expect(err).NotTo(HaveOccurred(), "Failed to get dump BPF map: "+m.Path())
	err = bpf.IterMapCmdOutput([]byte(out), iter)
	Expect(err).NotTo(HaveOccurred(), "Failed to parse BPF map dump: "+m.Path())
}

func dumpNATMap(felix *infrastructure.Felix) nat.MapMem {
	bm := nat.FrontendMap(&bpf.MapContext{})
	m := make(nat.MapMem)
	dumpBPFMap(felix, bm, nat.MapMemIter(m))
	return m
}

func dumpEPMap(felix *infrastructure.Felix) nat.BackendMapMem {
	bm := nat.BackendMap(&bpf.MapContext{})
	m := make(nat.BackendMapMem)
	dumpBPFMap(felix, bm, nat.BackendMapMemIter(m))
	return m
}

func dumpAffMap(felix *infrastructure.Felix) nat.AffinityMapMem {
	bm := nat.AffinityMap(&bpf.MapContext{})
	m := make(nat.AffinityMapMem)
	dumpBPFMap(felix, bm, nat.AffinityMapMemIter(m))
	return m
}

func dumpCTMap(felix *infrastructure.Felix) conntrack.MapMem {
	bm := conntrack.Map(&bpf.MapContext{})
	m := make(conntrack.MapMem)
	dumpBPFMap(felix, bm, conntrack.MapMemIter(m))
	return m
}

func dumpSendRecvMap(felix *infrastructure.Felix) nat.SendRecvMsgMapMem {
	bm := nat.SendRecvMsgMap(&bpf.MapContext{})
	m := make(nat.SendRecvMsgMapMem)
	dumpBPFMap(felix, bm, nat.SendRecvMsgMapMemIter(m))
	return m
}

func k8sService(name, clusterIP string, w *workload.Workload, port,
	tgtPort int, nodePort int32, protocol string) *v1.Service {
	k8sProto := v1.ProtocolTCP
	if protocol == "udp" {
		k8sProto = v1.ProtocolUDP
	}

	svcType := v1.ServiceTypeClusterIP
	if nodePort != 0 {
		svcType = v1.ServiceTypeNodePort
	}

	return &v1.Service{
		TypeMeta:   typeMetaV1("Service"),
		ObjectMeta: objectMetaV1(name),
		Spec: v1.ServiceSpec{
			ClusterIP: clusterIP,
			Type:      svcType,
			Selector: map[string]string{
				"name": w.Name,
			},
			Ports: []v1.ServicePort{
				{
					Protocol:   k8sProto,
					Port:       int32(port),
					NodePort:   nodePort,
					Name:       fmt.Sprintf("port-%d", tgtPort),
					TargetPort: intstr.FromInt(tgtPort),
				},
			},
		},
	}
}

func k8sLBService(name, clusterIP string, w *workload.Workload, port,
	tgtPort int, protocol string, externalIPs, srcRange []string) *v1.Service {
	k8sProto := v1.ProtocolTCP
	if protocol == "udp" {
		k8sProto = v1.ProtocolUDP
	}

	svcType := v1.ServiceTypeLoadBalancer
	return &v1.Service{
		TypeMeta:   typeMetaV1("Service"),
		ObjectMeta: objectMetaV1(name),
		Spec: v1.ServiceSpec{
			ClusterIP:                clusterIP,
			Type:                     svcType,
			LoadBalancerSourceRanges: srcRange,
			ExternalIPs:              externalIPs,
			Selector: map[string]string{
				"name": w.Name,
			},
			Ports: []v1.ServicePort{
				{
					Protocol:   k8sProto,
					Port:       int32(port),
					Name:       fmt.Sprintf("port-%d", tgtPort),
					TargetPort: intstr.FromInt(tgtPort),
				},
			},
		},
	}
}

func k8sGetEpsForService(k8s kubernetes.Interface, svc *v1.Service) []v1.EndpointSubset {
	ep, _ := k8s.CoreV1().
		Endpoints(svc.ObjectMeta.Namespace).
		Get(svc.ObjectMeta.Name, metav1.GetOptions{})
	log.WithField("endpoints",
		spew.Sprint(ep)).Infof("Got endpoints for %s", svc.ObjectMeta.Name)
	return ep.Subsets
}

func k8sGetEpsForServiceFunc(k8s kubernetes.Interface, svc *v1.Service) func() []v1.EndpointSubset {
	return func() []v1.EndpointSubset {
		return k8sGetEpsForService(k8s, svc)
	}
}

func k8sCreateLBServiceWithEndPoints(k8sClient kubernetes.Interface, name, clusterIP string, w *workload.Workload, port,
	tgtPort int, protocol string, externalIPs, srcRange []string) *v1.Service {
	var (
		testSvc          *v1.Service
		testSvcNamespace string
	)

	testSvc = k8sLBService(name, clusterIP, w, 80, tgtPort, protocol, externalIPs, srcRange)
	testSvcNamespace = testSvc.ObjectMeta.Namespace
	_, err := k8sClient.CoreV1().Services(testSvcNamespace).Create(testSvc)
	Expect(err).NotTo(HaveOccurred())
	Eventually(k8sGetEpsForServiceFunc(k8sClient, testSvc), "10s").Should(HaveLen(1),
		"Service endpoints didn't get created? Is controller-manager happy?")
	return testSvc
}
