// Copyright (c) 2019, 2022 Tigera, Inc. All rights reserved.
package policyrec_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/calico/lma/pkg/api"
	"github.com/projectcalico/calico/lma/pkg/policyrec"
)

const defaultTier = "default"

// flowWithError is a convenience type for passing in a flow along
// with whether it is processed successfully or not.
type flowWithError struct {
	flow        api.Flow
	shouldError bool
}

var _ = Describe("Policy Recommendation Engine", func() {
	var (
		re   policyrec.RecommendationEngine
		err  error
		kube *fake.Clientset
	)
	DescribeTable("Recommend policies for matching flows and endpoint",
		// endpointName and endpointNamespace are params for which recommended policies should be generated for.
		// They are used to configure the recommendation engine.
		// matchingFlows is the input flows that are passed to ProcessFlows.
		// expectedPolicies is a slice of StagedNetworkPolicy or StagedGlobalNetworkPolicy.
		func(
			kube kubernetes.Interface,
			endpointName, endpointNamespace, policyTier string, policyOrder *float64,
			matchingFlows []flowWithError, expectedPolicies interface{}) {

			By("Initializing a recommendation engine with namespace and name")
			re = policyrec.NewEndpointRecommendationEngine(kube, endpointName, endpointNamespace, policyTier, policyOrder)

			for _, flow := range matchingFlows {
				By("Processing matching flow")
				err = re.ProcessFlow(flow.flow)
				if flow.shouldError {
					Expect(err).ToNot(BeNil())
				} else {
					Expect(err).To(BeNil())
				}
			}

			By("Once all matched flows have been input for matching endpoint and getting recommended flows")
			recommendation, err := re.Recommend()
			Expect(err).To(BeNil())
			if policyrec.IsEmptyNamespace(endpointName) && !policyrec.IsEmptyNamespace(endpointNamespace) {
				// Expect only StagedGlobalNetworkPolicies.
				policies := expectedPolicies.([]*v3.StagedNetworkPolicy)
				// We loop through each expected policy and check instead of using ConsistsOf() matcher so that
				// we can use our custom MatchPolicy() Gomega matcher.
				for _, expectedPolicy := range policies {
					Expect(recommendation.NetworkPolicies).To(ContainElement(policyrec.MatchPolicy(expectedPolicy)))
				}
			} else if policyrec.IsEmptyNamespace(endpointNamespace) {
				// Expect only StagedGlobalNetworkPolicies.
				policies := expectedPolicies.([]*v3.StagedGlobalNetworkPolicy)
				// We loop through each expected policy and check instead of using ConsistsOf() matcher so that
				// we can use our custom MatchPolicy() Gomega matcher.
				for _, expectedPolicy := range policies {
					Expect(recommendation.GlobalNetworkPolicies).To(ContainElement(policyrec.MatchPolicy(expectedPolicy)))
				}
			} else {
				// Expect only StagedNetworkPolicies if a namespace is defined.
				policies := expectedPolicies.([]*v3.StagedNetworkPolicy)
				// We loop through each expected policy and check instead of using ConsistsOf() matcher so that
				// we can use our custom MatchPolicy() Gomega matcher.
				for _, expectedPolicy := range policies {
					Expect(recommendation.NetworkPolicies).To(ContainElement(policyrec.MatchPolicy(expectedPolicy)))
				}
			}
		},
		Entry("recommend a policy with egress rule for a flow between 2 endpoints and matching source endpoint",
			fake.NewSimpleClientset(), pod1Aggr, namespace1, defaultTier, nil,
			[]flowWithError{
				{flowPod1BlueToPod2Allow443ReporterSource, false},
				{flowPod1BlueToPod2Allow443ReporterDestination, true},
			},
			[]*v3.StagedNetworkPolicy{networkPolicyNamespace1Pod1BlueToPod2}),
		Entry("recommend a policy with egress rule for a flow between 2 endpoints with a non overlapping label - and matching source endpoint",
			fake.NewSimpleClientset(), pod1Aggr, namespace1, defaultTier, nil,
			[]flowWithError{
				{flowPod1BlueToPod2Allow443ReporterSource, false},
				{flowPod1BlueToPod2Allow443ReporterDestination, true},
				{flowPod1RedToPod2Allow443ReporterSource, false},
				{flowPod1RedToPod2Allow443ReporterDestination, true},
			},
			[]*v3.StagedNetworkPolicy{egressNetworkPolicyNamespace1Pod1ToPod2}),
		Entry("recommend a policy with ingress rule for a flow between 2 endpoints with a non overlapping label - and matching source endpoint",
			fake.NewSimpleClientset(), pod2Aggr, namespace1, defaultTier, nil,
			[]flowWithError{
				{flowPod1BlueToPod2Allow443ReporterSource, true},
				{flowPod1BlueToPod2Allow443ReporterDestination, false},
				{flowPod1RedToPod2Allow443ReporterSource, true},
				{flowPod1RedToPod2Allow443ReporterDestination, false},
			},
			[]*v3.StagedNetworkPolicy{ingressNetworkPolicyNamespace1Pod1ToPod2}),
		Entry("recommend a policy with egress rule for a flow between 2 endpoints and external network and matching source endpoint",
			fake.NewSimpleClientset(), pod1Aggr, namespace1, defaultTier, nil,
			[]flowWithError{
				{flowPod1BlueToPod2Allow443ReporterSource, false},
				{flowPod1BlueToPod2Allow443ReporterDestination, true},
				{flowPod1BlueToExternalAllow53ReporterSource, false},
			},
			[]*v3.StagedNetworkPolicy{networkPolicyNamespace1Pod1BlueToPod2AndExternalNet}),
		Entry("recommend a policy with egress rule for a flow between 2 endpoints and matching source endpoint",
			fake.NewSimpleClientset(), pod1Aggr, namespace1, defaultTier, nil,
			[]flowWithError{
				{flowPod1BlueToPod2Allow443ReporterSource, false},
				{flowPod1BlueToPod2Allow443ReporterDestination, true},
				{flowPod1BlueToPod3Allow5432ReporterSource, false},
				{flowPod1BlueToPod3Allow5432ReporterDestination, true},
				{flowPod1RedToPod3Allow8080ReporterSource, false},
				{flowPod1RedToPod3Allow8080ReporterDestination, true},
			},
			[]*v3.StagedNetworkPolicy{networkPolicyNamespace1Pod1ToPod2AndPod3}),
		Entry("recommend a policy with ingress and egress rules for a flow between 2 endpoints and matching source and destination endpoint",
			fake.NewSimpleClientset(), pod2Aggr, namespace1, defaultTier, nil,
			[]flowWithError{
				{flowPod1BlueToPod2Allow443ReporterSource, true},
				{flowPod1BlueToPod2Allow443ReporterDestination, false},
				{flowPod2ToPod3Allow5432ReporterSource, false},
				{flowPod2ToPod3Allow5432ReporterDestination, true},
			},
			[]*v3.StagedNetworkPolicy{networkPolicyNamespace1Pod2}),
		Entry("recommend a policy with ingress rule for flows and matching destination endpoint",
			fake.NewSimpleClientset(), pod3Aggr, namespace2, defaultTier, nil,
			[]flowWithError{
				{flowPod1BlueToPod3Allow5432ReporterSource, true},
				{flowPod1BlueToPod3Allow5432ReporterDestination, false},
				{flowPod1RedToPod3Allow8080ReporterSource, true},
				{flowPod1RedToPod3Allow8080ReporterDestination, false},
				{flowPod2ToPod3Allow5432ReporterSource, true},
				{flowPod2ToPod3Allow5432ReporterDestination, false},
				{flowGlobalNetworkSet1ToPod3Allow5432ReporterDestination, false},
			},
			[]*v3.StagedNetworkPolicy{networkPolicyNamespace1Pod3}),
		Entry("recommend a policy with ingress rule for a flow between 3 endpoints with no intersecting label - and matching destination endpoint",
			fake.NewSimpleClientset(), pod3Aggr, namespace2, defaultTier, nil,
			[]flowWithError{
				{flowPod4Rs1ToPod3Allow5432ReporterDestination, false},
				{flowPod4Rs2ToPod3Allow5432ReporterDestination, false},
			},
			[]*v3.StagedNetworkPolicy{ingressNetworkPolicyToNamespace2Pod3FromPod4Port5432}),
		Entry("recommend a policy with ingress rule for a flow between 3 endpoints with no intersecting label - and matching destination endpoint and 2 ports",
			fake.NewSimpleClientset(), pod3Aggr, namespace2, defaultTier, nil,
			[]flowWithError{
				{flowPod4Rs1ToPod3Allow5432ReporterDestination, false},
				{flowPod4Rs2ToPod3Allow5432ReporterDestination, false},
				{flowPod4Rs1ToPod3Allow8080ReporterDestination, false},
				{flowPod4Rs2ToPod3Allow8080ReporterDestination, false},
			},
			[]*v3.StagedNetworkPolicy{ingressNetworkPolicyToNamespace2Pod3FromPod4Port5432And8080}),
		Entry("recommend a namespace policy with egress rule for a flow between 2 endpoints and matching source endpoint",
			fake.NewSimpleClientset(namespace1Object, namespace2Object), emptyEndpoint, namespace1, defaultTier, nil,
			[]flowWithError{
				{flowPod1BlueToPod2Allow443ReporterSource, false},
				{flowPod1BlueToPod2Allow443ReporterDestination, false},
			},
			[]*v3.StagedNetworkPolicy{namespaceNetworkPolicyNamespace1Pod1BlueToPod2}),
		Entry("recommend a policy namespace with egress rule for a flow between 2 endpoints with a non overlapping label - and matching source endpoint",
			fake.NewSimpleClientset(namespace1Object, namespace2Object), emptyEndpoint, namespace1, defaultTier, nil,
			[]flowWithError{
				{flowPod1BlueToPod2Allow443ReporterSource, false},
				{flowPod1BlueToPod2Allow443ReporterDestination, false},
				{flowPod1RedToPod2Allow443ReporterSource, false},
				{flowPod1RedToPod2Allow443ReporterDestination, false},
			},
			[]*v3.StagedNetworkPolicy{namespaceEgressNetworkPolicyNamespace1Pod1ToPod2}),
		Entry("recommend a policy namespace with egress rule for a flow between 2 endpoints and external network and matching source endpoint",
			fake.NewSimpleClientset(namespace1Object, namespace2Object), emptyEndpoint, namespace1, defaultTier, nil,
			[]flowWithError{
				{flowPod1BlueToPod2Allow443ReporterSource, false},
				{flowPod1BlueToPod2Allow443ReporterDestination, false},
				{flowPod1BlueToExternalAllow53ReporterSource, false},
			},
			[]*v3.StagedNetworkPolicy{namespaceNetworkPolicyNamespace1Pod1BlueToPod2AndExternalNet}),
		Entry("recommend a policy namespace with egress rule for a flow between 2 endpoints and matching source endpoint",
			fake.NewSimpleClientset(namespace1Object, namespace2Object), emptyEndpoint, namespace1, defaultTier, nil,
			[]flowWithError{
				{flowPod1BlueToPod2Allow443ReporterSource, false},
				{flowPod1BlueToPod2Allow443ReporterDestination, false},
				{flowPod1BlueToPod3Allow5432ReporterSource, false},
				{flowPod1BlueToPod3Allow5432ReporterDestination, true},
				{flowPod1RedToPod3Allow8080ReporterSource, false},
				{flowPod1RedToPod3Allow8080ReporterDestination, true},
			},
			[]*v3.StagedNetworkPolicy{namespaceNetworkPolicyNamespace1Pod1ToPod2AndPod3}),
		Entry("recommend a policy namespace with ingress and egress rules for a flow between 2 endpoints and matching source and destination endpoint",
			fake.NewSimpleClientset(namespace1Object, namespace2Object), emptyEndpoint, namespace1, defaultTier, nil,
			[]flowWithError{
				{flowPod1BlueToPod2Allow443ReporterSource, false},
				{flowPod1BlueToPod2Allow443ReporterDestination, false},
				{flowPod2ToPod3Allow5432ReporterSource, false},
				{flowPod2ToPod3Allow5432ReporterDestination, true},
			},
			[]*v3.StagedNetworkPolicy{namespaceNetworkPolicyNamespace1Pod2}),
		Entry("recommend a policy namespace with ingress rule for flows and matching destination endpoint",
			fake.NewSimpleClientset(namespace1Object, namespace2Object), emptyEndpoint, namespace2, defaultTier, nil,
			[]flowWithError{
				{flowPod1BlueToPod3Allow5432ReporterSource, true},
				{flowPod1BlueToPod3Allow5432ReporterDestination, false},
				{flowPod1RedToPod3Allow8080ReporterSource, true},
				{flowPod1RedToPod3Allow8080ReporterDestination, false},
				{flowPod2ToPod3Allow5432ReporterSource, true},
				{flowPod2ToPod3Allow5432ReporterDestination, false},
				{flowGlobalNetworkSet1ToPod3Allow5432ReporterDestination, false},
			},
			[]*v3.StagedNetworkPolicy{namespaceNetworkPolicyNamespace1Pod3}),
		Entry("recommend a policy namespace with ingress rule for a flow between 3 endpoints with no intersecting label - and matching destination endpoint",
			fake.NewSimpleClientset(namespace1Object, namespace2Object), emptyEndpoint, namespace2, defaultTier, nil,
			[]flowWithError{
				{flowPod4Rs1ToPod3Allow5432ReporterDestination, false},
				{flowPod4Rs2ToPod3Allow5432ReporterDestination, false},
			},
			[]*v3.StagedNetworkPolicy{namespaceIngressNetworkPolicyToNamespace2Pod3FromPod4Port5432}),
		Entry("recommend a namespace policy with ingress rule for a flow between 3 endpoints with no intersecting label - and matching destination endpoint and 2 ports",
			fake.NewSimpleClientset(namespace1Object, namespace2Object), emptyEndpoint, namespace2, defaultTier, nil,
			[]flowWithError{
				{flowPod4Rs1ToPod3Allow5432ReporterDestination, false},
				{flowPod4Rs2ToPod3Allow5432ReporterDestination, false},
				{flowPod4Rs1ToPod3Allow8080ReporterDestination, false},
				{flowPod4Rs2ToPod3Allow8080ReporterDestination, false},
			},
			[]*v3.StagedNetworkPolicy{namespaceIngressNetworkPolicyToNamespace2Pod3FromPod4Port5432And8080}),
	)
	It("should reject flows that don't match endpoint name and namespace", func() {
		// Define the kubernetes interface
		kube = fake.NewSimpleClientset()

		By("Initializing a recommendation engine with namespace and name")
		re = policyrec.NewEndpointRecommendationEngine(kube, pod1Aggr, namespace1, defaultTier, nil)

		By("Processing flow that don't match")
		err = re.ProcessFlow(flowPod2ToPod3Allow5432ReporterSource)
		Expect(err).ToNot(BeNil())
		err = re.ProcessFlow(flowPod2ToPod3Allow5432ReporterDestination)
		Expect(err).ToNot(BeNil())
	})
	It("should reject flows that are for endpoint type that isn't wep", func() {
		// Define the kubernetes interface
		kube = fake.NewSimpleClientset()

		By("Initializing a recommendation engine with namespace and name")
		re = policyrec.NewEndpointRecommendationEngine(kube, ns1Aggr, namespace1, defaultTier, nil)

		By("Processing flow that don't match")
		err = re.ProcessFlow(flowPod2ToNs1Allow80ReporterSource)
		Expect(err).ToNot(BeNil())
	})
	It("should not produce any policies for flows that match endpoint name and namespace but not direction reported", func() {
		// Define the kubernetes interface
		kube = fake.NewSimpleClientset()

		By("Initializing a recommendation engine with namespace and name")
		re = policyrec.NewEndpointRecommendationEngine(kube, pod2Aggr, namespace1, defaultTier, nil)

		By("Processing flow that don't match")
		err = re.ProcessFlow(flowPod1BlueToPod2Allow443ReporterSource)
		Expect(err).ToNot(BeNil())
		err = re.ProcessFlow(flowPod1RedToPod2Allow443ReporterSource)
		Expect(err).ToNot(BeNil())

		_, err = re.Recommend()
		Expect(err).ToNot(BeNil())
	})
	It("should not produce any policies for flows that match endpoint name and namespace but not direction reported", func() {
		// Define the kubernetes interface
		kube = fake.NewSimpleClientset(namespace1Object, namespace2Object)

		By("Initializing a recommendation engine with namespace and name")
		re = policyrec.NewEndpointRecommendationEngine(kube, pod2Aggr, namespace1, defaultTier, nil)

		By("Processing flow that don't match")
		err = re.ProcessFlow(flowPod1BlueToPod2Allow443ReporterSource)
		Expect(err).ToNot(BeNil())
		err = re.ProcessFlow(flowPod1RedToPod2Allow443ReporterSource)
		Expect(err).ToNot(BeNil())

		_, err = re.Recommend()
		Expect(err).ToNot(BeNil())
	})
})
