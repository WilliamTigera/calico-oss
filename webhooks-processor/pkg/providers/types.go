// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package providers

import (
	"context"
	"encoding/json"
	"time"

	lsApi "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	"github.com/sirupsen/logrus"
)

type ProviderRespose struct {
	HttpStatusCode        int       `json:"httpResponseCode"`
	HttpStatusDescription string    `json:"httpResponseDescription"`
	HttpPayload           string    `json:"payload"`
	Timestamp             time.Time `json:"timestamp"`
}

type Provider interface {
	Validate(map[string]string) error
	Process(context.Context, map[string]string, map[string]string, *lsApi.Event) (ProviderRespose, error)
	Config() Config
}

type Config struct {
	RateLimiterDuration time.Duration `default:"1h"`
	RateLimiterCount    uint          `default:"100"`
	RequestTimeout      time.Duration `default:"5s"`
	RetryDuration       time.Duration `default:"2s"`
	RetryTimes          uint          `default:"5"`
}

func (r ProviderRespose) String() string {
	if b, err := json.Marshal(r); err == nil {
		return string(b)
	} else {
		logrus.WithError(err).Error("unable to marshall ProviderResponse structure")
		return ""
	}
}
