// Copyright (c) 2017-2019 Tigera, Inc. All rights reserved.
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
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/felix/fv/containers"
	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/errors"
	"github.com/projectcalico/libcalico-go/lib/options"
)

type TopologyOptions struct {
	FelixLogSeverity          string
	EnableIPv6                bool
	ExtraEnvVars              map[string]string
	ExtraVolumes              map[string]string
	WithTypha                 bool
	WithFelixTyphaTLS         bool
	TyphaLogSeverity          string
	IPIPEnabled               bool
	IPIPRoutesEnabled         bool
	VXLANEnabled              bool
	InitialFelixConfiguration *api.FelixConfiguration
	WithPrometheusPortTLS     bool
	NATOutgoingEnabled        bool
	DelayFelixStart           bool
}

func DefaultTopologyOptions() TopologyOptions {
	return TopologyOptions{
		FelixLogSeverity:  "info",
		EnableIPv6:        true,
		ExtraEnvVars:      map[string]string{},
		ExtraVolumes:      map[string]string{},
		WithTypha:         false,
		WithFelixTyphaTLS: false,
		TyphaLogSeverity:  "info",
		IPIPEnabled:       true,
		IPIPRoutesEnabled: true,
	}
}

func (opts TopologyOptions) EnableCloudWatchLogs(settings ...string) {

	// Enable CloudWatch logs.
	opts.ExtraEnvVars["FELIX_CLOUDWATCHLOGSREPORTERENABLED"] = "true"

	// Set particular settings for the calling test.
	param := ""
	for _, arg := range settings {
		if param == "" {
			param = "FELIX_CLOUDWATCHLOGS" + strings.ToUpper(arg)
		} else {
			opts.ExtraEnvVars[param] = arg
			param = ""
		}
	}
}

func (opts TopologyOptions) EnableFlowLogsFile(settings ...string) {

	// Enable CloudWatch logs.
	opts.ExtraEnvVars["FELIX_FLOWLOGSFILEENABLED"] = "true"

	// Set particular settings for the calling test.
	param := ""
	for _, arg := range settings {
		if param == "" {
			param = "FELIX_FLOWLOGSFILE" + strings.ToUpper(arg)
		} else {
			opts.ExtraEnvVars[param] = arg
			param = ""
		}
	}
}

// StartSingleNodeEtcdTopology starts an etcd container and a single Felix container; it initialises
// the datastore and installs a Node resource for the Felix node.
func StartSingleNodeEtcdTopology(options TopologyOptions) (felix *Felix, etcd *containers.Container, calicoClient client.Interface) {
	felixes, etcd, calicoClient := StartNNodeEtcdTopology(1, options)
	felix = felixes[0]
	return
}

// StartNNodeEtcdTopology starts an etcd container and a set of Felix hosts.  If n > 1, sets
// up IPIP, otherwise this is skipped.
//
// - Configures an IPAM pool for 10.65.0.0/16 (so that Felix programs the all-IPAM blocks IP set)
//   but (for simplicity) we don't actually use IPAM to assign IPs.
// - Configures routes between the hosts, giving each host 10.65.x.0/24, where x is the
//   index in the returned array.  When creating workloads, use IPs from the relevant block.
// - Configures the Tunnel IP for each host as 10.65.x.1.
func StartNNodeEtcdTopology(n int, opts TopologyOptions) (felixes []*Felix, etcd *containers.Container, client client.Interface) {
	log.Infof("Starting a %d-node etcd topology.", n)

	eds, err := GetEtcdDatastoreInfra()
	Expect(err).ToNot(HaveOccurred())
	etcd = eds.etcdContainer

	felixes, client = StartNNodeTopology(n, opts, eds)

	return
}

// StartSingleNodeEtcdTopology starts an etcd container and a single Felix container; it initialises
// the datastore and installs a Node resource for the Felix node.
func StartSingleNodeTopology(options TopologyOptions, infra DatastoreInfra) (felix *Felix, calicoClient client.Interface) {
	felixes, calicoClient := StartNNodeTopology(1, options, infra)
	felix = felixes[0]
	return
}

// StartNNodeEtcdTopology starts an etcd container and a set of Felix hosts.  If n > 1, sets
// up IPIP, otherwise this is skipped.
//
// - Configures an IPAM pool for 10.65.0.0/16 (so that Felix programs the all-IPAM blocks IP set)
//   but (for simplicity) we don't actually use IPAM to assign IPs.
// - Configures routes between the hosts, giving each host 10.65.x.0/24, where x is the
//   index in the returned array.  When creating workloads, use IPs from the relevant block.
// - Configures the Tunnel IP for each host as 10.65.x.1.
func StartNNodeTopology(n int, opts TopologyOptions, infra DatastoreInfra) (felixes []*Felix, client client.Interface) {
	log.Infof("Starting a %d-node topology.", n)
	success := false
	var err error
	defer func() {
		if !success {
			log.WithError(err).Error("Failed to start topology, tearing down containers")
			for _, felix := range felixes {
				felix.Stop()
			}
			infra.Stop()
		}
	}()

	// Get client.
	client = infra.GetCalicoClient()
	mustInitDatastore(client)

	// Add a CNX license.
	ApplyValidLicense(client)

	// If asked to, pre-create a felix configuration.  We do this before enabling IPIP because IPIP set-up can
	// create/update a FelixConfiguration as a side-effect.
	if opts.InitialFelixConfiguration != nil {
		log.WithField("config", opts.InitialFelixConfiguration).Info(
			"Installing initial FelixConfiguration")
		Eventually(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err = client.FelixConfigurations().Create(ctx, opts.InitialFelixConfiguration, options.SetOptions{})
			if _, ok := err.(errors.ErrorResourceAlreadyExists); ok {
				// Try to delete the unexpected config, then, if there's still time in the Eventually loop,
				// we'll try to recreate
				_, _ = client.FelixConfigurations().Delete(ctx, "default", options.DeleteOptions{})
			}
			return err
		}, "10s").ShouldNot(HaveOccurred())
	}

	if n > 1 {
		Eventually(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ipPool := api.NewIPPool()
			ipPool.Name = "test-pool"
			ipPool.Spec.CIDR = "10.65.0.0/16"
			ipPool.Spec.NATOutgoing = opts.NATOutgoingEnabled
			if opts.IPIPEnabled {
				ipPool.Spec.IPIPMode = api.IPIPModeAlways
			} else {
				ipPool.Spec.IPIPMode = api.IPIPModeNever
			}
			if opts.VXLANEnabled {
				ipPool.Spec.VXLANMode = api.VXLANModeAlways
			} else {
				ipPool.Spec.VXLANMode = api.VXLANModeNever
			}
			_, err = client.IPPools().Create(ctx, ipPool, options.SetOptions{})
			return err
		}).ShouldNot(HaveOccurred())
	}

	typhaIP := ""
	if opts.WithTypha {
		typha := RunTypha(infra, opts)
		opts.ExtraEnvVars["FELIX_TYPHAADDR"] = typha.IP + ":5473"
		typhaIP = typha.IP
	}

	for i := 0; i < n; i++ {
		// Then start Felix and create a node for it.
		felix := RunFelix(infra, opts)
		felix.TyphaIP = typhaIP
		if opts.IPIPEnabled {
			infra.SetExpectedIPIPTunnelAddr(felix, i, bool(n > 1))
		}
		if opts.VXLANEnabled {
			infra.SetExpectedVXLANTunnelAddr(felix, i, bool(n > 1))
		}

		var w chan struct{}
		if !opts.DelayFelixStart && felix.ExpectedIPIPTunnelAddr != "" {
			// If felix has an IPIP tunnel address defined, Felix may restart after loading its config.
			// Handle that here by monitoring the log and waiting for the correct tunnel IP to show up
			// before we return.
			w = felix.WatchStdoutFor(regexp.MustCompile(
				`"IpInIpTunnelAddr":"` + regexp.QuoteMeta(felix.ExpectedIPIPTunnelAddr) + `"`))
		}
		infra.AddNode(felix, i, bool(n > 1))
		if w != nil {
			// Wait for any Felix restart...
			log.Info("Wait for Felix to restart")
			Eventually(w, "10s").Should(BeClosed(),
				"Timed out waiting for Felix to restart with IpInIpTunnelAddress")
		}
		felixes = append(felixes, felix)
	}

	// Set up routes between the hosts, note: we're not using IPAM here but we set up similar
	// CIDR-based routes.
	for i, iFelix := range felixes {
		for j, jFelix := range felixes {
			if i == j {
				continue
			}

			jBlock := fmt.Sprintf("10.65.%d.0/24", j)
			if opts.IPIPEnabled && opts.IPIPRoutesEnabled {
				err := iFelix.ExecMayFail("ip", "route", "add", jBlock, "via", jFelix.IP, "dev", "tunl0", "onlink")
				Expect(err).ToNot(HaveOccurred())
			} else if !opts.VXLANEnabled {
				// If VXLAN is enabled, Felix will program these routes itself.
				err := iFelix.ExecMayFail("ip", "route", "add", jBlock, "via", jFelix.IP, "dev", "eth0")
				Expect(err).ToNot(HaveOccurred())
			}
		}
	}
	success = true
	return
}

func mustInitDatastore(client client.Interface) {
	Eventually(func() error {
		log.Info("Initializing the datastore...")
		ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
		err := client.EnsureInitialized(
			ctx,
			"v3.0.0-test",
			"v2.0.0-test",
			"felix-fv",
		)
		log.WithError(err).Info("EnsureInitialized result")
		return err
	}).ShouldNot(HaveOccurred(), "mustInitDatastore failed")
}

func applyLicense(client client.Interface, token, cert string) {
	Eventually(func() error {
		var licenseKey *api.LicenseKey

		ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
		licenseKey, err := client.LicenseKey().Get(ctx, "default", options.GetOptions{})
		if err != nil {
			// Assume does not exist
			log.WithError(err).Info("Failed to get license key, will try to create it...")
			licenseKey = api.NewLicenseKey()
			licenseKey.Name = "default"
		}
		licenseKey.Spec.Token = token
		licenseKey.Spec.Certificate = cert
		if licenseKey.ResourceVersion != "" {
			_, err = client.LicenseKey().Update(ctx, licenseKey, options.SetOptions{})
		} else {
			_, err = client.LicenseKey().Create(ctx, licenseKey, options.SetOptions{})
		}
		log.WithError(err).Info("Add license result")
		return err
	}).ShouldNot(HaveOccurred())
}

func ApplyValidLicense(client client.Interface) {
	applyLicense(client, `eyJhbGciOiJBMTI4R0NNS1ciLCJjdHkiOiJKV1QiLCJlbmMiOiJBMTI4R0NNIiwiaXYiOiJlaWNWbHlTbGxFMlAtQ25tIiwidGFnIjoiTk1KSHlRV2M1UWZ6M1dydHNCamxhZyIsInR5cCI6IkpXVCJ9.afBv55v15cFsaHqcsyDkfA.yBMyDIRFBtWxyNxI.Q18a_G6i2kiN0NsqtGSQjc0o2CrkdivRJFkpAlkYIttBAultPADLZmfgf0nzVqZkKAkOGSbIxjY5BgW59FEyaiEs8sL11HZqPB8l2eOqK4BSj5wx3yEhsFzQkD1pZZz8qVgE0Ml3SaSiGVhe4ADTiSsUBbU9JD_aRaa4m1QvS4IQiT_QuWxUtOi-LRXsvHURnkTs3K_WGu7_QW5RRHDGD_CP2kfTUMeSvcWSiT8vgrgPj5q4Zpz4XTWNT-u0sJraWu79tOqCu9YwKeDVMKgJ04sunGc9xsimkhUmOnwuiIEeR24GyL7I5FDrCUC6Oiif62o_ECaQA6NjHAFdq-LNCIb902tKD0BQ-q6AzUrjs21GNr9_oJZJXKL6m74UJULMVgxXZKze2IH9EXtQ0b2jHbi9-qyMp6Rc34Z4HtYmQPB3CRHjDTmzUpEXOsF-reYffRHLJY5DUk7fDfTnhBmUksYonuuGLKep1_YYAiAhkomj7mupFNVN5JnZx8P-v4cfAr4PZxF6Lw5utN5R1hArroYA1Z-2Et0LC6BbE6Q1j7_zmaBs2BEnNfWNn2LFBBOCHzax51ISz_DIcGSidsRDNE9vQDYhcb9MGqOtaCDAA5zHCArVxu2PiwJj6JNbdNB9nvLWlAqxUU4zJwNPFd9xQIR53RFNB0LHID-ab_H7_NFX0auolwSz5Fm14ID4SKvD7_1aqUJG9_WiEtNz9yZJL5vkspdSxnR59L4alUYErxSEWGmOIBvJPemftZBilH1Vmxt0MFyu7sxK_uEJ55OtxNXCfaa_MPp0Yhn9mjTeCSMH8dV6ahZuL8B85BHjFkqY_nLV5UKEvPcyflo4JLDAOvhTZ0bbqvheEx48FQPisSJoK5zY61FqK1tFrID5rdJQ4RMpe4Bix0Dy213hN08U1iNklHUgR-MMw2f4sfGouBm-3B-7P9bqwQlEVyKLkyBzOgWd0PADc0i5bdxCxoqL8AAehPTEGIk-lb2TKe71dCW47oZQwigRgbLHRJnYF9iVlFoXXf-MLH_edh5Gi2OD397MtuBvpGWS8KVjiyUYX-NhvOqgzqrRCH-7kRkmYBsL446hNzGYMjbxut488a2amVrsIuR4oerJnkSdK3o.MnNW4M-g2iiXOi1GVe5zaQ`,
		`-----BEGIN CERTIFICATE-----
MIIFxjCCA66gAwIBAgIQVq3rz5D4nQF1fIgMEh71DzANBgkqhkiG9w0BAQsFADCB
tTELMAkGA1UEBhMCVVMxEzARBgNVBAgTCkNhbGlmb3JuaWExFjAUBgNVBAcTDVNh
biBGcmFuY2lzY28xFDASBgNVBAoTC1RpZ2VyYSwgSW5jMSIwIAYDVQQLDBlTZWN1
cml0eSA8c2lydEB0aWdlcmEuaW8+MT8wPQYDVQQDEzZUaWdlcmEgRW50aXRsZW1l
bnRzIEludGVybWVkaWF0ZSBDZXJ0aWZpY2F0ZSBBdXRob3JpdHkwHhcNMTgwNDA1
MjEzMDI5WhcNMjAxMDA2MjEzMDI5WjCBnjELMAkGA1UEBhMCVVMxEzARBgNVBAgT
CkNhbGlmb3JuaWExFjAUBgNVBAcTDVNhbiBGcmFuY2lzY28xFDASBgNVBAoTC1Rp
Z2VyYSwgSW5jMSIwIAYDVQQLDBlTZWN1cml0eSA8c2lydEB0aWdlcmEuaW8+MSgw
JgYDVQQDEx9UaWdlcmEgRW50aXRsZW1lbnRzIENlcnRpZmljYXRlMIIBojANBgkq
hkiG9w0BAQEFAAOCAY8AMIIBigKCAYEAwg3LkeHTwMi651af/HEXi1tpM4K0LVqb
5oUxX5b5jjgi+LHMPzMI6oU+NoGPHNqirhAQqK/k7W7r0oaMe1APWzaCAZpHiMxE
MlsAXmLVUrKg/g+hgrqeije3JDQutnN9h5oZnsg1IneBArnE/AKIHH8XE79yMG49
LaKpPGhpF8NoG2yoWFp2ekihSohvqKxa3m6pxoBVdwNxN0AfWxb60p2SF0lOi6B3
hgK6+ILy08ZqXefiUs+GC1Af4qI1jRhPkjv3qv+H1aQVrq6BqKFXwWIlXCXF57CR
hvUaTOG3fGtlVyiPE4+wi7QDo0cU/+Gx4mNzvmc6lRjz1c5yKxdYvgwXajSBx2pw
kTP0iJxI64zv7u3BZEEII6ak9mgUU1CeGZ1KR2Xu80JiWHAYNOiUKCBYHNKDCUYl
RBErYcAWz2mBpkKyP6hbH16GjXHTTdq5xENmRDHabpHw5o+21LkWBY25EaxjwcZa
Y3qMIOllTZ2iRrXu7fSP6iDjtFCcE2bFAgMBAAGjZzBlMA4GA1UdDwEB/wQEAwIF
oDATBgNVHSUEDDAKBggrBgEFBQcDAjAdBgNVHQ4EFgQUIY7LzqNTzgyTBE5efHb5
kZ71BUEwHwYDVR0jBBgwFoAUxZA5kifzo4NniQfGKb+4wruTIFowDQYJKoZIhvcN
AQELBQADggIBAAK207LaqMrnphF6CFQnkMLbskSpDZsKfqqNB52poRvUrNVUOB1w
3dSEaBUjhFgUU6yzF+xnuH84XVbjD7qlM3YbdiKvJS9jrm71saCKMNc+b9HSeQAU
DGY7GPb7Y/LG0GKYawYJcPpvRCNnDLsSVn5N4J1foWAWnxuQ6k57ymWwcddibYHD
OPakOvO4beAnvax3+K5dqF0bh2Np79YolKdIgUVzf4KSBRN4ZE3AOKlBfiKUvWy6
nRGvu8O/8VaI0vGaOdXvWA5b61H0o5cm50A88tTm2LHxTXynE3AYriHxsWBbRpoM
oFnmDaQtGY67S6xGfQbwxrwCFd1l7rGsyBQ17cuusOvMNZEEWraLY/738yWKw3qX
U7KBxdPWPIPd6iDzVjcZrS8AehUEfNQ5yd26gDgW+rZYJoAFYv0vydMEyoI53xXs
cpY84qV37ZC8wYicugidg9cFtD+1E0nVgOLXPkHnmc7lIDHFiWQKfOieH+KoVCbb
zdFu3rhW31ygphRmgszkHwApllCTBBMOqMaBpS8eHCnetOITvyB4Kiu1/nKvVxhY
exit11KQv8F3kTIUQRm0qw00TSBjuQHKoG83yfimlQ8OazciT+aLpVaY8SOrrNnL
IJ8dHgTpF9WWHxx04DDzqrT7Xq99F9RzDzM7dSizGxIxonoWcBjiF6n5
-----END CERTIFICATE-----`)
}

func ApplyExpiredLicense(client client.Interface) {
	applyLicense(client, `eyJhbGciOiJBMTI4R0NNS1ciLCJjdHkiOiJKV1QiLCJlbmMiOiJBMTI4R0NNIiwiaXYiOiJvTGJ2cDhjOVRQdWRka3hiIiwidGFnIjoiSlNHMTZNT1hYRTBiUXpNc082OVFNdyIsInR5cCI6IkpXVCJ9.trUzyPt-thRmY0Xx23JAWQ.EzvF_OIEKtvzlXbN.EOyEEePRt2Ns1gb9UFvK8Ta-bI7Z5UjEz-mSpdFHTlnJq4kr4c2RWr8x-uEMPt5tgbSPl37P46HxxaLpq_lQLdGUgELkGvPZ-co-roGQaNF5NRRsyOtaug4oFRTUjXb0blzhptUKWIpdfDWBbK1o44EvrCaq3J6mP_-HTVygIQgOzORybRhHwO8fYApKWAPRtS04A6zMGj_FJr-2HvctKaoxpqi6O_Up-zvtnQZYJvEqhW9h1U3Yo5zI4op7K5piz2V5ELtybFla-bMmMUB5Hq7rKDdnORfGic6TVtLr2L_hv3BwEp-m8zrqUAfuzYRdT4IYeQebW9mwyrAGoSoA-QknT4fLLXxn0SzxzAKEC6stU4bDRbKW8sxqkDHVhBh3WpIGYOZC4b_QKCDI0Ri8MgB-ifPHDyiphAzohOxb2vpuU7GNq5F5vP3B1tMXsiIhMKOe6af5nptBqsH9-1WOGwgzc0VgnEnRaXOrRVENzhP1fybBWgZG9sitNq7AxQklZY4s59-BPKF9Jcd_7W35ylLvRpHoXArgd9dNPdDYMt_tfBgrl-ChJGBA_FyloUAnVj4A-RWh4D54bkFupyIsw873C1QBS25Aee0qsldPmq3rpXiSd1ecClmtsWs6vxquhSq63TDcl1mhxXEKSLjQngpnk2N81lDbfiVXc4ZhpFY2TtZn5myCePf2dNk88V_KZ0jZvdsTBFH3ztZ-bD17_dEAb5a2Ne1-6_7xE47EBMtdXOh4CKEv-p2NGOzk84YUqWwMOY9_e3imENRlnGWolyzJu_VhDMRKWMk1JbaDRigkjEYv3yUQ_dRPrNLUXCPDS3DUsjmc0HFhytTtvgWjj6E1-hqMXv5GkVLEu6noPY09drlR9xydd2Ka5xxDLzadulErKu5jZAzBQ43TvogKY31OHh6yXnlpvpkgpG6JGkzb1YcpnmUveXLSnbjxVmO21ID4hlB6y0B5ZtKnpFILhxTAz0_YKMdfv0V0K4vQS4zm_mNk_OzhAbZJiB7uhwCDj0H-T1sTCxH1lJNtxW8dwTMoii1PR_K0Mna9TdxVcE2XrGRxkqVfUonm7MSzH-DbyU-9pYbIafRtzKLlzrr-XCNVBTz31ZVmalFm2T8.qUaE2G1nzptgmAumyFOF8g`,
		`-----BEGIN CERTIFICATE-----
MIIFzjCCA7agAwIBAgIQDLgkTDLTHuGmiazKQo08BzANBgkqhkiG9w0BAQsFADCB
tTELMAkGA1UEBhMCVVMxEzARBgNVBAgTCkNhbGlmb3JuaWExFjAUBgNVBAcTDVNh
biBGcmFuY2lzY28xFDASBgNVBAoTC1RpZ2VyYSwgSW5jMSIwIAYDVQQLDBlTZWN1
cml0eSA8c2lydEB0aWdlcmEuaW8+MT8wPQYDVQQDEzZUaWdlcmEgRW50aXRsZW1l
bnRzIEludGVybWVkaWF0ZSBDZXJ0aWZpY2F0ZSBBdXRob3JpdHkwHhcNMTgwNDA1
MjMzNDA4WhcNMTgwNDA0MjMzNDA3WjCBpjELMAkGA1UEBhMCVVMxEzARBgNVBAgT
CkNhbGlmb3JuaWExFjAUBgNVBAcTDVNhbiBGcmFuY2lzY28xFDASBgNVBAoTC1Rp
Z2VyYSwgSW5jMSIwIAYDVQQLDBlTZWN1cml0eSA8c2lydEB0aWdlcmEuaW8+MTAw
LgYDVQQDEydUaWdlcmEgRW50aXRsZW1lbnRzIEV4cGlyZWQgQ2VydGlmaWNhdGUw
ggGiMA0GCSqGSIb3DQEBAQUAA4IBjwAwggGKAoIBgQC8Znw08LfOuISeYGseLAsr
Xzh/UU98qsnxZDnIrCMDtRxn1Xcu5KaHfNxAgNRYGXtgI/gT1lPdX01v3FUesGvi
nRugOnH/3JqpWkWf2rPxnxFxyEvOVty1LmZZF3rDxMYA11n9RLej+OCH22siA4dg
d4qTWncX1E62QR9c84WHImELo5m0809zPfGBrsDHRC6xcYZJP/gT/ddDkp4zSQwz
KTlVGXj4m6uewAfR+5HW35Xf5UALc/n6TwJSR3A4P5VCKUGT6WwWCLadjjBIYoAg
u3vQ1IFj6wKz4NPxev0hMOJ0MZB6KiX+KJ4UtEU2XyzGtvf5R49Zc9OLYJc9dNY2
RAUHSfduy2rXFUXTdMBKSr0amOtkO0gLwVeqfCGnZCkeVF+g5ruBy3oR790vSd/5
lwQgW4ZUDUY7VJQkC1pe2oPmvoyP3WdXMvlLz4uP7Ge4FfhjjJpBH5Lk0sxRziuo
Qch4PHKA0KhMQ1BtVM1K0QvXii5GTBoCeR65BVz+tF0CAwEAAaNnMGUwDgYDVR0P
AQH/BAQDAgWgMBMGA1UdJQQMMAoGCCsGAQUFBwMCMB0GA1UdDgQWBBQup4vPb8uI
ca01T6Bh3SepMQ1oZDAfBgNVHSMEGDAWgBTFkDmSJ/Ojg2eJB8Ypv7jCu5MgWjAN
BgkqhkiG9w0BAQsFAAOCAgEAhL15o5tSiu+4pQh0lVlMxFq9OW2HS8RHHH9cb/ns
Xke/F3POI7bZ4IxivAcaNBfYMKwlAAbYOOYHzwBbswyD53VZi6WTMBd4xQpeChrW
9BkAWShOt6tim3RH7K5LyajOwVWrE1yo26oj2pexG7nQqg0WTd9YmsZGPp1oPraQ
Hs18tBbjCCs/NkDlwfqvrCm8T6+MW1jLE/1q1bdBoZuICb+hKK8HjDxP7QPCX51F
4WHAMxVSCJe5m0o+cIo5Q4GY3tjAvNv1AKY1jxPkocbah+6I6dhqf3aRz+As1EHI
bd/1LFskx3K7FF2wHkTDS+FnxIPAwCH8CYRmypIQBEN7ItKsksXu6ZVpG44e/naI
9JeMRSg59SwEgS6/kKOLoF7zJcyLF56LN80QHFVaLKCWWm7vBKuMVxOxT9wuQP0l
0sglXHZ36qrk2UXebHPkvJCjY3j2dIP6Tv5bSfXVb43HjumsKB2hUu74xYbPZDuo
O70Yf/Rspkb4Fv2OHXMHtP+Y3WCIOzF2+e9sNB4WTFv/EJz18o0nvVvYEmtiWBQd
ATrr8ARvAp8p3d1JNXcgxPMJkOg1KWUgquiDGj5OVo4/XxAsDCdmKmC9+SARyl/b
QdtP3kQuhNNtAiZMMo+/HrsJfmrhr1o66a4RlhRhAoj4qewX56RKy2vQOdFaD+ZH
NSs=
-----END CERTIFICATE-----`)
}

func ApplyGracePeriodLicense(client client.Interface) {
	applyLicense(client, `eyJhbGciOiJBMTI4R0NNS1ciLCJjdHkiOiJKV1QiLCJlbmMiOiJBMTI4R0NNIiwiaXYiOiJ2WUtrRFpueEU5MHZ4b1M2IiwidGFnIjoiR0NuanlKR1VNdG5fZTZTT05iYmhkdyIsInR5cCI6IkpXVCJ9.eBFqEUk90daQJesyKCNkhQ.UZwcR4N-MSdXgD_X.EkTuq3PNyN9zxHbRfuVe0j5WFd0YUZORVoDWMPjHpi0au-HPRPutfBamy81AC0MytABwRcX5OcJG_SvPDzwYJliYYjZNQdfZQV_zLxrko1wqotVoUyo_cB7wgAxJfiszgeyeSdy5S8hVeUgPKFzS0-ndDCHU3f9RdN4htrpAed2XDbkh18LSqJ0iWq9wCUdkiWvRa9vlIxOlY-KRi4qO8ATZqjCuGxMauUmat3-7FPTYeVq7TqVxjbUEdgCWcu6NEKDC5-tnZmDAWB2JL9M4rg_cOdcmLyx3g04t5bAHqZCochKje_6PCzsu183Sch4dJXk2r505xJgL9gDW8fs7Jav311Bk7mWyV589eBDv2acJTuOe6jq8HKvbJPWx6cXqFAmP3YAUUeYDI739DyZn0Kcrp7I-jTl0bmAmjFn2gBIi2i9jTt_kOeJQigWxHyOGg6o72SqJsv8E59WoV8oPKCMIs-XfpECLxQ7CvZ87aYz3oUdHiGb-67-VL60TOr0rKa-3V8wBUhfwSwxRrTAy_kMyCJ97ZTXewv77kSUpejJnwzqyX99bsC_GKT4tEKWsez81sfLiELFBjch2Wp9ZzvVVKB3-Xv-nmyUnydPumB8u1HZI56rdB6LPS3DEJ9CAeXk3OHllkxpecfvsJGpa8qoCCpj4X1O1qEC9eS5t3V2TOVgRbmY92Xw-A2LJTeJVbuLFntrhNVeW3moAVfhr6lhk8SM_--vCSOFI8fyCTNWC_S512CBls5XRgzgPs5iipyyhUNAY9A3lWoDsocW3DLhxdHnyT5W0Bj3lXfwA5GoK7_oC1Wnd_g8svT-qt1epDuOIdpUtBPiKVXl3D5AzWTi_PjK-4yrppJ2FGJzW5fwoZriT8pZBAJLJLtMcU6BMVN5BuWNAOCwvxU-rX0hWSF3B_-0IgwG26oCVfaH8-dIdXV3u1x-Zgws1W05EgCfFs115mzADbOpCHP9w2a1FDwhaHBmUY7m_kpnaCe8QPpaTAr7zPZmPZxgZoUqZTk8iJ2XCKEwfHGFKFZLwaugBklDpYozupVG4bW1SgO4urvQ7aJA0BDmeOEYYk0z1oEmRcOWJwSDdSL4x-Oc6z4bgh6LHxR75VaJhnb7qdsTV.zZFiyibS-eWVUfEeeEsyuw`,
		`-----BEGIN CERTIFICATE-----
MIIFzjCCA7agAwIBAgIQDLgkTDLTHuGmiazKQo08BzANBgkqhkiG9w0BAQsFADCB
tTELMAkGA1UEBhMCVVMxEzARBgNVBAgTCkNhbGlmb3JuaWExFjAUBgNVBAcTDVNh
biBGcmFuY2lzY28xFDASBgNVBAoTC1RpZ2VyYSwgSW5jMSIwIAYDVQQLDBlTZWN1
cml0eSA8c2lydEB0aWdlcmEuaW8+MT8wPQYDVQQDEzZUaWdlcmEgRW50aXRsZW1l
bnRzIEludGVybWVkaWF0ZSBDZXJ0aWZpY2F0ZSBBdXRob3JpdHkwHhcNMTgwNDA1
MjMzNDA4WhcNMTgwNDA0MjMzNDA3WjCBpjELMAkGA1UEBhMCVVMxEzARBgNVBAgT
CkNhbGlmb3JuaWExFjAUBgNVBAcTDVNhbiBGcmFuY2lzY28xFDASBgNVBAoTC1Rp
Z2VyYSwgSW5jMSIwIAYDVQQLDBlTZWN1cml0eSA8c2lydEB0aWdlcmEuaW8+MTAw
LgYDVQQDEydUaWdlcmEgRW50aXRsZW1lbnRzIEV4cGlyZWQgQ2VydGlmaWNhdGUw
ggGiMA0GCSqGSIb3DQEBAQUAA4IBjwAwggGKAoIBgQC8Znw08LfOuISeYGseLAsr
Xzh/UU98qsnxZDnIrCMDtRxn1Xcu5KaHfNxAgNRYGXtgI/gT1lPdX01v3FUesGvi
nRugOnH/3JqpWkWf2rPxnxFxyEvOVty1LmZZF3rDxMYA11n9RLej+OCH22siA4dg
d4qTWncX1E62QR9c84WHImELo5m0809zPfGBrsDHRC6xcYZJP/gT/ddDkp4zSQwz
KTlVGXj4m6uewAfR+5HW35Xf5UALc/n6TwJSR3A4P5VCKUGT6WwWCLadjjBIYoAg
u3vQ1IFj6wKz4NPxev0hMOJ0MZB6KiX+KJ4UtEU2XyzGtvf5R49Zc9OLYJc9dNY2
RAUHSfduy2rXFUXTdMBKSr0amOtkO0gLwVeqfCGnZCkeVF+g5ruBy3oR790vSd/5
lwQgW4ZUDUY7VJQkC1pe2oPmvoyP3WdXMvlLz4uP7Ge4FfhjjJpBH5Lk0sxRziuo
Qch4PHKA0KhMQ1BtVM1K0QvXii5GTBoCeR65BVz+tF0CAwEAAaNnMGUwDgYDVR0P
AQH/BAQDAgWgMBMGA1UdJQQMMAoGCCsGAQUFBwMCMB0GA1UdDgQWBBQup4vPb8uI
ca01T6Bh3SepMQ1oZDAfBgNVHSMEGDAWgBTFkDmSJ/Ojg2eJB8Ypv7jCu5MgWjAN
BgkqhkiG9w0BAQsFAAOCAgEAhL15o5tSiu+4pQh0lVlMxFq9OW2HS8RHHH9cb/ns
Xke/F3POI7bZ4IxivAcaNBfYMKwlAAbYOOYHzwBbswyD53VZi6WTMBd4xQpeChrW
9BkAWShOt6tim3RH7K5LyajOwVWrE1yo26oj2pexG7nQqg0WTd9YmsZGPp1oPraQ
Hs18tBbjCCs/NkDlwfqvrCm8T6+MW1jLE/1q1bdBoZuICb+hKK8HjDxP7QPCX51F
4WHAMxVSCJe5m0o+cIo5Q4GY3tjAvNv1AKY1jxPkocbah+6I6dhqf3aRz+As1EHI
bd/1LFskx3K7FF2wHkTDS+FnxIPAwCH8CYRmypIQBEN7ItKsksXu6ZVpG44e/naI
9JeMRSg59SwEgS6/kKOLoF7zJcyLF56LN80QHFVaLKCWWm7vBKuMVxOxT9wuQP0l
0sglXHZ36qrk2UXebHPkvJCjY3j2dIP6Tv5bSfXVb43HjumsKB2hUu74xYbPZDuo
O70Yf/Rspkb4Fv2OHXMHtP+Y3WCIOzF2+e9sNB4WTFv/EJz18o0nvVvYEmtiWBQd
ATrr8ARvAp8p3d1JNXcgxPMJkOg1KWUgquiDGj5OVo4/XxAsDCdmKmC9+SARyl/b
QdtP3kQuhNNtAiZMMo+/HrsJfmrhr1o66a4RlhRhAoj4qewX56RKy2vQOdFaD+ZH
NSs=
-----END CERTIFICATE-----`)
}
