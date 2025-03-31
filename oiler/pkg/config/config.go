// Copyright (c) 2025 Tigera, Inc. All rights reserved.

package config

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/sirupsen/logrus"

	backend "github.com/projectcalico/calico/linseed/pkg/backend/api"
	linseed "github.com/projectcalico/calico/linseed/pkg/config"
)

const (
	// EnvConfigPrefix represents the prefix used to load ENV variables required for startup
	EnvConfigPrefix   = "OILER"
	OilerValidateMode = "validate"
	OilerMigrateMode  = "migrate"
)

type OilerMode string

// Config defines the parameters of the application
type Config struct {
	// LogLevel across the application and Elasticsearch clients
	LogLevel string `default:"INFO" split_words:"true"`
	JobName  string `split_words:"true"`

	Mode     OilerMode        `split_words:"true" default:"migrate"`
	DataType backend.DataType `split_words:"true"`

	ElasticPageSize int           `split_words:"true" default:"1000"`
	ElasticTimeOut  time.Duration `split_words:"true" default:"5m"`
	WaitForNewData  time.Duration `split_words:"true" default:"60s"`

	KubeConfig  string `envconfig:"KUBECONFIG"`
	Namespace   string `split_words:"true" default:"tigera-elasticsearch"`
	MetricsPort int    `split_words:"true" default:"8080"`

	// Primary Elastic configuration. These configurations will be prefixed with PRIMARY
	// Example: OILER_PRIMARY_ELASTIC_HOST=localhost or PRIMARY_ELASTIC_HOST=localhost
	PrimaryElasticClient *linseed.ElasticClientConfig
	PrimaryBackend       linseed.BackendType `envconfig:"PRIMARY_BACKEND" default:"elastic-multi-index"`
	PrimaryTenantID      string              `envconfig:"PRIMARY_TENANT_ID" default:""`
	Clusters             []string            `split_words:"true"`

	// Secondary Elastic configuration. These configurations will be prefixed with SECONDARY
	// Example: OILER_SECONDARY_ELASTIC_HOST=localhost or SECONDARY_ELASTIC_HOST=localhost
	SecondaryElasticClient *linseed.ElasticClientConfig
	SecondaryBackend       linseed.BackendType `envconfig:"SECONDARY_BACKEND" default:"elastic-multi-index"`
	SecondaryTenantID      string              `envconfig:"SECONDARY_TENANT_ID" default:""`
}

func LoadConfig() (*Config, error) {
	var err error
	config := &Config{}
	if err = envconfig.Process(EnvConfigPrefix, config); err != nil {
		logrus.WithError(err).Fatal("Unable to load envconfig %w", err)
	}

	// Process elastic configuration for the primary location
	primaryElasticConfig := &linseed.ElasticClientConfig{}
	if err = envconfig.Process(fmt.Sprintf("%s_%s", EnvConfigPrefix, "PRIMARY"), primaryElasticConfig); err != nil {
		logrus.WithError(err).Fatal("Unable to load primary elasticsearch envconfig %w", err)
	}
	config.PrimaryElasticClient = primaryElasticConfig

	// Process elastic configuration for the secondary location
	secondaryElasticConfig := &linseed.ElasticClientConfig{}
	if err = envconfig.Process(fmt.Sprintf("%s_%s", EnvConfigPrefix, "SECONDARY"), secondaryElasticConfig); err != nil {
		logrus.WithError(err).Fatal("Unable to load secondary elasticsearch envconfig %w", err)
	}
	config.SecondaryElasticClient = secondaryElasticConfig

	return config, nil
}
