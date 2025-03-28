// Copyright (c) 2025 Tigera, Inc. All rights reserved.

package operator

import (
	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	lmaapi "github.com/projectcalico/calico/lma/pkg/apis/v1"
)

// QueryParams maps the ES queries we need to perform to read
// data. We are querying using field generated_time. This is populated
// by Linseed when ingesting data. We can start from time zero or from
// a specific point in time. If we are in the middle of a pagination
// request we need to set the after key to read the next page.
func QueryParams(pageSize int, interval TimeInterval, useGeneratedTime bool) v1.QueryParams {
	timeRange := &lmaapi.TimeRange{}
	if useGeneratedTime {
		timeRange = &lmaapi.TimeRange{
			Field: "generated_time",
		}
	}
	params := v1.QueryParams{
		MaxPageSize: pageSize,
		TimeRange:   timeRange,
	}
	if interval.Start != nil {
		params.TimeRange.From = *interval.Start
	}
	if interval.End != nil {
		params.TimeRange.To = *interval.End
	}
	if interval.Cursor != nil {
		params.AfterKey = interval.Cursor
	}

	return params
}

// SortParameters represents how we sort data when
// reading. We need to sort ascendant using generated_time
// to slowly advance through all the data stored in
// Elasticsearch
func SortParameters(useGeneratedTime bool) []v1.SearchRequestSortBy {
	if useGeneratedTime {
		return []v1.SearchRequestSortBy{
			{
				Field: "generated_time",
			},
		}
	}
	return []v1.SearchRequestSortBy{}
}
