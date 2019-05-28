// Copyright (c) 2019 Tigera, Inc. All rights reserved.
package elastic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	"github.com/olivere/elastic"
	log "github.com/sirupsen/logrus"

	"github.com/tigera/compliance/pkg/config"
	"github.com/tigera/compliance/pkg/event"
	"github.com/tigera/compliance/pkg/list"
	"github.com/tigera/compliance/pkg/report"
)

type Client interface {
	report.AuditLogReportHandler
	report.FlowLogReportHandler
	report.ReportRetriever
	report.ReportStorer
	list.Destination
	event.Fetcher
	Backend() *elastic.Client
}

// client implements the Client interface.
type client struct {
	*elastic.Client
	indexSuffix string
}

// MustGetElasticClient returns the elastic Client, or panics if it's not possible.
func MustGetElasticClient(cfg *config.Config) Client {
	c, err := NewFromConfig(cfg)
	if err != nil {
		log.Panicf("Unable to connect to Elasticsearch: %v", err)
	}
	return c
}

// NewFromConfig returns a new elastic Client using the supplied configuration.
func NewFromConfig(cfg *config.Config) (Client, error) {
	ca, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}

	h := &http.Client{}
	if cfg.ParsedElasticURL.Scheme == "https" {
		if cfg.ElasticCA != "" {
			cert, err := ioutil.ReadFile(cfg.ElasticCA)
			if err != nil {
				return nil, err
			}
			ok := ca.AppendCertsFromPEM(cert)
			if !ok {
				return nil, fmt.Errorf("invalid Elasticsearch CA in environment variable ELASTIC_CA")
			}
		}

		h.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: ca}}
	}

	return New(
		h, cfg.ParsedElasticURL, cfg.ElasticUser, cfg.ElasticPassword, cfg.ElasticIndexSuffix,
		cfg.ElasticConnRetries, cfg.ElasticConnRetryInterval, cfg.ParsedLogLevel == log.DebugLevel)
}

// New returns a new elastic client using the supplied parameters. This method performs retries if creation of the
// client fails.
func New(
	h *http.Client, url *url.URL, username, password, indexSuffix string,
	retries int, retryInterval time.Duration, trace bool,
) (Client, error) {
	options := []elastic.ClientOptionFunc{
		elastic.SetURL(url.String()),
		elastic.SetHttpClient(h),
		elastic.SetErrorLog(log.StandardLogger()),
		elastic.SetSniff(false),
	}
	if trace {
		options = append(options, elastic.SetTraceLog(log.StandardLogger()))
	}
	if username != "" {
		options = append(options, elastic.SetBasicAuth(username, password))
	}

	var err error
	var c *elastic.Client
	for i := 0; i < retries; i++ {
		log.Info("Connecting to elastic")
		if c, err = elastic.NewClient(options...); err == nil {
			return &client{c, indexSuffix}, nil
		}
		time.Sleep(retryInterval)
	}
	log.Errorf("Unable to connect to Elastic after %d retries", retries)
	return nil, err
}

func (c *client) ensureIndexExists(index, mapping string) error {
	clog := log.WithField("index", index)

	// Check if index exists.
	exists, err := c.IndexExists(index).Do(context.Background())
	if err != nil {
		clog.WithError(err).Error("failed to check if index exists")
		return err
	}

	// Return if index exists
	if exists {
		clog.Info("index already exists, bailing out...")
		return nil
	}

	// Create index.
	clog.Info("index doesn't exist, creating...")
	createIndex, err := c.
		CreateIndex(index).
		Body(mapping).
		Do(context.Background())
	if err != nil {
		if elastic.IsConflict(err) {
			clog.Info("index already exists")
			return nil
		}
		clog.WithError(err).Error("failed to create index")
		return err
	}

	// Check if acknowledged
	if !createIndex.Acknowledged {
		clog.Warn("index creation has not yet been acknowledged...")
	}
	clog.Info("index successfully created!")
	return nil
}

func (c *client) clusterIndex(index, postfix string) string {
	return fmt.Sprintf("%s.%s.%s", index, c.indexSuffix, postfix)
}

func (c *client) Backend() *elastic.Client {
	return c.Client
}

func (c *client) Reset() {
	_, _ = c.Client.DeleteIndex(
		c.clusterIndex(reportsIndex, "*"),
		c.clusterIndex(snapshotsIndex, "*"),
		c.clusterIndex(auditLogIndex, "*"),
	).Do(context.Background())
}
