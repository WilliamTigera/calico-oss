//go:build fvtests
// +build fvtests

// Copyright (c) 2019-2022 Tigera, Inc. All rights reserved.

package fv_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"

	api "github.com/tigera/api/pkg/apis/projectcalico/v3"
	"github.com/tigera/api/pkg/lib/numorstring"

	"github.com/projectcalico/calico/felix/bpf/conntrack"
	"github.com/projectcalico/calico/felix/fv/connectivity"
	"github.com/projectcalico/calico/felix/fv/containers"
	"github.com/projectcalico/calico/felix/fv/infrastructure"
	"github.com/projectcalico/calico/felix/fv/utils"
	"github.com/projectcalico/calico/felix/fv/workload"
	"github.com/projectcalico/calico/felix/timeshim"
	client "github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/calico/libcalico-go/lib/options"
	"github.com/projectcalico/calico/libcalico-go/lib/set"
)

var bpfEnabled = (os.Getenv("FELIX_FV_ENABLE_BPF") == "true")

var dnsDir string

type mapping struct {
	lhs, rhs string
}

func mappingMatchesLine(m *mapping, line string) bool {
	return strings.Contains(line, "\""+m.lhs+"\"") && strings.Contains(line, "\""+m.rhs+"\"")
}

func fileHasMappingsAndNot(mappings []mapping, notMappings []mapping) func() error {
	mset := set.FromArray(mappings)
	notset := set.FromArray(notMappings)
	return func() error {
		f, err := os.Open(path.Join(dnsDir, "dnsinfo.txt"))
		if err == nil {
			var problems []string
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := scanner.Text()
				mset.Iter(func(item interface{}) error {
					m := item.(mapping)
					if mappingMatchesLine(&m, line) {
						return set.RemoveItem
					}
					return nil
				})
				notset.Iter(func(item interface{}) error {
					m := item.(mapping)
					if mappingMatchesLine(&m, line) {
						log.Infof("Found wrong mapping: %v", m)
						problems = append(problems, fmt.Sprintf("Found wrong mapping: %v", m))
					}
					return nil
				})
			}
			if mset.Len() == 0 {
				log.Info("All expected mappings found")
			} else {
				log.Infof("Missing %v expected mappings", mset.Len())
				mset.Iter(func(item interface{}) error {
					m := item.(mapping)
					log.Infof("Missed mapping: %v", m)
					problems = append(problems, fmt.Sprintf("Missed mapping: %v", m))
					return nil
				})
			}
			if len(problems) > 0 {
				return errors.New(strings.Join(problems, "\n"))
			}
		}
		return err
	}
}

func fileHasMappings(mappings []mapping) func() error {
	return fileHasMappingsAndNot(mappings, nil)
}

func fileHasMapping(lname, rname string) func() error {
	return fileHasMappings([]mapping{{lhs: lname, rhs: rname}})
}

func makeBPFConntrackEntry(ifIndex int, aIP, bIP net.IP, trusted bool) (conntrack.Key, conntrack.Value) {
	a2bLeg := conntrack.Leg{Opener: true, Ifindex: uint32(ifIndex), Whitelisted: true}
	b2aLeg := conntrack.Leg{Opener: false, Whitelisted: true}

	// BPF conntrack map convention is for the first IP to be the smaller one.  Bizarrely, the
	// "smaller" comparison here is with little endian byte ordering.
	aBytes := []byte(aIP.To4())
	bBytes := []byte(bIP.To4())
	if binary.LittleEndian.Uint32(aBytes) > binary.LittleEndian.Uint32(bBytes) {
		aIP, bIP = bIP, aIP
		a2bLeg, b2aLeg = b2aLeg, a2bLeg
	}

	now := time.Duration(timeshim.RealTime().KTimeNanos())

	// In the BPF dataplane, the decision whether a DNS connection is trusted - i.e. comparison
	// of the destination IP/port against DNSTrustedServers - is made at the time of seeing the
	// DNS request, with the result being stored as a flag (16) in the conntrack entry for the
	// connection.  For the FV tests in this file, we don't actually send any DNS request, but
	// instead simulate the conntrack state that the request would create.  That means creating
	// a conntrack with the 16 flag, if the DNS server is trusted, and without that flag if the
	// DNS server is not trusted.
	flags := uint8(0)
	if trusted {
		flags = conntrack.FlagTrustDNS
	}

	return conntrack.NewKey(17 /* UDP */, aIP, 53, bIP, 53), conntrack.NewValueNormal(now, now, flags, a2bLeg, b2aLeg)
}

var _ = Describe("_BPF-SAFE_ DNS Policy", func() {
	var (
		scapyTrusted *containers.Container
		pingTarget   *containers.Container
		etcd         *containers.Container
		felix        *infrastructure.Felix
		client       client.Interface
		infra        infrastructure.DatastoreInfra
		w            [1]*workload.Workload
	)

	dnsServerSetup := func(scapy *containers.Container, trusted bool) {
		if !bpfEnabled {
			// Establish conntrack state, in Felix, as though the workload just sent a DNS
			// request to the specified scapy.
			felix.Exec("conntrack", "-I", "-s", w[0].IP, "-d", scapy.IP, "-p", "UDP", "-t", "10", "--sport", "53", "--dport", "53")
		} else {
			// Same thing with calico-bpf.
			key, val := makeBPFConntrackEntry(w[0].InterfaceIndex(), net.ParseIP(w[0].IP), net.ParseIP(scapy.IP), trusted)
			felix.Exec("calico-bpf", "conntrack", "write",
				base64.StdEncoding.EncodeToString(key[:]),
				base64.StdEncoding.EncodeToString(val[:]))
		}

		// Wait a second here to allow time for the conntrack state to be established.
		time.Sleep(time.Second)

		// Allow scapy to route back to the workload.
		io.WriteString(scapy.Stdin,
			fmt.Sprintf("conf.route.add(host='%v',gw='%v')\n", w[0].IP, felix.IP))
	}

	sendDNSResponses := func(scapy *containers.Container, dnsSpecs []string) {
		// Drive scapy.
		for _, dnsSpec := range dnsSpecs {
			io.WriteString(scapy.Stdin,
				fmt.Sprintf("send(IP(dst='%v')/UDP(sport=53)/%v)\n", w[0].IP, dnsSpec))
		}
	}

	workloadCanPingTarget := func() error {
		out, err := w[0].ExecOutput("ping", "-c", "1", "-W", "1", pingTarget.IP)
		log.WithError(err).Infof("ping said:\n%v", out)
		if err != nil {
			log.Infof("stderr was:\n%v", string(err.(*exec.ExitError).Stderr))
		}
		return err
	}

	for _, m := range []api.DNSPolicyMode{
		api.DNSPolicyModeNoDelay,
		api.DNSPolicyModeDelayDNSResponse,
		api.DNSPolicyModeDelayDeniedPacket,
	} {
		mode := m
		Describe("DNSPolicyMode is "+string(mode), func() {
			BeforeEach(func() {
				opts := infrastructure.DefaultTopologyOptions()
				var err error
				dnsDir, err = ioutil.TempDir("", "dnsinfo")
				Expect(err).NotTo(HaveOccurred())

				// Start scapy first, so we can get its IP and configure Felix to trust it.
				scapyTrusted = containers.Run("scapy",
					containers.RunOpts{AutoRemove: true, WithStdinPipe: true},
					"-i", "--privileged", "tigera-test/scapy")

				// Run another instance of scapy as our ping target for the tests.
				pingTarget = containers.Run("scapy",
					containers.RunOpts{AutoRemove: true, WithStdinPipe: true},
					"-i", "--privileged", "tigera-test/scapy")

				// Now start etcd and Felix, with Felix trusting scapy's IP.
				opts.ExtraVolumes[dnsDir] = "/dnsinfo"
				opts.ExtraEnvVars["FELIX_DNSPOLICYMODE"] = string(mode)
				opts.ExtraEnvVars["FELIX_DNSCACHEFILE"] = "/dnsinfo/dnsinfo.txt"
				opts.ExtraEnvVars["FELIX_DNSCACHESAVEINTERVAL"] = "1"
				opts.ExtraEnvVars["FELIX_DNSTRUSTEDSERVERS"] = scapyTrusted.IP
				opts.ExtraEnvVars["FELIX_PolicySyncPathPrefix"] = "/var/run/calico/policysync"
				felix, etcd, client, infra = infrastructure.StartSingleNodeEtcdTopology(opts)
				infrastructure.CreateDefaultProfile(client, "default", map[string]string{"default": ""}, "")

				// Create a workload, using that profile.
				for ii := range w {
					iiStr := strconv.Itoa(ii)
					w[ii] = workload.Run(felix, "w"+iiStr, "default", "10.65.0.1"+iiStr, "8055", "tcp")
					w[ii].Configure(client)
				}
			})

			// Stop etcd and workloads, collecting some state if anything failed.
			AfterEach(func() {
				if CurrentGinkgoTestDescription().Failed {
					if bpfEnabled {
						felix.Exec("calico-bpf", "ipsets", "dump")
					}
					felix.Exec("ipset", "list")
					felix.Exec("iptables-save", "-c")
					felix.Exec("ip", "r")
					felix.Exec("conntrack", "-L")
				}

				for ii := range w {
					w[ii].Stop()
				}
				felix.Stop()

				if CurrentGinkgoTestDescription().Failed {
					etcd.Exec("etcdctl", "get", "/", "--prefix", "--keys-only")
				}
				etcd.Stop()
				infra.Stop()
			})

			DescribeTable("DNS response processing",
				func(dnsSpecs []string, check func() error) {
					dnsServerSetup(scapyTrusted, true)
					sendDNSResponses(scapyTrusted, dnsSpecs)
					scapyTrusted.Stdin.Close()
					Eventually(check, "10s", "2s").Should(Succeed())
				},

				Entry("A record", []string{
					"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='bankofsteve.com',qtype='A'),an=(DNSRR(rrname='bankofsteve.com',type='A',ttl=36000,rdata='192.168.56.1')))",
				},
					fileHasMapping("bankofsteve.com", "192.168.56.1"),
				),
				Entry("AAAA record", []string{
					"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='bankofsteve.com',qtype='AAAA'),an=(DNSRR(rrname='bankofsteve.com',type='AAAA',ttl=36000,rdata='fdf5:8944::3')))",
				},
					fileHasMapping("bankofsteve.com", "fdf5:8944::3"),
				),
				Entry("CNAME record", []string{
					"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='bankofsteve.com',qtype='CNAME'),an=(DNSRR(rrname='bankofsteve.com',type='CNAME',ttl=36000,rdata='my.home.server')))",
				},
					fileHasMapping("bankofsteve.com", "my.home.server"),
				),
				Entry("3 A records", []string{
					"DNS(qr=1,qdcount=1,ancount=3,qd=DNSQR(qname='microsoft.com',qtype='A'),an=(" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='19.16.5.102')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=36,rdata='10.146.25.132')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=48,rdata='35.5.5.199')" +
						"))",
				},
					fileHasMappings([]mapping{
						{lhs: "microsoft.com", rhs: "19.16.5.102"},
						{lhs: "microsoft.com", rhs: "10.146.25.132"},
						{lhs: "microsoft.com", rhs: "35.5.5.199"},
					}),
				),
				Entry("as many A records as can fit in 512 bytes", []string{
					// 19 answers => 590 bytes of UDP payload
					// 17 answers => 532 bytes of UDP payload
					// 16 answers => 503 bytes of UDP payload
					"DNS(qr=1,qdcount=1,ancount=16,qd=DNSQR(qname='microsoft.com',qtype='A'),an=(" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.1')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.2')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.3')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.4')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.5')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.6')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.7')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.8')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.9')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.10')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.11')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.12')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.13')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.14')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.15')/" +
						"DNSRR(rrname='microsoft.com',type='A',ttl=24,rdata='10.10.10.16')" +
						"))",
				},
					fileHasMappings([]mapping{
						{lhs: "microsoft.com", rhs: "10.10.10.1"},
						{lhs: "microsoft.com", rhs: "10.10.10.2"},
						{lhs: "microsoft.com", rhs: "10.10.10.3"},
						{lhs: "microsoft.com", rhs: "10.10.10.4"},
						{lhs: "microsoft.com", rhs: "10.10.10.5"},
						{lhs: "microsoft.com", rhs: "10.10.10.6"},
						{lhs: "microsoft.com", rhs: "10.10.10.7"},
						{lhs: "microsoft.com", rhs: "10.10.10.8"},
						{lhs: "microsoft.com", rhs: "10.10.10.9"},
						{lhs: "microsoft.com", rhs: "10.10.10.10"},
						{lhs: "microsoft.com", rhs: "10.10.10.11"},
						{lhs: "microsoft.com", rhs: "10.10.10.12"},
						{lhs: "microsoft.com", rhs: "10.10.10.13"},
						{lhs: "microsoft.com", rhs: "10.10.10.14"},
						{lhs: "microsoft.com", rhs: "10.10.10.15"},
						{lhs: "microsoft.com", rhs: "10.10.10.16"},
					}),
				),
			)

			DescribeTable("Benign DNS responses",
				// Various responses that we don't expect Felix to extract any information from, but
				// that should not cause any problem.
				func(dnsSpec string) {
					dnsServerSetup(scapyTrusted, true)
					sendDNSResponses(scapyTrusted, []string{
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='bankofsteve.com',qtype='A'),an=(DNSRR(rrname='bankofsteve.com',type='A',ttl=36000,rdata='192.168.56.1')))",
						dnsSpec,
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='fidget.com',qtype='A'),an=(DNSRR(rrname='fidget.com',type='A',ttl=36000,rdata='2.3.4.5')))",
					})
					scapyTrusted.Stdin.Close()
					Eventually(fileHasMappings([]mapping{
						{lhs: "bankofsteve.com", rhs: "192.168.56.1"},
						{lhs: "fidget.com", rhs: "2.3.4.5"},
					}), "10s", "2s").Should(Succeed())
				},
				Entry("MX",
					"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='bankofsteve.com',qtype='MX'),an=(DNSRR(rrname='bankofsteve.com',type='MX',ttl=36000,rdata='mail.bankofsteve.com')))",
				),
				Entry("TXT",
					"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='bankofsteve.com',qtype='TXT'),an=(DNSRR(rrname='bankofsteve.com',type='TXT',ttl=36000,rdata='v=spf1 ~all')))",
				),
				Entry("SRV",
					"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='_sip._tcp.bankofsteve.com',qtype='SRV'),an=(DNSRR(rrname='_sip._tcp.bankofsteve.com',type='SRV',ttl=36000,rdata='sipserver.bankofsteve.com')))",
				),
				Entry("PTR",
					"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='20',qtype='PTR'),an=(DNSRR(rrname='20',type='PTR',ttl=36000,rdata='sipserver.bankofsteve.com')))",
				),
				Entry("SOA",
					"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='dnsimple.com',qtype='SOA'),an=(DNSRR(rrname='dnsimple.com',type='SOA',ttl=36000,rdata='ns1.dnsimple.com admin.dnsimple.com 2013022001 86400 7200 604800 300')))",
				),
				Entry("ALIAS",
					"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='bankofsteve.com',qtype='ALIAS'),an=(DNSRR(rrname='bankofsteve.com',type='ALIAS',ttl=36000,rdata='example.server')))",
				),
				Entry("Class CH",
					"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='microsoft.com',qclass='CH',qtype='A'),an=(DNSRR(rrname='bankofsteve.com',rclass='CH',type='A',ttl=36000,rdata='10.10.10.10')))",
				),
				Entry("Class HS",
					"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='microsoft.com',qclass='HS',qtype='A'),an=(DNSRR(rrname='bankofsteve.com',rclass='HS',type='A',ttl=36000,rdata='10.10.10.10')))",
				),
				Entry("NXDOMAIN",
					"DNS(qr=1,qdcount=1,rcode=3,qd=DNSQR(qname='microsoft.com',qtype='A'))",
				),
				Entry("response that claims to have 3 answers but doesn't",
					"DNS(qr=1,qdcount=1,ancount=3,qd=DNSQR(qname='microsoft.com',qtype='A'))",
				),
			)

			Context("with an untrusted DNS server", func() {
				var scapyUntrusted *containers.Container

				BeforeEach(func() {
					// Start another scapy.  This one's IP won't be trusted by Felix.
					scapyUntrusted = containers.Run("scapy",
						containers.RunOpts{AutoRemove: true, WithStdinPipe: true},
						"-i", "--privileged", "tigera-test/scapy")
				})

				It("s DNS information should be ignored", func() {
					dnsServerSetup(scapyTrusted, true)
					dnsServerSetup(scapyUntrusted, false)
					sendDNSResponses(scapyTrusted, []string{
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='alice.com',qtype='A'),an=(DNSRR(rrname='alice.com',type='A',ttl=36000,rdata='10.10.10.1')))",
					})
					sendDNSResponses(scapyUntrusted, []string{
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='alice.com',qtype='A'),an=(DNSRR(rrname='alice.com',type='A',ttl=36000,rdata='10.10.10.2')))",
					})
					sendDNSResponses(scapyTrusted, []string{
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='alice.com',qtype='A'),an=(DNSRR(rrname='alice.com',type='A',ttl=36000,rdata='10.10.10.3')))",
					})
					sendDNSResponses(scapyUntrusted, []string{
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='alice.com',qtype='A'),an=(DNSRR(rrname='alice.com',type='A',ttl=36000,rdata='10.10.10.4')))",
					})
					sendDNSResponses(scapyTrusted, []string{
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='alice.com',qtype='A'),an=(DNSRR(rrname='alice.com',type='A',ttl=36000,rdata='10.10.10.5')))",
					})
					sendDNSResponses(scapyUntrusted, []string{
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='alice.com',qtype='A'),an=(DNSRR(rrname='alice.com',type='A',ttl=36000,rdata='10.10.10.6')))",
					})
					sendDNSResponses(scapyTrusted, []string{
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='alice.com',qtype='A'),an=(DNSRR(rrname='alice.com',type='A',ttl=36000,rdata='10.10.10.7')))",
					})
					scapyUntrusted.Stdin.Close()
					scapyTrusted.Stdin.Close()
					Eventually(fileHasMappingsAndNot([]mapping{
						{lhs: "alice.com", rhs: "10.10.10.1"},
						{lhs: "alice.com", rhs: "10.10.10.3"},
						{lhs: "alice.com", rhs: "10.10.10.5"},
						{lhs: "alice.com", rhs: "10.10.10.7"},
					}, []mapping{
						{lhs: "alice.com", rhs: "10.10.10.2"},
						{lhs: "alice.com", rhs: "10.10.10.4"},
						{lhs: "alice.com", rhs: "10.10.10.6"},
					}), "10s", "2s").Should(Succeed())
				})
			})

			Context("with policy in place first, then connection attempted", func() {
				BeforeEach(func() {
					policy := api.NewGlobalNetworkPolicy()
					policy.Name = "default-deny-egress"
					policy.Spec.Selector = "all()"
					udp := numorstring.ProtocolFromString(numorstring.ProtocolUDP)
					policy.Spec.Egress = []api.Rule{
						{
							Action:   api.Allow,
							Protocol: &udp,
							Destination: api.EntityRule{
								Ports: []numorstring.Port{numorstring.SinglePort(53)},
							},
						},
						{
							Action: api.Deny,
						},
					}
					_, err := client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
					Expect(err).NotTo(HaveOccurred())

					policy = api.NewGlobalNetworkPolicy()
					policy.Name = "allow-xyz"
					order := float64(20)
					policy.Spec.Order = &order
					policy.Spec.Selector = "all()"
					policy.Spec.Egress = []api.Rule{
						{
							Action:      api.Allow,
							Destination: api.EntityRule{Domains: []string{"xyz.com"}},
						},
					}
					_, err = client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
					Expect(err).NotTo(HaveOccurred())

					// Allow 2s for Felix to see and process that policy.
					time.Sleep(2 * time.Second)

					// We use the ping target container as a target IP for the workload to ping, so
					// arrange for it to route back to the workload.
					pingTarget.Exec("ip", "r", "add", w[0].IP, "via", felix.IP)

					// Create a chain of DNS info that maps xyz.com to that IP.
					dnsServerSetup(scapyTrusted, true)
					sendDNSResponses(scapyTrusted, []string{
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='xyz.com',qtype='CNAME'),an=(DNSRR(rrname='xyz.com',type='CNAME',ttl=60,rdata='bob.xyz.com')))",
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='bob.xyz.com',qtype='CNAME'),an=(DNSRR(rrname='bob.xyz.com',type='CNAME',ttl=10,rdata='server-5.xyz.com')))",
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='server-5.xyz.com',qtype='A'),an=(DNSRR(rrname='server-5.xyz.com',type='A',ttl=60,rdata='" + pingTarget.IP + "')))",
					})
					scapyTrusted.Stdin.Close()
				})

				It("workload can ping etcd", func() {
					// Allow 4 seconds for Felix to see the DNS responses and update ipsets.
					time.Sleep(4 * time.Second)

					// Ping should now go through.
					Expect(workloadCanPingTarget()).NotTo(HaveOccurred())
				})
			})

			Context("with host endpoint and ApplyOnForward policy", func() {
				if os.Getenv("FELIX_FV_ENABLE_BPF") == "true" {
					// Skip because BPF mode does not yet support HostEndpoints.
					return
				}

				BeforeEach(func() {
					hep := api.NewHostEndpoint()
					hep.Name = "felix-eth0"
					hep.Labels = map[string]string{"host-endpoint": "yes"}
					hep.Spec.Node = felix.Hostname
					hep.Spec.InterfaceName = "eth0"
					_, err := client.HostEndpoints().Create(utils.Ctx, hep, utils.NoOptions)
					Expect(err).NotTo(HaveOccurred())

					policy := api.NewGlobalNetworkPolicy()
					policy.Name = "allow-xyz-only"
					policy.Spec.Selector = "host-endpoint == 'yes'"
					policy.Spec.Egress = []api.Rule{
						{
							Action:      api.Allow,
							Destination: api.EntityRule{Domains: []string{"xyz.com"}},
						},
						{
							Action: api.Deny,
						},
					}
					policy.Spec.ApplyOnForward = true
					_, err = client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
					Expect(err).NotTo(HaveOccurred())

					// Allow 2s for Felix to see and process that policy.
					time.Sleep(2 * time.Second)

					// We use the ping target container as a target IP for the workload to ping, so
					// arrange for it to route back to the workload.
					pingTarget.Exec("ip", "r", "add", w[0].IP, "via", felix.IP)

					// Create a chain of DNS info that maps xyz.com to that IP.
					dnsServerSetup(scapyTrusted, true)
					sendDNSResponses(scapyTrusted, []string{
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='xyz.com',qtype='CNAME'),an=(DNSRR(rrname='xyz.com',type='CNAME',ttl=60,rdata='bob.xyz.com')))",
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='bob.xyz.com',qtype='CNAME'),an=(DNSRR(rrname='bob.xyz.com',type='CNAME',ttl=10,rdata='server-5.xyz.com')))",
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='server-5.xyz.com',qtype='A'),an=(DNSRR(rrname='server-5.xyz.com',type='A',ttl=60,rdata='" + pingTarget.IP + "')))",
					})
					scapyTrusted.Stdin.Close()
				})

				It("workload can ping etcd", func() {
					// Allow 4 seconds for Felix to see the DNS responses and update ipsets.
					time.Sleep(4 * time.Second)
					// Ping should now go through.
					Expect(workloadCanPingTarget()).NotTo(HaveOccurred())
				})
			})

			Context("with a chain of DNS info for xyz.com", func() {
				BeforeEach(func() {
					// We use the ping target container as a target IP for the workload to ping, so
					// arrange for it to route back to the workload.
					pingTarget.Exec("ip", "r", "add", w[0].IP, "via", felix.IP)

					// Create a chain of DNS info that maps xyz.com to that IP.
					dnsServerSetup(scapyTrusted, true)
					sendDNSResponses(scapyTrusted, []string{
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='xyz.com',qtype='CNAME'),an=(DNSRR(rrname='xyz.com',type='CNAME',ttl=60,rdata='bob.xyz.com')))",
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='bob.xyz.com',qtype='CNAME'),an=(DNSRR(rrname='bob.xyz.com',type='CNAME',ttl=10,rdata='server-5.xyz.com')))",
						"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='server-5.xyz.com',qtype='A'),an=(DNSRR(rrname='server-5.xyz.com',type='A',ttl=60,rdata='" + pingTarget.IP + "')))",
					})
					scapyTrusted.Stdin.Close()
				})

				It("workload can ping etcd, because there's no policy", func() {
					Expect(workloadCanPingTarget()).NotTo(HaveOccurred())
				})

				Context("with default-deny egress policy", func() {
					BeforeEach(func() {
						policy := api.NewGlobalNetworkPolicy()
						policy.Name = "default-deny-egress"
						policy.Spec.Selector = "all()"
						policy.Spec.Egress = []api.Rule{{
							Action: api.Deny,
						}}
						_, err := client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
						Expect(err).NotTo(HaveOccurred())
					})

					It("workload cannot ping etcd", func() {
						Eventually(workloadCanPingTarget, "10s", "2s").Should(HaveOccurred())
					})

					Context("with domain-allow egress policy", func() {
						BeforeEach(func() {
							policy := api.NewGlobalNetworkPolicy()
							policy.Name = "allow-xyz"
							order := float64(20)
							policy.Spec.Order = &order
							policy.Spec.Selector = "all()"
							policy.Spec.Egress = []api.Rule{
								{
									Action:      api.Allow,
									Destination: api.EntityRule{Domains: []string{"xyz.com"}},
								},
							}
							_, err := client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
							Expect(err).NotTo(HaveOccurred())
						})

						It("workload can ping etcd", func() {
							Eventually(workloadCanPingTarget, "5s", "1s").ShouldNot(HaveOccurred())
						})

						Context("with 11s sleep so that DNS info expires", func() {
							BeforeEach(func() {
								time.Sleep(11 * time.Second)
							})

							It("workload cannot ping etcd", func() {
								Eventually(workloadCanPingTarget, "5s", "1s").Should(HaveOccurred())
							})
						})

						Context("with a Felix restart", func() {
							BeforeEach(func() {
								felix.Restart()
								// Allow a bit of time for Felix to re-read the
								// persistent file and update the dataplane, but not
								// long enough (8s) for the DNS info to expire.
								time.Sleep(3 * time.Second)
							})

							It("workload can still ping etcd", func() {
								Eventually(workloadCanPingTarget, "5s", "1s").ShouldNot(HaveOccurred())
							})
						})
					})

					Context("with networkset with allowed egress domains", func() {
						BeforeEach(func() {
							gns := api.NewGlobalNetworkSet()
							gns.Name = "allow-xyz"
							gns.Labels = map[string]string{"thingy": "xyz"}
							gns.Spec.AllowedEgressDomains = []string{"xyz.com"}
							_, err := client.GlobalNetworkSets().Create(utils.Ctx, gns, utils.NoOptions)
							Expect(err).NotTo(HaveOccurred())

							policy := api.NewGlobalNetworkPolicy()
							policy.Name = "allow-xyz"
							order := float64(20)
							policy.Spec.Order = &order
							policy.Spec.Selector = "all()"
							policy.Spec.Egress = []api.Rule{
								{
									Action:      api.Allow,
									Destination: api.EntityRule{Selector: "thingy == 'xyz'"},
								},
							}
							_, err = client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
							Expect(err).NotTo(HaveOccurred())
						})

						Context("with a Felix restart", func() {
							BeforeEach(func() {
								felix.Restart()
								// Allow a bit of time for Felix to re-read the
								// persistent file and update the dataplane, but not
								// long enough (8s) for the DNS info to expire.
								time.Sleep(3 * time.Second)
							})

							It("workload can still ping etcd", func() {
								Eventually(workloadCanPingTarget, "5s", "1s").ShouldNot(HaveOccurred())
							})

							Context("with 10s sleep so that DNS info expires", func() {
								BeforeEach(func() {
									time.Sleep(10 * time.Second)
								})

								It("workload cannot ping etcd", func() {
									Eventually(workloadCanPingTarget, "5s", "1s").Should(HaveOccurred())
								})
							})
						})
					})
				})
			})
		})
	}
})

var _ = Describe("_BPF-SAFE_ DNS Policy with server on host", func() {
	var (
		scapyTrusted *containers.Container
		etcd         *containers.Container
		felix        *infrastructure.Felix
		client       client.Interface
		infra        infrastructure.DatastoreInfra
		w            [1]*workload.Workload
	)

	BeforeEach(func() {
		opts := infrastructure.DefaultTopologyOptions()
		var err error
		dnsDir, err = ioutil.TempDir("", "dnsinfo")
		Expect(err).NotTo(HaveOccurred())

		// Start etcd and Felix, with no trusted DNS server IPs yet.
		opts.ExtraVolumes[dnsDir] = "/dnsinfo"
		opts.ExtraEnvVars["FELIX_DNSCACHEFILE"] = "/dnsinfo/dnsinfo.txt"
		opts.ExtraEnvVars["FELIX_DNSCACHESAVEINTERVAL"] = "1"
		opts.ExtraEnvVars["FELIX_PolicySyncPathPrefix"] = "/var/run/calico/policysync"
		felix, etcd, client, infra = infrastructure.StartSingleNodeEtcdTopology(opts)
		infrastructure.CreateDefaultProfile(client, "default", map[string]string{"default": ""}, "")

		// Create a workload, using that profile.
		for ii := range w {
			iiStr := strconv.Itoa(ii)
			w[ii] = workload.Run(felix, "w"+iiStr, "default", "10.65.0.1"+iiStr, "8055", "tcp")
			w[ii].Configure(client)
		}

		// Start scapy, in the same namespace as Felix.
		scapyTrusted = containers.Run("scapy",
			containers.RunOpts{AutoRemove: true, WithStdinPipe: true, SameNamespace: felix.Container},
			"-i", "--privileged", "tigera-test/scapy")

		// Configure Felix to trust its own IP as a DNS server.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c := api.NewFelixConfiguration()
		c.Name = "default"
		c.Spec.DNSTrustedServers = &[]string{felix.IP}
		_, err = client.FelixConfigurations().Create(ctx, c, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Allow time for Felix to restart before we send the DNS response from scapy.
		time.Sleep(3 * time.Second)
	})

	// Stop etcd and workloads, collecting some state if anything failed.
	AfterEach(func() {
		if CurrentGinkgoTestDescription().Failed {
			if bpfEnabled {
				felix.Exec("calico-bpf", "ipsets", "dump")
			}
			felix.Exec("ipset", "list")
			felix.Exec("iptables-save", "-c")
			felix.Exec("ip", "r")
			felix.Exec("conntrack", "-L")
		}

		for ii := range w {
			w[ii].Stop()
		}
		felix.Stop()

		if CurrentGinkgoTestDescription().Failed {
			etcd.Exec("etcdctl", "get", "/", "--prefix", "--keys-only")
		}
		etcd.Stop()
		infra.Stop()
	})

	dnsServerSetup := func(scapy *containers.Container) {
		if !bpfEnabled {
			// Establish conntrack state, in Felix, as though the workload just sent a DNS
			// request to the specified scapy.  Note that for this group of tests, scapy shares
			// Felix's namespace and so has the same IP as Felix.
			felix.Exec("conntrack", "-I", "-s", w[0].IP, "-d", felix.IP, "-p", "UDP", "-t", "10", "--sport", "53", "--dport", "53")
		} else {
			// Same thing with calico-bpf.
			key, val := makeBPFConntrackEntry(w[0].InterfaceIndex(), net.ParseIP(w[0].IP), net.ParseIP(felix.IP), true)
			felix.Exec("calico-bpf", "conntrack", "write",
				base64.StdEncoding.EncodeToString(key[:]),
				base64.StdEncoding.EncodeToString(val[:]))
		}

		// Wait a second here to allow time for the conntrack state to be established.
		time.Sleep(time.Second)
	}

	sendDNSResponses := func(scapy *containers.Container, dnsSpecs []string) {
		// Drive scapy.
		for _, dnsSpec := range dnsSpecs {
			// Because we're sending from scapy in the same network namespace as Felix,
			// we need to use normal Linux sending instead of scapy's send function, as
			// the latter bypasses iptables.  We just use scapy to build the DNS
			// payload.
			io.WriteString(scapy.Stdin,
				fmt.Sprintf("dns = %v\n", dnsSpec))
			io.WriteString(scapy.Stdin, "import socket\n")
			io.WriteString(scapy.Stdin, "sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)\n")
			io.WriteString(scapy.Stdin,
				fmt.Sprintf("sock.bind(('%v', 53))\n", felix.IP))
			io.WriteString(scapy.Stdin,
				fmt.Sprintf("sock.sendto(dns.__bytes__(), ('%v', 53))\n", w[0].IP))
		}
	}

	DescribeTable("DNS response processing",
		func(dnsSpecs []string, check func() error) {
			dnsServerSetup(scapyTrusted)
			sendDNSResponses(scapyTrusted, dnsSpecs)
			scapyTrusted.Stdin.Close()
			Eventually(check, "10s", "2s").Should(Succeed())
		},

		Entry("A record", []string{
			"DNS(qr=1,qdcount=1,ancount=1,qd=DNSQR(qname='bankofsteve.com',qtype='A'),an=(DNSRR(rrname='bankofsteve.com',type='A',ttl=36000,rdata='192.168.56.1')))",
		},
			fileHasMapping("bankofsteve.com", "192.168.56.1"),
		),
	)
})

var _ = Describe("_BPF-SAFE_ Precise DNS logging", func() {
	var (
		etcd    *containers.Container
		felixes []*infrastructure.Felix
		felix   *infrastructure.Felix
		server  *infrastructure.Felix
		client  client.Interface
		infra   infrastructure.DatastoreInfra
		w       [2]*workload.Workload
		cc      *connectivity.Checker
	)

	BeforeEach(func() {
		opts := infrastructure.DefaultTopologyOptions()
		var err error
		dnsDir, err = ioutil.TempDir("", "dnsinfo")
		Expect(err).NotTo(HaveOccurred())

		// Start etcd and Felix, with no trusted DNS server IPs yet.
		opts.ExtraVolumes[dnsDir] = "/dnsinfo"
		opts.ExtraVolumes["/var/run/netns"] = "/var/run/netns"
		opts.ExtraEnvVars["FELIX_DNSCACHEFILE"] = "/dnsinfo/dnsinfo.txt"
		opts.ExtraEnvVars["FELIX_DNSCACHESAVEINTERVAL"] = "1"
		opts.ExtraEnvVars["FELIX_DNSLOGSFILEENABLED"] = "true"
		opts.ExtraEnvVars["FELIX_DNSLOGSFILEDIRECTORY"] = "/dnsinfo"
		opts.ExtraEnvVars["FELIX_DNSLOGSFLUSHINTERVAL"] = "1"
		opts.ExtraEnvVars["FELIX_PolicySyncPathPrefix"] = "/var/run/calico/policysync"
		opts.ExtraEnvVars["FELIX_DefaultEndpointToHostAction"] = "ACCEPT"
		opts.IPIPEnabled = false
		felixes, etcd, client, infra = infrastructure.StartNNodeEtcdTopology(2, opts)
		felix = felixes[0]
		server = felixes[1]
		infrastructure.CreateDefaultProfile(client, "default", map[string]string{"default": ""}, "")

		// Create a workload, using that profile.
		for ii := range w {
			iiStr := strconv.Itoa(ii)
			w[ii] = workload.Run(felix, "w"+iiStr, "default", "10.65.0.1"+iiStr, "8055", "tcp")
			w[ii].Configure(client)
		}

		// Configure Felix to trust itself, the other Felix, and w[1] as DNS servers.
		utils.UpdateFelixConfig(client, func(fc *api.FelixConfiguration) {
			fc.Spec.DNSTrustedServers = &[]string{felix.IP, server.IP, w[1].IP}
		})
		log.Info("Wait for Felix to restart")
		<-felix.WatchStdoutFor(regexp.MustCompile("Felix starting up"))
		log.Info("Felix has restarted")

		if bpfEnabled {
			// Wait for trusted DNS servers ipset to be populated.
			Eventually(func() bool {
				out, err := felix.ExecOutput("calico-bpf", "ipsets", "dump")
				Expect(err).NotTo(HaveOccurred())
				return (strings.Contains(out, w[1].IP+":53 (proto 17)") &&
					strings.Contains(out, felix.IP+":53 (proto 17)") &&
					strings.Contains(out, server.IP+":53 (proto 17)"))
			}, "5s", "0.5s").Should(BeTrue())

			// Ensure workloads are set up.
			for ii := range w {
				Eventually(func() int {
					return felix.NumTCBPFProgs(w[ii].InterfaceName)
				}, "5s", "0.5s").Should(Equal(2))
				Consistently(func() int {
					return felix.NumTCBPFProgs(w[ii].InterfaceName)
				}, "5s", "0.5s").Should(Equal(2))
			}
		}
		time.Sleep(5 * time.Second)

		// Ensure that workload policy programs are in place.
		cc = &connectivity.Checker{}
		cc.ExpectSome(w[0], w[1])
		cc.ExpectSome(w[1], w[0])
		cc.CheckConnectivity()
	})

	// Stop etcd and workloads, collecting some state if anything failed.
	AfterEach(func() {
		if CurrentGinkgoTestDescription().Failed {
			felix.Exec("calico-bpf", "ipsets", "dump", "--debug")
		}

		for ii := range w {
			w[ii].Stop()
		}
		felix.Stop()
		server.Stop()
		etcd.Stop()
		infra.Stop()
	})

	dnsRequestBytes := func(id uint16) []byte {
		pkt := gopacket.NewSerializeBuffer()
		dns := &layers.DNS{
			ID:      id,
			QR:      false,
			OpCode:  layers.DNSOpCodeQuery,
			QDCount: 1,
			Questions: []layers.DNSQuestion{{
				Name:  []byte("example.com"),
				Type:  layers.DNSTypeA,
				Class: layers.DNSClassIN,
			}},
		}
		err := dns.SerializeTo(pkt, gopacket.SerializeOptions{})
		Expect(err).NotTo(HaveOccurred())
		return pkt.Bytes()
	}

	dnsResponseBytes := func(id uint16) []byte {
		pkt := gopacket.NewSerializeBuffer()
		dns := &layers.DNS{
			ID:      id,
			QR:      true,
			OpCode:  layers.DNSOpCodeQuery,
			QDCount: 1,
			Questions: []layers.DNSQuestion{{
				Name:  []byte("example.com"),
				Type:  layers.DNSTypeA,
				Class: layers.DNSClassIN,
			}},
			ANCount: 1,
			Answers: []layers.DNSResourceRecord{{
				Name:  []byte("example.com"),
				Type:  layers.DNSTypeA,
				Class: layers.DNSClassIN,
				TTL:   3600,
				IP:    net.ParseIP("1.2.3.4"),
			}},
		}
		err := dns.SerializeTo(pkt, gopacket.SerializeOptions{})
		Expect(err).NotTo(HaveOccurred())
		return pkt.Bytes()
	}

	checkSingleDNSLogWithLatencyAndNoWarnings := func(dnsLogC chan struct{}, allowHostLatencyBug bool) func() (errs []error) {
		return func() (errs []error) {
			select {
			case <-dnsLogC:
				if !allowHostLatencyBug {
					// In iptables mode a warning log can be emitted because
					// we're missing timestamps on DNS packets sent from a
					// host-networked client or server.  In turn this means we
					// can't measure latency for exchanges involving a
					// host-networked client or server.
					errs = append(errs, errors.New("DNS warning logs were emitted"))
				}
			default:
			}
			dnsLogs, err := getDNSLogs(path.Join(dnsDir, "dns.log"))
			if err != nil {
				errs = append(errs, err)
			} else if len(dnsLogs) != 1 {
				errs = append(errs, fmt.Errorf("Unexpected number of DNS logs: %v", len(dnsLogs)))
			} else {
				if !strings.Contains(dnsLogs[0], `"count":1`) {
					errs = append(errs, fmt.Errorf("Unexpected count in DNS log: %v", dnsLogs[0]))
				}
				if !allowHostLatencyBug && !strings.Contains(dnsLogs[0], `"latency_count":1`) {
					// See just above for why we sometimes can't verify latency_count here.
					errs = append(errs, fmt.Errorf("Unexpected latency_count in DNS log: %v", dnsLogs[0]))
				}
			}
			return
		}
	}

	testDNSExchange := func(client, server interface{}) {
		var (
			clientContainer, serverContainer *containers.Container
			clientIP, serverIP               string
			clientNamespace, serverNamespace string
			allowHostLatencyBug              bool
		)
		switch c := client.(type) {
		case *workload.Workload:
			// Client is a workload.
			clientContainer = c.C
			clientIP = c.IP
			clientNamespace = c.NamespacePath()
		case *infrastructure.Felix:
			// Client is a host (Felix).
			clientContainer = c.Container
			clientIP = c.IP
			clientNamespace = "-"
			allowHostLatencyBug = !bpfEnabled
		}
		switch s := server.(type) {
		case *workload.Workload:
			// Server is a workload.
			serverContainer = s.C
			serverIP = s.IP
			serverNamespace = s.NamespacePath()
		case *infrastructure.Felix:
			// Server is a host (Felix).
			serverContainer = s.Container
			serverIP = s.IP
			serverNamespace = "-"
			allowHostLatencyBug = !bpfEnabled
		}
		dnsLogC := felix.WatchStdoutFor(regexp.MustCompile("WARNING.*DNS"))
		clientContainer.ExecWithInput(dnsRequestBytes(1), "/test-connection",
			clientNamespace,
			serverIP,
			"53",
			"--source-ip="+clientIP,
			"--source-port=53",
			"--protocol=udp-noconn",
			"--stdin")
		serverContainer.ExecWithInput(dnsResponseBytes(1), "/test-connection",
			serverNamespace,
			clientIP,
			"53",
			"--source-ip="+serverIP,
			"--source-port=53",
			"--protocol=udp-noconn",
			"--stdin")
		Eventually(checkSingleDNSLogWithLatencyAndNoWarnings(dnsLogC, allowHostLatencyBug), "5s", "0.5s").Should(BeEmpty())
	}

	It("logs correctly for (1) DNS from local workload client to local workload server", func() {
		testDNSExchange(w[0], w[1])
	})

	It("logs correctly for (2) DNS from local workload client to server on host", func() {
		testDNSExchange(w[0], felix)
	})

	It("logs correctly for (3) DNS from client on host to local workload server", func() {
		testDNSExchange(felix, w[1])
	})

	It("logs correctly for (4) DNS from local workload client to server elsewhere", func() {
		testDNSExchange(w[0], server)
	})

	It("logs correctly for (5) DNS from client on host to server elsewhere", func() {
		testDNSExchange(felix, server)
	})
})
