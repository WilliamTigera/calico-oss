// Copyright (c) 2019 Tigera, Inc. SelectAll rights reserved.
package xrefcache_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	. "github.com/tigera/compliance/internal/testutils"
	"github.com/tigera/compliance/pkg/syncer"
	"github.com/tigera/compliance/pkg/xrefcache"
)

var _ = Describe("Basic CRUD of network sets with no other resources present", func() {
	var tester *XrefCacheTester

	BeforeEach(func() {
		tester = NewXrefCacheTester()
		tester.OnStatusUpdate(syncer.NewStatusUpdateInSync())
	})

	// Ensure  the client resource list is in-sync with the resource helper.
	It("should handle basic CRUD and identify a network set with internet exposed", func() {
		By("applying a network set with no nets")
		tester.SetGlobalNetworkSet(Name1, NoLabels, 0)

		By("checking the cache settings")
		ns := tester.GetGlobalNetworkSet(Name1)
		Expect(ns).ToNot(BeNil())
		Expect(ns.Flags).To(BeZero())

		By("applying a network set with one public net")
		tester.SetGlobalNetworkSet(Name1, Label1, Public)

		By("checking the cache settings")
		ns = tester.GetGlobalNetworkSet(Name1)
		Expect(ns).ToNot(BeNil())
		Expect(ns.Flags).To(Equal(xrefcache.CacheEntryInternetExposed))

		By("applying a network set with one private net")
		tester.SetGlobalNetworkSet(Name1, Label1, Private)

		By("checking the cache settings")
		ns = tester.GetGlobalNetworkSet(Name1)
		Expect(ns).ToNot(BeNil())
		Expect(ns.Flags).To(BeZero())

		By("applying a network set with one private and one public net")
		tester.SetGlobalNetworkSet(Name1, Label1, Public|Private)

		By("checking the cache settings")
		ns = tester.GetGlobalNetworkSet(Name1)
		Expect(ns).ToNot(BeNil())
		Expect(ns.Flags).To(Equal(xrefcache.CacheEntryInternetExposed))

		By("applying another network set with no nets")
		tester.SetGlobalNetworkSet(Name2, NoLabels, 0)

		By("checking the cache settings")
		ns = tester.GetGlobalNetworkSet(Name1)
		Expect(ns).ToNot(BeNil())
		Expect(ns.Flags).To(Equal(xrefcache.CacheEntryInternetExposed))
		ns = tester.GetGlobalNetworkSet(Name2)
		Expect(ns).ToNot(BeNil())
		Expect(ns.Flags).To(BeZero())

		By("deleting the first network set")
		tester.DeleteGlobalNetworkSet(Name1)

		By("checking the cache settings")
		ns = tester.GetGlobalNetworkSet(Name1)
		Expect(ns).To(BeNil())
		ns = tester.GetGlobalNetworkSet(Name2)
		Expect(ns).ToNot(BeNil())
		Expect(ns.Flags).To(BeZero())

		By("deleting the second network set")
		tester.DeleteGlobalNetworkSet(Name2)

		By("checking the cache settings")
		ns = tester.GetGlobalNetworkSet(Name1)
		Expect(ns).To(BeNil())
		ns = tester.GetGlobalNetworkSet(Name2)
		Expect(ns).To(BeNil())
	})
})
