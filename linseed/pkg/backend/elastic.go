// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package backend

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/projectcalico/calico/linseed/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/projectcalico/calico/linseed/pkg/config"

	"github.com/olivere/elastic/v7"
	"github.com/sirupsen/logrus"

	calicotls "github.com/projectcalico/calico/crypto/pkg/tls"
	lmaelastic "github.com/projectcalico/calico/lma/pkg/elastic"
)

// MustGetElasticClient will create an elastic client or stop execution
// if configurations like certificate paths are invalid
func MustGetElasticClient(cfg config.Config) lmaelastic.Client {
	options := []elastic.ClientOptionFunc{
		elastic.SetURL(fmt.Sprintf("%s://%s:%s", cfg.ElasticScheme, cfg.ElasticHost, cfg.ElasticPort)),
		elastic.SetScheme(cfg.ElasticScheme),
		elastic.SetGzip(cfg.ElasticGZIPEnabled),
		elastic.SetSniff(cfg.ElasticSniffingEnabled),
	}

	if cfg.ElasticUsername != "" && cfg.ElasticPassword != "" {
		options = append(options, elastic.SetBasicAuth(cfg.ElasticUsername, cfg.ElasticPassword))
	} else {
		logrus.Warn("No credentials were passed in for Elastic. Will connect to ES without credentials")
	}

	// Use the standard logger to inherit configuration.
	log := logrus.StandardLogger()

	switch strings.ToLower(cfg.LogLevel) {
	case "error":
		options = append(options, elastic.SetErrorLog(log))
	case "info", "debug", "warning":
		options = append(options, elastic.SetInfoLog(log))
	case "trace":
		options = append(options, elastic.SetTraceLog(log))
	}

	options = append(options, elastic.SetHttpClient(mustGetHTTPClient(cfg)))
	esClient, err := elastic.NewClient(options...)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create Elastic client")
	}

	return lmaelastic.NewWithClient(esClient)
}

func mustGetHTTPClient(config config.Config) *http.Client {
	if config.ElasticScheme == "http" {
		logrus.Warn("SSL verification is disabled for Elastic communication. Will use a default HTTP client")
		return &http.Client{Transport: &metricsRoundTripper{defaultTransport: http.DefaultTransport}}
	}

	// Configure TLS
	tlsConfig := calicotls.NewTLSConfig(config.FIPSModeEnabled)

	// Configure CA certificates
	caCertPool := mustGetCACertPool(config)
	tlsConfig.RootCAs = caCertPool

	// Configure clients certificate if needed
	if config.ElasticMTLSEnabled {
		clientCert := mustGetClientCert(config)
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}

	transport := &http.Transport{TLSClientConfig: tlsConfig}

	return &http.Client{Transport: &metricsRoundTripper{defaultTransport: transport}}
}

func mustGetClientCert(config config.Config) tls.Certificate {
	// Read client certificate
	clientCert, err := tls.LoadX509KeyPair(config.ElasticClientCert, config.ElasticClientKey)
	if err != nil {
		logrus.WithError(err).Fatal("Failed load client x509 certificates")
	}
	return clientCert
}

func mustGetCACertPool(config config.Config) *x509.CertPool {
	// Read CA cert file
	caCert, err := os.ReadFile(config.ElasticCA)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to read CA certificate")
	}

	// Append CA to cert pool
	caCertPool := x509.NewCertPool()
	ok := caCertPool.AppendCertsFromPEM(caCert)
	if !ok {
		logrus.Fatal("Failed to parse root certificate")
	}
	return caCertPool
}

type metricsRoundTripper struct {
	defaultTransport http.RoundTripper
}

func (t *metricsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now().UTC()
	resp, err := t.defaultTransport.RoundTrip(req)

	if resp != nil {
		metrics.ElasticResponseDuration.With(t.methodPathLabels(req)).Observe(time.Since(start).Seconds())

		metrics.ElasticResponseStatus.With(t.methodCodePathLabels(req, resp)).Inc()
	}

	if err != nil {
		metrics.ElasticConnectionErrors.With(t.methodCodePathLabels(req, resp)).Inc()
	}

	return resp, err
}

func (t *metricsRoundTripper) methodPathLabels(req *http.Request) prometheus.Labels {
	return prometheus.Labels{
		metrics.LabelPath:   t.minifiedPath(req),
		metrics.LabelMethod: req.Method,
	}
}

func (t *metricsRoundTripper) methodCodePathLabels(req *http.Request, resp *http.Response) prometheus.Labels {
	return prometheus.Labels{
		metrics.LabelMethod: req.Method,
		metrics.LabelCode:   strconv.Itoa(resp.StatusCode),
		metrics.LabelPath:   t.minifiedPath(req),
	}
}

func (t *metricsRoundTripper) minifiedPath(req *http.Request) string {
	if strings.HasPrefix(req.URL.Path, "/_cat/aliases") {
		return "/_cat/aliases"
	}

	if strings.HasPrefix(req.URL.Path, "/_template") {
		return "/_template"
	}

	if strings.HasPrefix(req.URL.Path, "/_bulk") {
		return "/_bulk"
	}

	if strings.HasSuffix(req.URL.Path, "/_search") {
		return "/_search"
	}

	if strings.HasPrefix(req.URL.Path, "/<tigera_secure_ee_") {
		return "/<tigera_secure_ee_*>"
	}

	return req.URL.Path
}
