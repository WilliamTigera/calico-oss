// Copyright (c) 2020 Tigera, Inc. All rights reserved.
package rbac_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	"github.com/stretchr/testify/mock"
	"github.com/tigera/lma/pkg/api"
	"github.com/tigera/lma/pkg/auth"
	"github.com/tigera/lma/pkg/rbac"

	authzv1 "k8s.io/api/authorization/v1"
	"k8s.io/apiserver/pkg/authentication/user"
)

var _ = Describe("FlowHelper tests", func() {
	var mockAuthorizer *auth.MockRBACAuthorizer
	BeforeEach(func() {
		mockAuthorizer = new(auth.MockRBACAuthorizer)
	})
	It("caches unauthorized results", func() {
		usr := &user.DefaultInfo{}
		rh := rbac.NewCachedFlowHelper(usr, mockAuthorizer)
		mockAuthorizer.On("Authorize", mock.Anything, mock.Anything, mock.Anything).Return(false, nil).Times(4)

		By("checking permissions requiring 4 lookups")
		Expect(rh.CanListHostEndpoints()).To(BeFalse())
		Expect(rh.CanListNetworkSets("ns1")).To(BeFalse())
		Expect(rh.CanListPods("ns1")).To(BeFalse())
		Expect(rh.CanListGlobalNetworkSets()).To(BeFalse())

		By("checking the same permissions with cached results")
		Expect(rh.CanListHostEndpoints()).To(BeFalse())
		Expect(rh.CanListNetworkSets("ns1")).To(BeFalse())
		Expect(rh.CanListPods("ns1")).To(BeFalse())
		Expect(rh.CanListGlobalNetworkSets()).To(BeFalse())

		mockAuthorizer.AssertExpectations(GinkgoT())
	})

	DescribeTable(
		"CanListPolicy with global network policies",
		func(expectedCan bool, expectedCalls func(mockAuthorizer *auth.MockRBACAuthorizer)) {
			ph, ok := api.PolicyHitFromFlowLogPolicyString("0|tier1|tier1.gnp|allow", 0)
			Expect(ok).Should(BeTrue())

			expectedCalls(mockAuthorizer)
			rh := rbac.NewCachedFlowHelper(&user.DefaultInfo{}, mockAuthorizer)
			Expect(rh.CanListPolicy(&ph)).To(Equal(expectedCan))
		},
		TableEntry{
			Description: "Returns false without get access to tiers",
			Parameters: []interface{}{
				false,
				func(mockAuthorizer *auth.MockRBACAuthorizer) {
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "get", Group: "projectcalico.org", Resource: "tiers", Name: "tier1"},
						mock.Anything).Return(false, nil)
				},
			},
		},
		TableEntry{
			Description: "Returns true with get access to tiers and list access to specific tier",
			Parameters: []interface{}{
				true,
				func(mockAuthorizer *auth.MockRBACAuthorizer) {
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "get", Group: "projectcalico.org", Resource: "tiers", Name: "tier1"},
						mock.Anything).Return(true, nil)
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "list", Group: "projectcalico.org", Resource: "tier.globalnetworkpolicies"},
						mock.Anything).Return(false, nil)
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "list", Group: "projectcalico.org", Resource: "tier.globalnetworkpolicies", Name: "tier1.*"},
						mock.Anything).Return(true, nil)
				},
			},
		},
		TableEntry{
			Description: "Returns true with get access to tiers and list access to tiers",
			Parameters: []interface{}{
				true,
				func(mockAuthorizer *auth.MockRBACAuthorizer) {
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "get", Group: "projectcalico.org", Resource: "tiers", Name: "tier1"},
						mock.Anything).Return(true, nil)
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "list", Group: "projectcalico.org", Resource: "tier.globalnetworkpolicies"},
						mock.Anything).Return(true, nil)
				},
			},
		},
	)

	DescribeTable(
		"CanListPolicy with staged global network policies",
		func(expectedCan bool, expectedCalls func(mockAuthorizer *auth.MockRBACAuthorizer)) {
			ph, ok := api.PolicyHitFromFlowLogPolicyString("0|tier1|staged:tier1.gnp|allow", 0)
			Expect(ok).Should(BeTrue())

			expectedCalls(mockAuthorizer)
			rh := rbac.NewCachedFlowHelper(&user.DefaultInfo{}, mockAuthorizer)
			Expect(rh.CanListPolicy(&ph)).To(Equal(expectedCan))
		},
		TableEntry{
			Description: "Returns false without get access to tiers",
			Parameters: []interface{}{
				false,
				func(mockAuthorizer *auth.MockRBACAuthorizer) {
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "get", Group: "projectcalico.org", Resource: "tiers", Name: "tier1"},
						mock.Anything).Return(false, nil)
				},
			},
		},
		TableEntry{
			Description: "Returns true with get access to tiers and list access to specific tier",
			Parameters: []interface{}{
				true,
				func(mockAuthorizer *auth.MockRBACAuthorizer) {
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "get", Group: "projectcalico.org", Resource: "tiers", Name: "tier1"},
						mock.Anything).Return(true, nil)
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "list", Group: "projectcalico.org", Resource: "tier.stagedglobalnetworkpolicies"},
						mock.Anything).Return(false, nil)
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "list", Group: "projectcalico.org", Resource: "tier.stagedglobalnetworkpolicies", Name: "tier1.*"},
						mock.Anything).Return(true, nil)
				},
			},
		},
		TableEntry{
			Description: "Returns true with get access to tiers and list access to tiers",
			Parameters: []interface{}{
				true,
				func(mockAuthorizer *auth.MockRBACAuthorizer) {
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "get", Group: "projectcalico.org", Resource: "tiers", Name: "tier1"},
						mock.Anything).Return(true, nil)
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "list", Group: "projectcalico.org", Resource: "tier.stagedglobalnetworkpolicies"},
						mock.Anything).Return(true, nil)
				},
			},
		},
	)

	DescribeTable(
		"CanListPolicy with network policies",
		func(expectedCan bool, expectedCalls func(mockAuthorizer *auth.MockRBACAuthorizer)) {
			ph, ok := api.PolicyHitFromFlowLogPolicyString("0|tier1|ns1/tier1.np|allow", 0)
			Expect(ok).Should(BeTrue())

			expectedCalls(mockAuthorizer)
			rh := rbac.NewCachedFlowHelper(&user.DefaultInfo{}, mockAuthorizer)
			Expect(rh.CanListPolicy(&ph)).To(Equal(expectedCan))
		},
		TableEntry{
			Description: "Returns false without get access to tiers",
			Parameters: []interface{}{
				false,
				func(mockAuthorizer *auth.MockRBACAuthorizer) {
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "get", Group: "projectcalico.org", Resource: "tiers", Name: "tier1"},
						mock.Anything).Return(false, nil)
				},
			},
		},
		TableEntry{
			Description: "Returns true with get access to tiers and list access to specific tier",
			Parameters: []interface{}{
				true,
				func(mockAuthorizer *auth.MockRBACAuthorizer) {
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "get", Group: "projectcalico.org", Resource: "tiers", Name: "tier1"},
						mock.Anything).Return(true, nil)
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Namespace: "ns1", Verb: "list", Group: "projectcalico.org", Resource: "tier.networkpolicies"},
						mock.Anything).Return(false, nil)
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Namespace: "ns1", Verb: "list", Group: "projectcalico.org", Resource: "tier.networkpolicies", Name: "tier1.*"},
						mock.Anything).Return(true, nil)
				},
			},
		},
		TableEntry{
			Description: "Returns true with get access to tiers and list access to tiers",
			Parameters: []interface{}{
				true,
				func(mockAuthorizer *auth.MockRBACAuthorizer) {
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Verb: "get", Group: "projectcalico.org", Resource: "tiers", Name: "tier1"},
						mock.Anything).Return(true, nil)
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Namespace: "ns1", Verb: "list", Group: "projectcalico.org", Resource: "tier.networkpolicies"},
						mock.Anything).Return(true, nil)
				},
			},
		},
	)

	DescribeTable(
		"CanListPolicy with kubernetes network policies",
		func(expectedCan bool, expectedCalls func(mockAuthorizer *auth.MockRBACAuthorizer)) {
			ph, ok := api.PolicyHitFromFlowLogPolicyString("0|default|ns1/knp.default.np|allow", 0)
			Expect(ok).Should(BeTrue())

			expectedCalls(mockAuthorizer)
			rh := rbac.NewCachedFlowHelper(&user.DefaultInfo{}, mockAuthorizer)
			Expect(rh.CanListPolicy(&ph)).To(Equal(expectedCan))
		},
		TableEntry{
			Description: "Returns false without get access to tiers",
			Parameters: []interface{}{
				false,
				func(mockAuthorizer *auth.MockRBACAuthorizer) {
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Namespace: "ns1", Verb: "list", Group: "networking.k8s.io", Resource: "networkpolicies"},
						mock.Anything).Return(false, nil)
				},
			},
		},
		TableEntry{
			Description: "Returns true with get access to tiers and list access to specific tier",
			Parameters: []interface{}{
				true,
				func(mockAuthorizer *auth.MockRBACAuthorizer) {
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Namespace: "ns1", Verb: "list", Group: "networking.k8s.io", Resource: "networkpolicies"},
						mock.Anything).Return(true, nil)
				},
			},
		},
	)

	DescribeTable(
		"CanListPolicy with staged kubernetes network policies",
		func(expectedCan bool, expectedCalls func(mockAuthorizer *auth.MockRBACAuthorizer)) {
			ph, ok := api.PolicyHitFromFlowLogPolicyString("0|default|ns1/staged:knp.default.np|allow", 0)
			Expect(ok).Should(BeTrue())

			expectedCalls(mockAuthorizer)
			rh := rbac.NewCachedFlowHelper(&user.DefaultInfo{}, mockAuthorizer)
			Expect(rh.CanListPolicy(&ph)).To(Equal(expectedCan))
		},
		TableEntry{
			Description: "Returns false without get access to tiers",
			Parameters: []interface{}{
				false,
				func(mockAuthorizer *auth.MockRBACAuthorizer) {
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Namespace: "ns1", Verb: "list", Group: "projectcalico.org", Resource: "stagedkubernetesnetworkpolicies"},
						mock.Anything).Return(false, nil)
				},
			},
		},
		TableEntry{
			Description: "Returns true with get access to tiers and list access to specific tier",
			Parameters: []interface{}{
				true,
				func(mockAuthorizer *auth.MockRBACAuthorizer) {
					mockAuthorizer.On("Authorize", mock.Anything,
						&authzv1.ResourceAttributes{Namespace: "ns1", Verb: "list", Group: "projectcalico.org", Resource: "stagedkubernetesnetworkpolicies"},
						mock.Anything).Return(true, nil)
				},
			},
		},
	)
})
