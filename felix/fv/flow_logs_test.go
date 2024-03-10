//go:build fvtests
// +build fvtests

// Copyright (c) 2018-2023 Tigera, Inc. All rights reserved.

package fv_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	api "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/calico/felix/bpf/conntrack"
	"github.com/projectcalico/calico/felix/collector/flowlog"
	"github.com/projectcalico/calico/felix/fv/connectivity"
	"github.com/projectcalico/calico/felix/fv/flowlogs"
	"github.com/projectcalico/calico/felix/fv/infrastructure"
	"github.com/projectcalico/calico/felix/fv/metrics"
	"github.com/projectcalico/calico/felix/fv/utils"
	"github.com/projectcalico/calico/felix/fv/workload"
	"github.com/projectcalico/calico/libcalico-go/lib/apiconfig"
	client "github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/calico/libcalico-go/lib/options"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// Config variations covered here:
//
// - Non-default group name.
// - Non-default stream name.
// - Include endpoint labels.
//
// With those variations in place,
//
//   - Generate denied flows, as well as allowed.
//   - Generate flows from multiple client pods, sharing a prefix, each
//     of which makes multiple connections to an IP that matches a wep, hep
//     or ns.
//
// Verifications:
//
// - group and stream names
// - endpoint labels included or not
// - aggregation as expected
// - metrics are zero or non-zero as expected
// - correct counts of flows started and completed
// - action allow or deny as expected
//
// Still needed elsewhere:
//
// - Timing variations
// - start_time and end_time fields
//
//	        Host 1                              Host 2
//
//	wl-client-1                              wl-server-1 (allowed)
//	wl-client-2                              wl-server-2 (denied)
//	wl-client-3                              hep-IP
//	wl-client-4
//	      ns-IP
type aggregation int

const (
	AggrNone         aggregation = 0
	AggrBySourcePort aggregation = 1
	AggrByPodPrefix  aggregation = 2
)

type expectation struct {
	labels                bool
	policies              bool
	aggregationForAllowed aggregation
	aggregationForDenied  aggregation
}

type expectedPolicy struct {
	reporter string
	action   string
	policies []string
}

// FIXME!
var (
	networkSetIPsSupported  = true
	applyOnForwardSupported = false
)

// Flow logs have little to do with the backend, and these tests are relatively slow, so
// better to run with one backend only.  etcdv3 is easier because we create a fresh
// datastore for every test and so don't need to worry about cleaning resources up.
var _ = infrastructure.DatastoreDescribe("_BPF-SAFE_ flow log tests", []apiconfig.DatastoreType{apiconfig.EtcdV3}, func(getInfra infrastructure.InfraFactory) {

	bpfEnabled := os.Getenv("FELIX_FV_ENABLE_BPF") == "true"

	var (
		infra             infrastructure.DatastoreInfra
		tc                infrastructure.TopologyContainers
		opts              infrastructure.TopologyOptions
		useInvalidLicense bool
		expectation       expectation
		flowLogsReaders   []metrics.FlowLogReader
		client            client.Interface
		wlHost1           [4]*workload.Workload
		wlHost2           [2]*workload.Workload
		hostW             [2]*workload.Workload
		cc                *connectivity.Checker
	)

	BeforeEach(func() {
		useInvalidLicense = false
		infra = getInfra()
		opts = infrastructure.DefaultTopologyOptions()
		opts.IPIPEnabled = false

		opts.ExtraEnvVars["FELIX_FLOWLOGSFLUSHINTERVAL"] = "120"
		opts.ExtraEnvVars["FELIX_FLOWLOGSCOLLECTORDEBUGTRACE"] = "true"

		if networkSetIPsSupported {
			opts.ExtraEnvVars["FELIX_FLOWLOGSENABLENETWORKSETS"] = "true"
		}
	})

	JustBeforeEach(func() {
		tc, client = infrastructure.StartNNodeTopology(2, opts, infra)

		if useInvalidLicense {
			var felixPIDs []int
			for _, f := range tc.Felixes {
				felixPIDs = append(felixPIDs, f.GetFelixPID())
			}
			infrastructure.ApplyExpiredLicense(client)
			// Wait for felix to restart so we don't accidentally generate a flow log before the license takes effect.
			for i, f := range tc.Felixes {
				Eventually(f.GetFelixPID, "10s", "100ms").ShouldNot(Equal(felixPIDs[i]))
			}
		}

		// Install a default profile that allows all ingress and egress, in the absence of any Policy.
		infra.AddDefaultAllow()

		// Create workloads on host 1.
		for ii := range wlHost1 {
			wIP := fmt.Sprintf("10.65.0.%d", ii)
			wName := fmt.Sprintf("wl-host1-%d", ii)
			wlHost1[ii] = workload.Run(tc.Felixes[0], wName, "default", wIP, "8055", "tcp")
			wlHost1[ii].WorkloadEndpoint.GenerateName = "wl-host1-"
			wlHost1[ii].ConfigureInInfra(infra)
		}

		// Create workloads on host 2.
		for ii := range wlHost2 {
			wIP := fmt.Sprintf("10.65.1.%d", ii)
			wName := fmt.Sprintf("wl-host2-%d", ii)
			wlHost2[ii] = workload.Run(tc.Felixes[1], wName, "default", wIP, "8055", "tcp")
			wlHost2[ii].WorkloadEndpoint.GenerateName = "wl-host2-"
			wlHost2[ii].ConfigureInInfra(infra)
		}

		// Create a non-workload server on each host.
		for ii := range hostW {
			hostW[ii] = workload.Run(tc.Felixes[ii], fmt.Sprintf("host%d", ii), "", tc.Felixes[ii].IP, "8055", "tcp")
		}

		// Create a GlobalNetworkSet that includes host 1's IP.
		ns := api.NewGlobalNetworkSet()
		ns.Name = "ns-1"
		ns.Spec.Nets = []string{tc.Felixes[0].IP + "/32"}
		ns.Labels = map[string]string{
			"ips-for": "host1",
		}
		_, err := client.GlobalNetworkSets().Create(utils.Ctx, ns, utils.NoOptions)
		Expect(err).NotTo(HaveOccurred())

		// Create a HostEndpoint for host 2, with apply-on-forward ingress policy
		// that denies to the second workload on host 2, but allows everything
		// else.
		gnp := api.NewGlobalNetworkPolicy()
		gnp.Name = "gnp-1"
		gnp.Spec.Selector = "host-endpoint=='true'"
		if applyOnForwardSupported {
			// Use ApplyOnForward policy to generate deny flow logs for
			// connection to wlHost2[1].
			gnp.Spec.Ingress = []api.Rule{
				{
					Action: api.Deny,
					Destination: api.EntityRule{
						Selector: "name=='" + wlHost2[1].Name + "'",
					},
				},
				{
					Action: api.Allow,
				},
			}
		} else {
			// ApplyOnForward policy doesn't generate deny flow logs, so we'll
			// use a regular NetworkPolicy below instead, and just allow
			// through the HostEndpoint.
			gnp.Spec.Ingress = []api.Rule{
				{
					Action: api.Allow,
				},
			}
		}
		gnp.Spec.Egress = []api.Rule{
			{
				Action: api.Allow,
			},
		}
		gnp.Spec.ApplyOnForward = true
		_, err = client.GlobalNetworkPolicies().Create(utils.Ctx, gnp, utils.NoOptions)
		Expect(err).NotTo(HaveOccurred())

		if !applyOnForwardSupported {
			np := api.NewNetworkPolicy()
			np.Name = "default.np-1"
			np.Namespace = "default"
			np.Spec.Selector = "name=='" + wlHost2[1].Name + "'"
			np.Spec.Ingress = []api.Rule{
				{
					Action: api.Deny,
				},
			}
			_, err = client.NetworkPolicies().Create(utils.Ctx, np, utils.NoOptions)
			Expect(err).NotTo(HaveOccurred())
		}

		hep := api.NewHostEndpoint()
		hep.Name = "host2-eth0"
		hep.Labels = map[string]string{
			"name":          hep.Name,
			"host-endpoint": "true",
		}
		hep.Spec.Node = tc.Felixes[1].Hostname
		hep.Spec.ExpectedIPs = []string{tc.Felixes[1].IP}
		_, err = client.HostEndpoints().Create(utils.Ctx, hep, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())

		if BPFMode() {
			ensureAllNodesBPFProgramsAttached(tc.Felixes)
		}

		hostEndpointProgrammed := func() bool {
			if BPFMode() {
				return tc.Felixes[1].NumTCBPFProgsEth0() == 2
			} else {
				out, err := tc.Felixes[1].ExecOutput("iptables-save", "-t", "filter")
				Expect(err).NotTo(HaveOccurred())
				return (strings.Count(out, "cali-thfw-eth0") > 0)
			}
		}
		Eventually(hostEndpointProgrammed, "30s", "1s").Should(BeTrue(),
			"Expected HostEndpoint iptables rules to appear")
		if !BPFMode() {
			rulesProgrammed := func() bool {
				out0, err := tc.Felixes[0].ExecOutput("iptables-save", "-t", "filter")
				Expect(err).NotTo(HaveOccurred())
				out1, err := tc.Felixes[1].ExecOutput("iptables-save", "-t", "filter")
				Expect(err).NotTo(HaveOccurred())
				if strings.Count(out0, "ARE0|default") == 0 {
					return false
				}
				if strings.Count(out1, "default.gnp-1") == 0 {
					return false
				}
				if !applyOnForwardSupported && strings.Count(out1, "default.np-1") == 0 {
					return false
				}
				return true
			}
			Eventually(rulesProgrammed, "10s", "1s").Should(BeTrue(),
				"Expected iptables rules to appear on the correct felix instances")
		} else {
			Eventually(func() bool {
				return bpfCheckIfPolicyProgrammed(tc.Felixes[1], "eth0", "egress", "default.gnp-1", "allow", false)
			}, "5s", "200ms").Should(BeTrue())

			Eventually(func() bool {
				return bpfCheckIfPolicyProgrammed(tc.Felixes[1], "eth0", "ingress", "default.gnp-1", "allow", false)
			}, "5s", "200ms").Should(BeTrue())

			if !applyOnForwardSupported {
				Eventually(func() bool {
					return bpfCheckIfPolicyProgrammed(tc.Felixes[1], wlHost2[1].InterfaceName, "ingress", "default/default.np-1", "deny", true)
				}, "5s", "200ms").Should(BeTrue())
			}
		}

		// Describe the connectivity that we now expect.
		cc = &connectivity.Checker{}
		for _, source := range wlHost1 {
			// Workloads on host 1 can connect to the first workload on host 2.
			cc.ExpectSome(source, wlHost2[0])
			// But not the second.
			cc.ExpectNone(source, wlHost2[1])
		}
		// A workload on host 1 can connect to a non-workload server on host 2.
		cc.ExpectSome(wlHost1[0], hostW[1])
		// A workload on host 2 can connect to a non-workload server on host 1.
		cc.ExpectSome(wlHost2[0], hostW[0])

		// Do 3 rounds of connectivity checking.
		cc.CheckConnectivity()
		for ii := range tc.Felixes {
			tc.Felixes[ii].Exec("conntrack", "-L")
		}
		cc.CheckConnectivity()
		for ii := range tc.Felixes {
			tc.Felixes[ii].Exec("conntrack", "-L")
		}
		cc.CheckConnectivity()
		for ii := range tc.Felixes {
			tc.Felixes[ii].Exec("conntrack", "-L")
		}

		if bpfEnabled {
			// Make sure that conntrack scanning ticks at least once
			time.Sleep(3 * conntrack.ScanPeriod)
		} else {
			// Allow 6 seconds for the containers.Felix to poll conntrack.  (This is conntrack polling time plus 20%, which gives us
			// 10% leeway over the polling jitter of 10%)
			time.Sleep(6 * time.Second)
		}

		// Delete conntrack state so that we don't keep seeing 0-metric copies of the logs.  This will allow the flows
		// to expire quickly.
		for ii := range tc.Felixes {
			tc.Felixes[ii].Exec("conntrack", "-F")
		}
		for ii := range tc.Felixes {
			tc.Felixes[ii].Exec("conntrack", "-L")
		}

		flowLogsReaders = []metrics.FlowLogReader{}
		for _, f := range tc.Felixes {
			flowLogsReaders = append(flowLogsReaders, f)
		}
	})

	checkFlowLogs := func(flowLogsOutput string) {
		// Here, by way of illustrating what we need to check for, are the allowed
		// flow logs that we actually see for this test, as grouped and logged by
		// the code below that includes "started:" and "completed:".
		//
		// With default aggregation:
		// Host 1:
		// started: 3 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {hep - host2-eth0 -} allow src}
		// started: 24 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {wep default wl-host2-* -} allow src}
		// completed: 24 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {wep default wl-host2-* -} allow src}
		// completed: 3 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {hep - host2-eth0 -} allow src}
		// Host 2:
		// started: 12 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {wep default wl-host2-* -} allow dst}
		// started: 3 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {hep - host2-eth0 -} allow dst}
		// started: 3 {{[--] [--] 6 0 8055} {wep default wl-host2-* -} {net - pvt -} allow src}
		// completed: 3 {{[--] [--] 6 0 8055} {wep default wl-host2-* -} {net - pvt -} allow src}
		// completed: 12 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {wep default wl-host2-* -} allow dst}
		// completed: 3 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {hep - host2-eth0 -} allow dst}
		//
		// With aggregation none:
		// Host 1:
		// started: 1 {{[10 65 0 3] [10 65 1 0] 6 40849 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 1 {{[10 65 0 0] [10 65 1 0] 6 45549 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 1 {{[10 65 0 0] [10 65 1 0] 6 46873 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 1 {{[10 65 0 2] [10 65 1 1] 6 45995 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 1 {{[10 65 0 2] [10 65 1 0] 6 33465 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 1 {{[10 65 0 0] [172 17 0 19] 6 33615 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow src}
		// started: 1 {{[10 65 0 1] [10 65 1 1] 6 38211 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 1 {{[10 65 0 1] [10 65 1 0] 6 33455 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 1 {{[10 65 0 0] [172 17 0 19] 6 40601 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow src}
		// started: 1 {{[10 65 0 2] [10 65 1 0] 6 43601 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 1 {{[10 65 0 2] [10 65 1 0] 6 46791 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 1 {{[10 65 0 3] [10 65 1 0] 6 39177 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 1 {{[10 65 0 3] [10 65 1 1] 6 41265 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 1 {{[10 65 0 3] [10 65 1 1] 6 38243 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 1 {{[10 65 0 1] [10 65 1 1] 6 35933 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 1 {{[10 65 0 1] [10 65 1 1] 6 37573 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 1 {{[10 65 0 2] [10 65 1 1] 6 38251 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 1 {{[10 65 0 0] [172 17 0 19] 6 39371 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow src}
		// started: 1 {{[10 65 0 3] [10 65 1 1] 6 41429 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 1 {{[10 65 0 0] [10 65 1 1] 6 36303 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 1 {{[10 65 0 3] [10 65 1 0] 6 42645 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 1 {{[10 65 0 0] [10 65 1 0] 6 35515 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 1 {{[10 65 0 1] [10 65 1 0] 6 43049 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 1 {{[10 65 0 1] [10 65 1 0] 6 37091 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 1 {{[10 65 0 0] [10 65 1 1] 6 35479 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 1 {{[10 65 0 2] [10 65 1 1] 6 43967 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 1 {{[10 65 0 0] [10 65 1 1] 6 40211 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 1 {{[10 65 0 0] [10 65 1 0] 6 35515 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 1 {{[10 65 0 3] [10 65 1 1] 6 41429 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 1 {{[10 65 0 0] [172 17 0 19] 6 33615 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow src}
		// completed: 1 {{[10 65 0 2] [10 65 1 1] 6 38251 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 1 {{[10 65 0 3] [10 65 1 1] 6 41265 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 1 {{[10 65 0 3] [10 65 1 0] 6 42645 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 1 {{[10 65 0 1] [10 65 1 1] 6 35933 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 1 {{[10 65 0 2] [10 65 1 1] 6 45995 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 1 {{[10 65 0 0] [10 65 1 1] 6 36303 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 1 {{[10 65 0 2] [10 65 1 1] 6 43967 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 1 {{[10 65 0 0] [10 65 1 1] 6 40211 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 1 {{[10 65 0 1] [10 65 1 1] 6 38211 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 1 {{[10 65 0 2] [10 65 1 0] 6 43601 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 1 {{[10 65 0 3] [10 65 1 1] 6 38243 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 1 {{[10 65 0 1] [10 65 1 1] 6 37573 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 1 {{[10 65 0 0] [172 17 0 19] 6 40601 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow src}
		// completed: 1 {{[10 65 0 3] [10 65 1 0] 6 39177 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 1 {{[10 65 0 2] [10 65 1 0] 6 33465 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 1 {{[10 65 0 0] [10 65 1 0] 6 46873 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 1 {{[10 65 0 0] [10 65 1 0] 6 45549 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 1 {{[10 65 0 1] [10 65 1 0] 6 43049 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 1 {{[10 65 0 0] [10 65 1 1] 6 35479 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 1 {{[10 65 0 1] [10 65 1 0] 6 33455 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 1 {{[10 65 0 2] [10 65 1 0] 6 46791 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 1 {{[10 65 0 1] [10 65 1 0] 6 37091 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 1 {{[10 65 0 3] [10 65 1 0] 6 40849 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 1 {{[10 65 0 0] [172 17 0 19] 6 39371 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow src}
		// Host 2:
		// started: 1 {{[10 65 1 0] [172 17 0 3] 6 38445 8055} {wep default wl-host2-0-idx9 -} {net - pvt -} allow src}
		// started: 1 {{[10 65 0 3] [10 65 1 0] 6 42645 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 1 {{[10 65 0 0] [172 17 0 19] 6 40601 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow dst}
		// started: 1 {{[10 65 0 3] [10 65 1 0] 6 40849 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 1 {{[10 65 0 0] [172 17 0 19] 6 33615 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow dst}
		// started: 1 {{[10 65 0 1] [10 65 1 0] 6 43049 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 1 {{[10 65 0 0] [172 17 0 19] 6 39371 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow dst}
		// started: 1 {{[10 65 0 0] [10 65 1 0] 6 35515 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 1 {{[10 65 0 0] [10 65 1 0] 6 46873 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 1 {{[10 65 1 0] [172 17 0 3] 6 44977 8055} {wep default wl-host2-0-idx9 -} {net - pvt -} allow src}
		// started: 1 {{[10 65 1 0] [172 17 0 3] 6 36887 8055} {wep default wl-host2-0-idx9 -} {net - pvt -} allow src}
		// started: 1 {{[10 65 0 3] [10 65 1 0] 6 39177 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 1 {{[10 65 0 0] [10 65 1 0] 6 45549 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 1 {{[10 65 0 1] [10 65 1 0] 6 33455 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 1 {{[10 65 0 2] [10 65 1 0] 6 43601 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 1 {{[10 65 0 2] [10 65 1 0] 6 46791 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 1 {{[10 65 0 1] [10 65 1 0] 6 37091 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 1 {{[10 65 0 2] [10 65 1 0] 6 33465 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 1 {{[10 65 0 3] [10 65 1 0] 6 40849 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 1 {{[10 65 0 3] [10 65 1 0] 6 39177 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 1 {{[10 65 1 0] [172 17 0 3] 6 38445 8055} {wep default wl-host2-0-idx9 -} {net - pvt -} allow src}
		// completed: 1 {{[10 65 0 1] [10 65 1 0] 6 33455 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 1 {{[10 65 0 1] [10 65 1 0] 6 37091 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 1 {{[10 65 0 0] [172 17 0 19] 6 40601 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow dst}
		// completed: 1 {{[10 65 0 0] [10 65 1 0] 6 45549 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 1 {{[10 65 0 1] [10 65 1 0] 6 43049 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 1 {{[10 65 0 0] [172 17 0 19] 6 39371 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow dst}
		// completed: 1 {{[10 65 1 0] [172 17 0 3] 6 44977 8055} {wep default wl-host2-0-idx9 -} {net - pvt -} allow src}
		// completed: 1 {{[10 65 1 0] [172 17 0 3] 6 36887 8055} {wep default wl-host2-0-idx9 -} {net - pvt -} allow src}
		// completed: 1 {{[10 65 0 2] [10 65 1 0] 6 33465 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 1 {{[10 65 0 0] [172 17 0 19] 6 33615 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow dst}
		// completed: 1 {{[10 65 0 0] [10 65 1 0] 6 35515 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 1 {{[10 65 0 0] [10 65 1 0] 6 46873 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 1 {{[10 65 0 2] [10 65 1 0] 6 46791 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 1 {{[10 65 0 2] [10 65 1 0] 6 43601 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 1 {{[10 65 0 3] [10 65 1 0] 6 42645 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow dst}
		//
		// With aggregation by source port:
		// Host 1:
		// started: 3 {{[10 65 0 3] [10 65 1 1] 6 0 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 3 {{[10 65 0 0] [172 17 0 19] 6 0 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow src}
		// started: 3 {{[10 65 0 3] [10 65 1 0] 6 0 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 3 {{[10 65 0 1] [10 65 1 1] 6 0 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 3 {{[10 65 0 1] [10 65 1 0] 6 0 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 3 {{[10 65 0 2] [10 65 1 1] 6 0 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 3 {{[10 65 0 0] [10 65 1 0] 6 0 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow src}
		// started: 3 {{[10 65 0 0] [10 65 1 1] 6 0 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-1-idx11 -} allow src}
		// started: 3 {{[10 65 0 2] [10 65 1 0] 6 0 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 3 {{[10 65 0 0] [10 65 1 1] 6 0 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 3 {{[10 65 0 3] [10 65 1 0] 6 0 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 3 {{[10 65 0 1] [10 65 1 1] 6 0 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 3 {{[10 65 0 1] [10 65 1 0] 6 0 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 3 {{[10 65 0 0] [10 65 1 0] 6 0 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 3 {{[10 65 0 2] [10 65 1 0] 6 0 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow src}
		// completed: 3 {{[10 65 0 3] [10 65 1 1] 6 0 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-1-idx11 -} allow src}
		// completed: 3 {{[10 65 0 0] [172 17 0 19] 6 0 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow src}
		// completed: 3 {{[10 65 0 2] [10 65 1 1] 6 0 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-1-idx11 -} allow src}
		// Host 2:
		// started: 3 {{[10 65 0 0] [10 65 1 0] 6 0 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 3 {{[10 65 1 0] [172 17 0 3] 6 0 8055} {wep default wl-host2-0-idx9 -} {net - pvt -} allow src}
		// started: 3 {{[10 65 0 0] [172 17 0 19] 6 0 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow dst}
		// started: 3 {{[10 65 0 1] [10 65 1 0] 6 0 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 3 {{[10 65 0 2] [10 65 1 0] 6 0 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow dst}
		// started: 3 {{[10 65 0 3] [10 65 1 0] 6 0 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 3 {{[10 65 0 2] [10 65 1 0] 6 0 8055} {wep default wl-host1-2-idx5 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 3 {{[10 65 0 3] [10 65 1 0] 6 0 8055} {wep default wl-host1-3-idx7 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 3 {{[10 65 0 0] [10 65 1 0] 6 0 8055} {wep default wl-host1-0-idx1 -} {wep default wl-host2-0-idx9 -} allow dst}
		// completed: 3 {{[10 65 1 0] [172 17 0 3] 6 0 8055} {wep default wl-host2-0-idx9 -} {net - pvt -} allow src}
		// completed: 3 {{[10 65 0 0] [172 17 0 19] 6 0 8055} {wep default wl-host1-0-idx1 -} {hep - host2-eth0 -} allow dst}
		// completed: 3 {{[10 65 0 1] [10 65 1 0] 6 0 8055} {wep default wl-host1-1-idx3 -} {wep default wl-host2-0-idx9 -} allow dst}
		//
		// With aggregation by pod prefix (same as default aggregation):
		// Host 1:
		// started: 48 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {wep default wl-host2-* -} allow src}
		// started: 6 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {hep - host2-eth0 -} allow src}
		// completed: 3 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {hep - host2-eth0 -} allow src}
		// completed: 24 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {wep default wl-host2-* -} allow src}
		// Host 2:
		// started: 3 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {hep - host2-eth0 -} allow dst}
		// started: 12 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {wep default wl-host2-* -} allow dst}
		// started: 3 {{[--] [--] 6 0 8055} {wep default wl-host2-* -} {net - pvt -} allow src}
		// completed: 3 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {hep - host2-eth0 -} allow dst}
		// completed: 12 {{[--] [--] 6 0 8055} {wep default wl-host1-* -} {wep default wl-host2-* -} allow dst}
		// completed: 3 {{[--] [--] 6 0 8055} {wep default wl-host2-* -} {net - pvt -} allow src}

		// Within 30s we should see the complete set of expected allow and deny
		// flow logs.
		Eventually(func() error {
			flowTester := metrics.NewFlowTesterDeprecated(flowLogsReaders, expectation.labels, expectation.policies, 8055)
			err := flowTester.PopulateFromFlowLogs(flowLogsOutput)
			if err != nil {
				return err
			}

			// Only report errors at the end.
			var errs []string

			// Now we tick off each FlowMeta that we expect, and check that
			// the log(s) for each one are present and as expected.
			switch expectation.aggregationForAllowed {
			case AggrNone:
				for _, source := range wlHost1 {
					err = flowTester.CheckFlow(
						"wep default "+source.Name+" "+source.WorkloadEndpoint.GenerateName+"*", source.IP,
						"wep default "+wlHost2[0].Name+" "+wlHost2[0].WorkloadEndpoint.GenerateName+"*", wlHost2[0].IP,
						metrics.NoService, 3, 1,
						[]metrics.ExpectedPolicy{
							{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
							{"dst", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
						})
					if err != nil {
						errs = append(errs, fmt.Sprintf("Error agg for allowed; agg none; source %s; flow 1: %v", source.Name, err))
					}
					err = flowTester.CheckFlow(
						"wep default "+source.Name+" "+source.WorkloadEndpoint.GenerateName+"*", source.IP,
						"wep default "+wlHost2[1].Name+" "+wlHost2[1].WorkloadEndpoint.GenerateName+"*", wlHost2[1].IP,
						metrics.NoService, 3, 1,
						[]metrics.ExpectedPolicy{
							{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
							{}, // ""
						})
					if err != nil {
						errs = append(errs, fmt.Sprintf("Error agg for allowed; agg none; source %s; flow 2: %v", source.Name, err))
					}
				}

				err = flowTester.CheckFlow(
					"wep default "+wlHost1[0].Name+" "+wlHost1[0].WorkloadEndpoint.GenerateName+"*", wlHost1[0].IP,
					"hep - host2-eth0 "+tc.Felixes[1].Hostname, tc.Felixes[1].IP,
					metrics.NoService, 3, 1,
					[]metrics.ExpectedPolicy{
						{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
						{"dst", "allow", []string{"0|default|default.gnp-1|allow|0"}},
					})
				if err != nil {
					errs = append(errs, fmt.Sprintf("Error agg for allowed; agg none; flow hep: %v", err))
				}

				if networkSetIPsSupported {
					err = flowTester.CheckFlow(
						"wep default "+wlHost2[0].Name+" "+wlHost2[0].WorkloadEndpoint.GenerateName+"*", wlHost2[0].IP,
						"ns - ns-1 ns-1", tc.Felixes[0].IP,
						metrics.NoService, 3, 1,
						[]metrics.ExpectedPolicy{
							{}, // ""
							{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
						})
				} else {
					err = flowTester.CheckFlow(
						"wep default "+wlHost2[0].Name+" "+wlHost2[0].WorkloadEndpoint.GenerateName+"*", wlHost2[0].IP,
						"net - - pvt", tc.Felixes[0].IP,
						metrics.NoService, 3, 1,
						[]metrics.ExpectedPolicy{
							{}, // ""
							{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
						})
				}
				if err != nil {
					errs = append(errs, fmt.Sprintf("Error agg for allowed; agg none; netset: %v", err))
				}
			case AggrBySourcePort:
				for _, source := range wlHost1 {
					err = flowTester.CheckFlow(
						"wep default "+source.Name+" "+source.WorkloadEndpoint.GenerateName+"*", source.IP,
						"wep default "+wlHost2[0].Name+" "+wlHost2[0].WorkloadEndpoint.GenerateName+"*", wlHost2[0].IP,
						metrics.NoService, 1, 3,
						[]metrics.ExpectedPolicy{
							{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
							{"dst", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
						})
					if err != nil {
						errs = append(errs, fmt.Sprintf("Error agg for allowed; agg src port; source %s; flow 1: %v", source.Name, err))
					}
					err = flowTester.CheckFlow(
						"wep default "+source.Name+" "+source.WorkloadEndpoint.GenerateName+"*", source.IP,
						"wep default "+wlHost2[1].Name+" "+wlHost2[1].WorkloadEndpoint.GenerateName+"*", wlHost2[1].IP,
						metrics.NoService, 1, 3,
						[]metrics.ExpectedPolicy{
							{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
							{},
						})
					if err != nil {
						errs = append(errs, fmt.Sprintf("Error agg for allowed; agg src port; source %s; flow 2: %v", source.Name, err))
					}
				}

				err = flowTester.CheckFlow(
					"wep default "+wlHost1[0].Name+" "+wlHost1[0].WorkloadEndpoint.GenerateName+"*", wlHost1[0].IP,
					"hep - host2-eth0 "+tc.Felixes[1].Hostname, tc.Felixes[1].IP,
					metrics.NoService, 1, 3,
					[]metrics.ExpectedPolicy{
						{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
						{"dst", "allow", []string{"0|default|default.gnp-1|allow|0"}},
					})
				if err != nil {
					errs = append(errs, fmt.Sprintf("Error agg for allowed; agg src port; hep: %v", err))
				}

				if networkSetIPsSupported {
					err = flowTester.CheckFlow(
						"wep default "+wlHost2[0].Name+" "+wlHost2[0].WorkloadEndpoint.GenerateName+"*", wlHost2[0].IP,
						"ns - ns-1 ns-1", tc.Felixes[0].IP,
						metrics.NoService, 1, 3,
						[]metrics.ExpectedPolicy{
							{}, // ""
							{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
						})
				} else {
					err = flowTester.CheckFlow(
						"wep default "+wlHost2[0].Name+" "+wlHost2[0].WorkloadEndpoint.GenerateName+"*", wlHost2[0].IP,
						"net - - pvt", tc.Felixes[0].IP,
						metrics.NoService, 1, 3,
						[]metrics.ExpectedPolicy{
							{}, // ""
							{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
						})
				}
				if err != nil {
					errs = append(errs, fmt.Sprintf("Error agg for allowed; agg src port; netset: %v", err))
				}
			case AggrByPodPrefix:
				err = flowTester.CheckFlow(
					"wep default - wl-host1-*", "",
					"wep default - wl-host2-*", "",
					metrics.NoService, 1, 24,
					[]metrics.ExpectedPolicy{
						{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
						{}, // ""
					})
				if err != nil {
					errs = append(errs, fmt.Sprintf("Error agg for allowed; agg pod prefix; flow 1: %v", err))
				}
				err = flowTester.CheckFlow(
					"wep default - wl-host1-*", "",
					"wep default - wl-host2-*", "",
					metrics.NoService, 1, 12,
					[]metrics.ExpectedPolicy{
						{}, // ""
						{"dst", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
					})
				if err != nil {
					errs = append(errs, fmt.Sprintf("Error agg for allowed; agg pod prefix; flow 2: %v", err))
				}

				var policies []metrics.ExpectedPolicy

				policies = []metrics.ExpectedPolicy{
					{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
					{"dst", "allow", []string{"0|default|default.gnp-1|allow|0"}},
				}

				err = flowTester.CheckFlow(
					"wep default - wl-host1-*", "",
					"hep - - "+tc.Felixes[1].Hostname, "",
					metrics.NoService, 1, 3, policies)
				if err != nil {
					errs = append(errs, fmt.Sprintf("Error agg for allowed; agg pod prefix; hep: %v", err))
				}

				if networkSetIPsSupported {
					err = flowTester.CheckFlow(
						"wep default - wl-host2-*", "",
						"ns - - ns-1", "",
						metrics.NoService, 1, 3,
						[]metrics.ExpectedPolicy{
							{}, // ""
							{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
						})
				} else {
					err = flowTester.CheckFlow(
						"wep default - wl-host2-*", "",
						"net - - pvt", "",
						metrics.NoService, 1, 3,
						[]metrics.ExpectedPolicy{
							{}, // ""
							{"src", "allow", []string{"0|__PROFILE__|__PROFILE__.default|allow|0"}},
						})
				}
				if err != nil {
					errs = append(errs, fmt.Sprintf("Error agg for allowed; agg pod prefix; netset: %v", err))
				}
			}

			switch expectation.aggregationForDenied {
			case AggrNone:
				for _, source := range wlHost1 {
					err = flowTester.CheckFlow(
						"wep default "+source.Name+" "+source.WorkloadEndpoint.GenerateName+"*", source.IP,
						"wep default "+wlHost2[1].Name+" "+wlHost2[1].WorkloadEndpoint.GenerateName+"*", wlHost2[1].IP,
						metrics.NoService, 3, 1,
						[]metrics.ExpectedPolicy{
							{}, // ""
							{"dst", "deny", []string{"0|default|default/default.np-1|deny|0"}},
						})
					if err != nil {
						errs = append(errs, fmt.Sprintf("Error agg for denied; agg none: %v", err))
					}
				}
			case AggrBySourcePort:
				for _, source := range wlHost1 {
					err = flowTester.CheckFlow(
						"wep default "+source.Name+" "+source.WorkloadEndpoint.GenerateName+"*", source.IP,
						"wep default "+wlHost2[1].Name+" "+wlHost2[1].WorkloadEndpoint.GenerateName+"*", wlHost2[1].IP,
						metrics.NoService, 1, 3,
						[]metrics.ExpectedPolicy{
							{}, // ""
							{"dst", "deny", []string{"0|default|default/default.np-1|deny|0"}},
						})
					if err != nil {
						errs = append(errs, fmt.Sprintf("Error agg for denied; agg source port: %v", err))
					}
				}
			case AggrByPodPrefix:
				err = flowTester.CheckFlow(
					"wep default - wl-host1-*", "",
					"wep default - wl-host2-*", "",
					metrics.NoService, 1, 12,
					[]metrics.ExpectedPolicy{
						{}, // ""
						{"dst", "deny", []string{"0|default|default/default.np-1|deny|0"}},
					})
				if err != nil {
					errs = append(errs, fmt.Sprintf("Error agg for denied; agg pod prefix: %v", err))
				}
			}

			// Finally check that there are no remaining flow logs that we did not expect.
			err = flowTester.CheckAllFlowsAccountedFor()
			if err != nil {
				errs = append(errs, err.Error())
			}

			if len(errs) == 0 {
				return nil
			}

			return errors.New(strings.Join(errs, "\n==============\n"))
		}, "30s", "3s").ShouldNot(HaveOccurred())
	}

	cloudAndFile := func(flowLogsOutput string) {
		BeforeEach(func() {
			opts.ExtraEnvVars["FELIX_FLOWLOGSFLUSHINTERVAL"] = "2"
			opts.ExtraEnvVars["FELIX_FLOWLOGSENABLEHOSTENDPOINT"] = "true"
			opts.ExtraEnvVars["FELIX_FLOWLOGSFILEENABLED"] = "true"

			// Defaults for how we expect flow logs to be generated.
			expectation.labels = false
			expectation.policies = false
			expectation.aggregationForAllowed = AggrByPodPrefix
			expectation.aggregationForDenied = AggrBySourcePort
		})

		Context("with endpoint labels", func() {

			BeforeEach(func() {
				opts.ExtraEnvVars["FELIX_FLOWLOGSFILEINCLUDELABELS"] = "true"
				expectation.labels = true
			})

			It("should get expected flow logs", func() {
				checkFlowLogs(flowLogsOutput)
			})
		})

		Context("with allowed aggregation none", func() {

			BeforeEach(func() {
				opts.ExtraEnvVars["FELIX_FLOWLOGSFILEAGGREGATIONKINDFORALLOWED"] = strconv.Itoa(int(AggrNone))
				expectation.aggregationForAllowed = AggrNone
			})

			It("should get expected flow logs", func() {
				checkFlowLogs(flowLogsOutput)
			})
		})

		Context("with allowed aggregation by source port", func() {

			BeforeEach(func() {
				opts.ExtraEnvVars["FELIX_FLOWLOGSFILEAGGREGATIONKINDFORALLOWED"] = strconv.Itoa(int(AggrBySourcePort))
				expectation.aggregationForAllowed = AggrBySourcePort
			})

			It("should get expected flow logs", func() {
				checkFlowLogs(flowLogsOutput)
			})
		})

		Context("with allowed aggregation by pod prefix", func() {

			BeforeEach(func() {
				opts.ExtraEnvVars["FELIX_FLOWLOGSFILEAGGREGATIONKINDFORALLOWED"] = strconv.Itoa(int(AggrByPodPrefix))
				expectation.aggregationForAllowed = AggrByPodPrefix
			})

			It("should get expected flow logs", func() {
				checkFlowLogs(flowLogsOutput)
			})
		})

		Context("with denied aggregation none", func() {

			BeforeEach(func() {
				opts.ExtraEnvVars["FELIX_FLOWLOGSFILEAGGREGATIONKINDFORDENIED"] = strconv.Itoa(int(AggrNone))
				expectation.aggregationForDenied = AggrNone
			})

			It("should get expected flow logs", func() {
				checkFlowLogs(flowLogsOutput)
			})
		})

		Context("with denied aggregation by source port", func() {

			BeforeEach(func() {
				opts.ExtraEnvVars["FELIX_FLOWLOGSFILEAGGREGATIONKINDFORDENIED"] = strconv.Itoa(int(AggrBySourcePort))
				expectation.aggregationForDenied = AggrBySourcePort
			})

			It("should get expected flow logs", func() {
				checkFlowLogs(flowLogsOutput)
			})
		})

		Context("with denied aggregation by pod prefix", func() {

			BeforeEach(func() {
				opts.ExtraEnvVars["FELIX_FLOWLOGSFILEAGGREGATIONKINDFORDENIED"] = strconv.Itoa(int(AggrByPodPrefix))
				expectation.aggregationForDenied = AggrByPodPrefix
			})

			It("should get expected flow logs", func() {
				checkFlowLogs(flowLogsOutput)
			})
		})

		Context("with policies", func() {

			BeforeEach(func() {
				opts.ExtraEnvVars["FELIX_FLOWLOGSFILEINCLUDEPOLICIES"] = "true"
				expectation.policies = true
			})

			It("should get expected flow logs", func() {
				checkFlowLogs(flowLogsOutput)
			})
		})

	}

	Context("File flow logs", func() {
		Context("File output", func() { cloudAndFile("file") })
	})

	Context("File flow logs only", func() {

		BeforeEach(func() {
			// Defaults for how we expect flow logs to be generated.
			expectation.labels = false
			expectation.policies = false
			expectation.aggregationForAllowed = AggrByPodPrefix
			expectation.aggregationForDenied = AggrBySourcePort

			opts.ExtraEnvVars["FELIX_FLOWLOGSFILEENABLED"] = "true"
			opts.ExtraEnvVars["FELIX_FLOWLOGSFLUSHINTERVAL"] = "2"
			opts.ExtraEnvVars["FELIX_FLOWLOGSENABLEHOSTENDPOINT"] = "true"
		})

		It("should get expected flow logs", func() {
			checkFlowLogs("file")
		})

		Context("with an expired license", func() {
			BeforeEach(func() {
				useInvalidLicense = true
				// Reduce license poll interval so felix won't generate any flow logs before it spots the bad license.
				opts.ExtraEnvVars["FELIX_DebugUseShortPollIntervals"] = "true"
			})

			It("should get no flow logs", func() {
				endTime := time.Now().Add(30 * time.Second)
				// Check at least twice and for at least 30s.
				attempts := 0
				for time.Now().Before(endTime) || attempts < 2 {
					for _, f := range tc.Felixes {
						_, err := flowlogs.ReadFlowLogsFile(f.FlowLogDir())
						Expect(err).To(BeAssignableToTypeOf(&os.PathError{}))
					}
					time.Sleep(1 * time.Second)
					attempts++
				}
			})
		})
	})

	AfterEach(func() {
		if CurrentGinkgoTestDescription().Failed {
			for _, felix := range tc.Felixes {
				felix.Exec("iptables-save", "-c")
				felix.Exec("ipset", "list")
				felix.Exec("ip", "r")
				felix.Exec("ip", "a")
			}
			if bpfEnabled {
				for _, felix := range tc.Felixes {
					felix.Exec("calico-bpf", "ipsets", "dump")
					felix.Exec("calico-bpf", "routes", "dump")
					felix.Exec("calico-bpf", "nat", "dump")
					felix.Exec("calico-bpf", "nat", "aff")
					felix.Exec("calico-bpf", "conntrack", "dump")
					felix.Exec("calico-bpf", "arp", "dump")
					felix.Exec("calico-bpf", "counters", "dump")
					felix.Exec("calico-bpf", "ifstate", "dump")
					felix.Exec("calico-bpf", "policy", "dump", "eth0", "all")
				}
				for _, w := range wlHost1 {
					tc.Felixes[0].Exec("calico-bpf", "policy", "dump", w.InterfaceName, "all")
				}
				for _, w := range wlHost1 {
					tc.Felixes[1].Exec("calico-bpf", "policy", "dump", w.InterfaceName, "all")
				}
			}
		}

		for _, wl := range wlHost1 {
			wl.Stop()
		}
		for _, wl := range wlHost2 {
			wl.Stop()
		}
		for _, wl := range hostW {
			wl.Stop()
		}
		for _, felix := range tc.Felixes {
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

var _ = infrastructure.DatastoreDescribe("nat outgoing flow log tests", []apiconfig.DatastoreType{apiconfig.Kubernetes}, func(getInfra infrastructure.InfraFactory) {
	var (
		infra  infrastructure.DatastoreInfra
		tc     infrastructure.TopologyContainers
		client client.Interface

		workload1 *workload.Workload
		workload2 *workload.Workload
	)

	BeforeEach(func() {
		var err error

		infra = getInfra()
		opts := infrastructure.DefaultTopologyOptions()

		opts.ExtraEnvVars["FELIX_FLOWLOGSFLUSHINTERVAL"] = "2"
		opts.ExtraEnvVars["FELIX_FLOWLOGSFILEENABLED"] = "true"

		tc, client = infrastructure.StartSingleNodeTopology(opts, infra)

		ctx := context.Background()

		// Create an IPPool and assign an IP from that pool to workload1 so the packets are SNAT'd when a connection is
		// made from workload1 to workload2
		ippool := api.NewIPPool()
		ippool.Name = "nat-pool"
		ippool.Spec.CIDR = "10.244.255.0/24"
		ippool.Spec.NATOutgoing = true
		ippool, err = client.IPPools().Create(ctx, ippool, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())

		workload1 = workload.Run(tc.Felixes[0], "w1", "default", "10.244.255.1", "8055", "tcp")
		workload1.ConfigureInInfra(infra)

		workload2 = workload.Run(tc.Felixes[0], "w2", "default", "10.65.0.2", "8055", "tcp")
		workload2.ConfigureInInfra(infra)
	})

	AfterEach(func() {
		tc.Stop()
		infra.Stop()
	})

	It("Should report the nat outgoing ports for an SNAT'd flow", func() {
		cc := &connectivity.Checker{
			Protocol: "tcp",
		}

		cc.ExpectSome(workload1, workload2)
		cc.CheckConnectivity()

		var flows []flowlog.FlowLog
		var err error
		Eventually(func() error {
			flows, err = flowlogs.ReadFlowLogs(tc.Felixes[0].FlowLogDir(), "file")
			return err
		}, "20s", "1s").ShouldNot(HaveOccurred())

		Expect(flows).ShouldNot(BeEmpty())

		numExpectedFlows := 0
		// Test that flows from workload1 to workload2 have nat_outgoing_ports set.
		for _, flow := range flows {
			if flow.SrcMeta.AggregatedName == workload1.Name &&
				flow.DstMeta.AggregatedName == workload2.Name {
				numExpectedFlows++
				Expect(flow.NatOutgoingPorts).ShouldNot(BeEmpty())
			}
		}

		// Ensure that there was at least one flow that went through the nat outgoing port test above.
		Expect(numExpectedFlows).ShouldNot(BeZero())
	})
})
