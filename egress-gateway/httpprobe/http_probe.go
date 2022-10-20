// Copyright (c) 2022 Tigera, Inc. All rights reserved.

package httpprobe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	url2 "net/url"
	"time"

	"github.com/projectcalico/calico/felix/jitter"
	"github.com/projectcalico/calico/libcalico-go/lib/health"
	"github.com/sirupsen/logrus"
)

const HealthName = "http probes"

func StartBackgroundHTTPProbe(ctx context.Context, urls []string, interval time.Duration, timeout time.Duration, healthAgg *health.HealthAggregator) error {
	healthAgg.RegisterReporter(HealthName, &health.HealthReport{Ready: true}, timeout)
	// Since we want the overall readiness to be "up" if _any_ probe is successful, start one goroutine for each
	// URL.
	for _, url := range urls {
		_, err := url2.Parse(url)
		if err != nil {
			return fmt.Errorf("failed to parse HTTP probe URL %q: %w", url, err)
		}
	}
	for _, url := range urls {
		go LoopDoingProbes(ctx, url, interval, healthAgg)
	}
	return nil
}

func LoopDoingProbes(ctx context.Context, url string, interval time.Duration, healthAgg *health.HealthAggregator) {
	logCtx := logrus.WithField("url", url)
	logCtx.Info("HTTP probe goroutine started.")
	client := &http.Client{
		Timeout: interval, // Using interval here so we time out when we're ready to send the next probe.
	}
	ticker := jitter.NewTicker(interval*95/100, interval*10/100)
	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			logCtx.WithError(err).Warn("HTTP request creation failed")
		} else if resp, err := client.Do(req); err != nil {
			logCtx.WithError(err).Warn("HTTP probe failed")
		} else {
			_, err := io.ReadAll(resp.Body)
			if err != nil {
				logCtx.WithError(err).Warn("HTTP probe failed to read body")
			} else if err = resp.Body.Close(); err != nil {
				logCtx.WithError(err).Warn("HTTP probe failed to close body")
			} else {
				// Success!  We don't care about the status.
				logCtx.WithField("status", resp.Status).Debug("HTTP Probe succeeded")
				healthAgg.Report(HealthName, &health.HealthReport{Ready: true})
			}
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	logrus.Info("HTTP probe exiting: context canceled.")
}
