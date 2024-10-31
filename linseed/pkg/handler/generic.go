package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/olivere/elastic/v7"
	"github.com/sirupsen/logrus"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/linseed/pkg/middleware"
	"github.com/projectcalico/calico/lma/pkg/httputils"
)

// RWHandler implements a basic HTTP handler that allows us to have a common
// implementation for most APIs that use common verbs - list and create
type RWHandler[T any, P RequestParams, B BulkRequestParams] interface {
	ReadOnlyHandler[T, P]
	WriteHandler[B]
}

// ReadOnlyHandler implements a basic HTTP handler that allows a read only
// implementation for listing APIs
type ReadOnlyHandler[T any, P RequestParams] interface {
	List() http.HandlerFunc
}

// WriteHandler implements a basic HTTP handler that allows a write
// implementation for create APIs
type WriteHandler[B BulkRequestParams] interface {
	Create() http.HandlerFunc
}

// DeleteHandler implements a basic HTTP handler that allows a delete
// implementation for create APIs
type DeleteHandler[B BulkRequestParams] interface {
	Delete() http.HandlerFunc
}

// AggregationHandler implements a basic HTTP handler that allows a read only
// implementation for listing data in an aggregated form. Aggregation is
// performed using the underlying fields presents in the raw data that is being
// queried. This handler is used to provide a backwards compatible API
// for components like ui-apis and intrusion detection that make aggregated
// queries in Elastic and should not be used for other further implementations
type AggregationHandler[A RequestParams] interface {
	Aggregate() http.HandlerFunc
}

// GenericHandler implements a basic HTTP handler that allows us to have a common
// implementation for most APIs that use common verbs - list, create and aggregate data
type GenericHandler[T any, P RequestParams, B BulkRequestParams, A RequestParams] interface {
	ReadOnlyHandler[T, P]
	WriteHandler[B]
	AggregationHandler[A]
}

// RWDHandler implements a basic HTTP handler that allows us to have a common
// implementation for most APIs that use common verbs - list, create and delete
type RWDHandler[T any, P RequestParams, B BulkRequestParams] interface {
	ReadOnlyHandler[T, P]
	WriteHandler[B]
	DeleteHandler[B]
}

type (
	CreateFn[B BulkRequestParams]  func(context.Context, bapi.ClusterInfo, []B) (*v1.BulkResponse, error)
	ListFn[T any, P RequestParams] func(context.Context, bapi.ClusterInfo, *P) (*v1.List[T], error)
	DeleteFn[B BulkRequestParams]  func(context.Context, bapi.ClusterInfo, []B) (*v1.BulkResponse, error)
	AggregateFn[A RequestParams]   func(context.Context, bapi.ClusterInfo, *A) (*elastic.Aggregations, error)
)

// NewRWHandler returns a generic implementation for a handler that supports both create and list APIs
func NewRWHandler[T any, P RequestParams, B BulkRequestParams](c CreateFn[B], l ListFn[T, P]) RWHandler[T, P, B] {
	ch := createHandler[B]{c}
	lh := listHandler[T, P]{l}
	return &rwHandler[T, P, B]{
		createHandler: ch,
		listHandler:   lh,
	}
}

type rwHandler[T any, P RequestParams, B BulkRequestParams] struct {
	createHandler WriteHandler[B]
	listHandler   ReadOnlyHandler[T, P]
}

func (c rwHandler[T, P, B]) List() http.HandlerFunc {
	return c.listHandler.List()
}

func (c rwHandler[T, P, B]) Create() http.HandlerFunc {
	return c.createHandler.Create()
}

// NewReadOnlyHandler returns a generic implementation for a handler that supports only list APIs
func NewReadOnlyHandler[T any, P RequestParams](l ListFn[T, P]) ReadOnlyHandler[T, P] {
	return &listHandler[T, P]{
		ListFn: l,
	}
}

type listHandler[T any, P RequestParams] struct {
	ListFn[T, P]
}

func (h listHandler[T, P]) List() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		f := logrus.Fields{
			"path":   req.URL.Path,
			"method": req.Method,
		}
		logCtx := logrus.WithFields(f)

		if logrus.IsLevelEnabled(logrus.DebugLevel) {
			// Include the request body in our logs.
			body, err := ReadBody(w, req)
			if err != nil {
				logrus.WithError(err).Warn("Failed to read request body")
			}
			logCtx = logCtx.WithField("body", body)
		}

		params, httpErr := DecodeAndValidateReqParams[P](w, req)
		if httpErr != nil {
			logCtx.WithError(httpErr).Error("Failed to decode/validate request parameters")
			httputils.JSONError(w, httpErr, httpErr.Status)
			return
		}

		// Get the timeout from the request, and use it to build a context.
		timeout, err := Timeout(w, req)
		if err != nil {
			httputils.JSONError(w, &v1.HTTPError{
				Msg:    err.Error(),
				Status: http.StatusBadRequest,
			}, http.StatusBadRequest)
		}
		ctx, cancel := context.WithTimeout(req.Context(), timeout.Duration)
		defer cancel()

		// Get cluster and tenant information, which will have been populated by middleware.
		clusterInfo := bapi.ClusterInfo{
			Cluster: middleware.ClusterIDFromContext(req.Context()),
			Tenant:  middleware.TenantIDFromContext(req.Context()),
		}

		// Perform the request
		response, err := h.ListFn(ctx, clusterInfo, params)
		if err != nil {
			logCtx.WithError(err).Error("Error performing list")
			httputils.JSONError(w, &v1.HTTPError{
				Status: http.StatusInternalServerError,
				Msg:    err.Error(),
			}, http.StatusInternalServerError)
			return
		}
		logCtx.WithField("response", response).Debugf("Completed request")
		httputils.Encode(w, response)
	}
}

type createHandler[B BulkRequestParams] struct {
	CreateFn[B]
}

func (h createHandler[B]) Create() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		f := logrus.Fields{
			"path":   req.URL.Path,
			"method": req.Method,
		}
		logCtx := logrus.WithFields(f)

		if logrus.IsLevelEnabled(logrus.DebugLevel) {
			// Include the request body in our logs.
			body, err := ReadBody(w, req)
			if err != nil {
				logrus.WithError(err).Warn("Failed to read request body")
			}
			logCtx = logCtx.WithField("body", body)
		}

		data, httpErr := DecodeAndValidateBulkParams[B](w, req)
		if httpErr != nil {
			logCtx.WithError(httpErr).Error("Failed to decode/validate request parameters")
			httputils.JSONError(w, httpErr, httpErr.Status)
			return
		}

		// Bulk creation requests don't include a timeout, so use the default.
		ctx, cancel := context.WithTimeout(context.Background(), v1.DefaultTimeOut)
		defer cancel()
		clusterInfo := bapi.ClusterInfo{
			Cluster: middleware.ClusterIDFromContext(req.Context()),
			Tenant:  middleware.TenantIDFromContext(req.Context()),
		}

		// Call the creation function.
		response, err := h.CreateFn(ctx, clusterInfo, data)
		if err != nil {
			logCtx.WithError(err).Error("Error performing bulk ingestion")
			httputils.JSONError(w, &v1.HTTPError{
				Status: http.StatusInternalServerError,
				Msg:    err.Error(),
			}, http.StatusInternalServerError)
			return
		}
		logCtx.WithField("response", response).Debugf("Completed request")
		httputils.Encode(w, response)
	}
}

type compositeHandler[T any, P RequestParams, B BulkRequestParams, A RequestParams] struct {
	createHandler      WriteHandler[B]
	listHandler        ReadOnlyHandler[T, P]
	aggregationHandler AggregationHandler[A]
}

func (c compositeHandler[T, P, B, A]) List() http.HandlerFunc {
	return c.listHandler.List()
}

func (c compositeHandler[T, P, B, A]) Create() http.HandlerFunc {
	return c.createHandler.Create()
}

func (c compositeHandler[T, P, B, A]) Aggregate() http.HandlerFunc {
	return c.aggregationHandler.Aggregate()
}

// NewCompositeHandler returns a generic implementation for a handler that supports aggregation
func NewCompositeHandler[T any, P RequestParams, B BulkRequestParams, A RequestParams](c CreateFn[B], l ListFn[T, P], a AggregateFn[A]) GenericHandler[T, P, B, A] {
	ch := createHandler[B]{c}
	lh := listHandler[T, P]{l}
	ah := aggregationHandler[A]{a}
	return &compositeHandler[T, P, B, A]{
		createHandler:      ch,
		listHandler:        lh,
		aggregationHandler: ah,
	}
}

// GenericAggregationHandler implements a basic HTTP handler that allows us to have a common
// implementation for most APIs that use aggregation
type aggregationHandler[A RequestParams] struct {
	AggregateFn[A]
}

// NewAggregationHandler returns a generic implementation for a handler that supports aggregation
func NewAggregationHandler[A RequestParams](a AggregateFn[A]) AggregationHandler[A] {
	return &aggregationHandler[A]{
		AggregateFn: a,
	}
}

// Aggregate handles retrieval of time-series aggregated statistics (if the number of buckets
// is greater than 0) or data aggregation based on the underlying fields existing
// for the raw data queried. For example, aggregate flow logs by source_namespace
// field and source_name_aggr fields. An aggregation can also contain metrics like average/max/min/count
// that are extracted from aggregated raw results
func (h aggregationHandler[A]) Aggregate() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		f := logrus.Fields{
			"path":   req.URL.Path,
			"method": req.Method,
		}
		logCtx := logrus.WithFields(f)

		if logrus.IsLevelEnabled(logrus.DebugLevel) {
			// Include the request body in our logs.
			body, err := ReadBody(w, req)
			if err != nil {
				logrus.WithError(err).Warn("Failed to read request body")
			}
			logCtx = logCtx.WithField("body", body)
		}

		reqParams, httpErr := DecodeAndValidateReqParams[A](w, req)
		if httpErr != nil {
			logCtx.WithError(httpErr).Error("Failed to decode/validate request parameters")
			httputils.JSONError(w, httpErr, httpErr.Status)
			return
		}

		// Get the timeout from the request, and use it to build a context.
		timeout, err := Timeout(w, req)
		if err != nil {
			httputils.JSONError(w, &v1.HTTPError{
				Msg:    err.Error(),
				Status: http.StatusBadRequest,
			}, http.StatusBadRequest)
		}
		ctx, cancel := context.WithTimeout(req.Context(), timeout.Duration)
		defer cancel()

		clusterInfo := bapi.ClusterInfo{
			Cluster: middleware.ClusterIDFromContext(req.Context()),
			Tenant:  middleware.TenantIDFromContext(req.Context()),
		}

		response, err := h.AggregateFn(ctx, clusterInfo, reqParams)
		if err != nil {
			logCtx.WithError(err).Error("Failed to list aggregations")
			httputils.JSONError(w, &v1.HTTPError{
				Status: http.StatusInternalServerError,
				Msg:    err.Error(),
			}, http.StatusInternalServerError)
			return
		}

		logCtx.WithField("response", response).Debugf("Completed request")
		httputils.Encode(w, response)
	}
}

func ReadBody(w http.ResponseWriter, req *http.Request) (string, error) {
	req.Body = http.MaxBytesReader(w, req.Body, maxBulkBytes)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return "", err
	}
	req.Body = io.NopCloser(bytes.NewBuffer(body))
	return string(body), nil
}

type deleteHandler[B BulkRequestParams] struct {
	DeleteFn[B]
}

func (h deleteHandler[B]) Delete() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		f := logrus.Fields{
			"path":   req.URL.Path,
			"method": req.Method,
		}
		logCtx := logrus.WithFields(f)

		if logrus.IsLevelEnabled(logrus.DebugLevel) {
			// Include the request body in our logs.
			body, err := ReadBody(w, req)
			if err != nil {
				logrus.WithError(err).Warn("Failed to read request body")
			}
			logCtx = logCtx.WithField("body", body)
		}

		data, httpErr := DecodeAndValidateBulkParams[B](w, req)
		if httpErr != nil {
			logCtx.WithError(httpErr).Error("Failed to decode/validate request parameters")
			httputils.JSONError(w, httpErr, httpErr.Status)
			return
		}

		// Bulk creation requests don't include a timeout, so use the default.
		ctx, cancel := context.WithTimeout(context.Background(), v1.DefaultTimeOut)
		defer cancel()
		clusterInfo := bapi.ClusterInfo{
			Cluster: middleware.ClusterIDFromContext(req.Context()),
			Tenant:  middleware.TenantIDFromContext(req.Context()),
		}

		// Call the creation function.
		response, err := h.DeleteFn(ctx, clusterInfo, data)
		if err != nil {
			logCtx.WithError(err).Error("Error performing bulk delete")
			httputils.JSONError(w, &v1.HTTPError{
				Status: http.StatusInternalServerError,
				Msg:    err.Error(),
			}, http.StatusInternalServerError)
			return
		}
		logCtx.WithField("response", response).Debugf("Completed request")
		httputils.Encode(w, response)
	}
}

type rwdHandler[T any, P RequestParams, B BulkRequestParams] struct {
	createHandler WriteHandler[B]
	listHandler   ReadOnlyHandler[T, P]
	deleteHandler DeleteHandler[B]
}

func (c rwdHandler[T, P, B]) List() http.HandlerFunc {
	return c.listHandler.List()
}

func (c rwdHandler[T, P, B]) Create() http.HandlerFunc {
	return c.createHandler.Create()
}

func (c rwdHandler[T, P, B]) Delete() http.HandlerFunc {
	return c.deleteHandler.Delete()
}

// NewRWDHandler returns a generic implementation for a handler that supports create, delete and list APIs
func NewRWDHandler[T any, P RequestParams, B BulkRequestParams](c CreateFn[B], l ListFn[T, P], d DeleteFn[B]) RWDHandler[T, P, B] {
	ch := createHandler[B]{c}
	lh := listHandler[T, P]{l}
	dh := deleteHandler[B]{d}
	return &rwdHandler[T, P, B]{
		createHandler: ch,
		listHandler:   lh,
		deleteHandler: dh,
	}
}
