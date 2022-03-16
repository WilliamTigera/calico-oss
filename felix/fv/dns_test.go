//go:build fvtests
// +build fvtests

// Copyright (c) 2019-2022 Tigera, Inc. All rights reserved.

package fv_test

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	api "github.com/tigera/api/pkg/apis/projectcalico/v3"
	"github.com/tigera/api/pkg/lib/numorstring"

	"github.com/projectcalico/calico/felix/fv/containers"
	"github.com/projectcalico/calico/felix/fv/dns"
	"github.com/projectcalico/calico/felix/fv/infrastructure"
	"github.com/projectcalico/calico/felix/fv/utils"
	"github.com/projectcalico/calico/felix/fv/workload"
	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/felix/rules"
	client "github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/calico/libcalico-go/lib/options"
)

const nameserverPrefix = "nameserver "

var localNameservers []string

func GetLocalNameservers() (nameservers []string) {
	if localNameservers == nil {
		// Find out what Docker puts in a container's /etc/resolv.conf.
		resolvConf, err := utils.GetCommandOutput("docker", "run", "--rm", utils.Config.FelixImage, "cat", "/etc/resolv.conf")
		Expect(err).NotTo(HaveOccurred())
		for _, resolvConfLine := range strings.Split(resolvConf, "\n") {
			if strings.HasPrefix(resolvConfLine, nameserverPrefix) {
				localNameservers = append(localNameservers, strings.TrimSpace(resolvConfLine[len(nameserverPrefix):]))
			}
		}
		log.Infof("Discovered nameservers: %v", localNameservers)
	}
	return localNameservers
}

func getDNSLogs(logFile string) ([]string, error) {
	fileExists, err := BeARegularFile().Match(logFile)
	if err != nil {
		return nil, err
	}
	if !fileExists {
		return nil, fmt.Errorf("Expected DNS log file %v does not exist", logFile)
	}
	logBytes, err := ioutil.ReadFile(logFile)
	if err != nil {
		return nil, err
	}
	var logs []string
	for _, log := range strings.Split(string(logBytes), "\n") {
		// Filter out empty strings returned by strings.Split.
		if log != "" {
			logs = append(logs, log)
		}
	}
	return logs, nil
}

var _ = Describe("_BPF-SAFE_ DNS Policy", func() {

	var (
		etcd        *containers.Container
		felix       *infrastructure.Felix
		client      client.Interface
		infra       infrastructure.DatastoreInfra
		w           [1]*workload.Workload
		dnsDir      string
		dnsServerIP string
		// Path to the save file from the point of view inside the Felix container.
		// (Whereas dnsDir is the directory outside the container.)
		saveFile                       string
		saveFileMappedOutsideContainer bool

		enableLogs    bool
		enableLatency bool
		dnsMode       string
	)

	logAndReport := func(out string, err error) error {
		log.WithError(err).Infof("test-dns said:\n%v", out)
		return err
	}

	wgetMicrosoftErr := func() error {
		w[0].C.EnsureBinary("test-dns")
		out, err := w[0].ExecCombinedOutput("/test-dns", "-", "microsoft.com", fmt.Sprintf("--dns-server=%s:%d", dnsServerIP, 53))
		return logAndReport(out, err)
	}

	canWgetMicrosoft := func() {
		Eventually(wgetMicrosoftErr, "10s", "1s").ShouldNot(HaveOccurred())
		Consistently(wgetMicrosoftErr, "4s", "1s").ShouldNot(HaveOccurred())
	}

	cannotWgetMicrosoft := func() {
		Eventually(wgetMicrosoftErr, "10s", "1s").Should(HaveOccurred())
		Consistently(wgetMicrosoftErr, "4s", "1s").Should(HaveOccurred())
	}

	hostWgetMicrosoftErr := func() error {
		felix.EnsureBinary("test-dns")
		out, err := felix.ExecCombinedOutput("/test-dns", "-", "microsoft.com", fmt.Sprintf("--dns-server=%s:%d", dnsServerIP, 53))
		return logAndReport(out, err)
	}

	hostCanWgetMicrosoft := func() {
		Eventually(hostWgetMicrosoftErr, "10s", "1s").ShouldNot(HaveOccurred())
		Consistently(hostWgetMicrosoftErr, "4s", "1s").ShouldNot(HaveOccurred())
	}

	hostCannotWgetMicrosoft := func() {
		Eventually(hostWgetMicrosoftErr, "10s", "1s").Should(HaveOccurred())
		Consistently(hostWgetMicrosoftErr, "4s", "1s").Should(HaveOccurred())
	}

	getLastMicrosoftALog := func() (lastLog string) {
		dnsLogs, err := getDNSLogs(path.Join(dnsDir, "dns.log"))
		if err != nil {
			log.Infof("Error getting DNS logs: %v", err)
			return // empty string, so won't match anything that higher levels are looking for
		}
		for _, log := range dnsLogs {
			if strings.Contains(log, `"qname":"microsoft.com"`) && strings.Contains(log, `"qtype":"A"`) {
				lastLog = log
			}
		}
		return
	}

	BeforeEach(func() {
		saveFile = "/dnsinfo/dnsinfo.txt"
		saveFileMappedOutsideContainer = true
		enableLogs = true
		enableLatency = true
	})

	JustBeforeEach(func() {
		opts := infrastructure.DefaultTopologyOptions()
		var err error
		dnsDir, err = ioutil.TempDir("", "dnsinfo")
		Expect(err).NotTo(HaveOccurred())

		nameservers := GetLocalNameservers()
		dnsServerIP = nameservers[0]

		opts.ExtraVolumes[dnsDir] = "/dnsinfo"
		opts.ExtraEnvVars["FELIX_DNSCACHEFILE"] = saveFile
		// For this test file, configure DNSCacheSaveInterval to be much longer than any
		// test duration, so we can be sure that the writing of the dnsinfo.txt file is
		// triggered by shutdown instead of by a periodic timer.
		opts.ExtraEnvVars["FELIX_DNSCACHESAVEINTERVAL"] = "3600"
		opts.ExtraEnvVars["FELIX_DNSTRUSTEDSERVERS"] = strings.Join(GetLocalNameservers(), ",")
		opts.ExtraEnvVars["FELIX_PolicySyncPathPrefix"] = "/var/run/calico/policysync"
		opts.ExtraEnvVars["FELIX_DNSLOGSFILEDIRECTORY"] = "/dnsinfo"
		opts.ExtraEnvVars["FELIX_DNSLOGSFLUSHINTERVAL"] = "1"
		if dnsMode != "" {
			opts.ExtraEnvVars["FELIX_DNSPOLICYMODE"] = dnsMode
		}
		if enableLogs {
			// Default for this is false.  Set "true" to enable.
			opts.ExtraEnvVars["FELIX_DNSLOGSFILEENABLED"] = "true"
		}
		if !enableLatency {
			// Default for this is true.  Set "false" to disable.
			opts.ExtraEnvVars["FELIX_DNSLOGSLATENCY"] = "false"
		}
		// This file tests that Felix writes out its DNS mappings file on shutdown, so we
		// need to stop Felix gracefully.
		opts.FelixStopGraceful = true
		// Tests in this file require a node IP, so that Felix can attach a BPF program to
		// host interfaces.
		opts.NeedNodeIP = true
		felix, etcd, client, infra = infrastructure.StartSingleNodeEtcdTopology(opts)
		infrastructure.CreateDefaultProfile(client, "default", map[string]string{"default": ""}, "")

		// Create a workload, using that profile.
		for ii := range w {
			iiStr := strconv.Itoa(ii)
			w[ii] = workload.Run(felix, "w"+iiStr, "default", "10.65.0.1"+iiStr, "8055", "tcp")
			w[ii].Configure(client)
		}

		// Allow workloads to connect out to the Internet.
		felix.Exec(
			"iptables", "-w", "-t", "nat",
			"-A", "POSTROUTING",
			"-o", "eth0",
			"-j", "MASQUERADE", "--random-fully",
		)
	})

	// Stop etcd and workloads, collecting some state if anything failed.
	AfterEach(func() {
		if CurrentGinkgoTestDescription().Failed {
			felix.Exec("calico-bpf", "ipsets", "dump", "--debug")
			felix.Exec("ipset", "list")
			felix.Exec("iptables-save", "-c")
			felix.Exec("ip", "r")
		}

		for ii := range w {
			w[ii].Stop()
		}
		felix.Stop()
		if saveFileMappedOutsideContainer {
			Eventually(path.Join(dnsDir, "dnsinfo.txt"), "10s", "1s").Should(BeARegularFile())
		}

		if CurrentGinkgoTestDescription().Failed {
			etcd.Exec("etcdctl", "ls", "--recursive", "/")
		}
		etcd.Stop()
		infra.Stop()
	})

	Context("with save file in initially non-existent directory", func() {
		BeforeEach(func() {
			saveFile = "/a/b/c/d/e/dnsinfo.txt"
			saveFileMappedOutsideContainer = false
		})

		It("can wget microsoft.com", func() {
			canWgetMicrosoft()
		})
	})

	Context("after wget microsoft.com", func() {

		JustBeforeEach(func() {
			time.Sleep(time.Second)
			canWgetMicrosoft()
		})

		It("should emit microsoft.com DNS log with latency", func() {
			Eventually(getLastMicrosoftALog, "10s", "1s").Should(MatchRegexp(`"latency_count":[1-9]`))
		})

		Context("with a preceding DNS request that went unresponded", func() {

			if os.Getenv("FELIX_FV_ENABLE_BPF") == "true" {
				// Skip because the following test relies on a HostEndpoint.
				return
			}

			JustBeforeEach(func() {
				hep := api.NewHostEndpoint()
				hep.Name = "felix-eth0"
				hep.Labels = map[string]string{"host-endpoint": "yes"}
				hep.Spec.Node = felix.Hostname
				hep.Spec.InterfaceName = "eth0"
				_, err := client.HostEndpoints().Create(utils.Ctx, hep, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())

				udp := numorstring.ProtocolFromString("udp")
				policy := api.NewGlobalNetworkPolicy()
				policy.Name = "deny-dns"
				policy.Spec.Selector = "host-endpoint == 'yes'"
				policy.Spec.Egress = []api.Rule{
					{
						Action:   api.Deny,
						Protocol: &udp,
						Destination: api.EntityRule{
							Ports: []numorstring.Port{numorstring.SinglePort(53)},
						},
					},
					{
						Action: api.Allow,
					},
				}
				policy.Spec.ApplyOnForward = true
				_, err = client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())

				// DNS should now fail, leaving at least one unresponded DNS
				// request.
				cannotWgetMicrosoft()

				// Delete the policy again.
				_, err = client.GlobalNetworkPolicies().Delete(utils.Ctx, "deny-dns", options.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred())

				// Delete the host endpoint again.
				_, err = client.HostEndpoints().Delete(utils.Ctx, "felix-eth0", options.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred())

				// Wait 11 seconds so that the unresponded request timestamp is
				// eligible for cleanup.
				time.Sleep(11 * time.Second)

				// Now DNS and outbound connection should work.
				canWgetMicrosoft()
			})

			It("should emit microsoft.com DNS log with latency", func() {
				Eventually(getLastMicrosoftALog, "10s", "1s").Should(MatchRegexp(`"latency_count":[1-9]`))
			})
		})

		Context("with DNS latency disabled", func() {
			BeforeEach(func() {
				enableLatency = false
			})

			It("should emit microsoft.com DNS log without latency", func() {
				Eventually(getLastMicrosoftALog, "10s", "1s").Should(MatchRegexp(`"latency_count":0`))
			})
		})

		Context("with DNS logs disabled", func() {
			BeforeEach(func() {
				enableLogs = false
			})

			It("should not emit DNS logs", func() {
				Consistently(path.Join(dnsDir, "dns.log"), "5s", "1s").ShouldNot(BeARegularFile())
			})
		})
	})

	Context("after host wget microsoft.com", func() {

		JustBeforeEach(func() {
			time.Sleep(time.Second)
			hostCanWgetMicrosoft()
		})

		It("should emit DNS logs", func() {
			Eventually(getLastMicrosoftALog, "10s", "1s").ShouldNot(BeEmpty())
		})

		Context("with DNS logs disabled", func() {
			BeforeEach(func() {
				enableLogs = false
			})

			It("should not emit DNS logs", func() {
				Consistently(path.Join(dnsDir, "dns.log"), "5s", "1s").ShouldNot(BeARegularFile())
			})
		})
	})

	It("can wget microsoft.com", func() {
		canWgetMicrosoft()
	})

	It("host can wget microsoft.com", func() {
		hostCanWgetMicrosoft()
	})

	Context("with default-deny egress policy", func() {
		JustBeforeEach(func() {
			policy := api.NewGlobalNetworkPolicy()
			policy.Name = "default-deny-egress"
			policy.Spec.Selector = "all()"
			policy.Spec.Egress = []api.Rule{{
				Action: api.Deny,
			}}
			_, err := client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
			Expect(err).NotTo(HaveOccurred())
		})

		It("cannot wget microsoft.com", func() {
			cannotWgetMicrosoft()
		})

		// There's no HostEndpoint yet, so the policy doesn't affect the host.
		It("host can wget microsoft.com", func() {
			hostCanWgetMicrosoft()
		})

		configureGNPAllowToMicrosoft := func() {
			policy := api.NewGlobalNetworkPolicy()
			policy.Name = "allow-microsoft"
			order := float64(20)
			policy.Spec.Order = &order
			policy.Spec.Selector = "all()"
			udp := numorstring.ProtocolFromString(numorstring.ProtocolUDP)
			policy.Spec.Egress = []api.Rule{
				{
					Action:      api.Allow,
					Destination: api.EntityRule{Domains: []string{"microsoft.com", "www.microsoft.com"}},
				},
				{
					Action:   api.Allow,
					Protocol: &udp,
					Destination: api.EntityRule{
						Ports: []numorstring.Port{numorstring.SinglePort(53)},
					},
				},
			}
			_, err := client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
			Expect(err).NotTo(HaveOccurred())
		}

		Context("with HostEndpoint", func() {

			if os.Getenv("FELIX_FV_ENABLE_BPF") == "true" {
				// Skip because the following test relies on a HostEndpoint.
				return
			}

			JustBeforeEach(func() {
				hep := api.NewHostEndpoint()
				hep.Name = "hep-1"
				hep.Spec.Node = felix.Hostname
				hep.Spec.InterfaceName = "eth0"
				_, err := client.HostEndpoints().Create(utils.Ctx, hep, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())
			})

			It("host cannot wget microsoft.com", func() {
				hostCannotWgetMicrosoft()
			})

			Context("with domain-allow egress policy", func() {
				JustBeforeEach(configureGNPAllowToMicrosoft)

				It("host can wget microsoft.com", func() {
					hostCanWgetMicrosoft()
				})
			})
		})

		// For a smallish subset of tests try running using the different policy modes.
		for _, m := range []api.DNSPolicyMode{
			api.DNSPolicyModeNoDelay,
			api.DNSPolicyModeDelayDNSResponse,
			api.DNSPolicyModeDelayDeniedPacket,
		} {
			localMode := m
			Context("with DNSPolicyMode explicity set to "+string(localMode), func() {

				BeforeEach(func() {
					dnsMode = string(localMode)
				})

				// Helper used to check iptables contains the correct entries based on DNSPolicyMode and eBPF.
				checkIPTablesFunc := func(nfq100, nfq101 bool) func() error {
					return func() error {
						iptablesSaveOutput, err := felix.ExecCombinedOutput("iptables-save", "-c")
						if err != nil {
							return err
						}

						var foundReq, foundResp, found100, found101 bool
						for _, line := range strings.Split(iptablesSaveOutput, "\n") {
							if strings.Contains(line, "--nflog-group 3") {
								if strings.Contains(line, "NEW") {
									foundReq = true
								}
								if strings.Contains(line, "ESTABLISHED") {
									foundResp = true
								}
							} else if strings.Contains(line, "--queue-num 100") {
								found100 = true
							} else if strings.Contains(line, "--queue-num 101") && strings.Contains(line, "ESTABLISHED") {
								found101 = true
							}
						}

						if !foundReq {
							return fmt.Errorf("iptables does not contain the NFLOG DNS request snooping rule\n%s", iptablesSaveOutput)
						}
						if !nfq101 && !foundResp {
							return fmt.Errorf("iptables does not contain the NFLOG DNS response snooping rule\n%s", iptablesSaveOutput)
						}
						if nfq100 && !found100 {
							return fmt.Errorf("iptables does not contain the NFQUEUE id 100 rule\n%s", iptablesSaveOutput)
						}
						if nfq101 && !found101 {
							return fmt.Errorf("iptables does not contain the NFQUEUE id 101 rule\n%s", iptablesSaveOutput)
						}
						if found100 && found101 {
							return fmt.Errorf("iptables contains NFQUEUE id 100 and 101 rules\n%s", iptablesSaveOutput)
						}

						return nil
					}
				}

				if os.Getenv("FELIX_FV_ENABLE_BPF") != "true" && localMode == api.DNSPolicyModeNoDelay {
					It("has ingress and egress NFLOG DNS snooping rules and no NFQueue rules", func() {
						// Should be 6 snooping NFLOG rules (ingress/egress for forward,output,input).
						// Should be no nfqueue rules.
						Eventually(checkIPTablesFunc(false, false), "10s", "1s").ShouldNot(HaveOccurred())
					})
				}

				if os.Getenv("FELIX_FV_ENABLE_BPF") != "true" && localMode == api.DNSPolicyModeDelayDNSResponse {
					It("has ingress NFLOG and egress NFQUEUE DNS snooping rules", func() {
						// Should be 3 snooping NFLOG rules (egress for forward,output,input).
						// Should be 3 snooping NFQUEUE 101 rules (ingress for forward,output,input).
						// Should be no nfqueue 100 rules.
						Eventually(checkIPTablesFunc(false, true), "10s", "1s").ShouldNot(HaveOccurred())
					})
				}

				if os.Getenv("FELIX_FV_ENABLE_BPF") != "true" && localMode == api.DNSPolicyModeDelayDeniedPacket {
					It("has ingress and egress NFLOG rules and NFQUEUEd deny packets", func() {
						// Should be 6 snooping NFLOG rules (ingress/egress for forward,output,input).
						// Should only be nfqueue 100 rules.
						Eventually(checkIPTablesFunc(true, false), "10s", "1s").ShouldNot(HaveOccurred())
					})
				}

				Context("with domain-allow egress policy", func() {
					JustBeforeEach(configureGNPAllowToMicrosoft)

					It("can wget microsoft.com", func() {
						canWgetMicrosoft()
					})
				})

				Context("with namespaced domain-allow egress policy", func() {
					JustBeforeEach(func() {
						policy := api.NewNetworkPolicy()
						policy.Name = "allow-microsoft"
						policy.Namespace = "fv"
						order := float64(20)
						policy.Spec.Order = &order
						policy.Spec.Selector = "all()"
						udp := numorstring.ProtocolFromString(numorstring.ProtocolUDP)
						policy.Spec.Egress = []api.Rule{
							{
								Action:      api.Allow,
								Destination: api.EntityRule{Domains: []string{"microsoft.com", "www.microsoft.com"}},
							},
							{
								Action:   api.Allow,
								Protocol: &udp,
								Destination: api.EntityRule{
									Ports: []numorstring.Port{numorstring.SinglePort(53)},
								},
							},
						}
						_, err := client.NetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
						Expect(err).NotTo(HaveOccurred())
					})

					It("can wget microsoft.com", func() {
						canWgetMicrosoft()
					})
				})
			})
		}

		Context("with namespaced domain-allow egress policy in wrong namespace", func() {
			JustBeforeEach(func() {
				policy := api.NewNetworkPolicy()
				policy.Name = "allow-microsoft"
				policy.Namespace = "wibbly-woo"
				order := float64(20)
				policy.Spec.Order = &order
				policy.Spec.Selector = "all()"
				udp := numorstring.ProtocolFromString(numorstring.ProtocolUDP)
				policy.Spec.Egress = []api.Rule{
					{
						Action:      api.Allow,
						Destination: api.EntityRule{Domains: []string{"microsoft.com", "www.microsoft.com"}},
					},
					{
						Action:   api.Allow,
						Protocol: &udp,
						Destination: api.EntityRule{
							Ports: []numorstring.Port{numorstring.SinglePort(53)},
						},
					},
				}
				_, err := client.NetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())
			})

			It("cannot wget microsoft.com", func() {
				cannotWgetMicrosoft()
			})
		})

		Context("with wildcard domain-allow egress policy", func() {
			JustBeforeEach(func() {
				policy := api.NewGlobalNetworkPolicy()
				policy.Name = "allow-microsoft-wild"
				order := float64(20)
				policy.Spec.Order = &order
				policy.Spec.Selector = "all()"
				udp := numorstring.ProtocolFromString(numorstring.ProtocolUDP)
				policy.Spec.Egress = []api.Rule{
					{
						Action:      api.Allow,
						Destination: api.EntityRule{Domains: []string{"microsoft.*", "*.microsoft.com"}},
					},
					{
						Action:   api.Allow,
						Protocol: &udp,
						Destination: api.EntityRule{
							Ports: []numorstring.Port{numorstring.SinglePort(53)},
						},
					},
				}
				_, err := client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())
			})

			It("can wget microsoft.com", func() {
				canWgetMicrosoft()
			})
		})

		Context("with networkset with allowed egress domains", func() {
			JustBeforeEach(func() {
				gns := api.NewGlobalNetworkSet()
				gns.Name = "allow-microsoft"
				gns.Labels = map[string]string{"founder": "billg"}
				gns.Spec.AllowedEgressDomains = []string{"microsoft.com", "www.microsoft.com"}
				_, err := client.GlobalNetworkSets().Create(utils.Ctx, gns, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())

				policy := api.NewGlobalNetworkPolicy()
				policy.Name = "allow-microsoft"
				order := float64(20)
				policy.Spec.Order = &order
				policy.Spec.Selector = "all()"
				udp := numorstring.ProtocolFromString(numorstring.ProtocolUDP)
				policy.Spec.Egress = []api.Rule{
					{
						Action: api.Allow,
						Destination: api.EntityRule{
							Selector: "founder == 'billg'",
						},
					},
					{
						Action:   api.Allow,
						Protocol: &udp,
						Destination: api.EntityRule{
							Ports: []numorstring.Port{numorstring.SinglePort(53)},
						},
					},
				}
				_, err = client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())
			})

			It("can wget microsoft.com", func() {
				canWgetMicrosoft()
			})

			It("handles a domain set update", func() {
				// Create another GNS with same labels as the previous one, so that
				// the destination selector will now match this one as well, and so
				// the domain set membership will change.
				gns := api.NewGlobalNetworkSet()
				gns.Name = "allow-microsoft-2"
				gns.Labels = map[string]string{"founder": "billg"}
				gns.Spec.AllowedEgressDomains = []string{"port25.microsoft.com"}
				_, err := client.GlobalNetworkSets().Create(utils.Ctx, gns, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())

				time.Sleep(2 * time.Second)
				canWgetMicrosoft()
			})
		})

		Context("with networkset with allowed egress wildcard domains", func() {
			JustBeforeEach(func() {
				gns := api.NewGlobalNetworkSet()
				gns.Name = "allow-microsoft"
				gns.Labels = map[string]string{"founder": "billg"}
				gns.Spec.AllowedEgressDomains = []string{"microsoft.*", "*.microsoft.com"}
				_, err := client.GlobalNetworkSets().Create(utils.Ctx, gns, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())

				policy := api.NewGlobalNetworkPolicy()
				policy.Name = "allow-microsoft"
				order := float64(20)
				policy.Spec.Order = &order
				policy.Spec.Selector = "all()"
				udp := numorstring.ProtocolFromString(numorstring.ProtocolUDP)
				policy.Spec.Egress = []api.Rule{
					{
						Action: api.Allow,
						Destination: api.EntityRule{
							Selector: "founder == 'billg'",
						},
					},
					{
						Action:   api.Allow,
						Protocol: &udp,
						Destination: api.EntityRule{
							Ports: []numorstring.Port{numorstring.SinglePort(53)},
						},
					},
				}
				_, err = client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())
			})

			It("can wget microsoft.com", func() {
				canWgetMicrosoft()
			})

			It("handles a domain set update", func() {
				// Create another GNS with same labels as the previous one, so that
				// the destination selector will now match this one as well, and so
				// the domain set membership will change.
				gns := api.NewGlobalNetworkSet()
				gns.Name = "allow-microsoft-2"
				gns.Labels = map[string]string{"founder": "billg"}
				gns.Spec.AllowedEgressDomains = []string{"port25.microsoft.com"}
				_, err := client.GlobalNetworkSets().Create(utils.Ctx, gns, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())

				time.Sleep(2 * time.Second)
				canWgetMicrosoft()
			})
		})

		Context("with networkset with allowed egress domains", func() {
			JustBeforeEach(func() {
				ns := api.NewNetworkSet()
				ns.Name = "allow-microsoft"
				ns.Namespace = "fv"
				ns.Labels = map[string]string{"founder": "billg"}
				ns.Spec.AllowedEgressDomains = []string{"microsoft.com", "www.microsoft.com"}
				_, err := client.NetworkSets().Create(utils.Ctx, ns, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())

				policy := api.NewNetworkPolicy()
				policy.Name = "allow-microsoft"
				policy.Namespace = "fv"
				order := float64(20)
				policy.Spec.Order = &order
				policy.Spec.Selector = "all()"
				udp := numorstring.ProtocolFromString(numorstring.ProtocolUDP)
				policy.Spec.Egress = []api.Rule{
					{
						Action: api.Allow,
						Destination: api.EntityRule{
							Selector: "founder == 'billg'",
						},
					},
					{
						Action:   api.Allow,
						Protocol: &udp,
						Destination: api.EntityRule{
							Ports: []numorstring.Port{numorstring.SinglePort(53)},
						},
					},
				}
				_, err = client.NetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())
			})

			It("can wget microsoft.com", func() {
				canWgetMicrosoft()
			})

			It("handles a domain set update", func() {
				// Create another NetworkSet with same labels as the previous one, so that
				// the destination selector will now match this one as well, and so
				// the domain set membership will change.
				ns := api.NewNetworkSet()
				ns.Name = "allow-microsoft-2"
				ns.Namespace = "fv"
				ns.Labels = map[string]string{"founder": "billg"}
				ns.Spec.AllowedEgressDomains = []string{"port25.microsoft.com"}
				_, err := client.NetworkSets().Create(utils.Ctx, ns, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())

				time.Sleep(2 * time.Second)
				canWgetMicrosoft()
			})
		})

		Context("with networkset with allowed egress wildcard domains", func() {
			JustBeforeEach(func() {
				ns := api.NewNetworkSet()
				ns.Name = "allow-microsoft"
				ns.Namespace = "fv"
				ns.Labels = map[string]string{"founder": "billg"}
				ns.Spec.AllowedEgressDomains = []string{"microsoft.*", "*.microsoft.com"}
				_, err := client.NetworkSets().Create(utils.Ctx, ns, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())

				policy := api.NewNetworkPolicy()
				policy.Name = "allow-microsoft"
				policy.Namespace = "fv"
				order := float64(20)
				policy.Spec.Order = &order
				policy.Spec.Selector = "all()"
				udp := numorstring.ProtocolFromString(numorstring.ProtocolUDP)
				policy.Spec.Egress = []api.Rule{
					{
						Action: api.Allow,
						Destination: api.EntityRule{
							Selector: "founder == 'billg'",
						},
					},
					{
						Action:   api.Allow,
						Protocol: &udp,
						Destination: api.EntityRule{
							Ports: []numorstring.Port{numorstring.SinglePort(53)},
						},
					},
				}
				_, err = client.NetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())
			})

			It("can wget microsoft.com", func() {
				canWgetMicrosoft()
			})

			It("handles a domain set update", func() {
				// Create another NetworkSet with same labels as the previous one, so that
				// the destination selector will now match this one as well, and so
				// the domain set membership will change.
				ns := api.NewNetworkSet()
				ns.Name = "allow-microsoft-2"
				ns.Namespace = "fv"
				ns.Labels = map[string]string{"founder": "billg"}
				ns.Spec.AllowedEgressDomains = []string{"port25.microsoft.com"}
				_, err := client.NetworkSets().Create(utils.Ctx, ns, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())

				time.Sleep(2 * time.Second)
				canWgetMicrosoft()
			})
		})
	})
})

var _ = Describe("DNS Policy Mode: DelayDeniedPacket", func() {

	var (
		dnsserver *containers.Container
		etcd      *containers.Container
		felix     *infrastructure.Felix
		client    client.Interface
		infra     infrastructure.DatastoreInfra
		workload1 *workload.Workload
		workload2 *workload.Workload
		workloads []*workload.Workload
		policy    *api.NetworkPolicy
	)

	BeforeEach(func() {
		var err error

		opts := infrastructure.DefaultTopologyOptions()

		workload1Name := "w1"
		workload2Name := "w2"
		workload1IP := "10.65.0.1"
		workload2IP := "10.65.0.2"

		dnsRecords := map[string][]dns.RecordIP{"foobar.com": {{TTL: 20, IP: workload2IP}}}

		dnsserver = dns.StartServer(dnsRecords)

		opts.ExtraEnvVars["FELIX_DNSTRUSTEDSERVERS"] = dnsserver.IP
		opts.ExtraEnvVars["FELIX_PolicySyncPathPrefix"] = "/var/run/calico/policysync"
		opts.ExtraEnvVars["FELIX_DEBUGDNSRESPONSEDELAY"] = "200"
		opts.ExtraEnvVars["FELIX_DebugConsoleEnabled"] = "true"
		felix, etcd, client, infra = infrastructure.StartSingleNodeEtcdTopology(opts)
		infrastructure.CreateDefaultProfile(client, "default", map[string]string{"default": ""}, "")

		workload1 = workload.Run(felix, workload1Name, "default", workload1IP, "8055", "tcp")
		workload1.ConfigureInInfra(infra)
		workloads = append(workloads, workload1)

		workload2 = workload.Run(felix, workload2Name, "default", workload2IP, "8055", "udp")
		workload2.ConfigureInInfra(infra)
		workloads = append(workloads, workload2)

		udp := numorstring.ProtocolFromString(numorstring.ProtocolUDP)

		policy = api.NewNetworkPolicy()
		policy.Name = "allow-foobar"
		policy.Namespace = "default"
		order := float64(20)
		policy.Spec.Order = &order
		policy.Spec.Selector = workload1.NameSelector()
		policy.Spec.Egress = []api.Rule{
			{
				Metadata: &api.RuleMetadata{
					Annotations: map[string]string{
						"rule-name": "allow-foobar",
					},
				},
				Action:      api.Allow,
				Destination: api.EntityRule{Domains: []string{"foobar.com"}},
			},
			{
				Action:   api.Allow,
				Protocol: &udp,
				Destination: api.EntityRule{
					Ports: []numorstring.Port{numorstring.SinglePort(53)},
				},
			},
		}
		_, err = client.NetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
		Expect(err).NotTo(HaveOccurred())

		// Allow workloads to connect out to the Internet.
		felix.Exec(
			"iptables", "-w", "-t", "nat",
			"-A", "POSTROUTING",
			"-o", "eth0",
			"-j", "MASQUERADE", "--random-fully",
		)

		// Ensure that Felix is connected to nfqueue
		_, err = felix.ExecCombinedOutput("cat", "/proc/net/netfilter/nfnetlink_queue")
		Expect(err).ShouldNot(HaveOccurred())
	})

	// Stop etcd and workloads, collecting some state if anything failed.
	AfterEach(func() {
		if CurrentGinkgoTestDescription().Failed {
			felix.Exec("calico-bpf", "ipsets", "dump")
			felix.Exec("ipset", "list")
			felix.Exec("iptables-save", "-c")
			felix.Exec("ip", "r")
			felix.Exec("conntrack", "-L")
		}

		for ii := range workloads {
			workloads[ii].Stop()
		}
		workloads = nil

		felix.Stop()

		if CurrentGinkgoTestDescription().Failed {
			etcd.Exec("etcdctl", "ls", "--recursive", "/")
		}
		etcd.Stop()
		infra.Stop()
		dnsserver.Stop()
	})

	When("when the dns response isn't programmed before the packet reaches the dns policy rule", func() {
		It("nf repeats the packet and the packet is eventually accepted by the dns policy rule", func() {
			policyChainName := rules.PolicyChainName(rules.PolicyOutboundPfx, &proto.PolicyID{
				Tier: "default",
				Name: fmt.Sprintf("%s/default.%s", policy.Namespace, policy.Name),
			})

			waitForIptablesChain(felix, policyChainName)

			output, err := checkSingleShotDNSConnectivity(workload1, "foobar.com", dnsserver.IP)
			Expect(err).ShouldNot(HaveOccurred(), output)

			// Check that we hit the NFQUEUE rule at least once, to prove the packet was NF_REPEATED at least once before
			// being accepted.
			nfqueuedPacketsCount := getIptablesSavePacketCount(felix,
				fmt.Sprintf("cali-fw-%s", workload1.InterfaceName), "Drop if no policies passed packet[^\n]*NFQUEUE.*")
			Expect(nfqueuedPacketsCount).Should(BeNumerically(">", 0))

			dnsPolicyRulePacketsAllowed := getIptablesSavePacketCount(felix, policyChainName, "rule-name=allow-foobar[^\n]*cali40d")
			Expect(dnsPolicyRulePacketsAllowed).Should(Equal(1))
		})

		When("the connection to nfqueue is terminated", func() {
			It("restarts the connection and nf repeats the packet and the packet is eventually accepted by the dns policy rule", func() {
				policyChainName := rules.PolicyChainName(rules.PolicyOutboundPfx, &proto.PolicyID{
					Tier: "default",
					Name: fmt.Sprintf("%s/default.%s", policy.Namespace, policy.Name),
				})

				waitForIptablesChain(felix, policyChainName)

				output, err := felix.RunDebugConsoleCommand("close-nfqueue-conn")
				Expect(err).ShouldNot(HaveOccurred(), output)

				output = ""
				Eventually(func() error {
					output, err = checkSingleShotDNSConnectivity(workload1, "foobar.com", dnsserver.IP)
					return err
				}, "10s", "1s").ShouldNot(HaveOccurred(), output)

				// Check that we hit the NFQUEUE rule at least once, to prove the packet was NF_REPEATED at least once before
				// being accepted.
				nfqueuedPacketsCount := getIptablesSavePacketCount(felix,
					fmt.Sprintf("cali-fw-%s", workload1.InterfaceName), "Drop if no policies passed packet[^\n]*NFQUEUE.*")
				Expect(nfqueuedPacketsCount).Should(BeNumerically(">", 0))

				dnsPolicyRulePacketsAllowed := getIptablesSavePacketCount(felix, policyChainName, "rule-name=allow-foobar[^\n]*cali40d")
				Expect(dnsPolicyRulePacketsAllowed).Should(Equal(1))
			})
		})
	})
})

// waitForIptablesChain waits for the chain to be programmed on the felix instance. It eventually times out if it waits
// too long.
func waitForIptablesChain(felix *infrastructure.Felix, chainName string) {
	Eventually(func() int {
		iptablesSaveOutput, err := felix.ExecCombinedOutput("iptables-save", "-c")
		Expect(err).ShouldNot(HaveOccurred())

		re := regexp.MustCompile(fmt.Sprintf(`\-A %s`, chainName))
		matches := re.FindStringSubmatch(iptablesSaveOutput)
		return len(matches)
	}, "10s", "1s").Should(BeNumerically(">", 0))
}

// checkSingleShotDNSConnectivity sends a single udp request to the domain name from the given workload on port 8055.
// The dnsServerIP is used to tell the test-connection script what dns server to use to resolve the IP for the domain.
func checkSingleShotDNSConnectivity(w *workload.Workload, domainName, dnsServerIP string) (string, error) {
	w.C.EnsureBinary("test-connection")
	output, err := w.ExecCombinedOutput("/test-connection", "-", domainName, "8055", "--protocol=udp", fmt.Sprintf("--dns-server=%s:%d", dnsServerIP, 53))
	return output, err
}

// getIptablesSavePacketCount searches the given iptables-save output for the iptables rule identified by the chain and
// rule identifier given, and returns the packet count for that rule. If the rule isn't found, in the output and this function
// fails the test.
//
// The ruleIdentifier is a regex that targets the text in the rule AFTER the chain name.
func getIptablesSavePacketCount(felix *infrastructure.Felix, chainName, ruleIdentifier string) int {
	var count int
	regex := fmt.Sprintf(`\[(\d*):\d*\]\s-A %s[^\n]*%s.*`, chainName, ruleIdentifier)

	Eventually(func() error {
		iptablesSaveOutput, err := felix.ExecCombinedOutput("iptables-save", "-c")
		if err != nil {
			return err
		}

		re := regexp.MustCompile(regex)
		matches := re.FindStringSubmatch(iptablesSaveOutput)
		if len(matches) < 1 {
			return fmt.Errorf("no rule found for chain \"%s\" and identifier \"%s\"", chainName, ruleIdentifier)
		}

		count, err = strconv.Atoi(matches[1])
		return err
	}, "10s", "1s").ShouldNot(HaveOccurred())

	return count
}
