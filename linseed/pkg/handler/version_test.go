// Copyright 2022 Tigera. All rights reserved.

package handler_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/projectcalico/calico/linseed/pkg/handler"
)

func TestVersionCheck(t *testing.T) {
	type testResult struct {
		httpStatus int
		httpBody   string
	}

	tests := []struct {
		name string
		want testResult
	}{
		{
			"200 OK",
			testResult{200, "{\"buildDate\":\"\",\"gitCommit\":\"\",\"gitTag\":\"\",\"buildVersion\":\"\"}"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			healthCheck := handler.VersionCheck()

			rec := httptest.NewRecorder()
			req, err := http.NewRequest("GET", "/version", nil)
			assert.NoError(t, err)

			healthCheck.ServeHTTP(rec, req)

			bodyBytes, err := io.ReadAll(rec.Body)
			assert.NoError(t, err)

			assert.Equal(t, tt.want.httpStatus, rec.Result().StatusCode)
			assert.JSONEq(t, tt.want.httpBody, string(bodyBytes))
		})
	}
}
