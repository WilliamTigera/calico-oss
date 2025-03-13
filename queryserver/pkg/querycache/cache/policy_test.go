// Copyright (c) 2025 Tigera, Inc. All rights reserved.
package cache_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/projectcalico/calico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/syncersv1/updateprocessors"
	"github.com/projectcalico/calico/queryserver/pkg/querycache/cache"
	"github.com/projectcalico/calico/queryserver/pkg/querycache/dispatcherv1v3"
)

var _ = Describe("Querycache policy tests", func() {
	Context("validate IsKubernetesType()", func() {
		var policiesCache cache.PoliciesCache
		BeforeEach(func() {
			policiesCache = populateCache()
		})

		It("should return true for admin network policy", func() {
			policyData := policiesCache.GetPolicy(model.ResourceKey{
				Name:      "kanp.adminnetworkpolicy.test",
				Namespace: "",
				Kind:      "GlobalNetworkPolicy",
			})

			isKubPolicy, err := policyData.IsKubernetesType()
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isKubPolicy).To(BeTrue())
		})

		It("should return true for baseline admin network policy", func() {
			policyData := policiesCache.GetPolicy(model.ResourceKey{
				Name:      "kbanp.baselineadminnetworkpolicy.test",
				Namespace: "",
				Kind:      "GlobalNetworkPolicy",
			})

			isKubPolicy, err := policyData.IsKubernetesType()
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isKubPolicy).To(BeTrue())
		})

		It("should return true for kubernetes network policy", func() {
			policyData := policiesCache.GetPolicy(model.ResourceKey{
				Name:      "knp.default.test",
				Namespace: "default",
				Kind:      "NetworkPolicy",
			})

			isKubPolicy, err := policyData.IsKubernetesType()
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isKubPolicy).To(BeTrue())
		})

		It("should return false for calico policy", func() {
			policyData := policiesCache.GetPolicy(model.ResourceKey{
				Name:      "default.test",
				Namespace: "default",
				Kind:      "NetworkPolicy",
			})

			isKubPolicy, err := policyData.IsKubernetesType()
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isKubPolicy).To(BeFalse())
		})

		It("should return false for global calico policy", func() {
			policyData := policiesCache.GetPolicy(model.ResourceKey{
				Name:      "default.test",
				Namespace: "default",
				Kind:      "GlobalNetworkPolicy",
			})

			isKubPolicy, err := policyData.IsKubernetesType()
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isKubPolicy).To(BeFalse())
		})

		It("should return error for unknown policy", func() {
			policyData := policiesCache.GetPolicy(model.ResourceKey{
				Name:      "abc.default.test",
				Namespace: "default",
				Kind:      "NetworkPolicy",
			})

			_, err := policyData.IsKubernetesType()
			Expect(err).Should(HaveOccurred())
		})
	})
})

func populateCache() cache.PoliciesCache {
	policiesCache := cache.NewPoliciesCache()
	gnpConverter := dispatcherv1v3.NewConverterFromSyncerUpdateProcessor(
		updateprocessors.NewGlobalNetworkPolicyUpdateProcessor(),
	)

	npConverter := dispatcherv1v3.NewConverterFromSyncerUpdateProcessor(
		updateprocessors.NewNetworkPolicyUpdateProcessor(),
	)
	sgnpConverter := dispatcherv1v3.NewConverterFromSyncerUpdateProcessor(
		updateprocessors.NewStagedGlobalNetworkPolicyUpdateProcessor(),
	)
	snpConverter := dispatcherv1v3.NewConverterFromSyncerUpdateProcessor(
		updateprocessors.NewStagedNetworkPolicyUpdateProcessor(),
	)
	sk8snpConverter := dispatcherv1v3.NewConverterFromSyncerUpdateProcessor(
		updateprocessors.NewStagedKubernetesNetworkPolicyUpdateProcessor(),
	)
	tierConverter := dispatcherv1v3.NewConverterFromSyncerUpdateProcessor(
		updateprocessors.NewTierUpdateProcessor(),
	)

	dispatcherTypes := []dispatcherv1v3.Resource{
		{
			// We need to convert the GNP for use with the policy sorter, and to get the
			// correct selectors for the labelhandler.
			Kind:      apiv3.KindGlobalNetworkPolicy,
			Converter: gnpConverter,
		},
		{
			Kind:      model.KindKubernetesAdminNetworkPolicy,
			Converter: gnpConverter,
		},
		{
			Kind:      model.KindKubernetesBaselineAdminNetworkPolicy,
			Converter: gnpConverter,
		},
		{
			// Convert the KubernetesNetworkPolicy to NP.
			Kind:      model.KindKubernetesNetworkPolicy,
			Converter: npConverter,
		},
		{
			// We need to convert the NP for use with the policy sorter, and to get the
			// correct selectors for the labelhandler.
			Kind:      apiv3.KindNetworkPolicy,
			Converter: npConverter,
		},
		{
			// Convert the SGNP to GNP
			Kind:      apiv3.KindStagedGlobalNetworkPolicy,
			Converter: sgnpConverter,
		},
		{
			// Convert the SNP to NP
			Kind:      apiv3.KindStagedNetworkPolicy,
			Converter: snpConverter,
		},
		{
			// Convert the SK8SNP to NP
			Kind:      apiv3.KindStagedKubernetesNetworkPolicy,
			Converter: sk8snpConverter,
		},
		{
			Kind:      apiv3.KindTier,
			Converter: tierConverter,
		},
	}

	dispatcher := dispatcherv1v3.New(dispatcherTypes)
	policiesCache.RegisterWithDispatcher(dispatcher)

	//
	newKANPUpdate := api.Update{
		KVPair: model.KVPair{
			Key: model.ResourceKey{
				Name:      "kanp.adminnetworkpolicy.test",
				Namespace: "",
				Kind:      "GlobalNetworkPolicy",
			},
			Value: &v3.GlobalNetworkPolicy{
				TypeMeta: v1.TypeMeta{
					Kind:       "GlobalNetworkPolicy",
					APIVersion: "projectcalico.org/v3",
				},
				ObjectMeta: v1.ObjectMeta{

					Name: "kanp.adminnetworkpolicy.test",
					UID:  "e398dea3-328b-48ca-b152-1efcafaccc24",
				},
				Spec: v3.GlobalNetworkPolicySpec{
					Tier: "adminnetworkpolicy",
					Egress: []v3.Rule{
						v3.Rule{
							Action: apiv3.Allow,
						},
					},
					Selector: "projectcalico.org/orchestrator == 'k8s'",
				},
			},
		},
		UpdateType: api.UpdateTypeKVNew,
	}

	newKBANPUpdate := api.Update{
		KVPair: model.KVPair{
			Key: model.ResourceKey{
				Name:      "kbanp.baselineadminnetworkpolicy.test",
				Namespace: "",
				Kind:      "GlobalNetworkPolicy",
			},
			Value: &v3.GlobalNetworkPolicy{
				TypeMeta: v1.TypeMeta{
					Kind:       "GlobalNetworkPolicy",
					APIVersion: "projectcalico.org/v3",
				},
				ObjectMeta: v1.ObjectMeta{

					Name: "kbanp.baselineadminnetworkpolicy.test",
					UID:  "e398dea3-328b-48ca-b152-1efcafaccc24",
				},
				Spec: v3.GlobalNetworkPolicySpec{
					Tier: "baselineadminnetworkpolicy",
					Egress: []v3.Rule{
						v3.Rule{
							Action: apiv3.Allow,
						},
					},
					Selector: "projectcalico.org/orchestrator == 'k8s'",
				},
			},
		},
		UpdateType: api.UpdateTypeKVNew,
	}

	newKNPUpdate := api.Update{
		KVPair: model.KVPair{
			Key: model.ResourceKey{
				Name:      "knp.default.test",
				Namespace: "default",
				Kind:      "NetworkPolicy",
			},
			Value: &v3.NetworkPolicy{
				TypeMeta: v1.TypeMeta{
					Kind:       "NetworkPolicy",
					APIVersion: "projectcalico.org/v3",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:      "knp.default.test",
					Namespace: "default",
					UID:       "62cb2a82-77ff-44ed-8aab-fabab0a3b521",
				},
				Spec: v3.NetworkPolicySpec{
					Tier:  "",
					Types: []v3.PolicyType{"Ingress"},
				},
			},
		},
		UpdateType: api.UpdateTypeKVNew,
	}

	newCalicoUpdate := api.Update{
		KVPair: model.KVPair{
			Key: model.ResourceKey{
				Name:      "default.test",
				Namespace: "default",
				Kind:      "NetworkPolicy",
			},
			Value: &v3.NetworkPolicy{
				TypeMeta: v1.TypeMeta{
					Kind:       "NetworkPolicy",
					APIVersion: "projectcalico.org/v3",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:      "default.test",
					Namespace: "default",
					UID:       "cd4f30f4-a06c-44b9-a4a9-72b41dbf4658",
				},
				Spec: v3.NetworkPolicySpec{
					Tier:     "default",
					Selector: "all()",
					Types:    []v3.PolicyType{"Ingress"},
				},
			},
		},
		UpdateType: api.UpdateTypeKVNew,
	}

	newGlobalCalicoUpdate := api.Update{
		KVPair: model.KVPair{
			Key: model.ResourceKey{
				Name:      "default.test",
				Namespace: "default",
				Kind:      "GlobalNetworkPolicy",
			},
			Value: &v3.GlobalNetworkPolicy{
				TypeMeta: v1.TypeMeta{
					Kind:       "GlobalNetworkPolicy",
					APIVersion: "projectcalico.org/v3",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:      "default.test",
					Namespace: "default",
					UID:       "cd4f30f4-a06c-44b9-a4a9-72b41dbf4658",
				},
				Spec: v3.GlobalNetworkPolicySpec{
					Tier:     "default",
					Selector: "all()",
					Types:    []v3.PolicyType{"Ingress"},
				},
			},
		},
		UpdateType: api.UpdateTypeKVNew,
	}

	newRandomUpdate := api.Update{
		KVPair: model.KVPair{
			Key: model.ResourceKey{
				Name:      "abc.default.test",
				Namespace: "default",
				Kind:      "NetworkPolicy",
			},
			Value: &v3.NetworkPolicy{
				TypeMeta: v1.TypeMeta{
					Kind:       "NetworkPolicy",
					APIVersion: "projectcalico.org/v3",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:      "abc.default.test",
					Namespace: "default",
					//UID:       "cd4f30f4-a06c-44b9-a4a9-72b41dbf4658",
				},
				Spec: v3.NetworkPolicySpec{
					Tier:     "default",
					Selector: "all()",
					Types:    []v3.PolicyType{"Ingress"},
				},
			},
		},
		UpdateType: api.UpdateTypeKVNew,
	}

	updates := []api.Update{
		newKANPUpdate,
		newKBANPUpdate,
		newKNPUpdate,
		newCalicoUpdate,
		newGlobalCalicoUpdate,
		newRandomUpdate,
	}

	dispatcher.OnUpdates(updates)

	return policiesCache
}
