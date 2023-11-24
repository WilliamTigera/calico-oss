// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package waf

import (
	"context"
	"fmt"

	lmaindex "github.com/projectcalico/calico/linseed/pkg/internal/lma/elastic/index"

	"github.com/projectcalico/calico/libcalico-go/lib/json"

	"github.com/olivere/elastic/v7"
	"github.com/sirupsen/logrus"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	"github.com/projectcalico/calico/linseed/pkg/backend/api"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/index"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/logtools"
	lmaelastic "github.com/projectcalico/calico/lma/pkg/elastic"
)

type wafLogBackend struct {
	client               *elastic.Client
	lmaclient            lmaelastic.Client
	templates            bapi.IndexInitializer
	deepPaginationCutOff int64
	queryHelper          lmaindex.Helper
	singleIndex          bool
	index                bapi.Index
}

func NewBackend(c lmaelastic.Client, cache bapi.IndexInitializer, deepPaginationCutOff int64) bapi.WAFBackend {
	return &wafLogBackend{
		client:               c.Backend(),
		queryHelper:          lmaindex.MultiIndexWAFLogs(),
		lmaclient:            c,
		templates:            cache,
		deepPaginationCutOff: deepPaginationCutOff,
		singleIndex:          false,
		index:                index.WAFLogMultiIndex,
	}
}

func NewSingleIndexBackend(c lmaelastic.Client, cache bapi.IndexInitializer, deepPaginationCutOff int64, options ...index.Option) bapi.WAFBackend {
	return &wafLogBackend{
		client:               c.Backend(),
		queryHelper:          lmaindex.SingleIndexWAFLogs(),
		lmaclient:            c,
		templates:            cache,
		deepPaginationCutOff: deepPaginationCutOff,
		singleIndex:          true,
		index:                index.WAFLogIndex(options...),
	}
}

type logWithExtras struct {
	v1.WAFLog `json:",inline"`
	Cluster   string `json:"cluster"`
	Tenant    string `json:"tenant,omitempty"`
}

// prepareForWrite wraps a log in a document that includes the cluster and tenant if
// the backend is configured to write to a single index.
func (b *wafLogBackend) prepareForWrite(i bapi.ClusterInfo, l v1.WAFLog) interface{} {
	if b.singleIndex {
		return &logWithExtras{
			WAFLog:  l,
			Cluster: i.Cluster,
			Tenant:  i.Tenant,
		}
	}
	return l
}

// Create the given logs in elasticsearch.
func (b *wafLogBackend) Create(ctx context.Context, i bapi.ClusterInfo, logs []v1.WAFLog) (*v1.BulkResponse, error) {
	log := bapi.ContextLogger(i)

	if err := i.Valid(); err != nil {
		return nil, err
	}

	err := b.templates.Initialize(ctx, b.index, i)
	if err != nil {
		return nil, err
	}

	// Determine the index to write to using an alias
	alias := b.index.Alias(i)
	log.Debugf("Writing WAF logs in bulk to alias %s", alias)

	// Build a bulk request using the provided logs.
	bulk := b.client.Bulk()

	for _, f := range logs {
		// Add this log to the bulk request.
		req := elastic.NewBulkIndexRequest().Index(alias).Doc(b.prepareForWrite(i, f))
		bulk.Add(req)
	}

	// Send the bulk request.
	resp, err := bulk.Do(ctx)
	if err != nil {
		log.Errorf("Error writing log: %s", err)
		return nil, fmt.Errorf("failed to write log: %s", err)
	}
	fields := logrus.Fields{
		"succeeded": len(resp.Succeeded()),
		"failed":    len(resp.Failed()),
	}
	log.WithFields(fields).Debugf("WAF log bulk request complete: %+v", resp)

	return &v1.BulkResponse{
		Total:     len(resp.Items),
		Succeeded: len(resp.Succeeded()),
		Failed:    len(resp.Failed()),
		Errors:    v1.GetBulkErrors(resp),
	}, nil
}

// List lists logs that match the given parameters.
func (b *wafLogBackend) List(ctx context.Context, i api.ClusterInfo, opts *v1.WAFLogParams) (*v1.List[v1.WAFLog], error) {
	log := bapi.ContextLogger(i)

	// Get the base query.
	search, startFrom, err := b.getSearch(i, opts)
	if err != nil {
		return nil, err
	}

	results, err := search.Do(ctx)
	if err != nil {
		return nil, err
	}

	logs := []v1.WAFLog{}
	for _, h := range results.Hits.Hits {
		l := v1.WAFLog{}
		err = json.Unmarshal(h.Source, &l)
		if err != nil {
			log.WithError(err).Error("Error unmarshalling WAF log")
			continue
		}
		logs = append(logs, l)
	}

	// If an index has more than 10000 items or other value configured via index.max_result_window
	// setting in Elastic, we need to perform deep pagination
	pitID, err := logtools.NextPointInTime(ctx, b.client, b.index.Index(i), results, b.deepPaginationCutOff, log)
	if err != nil {
		return nil, err
	}

	return &v1.List[v1.WAFLog]{
		TotalHits: results.TotalHits(),
		Items:     logs,
		AfterKey:  logtools.NextAfterKey(opts, startFrom, pitID, results, b.deepPaginationCutOff),
	}, nil
}

func (b *wafLogBackend) Aggregations(ctx context.Context, i api.ClusterInfo, opts *v1.WAFLogAggregationParams) (*elastic.Aggregations, error) {
	// Get the base query.
	search, _, err := b.getSearch(i, &opts.WAFLogParams)
	if err != nil {
		return nil, err
	}

	// Add in any aggregations provided by the client. We need to handle two cases - one where this is a
	// time-series request, and another when it's just an aggregation request.
	if opts.NumBuckets > 0 {
		// Time-series.
		hist := elastic.NewAutoDateHistogramAggregation().
			Field("@timestamp").
			Buckets(opts.NumBuckets)
		for name, agg := range opts.Aggregations {
			hist = hist.SubAggregation(name, logtools.RawAggregation{RawMessage: agg})
		}
		search.Aggregation(v1.TimeSeriesBucketName, hist)
	} else {
		// Not time-series. Just add the aggs as they are.
		for name, agg := range opts.Aggregations {
			search = search.Aggregation(name, logtools.RawAggregation{RawMessage: agg})
		}
	}

	// Do the search.
	results, err := search.Do(ctx)
	if err != nil {
		return nil, err
	}

	return &results.Aggregations, nil
}

func (b *wafLogBackend) getSearch(i bapi.ClusterInfo, opts *v1.WAFLogParams) (*elastic.SearchService, int, error) {
	if err := i.Valid(); err != nil {
		return nil, 0, err
	}

	// Build the query.
	q, err := b.buildQuery(i, opts)
	if err != nil {
		return nil, 0, err
	}
	query := b.client.Search().
		Size(opts.GetMaxPageSize()).
		Query(q)

	// Configure pagination options
	var startFrom int
	query, startFrom, err = logtools.ConfigureCurrentPage(query, opts, b.index.Index(i))
	if err != nil {
		return nil, 0, err
	}

	// Configure sorting.
	if len(opts.Sort) != 0 {
		for _, s := range opts.Sort {
			query.Sort(s.Field, !s.Descending)
		}
	} else {
		query.Sort(b.queryHelper.GetTimeField(), true)
	}
	return query, startFrom, nil
}

// buildQuery builds an elastic query using the given parameters.
func (b *wafLogBackend) buildQuery(i bapi.ClusterInfo, opts *v1.WAFLogParams) (elastic.Query, error) {
	// Start with the base flow log query using common fields.
	query, err := logtools.BuildQuery(b.queryHelper, i, opts)
	if err != nil {
		return nil, err
	}

	return query, nil
}
