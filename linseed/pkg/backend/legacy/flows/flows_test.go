// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package flows_test

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/olivere/elastic/v7"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/projectcalico/calico/libcalico-go/lib/json"
	"github.com/projectcalico/calico/libcalico-go/lib/logutils"
	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/flows"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/index"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/templates"
	backendutils "github.com/projectcalico/calico/linseed/pkg/backend/testutils"
	"github.com/projectcalico/calico/linseed/pkg/config"
	"github.com/projectcalico/calico/linseed/pkg/testutils"
	lmav1 "github.com/projectcalico/calico/lma/pkg/apis/v1"
	lmaelastic "github.com/projectcalico/calico/lma/pkg/elastic"
)

var (
	client      lmaelastic.Client
	cache       bapi.IndexInitializer
	fb          bapi.FlowBackend
	flb         bapi.FlowLogBackend
	ctx         context.Context
	cluster     string
	indexGetter bapi.Index
)

// RunAllModes runs the given test function twice, once using the single-index backend, and once using
// the multi-index backend.
func RunAllModes(t *testing.T, name string, testFn func(t *testing.T)) {
	// Run using the multi-index backend.
	t.Run(fmt.Sprintf("%s [legacy]", name), func(t *testing.T) {
		defer setupTest(t, false)()
		testFn(t)
	})

	// Run using the single-index backend.
	t.Run(fmt.Sprintf("%s [singleindex]", name), func(t *testing.T) {
		defer setupTest(t, true)()
		testFn(t)
	})
}

func setupTest(t *testing.T, singleIndex bool) func() {
	// Hook logrus into testing.T
	config.ConfigureLogging("DEBUG")
	logCancel := logutils.RedirectLogrusToTestingT(t)

	// Create an elasticsearch client to use for the test. For this suite, we use a real
	// elasticsearch instance created via "make run-elastic".
	esClient, err := elastic.NewSimpleClient(elastic.SetURL("http://localhost:9200"), elastic.SetInfoLog(logrus.StandardLogger()))
	require.NoError(t, err)
	client = lmaelastic.NewWithClient(esClient)
	cache = templates.NewCachedInitializer(client, 1, 0)

	// Create backends to use.
	if singleIndex {
		indexGetter = index.FlowLogIndex()
		fb = flows.NewSingleIndexFlowBackend(client)
		flb = flows.NewSingleIndexFlowLogBackend(client, cache, 10000)
	} else {
		fb = flows.NewFlowBackend(client)
		flb = flows.NewFlowLogBackend(client, cache, 10000)
		indexGetter = index.FlowLogMultiIndex
	}

	// Create a random cluster name for each test to make sure we don't
	// interfere between tests.
	cluster = backendutils.RandomClusterName()

	// Set a timeout for each test.
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)

	// Function contains teardown logic.
	return func() {
		// Cancel the context.
		cancel()

		// Cleanup any data that might left over from a previous run.
		err = backendutils.CleanupIndices(context.Background(), esClient, singleIndex, indexGetter, bapi.ClusterInfo{Cluster: cluster})
		require.NoError(t, err)

		// Cancel logging
		logCancel()
	}
}

// TestListFlows tests running a real elasticsearch query to list flows.
func TestListFlows(t *testing.T) {
	RunAllModes(t, "TestListFlows", func(t *testing.T) {
		clusterInfo := bapi.ClusterInfo{Cluster: cluster}

		// Put some data into ES so we can query it.
		bld := backendutils.NewFlowLogBuilder()
		bld.WithType("wep").
			WithSourceNamespace("default").
			WithDestNamespace("kube-system").
			WithDestName("kube-dns-*").
			WithDestIP("10.0.0.10").
			WithDestService("kube-dns", 53).
			WithDestPort(53).
			WithProtocol("udp").
			WithSourceName("my-deployment").
			WithSourceIP("192.168.1.1").
			WithRandomFlowStats().WithRandomPacketStats().
			WithReporter("src").WithAction("allowed").
			WithSourceLabels("bread=rye", "cheese=brie", "wine=none").
			WithPolicies("0|allow-tigera|tigera-system/allow-tigera.cnx-apiserver-access|allow|1").
			WithProcessName("/usr/bin/curl")
		expected := populateFlowData(t, ctx, bld, client, clusterInfo)

		// Set time range so that we capture all of the populated flow logs.
		opts := v1.L3FlowParams{}
		opts.TimeRange = &lmav1.TimeRange{}
		opts.TimeRange.From = time.Now().Add(-5 * time.Minute)
		opts.TimeRange.To = time.Now().Add(5 * time.Minute)

		// Query for flows. There should be a single flow from the populated data.
		r, err := fb.List(ctx, clusterInfo, &opts)
		require.NoError(t, err)
		require.Len(t, r.Items, 1)
		require.Nil(t, r.AfterKey)

		// Assert that the flow data is populated correctly.
		require.Equal(t, expected, r.Items[0])
	})
}

// TestMultipleFlows tests that we return multiple flows properly.
func TestMultipleFlows(t *testing.T) {
	RunAllModes(t, "TestMultipleFlows", func(t *testing.T) {
		// Both flows use the same cluster information.
		clusterInfo := bapi.ClusterInfo{Cluster: cluster}

		// Template for flow #1.
		bld := backendutils.NewFlowLogBuilder()
		bld.WithType("wep").
			WithSourceNamespace("tigera-operator").
			WithDestNamespace("kube-system").
			WithDestName("kube-dns-*").
			WithDestIP("10.0.0.10").
			WithDestService("kube-dns", 53).
			WithDestPort(53).
			WithProtocol("udp").
			WithSourceName("tigera-operator").
			WithSourceIP("34.15.66.3").
			WithRandomFlowStats().WithRandomPacketStats().
			WithReporter("src").WithAction("allowed").
			WithSourceLabels("bread=rye", "cheese=brie", "wine=none")
		exp1 := populateFlowData(t, ctx, bld, client, clusterInfo)

		// Template for flow #2.
		bld2 := backendutils.NewFlowLogBuilder()
		bld2.WithType("wep").
			WithSourceNamespace("default").
			WithDestNamespace("kube-system").
			WithDestName("kube-dns-*").
			WithDestIP("10.0.0.10").
			WithDestService("kube-dns", 53).
			WithDestPort(53).
			WithProtocol("udp").
			WithSourceName("my-deployment").
			WithSourceIP("192.168.1.1").
			WithRandomFlowStats().WithRandomPacketStats().
			WithReporter("src").WithAction("allowed").
			WithSourceLabels("bread=rye", "cheese=brie", "wine=none")
		exp2 := populateFlowData(t, ctx, bld2, client, clusterInfo)

		// Set time range so that we capture all of the populated flow logs.
		opts := v1.L3FlowParams{}
		opts.TimeRange = &lmav1.TimeRange{}
		opts.TimeRange.From = time.Now().Add(-5 * time.Minute)
		opts.TimeRange.To = time.Now().Add(5 * time.Minute)

		// Query for flows. There should be two flows from the populated data.
		r, err := fb.List(ctx, clusterInfo, &opts)
		require.NoError(t, err)
		require.Len(t, r.Items, 2)
		require.Nil(t, r.AfterKey)

		// Assert that the flow data is populated correctly.
		require.Equal(t, exp1, r.Items[1])
		require.Equal(t, exp2, r.Items[0])
	})
}

// TestSourceIPAndDestIPFlows tests that we return multiple flows properly with source IP and
// destination IP
func TestSourceIPAndDestIPFlows(t *testing.T) {
	RunAllModes(t, "TestMultipleFlows", func(t *testing.T) {
		// Both flows use the same cluster information.
		clusterInfo := bapi.ClusterInfo{Cluster: cluster}

		// Flow logs batch #1.
		bld := backendutils.NewFlowLogBuilder()
		bld.WithType("wep").
			WithSourceNamespace("tigera-operator").
			WithDestNamespace("kube-system").
			WithDestName("kube-dns-*").
			WithDestIP("10.0.0.10").
			WithDestService("kube-dns", 53).
			WithDestPort(53).
			WithProtocol("udp").
			WithSourceName("tigera-operator").
			WithSourceIP("34.15.66.3").
			WithRandomFlowStats().WithRandomPacketStats().
			WithReporter("src").WithAction("allowed").
			WithSourceLabels("bread=rye", "cheese=brie", "wine=none")
		_ = populateFlowData(t, ctx, bld, client, clusterInfo)

		// Flow logs batch #2.
		bld.WithType("wep").
			WithSourceNamespace("tigera-operator").
			WithDestNamespace("kube-system").
			WithDestName("kube-dns-*").
			WithDestIP("10.0.0.10").
			WithDestService("kube-dns", 53).
			WithDestPort(53).
			WithProtocol("udp").
			WithSourceName("tigera-operator").
			WithSourceIP("192.168.66.3").
			WithRandomFlowStats().WithRandomPacketStats().
			WithReporter("src").WithAction("allowed").
			WithSourceLabels("bread=rye", "cheese=brie", "wine=none")
		_ = populateFlowData(t, ctx, bld, client, clusterInfo)

		// Flow logs batch #3.
		bld.WithType("wep").
			WithSourceNamespace("tigera-operator").
			WithDestNamespace("kube-system").
			WithDestName("kube-dns-*").
			WithDestIP("10.0.0.9").
			WithDestService("kube-dns", 53).
			WithDestPort(53).
			WithProtocol("udp").
			WithSourceName("tigera-operator").
			WithSourceIP("192.168.66.3").
			WithRandomFlowStats().WithRandomPacketStats().
			WithReporter("src").WithAction("allowed").
			WithSourceLabels("bread=rye", "cheese=brie", "wine=none")
		_ = populateFlowData(t, ctx, bld, client, clusterInfo)

		// Build a flow log based on the 3 batches
		exp1 := bld.ExpectedFlow(t)

		// Template for flow logs batch #4.
		bld2 := backendutils.NewFlowLogBuilder()
		bld2.WithType("wep").
			WithSourceNamespace("default").
			WithDestNamespace("kube-system").
			WithDestName("kube-dns-*").
			WithDestIP("10.0.0.10").
			WithDestService("kube-dns", 53).
			WithDestPort(53).
			WithProtocol("udp").
			WithSourceName("my-deployment").
			WithSourceIP("192.168.1.1").
			WithRandomFlowStats().WithRandomPacketStats().
			WithReporter("src").WithAction("allowed").
			WithSourceLabels("bread=rye", "cheese=brie", "wine=none")
		exp2 := populateFlowData(t, ctx, bld2, client, clusterInfo)

		// Set time range so that we capture all the populated flow logs.
		opts := v1.L3FlowParams{}
		opts.TimeRange = &lmav1.TimeRange{}
		opts.TimeRange.From = time.Now().Add(-5 * time.Minute)
		opts.TimeRange.To = time.Now().Add(5 * time.Minute)

		// Query for flows. There should be two flows from the populated data.
		r, err := fb.List(ctx, clusterInfo, &opts)
		require.NoError(t, err)
		require.Len(t, r.Items, 2)
		require.Nil(t, r.AfterKey)

		// Assert that the flow data is populated correctly.
		require.Equal(t, exp2, r.Items[0])
		require.Equal(t, *exp1, r.Items[1])
	})
}

// TestFlowMultiplePolicies tests a flow that traverses multiple policies and ultimately
// hits the default profile allow rule.
func TestFlowMultiplePolicies(t *testing.T) {
	RunAllModes(t, "TestFlowMultiplePolicies", func(t *testing.T) {
		clusterInfo := bapi.ClusterInfo{Cluster: cluster}

		// Put some data into ES so we can query it.
		bld := backendutils.NewFlowLogBuilder()
		bld.WithType("wep").
			WithSourceNamespace("default").
			WithDestNamespace("kube-system").
			WithDestName("kube-dns-*").
			WithDestIP("10.0.0.10").
			WithDestService("kube-dns", 53).
			WithDestPort(53).
			WithProtocol("tcp").
			WithSourceName("my-deployment").
			WithSourceIP("192.168.1.1").
			WithRandomFlowStats().WithRandomPacketStats().
			WithReporter("src").WithAction("allowed").
			WithSourceLabels("bread=rye", "cheese=brie", "wine=none").
			// Add in a couple of policies, as well as the default profile hit.
			WithPolicy("0|allow-tigera|kube-system/allow-tigera.cluster-dns|pass|1").
			WithPolicy("1|__PROFILE__|__PROFILE__.kns.kube-system|allow|0")

		expected := populateFlowData(t, ctx, bld, client, clusterInfo)

		// Add in the expected policies.
		expected.Policies = []v1.Policy{
			{
				Tier:      "allow-tigera",
				Name:      "cluster-dns",
				Namespace: "kube-system",
				Action:    "pass",
				Count:     expected.LogStats.FlowLogCount,
				RuleID:    testutils.IntPtr(1),
			},
			{
				Tier:      "__PROFILE__",
				Name:      "kns.kube-system",
				Namespace: "",
				Action:    "allow",
				Count:     expected.LogStats.FlowLogCount,
				RuleID:    testutils.IntPtr(0),
				IsProfile: true,
			},
		}

		// Set time range so that we capture all of the populated flow logs.
		opts := v1.L3FlowParams{}
		opts.TimeRange = &lmav1.TimeRange{}
		opts.TimeRange.From = time.Now().Add(-5 * time.Minute)
		opts.TimeRange.To = time.Now().Add(5 * time.Minute)

		// Query for flows. There should be a single flow from the populated data.
		r, err := fb.List(ctx, clusterInfo, &opts)
		require.NoError(t, err)
		require.Len(t, r.Items, 1)
		require.Nil(t, r.AfterKey)

		// Assert that the flow data is populated correctly.
		require.Equal(t, expected, r.Items[0])
	})
}

func TestFlowFiltering(t *testing.T) {
	type testCase struct {
		Name   string
		Params v1.L3FlowParams

		// Configuration for which flows are expected to match.
		ExpectFlow1 bool
		ExpectFlow2 bool

		// Number of logs to create
		NumLogs int

		// Whether to perform an equality comparison on the returned
		// flows. Can be useful for tests where stats differ.
		SkipComparison bool
	}

	numExpected := func(tc testCase) int {
		num := 0
		if tc.ExpectFlow1 {
			num++
		}
		if tc.ExpectFlow2 {
			num++
		}
		return num
	}

	testcases := []testCase{
		{
			Name: "should query a flow based on source type",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				SourceTypes: []v1.EndpointType{v1.WEP},
			},
			ExpectFlow1: true,
			ExpectFlow2: false, // Flow 2 is type hep, so won't match.
		},
		{
			Name: "should query a flow based on multiple destination types",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				SourceTypes: []v1.EndpointType{v1.WEP, v1.HEP},
			},
			ExpectFlow1: true,
			ExpectFlow2: true,
		},
		{
			Name: "should query a flow based on destination type",
			Params: v1.L3FlowParams{
				QueryParams:      v1.QueryParams{},
				DestinationTypes: []v1.EndpointType{v1.WEP},
			},
			ExpectFlow1: true,
			ExpectFlow2: false, // Flow 2 is type hep, so won't match.
		},
		{
			Name: "should query a flow based on multiple destination types",
			Params: v1.L3FlowParams{
				QueryParams:      v1.QueryParams{},
				DestinationTypes: []v1.EndpointType{v1.WEP, v1.HEP},
			},
			ExpectFlow1: true,
			ExpectFlow2: true,
		},
		{
			Name: "should query a flow based on source namespace",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				NamespaceMatches: []v1.NamespaceMatch{
					{
						Type:       v1.MatchTypeSource,
						Namespaces: []string{"default"},
					},
				},
			},
			ExpectFlow1: false, // Flow 1 has source namespace tigera-operator
			ExpectFlow2: true,
		},
		{
			Name: "should query a flow based on multiple source namespaces",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				NamespaceMatches: []v1.NamespaceMatch{
					{
						Type:       v1.MatchTypeSource,
						Namespaces: []string{"default", "tigera-operator"},
					},
				},
			},
			ExpectFlow1: true,
			ExpectFlow2: true,
		},
		{
			Name: "should query a flow based on destination namespace",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				NamespaceMatches: []v1.NamespaceMatch{
					{
						Type:       v1.MatchTypeDest,
						Namespaces: []string{"kube-system"},
					},
				},
			},
			ExpectFlow1: false, // Flow 1 has dest namespace openshift-system
			ExpectFlow2: true,
		},
		{
			Name: "should query a flow based on multiple destination namespace",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				NamespaceMatches: []v1.NamespaceMatch{
					{
						Type:       v1.MatchTypeDest,
						Namespaces: []string{"kube-system", "openshift-dns"},
					},
				},
			},
			ExpectFlow1: true,
			ExpectFlow2: true,
		},
		{
			Name: "should query a flow based on namespace MatchTypeAny",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				NamespaceMatches: []v1.NamespaceMatch{
					{
						Type:       v1.MatchTypeAny,
						Namespaces: []string{"kube-system"},
					},
				},
			},
			ExpectFlow1: false,
			ExpectFlow2: true,
		},
		{
			Name: "should query a flow based on source label equal selector",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				SourceSelectors: []v1.LabelSelector{
					{
						Key:      "bread",
						Operator: "=",
						Values:   []string{"rye"},
					},
				},
			},
			ExpectFlow1: true,
			ExpectFlow2: false, // Flow 2 doesn't have the label
		},
		{
			Name: "should query a flow based on dest label equal selector",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				DestinationSelectors: []v1.LabelSelector{
					{
						Key:      "dest_iteration",
						Operator: "=",
						Values:   []string{"0"},
					},
				},
			},
			// Both flows have this label set on destination.
			ExpectFlow1: true,
			ExpectFlow2: true,
		},
		{
			Name: "should query a flow based on dest label selector matching none",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				DestinationSelectors: []v1.LabelSelector{
					{
						Key:      "cranberry",
						Operator: "=",
						Values:   []string{"sauce"},
					},
				},
			},
			// neither flow has this label set on destination.
			ExpectFlow1: false,
			ExpectFlow2: false,
		},
		{
			Name: "should query a flow based on multiple source labels",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				SourceSelectors: []v1.LabelSelector{
					{
						Key:      "bread",
						Operator: "=",
						Values:   []string{"rye"},
					},
					{
						Key:      "cheese",
						Operator: "=",
						Values:   []string{"cheddar"},
					},
				},
			},
			ExpectFlow1: true,
			ExpectFlow2: false, // Missing both labels
		},
		{
			Name: "should query a flow based on multiple destination values for a single label",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				DestinationSelectors: []v1.LabelSelector{
					{
						Key:      "dest_iteration",
						Operator: "=",
						Values:   []string{"0", "1"},
					},
				},
			},

			// Both have this label.
			ExpectFlow1: true,
			ExpectFlow2: true,
			NumLogs:     2,
		},
		{
			Name: "should query a flow based on multiple destination values for a single label not comprehensive",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				DestinationSelectors: []v1.LabelSelector{
					{
						Key:      "dest_iteration",
						Operator: "=",
						Values:   []string{"0", "1"},
					},
				},
			},

			// Both have this label.
			ExpectFlow1: true,
			ExpectFlow2: true,
			NumLogs:     4,

			// Skip comparison on this one, since the returned flows don't match the expected ones
			// due to the filtering and the simplicity of our test modeling of flow logs.
			SkipComparison: true,
		},
		{
			Name: "should query a flow based on action",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				Actions:     []v1.FlowAction{v1.FlowActionAllow},
			},

			ExpectFlow1: true, // Only the first flow allows.
			ExpectFlow2: false,
		},
		{
			Name: "should query a flow based on multiple actions",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				Actions:     []v1.FlowAction{v1.FlowActionAllow, v1.FlowActionDeny},
			},

			ExpectFlow1: true,
			ExpectFlow2: true,
		},
		{
			Name: "should query a flow based on source name aggr",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				NameAggrMatches: []v1.NameMatch{
					{
						Type:  v1.MatchTypeSource,
						Names: []string{"tigera-operator-*"},
					},
				},
			},

			ExpectFlow1: true,
			ExpectFlow2: false,
		},
		{
			Name: "should query a flow based on dest name aggr",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				NameAggrMatches: []v1.NameMatch{
					{
						Type:  v1.MatchTypeDest,
						Names: []string{"kube-dns-*"},
					},
				},
			},

			ExpectFlow1: false,
			ExpectFlow2: true,
		},
		{
			Name: "should query a flow based on any name aggr",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				NameAggrMatches: []v1.NameMatch{
					{
						Type:  v1.MatchTypeAny,
						Names: []string{"kube-dns-*"},
					},
				},
			},

			ExpectFlow1: false,
			ExpectFlow2: true,
		},
		{
			Name: "should query based on unprotected flows",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				PolicyMatches: []v1.PolicyMatch{
					{
						// Match the first flow's profile hit. This match returns all "unprotected"
						// flows in all namespaces.
						Tier:   "__PROFILE__",
						Action: ActionPtr(v1.FlowActionAllow),
					},
				},
			},

			ExpectFlow1: true,
			ExpectFlow2: false,
		},
		{
			Name: "should query based on unprotected flows within a namespace",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				NamespaceMatches: []v1.NamespaceMatch{
					{
						Type:       v1.MatchTypeAny,
						Namespaces: []string{"openshift-dns"},
					},
				},
				PolicyMatches: []v1.PolicyMatch{
					{
						// Match the first flow's profile hit. This match returns all "unprotected"
						// flows from the openshift-dns namespace.
						Tier:   "__PROFILE__",
						Name:   testutils.StringPtr("kns.openshift-dns"),
						Action: ActionPtr(v1.FlowActionAllow),
					},
				},
			},

			ExpectFlow1: true,
			ExpectFlow2: false,
		},
		{
			Name: "should query based on a specific policy hit tier",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				PolicyMatches: []v1.PolicyMatch{
					{
						Tier: "allow-tigera",
					},
				},
			},

			// Both flows have a policy hit in this tier.
			ExpectFlow1: true,
			ExpectFlow2: true,
		},
		{
			Name: "should query based on a specific policy hit tier and action",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				PolicyMatches: []v1.PolicyMatch{
					{
						Tier:   "allow-tigera",
						Action: ActionPtr(v1.FlowActionAllow),
					},
				},
			},

			// Both flows have a policy hit in this tier, but only the second
			// is allowed by the tier.
			ExpectFlow1: false,
			ExpectFlow2: true,
		},
		{
			Name: "should query based on a specific policy hit name and namespace",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				PolicyMatches: []v1.PolicyMatch{
					{
						Name:      testutils.StringPtr("cluster-dns"),
						Namespace: testutils.StringPtr("kube-system"),
					},
				},
			},

			ExpectFlow1: false,
			ExpectFlow2: true,
		},
		{
			Name: "should query based on a specific policy hit name",
			Params: v1.L3FlowParams{
				QueryParams: v1.QueryParams{},
				PolicyMatches: []v1.PolicyMatch{
					{
						Name: testutils.StringPtr("cluster-dns"),
					},
				},
			},

			ExpectFlow1: true,
			ExpectFlow2: true,
		},
		{
			// This test should match both flows, but does so by fully-specifying all
			// query parameters.
			Name: "should query both flows with a complex multi-part query",
			Params: v1.L3FlowParams{
				QueryParams:      v1.QueryParams{},
				Actions:          []v1.FlowAction{v1.FlowActionAllow, v1.FlowActionDeny},
				SourceTypes:      []v1.EndpointType{v1.WEP, v1.HEP},
				DestinationTypes: []v1.EndpointType{v1.WEP, v1.HEP},
				NamespaceMatches: []v1.NamespaceMatch{
					{
						Type:       v1.MatchTypeDest,
						Namespaces: []string{"kube-system", "openshift-dns"},
					},
					{
						Type:       v1.MatchTypeSource,
						Namespaces: []string{"default", "tigera-operator"},
					},
				},
			},

			ExpectFlow1: true,
			ExpectFlow2: true,
		},
		{
			// This test uses a complex query that ultimately only matches on of the flows
			// beacause it doesn't include flow1's destination namespace.
			Name: "should query a flow with a complex multi-part query",
			Params: v1.L3FlowParams{
				QueryParams:      v1.QueryParams{},
				Actions:          []v1.FlowAction{v1.FlowActionAllow, v1.FlowActionDeny},
				SourceTypes:      []v1.EndpointType{v1.WEP, v1.HEP},
				DestinationTypes: []v1.EndpointType{v1.WEP, v1.HEP},
				NamespaceMatches: []v1.NamespaceMatch{
					{
						Type:       v1.MatchTypeDest,
						Namespaces: []string{"openshift-dns"},
					},
					{
						Type:       v1.MatchTypeSource,
						Namespaces: []string{"default", "tigera-operator"},
					},
				},
				PolicyMatches: []v1.PolicyMatch{
					{
						// Match the first flow's profile hit.
						Tier:   "__PROFILE__",
						Name:   testutils.StringPtr("kns.openshift-dns"),
						Action: ActionPtr(v1.FlowActionAllow),
					},
				},
			},

			ExpectFlow1: true,
			ExpectFlow2: false,
		},
	}

	for _, testcase := range testcases {
		// Each testcase creates multiple flows, and then uses
		// different filtering parameters provided in the L3FlowParams
		// to query one or more flows.
		RunAllModes(t, testcase.Name, func(t *testing.T) {
			clusterInfo := bapi.ClusterInfo{Cluster: cluster}

			// Set the time range for the test. We set this per-test
			// so that the time range captures the windows that the logs
			// are created in.
			tr := &lmav1.TimeRange{}
			tr.From = time.Now().Add(-5 * time.Minute)
			tr.To = time.Now().Add(5 * time.Minute)
			testcase.Params.QueryParams.TimeRange = tr

			numLogs := testcase.NumLogs
			if numLogs == 0 {
				numLogs = 1
			}

			// Template for flow #1.
			bld := backendutils.NewFlowLogBuilder()
			bld.WithType("wep").
				WithSourceNamespace("tigera-operator").
				WithDestNamespace("openshift-dns").
				WithDestName("openshift-dns-*").
				WithDestIP("10.0.0.10").
				WithDestService("openshift-dns", 53).
				WithDestPort(1053).
				WithSourcePort(1010).
				WithProtocol("udp").
				WithSourceName("tigera-operator-*").
				WithSourceIP("34.15.66.3").
				WithRandomFlowStats().WithRandomPacketStats().
				WithReporter("src").WithAction("allow").
				WithSourceLabels("bread=rye", "cheese=cheddar", "wine=none").
				// Pass followed by a profile allow.
				WithPolicy("0|allow-tigera|openshift-dns/allow-tigera.cluster-dns|pass|1").
				WithPolicy("1|__PROFILE__|__PROFILE__.kns.openshift-dns|allow|0").
				WithDestDomains("www.tigera.io", "www.calico.com", "www.kubernetes.io", "www.docker.com")
			exp1 := populateFlowDataN(t, ctx, bld, client, clusterInfo, numLogs)

			// Template for flow #2.
			bld2 := backendutils.NewFlowLogBuilder()
			bld2.WithType("hep").
				WithSourceNamespace("default").
				WithDestNamespace("kube-system").
				WithDestName("kube-dns-*").
				WithDestIP("10.0.0.10").
				WithDestService("kube-dns", 53).
				WithDestPort(53).
				WithSourcePort(5656).
				WithProtocol("udp").
				WithSourceName("my-deployment-*").
				WithSourceIP("192.168.1.1").
				WithRandomFlowStats().WithRandomPacketStats().
				WithReporter("src").WithAction("deny").
				WithSourceLabels("cheese=brie").
				// Explicit allow.
				WithPolicy("0|allow-tigera|kube-system/allow-tigera.cluster-dns|allow|1").
				WithDestDomains("www.tigera.io", "www.calico.com", "www.kubernetes.io", "www.docker.com")

			exp2 := populateFlowDataN(t, ctx, bld2, client, clusterInfo, numLogs)

			// Query for flows.
			r, err := fb.List(ctx, clusterInfo, &testcase.Params)
			require.NoError(t, err)
			require.Len(t, r.Items, numExpected(testcase))
			require.Nil(t, r.AfterKey)

			if testcase.SkipComparison {
				return
			}

			// Assert that the correct flows are returned.
			if testcase.ExpectFlow1 {
				require.Contains(t, r.Items, exp1)
			}
			if testcase.ExpectFlow2 {
				require.Contains(t, r.Items, exp2)
			}
		})
	}
}

// TestPagination tests that we return multiple flows properly using pagination.
func TestPagination(t *testing.T) {
	RunAllModes(t, "TestPagination", func(t *testing.T) {
		// Both flows use the same cluster information.
		clusterInfo := bapi.ClusterInfo{Cluster: cluster}

		// Template for flow #1.
		bld := backendutils.NewFlowLogBuilder()
		bld.WithType("wep").
			WithSourceNamespace("tigera-operator").
			WithDestNamespace("kube-system").
			WithDestName("kube-dns-*").
			WithDestIP("10.0.0.10").
			WithDestService("kube-dns", 53).
			WithDestPort(53).
			WithProtocol("udp").
			WithSourceName("tigera-operator").
			WithSourceIP("34.15.66.3").
			WithRandomFlowStats().WithRandomPacketStats().
			WithReporter("src").WithAction("allowed").
			WithSourceLabels("bread=rye", "cheese=brie", "wine=none").
			WithDestDomains("www.tigera.io", "www.calico.com", "www.kubernetes.io", "www.docker.com")
		exp1 := populateFlowData(t, ctx, bld, client, clusterInfo)

		// Template for flow #2.
		bld2 := backendutils.NewFlowLogBuilder()
		bld2.WithType("wep").
			WithSourceNamespace("default").
			WithDestNamespace("kube-system").
			WithDestName("kube-dns-*").
			WithDestIP("10.0.0.10").
			WithDestService("kube-dns", 53).
			WithDestPort(53).
			WithProtocol("udp").
			WithSourceName("my-deployment").
			WithSourceIP("192.168.1.1").
			WithRandomFlowStats().WithRandomPacketStats().
			WithReporter("src").WithAction("allowed").
			WithSourceLabels("bread=rye", "cheese=brie", "wine=none").
			WithDestDomains("www.tigera.io", "www.calico.com", "www.kubernetes.io", "www.docker.com")
		exp2 := populateFlowData(t, ctx, bld2, client, clusterInfo)

		// Set time range so that we capture all of the populated flow logs.
		opts := v1.L3FlowParams{}
		opts.TimeRange = &lmav1.TimeRange{}
		opts.TimeRange.From = time.Now().Add(-5 * time.Minute)
		opts.TimeRange.To = time.Now().Add(5 * time.Minute)

		// Also set a max results of 1, so that we only get one flow at a time.
		opts.MaxPageSize = 1

		// Query for flows. There should be a single flow from the populated data.
		r, err := fb.List(ctx, clusterInfo, &opts)
		require.NoError(t, err)
		require.Len(t, r.Items, 1)
		require.NotNil(t, r.AfterKey)
		require.Equal(t, exp2, r.Items[0])

		// Now, send another request. This time, passing in the pagination key
		// returned from the first. We should get the second flow.
		opts.AfterKey = r.AfterKey
		r, err = fb.List(ctx, clusterInfo, &opts)
		require.NoError(t, err)
		require.Len(t, r.Items, 1)
		require.NotNil(t, r.AfterKey)
		require.Equal(t, exp1, r.Items[0])
	})
}

// Definitions for search results to be used in the tests below.

//go:embed testdata/elastic_valid_flow.json
var validSingleFlow []byte

// Test the handling of various responses from elastic. This suite of tests uses a mock http server
// to return custom responses from elastic without the need for running a real elastic server.
// This can be useful for simulating strange or malformed responses from Elasticsearch.
func TestElasticResponses(t *testing.T) {
	// Set elasticResponse in each test to mock out a given response from Elastic.
	var server *httptest.Server
	var ctx context.Context
	var opts v1.L3FlowParams
	var clusterInfo bapi.ClusterInfo

	// setupAndTeardown initializes and tears down each test.
	setupAndTeardown := func(t *testing.T, elasticResponse []byte) func() {
		// Hook logrus into testing.T
		config.ConfigureLogging("DEBUG")
		logCancel := logutils.RedirectLogrusToTestingT(t)

		// Create a mock server to return elastic responses.
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			_, err := w.Write(elasticResponse)
			require.NoError(t, err)
		}))

		// Configure the elastic client to use the URL of our test server.
		esClient, err := elastic.NewSimpleClient(elastic.SetURL(server.URL))
		require.NoError(t, err)
		client = lmaelastic.NewWithClient(esClient)

		// Create a FlowBackend using the client.
		fb = flows.NewFlowBackend(client)

		// Basic parameters for each test.
		clusterInfo.Cluster = cluster
		opts.TimeRange = &lmav1.TimeRange{}
		opts.TimeRange.From = time.Now().Add(-5 * time.Minute)
		opts.TimeRange.To = time.Now().Add(5 * time.Minute)

		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 1*time.Minute)

		// Teardown goes within this returned func.
		return func() {
			cancel()
			logCancel()
		}
	}

	type testCase struct {
		// Name of the test
		name string

		// Response from elastic to be returned by the mock server.
		response interface{}

		// Expected error
		err bool
	}

	// Define the list of testcases to run
	testCases := []testCase{
		{
			name:     "empty json",
			response: []byte("{}"),
			err:      false,
		},
		{
			name:     "malformed json",
			response: []byte("{"),
			err:      true,
		},
		{
			name:     "timeout",
			response: elastic.SearchResult{TimedOut: true},
			err:      true,
		},
		{
			name:     "valid single flow",
			response: validSingleFlow,
			err:      false,
		},
	}

	for _, testcase := range testCases {
		t.Run(testcase.name, func(t *testing.T) {
			// We allow either raw byte arrays, or structures to be passed
			// as input. If it's a struct, serialize it first.
			var err error
			bs, ok := testcase.response.([]byte)
			if !ok {
				bs, err = json.Marshal(testcase.response)
				require.NoError(t, err)
			}
			defer setupAndTeardown(t, bs)()

			// Query for flows.
			_, err = fb.List(ctx, clusterInfo, &opts)
			if testcase.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestMultiTenancy creates data for multiple tenants and asserts that it is handled properly.
func TestMultiTenancy(t *testing.T) {
	RunAllModes(t, "multiple tenants basic", func(t *testing.T) {
		// For this test, we will use two tenants with the same cluster ID, to be
		// extra sneaky.
		tenantA := "tenant-a"
		tenantB := "tenant-b"
		tenantAInfo := bapi.ClusterInfo{Cluster: cluster, Tenant: tenantA}
		tenantBInfo := bapi.ClusterInfo{Cluster: cluster, Tenant: tenantB}

		// Template for flow.
		bld := backendutils.NewFlowLogBuilder()
		bld.WithType("wep").
			WithSourceNamespace("tigera-operator").
			WithDestNamespace("kube-system").
			WithDestName("kube-dns-*").
			WithDestIP("10.0.0.10").
			WithDestService("kube-dns", 53).
			WithDestPort(53).
			WithProtocol("udp").
			WithSourceName("tigera-operator").
			WithSourceIP("34.15.66.3").
			WithRandomFlowStats().WithRandomPacketStats().
			WithReporter("src").WithAction("allowed").
			WithSourceLabels("bread=rye", "cheese=brie", "wine=none").
			WithDestDomains("www.tigera.io", "www.calico.com", "www.kubernetes.io", "www.docker.com")

		// Create the flow for tenant A.
		exp1 := populateFlowData(t, ctx, bld, client, tenantAInfo)

		// Set time range so that we capture all of the populated flow logs.
		opts := v1.L3FlowParams{}
		opts.TimeRange = &lmav1.TimeRange{}
		opts.TimeRange.From = time.Now().Add(-5 * time.Minute)
		opts.TimeRange.To = time.Now().Add(5 * time.Minute)

		// Query for flows using tenant A - there should be one flow from the populated data.
		r, err := fb.List(ctx, tenantAInfo, &opts)
		require.NoError(t, err)
		require.Len(t, r.Items, 1)
		require.Nil(t, r.AfterKey)
		require.Equal(t, exp1, r.Items[0])

		// Query for the flow using tenant B - we should get no results.
		r, err = fb.List(ctx, tenantBInfo, &opts)
		require.NoError(t, err)
		require.Len(t, r.Items, 0)
		require.Nil(t, r.AfterKey)
	})

	RunAllModes(t, "multiple tenants with similar names", func(t *testing.T) {
		// For this test, we use tenant IDs that are prefixes of each other.
		tenantA := "shaz"
		tenantB := "shazam"
		tenantAInfo := bapi.ClusterInfo{Cluster: cluster, Tenant: tenantA}
		tenantBInfo := bapi.ClusterInfo{Cluster: cluster, Tenant: tenantB}

		// Template for flow.
		bld := backendutils.NewFlowLogBuilder()
		bld.WithType("wep").
			WithSourceNamespace("tigera-operator").
			WithDestNamespace("kube-system").
			WithDestName("kube-dns-*").
			WithDestIP("10.0.0.10").
			WithDestService("kube-dns", 53).
			WithDestPort(53).
			WithProtocol("udp").
			WithSourceName("tigera-operator").
			WithSourceIP("34.15.66.3").
			WithRandomFlowStats().WithRandomPacketStats().
			WithReporter("src").WithAction("allowed").
			WithSourceLabels("bread=rye", "cheese=brie", "wine=none").
			WithDestDomains("www.tigera.io", "www.calico.com", "www.kubernetes.io", "www.docker.com")

		// Modify the builder for tenant B so that we can distinguish the two flows.
		bld2 := bld.Copy()
		bld2.WithReporter("dst")

		// Create the flow for both tenants
		exp1 := populateFlowData(t, ctx, bld, client, tenantAInfo)
		exp2 := populateFlowData(t, ctx, bld2, client, tenantBInfo)

		// Set time range so that we capture all of the populated flow logs.
		opts := v1.L3FlowParams{}
		opts.TimeRange = &lmav1.TimeRange{}
		opts.TimeRange.From = time.Now().Add(-5 * time.Minute)
		opts.TimeRange.To = time.Now().Add(5 * time.Minute)

		// Query for flows using tenant A - there should be one flow from the populated data.
		r, err := fb.List(ctx, tenantAInfo, &opts)
		require.NoError(t, err)
		require.Len(t, r.Items, 1)
		require.Nil(t, r.AfterKey)
		require.Equal(t, exp1, r.Items[0])

		// Query for flows using tenant B - there should be one flow from the populated data.
		r, err = fb.List(ctx, tenantBInfo, &opts)
		require.NoError(t, err)
		require.Len(t, r.Items, 1)
		require.Nil(t, r.AfterKey)
		require.Equal(t, exp2, r.Items[0])

		// Query for the flow specifying a tenant with a wildcard in it - should get no results.
		// It isn't actually possible for this codepath to be hit in a real system, since Linseed enforces
		// an expected tenant ID on all requests. We test it here nonetheless.
		wildcardTenant := bapi.ClusterInfo{Cluster: cluster, Tenant: "shaz*"}
		_, err = fb.List(ctx, wildcardTenant, &opts)
		require.Error(t, err)
	})
}

// populateFlowData writes a series of flow logs to elasticsearch, and returns the FlowLog that we
// should expect to exist as a result. This can be used to assert round-tripping and aggregation against ES is working correctly.
func populateFlowData(t *testing.T, ctx context.Context, b *backendutils.FlowLogBuilder, client lmaelastic.Client, info bapi.ClusterInfo) v1.L3Flow {
	return populateFlowDataN(t, ctx, b, client, info, 10)
}

func populateFlowDataN(t *testing.T, ctx context.Context, b *backendutils.FlowLogBuilder, client lmaelastic.Client, info bapi.ClusterInfo, n int) v1.L3Flow {
	batch := []v1.FlowLog{}

	for i := 0; i < n; i++ {
		// We want a variety of label keys and values,
		// so base this one off of the loop variable.
		// Note: We use a nested terms aggregation to get labels, which has an
		// inherent maximum number of buckets of 10. As a result, if a flow has more than
		// 10 labels, not all of them will be shown. We might be able to use a composite aggregation instead,
		// but these are more expensive.
		b.WithDestLabels(fmt.Sprintf("dest_iteration=%d", i))
		f, err := b.Build()
		require.NoError(t, err)

		// Add it to the batch
		batch = append(batch, *f)
	}

	// Create the batch.
	// Creating the flow logs may fail due to conflicts with other tests modifying the same index.
	// Since go test runs packages in parallel, we need to retry a few times to avoid flakiness.
	// We could avoid this by creating a new ES instance per-test or per-package, but that would
	// slow down the test and use more resources. This is a reasonable compromise, and what clients will need to do anyway.
	attempts := 0
	response, err := flb.Create(ctx, info, batch)
	for err != nil && attempts < 5 {
		logrus.WithError(err).Info("[TEST] Retrying flow log creation due to error")
		attempts++
		response, err = flb.Create(ctx, info, batch)
	}
	require.NoError(t, err)
	require.Equal(t, []v1.BulkError(nil), response.Errors)
	require.Equal(t, 0, response.Failed)

	// Refresh the index so that data is readily available for the test. Otherwise, we need to wait
	// for the refresh interval to occur.
	err = backendutils.RefreshIndex(ctx, client, indexGetter.Index(info))
	require.NoError(t, err)

	// Return the expected flow based on the batch of flows we created above.
	expected := b.ExpectedFlow(t)
	return *expected
}

func ActionPtr(val v1.FlowAction) *v1.FlowAction {
	return &val
}
