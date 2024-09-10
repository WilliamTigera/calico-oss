//go:build !windows
// +build !windows

// Copyright (c) 2024 Tigera, Inc. All rights reserved.
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

package dnsresolver_test

import (
	"testing"

	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/felix/bpf/dnsresolver"
	"github.com/projectcalico/calico/felix/bpf/mock"
)

var ids = map[string]uint64{
	"1":    1,
	"2":    2,
	"3":    3,
	"4":    4,
	"5":    5,
	"111":  111,
	"123":  123,
	"1234": 1234,
	"666":  666,
}

func TestDomainTracker(t *testing.T) {
	RegisterTestingT(t)

	log.SetLevel(log.DebugLevel)

	tracker, err := dnsresolver.NewDomainTrackerWithMaps(func(s string) uint64 {
		return ids[s]
	},
		mock.NewMockMap(dnsresolver.DNSPfxMapParams),
		mock.NewMockMap(dnsresolver.DNSSetMapParams),
	)
	Expect(err).NotTo(HaveOccurred())
	defer tracker.Close()

	m := tracker.Maps()
	dnsPfxMap, dnsSetsMap := m[0], m[1]

	tracker.Add("www.ubuntu.com", "111")
	tracker.Add("ubuntu.com", "123")
	tracker.Add("*.ubuntu.com", "1234")
	tracker.Add("archive.ubuntu.com", "1", "2", "3")
	err = tracker.ApplyAllChanges()
	Expect(err).NotTo(HaveOccurred())

	v, err := dnsPfxMap.Get(dnsresolver.NewPfxKey("ubuntu.com").AsBytes())
	Expect(err).NotTo(HaveOccurred())
	pid := uint64(dnsresolver.DNSPfxValueFromBytes(v))
	/* includes */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 123).AsBytes()) /* ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	/* does not include */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 1234).AsBytes()) /* *.ubuntu.com */
	Expect(err).To(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 111).AsBytes()) /* archive.ubuntu.com */
	Expect(err).To(HaveOccurred())

	v, err = dnsPfxMap.Get(dnsresolver.NewPfxKey("*.ubuntu.com").AsBytes())
	Expect(err).NotTo(HaveOccurred())
	pid = uint64(dnsresolver.DNSPfxValueFromBytes(v))
	pidStarUbuntu := pid
	/* includes */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 1234).AsBytes()) /* *.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	/* does not include */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 123).AsBytes()) /* ubuntu.com */
	Expect(err).To(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 111).AsBytes()) /* www.ubuntu.com */
	Expect(err).To(HaveOccurred())

	v, err = dnsPfxMap.Get(dnsresolver.NewPfxKey("www.ubuntu.com").AsBytes())
	Expect(err).NotTo(HaveOccurred())
	pid = uint64(dnsresolver.DNSPfxValueFromBytes(v))
	pidWWW := pid
	/* includes */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 111).AsBytes()) /* www.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 1234).AsBytes()) /* *.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	/* does not include */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 123).AsBytes()) /* ubuntu.com */
	Expect(err).To(HaveOccurred())

	v, err = dnsPfxMap.Get(dnsresolver.NewPfxKey("archive.ubuntu.com").AsBytes())
	Expect(err).NotTo(HaveOccurred())
	pid = uint64(dnsresolver.DNSPfxValueFromBytes(v))
	pidArchive := pid
	/* includes */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 1).AsBytes()) /* *archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 2).AsBytes()) /* *archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 3).AsBytes()) /* *archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 1234).AsBytes()) /* *.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())

	/* add again */
	tracker.Add("archive.ubuntu.com", "1")
	err = tracker.ApplyAllChanges()
	Expect(err).NotTo(HaveOccurred())

	/* includes the same stuff */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 1).AsBytes()) /* archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 2).AsBytes()) /* archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 3).AsBytes()) /* archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 1234).AsBytes()) /* *.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())

	tracker.Add("archive.ubuntu.com", "4")
	err = tracker.ApplyAllChanges()
	Expect(err).NotTo(HaveOccurred())

	/* includes the same stuff */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 1).AsBytes()) /* archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 2).AsBytes()) /* archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 3).AsBytes()) /* archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 4).AsBytes()) /* archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 1234).AsBytes()) /* *.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())

	tracker.Del("archive.ubuntu.com", "1", "3")
	err = tracker.ApplyAllChanges()
	Expect(err).NotTo(HaveOccurred())

	/* includes the same stuff */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 2).AsBytes()) /* archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 4).AsBytes()) /* archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 1234).AsBytes()) /* *.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	/* does not include */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 1).AsBytes()) /* archive.ubuntu.com */
	Expect(err).To(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 3).AsBytes()) /* archive.ubuntu.com */
	Expect(err).To(HaveOccurred())

	tracker.Del("*.ubuntu.com", "1234")
	err = tracker.ApplyAllChanges()
	Expect(err).NotTo(HaveOccurred())

	/* no more includes */
	_, err = dnsPfxMap.Get(dnsresolver.NewPfxKey("*.ubuntu.com").AsBytes())
	Expect(err).To(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidStarUbuntu, 1234).AsBytes()) /* *.ubuntu.com */
	Expect(err).To(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidArchive, 1234).AsBytes()) /* archive.ubuntu.com */
	Expect(err).To(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pidWWW, 1234).AsBytes()) /* www.ubuntu.com */
	Expect(err).To(HaveOccurred())
}

func TestDomainTrackerWildcards(t *testing.T) {
	RegisterTestingT(t)

	log.SetLevel(log.DebugLevel)

	tracker, err := dnsresolver.NewDomainTrackerWithMaps(func(s string) uint64 {
		return ids[s]
	},
		mock.NewMockMap(dnsresolver.DNSPfxMapParams),
		mock.NewMockMap(dnsresolver.DNSSetMapParams),
	)
	Expect(err).NotTo(HaveOccurred())
	defer tracker.Close()

	m := tracker.Maps()
	dnsPfxMap, dnsSetsMap := m[0], m[1]

	tracker.Add("*.archive.ubuntu.com", "3")
	tracker.Add("*.ubuntu.com", "2")
	tracker.Add("archive.ubuntu.com", "111")
	tracker.Add("*.com", "1")
	err = tracker.ApplyAllChanges()
	Expect(err).NotTo(HaveOccurred())

	v, err := dnsPfxMap.Get(dnsresolver.NewPfxKey("archive.ubuntu.com").AsBytes())
	Expect(err).NotTo(HaveOccurred())
	pid := uint64(dnsresolver.DNSPfxValueFromBytes(v))
	/* includes */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 111).AsBytes()) /* archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 2).AsBytes()) /* *. ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 1).AsBytes()) /* *.com */
	Expect(err).NotTo(HaveOccurred())
	/* does not include */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 3).AsBytes()) /* *.archive.ubuntu.com */
	Expect(err).To(HaveOccurred())

	v, err = dnsPfxMap.Get(dnsresolver.NewPfxKey(".archive.ubuntu.com").AsBytes())
	Expect(err).NotTo(HaveOccurred())
	pid = uint64(dnsresolver.DNSPfxValueFromBytes(v))
	/* includes */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 3).AsBytes()) /* *.archive.ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 2).AsBytes()) /* *. ubuntu.com */
	Expect(err).NotTo(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 1).AsBytes()) /* *.com */
	Expect(err).NotTo(HaveOccurred())
	/* does not include */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 111).AsBytes()) /* archive.ubuntu.com */
	Expect(err).To(HaveOccurred())

	v, err = dnsPfxMap.Get(dnsresolver.NewPfxKey("*.com").AsBytes())
	Expect(err).NotTo(HaveOccurred())
	pid = uint64(dnsresolver.DNSPfxValueFromBytes(v))
	/* includes */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 1).AsBytes()) /* *.com */
	Expect(err).NotTo(HaveOccurred())
	/* does not include */
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 111).AsBytes()) /* archive.ubuntu.com */
	Expect(err).To(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 2).AsBytes()) /* *. ubuntu.com */
	Expect(err).To(HaveOccurred())
	_, err = dnsSetsMap.Get(dnsresolver.NewDNSSetKey(pid, 3).AsBytes()) /* *.archive.ubuntu.com */
	Expect(err).To(HaveOccurred())
}
