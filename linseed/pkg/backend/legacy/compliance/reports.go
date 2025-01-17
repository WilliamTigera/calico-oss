// Copyright (c) 2023 Tigera All rights reserved.

package compliance

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/olivere/elastic/v7"
	"github.com/sirupsen/logrus"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	"github.com/projectcalico/calico/linseed/pkg/backend"
	"github.com/projectcalico/calico/linseed/pkg/backend/api"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/index"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/logtools"
	lmaindex "github.com/projectcalico/calico/linseed/pkg/internal/lma/elastic/index"
	lmaelastic "github.com/projectcalico/calico/lma/pkg/elastic"
)

func NewReportsBackend(c lmaelastic.Client, cache bapi.IndexInitializer, deepPaginationCutOff int64) bapi.ReportsBackend {
	return &reportsBackend{
		client:               c.Backend(),
		lmaclient:            c,
		templates:            cache,
		deepPaginationCutOff: deepPaginationCutOff,
		queryHelper:          lmaindex.MultiIndexComplianceReports(),
		singleIndex:          false,
		index:                index.ComplianceReportMultiIndex,
	}
}

func NewSingleIndexReportsBackend(c lmaelastic.Client, cache bapi.IndexInitializer, deepPaginationCutOff int64, options ...index.Option) bapi.ReportsBackend {
	return &reportsBackend{
		client:               c.Backend(),
		lmaclient:            c,
		templates:            cache,
		deepPaginationCutOff: deepPaginationCutOff,
		queryHelper:          lmaindex.SingleIndexComplianceReports(),
		singleIndex:          true,
		index:                index.ComplianceReportsIndex(options...),
	}
}

type reportsBackend struct {
	client               *elastic.Client
	templates            bapi.IndexInitializer
	lmaclient            lmaelastic.Client
	deepPaginationCutOff int64
	queryHelper          lmaindex.Helper
	singleIndex          bool
	index                api.Index
}

type reportWithExtras struct {
	v1.ReportData `json:",inline"`
	Tenant        string `json:"tenant,omitempty"`
}

// prepareForWrite sets the cluster field, and wraps the log in a document to set tenant if
// the backend is configured to write to a single index.
func (b *reportsBackend) prepareForWrite(i bapi.ClusterInfo, l v1.ReportData) interface{} {
	l.Cluster = i.Cluster

	if b.singleIndex {
		return &reportWithExtras{
			ReportData: l,
			Tenant:     i.Tenant,
		}
	}
	return l
}

func (b *reportsBackend) List(ctx context.Context, i bapi.ClusterInfo, opts *v1.ReportDataParams) (*v1.List[v1.ReportData], error) {
	log := bapi.ContextLogger(i)

	query, startFrom, err := b.getSearch(i, opts)
	if err != nil {
		return nil, err
	}

	results, err := query.Do(ctx)
	if err != nil {
		return nil, err
	}

	logs := []v1.ReportData{}
	for _, h := range results.Hits.Hits {
		l := v1.ReportData{}
		err = json.Unmarshal(h.Source, &l)
		if err != nil {
			log.WithError(err).Error("Error unmarshalling log")
			continue
		}
		l.ID = backend.ToApplicationID(b.singleIndex, h.Id, i)
		logs = append(logs, l)
	}

	// If an index has more than 10000 items or other value configured via index.max_result_window
	// setting in Elastic, we need to perform deep pagination
	pitID, err := logtools.NextPointInTime(ctx, b.client, b.index.Index(i), results, b.deepPaginationCutOff, log)
	if err != nil {
		return nil, err
	}

	return &v1.List[v1.ReportData]{
		Items:     logs,
		TotalHits: results.TotalHits(),
		AfterKey:  logtools.NextAfterKey(opts, startFrom, pitID, results, b.deepPaginationCutOff),
	}, nil
}

func (b *reportsBackend) Create(ctx context.Context, i bapi.ClusterInfo, l []v1.ReportData) (*v1.BulkResponse, error) {
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
	log.Infof("Writing report data in bulk to alias %s", alias)

	// Build a bulk request using the provided logs.
	bulk := b.client.Bulk()

	for _, f := range l {
		// Add this log to the bulk request. Set the ID, and remove it from the
		// body of the document.
		id := backend.ToElasticID(b.singleIndex, f.ID, i)
		f.ID = ""
		req := elastic.NewBulkIndexRequest().Index(alias).Doc(b.prepareForWrite(i, f)).Id(id)
		bulk.Add(req)
	}

	// Send the bulk request.
	resp, err := bulk.Do(ctx)
	if err != nil {
		log.Errorf("Error writing report data: %s", err)
		return nil, fmt.Errorf("failed to write report data: %s", err)
	}
	fields := logrus.Fields{
		"succeeded": len(resp.Succeeded()),
		"failed":    len(resp.Failed()),
	}
	log.WithFields(fields).Debugf("Compliance report bulk request complete: %+v", resp)

	return &v1.BulkResponse{
		Total:     len(resp.Items),
		Succeeded: len(resp.Succeeded()),
		Failed:    len(resp.Failed()),
		Errors:    v1.GetBulkErrors(resp),
	}, nil
}

func (b *reportsBackend) getSearch(i bapi.ClusterInfo, opts *v1.ReportDataParams) (*elastic.SearchService, int, error) {
	if err := i.Valid(); err != nil {
		return nil, 0, err
	}

	q := b.buildQuery(i, opts)

	// Build the query, sorting by time.
	query := b.client.Search().
		Size(opts.GetMaxPageSize()).
		Query(q)

	// Configure pagination options
	var startFrom int
	var err error
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
		query.Sort("endTime", true)
	}
	return query, startFrom, nil
}

func (b *reportsBackend) buildQuery(i bapi.ClusterInfo, p *v1.ReportDataParams) elastic.Query {
	query := b.queryHelper.BaseQuery(i)
	if p.TimeRange != nil {
		query.Must(b.queryHelper.NewTimeRangeQuery(p.TimeRange))
	}
	if p.ID != "" {
		query.Must(elastic.NewTermQuery("_id", backend.ToElasticID(b.singleIndex, p.ID, i)))
	}

	if len(p.ReportMatches) > 0 {
		rqueries := []elastic.Query{}
		for _, r := range p.ReportMatches {
			if r.ReportName != "" && r.ReportTypeName != "" {
				rqueries = append(rqueries, elastic.NewBoolQuery().Must(
					elastic.NewMatchQuery("reportTypeName", r.ReportTypeName),
					elastic.NewMatchQuery("reportName", r.ReportName),
				))
			} else if r.ReportName == "" && r.ReportTypeName != "" {
				rqueries = append(rqueries, elastic.NewMatchQuery("reportTypeName", r.ReportTypeName))
			} else if r.ReportName != "" && r.ReportTypeName == "" {
				rqueries = append(rqueries, elastic.NewMatchQuery("reportName", r.ReportName))
			}
		}
		if len(rqueries) > 0 {
			// Must match at least one of the given report matches.
			query.Must(elastic.NewBoolQuery().Should(rqueries...))
		}
	}

	return query
}
