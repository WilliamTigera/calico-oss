// Copyright (c) 2023 Tigera, Inc. All rights reserved.

//go:build fvtests

package fv_test

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/projectcalico/calico/linseed/pkg/client"

	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/index"
	"github.com/projectcalico/calico/linseed/pkg/backend/testutils"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	"github.com/projectcalico/calico/linseed/pkg/config"
	lmav1 "github.com/projectcalico/calico/lma/pkg/apis/v1"
)

func RunEventsTest(t *testing.T, name string, testFn func(*testing.T, bapi.Index)) {
	t.Run(fmt.Sprintf("%s [MultiIndex]", name), func(t *testing.T) {
		args := DefaultLinseedArgs()
		defer setupAndTeardown(t, args, index.EventsMultiIndex)()
		testFn(t, index.EventsMultiIndex)
	})

	t.Run(fmt.Sprintf("%s [SingleIndex]", name), func(t *testing.T) {
		configureIndices := RunConfigureElasticLinseed(t, &RunConfigureElasticArgs{
			AlertBaseIndexName: index.AlertsIndex().Name(bapi.ClusterInfo{}),
			AlertPolicyName:    index.AlertsIndex().ILMPolicyName(),
		})
		if configureIndices.ListedInDockerPS() {
			configureIndices.Stop()
		}
		args := DefaultLinseedArgs()
		args.Backend = config.BackendTypeSingleIndex
		defer setupAndTeardown(t, args, index.AlertsIndex())()
		testFn(t, index.AlertsIndex())
	})
}

func TestFV_Events(t *testing.T) {
	RunEventsTest(t, "should return an empty list if there are no events", func(t *testing.T, idx bapi.Index) {
		params := v1.EventParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: time.Now().Add(-5 * time.Second),
					To:   time.Now(),
				},
			},
		}

		// Perform a query.
		events, err := cli.Events(cluster).List(ctx, &params)
		require.NoError(t, err)
		require.Equal(t, []v1.Event{}, events.Items)
	})

	RunEventsTest(t, "should create and list events", func(t *testing.T, idx bapi.Index) {
		// Create a basic event.
		events := []v1.Event{
			{
				Time:        v1.NewEventTimestamp(time.Now().Unix()),
				Description: "A rather uneventful evening",
				Origin:      "TODO",
				Severity:    1,
				Type:        "TODO",
			},
		}
		bulk, err := cli.Events(cluster).Create(ctx, events)
		require.NoError(t, err)
		require.Equal(t, bulk.Succeeded, 1, "create event did not succeed")

		// Refresh elasticsearch so that results appear.
		testutils.RefreshIndex(ctx, lmaClient, idx.Index(clusterInfo))

		// Read it back.
		params := v1.EventParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: time.Now().Add(-5 * time.Second),
					To:   time.Now().Add(5 * time.Second),
				},
			},
		}
		resp, err := cli.Events(cluster).List(ctx, &params)
		require.NoError(t, err)

		// The ID should be set, but random, so we can't assert on its value.
		require.Equal(t, events, testutils.AssertLogIDAndCopyEventsWithoutID(t, resp))
	})

	RunEventsTest(t, "should dismiss and delete events", func(t *testing.T, idx bapi.Index) {
		// Create a basic event.
		events := []v1.Event{
			{
				ID:          "ABC",
				Time:        v1.NewEventTimestamp(time.Now().Unix()),
				Description: "A rather uneventful evening",
				Origin:      "TODO",
				Severity:    1,
				Type:        "TODO",
			},
		}
		bulk, err := cli.Events(cluster).Create(ctx, events)
		require.NoError(t, err)
		require.Equal(t, bulk.Succeeded, 1, "create event did not succeed")

		// Refresh elasticsearch so that results appear.
		testutils.RefreshIndex(ctx, lmaClient, idx.Index(clusterInfo))

		// Read it back.
		params := v1.EventParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: time.Now().Add(-5 * time.Second),
					To:   time.Now().Add(5 * time.Second),
				},
			},
		}
		resp, err := cli.Events(cluster).List(ctx, &params)
		require.NoError(t, err)

		// The ID should be set, it should not be dismissed.
		require.NotEqual(t, "", resp.Items[0].ID)
		require.False(t, resp.Items[0].Dismissed)

		// We should be able to dismiss the event.
		bulk, err = cli.Events(cluster).UpdateDismissFlag(ctx, []v1.Event{{ID: "ABC", Dismissed: true}})
		require.NoError(t, err)
		require.Equal(t, bulk.Succeeded, 1, "dismiss event did not succeed")

		// Reading it back should show the event as dismissed.
		testutils.RefreshIndex(ctx, lmaClient, idx.Index(clusterInfo))
		resp, err = cli.Events(cluster).List(ctx, &params)
		require.NoError(t, err)
		require.Len(t, resp.Items, 1)
		require.True(t, resp.Items[0].Dismissed)

		// Now, delete the event.
		bulk, err = cli.Events(cluster).Delete(ctx, []v1.Event{{ID: "ABC"}})
		require.NoError(t, err)
		require.Equal(t, bulk.Succeeded, 1, "delete event did not succeed")

		// Reading it back should show the no events.
		testutils.RefreshIndex(ctx, lmaClient, idx.Index(clusterInfo))
		resp, err = cli.Events(cluster).List(ctx, &params)
		require.NoError(t, err)
		require.Len(t, resp.Items, 0)
	})

	RunEventsTest(t, "should support pagination", func(t *testing.T, idx bapi.Index) {
		totalItems := 5

		// Create 5 events.
		logTime := time.Unix(100, 0).UTC()
		for i := 0; i < totalItems; i++ {
			events := []v1.Event{
				{
					Time: v1.NewEventTimestamp(logTime.Unix() + int64(i)), // Make sure events are ordered.
					Host: fmt.Sprintf("%d", i),
				},
			}
			bulk, err := cli.Events(cluster).Create(ctx, events)
			require.NoError(t, err)
			require.Equal(t, bulk.Succeeded, 1, "create events did not succeed")
		}

		// Refresh elasticsearch so that results appear.
		testutils.RefreshIndex(ctx, lmaClient, idx.Index(clusterInfo))

		// Read them back one at a time.
		var afterKey map[string]interface{}
		for i := 0; i < totalItems-1; i++ {
			params := v1.EventParams{
				QueryParams: v1.QueryParams{
					TimeRange: &lmav1.TimeRange{
						From: logTime.Add(-5 * time.Second),
						To:   logTime.Add(5 * time.Second),
					},
					MaxPageSize: 1,
					AfterKey:    afterKey,
				},
			}
			resp, err := cli.Events(cluster).List(ctx, &params)
			require.NoError(t, err)
			require.Equal(t, 1, len(resp.Items))
			require.Equal(t, []v1.Event{
				{
					Time: v1.NewEventTimestamp(logTime.Unix() + int64(i)),
					Host: fmt.Sprintf("%d", i),
				},
			}, testutils.AssertLogIDAndCopyEventsWithoutID(t, resp), fmt.Sprintf("Event #%d did not match", i))
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
		params := v1.EventParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: logTime.Add(-5 * time.Second),
					To:   logTime.Add(5 * time.Second),
				},
				MaxPageSize: 1,
				AfterKey:    afterKey,
			},
		}
		resp, err := cli.Events(cluster).List(ctx, &params)
		require.NoError(t, err)
		require.Equal(t, 1, len(resp.Items))
		require.Equal(t, []v1.Event{
			{
				Time: v1.NewEventTimestamp(logTime.Unix() + int64(lastItem)),
				Host: fmt.Sprintf("%d", lastItem),
			},
		}, testutils.AssertLogIDAndCopyEventsWithoutID(t, resp), fmt.Sprintf("Event #%d did not match", lastItem))
		require.Equal(t, resp.TotalHits, int64(totalItems))

		// Once we reach the end of the data, we should not receive
		// an afterKey
		require.Nil(t, resp.AfterKey)
	})

	RunEventsTest(t, "should support pagination for items >= 10000 for events", func(t *testing.T, idx bapi.Index) {
		totalItems := 10001
		// Create > 10K events.
		logTime := time.Now().UTC()
		var events []v1.Event
		// add events with timestamp format
		for i := 0; i < totalItems; i++ {
			events = append(events, v1.Event{
				ID:   strconv.Itoa(i + 1),
				Time: v1.NewEventTimestamp(logTime.Add(time.Duration(i+1) * time.Second).Unix()), // Make sure events are ordered.
				Host: fmt.Sprintf("%d", i+1),
			},
			)
		}

		bulk, err := cli.Events(cluster).Create(ctx, events)
		require.NoError(t, err)
		require.Equal(t, totalItems, bulk.Total, "create events did not succeed")

		// Refresh elasticsearch so that results appear.
		testutils.RefreshIndex(ctx, lmaClient, idx.Index(clusterInfo))

		// Stream through all the items.
		params := v1.EventParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: time.Time{},
					To:   time.Now().Add(time.Duration(2*totalItems) * time.Minute),
				},
				MaxPageSize: 1000,
			},
		}

		pager := client.NewListPager[v1.Event](&params)
		pages, errors := pager.Stream(ctx, cli.Events(cluster).List)

		receivedItems := 0
		for page := range pages {
			receivedItems = receivedItems + len(page.Items)
			logrus.Infof("Total Hits is %d", page.TotalHits)
		}

		if err, ok := <-errors; ok {
			require.NoError(t, err)
		}

		require.Equal(t, receivedItems, totalItems)
	})

	RunEventsTest(t, "should support pagination for items >= 10000 for events with timestamps in different formats", func(t *testing.T, idx bapi.Index) {
		totalItems := 10001
		// Create > 10K events.
		logTime := time.Now().UTC()
		var events []v1.Event
		// add events with timestamp format
		for i := 0; i < totalItems/2; i++ {
			events = append(events, v1.Event{
				ID:   strconv.Itoa(i + 1),
				Time: v1.NewEventTimestamp(logTime.Add(time.Duration(i+1) * time.Second).Unix()), // Make sure events are ordered.
				Host: fmt.Sprintf("%d", i+1),
			},
			)
		}

		// add additional events with ISO format
		for i := totalItems / 2; i < totalItems; i++ {
			events = append(events, v1.Event{
				ID:   strconv.Itoa(totalItems + i + 1),
				Time: v1.NewEventDate(logTime.Add(time.Duration(i+1+totalItems) * time.Second)), // Make sure events are ordered.
				Host: fmt.Sprintf("%d", i+1+totalItems),
			},
			)
		}

		bulk, err := cli.Events(cluster).Create(ctx, events)
		require.NoError(t, err)
		require.Equal(t, totalItems, bulk.Total, "create events did not succeed")

		// Refresh elasticsearch so that results appear.
		testutils.RefreshIndex(ctx, lmaClient, idx.Index(clusterInfo))

		// Stream through all the items.
		params := v1.EventParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: time.Time{},
					To:   time.Now().Add(time.Duration(2*totalItems) * time.Minute),
				},
				MaxPageSize: 1000,
			},
		}

		pager := client.NewListPager[v1.Event](&params)
		pages, errors := pager.Stream(ctx, cli.Events(cluster).List)

		receivedItems := 0
		for page := range pages {
			receivedItems = receivedItems + len(page.Items)
			logrus.Infof("Total Hits is %d", page.TotalHits)
		}

		if err, ok := <-errors; ok {
			require.NoError(t, err)
		}

		require.Equal(t, receivedItems, totalItems)
	})
}

func TestFV_EventFiltering(t *testing.T) {
	// Define events to be used in the test.
	ip := "172.17.0.1"
	startTime := time.Unix(1, 0)
	eventTime := time.Unix(2, 0)
	endTime := time.Unix(3, 0)
	events := []v1.Event{
		{
			Time:        v1.NewEventTimestamp(eventTime.Unix()),
			Description: "A rather uneventful evening",
			Origin:      "TODO",
			Severity:    1,
			Type:        "TODO",
		},
		{
			Time:            v1.NewEventTimestamp(eventTime.Unix()),
			Description:     "A suspicious DNS query",
			Origin:          "TODO",
			Severity:        1,
			Type:            "suspicious_dns_query",
			SourceName:      "my-source-name-123",
			SourceNamespace: "my-app-namespace",
			SourceIP:        &ip,
		},
		{
			Time:            v1.NewEventTimestamp(eventTime.Unix()),
			Description:     "A NOT so suspicious DNS query",
			Origin:          "TODO",
			Severity:        1,
			Type:            "suspicious_dns_query",
			SourceName:      "my-source-name-456",
			SourceNamespace: "my-app-namespace",
			SourceIP:        &ip,
		},
	}

	// Build up array of test cases.
	type eventFilterTest struct {
		selector       string
		expectedEvents []v1.Event
	}
	tests := []eventFilterTest{
		{
			"type IN { suspicious_dns_query, gtf_suspicious_dns_query } " +
				// `in` with a value allows us to use wildcards
				"AND \"source_name\" in {\"*source-name-123\"} " +
				// and here we're doing an exact match
				"AND \"source_namespace\" = \"my-app-namespace\" " +
				"AND 'source_ip' >= '172.16.0.0' AND source_ip <= '172.32.0.0'",
			[]v1.Event{events[1]},
		},
		{
			"NOT (type IN { suspicious_dns_query, gtf_suspicious_dns_query })",
			[]v1.Event{events[0]},
		},
		{
			"type IN { suspicious_dns_query, gtf_suspicious_dns_query } ",
			[]v1.Event{events[1], events[2]},
		},
		{"source_namespace IN {'app'}", nil},
		{"source_namespace IN {'*app*'}", []v1.Event{events[1], events[2]}},
		{"source_name IN {'my-*-123'}", []v1.Event{events[1]}},
		{"'source_ip' >= '172.16.0.0' AND source_ip <= '172.32.0.0'", []v1.Event{events[1], events[2]}},
		{"'source_ip' >= '172.16.0.0' AND source_ip <= '172.17.0.0'", nil},
	}

	for _, tt := range tests {
		name := fmt.Sprintf("filter events with selector: %s", tt.selector)
		RunEventsTest(t, name, func(t *testing.T, idx bapi.Index) {
			// Create all events.
			bulk, err := cli.Events(cluster).Create(ctx, events)
			require.NoError(t, err)
			require.Equal(t, bulk.Succeeded, 3, "create event did not succeed")

			// Refresh elasticsearch so that results appear.
			err = testutils.RefreshIndex(ctx, lmaClient, idx.Index(clusterInfo))
			require.NoError(t, err)

			// Read it back.
			params := v1.EventParams{
				QueryParams:        v1.QueryParams{TimeRange: &lmav1.TimeRange{From: startTime, To: endTime}},
				LogSelectionParams: v1.LogSelectionParams{Selector: tt.selector},
			}
			resp, err := cli.Events(cluster).List(ctx, &params)
			require.NoError(t, err)

			// The ID should be set, but random, so we can't assert on its value.
			require.Equal(t, tt.expectedEvents, testutils.AssertLogIDAndCopyEventsWithoutID(t, resp))
		})
	}
}

func TestFV_EventsTenancy(t *testing.T) {
	RunEventsTest(t, "should support tenancy restriction", func(t *testing.T, idx bapi.Index) {
		// Instantiate a client for an unexpected tenant.
		args := DefaultLinseedArgs()
		args.TenantID = "bad-tenant"
		tenantCLI, err := NewLinseedClient(args)
		require.NoError(t, err)

		// Create a basic log. We expect this to fail, since we're using
		// an unexpected tenant ID on the request.
		events := []v1.Event{
			{
				Time:        v1.NewEventTimestamp(time.Now().Unix()),
				Description: "A rather uneventful evening",
				Origin:      "TODO",
				Severity:    1,
				Type:        "TODO",
			},
		}
		bulk, err := tenantCLI.Events(cluster).Create(ctx, events)
		require.ErrorContains(t, err, "Bad tenant identifier")
		require.Nil(t, bulk)

		// Try a read as well.
		params := v1.EventParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: time.Now().Add(-5 * time.Second),
					To:   time.Now().Add(5 * time.Second),
				},
			},
		}
		resp, err := tenantCLI.Events(cluster).List(ctx, &params)
		require.ErrorContains(t, err, "Bad tenant identifier")
		require.Nil(t, resp)
	})
}
