// Copyright (c) 2024 Tigera, Inc. All rights reserved.

package ut_test

import (
        //"fmt"
	"os"

        "testing"
        //"github.com/google/gopacket"
        //"github.com/google/gopacket/layers"
        . "github.com/onsi/gomega"

	"github.com/projectcalico/calico/felix/bpf/bpfdefs"
	"github.com/projectcalico/calico/felix/bpf/iptables"
        "github.com/projectcalico/calico/felix/bpf/dnsresolver"
        "github.com/projectcalico/calico/felix/bpf/ipsets"
        //"github.com/projectcalico/calico/felix/bpf/maps"
        "github.com/projectcalico/calico/felix/ip"
)


func TestBPFDnsParserProgramForIPTables(t *testing.T) {
        RegisterTestingT(t)
        err := iptables.CreateObjPinDir()
        Expect(err).NotTo(HaveOccurred())
	defer iptables.CleanupObjPinDir()

	err = iptables.LoadDNSParserBPFProgram("debug")
	Expect(err).NotTo(HaveOccurred())

	pinPath := bpfdefs.DnsObjDir + "cali_ipt_parse_dns"
	_, err = os.Stat(pinPath)
	Expect(err).NotTo(HaveOccurred())

	defer os.RemoveAll(pinPath)

        // DNS response to archive.ubuntu.com with multiple A answers. The packet
        // was obtained using tcpdump.
        pktBytes := []byte{
                26, 97, 165, 211, 168, 175, 246, 111, 42, 69, 108, 168, 8, 0, 69, 0, 0,
                234, 220, 104, 64, 0, 125, 17, 79, 45, 10, 100, 0, 10, 192, 168, 6, 87,
                0, 53, 225, 200, 0, 214, 38, 108, 205, 111, 129, 128, 0, 1, 0, 5, 0, 0,
                0, 0, 7, 97, 114, 99, 104, 105, 118, 101, 6, 117, 98, 117, 110, 116,
                117, 3, 99, 111, 109, 0, 0, 1, 0, 1, 7, 97, 114, 99, 104, 105, 118, 101,
                6, 117, 98, 117, 110, 116, 117, 3, 99, 111, 109, 0, 0, 1, 0, 1, 0, 0, 0,
                25, 0, 4, 91, 189, 91, 83, 7, 97, 114, 99, 104, 105, 118, 101, 6, 117,
                98, 117, 110, 116, 117, 3, 99, 111, 109, 0, 0, 1, 0, 1, 0, 0, 0, 25, 0,
                4, 185, 125, 190, 36, 7, 97, 114, 99, 104, 105, 118, 101, 6, 117, 98,
                117, 110, 116, 117, 3, 99, 111, 109, 0, 0, 1, 0, 1, 0, 0, 0, 25, 0, 4,
                91, 189, 91, 82, 7, 97, 114, 99, 104, 105, 118, 101, 6, 117, 98, 117,
                110, 116, 117, 3, 99, 111, 109, 0, 0, 1, 0, 1, 0, 0, 0, 25, 0, 4, 185,
                125, 190, 39, 7, 97, 114, 99, 104, 105, 118, 101, 6, 117, 98, 117, 110,
                116, 117, 3, 99, 111, 109, 0, 0, 1, 0, 1, 0, 0, 0, 25, 0, 4, 91, 189,
                91, 81,
        }

        ids := map[string]uint64{
                "1":    1,
                "2":    2,
                "3":    3,
                "123":  123,
                "1234": 1234,
                "666":  666,
        }

        tracker, err := dnsresolver.NewDomainTracker(4, func(s string) uint64 {
                return ids[s]
        })
        Expect(err).NotTo(HaveOccurred())
        defer tracker.Close()

        tracker.Add("ubuntu.com", "123")
        tracker.Add("*.ubuntu.com", "1234")
        tracker.Add("archive.ubuntu.com", "1", "2", "3")
        err = tracker.ApplyAllChanges()
        Expect(err).NotTo(HaveOccurred())

	res, err := bpftoolProgRun(pinPath, pktBytes, nil)
	Expect(err).NotTo(HaveOccurred())
	Expect(res.Retval).To(Equal(1))

       for _, setID := range []uint64{1, 2, 3, 1234} {
                _, err := ipsMap.Get(
                        ipsets.MakeBPFIPSetEntry(setID, ip.CIDRFromStringNoErr("91.189.91.81/32").(ip.V4CIDR), 0, 0).AsBytes())
                Expect(err).NotTo(HaveOccurred())
                _, err = ipsMap.Get(
                        ipsets.MakeBPFIPSetEntry(setID, ip.CIDRFromStringNoErr("91.189.91.82/32").(ip.V4CIDR), 0, 0).AsBytes())
                Expect(err).NotTo(HaveOccurred())
                _, err = ipsMap.Get(
                        ipsets.MakeBPFIPSetEntry(setID, ip.CIDRFromStringNoErr("91.189.91.83/32").(ip.V4CIDR), 0, 0).AsBytes())
                Expect(err).NotTo(HaveOccurred())
                _, err = ipsMap.Get(
                        ipsets.MakeBPFIPSetEntry(setID, ip.CIDRFromStringNoErr("185.125.190.36/32").(ip.V4CIDR), 0, 0).AsBytes())
                Expect(err).NotTo(HaveOccurred())
                _, err = ipsMap.Get(
                        ipsets.MakeBPFIPSetEntry(setID, ip.CIDRFromStringNoErr("185.125.190.39/32").(ip.V4CIDR), 0, 0).AsBytes())
                Expect(err).NotTo(HaveOccurred())
        }


}

