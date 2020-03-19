// Copyright (c) 2019-2020 Tigera, Inc. All rights reserved.

package intdataplane

import (
	"net"
	"regexp"
	"sync"
	"time"

	"github.com/google/gopacket/layers"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/felix/testutils"
	"github.com/projectcalico/libcalico-go/lib/set"
)

var _ = Describe("Domain Info Store", func() {
	var (
		domainStore     *domainInfoStore
		mockDNSRecA1    = testutils.MakeA("a.com", "10.0.0.10")
		mockDNSRecA2    = testutils.MakeA("b.com", "10.0.0.20")
		mockDNSRecAAAA1 = testutils.MakeAAAA("aaaa.com", "fe80:fe11::1")
		mockDNSRecAAAA2 = testutils.MakeAAAA("bbbb.com", "fe80:fe11::2")
		invalidDNSRec   = layers.DNSResourceRecord{
			Name:       []byte("invalid#rec.com"),
			Type:       layers.DNSTypeMX,
			Class:      layers.DNSClassAny,
			TTL:        2147483648,
			DataLength: 0,
			Data:       []byte("999.000.999.000"),
			IP:         net.ParseIP("999.000.999.000"),
		}
		mockDNSRecCNAME = []layers.DNSResourceRecord{
			testutils.MakeCNAME("cname1.com", "cname2.com"),
			testutils.MakeCNAME("cname2.com", "cname3.com"),
			testutils.MakeCNAME("cname3.com", "a.com"),
		}
	)

	// Program a DNS record as an "answer" type response.
	programDNSAnswer := func(domainStore *domainInfoStore, dnsPacket layers.DNSResourceRecord) {
		var layerDNS layers.DNS
		layerDNS.Answers = append(layerDNS.Answers, dnsPacket)
		domainStore.processDNSPacket(&layerDNS)
	}

	// Program a DNS record as an "additionals" type response.
	programDNSAdditionals := func(domainStore *domainInfoStore, dnsPacket layers.DNSResourceRecord) {
		var layerDNS layers.DNS
		layerDNS.Additionals = append(layerDNS.Additionals, dnsPacket)
		domainStore.processDNSPacket(&layerDNS)
	}

	// Assert that the domain store accepted and signaled the given record (and reason).
	AssertDomainChanged := func(domainStore *domainInfoStore, d string, r string) {
		receivedInfo := <-domainStore.domainInfoChanges
		log.Infof("domainInfoChanged:  %s %s expected %s", receivedInfo.domain, receivedInfo.reason, d)
		Expect(receivedInfo).To(Equal(&domainInfoChanged{domain: d, reason: r}))
	}

	// Assert that the domain store registered the given record and then process its expiration.
	AssertValidRecord := func(dnsRec layers.DNSResourceRecord) {
		It("should result in a domain entry", func() {
			Expect(domainStore.GetDomainIPs(string(dnsRec.Name))).To(Equal([]string{dnsRec.IP.String()}))
		})
		It("should expire and signal a domain change", func() {
			domainStore.processMappingExpiry(string(dnsRec.Name), dnsRec.IP.String())
			AssertDomainChanged(domainStore, string(dnsRec.Name), "mapping expired")
			Expect(domainStore.collectGarbage()).To(Equal(1))
		})
	}

	// Create a new datastore.
	domainStoreCreateEx := func(capacity int) {
		domainChannel := make(chan *domainInfoChanged, capacity)
		config := &Config{
			DNSCacheFile:         "/dnsinfo",
			DNSCacheSaveInterval: time.Minute,
		}
		// For UT purposes, arrange that mappings always appear to have expired when UT code
		// calls processMappingExpiry.
		domainStore = newDomainInfoStoreWithShims(domainChannel, config, func(time.Time) bool { return true })
	}
	domainStoreCreate := func() {
		// Create domain info store with 100 capacity for changes channel.
		domainStoreCreateEx(100)
	}

	// Basic validation tests that add/expire one or two DNS records of A and AAAA type to the data store.
	domainStoreTestValidRec := func(dnsRec1, dnsRec2 layers.DNSResourceRecord) {
		Describe("receiving a DNS packet", func() {
			BeforeEach(func() {
				domainStoreCreate()
			})

			Context("with a valid type A DNS answer record", func() {
				BeforeEach(func() {
					programDNSAnswer(domainStore, dnsRec1)
					AssertDomainChanged(domainStore, string(dnsRec1.Name), "mapping added")
				})
				AssertValidRecord(dnsRec1)
			})

			Context("with a valid type A DNS additional record", func() {
				BeforeEach(func() {
					programDNSAdditionals(domainStore, dnsRec1)
					AssertDomainChanged(domainStore, string(dnsRec1.Name), "mapping added")
				})
				AssertValidRecord(dnsRec1)
			})

			Context("with two valid type A DNS answer records", func() {
				BeforeEach(func() {
					programDNSAnswer(domainStore, dnsRec1)
					AssertDomainChanged(domainStore, string(dnsRec1.Name), "mapping added")
					programDNSAnswer(domainStore, dnsRec2)
					AssertDomainChanged(domainStore, string(dnsRec2.Name), "mapping added")
				})
				AssertValidRecord(dnsRec1)
				AssertValidRecord(dnsRec2)
			})

			Context("with two valid type A DNS additional records", func() {
				BeforeEach(func() {
					programDNSAdditionals(domainStore, dnsRec1)
					AssertDomainChanged(domainStore, string(dnsRec1.Name), "mapping added")
					programDNSAdditionals(domainStore, dnsRec2)
					AssertDomainChanged(domainStore, string(dnsRec2.Name), "mapping added")
				})
				AssertValidRecord(dnsRec1)
				AssertValidRecord(dnsRec2)
			})
		})
	}

	// Check that a malformed DNS record will not be accepted.
	domainStoreTestInvalidRec := func(dnsRec layers.DNSResourceRecord) {
		Context("with an invalid DNS record", func() {
			BeforeEach(func() {
				domainStoreCreate()
				programDNSAnswer(domainStore, dnsRec)
			})
			It("should return nil", func() {
				Expect(domainStore.GetDomainIPs(string(dnsRec.Name))).To(BeNil())
			})
		})
	}

	// Check that a chain of CNAME records with one A record results in a domain change only for the latter.
	domainStoreTestCNAME := func(CNAMErecs []layers.DNSResourceRecord, aRec layers.DNSResourceRecord) {
		Context("with a chain of CNAME records", func() {
			BeforeEach(func() {
				// The ordering of the signals sent back through domainInfoChanges is not deterministic, which prevents
				// us from asserting their specific values. But we can still check that the correct number of signals
				// are sent based on the length of the CNAME chain passed in.
				domainStoreCreate()
				for _, r := range CNAMErecs {
					programDNSAnswer(domainStore, r)
					Expect(domainStore.domainInfoChanges).Should(Receive())
				}
				programDNSAnswer(domainStore, aRec)
				Expect(domainStore.domainInfoChanges).Should(Receive())
			})
			It("should result in a CNAME->A mapping", func() {
				Expect(domainStore.GetDomainIPs(string(CNAMErecs[0].Name))).To(Equal([]string{aRec.IP.String()}))
			})
		})
	}

	domainStoreTestValidRec(mockDNSRecA1, mockDNSRecA2)
	domainStoreTestValidRec(mockDNSRecAAAA1, mockDNSRecAAAA2)
	domainStoreTestInvalidRec(invalidDNSRec)
	domainStoreTestCNAME(mockDNSRecCNAME, mockDNSRecA1)

	expectChangesFor := func(domains ...string) {
		domainsSignaled := set.New()
	loop:
		for {
			select {
			case signal := <-domainStore.domainInfoChanges:
				log.Debugf("Got domain change signal for %v", signal.domain)
				domainsSignaled.Add(signal.domain)
			default:
				break loop
			}
		}
		// We shouldn't care if _more_ domains are signaled than we expect.  Just check that
		// the expected ones _are_ signaled.
		for _, domain := range domains {
			Expect(domainsSignaled.Contains(domain)).To(BeTrue())
		}
	}

	Context("with monitor thread", func() {

		var (
			expectedSeen      bool
			expectedDomainIPs []string
			monitorMutex      sync.Mutex
			killMonitor       chan struct{}
			domainChannel     chan *domainInfoChanged
		)

		monitor := func(domain string) {
			for {
			loop:
				for {
					select {
					case <-killMonitor:
						return
					case signal := <-domainChannel:
						log.Debugf("Got domain change signal for %v", signal.domain)
						monitorMutex.Lock()
						if signal.domain == domain {
							expectedSeen = true
							expectedDomainIPs = domainStore.GetDomainIPs(domain)
						}
						monitorMutex.Unlock()
					default:
						break loop
					}
				}
			}
		}

		checkMonitor := func(expectedIPs []string) {
			time.Sleep(time.Second)
			monitorMutex.Lock()
			defer monitorMutex.Unlock()
			Expect(expectedSeen).To(BeTrue())
			expectedSeen = false
			Expect(expectedDomainIPs).To(Equal(expectedIPs))
		}

		BeforeEach(func() {
			expectedSeen = false
			killMonitor = make(chan struct{})
			domainStoreCreateEx(0)
			domainChannel = domainStore.domainInfoChanges
			go monitor("*.microsoft.com")
		})

		AfterEach(func() {
			domainChannel = nil
			close(killMonitor)
		})

		It("microsoft case", func() {
			Expect(domainStore.GetDomainIPs("*.microsoft.com")).To(Equal([]string(nil)))
			programDNSAnswer(domainStore, testutils.MakeCNAME("www.microsoft.com", "www.microsoft.com-c-3.edgekey.net"))
			checkMonitor(nil)
			programDNSAnswer(domainStore, testutils.MakeCNAME("www.microsoft.com-c-3.edgekey.net", "www.microsoft.com-c-3.edgekey.net.globalredir.akadns.net"))
			programDNSAnswer(domainStore, testutils.MakeCNAME("www.microsoft.com-c-3.edgekey.net.globalredir.akadns.net", "e13678.dspb.akamaiedge.net"))
			programDNSAnswer(domainStore, testutils.MakeA("e13678.dspb.akamaiedge.net", "104.75.174.50"))
			checkMonitor([]string{"104.75.174.50"})
		})
	})

	// Test where:
	// - a1.com and a2.com are both CNAMEs for b.com
	// - b.com is a CNAME for c.com
	// - c.com resolves to an IP address
	// The ipsets manager is interested in both a1.com and a2.com.
	//
	// The key point is that when the IP address for c.com changes, the ipsets manager
	// should be notified that domain info has changed for both a1.com and a2.com.
	It("should handle a branched DNS graph", func() {
		domainStoreCreate()
		programDNSAnswer(domainStore, testutils.MakeCNAME("a1.com", "b.com"))
		programDNSAnswer(domainStore, testutils.MakeCNAME("a2.com", "b.com"))
		programDNSAnswer(domainStore, testutils.MakeCNAME("b.com", "c.com"))
		programDNSAnswer(domainStore, testutils.MakeA("c.com", "3.4.5.6"))
		expectChangesFor("a1.com", "a2.com", "b.com", "c.com")
		Expect(domainStore.GetDomainIPs("a1.com")).To(Equal([]string{"3.4.5.6"}))
		Expect(domainStore.GetDomainIPs("a2.com")).To(Equal([]string{"3.4.5.6"}))
		programDNSAnswer(domainStore, testutils.MakeA("c.com", "7.8.9.10"))
		expectChangesFor("a1.com", "a2.com", "c.com")
		Expect(domainStore.GetDomainIPs("a1.com")).To(ConsistOf("3.4.5.6", "7.8.9.10"))
		Expect(domainStore.GetDomainIPs("a2.com")).To(ConsistOf("3.4.5.6", "7.8.9.10"))
		domainStore.processMappingExpiry("c.com", "3.4.5.6")
		expectChangesFor("a1.com", "a2.com", "c.com")
		Expect(domainStore.GetDomainIPs("a1.com")).To(Equal([]string{"7.8.9.10"}))
		Expect(domainStore.GetDomainIPs("a2.com")).To(Equal([]string{"7.8.9.10"}))
		// No garbage yet, because c.com still has a value and is the RHS of other mappings.
		Expect(domainStore.collectGarbage()).To(Equal(0))
	})

	It("is not vulnerable to CNAME loops", func() {
		domainStoreCreate()
		programDNSAnswer(domainStore, testutils.MakeCNAME("a.com", "b.com"))
		programDNSAnswer(domainStore, testutils.MakeCNAME("b.com", "c.com"))
		programDNSAnswer(domainStore, testutils.MakeCNAME("c.com", "a.com"))
		Expect(domainStore.GetDomainIPs("a.com")).To(BeEmpty())
	})

	It("0.0.0.0 is ignored", func() {
		domainStoreCreate()
		// 0.0.0.0 should be ignored.
		programDNSAnswer(domainStore, testutils.MakeA("a.com", "0.0.0.0"))
		Expect(domainStore.GetDomainIPs("a.com")).To(BeEmpty())
		// But not any other IP.
		programDNSAnswer(domainStore, testutils.MakeA("a.com", "0.0.0.1"))
		Expect(domainStore.GetDomainIPs("a.com")).To(HaveLen(1))
	})

	DescribeTable("it should identify wildcards",
		func(domain string, expectedIsWildcard bool) {
			Expect(isWildcard(domain)).To(Equal(expectedIsWildcard))
		},
		Entry("*.com",
			"*.com", true),
		Entry(".com",
			".com", false),
		Entry("google.com",
			"google.com", false),
		Entry("*.google.com",
			"*.google.com", true),
		Entry("update.*.tigera.io",
			"update.*.tigera.io", true),
		Entry("cpanel.blog.org",
			"cpanel.blog.org", false),
	)

	DescribeTable("it should build correct wildcard regexps",
		func(wildcard, expectedRegexp string) {
			Expect(wildcardToRegexpString(wildcard)).To(Equal(expectedRegexp))
		},
		Entry("*.com",
			"*.com", "^.*\\.com$"),
		Entry("*.google.com",
			"*.google.com", "^.*\\.google\\.com$"),
		Entry("update.*.tigera.io",
			"update.*.tigera.io", "^update\\..*\\.tigera\\.io$"),
	)

	DescribeTable("wildcards match as expected",
		func(wildcard, name string, expectedMatch bool) {
			regex, err := regexp.Compile(wildcardToRegexpString(wildcard))
			Expect(err).NotTo(HaveOccurred())
			Expect(regex.MatchString(name)).To(Equal(expectedMatch))
		},
		Entry("*.com",
			"*.com", "google.com", true),
		Entry("*.com",
			"*.com", "www.google.com", true),
		Entry("*.com",
			"*.com", "com", false),
		Entry("*.com",
			"*.com", "tigera.io", false),
		Entry("*.google.com",
			"*.google.com", "www.google.com", true),
		Entry("*.google.com",
			"*.google.com", "ipv6.google.com", true),
		Entry("*.google.com",
			"*.google.com", "ipv6google.com", false),
		Entry("*.google.com",
			"*.google.com", "ipv6.experimental.google.com", true),
		Entry("update.*.tigera.io",
			"update.*.tigera.io", "update.calico.tigera.io", true),
		Entry("update.*.tigera.io",
			"update.*.tigera.io", "update.tsee.tigera.io", true),
		Entry("update.*.tigera.io",
			"update.*.tigera.io", "update.security.tsee.tigera.io", true),
		Entry("update.*.tigera.io",
			"update.*.tigera.io", "update.microsoft.com", false),
	)

	Context("wildcard handling", func() {
		BeforeEach(func() {
			domainStoreCreate()
		})

		// Test where wildcard is configured in the data model before we have any DNS
		// information that matches it.
		Context("with client interested in *.google.com", func() {
			BeforeEach(func() {
				Expect(domainStore.GetDomainIPs("*.google.com")).To(BeEmpty())
			})

			Context("with IP for update.google.com", func() {
				BeforeEach(func() {
					programDNSAnswer(domainStore, testutils.MakeA("update.google.com", "1.2.3.5"))
				})

				It("should update *.google.com", func() {
					expectChangesFor("*.google.com")
					Expect(domainStore.GetDomainIPs("*.google.com")).To(Equal([]string{"1.2.3.5"}))
				})
			})
		})

		// Test where wildcard is configured in the data model when we already have DNS
		// information that matches it.
		Context("with IP for update.google.com", func() {
			BeforeEach(func() {
				programDNSAnswer(domainStore, testutils.MakeA("update.google.com", "1.2.3.5"))
			})

			It("should get that IP for *.google.com", func() {
				Expect(domainStore.GetDomainIPs("*.google.com")).To(Equal([]string{"1.2.3.5"}))
			})
		})
	})
})
