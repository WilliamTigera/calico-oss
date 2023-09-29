// Copyright (c) 2023 Tigera All rights reserved.

package compliance_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	backendutils "github.com/projectcalico/calico/linseed/pkg/backend/testutils"
	lmav1 "github.com/projectcalico/calico/lma/pkg/apis/v1"
)

func TestBenchmarksBasic(t *testing.T) {
	RunAllModes(t, "invalid ClusterInfo", func(t *testing.T) {
		f := v1.Benchmarks{}
		p := v1.BenchmarksParams{}

		// Empty cluster info.
		empty := bapi.ClusterInfo{}
		_, err := bb.Create(ctx, empty, []v1.Benchmarks{f})
		require.Error(t, err)
		_, err = bb.List(ctx, empty, &p)
		require.Error(t, err)

		// Invalid tenant ID in cluster info.
		badTenant := bapi.ClusterInfo{Cluster: cluster, Tenant: "one,two"}
		_, err = bb.Create(ctx, badTenant, []v1.Benchmarks{f})
		require.Error(t, err)
		_, err = bb.List(ctx, badTenant, &p)
		require.Error(t, err)
	})

	// Run each test with a tenant specified, and also without a tenant.
	for _, tenant := range []string{backendutils.RandomTenantName(), ""} {
		name := fmt.Sprintf("create and retrieve benchmarks (tenant=%s)", tenant)
		RunAllModes(t, name, func(t *testing.T) {
			clusterInfo.Tenant = tenant

			f := v1.Benchmarks{
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

			response, err := bb.Create(ctx, clusterInfo, []v1.Benchmarks{f})
			require.NoError(t, err)
			require.Equal(t, []v1.BulkError(nil), response.Errors)
			require.Equal(t, 0, response.Failed)

			err = backendutils.RefreshIndex(ctx, client, bIndexGetter.Index(clusterInfo))
			require.NoError(t, err)

			// Read it back and check it matches.
			p := v1.BenchmarksParams{}
			resp, err := bb.List(ctx, clusterInfo, &p)
			require.NoError(t, err)
			require.Len(t, resp.Items, 1)
			require.NotEqual(t, "", resp.Items[0].ID)
			resp.Items[0].ID = ""
			require.Equal(t, f, resp.Items[0])
		})
	}
}

func TestBenchmarksFiltering(t *testing.T) {
	type testcase struct {
		Name    string
		Params  *v1.BenchmarksParams
		Expect1 bool
		Expect2 bool
	}

	testcases := []testcase{
		{
			Name: "should filter benchmarks based on ID",
			Params: &v1.BenchmarksParams{
				ID: "bm1",
			},
			Expect1: true,
			Expect2: false,
		},
		{
			Name: "should filter benchmarks based on Type",
			Params: &v1.BenchmarksParams{
				Type: v1.TypeKubernetes,
			},
			Expect1: true,
			Expect2: false,
		},
		{
			Name: "should filter benchmarks based on Version",
			Params: &v1.BenchmarksParams{
				Filters: []v1.BenchmarksFilter{
					{Version: "v2"},
				},
			},
			Expect1: false,
			Expect2: true,
		},
		{
			Name: "should filter benchmarks based on multiple versions",
			Params: &v1.BenchmarksParams{
				Filters: []v1.BenchmarksFilter{
					{Version: "v1"},
					{Version: "v2"},
				},
			},
			Expect1: true,
			Expect2: true,
		},
		{
			Name: "should filter benchmarks based on a single node name",
			Params: &v1.BenchmarksParams{
				Filters: []v1.BenchmarksFilter{
					{NodeNames: []string{"golem"}},
				},
			},
			Expect1: false,
			Expect2: true,
		},
		{
			Name: "should filter benchmarks based on multiple node names",
			Params: &v1.BenchmarksParams{
				Filters: []v1.BenchmarksFilter{
					{NodeNames: []string{"lodestone", "golem"}},
				},
			},
			Expect1: true,
			Expect2: true,
		},
		{
			Name: "should filter benchmarks based on timestamp",
			Params: &v1.BenchmarksParams{
				QueryParams: v1.QueryParams{
					TimeRange: &lmav1.TimeRange{
						From: time.Unix(1000, 0),
						To:   time.Unix(3000, 0),
					},
				},
			},
			Expect1: false,
			Expect2: true,
		},
	}

	for _, tc := range testcases {
		// Run each test with a tenant specified, and also without a tenant.
		for _, tenant := range []string{backendutils.RandomTenantName(), ""} {
			name := fmt.Sprintf("%s (tenant=%s)", tc.Name, tenant)
			RunAllModes(t, name, func(t *testing.T) {
				clusterInfo.Tenant = tenant

				bm1 := v1.Benchmarks{
					ID:                "bm1",
					Version:           "v1",
					KubernetesVersion: "v1.0",
					Type:              v1.TypeKubernetes,
					NodeName:          "lodestone",
					Timestamp:         metav1.Time{Time: time.Unix(10, 0)},
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
				bm2 := v1.Benchmarks{
					ID:                "bm2",
					Version:           "v2",
					KubernetesVersion: "v1.2",
					Type:              "unknownType",
					NodeName:          "golem",
					Timestamp:         metav1.Time{Time: time.Unix(2000, 0)},
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

				response, err := bb.Create(ctx, clusterInfo, []v1.Benchmarks{bm1, bm2})
				require.NoError(t, err)
				require.Equal(t, []v1.BulkError(nil), response.Errors)
				require.Equal(t, 0, response.Failed)

				err = backendutils.RefreshIndex(ctx, client, bIndexGetter.Index(clusterInfo))
				require.NoError(t, err)

				resp, err := bb.List(ctx, clusterInfo, tc.Params)
				require.NoError(t, err)

				if tc.Expect1 {
					require.Contains(t, resp.Items, bm1)
				} else {
					require.NotContains(t, resp.Items, bm1)
				}
				if tc.Expect2 {
					require.Contains(t, resp.Items, bm2)
				} else {
					require.NotContains(t, resp.Items, bm2)
				}

				// A non-matching tenant should never return any results.
				// Note that in production, the backend should never receive a request with
				// an unexpected tenant ID because Linseed rejects this earlier in the stack,
				// but we should still handle it properly.
				badClusterInfo := clusterInfo
				badClusterInfo.Tenant = "bad-tenant"
				resp, err = bb.List(ctx, badClusterInfo, tc.Params)
				require.NoError(t, err)
				require.Empty(t, resp.Items)
			})
		}
	}
}

func TestBenchmarkSorting(t *testing.T) {
	RunAllModes(t, "should respect sorting", func(t *testing.T) {
		clusterInfo := bapi.ClusterInfo{Cluster: cluster}

		t1 := time.Unix(100, 0)
		t2 := time.Unix(500, 0)

		bm1 := v1.Benchmarks{
			ID:                "bm1",
			Version:           "v1",
			KubernetesVersion: "v1.0",
			Type:              v1.TypeKubernetes,
			NodeName:          "lodestone",
			Timestamp:         metav1.Time{Time: t1},
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
		bm2 := v1.Benchmarks{
			ID:                "bm2",
			Version:           "v2",
			KubernetesVersion: "v1.2",
			Type:              "unknownType",
			NodeName:          "golem",
			Timestamp:         metav1.Time{Time: t2},
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

		_, err := bb.Create(ctx, clusterInfo, []v1.Benchmarks{bm1, bm2})
		require.NoError(t, err)

		err = backendutils.RefreshIndex(ctx, client, bIndexGetter.Index(clusterInfo))
		require.NoError(t, err)

		// Query for flow logs without sorting.
		params := v1.BenchmarksParams{}
		r, err := bb.List(ctx, clusterInfo, &params)
		require.NoError(t, err)
		require.Len(t, r.Items, 2)
		require.Nil(t, r.AfterKey)

		// Assert that the logs are returned in the correct order.
		require.Equal(t, bm1, r.Items[1])
		require.Equal(t, bm2, r.Items[0])

		// Query again, this time sorting in order to get the logs in reverse order.
		params.Sort = []v1.SearchRequestSortBy{
			{
				Field:      "timestamp",
				Descending: false,
			},
		}
		r, err = bb.List(ctx, clusterInfo, &params)
		require.NoError(t, err)
		require.Len(t, r.Items, 2)
		require.Equal(t, bm2, r.Items[1])
		require.Equal(t, bm1, r.Items[0])
	})
}
