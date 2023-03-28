// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package handler_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/projectcalico/calico/linseed/pkg/testutils"

	"github.com/projectcalico/calico/libcalico-go/lib/json"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	lmav1 "github.com/projectcalico/calico/lma/pkg/apis/v1"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	"github.com/projectcalico/calico/linseed/pkg/handler"
)

const (
	jsonContentType        = "application/json"
	jsonNewlineContentType = "application/x-ndjson"
)

func TestDecodeAndValidateReqParams(t *testing.T) {
	type testCase[T handler.RequestParams] struct {
		name       string
		req        *http.Request
		want       *T
		wantErr    bool
		errorMsg   string
		statusCode int
	}

	params := v1.L3FlowParams{QueryParams: v1.QueryParams{TimeRange: &lmav1.TimeRange{
		From: time.Unix(0, 0),
		To:   time.Unix(0, 0),
	}}}

	tests := []testCase[v1.L3FlowParams]{
		{
			"no body",
			reqNoBody(jsonContentType),
			&v1.L3FlowParams{},
			true,
			"empty request body",
			http.StatusBadRequest,
		},
		{
			"empty body",
			req("", jsonContentType),
			&v1.L3FlowParams{},
			true,
			"Request body must not be empty",
			http.StatusBadRequest,
		},
		{
			"empty json",
			req("{}", jsonContentType),
			&v1.L3FlowParams{},
			false,
			"",
			http.StatusOK,
		},
		{
			"malformed json",
			req("{#4FEF}", jsonContentType),
			&v1.L3FlowParams{},
			true,
			"Request body contains badly-formed JSON (at position 2)",
			http.StatusBadRequest,
		},
		{
			"other content-type",
			req(marshall(params), "application/xml"),
			&params,
			true,
			"Received a request with content-type (application/xml) that is not supported",
			http.StatusUnsupportedMediaType,
		},

		{
			"with time range",
			req(marshall(params), jsonContentType),
			&params,
			false,
			"",
			200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := handler.DecodeAndValidateReqParams[v1.L3FlowParams](httptest.NewRecorder(), tt.req)

			if tt.wantErr {
				require.Error(t, err)

				var httpErr *v1.HTTPError
				assert.Equal(t, err.Error(), tt.errorMsg)
				if errors.As(err, &httpErr) {
					assert.Equal(t, httpErr.Status, tt.statusCode)
					assert.Equal(t, httpErr.Msg, tt.errorMsg)
				}
			} else {
				if !cmp.Equal(tt.want, got) {
					t.Errorf("want=%#v got %#v", tt.want, got)
				}
			}
		})
	}
}

func req(body string, contentType string) *http.Request {
	req, _ := http.NewRequest("POST", "any", bytes.NewBufferString(body))
	req.Header.Set("Content-type", contentType)
	return req
}

func reqNoBody(contentType string) *http.Request {
	req, _ := http.NewRequest("POST", "any", nil)
	req.Header.Set("Content-type", contentType)
	return req
}

func marshall[T any](params T) string {
	newData, _ := json.Marshal(params)
	return string(newData)
}

func encode[T any](params []T, delim string) string {
	var buffer bytes.Buffer

	for _, p := range params {
		newData, _ := json.Marshal(p)
		buffer.Write(newData)
		buffer.WriteString(delim)
	}

	return buffer.String()
}

func TestValidateFlowLogBulkParams(t *testing.T) {
	type testCase struct {
		name       string
		req        *http.Request
		want       []v1.FlowLog
		wantErr    bool
		errorMsg   string
		statusCode int
	}

	params := []v1.FlowLog{
		{
			DestType:          "wep",
			DestNamespace:     "ns-dest",
			DestNameAggr:      "dest-*",
			DestPort:          testutils.Int64Ptr(90001),
			SourceType:        "wep",
			SourceNamespace:   "ns-source",
			SourceNameAggr:    "source-*",
			SourcePort:        testutils.Int64Ptr(443),
			NumFlows:          1,
			NumFlowsStarted:   1,
			NumFlowsCompleted: 0,
		},
		{
			DestType:          "wep",
			DestNamespace:     "ns-dest",
			DestNameAggr:      "dest-*",
			DestPort:          testutils.Int64Ptr(90002),
			SourceType:        "wep",
			SourceNamespace:   "ns-source",
			SourceNameAggr:    "source-*",
			SourcePort:        testutils.Int64Ptr(443),
			NumFlows:          1,
			NumFlowsStarted:   1,
			NumFlowsCompleted: 0,
		},
	}

	tests := []testCase{
		{
			"no body", reqNoBody(jsonNewlineContentType),
			[]v1.FlowLog{},
			true, "Received a request with an empty body", http.StatusBadRequest,
		},
		{
			"empty body", req("", jsonNewlineContentType),
			[]v1.FlowLog{},
			true, "Request body contains badly-formed JSON", http.StatusBadRequest,
		},
		{
			"malformed json", req("{#4FEF}", jsonNewlineContentType),
			[]v1.FlowLog{},
			true, "Request body contains badly-formed JSON", http.StatusBadRequest,
		},
		{
			"other content-type", req(encode(params, "\n"), "application/xml"), params,
			true, "Received a request with content-type (application/xml) that is not supported", http.StatusUnsupportedMediaType,
		},
		{
			"newline in json field value", req("{\"dest_name_aggr\":\"lorem lipsum\n\"}", jsonNewlineContentType),
			[]v1.FlowLog{},
			true, "Request body contains badly-formed JSON", http.StatusBadRequest,
		},
		{
			"new fields", req("{\"newfields\":\"any\"}", jsonNewlineContentType),
			[]v1.FlowLog{},
			true, "Request body contains badly-formed JSON", http.StatusBadRequest,
		},

		{
			"escaped newline in json field value", req("{\"dest_name_aggr\":\"lorem lipsum\\n\"}", jsonNewlineContentType),
			[]v1.FlowLog{{DestNameAggr: "lorem lipsum\n"}},
			false, "", http.StatusOK,
		},

		{
			"bulk insert -  Linux", req(encode[v1.FlowLog](params, "\n"), jsonNewlineContentType),
			params, false, "",
			200,
		},

		{
			"bulk insert - Windows", req(encode[v1.FlowLog](params, "\r\n"), jsonNewlineContentType),
			params, false, "",
			200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := handler.DecodeAndValidateBulkParams[v1.FlowLog](httptest.NewRecorder(), tt.req)
			if tt.wantErr {
				require.Error(t, err)

				var httpErr *v1.HTTPError
				assert.Equal(t, err.Error(), tt.errorMsg)
				if errors.As(err, &httpErr) {
					assert.Equal(t, httpErr.Status, tt.statusCode)
					assert.Equal(t, httpErr.Msg, tt.errorMsg)
				}

			} else {
				require.NoError(t, err)
				if !cmp.Equal(tt.want, got) {
					t.Errorf("want=%#v got %#v", tt.want, got)
				}
			}
		})
	}
}
