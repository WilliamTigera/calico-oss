// Copyright (c) 2025 Tigera, Inc. All rights reserved.

package compliance

import (
	"context"
	"time"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator"
)

type BenchmarksOperator struct {
	backend              bapi.BenchmarksBackend
	clusterInfo          bapi.ClusterInfo
	queryByGeneratedTime bool
}

func (f BenchmarksOperator) Read(ctx context.Context, current operator.TimeInterval, pageSize int) (*v1.List[v1.Benchmarks], *operator.TimeInterval, error) {
	logParams := v1.BenchmarksParams{
		QueryParams: operator.QueryParams(pageSize, current, f.queryByGeneratedTime),
		Sort:        operator.SortParameters(f.queryByGeneratedTime),
	}

	list, err := f.backend.List(ctx, f.clusterInfo, &logParams)
	if err != nil {
		return nil, nil, err
	}

	var lastGeneratedTime *time.Time
	items := list.Items
	if len(items) != 0 {
		lastGeneratedTime = items[len(items)-1].GeneratedTime
	} else {
		lastGeneratedTimeFromCursor := current.LastGeneratedTime()
		lastGeneratedTime = &lastGeneratedTimeFromCursor
	}

	return list, operator.Next(list.GetAfterKey(), lastGeneratedTime, current.Start), nil

}

func (f BenchmarksOperator) Write(ctx context.Context, items []v1.Benchmarks) (*v1.BulkResponse, error) {
	return f.backend.Create(ctx, f.clusterInfo, items)
}

func (f BenchmarksOperator) Transform(items []v1.Benchmarks) []string {
	var result []string
	for _, item := range items {
		result = append(result, item.ID)
	}
	return result
}

func NewBenchmarksOperator(backend bapi.BenchmarksBackend, clusterInfo bapi.ClusterInfo, queryByGeneratedTime bool) BenchmarksOperator {
	return BenchmarksOperator{
		backend:              backend,
		clusterInfo:          clusterInfo,
		queryByGeneratedTime: queryByGeneratedTime,
	}
}
