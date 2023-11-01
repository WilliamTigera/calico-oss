// Copyright (c) 2023 Tigera, Inc. All rights reserved.

//go:build fvtests

package fv_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/projectcalico/calico/linseed/pkg/client"
	"github.com/projectcalico/calico/linseed/pkg/config"

	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/index"
	"github.com/projectcalico/calico/linseed/pkg/backend/testutils"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	lmav1 "github.com/projectcalico/calico/lma/pkg/apis/v1"
)

func RunComplianceBenchmarkTest(t *testing.T, name string, testFn func(*testing.T, bapi.Index)) {
	t.Run(fmt.Sprintf("%s [MultiIndex]", name), func(t *testing.T) {
		args := DefaultLinseedArgs()
		defer setupAndTeardown(t, args, nil, index.ComplianceBenchmarkMultiIndex)()
		testFn(t, index.ComplianceBenchmarkMultiIndex)
	})

	t.Run(fmt.Sprintf("%s [SingleIndex]", name), func(t *testing.T) {
		confArgs := &RunConfigureElasticArgs{
			ComplianceBenchmarksBaseIndexName: index.ComplianceBenchmarksIndex().Name(bapi.ClusterInfo{}),
			ComplianceBenchmarksPolicyName:    index.ComplianceBenchmarksIndex().ILMPolicyName(),
		}
		args := DefaultLinseedArgs()
		args.Backend = config.BackendTypeSingleIndex
		defer setupAndTeardown(t, args, confArgs, index.ComplianceBenchmarksIndex())()
		testFn(t, index.ComplianceBenchmarksIndex())
	})
}

func TestFV_ComplianceBenchmarks(t *testing.T) {
	RunComplianceBenchmarkTest(t, "should return an empty list if there are no benchmarks", func(t *testing.T, idx bapi.Index) {
		params := v1.BenchmarksParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: time.Now().Add(-5 * time.Second),
					To:   time.Now(),
				},
			},
		}

		// Perform a query.
		benchmarks, err := cli.Compliance(cluster).Benchmarks().List(ctx, &params)
		require.NoError(t, err)
		require.Equal(t, []v1.Benchmarks{}, benchmarks.Items)
	})

	RunComplianceBenchmarkTest(t, "should create and list benchmarks", func(t *testing.T, idx bapi.Index) {
		benchmarks := v1.Benchmarks{
			Version:           "v1",
			KubernetesVersion: "v1.0",
			Type:              v1.TypeKubernetes,
			NodeName:          "lodestone",
			Timestamp:         metav1.Time{Time: time.Unix(1, 0)},
			Error:             "",
			Tests: []v1.BenchmarkTest{
				{
					Section:     "a.1",
					SectionDesc: "testing the test",
					TestNumber:  "1",
					TestDesc:    "making sure that we're right",
					TestInfo:    "information is fluid",
					Status:      "Just swell",
					Scored:      true,
				},
			},
		}
		bulk, err := cli.Compliance(cluster).Benchmarks().Create(ctx, []v1.Benchmarks{benchmarks})
		require.NoError(t, err)
		require.Equal(t, bulk.Succeeded, 1, "create did not succeed")

		// Refresh elasticsearch so that results appear.
		testutils.RefreshIndex(ctx, lmaClient, idx.Index(clusterInfo))

		// Read it back, passing an ID query.
		params := v1.BenchmarksParams{ID: benchmarks.UID()}
		resp, err := cli.Compliance(cluster).Benchmarks().List(ctx, &params)
		require.NoError(t, err)

		// The ID should be set.
		require.Len(t, resp.Items, 1)
		require.Equal(t, benchmarks.UID(), resp.Items[0].ID)
		resp.Items[0].ID = ""
		require.Equal(t, benchmarks, resp.Items[0])

		// Read it back, using a time range
		params = v1.BenchmarksParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: time.Unix(0, 0),
					To:   time.Unix(2, 0),
				},
			},
		}
		resp, err = cli.Compliance(cluster).Benchmarks().List(ctx, &params)
		require.NoError(t, err)

		// The ID should be set.
		require.Len(t, resp.Items, 1)
		require.Equal(t, benchmarks.UID(), resp.Items[0].ID)
		resp.Items[0].ID = ""
		require.Equal(t, benchmarks, resp.Items[0])
	})

	RunComplianceBenchmarkTest(t, "should support pagination", func(t *testing.T, idx bapi.Index) {
		totalItems := 5

		// Create 5 Benchmarks.
		logTime := time.Unix(0, 0).UTC()
		for i := 0; i < totalItems; i++ {
			benchmarks := []v1.Benchmarks{
				{
					Timestamp: metav1.Time{Time: logTime.Add(time.Duration(i) * time.Second)},
					NodeName:  fmt.Sprintf("%d", i),
				},
			}
			bulk, err := cli.Compliance(cluster).Benchmarks().Create(ctx, benchmarks)
			require.NoError(t, err)
			require.Equal(t, bulk.Succeeded, 1, "create benchmarks did not succeed")
		}

		// Refresh elasticsearch so that results appear.
		testutils.RefreshIndex(ctx, lmaClient, idx.Index(clusterInfo))

		// Iterate through the first 4 pages and check they are correct.
		var afterKey map[string]interface{}
		for i := 0; i < totalItems-1; i++ {
			params := v1.BenchmarksParams{
				QueryParams: v1.QueryParams{
					TimeRange: &lmav1.TimeRange{
						From: logTime.Add(-5 * time.Second),
						To:   logTime.Add(5 * time.Second),
					},
					MaxPageSize: 1,
					AfterKey:    afterKey,
				},
				Sort: []v1.SearchRequestSortBy{
					{
						Field: "timestamp",
					},
				},
			}
			resp, err := cli.Compliance(cluster).Benchmarks().List(ctx, &params)
			require.NoError(t, err)
			require.Equal(t, 1, len(resp.Items))
			require.Equal(t, []v1.Benchmarks{
				{
					Timestamp: metav1.Time{Time: logTime.Add(time.Duration(i) * time.Second)},
					NodeName:  fmt.Sprintf("%d", i),
				},
			}, benchmarksWithUTCTime(resp), fmt.Sprintf("Benchmark #%d did not match", i))
			require.NotNil(t, resp.AfterKey)
			require.Contains(t, resp.AfterKey, "startFrom")
			require.Equal(t, resp.AfterKey["startFrom"], float64(i+1))
			require.Equal(t, resp.TotalHits, int64(totalItems))

			// Use the afterKey for the next query.
			afterKey = resp.AfterKey
		}

		// If we query once more, we should get the last page, and no afterkey, since
		// we have paged through all the items.
		lastItem := totalItems - 1
		params := v1.BenchmarksParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: logTime.Add(-5 * time.Second),
					To:   logTime.Add(5 * time.Second),
				},
				MaxPageSize: 1,
				AfterKey:    afterKey,
			},
			Sort: []v1.SearchRequestSortBy{
				{
					Field: "timestamp",
				},
			},
		}
		resp, err := cli.Compliance(cluster).Benchmarks().List(ctx, &params)
		require.NoError(t, err)
		require.Equal(t, 1, len(resp.Items))
		require.Equal(t, []v1.Benchmarks{
			{
				Timestamp: metav1.Time{Time: logTime.Add(time.Duration(lastItem) * time.Second)},
				NodeName:  fmt.Sprintf("%d", lastItem),
			},
		}, benchmarksWithUTCTime(resp), fmt.Sprintf("Benchmark #%d did not match", lastItem))
		require.Equal(t, resp.TotalHits, int64(totalItems))

		// Once we reach the end of the data, we should not receive
		// an afterKey
		require.Nil(t, resp.AfterKey)
	})

	RunComplianceBenchmarkTest(t, "should support pagination for items >= 10000 for Benchmarks", func(t *testing.T, idx bapi.Index) {
		totalItems := 10001
		// Create > 10K benchmarks.
		logTime := time.Unix(0, 0).UTC()
		var benchmarks []v1.Benchmarks
		for i := 0; i < totalItems; i++ {
			benchmarks = append(benchmarks,
				v1.Benchmarks{
					Timestamp: metav1.Time{Time: logTime.Add(time.Duration(i) * time.Second)},
					NodeName:  fmt.Sprintf("%d", i),
				},
			)
		}
		bulk, err := cli.Compliance(cluster).Benchmarks().Create(ctx, benchmarks)
		require.NoError(t, err)
		require.Equal(t, totalItems, bulk.Succeeded, "create benchmarks did not succeed")

		// Refresh elasticsearch so that results appear.
		testutils.RefreshIndex(ctx, lmaClient, idx.Index(clusterInfo))

		// Stream through all the items.
		params := v1.BenchmarksParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: logTime.Add(-5 * time.Second),
					To:   logTime.Add(time.Duration(totalItems) * time.Second),
				},
				MaxPageSize: 1000,
			},
		}

		pager := client.NewListPager[v1.Benchmarks](&params)
		pages, errors := pager.Stream(ctx, cli.Compliance(cluster).Benchmarks().List)

		receivedItems := 0
		for page := range pages {
			receivedItems = receivedItems + len(page.Items)
		}

		if err, ok := <-errors; ok {
			require.NoError(t, err)
		}

		require.Equal(t, receivedItems, totalItems)
	})
}

func TestFV_BenchmarksTenancy(t *testing.T) {
	RunComplianceBenchmarkTest(t, "should support tenancy restriction", func(t *testing.T, idx bapi.Index) {
		// Instantiate a client for an unexpected tenant.
		args := DefaultLinseedArgs()
		args.TenantID = "bad-tenant"
		tenantCLI, err := NewLinseedClient(args)
		require.NoError(t, err)

		// Create a basic flow log. We expect this to fail, since we're using
		// an unexpected tenant ID on the request.
		benchmarks := v1.Benchmarks{
			Version:           "v1",
			KubernetesVersion: "v1.0",
			Type:              v1.TypeKubernetes,
			NodeName:          "lodestone",
			Timestamp:         metav1.Time{Time: time.Unix(1, 0)},
			Error:             "",
			Tests: []v1.BenchmarkTest{
				{
					Section:     "a.1",
					SectionDesc: "testing the test",
					TestNumber:  "1",
					TestDesc:    "making sure that we're right",
					TestInfo:    "information is fluid",
					Status:      "Just swell",
					Scored:      true,
				},
			},
		}
		bulk, err := tenantCLI.Compliance(cluster).Benchmarks().Create(ctx, []v1.Benchmarks{benchmarks})
		require.ErrorContains(t, err, "Bad tenant identifier")
		require.Nil(t, bulk)

		// Try a read as well.
		params := v1.BenchmarksParams{ID: benchmarks.UID()}
		resp, err := tenantCLI.Compliance(cluster).Benchmarks().List(ctx, &params)
		require.ErrorContains(t, err, "Bad tenant identifier")
		require.Nil(t, resp)
	})
}

func benchmarksWithUTCTime(resp *v1.List[v1.Benchmarks]) []v1.Benchmarks {
	for idx, benchmark := range resp.Items {
		utcTime := benchmark.Timestamp.UTC()
		resp.Items[idx].Timestamp = metav1.Time{Time: utcTime}
		resp.Items[idx].ID = ""
	}
	return resp.Items
}
