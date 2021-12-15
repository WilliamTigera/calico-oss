// +build fvtests

// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package fv_test

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	log "github.com/sirupsen/logrus"

	api "github.com/tigera/api/pkg/apis/projectcalico/v3"
	"github.com/tigera/api/pkg/lib/numorstring"

	"github.com/projectcalico/calico/felix/bpf/conntrack"
	"github.com/projectcalico/calico/felix/collector"
	"github.com/projectcalico/calico/felix/fv/infrastructure"
	"github.com/projectcalico/calico/felix/fv/metrics"
	"github.com/projectcalico/calico/felix/fv/utils"
	"github.com/projectcalico/calico/felix/fv/workload"

	"github.com/projectcalico/calico/libcalico-go/lib/apiconfig"
	client "github.com/projectcalico/calico/libcalico-go/lib/clientv3"
)

// This is an extension of the flow_logs_tests.go file to test flow logs for flows enforced with DNS based policies.
//
// Felix1
//  EP1-1 ----> www.google.com
//  EP1-1 ----> microsoft.com
//
// NetworkSet "netset1" with *.google.com
// NetworkSet "netset2" with microsoft.com
//
// Egress Policies
//   Tier1                          |  Tier2
//   snp1-1 (A-netset1 and netset2) |  np2-1 (A-netset2)
//
// A=allow

// These tests use a small subset of the policy types - the main purpose of the tests is to check handling of DNS
// based policy and reverse DNS lookup.
// TODO(rlb): add in BPF checks when DNS policy is fully fixed for BPF.
var _ = infrastructure.DatastoreDescribe("flow log with DNS tests", []apiconfig.DatastoreType{apiconfig.Kubernetes}, func(getInfra infrastructure.InfraFactory) {

	var (
		infra           infrastructure.DatastoreInfra
		opts            infrastructure.TopologyOptions
		felixes         []*infrastructure.Felix
		flowLogsReaders []metrics.FlowLogReader
		client          client.Interface
		ep1_1           *workload.Workload
		dnsServerIP     string
	)

	bpfEnabled := os.Getenv("FELIX_FV_ENABLE_BPF") == "true"

	logAndReport := func(out string, err error) error {
		log.WithError(err).Infof("test-dns said:\n%v", out)
		return err
	}

	wgetDomainErrFn := func(domain string) func() error {
		return func() error {
			ep1_1.C.EnsureBinary("test-dns")
			out, err := ep1_1.ExecCombinedOutput("/test-dns", "-", domain, fmt.Sprintf("--dns-server=%s:%d", dnsServerIP, 53))
			return logAndReport(out, err)
		}
	}

	canWgetDomain := func(domain string) {
		ExpectWithOffset(1, wgetDomainErrFn(domain)()).NotTo(HaveOccurred())
		ConsistentlyWithOffset(1, wgetDomainErrFn(domain), "4s", "1s").ShouldNot(HaveOccurred())
	}

	cannotWgetDomain := func(domain string) {
		ExpectWithOffset(1, wgetDomainErrFn(domain)()).To(HaveOccurred())
		ConsistentlyWithOffset(1, wgetDomainErrFn(domain), "4s", "1s").Should(HaveOccurred())
	}

	BeforeEach(func() {
		infra = getInfra()
		opts = infrastructure.DefaultTopologyOptions()

		nameservers := GetLocalNameservers()
		dnsServerIP = nameservers[0]

		opts.IPIPEnabled = false
		opts.ExtraEnvVars["FELIX_FLOWLOGSFILEENABLED"] = "true"
		opts.ExtraEnvVars["FELIX_FLOWLOGSFLUSHINTERVAL"] = "5"
		opts.ExtraEnvVars["FELIX_FLOWLOGSENABLEHOSTENDPOINT"] = "true"
		opts.ExtraEnvVars["FELIX_FLOWLOGSENABLENETWORKSETS"] = "true"
		opts.ExtraEnvVars["FELIX_FLOWLOGSFILEINCLUDELABELS"] = "true"
		opts.ExtraEnvVars["FELIX_FLOWLOGSFILEINCLUDEPOLICIES"] = "true"
		opts.ExtraEnvVars["FELIX_FLOWLOGSFILEAGGREGATIONKINDFORALLOWED"] = strconv.Itoa(int(AggrNone))
		opts.ExtraEnvVars["FELIX_FLOWLOGSFILEAGGREGATIONKINDFORDENIED"] = strconv.Itoa(int(AggrNone))
		opts.ExtraEnvVars["FELIX_FLOWLOGSCOLLECTORDEBUGTRACE"] = "true"
		opts.ExtraEnvVars["FELIX_DNSTRUSTEDSERVERS"] = strings.Join(nameservers, ",")
		opts.ExtraEnvVars["FELIX_DNSLOGSFILEENABLED"] = "false"
		opts.ExtraEnvVars["FELIX_DNSLOGSLATENCY"] = "false"

		// Start felix instances.
		felixes, client = infrastructure.StartNNodeTopology(1, opts, infra)

		// Install a default profile that allows all ingress and egress, in the absence of any Policy.
		infra.AddDefaultAllow()

		// Create workload on host 1.
		ep1_1 = workload.Run(felixes[0], "ep1-1", "default", "10.65.0.0", "8055", "tcp")
		ep1_1.ConfigureInInfra(infra)

		// Create tiers tier1 and tier2
		tier := api.NewTier()
		tier.Name = "tier1"
		tier.Spec.Order = &float1_0
		_, err := client.Tiers().Create(utils.Ctx, tier, utils.NoOptions)

		tier = api.NewTier()
		tier.Name = "tier2"
		tier.Spec.Order = &float2_0
		_, err = client.Tiers().Create(utils.Ctx, tier, utils.NoOptions)

		// Add two global network sets one for the two different domains.
		gns := api.NewGlobalNetworkSet()
		gns.Name = "netset1"
		gns.Labels = map[string]string{"netset1": ""}
		gns.Spec.AllowedEgressDomains = []string{"*.google.com"}
		_, err = client.GlobalNetworkSets().Create(utils.Ctx, gns, utils.NoOptions)
		Expect(err).NotTo(HaveOccurred())

		gns = api.NewGlobalNetworkSet()
		gns.Name = "netset2"
		gns.Labels = map[string]string{"netset2": ""}
		gns.Spec.AllowedEgressDomains = []string{"microsoft.com"}
		_, err = client.GlobalNetworkSets().Create(utils.Ctx, gns, utils.NoOptions)
		Expect(err).NotTo(HaveOccurred())

		// Allow traffic to networksets in staged policy
		udp := numorstring.ProtocolFromString(numorstring.ProtocolUDP)
		sgnp := api.NewStagedGlobalNetworkPolicy()
		sgnp.Name = "tier1.ep1-1-allow-netset1-netset2"
		sgnp.Spec.Order = &float1_0
		sgnp.Spec.Tier = "tier1"
		sgnp.Spec.Selector = ep1_1.NameSelector()
		sgnp.Spec.Types = []api.PolicyType{api.PolicyTypeEgress}
		sgnp.Spec.Egress = []api.Rule{
			{
				Action:   api.Allow,
				Protocol: &udp,
				Destination: api.EntityRule{
					Ports: []numorstring.Port{numorstring.SinglePort(53)},
				},
			},
			{
				Destination: api.EntityRule{
					Selector: "has(netset1)",
				},
				Action: api.Allow,
			},
			{
				Destination: api.EntityRule{
					Selector: "has(netset2)",
				},
				Action: api.Allow,
			},
		}
		_, err = client.StagedGlobalNetworkPolicies().Create(utils.Ctx, sgnp, utils.NoOptions)
		Expect(err).NotTo(HaveOccurred())

		// Allow traffic to networkset1.
		gnp := api.NewGlobalNetworkPolicy()
		gnp.Name = "tier2.ep1-1-allow-netset2"
		gnp.Spec.Order = &float1_0
		gnp.Spec.Tier = "tier2"
		gnp.Spec.Selector = ep1_1.NameSelector()
		gnp.Spec.Types = []api.PolicyType{api.PolicyTypeEgress}
		gnp.Spec.Egress = []api.Rule{
			{
				Action:   api.Allow,
				Protocol: &udp,
				Destination: api.EntityRule{
					Ports: []numorstring.Port{numorstring.SinglePort(53)},
				},
			},
			{
				Destination: api.EntityRule{
					Selector: "has(netset2)",
				},
				Action: api.Allow,
			},
		}
		_, err = client.GlobalNetworkPolicies().Create(utils.Ctx, gnp, utils.NoOptions)
		Expect(err).NotTo(HaveOccurred())

		flowLogsReaders = []metrics.FlowLogReader{}
		for _, f := range felixes {
			flowLogsReaders = append(flowLogsReaders, f)
		}

		// Allow workloads to connect out to the Internet.
		felixes[0].Exec(
			"iptables", "-w", "-t", "nat",
			"-A", "POSTROUTING",
			"-o", "eth0",
			"-j", "MASQUERADE", "--random-fully",
		)

		// Wait for rules to be programmed.
		time.Sleep(5 * time.Second)
	})

	It("should correctly resolve and connectivity should be based on enforced policy.", func() {
		// Run a few tests for both interesting domains.  These should work immediately and consistently.
		canWgetDomain("microsoft.com")
		cannotWgetDomain("www.google.com")
		canWgetDomain("microsoft.com")
		cannotWgetDomain("www.google.com")
		canWgetDomain("microsoft.com")
		cannotWgetDomain("www.google.com")

		if bpfEnabled {
			// Make sure that conntrack scanning ticks at least once
			time.Sleep(3 * conntrack.ScanPeriod)
		} else {
			// Allow 6 seconds for the Felixes to poll conntrack.  (This is conntrack polling time plus 20%, which gives us
			// 10% leeway over the polling jitter of 10%)
			time.Sleep(6 * time.Second)
		}

		// Delete conntrack state so that we don't keep seeing 0-metric copies of the logs.  This will allow the flows
		// to expire quickly.
		for ii := range felixes {
			felixes[ii].Exec("conntrack", "-F")
		}

		Eventually(func() error {
			flowTester := metrics.NewFlowTester(flowLogsReaders, true, true, 0)

			// Track all errors before failing.  All flows originating from our workload should be going to either
			// the DNS server or the network sets.  If bound for the network sets then networkset1 should be denied and
			// networkset2 should be allowed.  All should have policy hits from both tiers.
			var errs []string
			var foundDNS, foundNetset1, foundNetset2 bool
			err := flowTester.IterFlows("file", func(flowLog collector.FlowLog) error {
				// Source for every log should be ep1_1.
				if flowLog.SrcMeta.Type != "wep" || flowLog.SrcMeta.Namespace != "default" || flowLog.SrcMeta.Name != ep1_1.Name {
					errs = append(errs, fmt.Sprintf("Unexpected source meta in flow: %#v", flowLog.SrcMeta))
					return nil
				}

				// Handle DNS requests separately.  These should have policy hits including both the staged policy and
				// the enforced policy.
				if flowLog.Tuple.GetDestPort() == 53 {
					foundDNS = true
					if len(flowLog.FlowPolicies) != 2 {
						errs = append(errs, fmt.Sprintf("Unexpected number of policies for DNS: %#v", flowLog.FlowPolicies))
						return nil
					}
					delete(flowLog.FlowPolicies, "0|tier1|tier1.staged:ep1-1-allow-netset1-netset2|allow|0")
					delete(flowLog.FlowPolicies, "1|tier2|tier2.ep1-1-allow-netset2|allow|0")
					if len(flowLog.FlowPolicies) != 0 {
						errs = append(errs, fmt.Sprintf("Unexpected policies for DNS: %#v", flowLog.FlowPolicies))
						return nil
					}
					return nil
				}

				// If not DNS, the destination should be a DNS match to networkset.
				if flowLog.DstMeta.Type != "ns" {
					errs = append(errs, fmt.Sprintf("Unexpected dest meta in flow: %#v", flowLog.DstMeta))
					return nil
				}

				if flowLog.DstMeta.Name == "netset1" {
					// Netset1 is matched by the staged policy and the default drop from the enforced policy.
					// The drop by the enforced policy should be an exact match. The hit from staged policy may be
					// an allow if the network set has been programmed or otherwise a no-match deny.  As a result we
					// have to expect 2 or 3 policies.
					foundNetset1 = true
					if len(flowLog.FlowPolicies) != 2 && len(flowLog.FlowPolicies) != 3 {
						errs = append(errs, fmt.Sprintf("Unexpected number of policies for netset1: %#v", flowLog.FlowPolicies))
						return nil
					}
					delete(flowLog.FlowPolicies, "0|tier1|tier1.staged:ep1-1-allow-netset1-netset2|allow|1")
					delete(flowLog.FlowPolicies, "0|tier1|tier1.staged:ep1-1-allow-netset1-netset2|deny|-1")
					delete(flowLog.FlowPolicies, "1|tier2|tier2.ep1-1-allow-netset2|deny|-1")
					if len(flowLog.FlowPolicies) != 0 {
						errs = append(errs, fmt.Sprintf("Unexpected policies for netset1: %#v", flowLog.FlowPolicies))
						return nil
					}
				}

				if flowLog.DstMeta.Name == "netset2" {
					// Netset2 is matched by the staged policy and the default allow from the enforced policy.
					// The allow by the enforced policy should be an exact match because the policy would otherwise
					// be dropped and packet retry will continue until it is allowed. The hit from staged policy may be
					// an allow if the network set has been programmed or otherwise a no-match deny.  As a result we
					// have to expect 2 or 3 policies.
					foundNetset2 = true
					if len(flowLog.FlowPolicies) != 2 && len(flowLog.FlowPolicies) != 3 {
						errs = append(errs, fmt.Sprintf("Unexpected number of policies for netset2: %#v", flowLog.FlowPolicies))
						return nil
					}
					delete(flowLog.FlowPolicies, "0|tier1|tier1.staged:ep1-1-allow-netset1-netset2|allow|2")
					delete(flowLog.FlowPolicies, "0|tier1|tier1.staged:ep1-1-allow-netset1-netset2|deny|-1")
					delete(flowLog.FlowPolicies, "1|tier2|tier2.ep1-1-allow-netset2|allow|1")
					if len(flowLog.FlowPolicies) != 0 {
						errs = append(errs, fmt.Sprintf("Unexpected policies for netset2: %#v", flowLog.FlowPolicies))
					}
				}

				return nil
			})
			if err != nil {
				errs = append(errs, err.Error())
			} else {
				if !foundDNS {
					errs = append(errs, "No DNS flow found")
				}
				if !foundNetset1 {
					errs = append(errs, "No flow to GlobalNetworkSet(netset1) found")
				}
				if !foundNetset2 {
					errs = append(errs, "No flow to GlobalNetworkSet(netset2) found")
				}
			}

			if len(errs) == 0 {
				return nil
			}

			return errors.New(strings.Join(errs, "\n==============\n"))
		}, "30s", "3s").ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		if CurrentGinkgoTestDescription().Failed {
			for _, felix := range felixes {
				felix.Exec("iptables-save", "-c")
				felix.Exec("ipset", "list")
				felix.Exec("ip", "r")
				felix.Exec("ip", "a")
			}
		}

		ep1_1.Stop()
		for _, felix := range felixes {
			if bpfEnabled {
				felix.Exec("calico-bpf", "connect-time", "clean")
			}
			felix.Stop()
		}

		if CurrentGinkgoTestDescription().Failed {
			infra.DumpErrorData()
		}
		infra.Stop()
	})
})
