// Copyright (c) 2022-2024 Tigera Inc. All rights reserved.

package config

import (
	"fmt"

	log "github.com/sirupsen/logrus"

	"github.com/kelseyhightower/envconfig"

	"github.com/projectcalico/calico/libcalico-go/lib/logutils"
)

type Config struct {
	LogLevel log.Level `envconfig:"LOG_LEVEL" default:"INFO"`

	// Linseed parameters
	LinseedURL        string `envconfig:"LINSEED_URL" default:"https://tigera-linseed.tigera-elasticsearch.svc"`
	LinseedCA         string `envconfig:"LINSEED_CA" default:"/etc/pki/tls/certs/tigera-ca-bundle.crt"`
	LinseedClientCert string `envconfig:"LINSEED_CLIENT_CERT" default:"/etc/pki/tls/certs/tigera-ca-bundle.crt"`
	LinseedClientKey  string `envconfig:"LINSEED_CLIENT_KEY"`
	LinseedToken      string `envconfig:"LINSEED_TOKEN" default:"/var/run/secrets/kubernetes.io/serviceaccount/token"`

	// For Calico Cloud, the tenant ID to use.
	TenantID string `envconfig:"TENANT_ID"`

	// This setting is required for es proxy that performs the authentication and authorization for a user.
	EnableMultiClusterClient       bool   `envconfig:"ENABLE_MULTI_CLUSTER_CLIENT" default:"false"`
	MultiClusterForwardingCA       string `envconfig:"MULTI_CLUSTER_FORWARDING_CA" default:"/etc/pki/tls/certs/tigera-ca-bundle.crt"`
	MultiClusterForwardingEndpoint string `envconfig:"MULTI_CLUSTER_FORWARDING_ENDPOINT" default:"https://tigera-manager.tigera-manager.svc:9443"`

	TenantNamespace string `envconfig:"TENANT_NAMESPACE" default:""`
}

func LoadConfig() (*Config, error) {
	var err error
	config := &Config{}
	err = envconfig.Process("", config)
	if err != nil {
		return nil, err
	}

	if config.TenantNamespace != "" && config.TenantID == "" {
		return nil, fmt.Errorf("cannot define the TenantID without the TenantNamespace")
	}
	return config, nil
}

func (c *Config) InitializeLogging() {
	log.SetFormatter(&logutils.Formatter{})
	log.AddHook(&logutils.ContextHook{})
	log.SetLevel(c.LogLevel)
}
