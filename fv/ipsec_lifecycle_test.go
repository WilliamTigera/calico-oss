// +build fvtests

// Copyright (c) 2018 Tigera, Inc. All rights reserved.

package fv_test

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/projectcalico/felix/fv/connectivity"
	"github.com/projectcalico/felix/fv/infrastructure"
	"github.com/projectcalico/felix/fv/utils"
	"github.com/projectcalico/felix/fv/workload"
	"github.com/projectcalico/libcalico-go/lib/apiconfig"
)

var _ = infrastructure.DatastoreDescribe("IPsec lifecycle tests", []apiconfig.DatastoreType{apiconfig.EtcdV3, apiconfig.Kubernetes}, func(getInfra infrastructure.InfraFactory) {

	var (
		infra   infrastructure.DatastoreInfra
		felixes []*infrastructure.Felix
		// w[n] is a simulated workload for host n.  It has its own network namespace (as if it was a container).
		w [2]*workload.Workload
		// hostW[n] is a simulated host networked workload for host n.  It runs in felix's network namespace.
		hostW [2]*workload.Workload
		cc    *connectivity.Checker
	)

	BeforeEach(func() {
		infra = getInfra()
		topologyOptions := infrastructure.DefaultTopologyOptions()

		// Enable IPsec.
		topologyOptions.ExtraEnvVars["FELIX_IPSECMODE"] = "PSK"
		topologyOptions.ExtraEnvVars["FELIX_IPSECPSKFILE"] = "/proc/1/cmdline"
		topologyOptions.ExtraEnvVars["FELIX_IPSECIKEAlGORITHM"] = "aes128gcm16-prfsha256-ecp256"
		topologyOptions.ExtraEnvVars["FELIX_IPSECESPAlGORITHM"] = "aes128gcm16-ecp256"
		topologyOptions.ExtraEnvVars["FELIX_IPSECREKEYTIME"] = "20"
		topologyOptions.IPIPEnabled = false

		felixes, _ = infrastructure.StartNNodeTopology(2, topologyOptions, infra)

		// Install a default profile that allows all ingress and egress, in the absence of any Policy.
		infra.AddDefaultAllow()

		// Create workloads, using that profile.  One on each "host".
		for ii := range w {
			wIP := fmt.Sprintf("10.65.%d.2", ii)
			wName := fmt.Sprintf("w%d", ii)
			w[ii] = workload.Run(felixes[ii], wName, "default", wIP, "8055", "udp")
			w[ii].ConfigureInDatastore(infra)

			hostW[ii] = workload.Run(felixes[ii], fmt.Sprintf("host%d", ii), "", felixes[ii].IP, "8055", "udp")
		}

		// Wait for Felix to program the IPsec policy.  Otherwise, we might see some unencrypted traffic at
		// start-of-day.  There's not much we can do about that in general since we don't know the workload's IP
		// to blacklist it until we hear about the workload.
		const numPoliciesPerWep = 3
		for i, f := range felixes {
			for j := range felixes {
				if i == j {
					continue
				}

				polCount := func() int {
					out, err := f.ExecOutput("ip", "xfrm", "policy")
					Expect(err).NotTo(HaveOccurred())
					return strings.Count(out, w[j].IP)
				}
				// Felix might restart during set up, causing a 2s delay here.
				Eventually(polCount, "5s", "100ms").Should(Equal(numPoliciesPerWep),
					fmt.Sprintf("Expected to see %d IPsec policies for workload IP %s in felix container %s",
						numPoliciesPerWep, w[j].IP, f.Name))
			}
		}

		cc = &connectivity.Checker{Protocol: "udp"}
	})

	AfterEach(func() {
		if CurrentGinkgoTestDescription().Failed {
			utils.Run("docker", "ps", "-a")
			for _, felix := range felixes {
				felix.Exec("swanctl", "--list-sas")
				felix.Exec("ip", "-s", "xfrm", "state")
				felix.Exec("ip", "-s", "xfrm", "policy")
			}
		}

		for _, wl := range w {
			wl.Stop()
		}
		for _, wl := range hostW {
			wl.Stop()
		}
		for _, felix := range felixes {
			felix.Stop()
		}

		if CurrentGinkgoTestDescription().Failed {
			infra.DumpErrorData()
		}
		infra.Stop()
	})

	// Function to get number of SAs for connection (src->dest) and SPI for first SA on destination workload.
	getDestSPIs := func(src, dest *infrastructure.Felix) []string {
		output, err := dest.ExecOutput("ip", "xfrm", "state")
		Expect(err).NotTo(HaveOccurred())

		saDirectionInfo := regexp.QuoteMeta(fmt.Sprintf("src %s dst %s", src.IP, dest.IP))
		matches := regexp.MustCompile(saDirectionInfo+`(?s:.*?)spi (0x[a-f0-9]+)`).FindAllStringSubmatch(output, -1)
		var spis []string
		for _, m := range matches {
			spis = append(spis, m[1])
		}
		return spis
	}

	It("Should rekey properly and cause acceptable packet loss", func() {
		// Start packet loss test and monitor SA changes of a destination workload.

		// Do a simple connection test first.
		// We do not want to spend time on packet loss test if a simple connection test fails.
		cc.ExpectSome(w[0], w[1])
		cc.CheckConnectivity()
		cc.ResetExpectations()

		// Get the starting set of SPIs.  Usually, there'll only be one SA but there can be 2 if both Charons
		// happen to start their IKE exchange at the same time.
		var startSPIs []string
		Eventually(func() []string {
			startSPIs = getDestSPIs(felixes[0], felixes[1])
			return startSPIs
		}, "10s").Should(Or(HaveLen(1), HaveLen(2)))
		// The acceptable packet loss threshold here is slightly arbitrary.
		// We choose 50 because we've seen 22 packets lost in a Semaphore run.
		// Real fix to this issue (to drop 0 packets) would be in the charon code...
		cc.ExpectLoss(w[0], w[1], 30*time.Second, -1, 50)
		cc.CheckConnectivity()

		endSPIs := getDestSPIs(felixes[0], felixes[1])
		for _, s := range startSPIs {
			Expect(endSPIs).NotTo(ContainElement(s), fmt.Sprintf("Expected SA with SPI %s to have been removed after rekey", s))
		}
	})

	It("Felix should restart if charon daemon exits", func() {
		felix := felixes[0]

		// Get felix/charon's PID so we can check that it restarts...
		felixPID := felix.GetFelixPID()
		charonPID := felix.GetSinglePID("/usr/lib/ipsec/charon")

		// Kill charon daemon
		killProcess(felix, fmt.Sprint(charonPID))

		Eventually(felix.GetFelixPID, "5s", "100ms").ShouldNot(Equal(felixPID),
			"Felix failed to restart after killing the charon")

		Eventually(func() int {
			return felix.GetSinglePID("/usr/lib/ipsec/charon")
		}, "3s").ShouldNot(Equal(charonPID), "New charon process")
	})
})

func killProcess(felix *infrastructure.Felix, pidString string) {
	_, err := felix.ExecOutput("kill", "-9", pidString)
	Expect(err).NotTo(HaveOccurred())
}
