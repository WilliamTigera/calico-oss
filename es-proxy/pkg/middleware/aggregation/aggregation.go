// Copyright (c) 2021-2022 Tigera, Inc. All rights reserved.
package aggregation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/olivere/elastic/v7"
	log "github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/projectcalico/calico/es-proxy/pkg/apis/v1"
	"github.com/projectcalico/calico/es-proxy/pkg/middleware"
	validator "github.com/projectcalico/calico/libcalico-go/lib/validator/v3"
	lapi "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	"github.com/projectcalico/calico/linseed/pkg/client"
	"github.com/projectcalico/calico/lma/pkg/httputils"
	"github.com/projectcalico/calico/lma/pkg/k8s"
)

// This file implements an aggregated data query handler. The primary use of this is for the UX when querying aggregated
// data for specific service graph nodes and edges.

const (
	minAggregationInterval = 10 * time.Minute
	minTimeBuckets         = 4
	maxTimeBuckets         = 24
)

type DataType int

const (
	TypeDNS DataType = iota
	TypeL7
	TypeFlows
)

func NewHandler(lsclient client.Client, clientSetFactory k8s.ClientSetFactory, typ DataType) http.Handler {
	switch typ {
	case TypeDNS:
		return NewDNSHandler(lsclient, &realAuthorizer{
			clientSetFactory: clientSetFactory,
		})
	case TypeL7:
		return NewL7Handler(lsclient, &realAuthorizer{
			clientSetFactory: clientSetFactory,
		})
	case TypeFlows:
		return NewFlowHandler(lsclient, &realAuthorizer{
			clientSetFactory: clientSetFactory,
		})
	}
	panic("Unhandled aggregation type")
}

func NewDNSHandler(c client.Client, auth Authorizer) http.Handler {
	return &genericHandler{
		&dnsHandler{
			lsclient:   c,
			authorizer: auth,
		},
	}
}

func NewL7Handler(c client.Client, auth Authorizer) http.Handler {
	return &genericHandler{
		&l7Handler{
			lsclient:   c,
			authorizer: auth,
		},
	}
}

func NewFlowHandler(c client.Client, auth Authorizer) http.Handler {
	return &genericHandler{
		&flowHandler{
			lsclient:   c,
			authorizer: auth,
		},
	}
}

// RequestData encapsulates data parsed from the request that is shared between the various components that construct
// the service graph.
type RequestData struct {
	HTTPRequest        *http.Request
	AggregationRequest v1.AggregationRequest
	IsTimeSeries       bool
	NumBuckets         int
}

// dnsHandler handles requests for DNS log stats.
type dnsHandler struct {
	lsclient   client.Client
	authorizer Authorizer
}

func (s *dnsHandler) RunQuery(ctx context.Context, rd *RequestData) (*v1.AggregationResponse, error) {
	// Create the query.
	params := lapi.DNSAggregationParams{}
	params.Selector = rd.AggregationRequest.Selector
	if verbs, err := s.authorizer.PerformUserAuthorizationReview(ctx, rd); err != nil {
		return nil, err
	} else if len(verbs) == 0 {
		return nil, &httputils.HttpStatusError{
			Msg:    "Forbidden",
			Status: http.StatusForbidden,
		}
	} else {
		params.Permissions = verbs
	}
	params.NumBuckets = rd.NumBuckets
	params.TimeRange = rd.AggregationRequest.TimeRange

	// Add in the aggregations.
	params.Aggregations = make(map[string]json.RawMessage)
	for n, a := range rd.AggregationRequest.Aggregations {
		params.Aggregations[n] = json.RawMessage(a)
	}

	// Run the query.
	aggs, err := s.lsclient.DNSLogs(rd.AggregationRequest.Cluster).Aggregations(ctx, &params)
	if err != nil {
		return nil, err
	}
	return extractAggregationResults(aggs, rd)
}

// l7Handler handles requests for L7 log stats.
type l7Handler struct {
	lsclient   client.Client
	authorizer Authorizer
}

func (s *l7Handler) RunQuery(ctx context.Context, rd *RequestData) (*v1.AggregationResponse, error) {
	// Create the query.
	params := lapi.L7AggregationParams{}
	params.Selector = rd.AggregationRequest.Selector
	if verbs, err := s.authorizer.PerformUserAuthorizationReview(ctx, rd); err != nil {
		return nil, err
	} else if len(verbs) == 0 {
		return nil, &httputils.HttpStatusError{
			Msg:    "Forbidden",
			Status: http.StatusForbidden,
		}
	} else {
		params.Permissions = verbs
	}
	params.NumBuckets = rd.NumBuckets
	params.TimeRange = rd.AggregationRequest.TimeRange

	// Add in the aggregations.
	params.Aggregations = make(map[string]json.RawMessage)
	for n, a := range rd.AggregationRequest.Aggregations {
		params.Aggregations[n] = json.RawMessage(a)
	}

	// Run the query.
	aggs, err := s.lsclient.L7Logs(rd.AggregationRequest.Cluster).Aggregations(ctx, &params)
	if err != nil {
		return nil, err
	}
	return extractAggregationResults(aggs, rd)
}

// flowHandler handles requests for flow log stats.
type flowHandler struct {
	lsclient   client.Client
	authorizer Authorizer
}

func (s *flowHandler) RunQuery(ctx context.Context, rd *RequestData) (*v1.AggregationResponse, error) {
	// Create the query.
	params := lapi.FlowLogAggregationParams{}
	params.Selector = rd.AggregationRequest.Selector
	if verbs, err := s.authorizer.PerformUserAuthorizationReview(ctx, rd); err != nil {
		return nil, err
	} else if len(verbs) == 0 {
		return nil, &httputils.HttpStatusError{
			Msg:    "Forbidden",
			Status: http.StatusForbidden,
		}
	} else {
		params.Permissions = verbs
	}
	params.NumBuckets = rd.NumBuckets
	params.TimeRange = rd.AggregationRequest.TimeRange

	// Add in the aggregations.
	params.Aggregations = make(map[string]json.RawMessage)
	for n, a := range rd.AggregationRequest.Aggregations {
		params.Aggregations[n] = json.RawMessage(a)
	}

	// Run the query.
	aggs, err := s.lsclient.FlowLogs(rd.AggregationRequest.Cluster).Aggregations(ctx, &params)
	if err != nil {
		return nil, err
	}
	return extractAggregationResults(aggs, rd)
}

// QueryMaker describes an interface that allows clients to make queries for stats data.
type QueryMaker interface {
	RunQuery(context.Context, *RequestData) (*v1.AggregationResponse, error)
}

// genericHandler is a generic HTTP server to handling stats requests. The type-specific
// logic is implemented within the QueryMaker.
type genericHandler struct {
	backend QueryMaker
}

func (s *genericHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	start := time.Now()

	// Extract the request specific data used to collate and filter the data.
	rd, err := getAggregationRequest(w, req)
	if err != nil {
		httputils.EncodeError(w, err)
		return
	}

	// Construct a context with timeout based on the service graph request.
	ctx, cancel := context.WithTimeout(req.Context(), rd.AggregationRequest.Timeout)
	defer cancel()

	res, err := s.backend.RunQuery(ctx, rd)
	if err != nil {
		httputils.EncodeError(w, err)
		return
	}

	httputils.Encode(w, res)
	log.Debugf("Aggregation request took %s", time.Since(start))
}

func extractAggregationResults(aggs elastic.Aggregations, rd *RequestData) (*v1.AggregationResponse, error) {
	res := v1.AggregationResponse{}
	if !rd.IsTimeSeries {
		// There is no time series, therefore the data is all in the main bucket.
		res.Buckets = append(res.Buckets, v1.AggregationTimeBucket{
			StartTime:    metav1.Time{Time: rd.AggregationRequest.TimeRange.From},
			Aggregations: aggs,
		})
	} else {
		// There is a time series. The time aggregation is in the main bucket and then the data for each time
		// bucket is in the sub aggregation.
		timebuckets, ok := aggs.AutoDateHistogram(lapi.TimeSeriesBucketName)
		if !ok {
			return nil, fmt.Errorf("no valid time buckets in aggregation response")
		}
		for _, b := range timebuckets.Buckets {
			// Pull out the aggregation results.
			results := make(map[string]json.RawMessage)
			for an := range rd.AggregationRequest.Aggregations {
				results[an] = b.Aggregations[an]
			}

			// Elasticsearch stores dates in milliseconds since the epoch.
			res.Buckets = append(res.Buckets, v1.AggregationTimeBucket{
				StartTime:    metav1.Time{Time: time.Unix(int64(b.Key)/1000, 0)},
				Aggregations: results,
			})
		}
	}
	return &res, nil
}

// getAggregationRequest parses the request from the HTTP request body.
func getAggregationRequest(w http.ResponseWriter, req *http.Request) (*RequestData, error) {
	// Extract the request from the body.
	var ar v1.AggregationRequest

	if err := httputils.Decode(w, req, &ar); err != nil {
		return nil, err
	}

	// Validate parameters.
	if err := validator.Validate(ar); err != nil {
		return nil, &httputils.HttpStatusError{
			Status: http.StatusBadRequest,
			Msg:    fmt.Sprintf("Request body contains invalid data: %v", err),
			Err:    err,
		}
	}

	if ar.Timeout == 0 {
		ar.Timeout = middleware.DefaultRequestTimeout
	}
	if ar.Cluster == "" {
		ar.Cluster = middleware.MaybeParseClusterNameFromRequest(req)
	}
	if len(ar.Aggregations) == 0 {
		return nil, httputils.NewHttpStatusErrorBadRequest("Request body contains no aggregations", nil)
	}
	return &RequestData{
		HTTPRequest:        req,
		AggregationRequest: ar,
		IsTimeSeries:       ar.IncludeTimeSeries,
		NumBuckets:         getNumBuckets(ar),
	}, nil
}

// getNumBuckets returns the max number of buckets to request for a time series.
func getNumBuckets(ar v1.AggregationRequest) int {
	if !ar.IncludeTimeSeries {
		return 0
	}

	// Each bucket should be a least _minAggregationInterval_, and we always want at least _minTimeBuckets_ data points.
	// Determine the ideal number of buckets, maxing out at _maxTimeBuckets_.
	duration := ar.TimeRange.Duration()

	numMinIntervals := duration / minAggregationInterval
	if numMinIntervals < minTimeBuckets {
		return minTimeBuckets
	} else if numMinIntervals <= maxTimeBuckets {
		return int(numMinIntervals)
	} else {
		return maxTimeBuckets
	}
}
