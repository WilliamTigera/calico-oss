// Copyright (c) 2025 Tigera, Inc. All rights reserved.

package audit

import (
	"context"
	"time"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator"
)

type Operator struct {
	auditBackend bapi.AuditBackend
	clusterInfo  bapi.ClusterInfo
	auditType    v1.AuditLogType
}

func (a Operator) Read(ctx context.Context, current operator.TimeInterval, pageSize int) (*v1.List[v1.AuditLog], *operator.TimeInterval, error) {
	logParams := v1.AuditLogParams{
		QueryParams: operator.QueryParams(pageSize, current),
		Sort:        operator.SortParameters(),
		Type:        a.auditType,
	}

	list, err := a.auditBackend.List(ctx, a.clusterInfo, &logParams)
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

func (a Operator) Write(ctx context.Context, items []v1.AuditLog) (*v1.BulkResponse, error) {
	return a.auditBackend.Create(ctx, a.auditType, a.clusterInfo, items)
}

func NewOperator(auditType v1.AuditLogType, backend bapi.AuditBackend, clusterInfo bapi.ClusterInfo) Operator {
	return Operator{
		auditType:    auditType,
		auditBackend: backend,
		clusterInfo:  clusterInfo,
	}
}
