// Copyright (c) 2021-2023 Tigera, Inc. All rights reserved.
package search

import (
	"context"
	gojson "encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/projectcalico/calico/compliance/pkg/datastore"
	v1 "github.com/projectcalico/calico/es-proxy/pkg/apis/v1"
	"github.com/projectcalico/calico/es-proxy/pkg/math"
	"github.com/projectcalico/calico/es-proxy/pkg/middleware"
	"github.com/projectcalico/calico/libcalico-go/lib/json"
	validator "github.com/projectcalico/calico/libcalico-go/lib/validator/v3"
	"github.com/projectcalico/calico/libcalico-go/lib/validator/v3/query"
	lapi "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	"github.com/projectcalico/calico/linseed/pkg/client"
	"github.com/projectcalico/calico/lma/pkg/elastic"
	"github.com/projectcalico/calico/lma/pkg/httputils"
)

type SearchType int

const (
	SearchTypeFlows SearchType = iota
	SearchTypeDNS
	SearchTypeL7
	SearchTypeEvents
)

type RequestType interface {
	v1.CommonSearchRequest | v1.FlowLogSearchRequest
}

// SearchHandler is a handler for the /search endpoint.
//
// Calls different handlers based on the SearchType - flowLogTypeSearchHandler for SearchTypeFlows and commonTypeSearchHandler
// for other types.
func SearchHandler(t SearchType, authReview middleware.AuthorizationReview, k8sClient datastore.ClientSet, lsclient client.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse request body onto search parameters. If an error occurs while decoding define an http
		// error and return.
		var response *v1.SearchResponse
		var err error
		switch t {
		case SearchTypeFlows:
			response, err = flowlogTypeSearchHandler(authReview, k8sClient, lsclient, w, r)
		default:
			response, err = commonTypeSearchHandler(t, authReview, k8sClient, lsclient, w, r)
		}

		if err != nil {
			httputils.EncodeError(w, err)
			return
		}

		httputils.Encode(w, response)
	})
}

// flowlogTypeSearchHandler handles flowlog search requests.
//
// Uses a request body (JSON.blob) to extract parameters to build an elasticsearch query,
// executes it and returns the results.
func flowlogTypeSearchHandler(authReview middleware.AuthorizationReview, k8sClient datastore.ClientSet,
	lsclient client.Client, w http.ResponseWriter, r *http.Request) (*v1.SearchResponse, error) {

	// Decode the request
	searchRequest, err := parseBody[v1.FlowLogSearchRequest](w, r)
	if err != nil {
		return nil, err
	}

	// Validate request and set defaults
	err = defaultAndValidateCommonRequest(r, &searchRequest.CommonSearchRequest)
	if err != nil {
		return nil, err
	}

	// Create a context with timeout to ensure we don't block for too long with this query.
	// This releases timer resources if the operation completes before the timeout.
	ctx, cancel := context.WithTimeout(r.Context(), searchRequest.Timeout.Duration)
	defer cancel()

	// Perform the search.
	return searchFlowLogs(ctx, lsclient, searchRequest, authReview, k8sClient)
}

// commonTypeSearchHandler handles dnslogs, l7logs, and event search requests.
//
// Uses a request body (JSON.blob) to extract parameters to build an elasticsearch query,
// executes it and returns the results.
func commonTypeSearchHandler(t SearchType, authReview middleware.AuthorizationReview, k8sClient datastore.ClientSet,
	lsclient client.Client, w http.ResponseWriter, r *http.Request) (*v1.SearchResponse, error) {
	// Decode the request
	searchRequest, err := parseBody[v1.CommonSearchRequest](w, r)
	if err != nil {
		return nil, err
	}

	// Validate request and set defaults
	err = defaultAndValidateCommonRequest(r, searchRequest)
	if err != nil {
		return nil, err
	}
	// Create a context with timeout to ensure we don't block for too long with this query.
	// This releases timer resources if the operation completes before the timeout.
	ctx, cancel := context.WithTimeout(r.Context(), searchRequest.Timeout.Duration)
	defer cancel()

	// Perform the search.
	switch t {
	case SearchTypeDNS:
		return searchDNSLogs(ctx, lsclient, searchRequest, authReview, k8sClient)
	case SearchTypeL7:
		return searchL7Logs(ctx, lsclient, searchRequest, authReview, k8sClient)
	case SearchTypeEvents:
		return searchEvents(ctx, lsclient, searchRequest, authReview, k8sClient)
	}
	return nil, fmt.Errorf("Unhandled search type")
}

// parseBody extracts query parameters from the request body (JSON.blob) into RequestType.
//
// Will define an http.Error if an error occurs.
func parseBody[T RequestType](w http.ResponseWriter, r *http.Request) (*T, error) {
	// Validate http method.
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		logrus.WithError(middleware.ErrInvalidMethod).Info("Invalid http method.")

		return nil, &httputils.HttpStatusError{
			Status: http.StatusMethodNotAllowed,
			Msg:    middleware.ErrInvalidMethod.Error(),
			Err:    middleware.ErrInvalidMethod,
		}
	}

	params := new(T)

	// Decode the http request body into the struct.
	if err := httputils.Decode(w, r, params); err != nil {
		var mr *httputils.HttpStatusError
		if errors.As(err, &mr) {
			logrus.WithError(mr.Err).Info(mr.Msg)
			return nil, mr
		} else {
			logrus.WithError(mr.Err).Info("Error parsing /search request.")
			return nil, &httputils.HttpStatusError{
				Status: http.StatusMethodNotAllowed,
				Msg:    http.StatusText(http.StatusInternalServerError),
				Err:    err,
			}
		}
	}

	return params, nil
}

// defaultAndValidateCommonRequest validates CommonSearchRequest fields and defaults them where needed.
//
// Will define an http.Error if an error occurs.
func defaultAndValidateCommonRequest(r *http.Request, c *v1.CommonSearchRequest) error {
	// Initialize the search parameters to their default values.
	if c == nil {
		return fmt.Errorf("SearchRequest is not initialized before validation")
	}

	if c.PageSize == nil {
		size := elastic.DefaultPageSize
		c.PageSize = &size
	}

	if c.Timeout == nil {
		c.Timeout = &metav1.Duration{Duration: middleware.DefaultRequestTimeout}
	}

	// Validate parameters.
	if err := validator.Validate(c); err != nil {
		return &httputils.HttpStatusError{
			Status: http.StatusBadRequest,
			Msg:    err.Error(),
			Err:    err,
		}
	}

	// Set cluster name to default: "cluster", if empty.
	if c.ClusterName == "" {
		c.ClusterName = middleware.MaybeParseClusterNameFromRequest(r)
	}

	// Check that we are not attempting to enumerate more than the maximum number of results.
	if c.PageNum*(*c.PageSize) > middleware.MaxNumResults {
		return &httputils.HttpStatusError{
			Status: http.StatusBadRequest,
			Msg:    "page number overflow",
			Err:    errors.New("page number / Page size combination is too large"),
		}
	}

	// At the moment, we only support a single sort by field.
	// TODO(rlb): Need to check the fields are valid for the index type. Maybe something else for the
	// index helper.
	if len(c.SortBy) > 1 {
		return &httputils.HttpStatusError{
			Status: http.StatusBadRequest,
			Msg:    "too many sort fields specified",
			Err:    errors.New("too many sort fields specified"),
		}
	}

	// We want to allow user to be able to select using only From the UI
	if c.TimeRange != nil && c.TimeRange.To.IsZero() && !c.TimeRange.From.IsZero() {
		c.TimeRange.To = time.Now().UTC()
	}

	return nil
}

func validateSelector(selector string, t SearchType) error {
	q, err := query.ParseQuery(selector)
	if err != nil {
		return &httputils.HttpStatusError{
			Status: http.StatusBadRequest,
			Err:    err,
			Msg:    fmt.Sprintf("Invalid selector (%s) in request: %v", selector, err),
		}
	}

	// Validate the atoms in the selector.
	var validator query.Validator
	switch t {
	case SearchTypeL7:
		validator = query.IsValidL7LogsAtom
	case SearchTypeDNS:
		validator = query.IsValidDNSAtom
	case SearchTypeEvents:
		validator = query.IsValidEventsKeysAtom
	case SearchTypeFlows:
		validator = query.IsValidFlowsAtom
	default:
		return fmt.Errorf("Invalid search type: %v", t)
	}

	if err := query.Validate(q, validator); err != nil {
		return &httputils.HttpStatusError{
			Status: http.StatusBadRequest,
			Err:    err,
			Msg:    fmt.Sprintf("Invalid selector (%s) in request: %v", selector, err),
		}
	}

	return nil
}

// intoLogParams converts a request into the given Linseed API parameters.
func intoLogParams(ctx context.Context, t SearchType, request *v1.CommonSearchRequest, params lapi.LogParams, authReview middleware.AuthorizationReview) error {
	// Add in the selector.
	if len(request.Selector) > 0 {
		// Validate the selector. Linseed performs the same check, but
		// better to short-circuit the request if we can avoid it.
		if err := validateSelector(request.Selector, t); err != nil {
			return err
		}
		params.SetSelector(request.Selector)
	}

	// Time range query.
	if request.TimeRange != nil {
		params.SetTimeRange(request.TimeRange)
	}

	// Get the user's permissions. We'll pass these to Linseed to filter out logs that
	// the user doens't have permission to view.
	verbs, err := authReview.PerformReview(ctx, request.ClusterName)
	if err != nil {
		return err
	}
	params.SetPermissions(verbs)

	// Configure sorting, if set.
	for _, s := range request.SortBy {
		if s.Field == "" {
			continue
		}
		params.SetSortBy([]lapi.SearchRequestSortBy{
			{
				Field:      s.Field,
				Descending: s.Descending,
			},
		})
	}

	// if len(params.Filter) > 0 {
	// 	for _, filter := range params.Filter {
	// 		q := elastic.NewRawStringQuery(string(filter))
	// 		esquery = esquery.Filter(q)
	// 	}
	// }

	// Configure pagination, timeout, etc.
	params.SetTimeout(request.Timeout)
	params.SetMaxPageSize(*request.PageSize)
	if request.PageNum != 0 {
		// TODO: Ideally, clients don't know the format of the AfterKey. In order to satisfy
		// the exising UI API, we need to for now.
		params.SetAfterKey(map[string]interface{}{
			"startFrom": request.PageNum * (*request.PageSize),
		})
	}

	return nil
}

// searchFlowLogs calls searchLogs, configured for flow logs.
func searchFlowLogs(
	ctx context.Context,
	lsclient client.Client,
	request *v1.FlowLogSearchRequest,
	authReview middleware.AuthorizationReview,
	k8sClient datastore.ClientSet,
) (*v1.SearchResponse, error) {
	// build base params.
	params := &lapi.FlowLogParams{}

	if len(request.PolicyMatches) > 0 {
		var policyMatches []lapi.PolicyMatch
		for _, item := range request.PolicyMatches {
			if (item != v1.PolicyMatch{}) {
				pm := lapi.PolicyMatch{
					Name:      item.Name,
					Namespace: item.Namespace,
					Action:    item.Action,
					Tier:      item.Tier,
				}
				policyMatches = append(policyMatches, pm)
			}
		}
		if len(policyMatches) > 0 {
			params.PolicyMatches = policyMatches
		}
	}

	// Merge in common search request parameters.
	err := intoLogParams(ctx, SearchTypeFlows, &request.CommonSearchRequest, params, authReview)
	if err != nil {
		return nil, err
	}

	listFn := lsclient.FlowLogs(request.ClusterName).List
	return searchLogs(ctx, listFn, params, authReview, k8sClient)
}

// searchFlowLogs calls searchLogs, configured for DNS logs.
func searchDNSLogs(
	ctx context.Context,
	lsclient client.Client,
	request *v1.CommonSearchRequest,
	authReview middleware.AuthorizationReview,
	k8sClient datastore.ClientSet,
) (*v1.SearchResponse, error) {
	params := &lapi.DNSLogParams{}
	err := intoLogParams(ctx, SearchTypeDNS, request, params, authReview)
	if err != nil {
		return nil, err
	}
	listFn := lsclient.DNSLogs(request.ClusterName).List
	return searchLogs(ctx, listFn, params, authReview, k8sClient)
}

// searchL7Logs calls searchLogs, configured for DNS logs.
func searchL7Logs(
	ctx context.Context,
	lsclient client.Client,
	request *v1.CommonSearchRequest,
	authReview middleware.AuthorizationReview,
	k8sClient datastore.ClientSet,
) (*v1.SearchResponse, error) {
	params := &lapi.L7LogParams{}
	err := intoLogParams(ctx, SearchTypeL7, request, params, authReview)
	if err != nil {
		return nil, err
	}
	listFn := lsclient.L7Logs(request.ClusterName).List
	return searchLogs(ctx, listFn, params, authReview, k8sClient)
}

// searchEvents calls searchLogs, configured for events.
func searchEvents(
	ctx context.Context,
	lsclient client.Client,
	request *v1.CommonSearchRequest,
	authReview middleware.AuthorizationReview,
	k8sClient datastore.ClientSet,
) (*v1.SearchResponse, error) {
	params := &lapi.EventParams{}
	err := intoLogParams(ctx, SearchTypeEvents, request, params, authReview)
	if err != nil {
		return nil, err
	}

	// For security event search requests, we need to modify the Elastic query
	// to exclude events which match exceptions created by users.
	eventExceptionList, err := k8sClient.AlertExceptions().List(ctx, metav1.ListOptions{})
	if err != nil {
		logrus.WithError(err).Error("failed to list alert exceptions")
		return nil, &httputils.HttpStatusError{
			Status: http.StatusInternalServerError,
			Msg:    err.Error(),
			Err:    err,
		}
	}

	var selectors []string
	now := &metav1.Time{Time: time.Now()}
	for _, alertException := range eventExceptionList.Items {
		if alertException.Spec.StartTime.Before(now) {
			if alertException.Spec.EndTime != nil && alertException.Spec.EndTime.Before(now) {
				// skip expired alert exceptions
				logrus.Debugf(`skipping expired alert exception="%s"`, alertException.GetName())
				continue
			}

			// Validate the selector first.
			err := validateSelector(alertException.Spec.Selector, SearchTypeEvents)
			if err != nil {
				logrus.WithError(err).Warnf(`ignoring alert exception="%s", failed to parse selector="%s"`,
					alertException.GetName(), alertException.Spec.Selector)
				continue
			}
			selectors = append(selectors, alertException.Spec.Selector)
		}
	}

	if len(selectors) > 0 {
		if len(selectors) == 1 {
			// Just one selector - invert it.
			params.Selector = fmt.Sprintf("NOT ( %s )", selectors[0])
		} else {
			// Combine the selectors using OR, and then negate since we don't want
			// any alerts that match any of the selectors.
			// i.e., NOT ( (SEL1) OR (SEL2) OR (SEL3) )
			params.Selector = fmt.Sprintf("NOT (( %s ))", strings.Join(selectors, " ) OR ( "))
		}
	}

	listFn := lsclient.Events(request.ClusterName).List
	return searchLogs(ctx, listFn, params, authReview, k8sClient)
}

type Hit[T any] struct {
	ID     string `json:"id,omitempty"`
	Source T      `json:"source"`
}

// searchLogs performs a search against the Linseed API for logs that match the given
// parameters, using the provided client.ListFunc.
func searchLogs[T any](
	ctx context.Context,
	listFunc client.ListFunc[T],
	params lapi.LogParams,
	authReview middleware.AuthorizationReview,
	k8sClient datastore.ClientSet,
) (*v1.SearchResponse, error) {
	pageSize := params.GetMaxPageSize()

	// Perform the query.
	start := time.Now()
	items, err := listFunc(ctx, params)
	if err != nil {
		return nil, &httputils.HttpStatusError{
			Status: http.StatusInternalServerError,
			Msg:    "error performing search",
			Err:    err,
		}
	}

	// Build the hits response. We want to keep track of errors, but still return
	// as many results as we can.
	var hits []gojson.RawMessage
	for _, item := range items.Items {
		// ID is only set when the UI needs it.
		var id string
		switch i := any(item).(type) {
		case lapi.Event:
			id = i.ID
		}
		hit := Hit[T]{
			ID:     id,
			Source: item,
		}
		hitJSON, err := json.Marshal(hit)
		if err != nil {
			logrus.WithError(err).WithField("hit", hit).Error("Error marshaling search result")
			return nil, &httputils.HttpStatusError{
				Status: http.StatusInternalServerError,
				Msg:    "error marshaling search result from linseed",
				Err:    err,
			}
		}
		hits = append(hits, gojson.RawMessage(hitJSON))
	}

	// Calculate the number of pages, given the request's page size.
	cappedTotalHits := math.MinInt(int(items.TotalHits), middleware.MaxNumResults)
	numPages := 0
	if pageSize > 0 {
		numPages = ((cappedTotalHits - 1) / pageSize) + 1
	}

	return &v1.SearchResponse{
		TimedOut:  false, // TODO: Is this used?
		Took:      metav1.Duration{Duration: time.Since(start)},
		NumPages:  numPages,
		TotalHits: int(items.TotalHits),
		Hits:      hits,
	}, nil
}
