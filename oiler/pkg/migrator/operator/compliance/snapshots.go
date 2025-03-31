// Copyright (c) 2025 Tigera, Inc. All rights reserved.

package compliance

import (
	"context"
	"time"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator"
)

type SnapshotsOperator struct {
	backend              bapi.SnapshotsBackend
	clusterInfo          bapi.ClusterInfo
	queryByGeneratedTime bool
}

func (f SnapshotsOperator) Read(ctx context.Context, current operator.TimeInterval, pageSize int) (*v1.List[v1.Snapshot], *operator.TimeInterval, error) {
	logParams := v1.SnapshotParams{
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
		lastGeneratedTime = items[len(items)-1].ResourceList.GeneratedTime
	} else {
		lastGeneratedTimeFromCursor := current.LastGeneratedTime()
		lastGeneratedTime = &lastGeneratedTimeFromCursor
	}

	return list, operator.Next(list.GetAfterKey(), lastGeneratedTime, current.Start), nil

}

func (f SnapshotsOperator) Write(ctx context.Context, items []v1.Snapshot) (*v1.BulkResponse, error) {
	return f.backend.Create(ctx, f.clusterInfo, items)
}

func (f SnapshotsOperator) Transform(items []v1.Snapshot) []string {
	var result []string
	for _, item := range items {
		result = append(result, item.ID)
	}
	return result
}

func NewSnapshotsOperator(backend bapi.SnapshotsBackend, clusterInfo bapi.ClusterInfo, queryByGeneratedTime bool) SnapshotsOperator {
	return SnapshotsOperator{
		backend:              backend,
		clusterInfo:          clusterInfo,
		queryByGeneratedTime: queryByGeneratedTime,
	}
}
