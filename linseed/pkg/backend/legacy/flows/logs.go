// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package flows

import (
	"context"
	"fmt"

	"github.com/projectcalico/calico/libcalico-go/lib/json"

	"github.com/sirupsen/logrus"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"

	"github.com/olivere/elastic/v7"

	"github.com/projectcalico/calico/linseed/pkg/backend/api"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/index"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/logtools"
	lmaindex "github.com/projectcalico/calico/linseed/pkg/internal/lma/elastic/index"
	lmaelastic "github.com/projectcalico/calico/lma/pkg/elastic"
)

type flowLogBackend struct {
	client               *elastic.Client
	lmaclient            lmaelastic.Client
	queryHelper          lmaindex.Helper
	initializer          bapi.IndexInitializer
	deepPaginationCutOff int64
	singleIndex          bool
	index                bapi.Index
}

func NewFlowLogBackend(c lmaelastic.Client, cache bapi.IndexInitializer, deepPaginationCutOff int64) bapi.FlowLogBackend {
	return &flowLogBackend{
		client:               c.Backend(),
		lmaclient:            c,
		initializer:          cache,
		queryHelper:          lmaindex.MultiIndexFlowLogs(),
		deepPaginationCutOff: deepPaginationCutOff,
		index:                index.FlowLogMultiIndex,
	}
}

// NewSingleIndexFlowLogBackend returns a new flow log backend that writes to a single index.
func NewSingleIndexFlowLogBackend(c lmaelastic.Client, cache bapi.IndexInitializer, deepPaginationCutOff int64, options ...index.Option) bapi.FlowLogBackend {
	return &flowLogBackend{
		client:               c.Backend(),
		lmaclient:            c,
		initializer:          cache,
		queryHelper:          lmaindex.SingleIndexFlowLogs(),
		deepPaginationCutOff: deepPaginationCutOff,
		singleIndex:          true,
		index:                index.FlowLogIndex(options...),
	}
}

type flowLogWithExtras struct {
	v1.FlowLog `json:",inline"`
	Cluster    string `json:"cluster"`
	Tenant     string `json:"tenant,omitempty"`
}

// prepareForWrite wraps a flow log in a document that includes the cluster and tenant if
// the backend is configured to write to a single index.
func (b *flowLogBackend) prepareForWrite(i bapi.ClusterInfo, f v1.FlowLog) interface{} {
	if b.singleIndex {
		return flowLogWithExtras{
			FlowLog: f,
			Cluster: i.Cluster,
			Tenant:  i.Tenant,
		}
	}
	return f
}

// Create the given flow log in elasticsearch.
func (b *flowLogBackend) Create(ctx context.Context, i bapi.ClusterInfo, logs []v1.FlowLog) (*v1.BulkResponse, error) {
	log := bapi.ContextLogger(i)

	if err := i.Valid(); err != nil {
		return nil, err
	}

	err := b.initializer.Initialize(ctx, b.index, i)
	if err != nil {
		return nil, err
	}

	// Determine the index to write to using an alias
	alias := b.index.Alias(i)
	log.Infof("Writing flow logs in bulk to alias %s", alias)

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
		log.Errorf("Error writing flow log: %s", err)
		return nil, fmt.Errorf("failed to write flow log: %s", err)
	}
	fields := logrus.Fields{
		"succeeded": len(resp.Succeeded()),
		"failed":    len(resp.Failed()),
	}
	log.WithFields(fields).Debugf("Flow log bulk request complete: %+v", resp)

	return &v1.BulkResponse{
		Total:     len(resp.Items),
		Succeeded: len(resp.Succeeded()),
		Failed:    len(resp.Failed()),
		Errors:    v1.GetBulkErrors(resp),
	}, nil
}

// List lists logs that match the given parameters.
func (b *flowLogBackend) List(ctx context.Context, i api.ClusterInfo, opts *v1.FlowLogParams) (*v1.List[v1.FlowLog], error) {
	log := bapi.ContextLogger(i)

	if err := i.Valid(); err != nil {
		return nil, err
	}

	query, startFrom, err := b.getSearch(i, opts)
	if err != nil {
		return nil, err
	}

	results, err := query.Do(ctx)
	if err != nil {
		return nil, err
	}

	logs := []v1.FlowLog{}
	for _, h := range results.Hits.Hits {
		l := v1.FlowLog{}
		err = json.Unmarshal(h.Source, &l)
		if err != nil {
			log.WithError(err).Error("Error unmarshalling log")
			continue
		}
		l.ID = h.Id
		logs = append(logs, l)
	}

	// If an index has more than 10000 items or other value configured via index.max_result_window
	// setting in Elastic, we need to perform deep pagination
	pitID, err := logtools.NextPointInTime(ctx, b.client, b.index.Index(i), results, b.deepPaginationCutOff, log)
	if err != nil {
		return nil, err
	}

	return &v1.List[v1.FlowLog]{
		Items:     logs,
		TotalHits: results.TotalHits(),
		AfterKey:  logtools.NextAfterKey(opts, startFrom, pitID, results, b.deepPaginationCutOff),
	}, nil
}

func (b *flowLogBackend) Aggregations(ctx context.Context, i api.ClusterInfo, opts *v1.FlowLogAggregationParams) (*elastic.Aggregations, error) {
	// Get the base query.
	search, _, err := b.getSearch(i, &opts.FlowLogParams)
	if err != nil {
		return nil, err
	}

	// Add in any aggregations provided by the client. We need to handle two cases - one where this is a
	// time-series request, and another when it's just an aggregation request.
	if opts.NumBuckets > 0 {
		// Time-series.
		hist := elastic.NewAutoDateHistogramAggregation().
			Field(b.queryHelper.GetTimeField()).
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

func (b *flowLogBackend) getSearch(i bapi.ClusterInfo, opts *v1.FlowLogParams) (*elastic.SearchService, int, error) {
	if i.Cluster == "" {
		return nil, 0, fmt.Errorf("no cluster ID on request")
	}

	q, err := b.buildQuery(i, opts)
	if err != nil {
		return nil, 0, err
	}

	// Build the query, sorting by time.
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
	if len(opts.GetSortBy()) != 0 {
		for _, s := range opts.GetSortBy() {
			query.Sort(s.Field, !s.Descending)
		}
	} else {
		query.Sort(b.queryHelper.GetTimeField(), true)
	}
	return query, startFrom, nil
}

// buildQuery builds an elastic query using the given parameters.
func (b *flowLogBackend) buildQuery(i bapi.ClusterInfo, opts *v1.FlowLogParams) (elastic.Query, error) {
	// Start with the base flow log query using common fields.
	start, end := logtools.ExtractTimeRange(opts.GetTimeRange())
	query, err := logtools.BuildQuery(b.queryHelper, i, opts, start, end)
	if err != nil {
		return nil, err
	}

	if len(opts.IPMatches) > 0 {
		for _, match := range opts.IPMatches {
			// Get the list of values as an interface{}, as needed for a terms query.
			values := []interface{}{}
			for _, t := range match.IPs {
				values = append(values, t)
			}

			switch match.Type {
			case v1.MatchTypeSource:
				query.Filter(elastic.NewTermsQuery("source_ip", values...))
			case v1.MatchTypeDest:
				query.Filter(elastic.NewTermsQuery("dest_ip", values...))
			case v1.MatchTypeAny:
				fallthrough
			default:
				// By default, treat as an "any" match. Return any flows that have a source
				// or destination name that matches.
				query.Filter(elastic.NewBoolQuery().Should(
					elastic.NewTermsQuery("source_ip", values...),
					elastic.NewTermsQuery("dest_ip", values...),
				).MinimumNumberShouldMatch(1))
			}
		}
	}

	return query, nil
}
