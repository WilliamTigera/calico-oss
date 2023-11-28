// Copyright (c) 2023 Tigera, Inc. All rights reserved.
package index

import (
	"time"

	"github.com/olivere/elastic/v7"

	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	lmav1 "github.com/projectcalico/calico/lma/pkg/apis/v1"

	apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
)

// FlowLogs returns an instance of the flow logs index helper that uses a single index.
func SingleIndexBenchmarks() Helper {
	return benchmarksIndexHelper{singleIndex: true}
}

// LegacyFlowLogs returns an instance of the flow logs index helper.
func MultiIndexBenchmarks() Helper {
	return benchmarksIndexHelper{}
}

// benchmarksIndexHelper implements the Helper interface for flow logs.
type benchmarksIndexHelper struct {
	singleIndex bool
}

func (h benchmarksIndexHelper) BaseQuery(i bapi.ClusterInfo) *elastic.BoolQuery {
	q := elastic.NewBoolQuery()
	if h.singleIndex {
		q.Must(elastic.NewTermQuery("cluster", i.Cluster))
		if i.Tenant != "" {
			q.Must(elastic.NewTermQuery("tenant", i.Tenant))
		}
	}
	return q
}

func (h benchmarksIndexHelper) NewSelectorQuery(selector string) (elastic.Query, error) {
	return nil, nil
}

func (h benchmarksIndexHelper) NewRBACQuery(resources []apiv3.AuthorizedResourceVerbs) (elastic.Query, error) {
	return nil, nil
}

func (h benchmarksIndexHelper) NewTimeRangeQuery(r *lmav1.TimeRange) elastic.Query {
	unset := time.Time{}
	tr := elastic.NewRangeQuery(GetTimeFieldForQuery(h, r))
	if r.From != unset {
		tr.From(r.From)
	}
	if r.To != unset {
		tr.To(r.To)
	}
	return tr
}

func (h benchmarksIndexHelper) GetTimeField() string {
	return "timestamp"
}
