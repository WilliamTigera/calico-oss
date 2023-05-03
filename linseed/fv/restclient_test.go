// Copyright (c) 2023 Tigera, Inc. All rights reserved.

//go:build fvtests

package fv_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/projectcalico/calico/libcalico-go/lib/logutils"
	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	"github.com/projectcalico/calico/linseed/pkg/backend/testutils"
	"github.com/projectcalico/calico/linseed/pkg/client/rest"
	"github.com/projectcalico/calico/linseed/pkg/config"
	lmav1 "github.com/projectcalico/calico/lma/pkg/apis/v1"
)

var (
	rc     rest.RESTClient
	tenant string
)

func restSetupAndTeardown(t *testing.T) func() {
	// Hook logrus into testing.T
	config.ConfigureLogging("DEBUG")
	logCancel := logutils.RedirectLogrusToTestingT(t)

	// Create a random cluster name for each test to make sure we don't
	// interfere between tests.
	cluster = testutils.RandomClusterName()

	// Set tenant to the value expected in the FVs.
	tenant = "tenant-a"

	// Build a basic RESTClient.
	var err error
	cfg := rest.Config{
		CACertPath:     "cert/RootCA.crt",
		URL:            "https://localhost:8444/",
		ClientCertPath: "cert/localhost.crt",
		ClientKeyPath:  "cert/localhost.key",
	}
	rc, err = rest.NewClient(tenant, cfg, rest.WithTokenPath(TokenPath))
	require.NoError(t, err)

	return func() {
		logCancel()
	}
}

func TestFV_RESTClient(t *testing.T) {
	t.Run("should reject requests from a client with no client cert", func(t *testing.T) {
		defer restSetupAndTeardown(t)()

		// This test verifies mTLS works as expected.
		badClient, err := rest.NewClient(tenant, rest.Config{
			CACertPath: "cert/RootCA.crt",
			URL:        "https://localhost:8444/",
		})
		require.NoError(t, err)

		params := v1.L3FlowParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: time.Now().Add(-5 * time.Second),
					To:   time.Now(),
				},
			},
		}
		flows := v1.List[v1.L3Flow]{}
		err = badClient.Post().
			Path("/flows").
			Cluster(cluster).
			Params(&params).
			Do(context.TODO()).
			Into(&flows)
		require.Error(t, err)
		require.Contains(t, err.Error(), "bad certificate")
	})

	t.Run("should handle an OK response", func(t *testing.T) {
		defer restSetupAndTeardown(t)()

		// Build and send a request.
		params := v1.L3FlowParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: time.Now().Add(-5 * time.Second),
					To:   time.Now(),
				},
			},
		}
		flows := v1.List[v1.L3Flow]{}

		err := rc.Post().
			Path("/flows").
			Cluster(cluster).
			Params(&params).
			Do(context.TODO()).
			Into(&flows)
		require.NoError(t, err)
	})

	t.Run("should handle a 404 response", func(t *testing.T) {
		defer restSetupAndTeardown(t)()

		// Build and send a request.
		params := v1.L3FlowParams{
			QueryParams: v1.QueryParams{
				TimeRange: &lmav1.TimeRange{
					From: time.Now().Add(-5 * time.Second),
					To:   time.Now(),
				},
			},
		}
		flows := v1.List[v1.L3Flow]{}

		err := rc.Post().
			Path("/bad/url").
			Cluster(cluster).
			Params(&params).
			Do(context.TODO()).
			Into(&flows)
		require.Error(t, err)
	})
}
