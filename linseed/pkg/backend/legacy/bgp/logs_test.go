// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package bgp_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/projectcalico/calico/libcalico-go/lib/logutils"
	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/bgp"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/index"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/templates"
	"github.com/projectcalico/calico/linseed/pkg/backend/testutils"
	backendutils "github.com/projectcalico/calico/linseed/pkg/backend/testutils"
	"github.com/projectcalico/calico/linseed/pkg/config"
	lmav1 "github.com/projectcalico/calico/lma/pkg/apis/v1"
	lmaelastic "github.com/projectcalico/calico/lma/pkg/elastic"
)

var (
	client      lmaelastic.Client
	b           bapi.BGPBackend
	ctx         context.Context
	cluster1    string
	cluster2    string
	cluster3    string
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

// setupTest runs common logic before each test, and also returns a function to perform teardown
// after each test.
func setupTest(t *testing.T, singleIndex bool) func() {
	// Hook logrus into testing.T
	config.ConfigureLogging("DEBUG")
	logCancel := logutils.RedirectLogrusToTestingT(t)

	// Create an elasticsearch client to use for the test. For this suite, we use a real
	// elasticsearch instance created via "make run-elastic".
	esClient, err := backendutils.CreateElasticClient()

	require.NoError(t, err)
	client = lmaelastic.NewWithClient(esClient)
	cache := templates.NewCachedInitializer(client, 1, 0)

	// Instantiate a backend.
	if singleIndex {
		indexGetter = index.BGPLogIndex()
		b = bgp.NewSingleIndexBackend(client, cache, 1000)
	} else {
		b = bgp.NewBackend(client, cache, 10000)
		indexGetter = index.BGPLogMultiIndex
	}

	// Create a random cluster name for each test to make sure we don't
	// interfere between tests.
	cluster1 = testutils.RandomClusterName()
	cluster2 = testutils.RandomClusterName()
	cluster3 = testutils.RandomClusterName()

	// Each test should take less than 5 seconds.
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)

	// Function contains teardown logic.
	return func() {
		for _, cluster := range []string{cluster1, cluster2, cluster3} {
			err = backendutils.CleanupIndices(context.Background(), esClient, singleIndex, indexGetter, bapi.ClusterInfo{Cluster: cluster})
		}
		require.NoError(t, err)

		// Cancel the context
		cancel()
		logCancel()
	}
}

// TestCreateBGPLog tests running a real elasticsearch query to create a kube bgp log.
func TestCreateBGPLog(t *testing.T) {
	// Run each testcase both as a multi-tenant scenario, as well as a single-tenant case.
	for _, tenant := range []string{backendutils.RandomTenantName(), ""} {
		name := fmt.Sprintf("TestCreateBGPLog (tenant=%s)", tenant)
		RunAllModes(t, name, func(t *testing.T) {
			cluster1Info := bapi.ClusterInfo{Cluster: cluster1, Tenant: tenant}
			cluster2Info := bapi.ClusterInfo{Cluster: cluster2, Tenant: tenant}
			cluster3Info := bapi.ClusterInfo{Cluster: cluster3, Tenant: tenant}

			f := v1.BGPLog{
				LogTime:   "1990-09-15T06:12:32",
				Message:   "BGP is wonderful",
				IPVersion: v1.IPv6BGPLog,
				Host:      "lenox",
			}

			// Create the event in ES.
			for _, clusterInfo := range []bapi.ClusterInfo{cluster1Info, cluster2Info, cluster3Info} {
				resp, err := b.Create(ctx, clusterInfo, []v1.BGPLog{f})
				require.NoError(t, err)
				require.Equal(t, []v1.BulkError(nil), resp.Errors)
				require.Equal(t, 1, resp.Total)
				require.Equal(t, 0, resp.Failed)
				require.Equal(t, 1, resp.Succeeded)

				// Refresh the index.
				err = testutils.RefreshIndex(ctx, client, indexGetter.Index(clusterInfo))
				require.NoError(t, err)
			}

			// List the log, assert that it matches the one we just wrote.
			start, err := time.Parse(v1.BGPLogTimeFormat, "1990-09-15T06:12:00")
			require.NoError(t, err)
			params := &v1.BGPLogParams{
				QueryParams: v1.QueryParams{
					TimeRange: &lmav1.TimeRange{
						From: start,
						To:   time.Now(),
					},
				},
			}

			t.Run("should query single cluster", func(t *testing.T) {
				clusterInfo := cluster1Info
				results, err := b.List(ctx, clusterInfo, params)
				require.NoError(t, err)
				require.Equal(t, 1, len(results.Items))
				backendutils.AssertBGPLogClusterAndReset(t, clusterInfo.Cluster, &results.Items[0])
				require.Equal(t, f, results.Items[0])

				// List again with a bogus tenant ID.
				results, err = b.List(ctx, bapi.ClusterInfo{Cluster: clusterInfo.Cluster, Tenant: "bogus-tenant"}, params)
				require.NoError(t, err)
				require.Equal(t, 0, len(results.Items))
			})

			t.Run("should query multiple clusters", func(t *testing.T) {
				selectedClusters := []string{cluster2, cluster3}
				params.SetClusters(selectedClusters)

				results, err := b.List(ctx, bapi.ClusterInfo{Cluster: v1.QueryMultipleClusters}, params)
				require.NoError(t, err)
				require.Equal(t, 2, len(results.Items))

				require.Falsef(t, backendutils.MatchIn(results.Items, backendutils.BGPLogClusterEquals(cluster1)), "cluster1 should not be in the results")
				for _, cluster := range selectedClusters {
					require.Truef(t, backendutils.MatchIn(results.Items, backendutils.BGPLogClusterEquals(cluster)), "cluster %s should be in the results", cluster)
				}
			})

			t.Run("should query all clusters", func(t *testing.T) {
				params.SetAllClusters(true)
				results, err := b.List(ctx, bapi.ClusterInfo{Cluster: v1.QueryMultipleClusters}, params)
				require.NoError(t, err)
				for _, cluster := range []string{cluster1, cluster2, cluster3} {
					require.Truef(t, backendutils.MatchIn(results.Items, backendutils.BGPLogClusterEquals(cluster)), "cluster %s should be in the results", cluster)
				}
			})
		})
	}

	RunAllModes(t, "no cluster name given", func(t *testing.T) {
		// It should reject requests with no cluster name given.
		clusterInfo := bapi.ClusterInfo{}
		f := v1.BGPLog{
			LogTime:   "1990-09-15T06:12:32",
			Message:   "BGP is wonderful",
			IPVersion: v1.IPv6BGPLog,
			Host:      "lenox",
		}
		_, err := b.Create(ctx, clusterInfo, []v1.BGPLog{f})
		require.Error(t, err)

		start, err := time.Parse(v1.BGPLogTimeFormat, "1990-09-15T06:12:00")
		require.NoError(t, err)
		params := &v1.BGPLogParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: start,
					To:   time.Now(),
				},
			},
		}
		results, err := b.List(ctx, clusterInfo, params)
		require.Error(t, err)
		require.Nil(t, results)
	})
}
