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

package infrastructure

import (
	"fmt"
	"path/filepath"

	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/felix/fv/containers"
	"github.com/projectcalico/felix/fv/utils"
)

type Felix struct {
	*containers.Container

	// ExpectedIPIPTunnelAddr contains the IP that the infrastructure expects to
	// get assigned to the IPIP tunnel.  Filled in by AddNode().
	ExpectedIPIPTunnelAddr string

	// IP of the Typha that this Felix is using (if any).
	TyphaIP string

	startupDelayed bool
}

func (f *Felix) GetFelixPID() int {
	if f.startupDelayed {
		log.Panic("GetFelixPID() called but startup is delayed")
	}
	return f.GetSinglePID("calico-felix")
}

func (f *Felix) GetFelixPIDs() []int {
	if f.startupDelayed {
		log.Panic("GetFelixPIDs() called but startup is delayed")
	}
	return f.GetPIDs("calico-felix")
}

func (f *Felix) TriggerDelayedStart() {
	if !f.startupDelayed {
		log.Panic("TriggerDelayedStart() called but startup wasn't delayed")
	}
	f.Exec("touch", "/start-trigger")
	f.startupDelayed = false
}

func RunFelix(infra DatastoreInfra, options TopologyOptions) *Felix {
	log.Info("Starting felix")
	ipv6Enabled := fmt.Sprint(options.EnableIPv6)

	args := infra.GetDockerArgs()
	args = append(args,
		"--privileged",
		"-e", "FELIX_LOGSEVERITYSCREEN="+options.FelixLogSeverity,
		"-e", "FELIX_PROMETHEUSMETRICSENABLED=true",
		"-e", "FELIX_PROMETHEUSREPORTERENABLED=true",
		"-e", "FELIX_USAGEREPORTINGENABLED=false",
		"-e", "FELIX_IPV6SUPPORT="+ipv6Enabled,
		"-v", "/lib/modules:/lib/modules",
	)

	if options.WithPrometheusPortTLS {
		EnsureTLSCredentials()
		options.ExtraVolumes[CertDir] = CertDir
		options.ExtraEnvVars["FELIX_PROMETHEUSREPORTERCAFILE"] = filepath.Join(CertDir, "ca.crt")
		options.ExtraEnvVars["FELIX_PROMETHEUSREPORTERKEYFILE"] = filepath.Join(CertDir, "server.key")
		options.ExtraEnvVars["FELIX_PROMETHEUSREPORTERCERTFILE"] = filepath.Join(CertDir, "server.crt")
		options.ExtraEnvVars["FELIX_PROMETHEUSMETRICSCAFILE"] = filepath.Join(CertDir, "ca.crt")
		options.ExtraEnvVars["FELIX_PROMETHEUSMETRICSKEYFILE"] = filepath.Join(CertDir, "server.key")
		options.ExtraEnvVars["FELIX_PROMETHEUSMETRICSCERTFILE"] = filepath.Join(CertDir, "server.crt")
	}

	if options.DelayFelixStart {
		args = append(args, "-e", "DELAY_FELIX_START=true")
	}

	for k, v := range options.ExtraEnvVars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	for k, v := range options.ExtraVolumes {
		args = append(args, "-v", fmt.Sprintf("%s:%s", k, v))
	}

	args = append(args,
		utils.Config.FelixImage,
	)

	c := containers.Run("felix",
		containers.RunOpts{AutoRemove: true},
		args...,
	)

	if options.EnableIPv6 {
		c.Exec("sysctl", "-w", "net.ipv6.conf.all.disable_ipv6=0")
		c.Exec("sysctl", "-w", "net.ipv6.conf.default.disable_ipv6=0")
		c.Exec("sysctl", "-w", "net.ipv6.conf.lo.disable_ipv6=0")
		c.Exec("sysctl", "-w", "net.ipv6.conf.all.forwarding=1")
	} else {
		c.Exec("sysctl", "-w", "net.ipv6.conf.all.disable_ipv6=1")
		c.Exec("sysctl", "-w", "net.ipv6.conf.default.disable_ipv6=1")
		c.Exec("sysctl", "-w", "net.ipv6.conf.lo.disable_ipv6=1")
		c.Exec("sysctl", "-w", "net.ipv6.conf.all.forwarding=0")
	}

	// Configure our model host to drop forwarded traffic by default.  Modern
	// Kubernetes/Docker hosts now have this setting, and the consequence is that
	// whenever Calico policy intends to allow a packet, it must explicitly ACCEPT
	// that packet, not just allow it to pass through cali-FORWARD and assume it will
	// be accepted by the rest of the chain.  Establishing that setting in this FV
	// allows us to test that.
	c.Exec("iptables", "-P", "FORWARD", "DROP")

	return &Felix{
		Container:      c,
		startupDelayed: options.DelayFelixStart,
	}
}
