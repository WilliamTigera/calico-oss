// Copyright 2019 Tigera Inc. All rights reserved.

package elastic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/araddon/dateparse"
	"github.com/olivere/elastic"
	log "github.com/sirupsen/logrus"

	"github.com/tigera/intrusion-detection/controller/pkg/db"
	"github.com/tigera/intrusion-detection/controller/pkg/events"
)

const (
	IPSetIndexPattern   = ".tigera.ipset.%s"
	StandardType        = "_doc"
	FlowLogIndexPattern = "tigera_secure_ee_flows.%s.*"
	EventIndexPattern   = "tigera_secure_ee_events.%s"
	QuerySize           = 1000
	MaxClauseCount      = 1024
)

var IPSetIndex string
var EventIndex string
var FlowLogIndex string

func init() {
	cluster := os.Getenv("CLUSTER_NAME")
	if cluster == "" {
		cluster = "cluster"
	}
	IPSetIndex = fmt.Sprintf(IPSetIndexPattern, cluster)
	EventIndex = fmt.Sprintf(EventIndexPattern, cluster)
	FlowLogIndex = fmt.Sprintf(FlowLogIndexPattern, cluster)
}

type ipSetDoc struct {
	CreatedAt time.Time    `json:"created_at"`
	IPs       db.IPSetSpec `json:"ips"`
}

type Elastic struct {
	c *elastic.Client
}

func NewElastic(h *http.Client, url *url.URL, username, password string) (*Elastic, error) {

	options := []elastic.ClientOptionFunc{
		elastic.SetURL(url.String()),
		elastic.SetHttpClient(h),
		elastic.SetErrorLog(log.StandardLogger()),
		elastic.SetSniff(false),
		elastic.SetHealthcheck(false),
		//elastic.SetTraceLog(log.StandardLogger()),
	}
	if username != "" {
		options = append(options, elastic.SetBasicAuth(username, password))
	}
	c, err := elastic.NewClient(options...)
	if err != nil {
		return nil, err
	}
	e := &Elastic{c}
	go func() {
		ctx := context.Background()
		err := e.ensureIndexExists(ctx, IPSetIndex, ipSetMapping)
		if err != nil {
			log.WithError(err).WithField("index", IPSetIndex).Errorf("Could not create index")
		}
		err = e.ensureIndexExists(ctx, EventIndex, eventMapping)
		if err != nil {
			log.WithError(err).WithField("index", EventIndex).Errorf("Could not create index")
		}
	}()
	return e, nil
}

func (e *Elastic) ListIPSets(ctx context.Context) ([]db.IPSetMeta, error) {
	q := elastic.NewMatchAllQuery()
	scroller := e.c.Scroll(IPSetIndex).Type(StandardType).Version(true).FetchSource(false).Query(q)

	var ids []db.IPSetMeta
	for {
		res, err := scroller.Do(ctx)
		if err == io.EOF {
			return ids, nil
		}
		if elastic.IsNotFound(err) {
			// If we 404, just return an empty slice.
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		for _, hit := range res.Hits.Hits {
			ids = append(ids, db.IPSetMeta{Name: hit.Id, Version: hit.Version})
		}
	}
}

func (e *Elastic) PutIPSet(ctx context.Context, name string, set db.IPSetSpec) error {
	err := e.ensureIndexExists(ctx, IPSetIndex, ipSetMapping)
	if err != nil {
		return err
	}

	// Put document
	body := ipSetDoc{CreatedAt: time.Now(), IPs: set}
	_, err = e.c.Index().Index(IPSetIndex).Type(StandardType).Id(name).BodyJson(body).Do(ctx)
	log.WithField("name", name).Info("IP set stored")

	return err
}

func (e *Elastic) ensureIndexExists(ctx context.Context, idx, mapping string) error {
	// Ensure Index exists, or update mappings if it does
	exists, err := e.c.IndexExists(idx).Do(ctx)
	if err != nil {
		return err
	}
	if !exists {
		r, err := e.c.CreateIndex(idx).Body(mapping).Do(ctx)
		if err != nil {
			return err
		}
		if !r.Acknowledged {
			return fmt.Errorf("not acknowledged index %s create", idx)
		}
	} else {
		r, err := e.c.PutMapping().Index(idx).BodyString(mapping).Do(ctx)
		if err != nil {
			return err
		}
		if !r.Acknowledged {
			return fmt.Errorf("not acknowledged index %s update", idx)
		}
	}
	return nil
}

func (e *Elastic) GetIPSet(ctx context.Context, name string) (db.IPSetSpec, error) {
	res, err := e.c.Get().Index(IPSetIndex).Type(StandardType).Id(name).Do(ctx)
	if err != nil {
		return nil, err
	}

	if res.Source == nil {
		return nil, errors.New("Elastic document has nil Source")
	}

	var doc map[string]interface{}
	err = json.Unmarshal(*res.Source, &doc)
	if err != nil {
		return nil, err
	}
	i, ok := doc["ips"]
	if !ok {
		return nil, errors.New("Elastic document missing ips section")
	}

	ia, ok := i.([]interface{})
	if !ok {
		return nil, fmt.Errorf("Unknown type for %#v", i)
	}
	ips := db.IPSetSpec{}
	for _, v := range ia {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("Unknown type for %#v", s)
		}
		ips = append(ips, s)
	}

	return ips, nil
}

func (e *Elastic) GetIPSetModified(ctx context.Context, name string) (time.Time, error) {
	res, err := e.c.Get().Index(IPSetIndex).Type(StandardType).Id(name).FetchSourceContext(elastic.NewFetchSourceContext(true).Include("created_at")).Do(ctx)
	if err != nil {
		return time.Time{}, err
	}

	if res.Source == nil {
		return time.Time{}, err
	}

	var doc map[string]interface{}
	err = json.Unmarshal(*res.Source, &doc)
	if err != nil {
		return time.Time{}, err
	}

	createdAt, ok := doc["created_at"]
	if !ok {
		// missing created_at field
		return time.Time{}, nil
	}

	switch createdAt.(type) {
	case string:
		return dateparse.ParseIn(createdAt.(string), time.UTC)
	default:
		return time.Time{}, fmt.Errorf("Unexpected type for %#v", createdAt)
	}
}

func (e *Elastic) QueryIPSet(ctx context.Context, name string) (db.SecurityEventIterator, error) {
	ipset, err := e.GetIPSet(ctx, name)
	if err != nil {
		return nil, err
	}
	queryTerms := splitIPSetToInterface(ipset)

	f := func(ipset, field string, terms []interface{}) *elastic.ScrollService {
		q := elastic.NewTermsQuery(field, terms...)
		return e.c.Scroll(FlowLogIndex).SortBy(elastic.SortByDoc{}).Query(q).Size(QuerySize)
	}

	var scrollers []scrollerEntry
	for _, t := range queryTerms {
		scrollers = append(scrollers, scrollerEntry{name: "source_ip", scroller: f(name, "source_ip", t), terms: t})
		scrollers = append(scrollers, scrollerEntry{name: "dest_ip", scroller: f(name, "dest_ip", t), terms: t})
	}

	return &flowLogIterator{
		scrollers: scrollers,
		ctx:       ctx,
		name:      name,
	}, nil
}

func splitIPSetToInterface(ipset db.IPSetSpec) [][]interface{} {
	terms := make([][]interface{}, 1)
	for _, ip := range ipset {
		if len(terms[len(terms)-1]) >= MaxClauseCount {
			terms = append(terms, []interface{}{ip})
		} else {
			terms[len(terms)-1] = append(terms[len(terms)-1], ip)
		}
	}
	return terms
}

func (e *Elastic) DeleteIPSet(ctx context.Context, m db.IPSetMeta) error {
	ds := e.c.Delete().Index(IPSetIndex).Type(StandardType).Id(m.Name)
	if m.Version != nil {
		ds = ds.Version(*m.Version)
	}
	_, err := ds.Do(ctx)
	return err
}

func (e *Elastic) PutSecurityEvent(ctx context.Context, f events.SecurityEvent) error {
	err := e.ensureIndexExists(ctx, EventIndex, eventMapping)
	if err != nil {
		return err
	}

	_, err = e.c.Index().Index(EventIndex).Type(StandardType).Id(f.ID()).BodyJson(f).Do(ctx)
	return err
}

func (e *Elastic) GetDatafeeds(ctx context.Context, feedIDs ...string) ([]DatafeedSpec, error) {
	params := strings.Join(feedIDs, ",")

	resp, err := e.c.PerformRequest(ctx, elastic.PerformRequestOptions{
		Method: "GET",
		Path:   fmt.Sprintf("/_xpack/ml/datafeeds/%s", params),
	})
	if err != nil {
		return nil, err
	}

	var getDatafeedsResponse GetDatafeedResponseSpec
	err = json.Unmarshal(resp.Body, &getDatafeedsResponse)
	if err != nil {
		return nil, err
	}

	return getDatafeedsResponse.Datafeeds, nil
}

func (e *Elastic) GetDatafeedStats(ctx context.Context, feedIDs ...string) ([]DatafeedCountsSpec, error) {
	params := strings.Join(feedIDs, ",")

	resp, err := e.c.PerformRequest(ctx, elastic.PerformRequestOptions{
		Method: "GET",
		Path:   fmt.Sprintf("/_xpack/ml/datafeeds/%s/_stats", params),
	})
	if err != nil {
		return nil, err
	}

	var getDatafeedStatsResponse GetDatafeedStatsResponseSpec
	err = json.Unmarshal(resp.Body, &getDatafeedStatsResponse)
	if err != nil {
		return nil, err
	}

	return getDatafeedStatsResponse.Datafeeds, nil
}

func (e *Elastic) StartDatafeed(ctx context.Context, feedID string, options *OpenDatafeedOptions) (bool, error) {
	requestOptions := elastic.PerformRequestOptions{
		Method: "POST",
		Path:   fmt.Sprintf("/_xpack/ml/datafeeds/%s/_start", feedID),
	}
	if options != nil {
		requestOptions.Body = options
	}
	resp, err := e.c.PerformRequest(ctx, requestOptions)
	if err != nil {
		return false, err
	}

	var openJobResponse map[string]bool
	err = json.Unmarshal(resp.Body, &openJobResponse)
	if err != nil {
		return false, err
	}

	return openJobResponse["started"], nil
}

func (e *Elastic) StopDatafeed(ctx context.Context, feedID string, options *CloseDatafeedOptions) (bool, error) {
	requestOptions := elastic.PerformRequestOptions{
		Method: "POST",
		Path:   fmt.Sprintf("/_xpack/ml/datafeeds/%s/_stop", feedID),
	}
	if options != nil {
		requestOptions.Body = options
	}
	resp, err := e.c.PerformRequest(ctx, requestOptions)
	if err != nil {
		return false, err
	}

	var openDatafeedResponse map[string]bool
	err = json.Unmarshal(resp.Body, &openDatafeedResponse)
	if err != nil {
		return false, err
	}

	return openDatafeedResponse["stopped"], nil
}

func (e *Elastic) GetJobs(ctx context.Context, jobIDs ...string) ([]JobSpec, error) {
	params := strings.Join(jobIDs, ",")

	resp, err := e.c.PerformRequest(ctx, elastic.PerformRequestOptions{
		Method: "GET",
		Path:   fmt.Sprintf("/_xpack/ml/anomaly_detectors/%s", params),
	})
	if err != nil {
		return nil, err
	}

	var getJobsResponse GetJobResponseSpec
	err = json.Unmarshal(resp.Body, &getJobsResponse)
	if err != nil {
		return nil, err
	}

	return getJobsResponse.Jobs, nil
}

func (e *Elastic) GetJobStats(ctx context.Context, jobIDs ...string) ([]JobStatsSpec, error) {
	params := strings.Join(jobIDs, ",")

	resp, err := e.c.PerformRequest(ctx, elastic.PerformRequestOptions{
		Method: "GET",
		Path:   fmt.Sprintf("/_xpack/ml/anomaly_detectors/%s/_stats", params),
	})
	if err != nil {
		return nil, err
	}

	var getJobsStatsResponse GetJobStatsResponseSpec
	err = json.Unmarshal(resp.Body, &getJobsStatsResponse)
	if err != nil {
		return nil, err
	}

	return getJobsStatsResponse.Jobs, nil
}

func (e *Elastic) OpenJob(ctx context.Context, jobID string, options *OpenJobOptions) (bool, error) {
	requestOptions := elastic.PerformRequestOptions{
		Method: "POST",
		Path:   fmt.Sprintf("/_xpack/ml/anomaly_detectors/%s/_open", jobID),
	}
	if options != nil {
		requestOptions.Body = options
	}
	resp, err := e.c.PerformRequest(ctx, requestOptions)
	if err != nil {
		return false, err
	}

	var openJobResponse map[string]bool
	err = json.Unmarshal(resp.Body, &openJobResponse)
	if err != nil {
		return false, err
	}

	return openJobResponse["opened"], nil
}

func (e *Elastic) CloseJob(ctx context.Context, jobID string, options *CloseJobOptions) (bool, error) {
	requestOptions := elastic.PerformRequestOptions{
		Method: "POST",
		Path:   fmt.Sprintf("/_xpack/ml/anomaly_detectors/%s/_close", jobID),
	}
	if options != nil {
		requestOptions.Body = options
	}
	resp, err := e.c.PerformRequest(ctx, requestOptions)
	if err != nil {
		return false, err
	}

	var openJobResponse map[string]bool
	err = json.Unmarshal(resp.Body, &openJobResponse)
	if err != nil {
		return false, err
	}

	return openJobResponse["closed"], nil
}

func (e *Elastic) GetBuckets(ctx context.Context, jobID string, options *GetBucketsOptions) ([]BucketSpec, error) {
	optTimestamp := ""
	if options.Timestamp != nil {
		optTimestamp = fmt.Sprintf("/%s", options.Timestamp.Format(time.RFC3339))
	}

	requestOptions := elastic.PerformRequestOptions{
		Method: "POST",
		Path:   fmt.Sprintf("/_xpack/ml/anomaly_detectors/%s/results/buckets%s", jobID, optTimestamp),
	}
	if options != nil {
		requestOptions.Body = options
	}
	resp, err := e.c.PerformRequest(ctx, requestOptions)
	if err != nil {
		return nil, err
	}

	var getBucketsResponse GetBucketsResponseSpec
	err = json.Unmarshal(resp.Body, &getBucketsResponse)
	if err != nil {
		return nil, err
	}

	return getBucketsResponse.Buckets, nil
}

func (e *Elastic) GetRecords(ctx context.Context, jobID string, options *GetRecordsOptions) ([]RecordSpec, error) {
	requestOptions := elastic.PerformRequestOptions{
		Method: "POST",
		Path:   fmt.Sprintf("/_xpack/ml/anomaly_detectors/%s/results/records", jobID),
	}
	if options != nil {
		requestOptions.Body = options
	}
	resp, err := e.c.PerformRequest(ctx, requestOptions)
	if err != nil {
		return nil, err
	}

	var getRecordsResponse GetRecordsResponseSpec
	err = json.Unmarshal(resp.Body, &getRecordsResponse)
	if err != nil {
		return nil, err
	}

	return getRecordsResponse.Records, nil
}
