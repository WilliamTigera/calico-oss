// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package audit

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/linseed/pkg/handler"

	authzv1 "k8s.io/api/authorization/v1"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/linseed/pkg/middleware"
	"github.com/projectcalico/calico/lma/pkg/httputils"
)

const (
	LogPath            = "/audit/logs"
	AggsPath           = "/audit/logs/aggregation"
	LogPathBulkPattern = "/audit/logs/%s/bulk"
)

type audit struct {
	logs         bapi.AuditBackend
	aggregations handler.AggregationHandler[v1.AuditLogAggregationParams]
}

func New(logs bapi.AuditBackend) *audit {
	return &audit{
		logs:         logs,
		aggregations: handler.NewAggregationHandler[v1.AuditLogAggregationParams](logs.Aggregations),
	}
}

func (h audit) APIS() []handler.API {
	return []handler.API{
		{
			Method:          "POST",
			URL:             LogPath,
			Handler:         h.GetLogs(),
			AuthzAttributes: &authzv1.ResourceAttributes{Verb: handler.Get, Group: handler.APIGroup, Resource: "auditlogs"},
		},
		{
			Method:          "POST",
			URL:             fmt.Sprintf(LogPathBulkPattern, v1.AuditLogTypeEE),
			Handler:         h.BulkAuditEE(),
			AuthzAttributes: &authzv1.ResourceAttributes{Verb: handler.Create, Group: handler.APIGroup, Resource: "ee_auditlogs"},
		},
		{
			Method:          "POST",
			URL:             fmt.Sprintf(LogPathBulkPattern, v1.AuditLogTypeKube),
			Handler:         h.BulkAuditKube(),
			AuthzAttributes: &authzv1.ResourceAttributes{Verb: handler.Create, Group: handler.APIGroup, Resource: "kube_auditlogs"},
		},
		{
			Method:  "POST",
			URL:     AggsPath,
			Handler: h.aggregations.Aggregate(),
		},
	}
}

// BulkAuditEE handles bulk ingestion requests to add EE Audit logs, typically from fluentd.
func (h audit) BulkAuditEE() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		logs, err := handler.DecodeAndValidateBulkParams[v1.AuditLog](w, req)
		if err != nil {
			log.WithError(err).Error("Failed to decode/validate request parameters")
			var httpErr *v1.HTTPError
			if errors.As(err, &httpErr) {
				httputils.JSONError(w, httpErr, httpErr.Status)
			} else {
				httputils.JSONError(w, &v1.HTTPError{
					Msg:    err.Error(),
					Status: http.StatusBadRequest,
				}, http.StatusBadRequest)
			}
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), v1.DefaultTimeOut)
		defer cancel()
		clusterInfo := bapi.ClusterInfo{
			Cluster: middleware.ClusterIDFromContext(req.Context()),
			Tenant:  middleware.TenantIDFromContext(req.Context()),
		}

		response, err := h.logs.Create(ctx, v1.AuditLogTypeEE, clusterInfo, logs)
		if err != nil {
			log.WithError(err).Error("Failed to ingest EE audit logs")
			httputils.JSONError(w, &v1.HTTPError{
				Status: http.StatusInternalServerError,
				Msg:    err.Error(),
			}, http.StatusInternalServerError)
			return
		}
		log.Debugf("Bulk response is: %+v", response)
		httputils.Encode(w, response)
	}
}

// BulkAuditKube handles bulk ingestion requests to add Kube Audit logs, typically from fluentd.
func (h audit) BulkAuditKube() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		logs, err := handler.DecodeAndValidateBulkParams[v1.AuditLog](w, req)
		if err != nil {
			log.WithError(err).Error("Failed to decode/validate request parameters")
			var httpErr *v1.HTTPError
			if errors.As(err, &httpErr) {
				httputils.JSONError(w, httpErr, httpErr.Status)
			} else {
				httputils.JSONError(w, &v1.HTTPError{
					Msg:    err.Error(),
					Status: http.StatusBadRequest,
				}, http.StatusBadRequest)
			}
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), v1.DefaultTimeOut)
		defer cancel()
		clusterInfo := bapi.ClusterInfo{
			Cluster: middleware.ClusterIDFromContext(req.Context()),
			Tenant:  middleware.TenantIDFromContext(req.Context()),
		}

		response, err := h.logs.Create(ctx, v1.AuditLogTypeKube, clusterInfo, logs)
		if err != nil {
			log.WithError(err).Error("Failed to ingest Kube audit logs")
			httputils.JSONError(w, &v1.HTTPError{
				Status: http.StatusInternalServerError,
				Msg:    err.Error(),
			}, http.StatusInternalServerError)
			return
		}
		log.Debugf("Bulk response is: %+v", response)
		httputils.Encode(w, response)
	}
}

func (h audit) GetLogs() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		reqParams, err := handler.DecodeAndValidateReqParams[v1.AuditLogParams](w, req)
		if err != nil {
			log.WithError(err).Error("Failed to decode/validate request parameters")
			var httpErr *v1.HTTPError
			if errors.As(err, &httpErr) {
				httputils.JSONError(w, httpErr, httpErr.Status)
			} else {
				httputils.JSONError(w, &v1.HTTPError{
					Msg:    err.Error(),
					Status: http.StatusBadRequest,
				}, http.StatusBadRequest)
			}
			return
		}

		if reqParams.Timeout == nil {
			reqParams.Timeout = &metav1.Duration{Duration: v1.DefaultTimeOut}
		}

		clusterInfo := bapi.ClusterInfo{
			Cluster: middleware.ClusterIDFromContext(req.Context()),
			Tenant:  middleware.TenantIDFromContext(req.Context()),
		}

		ctx, cancel := context.WithTimeout(context.Background(), reqParams.Timeout.Duration)
		defer cancel()
		response, err := h.logs.List(ctx, clusterInfo, reqParams)
		if err != nil {
			log.WithError(err).Error("Failed to list Audit logs")
			httputils.JSONError(w, &v1.HTTPError{
				Status: http.StatusInternalServerError,
				Msg:    err.Error(),
			}, http.StatusInternalServerError)
			return
		}

		log.Debugf("%s response is: %+v", LogPath, response)
		httputils.Encode(w, response)
	}
}
