// Copyright (c) 2025 Tigera, Inc. All rights reserved.

package waf

import (
	"context"
	"time"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator"
)

type Operator struct {
	backend              bapi.WAFBackend
	clusterInfo          bapi.ClusterInfo
	queryByGeneratedTime bool
}

func (f Operator) Read(ctx context.Context, current operator.TimeInterval, pageSize int) (*v1.List[v1.WAFLog], *operator.TimeInterval, error) {
	logParams := v1.WAFLogParams{
		QueryParams:     operator.QueryParams(pageSize, current, f.queryByGeneratedTime),
		QuerySortParams: v1.QuerySortParams{Sort: operator.SortParameters(f.queryByGeneratedTime)},
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

func (f Operator) Write(ctx context.Context, items []v1.WAFLog) (*v1.BulkResponse, error) {
	return f.backend.Create(ctx, f.clusterInfo, items)
}

func (f Operator) Transform(items []v1.WAFLog) []string {
	var result []string
	for _, item := range items {
		result = append(result, item.ID)
	}
	return result
}

func NewOperator(backend bapi.WAFBackend, clusterInfo bapi.ClusterInfo, queryByGeneratedTime bool) Operator {
	return Operator{
		backend:              backend,
		clusterInfo:          clusterInfo,
		queryByGeneratedTime: queryByGeneratedTime,
	}
}
