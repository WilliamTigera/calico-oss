// Copyright (c) 2019 Tigera, Inc. SelectAll rights reserved.
package xrefcache_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	networkingv1 "k8s.io/api/networking/v1"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"

	. "github.com/tigera/compliance/internal/testutils"
	"github.com/tigera/compliance/pkg/xrefcache"
)

var _ = Describe("Basic CRUD of network policies with no other resources present", func() {
	var tester *XRefCacheTester

	BeforeEach(func() {
		tester = NewXrefCacheTester()
	})

	It("should handle basic CRUD of GlobalNetworkPolicy and determine non-xref state", func() {
		By("applying a GlobalNetworkPolicy, ingress no rules")
		tester.SetGlobalNetworkPolicy(Name1, SelectAll,
			[]apiv3.Rule{},
			nil,
		)

		By("checking the cache settings")
		np := tester.GetGlobalNetworkPolicy(Name1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedIngress))

		By("applying a GlobalNetworkPolicy, egress no rules")
		tester.SetGlobalNetworkPolicy(Name1, SelectAll,
			nil,
			[]apiv3.Rule{},
		)

		By("checking the cache settings")
		np = tester.GetGlobalNetworkPolicy(Name1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedEgress))

		By("applying a GlobalNetworkPolicy, ingress, one allow source rule with internet CIDR")
		tester.SetGlobalNetworkPolicy(Name1, SelectAll,
			[]apiv3.Rule{
				CalicoRuleNets(Allow, Source, Public),
			},
			nil,
		)

		By("checking the cache settings")
		np = tester.GetGlobalNetworkPolicy(Name1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedIngress | xrefcache.CacheEntryInternetExposedIngress))

		By("applying a GlobalNetworkPolicy, ingress, one allow destination rule with internet CIDR")
		tester.SetGlobalNetworkPolicy(Name1, SelectAll,
			[]apiv3.Rule{
				CalicoRuleNets(Allow, Destination, Public),
			},
			nil,
		)

		By("checking the cache settings - dest CIDR not relevant for ingress rule")
		np = tester.GetGlobalNetworkPolicy(Name1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(
			xrefcache.CacheEntryProtectedIngress |
				xrefcache.CacheEntryInternetExposedIngress |
				xrefcache.CacheEntryOtherNamespaceExposedIngress,
		))

		By("applying a GlobalNetworkPolicy, ingress, one allow source rule with private CIDR")
		tester.SetGlobalNetworkPolicy(Name1, SelectAll,
			[]apiv3.Rule{
				CalicoRuleNets(Allow, Source, Private),
			},
			nil,
		)

		By("checking the cache settings")
		np = tester.GetGlobalNetworkPolicy(Name1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedIngress))

		By("applying a GlobalNetworkPolicy, ingress and egress no rules")
		tester.SetGlobalNetworkPolicy(Name1, SelectAll,
			[]apiv3.Rule{},
			[]apiv3.Rule{},
		)

		By("checking the cache settings")
		np = tester.GetGlobalNetworkPolicy(Name1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedIngress | xrefcache.CacheEntryProtectedEgress))

		By("applying a GlobalNetworkPolicy, egress, one allow destination rule with internet CIDR")
		tester.SetGlobalNetworkPolicy(Name1, SelectAll,
			nil,
			[]apiv3.Rule{
				CalicoRuleNets(Allow, Destination, Public),
			},
		)

		By("checking the cache settings")
		np = tester.GetGlobalNetworkPolicy(Name1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedEgress | xrefcache.CacheEntryInternetExposedEgress))

		By("applying a GlobalNetworkPolicy, egress, one allow source rule with internet CIDR")
		tester.SetGlobalNetworkPolicy(Name1, SelectAll,
			nil,
			[]apiv3.Rule{
				CalicoRuleNets(Allow, Source, Public),
			},
		)

		By("checking the cache settings - source CIDR not relevant for egress rule")
		np = tester.GetGlobalNetworkPolicy(Name1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(
			xrefcache.CacheEntryProtectedEgress |
				xrefcache.CacheEntryInternetExposedEgress |
				xrefcache.CacheEntryOtherNamespaceExposedEgress,
		))

		By("applying a GlobalNetworkPolicy, egress, one allow destination rule with private CIDR")
		tester.SetGlobalNetworkPolicy(Name1, SelectAll,
			nil,
			[]apiv3.Rule{
				CalicoRuleNets(Allow, Destination, Private),
			},
		)

		By("checking the cache settings")
		np = tester.GetGlobalNetworkPolicy(Name1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedEgress))

		By("deleting the first network policy")
		tester.DeleteGlobalNetworkPolicy(Name1)

		By("checking the cache settings")
		np = tester.GetGlobalNetworkPolicy(Name1)
		Expect(np).To(BeNil())
	})

	It("should handle basic CRUD of Calico NetworkPolicy and determine non-xref state", func() {
		By("applying a NetworkPolicy, ingress no rules")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			[]apiv3.Rule{},
			nil,
		)

		By("checking the cache settings")
		np := tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedIngress))

		By("applying a NetworkPolicy, egress no rules")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			nil,
			[]apiv3.Rule{},
		)

		By("checking the cache settings")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedEgress))

		By("applying a NetworkPolicy, ingress, one allow source rule with internet CIDR")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			[]apiv3.Rule{
				CalicoRuleNets(Allow, Source, Public),
			},
			nil,
		)

		By("checking the cache settings")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(
			xrefcache.CacheEntryProtectedIngress |
				xrefcache.CacheEntryInternetExposedIngress,
		))

		By("applying a NetworkPolicy, ingress, one allow source rule with private CIDR")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			[]apiv3.Rule{
				CalicoRuleNets(Allow, Source, Private),
			},
			nil,
		)

		By("checking the cache settings")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedIngress))

		By("applying a NetworkPolicy, ingress, one allow source rule with selector")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			[]apiv3.Rule{
				CalicoRuleSelectors(Allow, Source, Select1, NoNamespaceSelector),
			},
			nil,
		)

		By("checking the cache settings")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedIngress))

		By("applying a NetworkPolicy, ingress, one allow source rule with namespace selector")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			[]apiv3.Rule{
				CalicoRuleSelectors(Allow, Source, NoSelector, Select1),
			},
			nil,
		)

		By("checking the cache settings")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(
			xrefcache.CacheEntryProtectedIngress |
				xrefcache.CacheEntryOtherNamespaceExposedIngress,
		))

		By("applying a NetworkPolicy, ingress, one allow destination rule with internet CIDR")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			[]apiv3.Rule{
				CalicoRuleNets(Allow, Destination, Public),
			},
			nil,
		)

		By("checking the cache settings - dest CIDR not relevant for ingress rule")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(
			xrefcache.CacheEntryProtectedIngress |
				xrefcache.CacheEntryInternetExposedIngress |
				xrefcache.CacheEntryOtherNamespaceExposedIngress,
		))

		By("applying a NetworkPolicy, ingress, one allow destination rule with selector")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			[]apiv3.Rule{
				CalicoRuleSelectors(Allow, Destination, Select1, NoNamespaceSelector),
			},
			nil,
		)

		By("checking the cache settings - dest selector not relevant for ingress rule")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(
			xrefcache.CacheEntryProtectedIngress |
				xrefcache.CacheEntryInternetExposedIngress |
				xrefcache.CacheEntryOtherNamespaceExposedIngress,
		))

		By("applying a NetworkPolicy, ingress and egress no rules")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			[]apiv3.Rule{},
			[]apiv3.Rule{},
		)

		By("checking the cache settings")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedIngress | xrefcache.CacheEntryProtectedEgress))

		By("applying a NetworkPolicy, egress, one allow destination rule with internet CIDR")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			nil,
			[]apiv3.Rule{
				CalicoRuleNets(Allow, Destination, Public),
			},
		)

		By("checking the cache settings")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedEgress | xrefcache.CacheEntryInternetExposedEgress))

		By("applying a NetworkPolicy, egress, one allow destination rule with private CIDR")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			nil,
			[]apiv3.Rule{
				CalicoRuleNets(Allow, Destination, Private),
			},
		)

		By("checking the cache settings")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedEgress))

		By("applying a NetworkPolicy, egress, one allow destination rule with selector")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			nil,
			[]apiv3.Rule{
				CalicoRuleSelectors(Allow, Destination, Select1, NoNamespaceSelector),
			},
		)

		By("checking the cache settings")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedEgress))

		By("applying a NetworkPolicy, egress, one allow destination rule with namespace selector")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			nil,
			[]apiv3.Rule{
				CalicoRuleSelectors(Allow, Destination, NoSelector, Select1),
			},
		)

		By("checking the cache settings")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(
			xrefcache.CacheEntryProtectedEgress |
				xrefcache.CacheEntryOtherNamespaceExposedEgress,
		))

		By("applying a NetworkPolicy, egress, one allow source rule with internet CIDR")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			nil,
			[]apiv3.Rule{
				CalicoRuleNets(Allow, Source, Public),
			},
		)

		By("checking the cache settings - source CIDR not relevant for egress rule")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(
			xrefcache.CacheEntryProtectedEgress |
				xrefcache.CacheEntryInternetExposedEgress |
				xrefcache.CacheEntryOtherNamespaceExposedEgress,
		))

		By("applying a NetworkPolicy, egress, one allow source rule with selector")
		tester.SetNetworkPolicy(Name1, Namespace1, SelectAll,
			nil,
			[]apiv3.Rule{
				CalicoRuleSelectors(Allow, Source, Select1, NoNamespaceSelector),
			},
		)

		By("checking the cache settings - source selector not relevant for egress rule")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(
			xrefcache.CacheEntryProtectedEgress |
				xrefcache.CacheEntryInternetExposedEgress |
				xrefcache.CacheEntryOtherNamespaceExposedEgress,
		))

		By("deleting the first network policy")
		tester.DeleteNetworkPolicy(Name1, Namespace1)

		By("checking the cache settings")
		np = tester.GetNetworkPolicy(Name1, Namespace1)
		Expect(np).To(BeNil())
	})

	It("should handle basic CRUD of Calico K8sNetworkPolicy and determine non-xref state", func() {
		By("applying a K8sNetworkPolicy, ingress no rules")
		tester.SetK8sNetworkPolicy(Name1, Namespace1, SelectAll,
			[]networkingv1.NetworkPolicyIngressRule{},
			nil,
		)

		By("checking the cache settings")
		np := tester.GetK8sNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedIngress))

		By("applying a K8sNetworkPolicy, egress no rules")
		tester.SetK8sNetworkPolicy(Name1, Namespace1, SelectAll,
			nil,
			[]networkingv1.NetworkPolicyEgressRule{},
		)

		By("checking the cache settings")
		np = tester.GetK8sNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedEgress))

		By("applying a K8sNetworkPolicy, ingress, one allow source rule with internet CIDR")
		tester.SetK8sNetworkPolicy(Name1, Namespace1, SelectAll,
			[]networkingv1.NetworkPolicyIngressRule{
				K8sIngressRuleNets(Allow, Source, Public),
			},
			nil,
		)

		By("checking the cache settings")
		np = tester.GetK8sNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(
			xrefcache.CacheEntryProtectedIngress |
				xrefcache.CacheEntryInternetExposedIngress,
		))

		By("applying a K8sNetworkPolicy, ingress, one allow source rule with private CIDR")
		tester.SetK8sNetworkPolicy(Name1, Namespace1, SelectAll,
			[]networkingv1.NetworkPolicyIngressRule{
				K8sIngressRuleNets(Allow, Source, Private),
			},
			nil,
		)

		By("checking the cache settings")
		np = tester.GetK8sNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedIngress))

		By("applying a K8sNetworkPolicy, ingress, one allow source rule with selector")
		tester.SetK8sNetworkPolicy(Name1, Namespace1, SelectAll,
			[]networkingv1.NetworkPolicyIngressRule{
				K8sIngressRuleSelectors(Allow, Source, Select1, NoNamespaceSelector),
			},
			nil,
		)

		By("checking the cache settings")
		np = tester.GetK8sNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedIngress))

		By("applying a K8sNetworkPolicy, ingress, one allow source rule with namespace selector")
		tester.SetK8sNetworkPolicy(Name1, Namespace1, SelectAll,
			[]networkingv1.NetworkPolicyIngressRule{
				K8sIngressRuleSelectors(Allow, Source, NoSelector, Select1),
			},
			nil,
		)

		By("checking the cache settings")
		np = tester.GetK8sNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(
			xrefcache.CacheEntryProtectedIngress |
				xrefcache.CacheEntryOtherNamespaceExposedIngress,
		))

		By("applying a K8sNetworkPolicy, ingress and egress no rules")
		tester.SetK8sNetworkPolicy(Name1, Namespace1, SelectAll,
			[]networkingv1.NetworkPolicyIngressRule{},
			[]networkingv1.NetworkPolicyEgressRule{},
		)

		By("checking the cache settings")
		np = tester.GetK8sNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedIngress | xrefcache.CacheEntryProtectedEgress))

		By("applying a K8sNetworkPolicy, egress, one allow destination rule with internet CIDR")
		tester.SetK8sNetworkPolicy(Name1, Namespace1, SelectAll,
			nil,
			[]networkingv1.NetworkPolicyEgressRule{
				K8sEgressRuleNets(Allow, Destination, Public),
			},
		)

		By("checking the cache settings")
		np = tester.GetK8sNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedEgress | xrefcache.CacheEntryInternetExposedEgress))

		By("applying a K8sNetworkPolicy, egress, one allow destination rule with private CIDR")
		tester.SetK8sNetworkPolicy(Name1, Namespace1, SelectAll,
			nil,
			[]networkingv1.NetworkPolicyEgressRule{
				K8sEgressRuleNets(Allow, Destination, Private),
			},
		)

		By("checking the cache settings")
		np = tester.GetK8sNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedEgress))

		By("applying a K8sNetworkPolicy, egress, one allow destination rule with selector")
		tester.SetK8sNetworkPolicy(Name1, Namespace1, SelectAll,
			nil,
			[]networkingv1.NetworkPolicyEgressRule{
				K8sEgressRuleSelectors(Allow, Destination, Select1, NoNamespaceSelector),
			},
		)

		By("checking the cache settings")
		np = tester.GetK8sNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(xrefcache.CacheEntryProtectedEgress))

		By("applying a K8sNetworkPolicy, egress, one allow destination rule with namespace selector")
		tester.SetK8sNetworkPolicy(Name1, Namespace1, SelectAll,
			nil,
			[]networkingv1.NetworkPolicyEgressRule{
				K8sEgressRuleSelectors(Allow, Destination, NoSelector, Select1),
			},
		)

		By("checking the cache settings")
		np = tester.GetK8sNetworkPolicy(Name1, Namespace1)
		Expect(np).ToNot(BeNil())
		Expect(np.Flags).To(Equal(
			xrefcache.CacheEntryProtectedEgress |
				xrefcache.CacheEntryOtherNamespaceExposedEgress,
		))

		By("deleting the first network policy")
		tester.DeleteK8sNetworkPolicy(Name1, Namespace1)

		By("checking the cache settings")
		np = tester.GetK8sNetworkPolicy(Name1, Namespace1)
		Expect(np).To(BeNil())
	})
})
