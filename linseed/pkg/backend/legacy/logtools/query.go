// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package logtools

import (
	"context"
	"fmt"
	"strconv"
	"time"

	lmav1 "github.com/projectcalico/calico/lma/pkg/apis/v1"

	"github.com/olivere/elastic/v7"
	"github.com/sirupsen/logrus"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	lmaindex "github.com/projectcalico/calico/linseed/pkg/internal/lma/elastic/index"
)

// BuildQuery builds an elastic log query using the given parameters.
func BuildQuery(h lmaindex.Helper, i bapi.ClusterInfo, opts v1.LogParams, start time.Time, end time.Time) (*elastic.BoolQuery, error) {
	query := h.BaseQuery(i)

	// Parse times from the request. We default to a time-range query
	// if no other search parameters are given.
	query.Filter(h.NewTimeRangeQuery(start, end))

	// If RBAC constraints were given, add them in.
	if perms := opts.GetPermissions(); len(perms) > 0 {
		rbacQuery, err := h.NewRBACQuery(perms)
		if err != nil {
			return nil, err
		}
		if rbacQuery != nil {
			query.Filter(rbacQuery)
		}
	}

	// If a selector was provided, parse it and add it in.
	if sel := opts.GetSelector(); len(sel) > 0 {
		selQuery, err := h.NewSelectorQuery(sel)
		if err != nil {
			return nil, err
		}
		if selQuery != nil {
			query.Must(selQuery)
		}
	}

	return query, nil
}

func ExtractTimeRange(timeRange *lmav1.TimeRange) (time.Time, time.Time) {
	// Parse times from the request. We default to a time-range query
	// if no other search parameters are given.
	var start, end time.Time
	if timeRange != nil {
		start = timeRange.From
		end = timeRange.To
	} else {
		// Default to the start of the timeline
		start = time.Time{}
		end = time.Now()
	}
	return start, end
}

// StartFrom parses the given parameters to determine which log to start from in the ES query.
func StartFrom(opts v1.Params) (int, error) {
	if ak := opts.GetAfterKey(); ak != nil {
		if val, ok := ak["startFrom"]; ok {
			switch v := val.(type) {
			case string:
				if sf, err := strconv.Atoi(v); err == nil {
					return sf, nil
				} else {
					return 0, fmt.Errorf("Could not parse startFrom (%s) as an integer", v)
				}
			case float64:
				logrus.WithField("val", val).Trace("Handling float64 startFrom")
				return int(v), nil
			case int:
				logrus.WithField("val", val).Trace("Handling int startFrom")
				return v, nil
			default:
				logrus.WithField("val", val).Warnf("Unexpected type (%T) for startFrom, will not perform paging", val)
			}
		}
	}
	logrus.Trace("Starting query from 0")
	return 0, nil
}

// searchFrom parses the given parameters to determine which log to start from in the ES query for deep pagination
func searchFrom(opts v1.Params) ([]interface{}, error) {
	if ak := opts.GetAfterKey(); ak != nil {
		if val, ok := ak["searchFrom"]; ok {
			switch v := val.(type) {
			case []interface{}:
				logrus.WithField("val", val).Trace("Handling array searchFrom")
				return v, nil
			default:
				logrus.WithField("val", val).Warnf("Unexpected type (%T) for searchFrom, will not perform paging", val)
			}
		}
	}
	logrus.Trace("Starting query without search from")
	return nil, nil
}

func pointInTime(opts v1.Params) (*string, error) {
	if ak := opts.GetAfterKey(); ak != nil {
		if val, ok := ak["pit"]; ok {
			id, ok := val.(string)
			if !ok {
				return nil, fmt.Errorf("missing pit parameter")
			}

			return &id, nil
		}
	}
	return nil, nil
}

// NextStartFromAfterKey generates an AfterKey to use for log queries that use startFrom to pass
// the document index from which to start the next page of results.
func NextStartFromAfterKey(opts v1.Params, numHits, prevStartFrom int, totalHits int64) map[string]interface{} {
	var ak map[string]interface{}

	// Calculate the next starting point using the value received in the request
	// and the current hits returned on the query
	nextStartFrom := prevStartFrom + numHits

	if numHits < opts.GetMaxPageSize() || nextStartFrom >= int(totalHits) {
		// We fully satisfied the request, no afterkey.
		ak = nil
	} else {
		// There are more hits, return an afterKey the client can use for pagination.
		// We add the number of hits to the start from provided on the request, if any.
		ak = map[string]interface{}{
			"startFrom": nextStartFrom,
		}
	}
	return ak
}

// NextPointInTime retrieves the next point in time (a point in time is opened for ES queries that have
// more than 10000 items or other value configured via index.max_result_window setting). If a refresh
// occurs while we query an index with more than index.max_result_window setting items, the returned
// results might be inconsistent. A point in time will preserve the current index state.
// If an index has less than index.max_result_window setting, point in time will default an empty string.
func NextPointInTime(ctx context.Context, client *elastic.Client, index string, results *elastic.SearchResult,
	deepPaginationCutOff int64, log *logrus.Entry,
) (string, error) {
	var pitID string
	// This is how we determine that an index has more items
	// than index.max_result_window setting. TotalHits will
	// return a value equal to index.max_result_window setting
	if results.TotalHits() >= deepPaginationCutOff {
		if len(results.Hits.Hits) > 0 {
			if results.PitId == "" {
				// Create a new point in time in order to ensure results are returned in the correct order
				pointInTimeResponse, err := client.OpenPointInTime(index).KeepAlive("10s").Do(ctx)
				if err != nil {
					return pitID, err
				}
				pitID = pointInTimeResponse.Id
			} else {
				// Use the refreshed point in time that was returned by Elastic
				pitID = results.PitId
			}
		} else {
			// If we have reached the last page, and we are querying an index with more than
			// index.max_result_window items, we need to close the point in time to release resources
			if results.PitId != "" {
				resp, err := client.ClosePointInTime(results.PitId).Do(ctx)
				if err != nil {
					log.WithError(err).Warnf("Failed to close point in time %s", pitID)
					return "", nil
				}

				if !resp.Succeeded {
					log.Warnf("Failed to close point in time %s", pitID)
					return "", nil
				}
			}
		}
	}

	return pitID, nil
}

func ConfigureCurrentPage(query *elastic.SearchService, opts v1.Params, index string) (*elastic.SearchService, int, error) {
	// Get the startFrom param, if any.
	startFrom, err := StartFrom(opts)
	if err != nil {
		return nil, 0, err
	}

	// Get the searchAfter param, if any.
	searchAfter, err := searchFrom(opts)
	if err != nil {
		return nil, 0, err
	}

	// Get the pit param, if any.
	pit, err := pointInTime(opts)
	if err != nil {
		return nil, 0, err
	}

	if searchAfter == nil {
		// Queries for indices that have less than index.max_result_window items
		// require the index to be specified on the request
		query = query.From(startFrom).Index(index)
	} else {
		query = query.SearchAfter(searchAfter...)
		// Set the point in time for the next query and extend its lifetime
		if pit != nil {
			query.PointInTime(elastic.NewPointInTimeWithKeepAlive(*pit, "10s"))
		}
	}

	return query, startFrom, nil
}

// NextAfterKey will craft the AfterKey parameter present on the response based on the type of pagination used
func NextAfterKey(opts v1.Params, prevStartFrom int, pitID string, results *elastic.SearchResult, deepPaginationCutOff int64) map[string]interface{} {
	var afterKey map[string]interface{}
	// For requests over the index.max_result_window size cutoff, ES does not support the use of from parameter when
	// performing pagination. Instead, we must use search_after and create a point-in-time to iterate via documents.
	// This is more expensive, so for smaller requests we default to from.
	// Linseed API will use startFrom key for pagination using from parameter
	// and searchFrom key for pagination using search_from parameter together with
	// pit key
	if results.TotalHits() >= deepPaginationCutOff {
		if len(results.Hits.Hits) > 0 {
			sort := results.Hits.Hits[len(results.Hits.Hits)-1].Sort
			if sort != nil {
				afterKey = map[string]interface{}{
					"searchFrom": sort,
				}
				afterKey["pit"] = pitID
			}
		}
	} else {
		afterKey = NextStartFromAfterKey(opts, len(results.Hits.Hits), prevStartFrom, results.TotalHits())
	}

	return afterKey
}
