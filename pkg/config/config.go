// Copyright (c) 2020 Tigera, Inc. All rights reserved.

package config

import (
	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/libcalico-go/lib/logutils"
)

type Config struct {
	// LogLevel
	LogLevel string `envconfig:"LOG_LEVEL"`

	// Socket to dial
	DialTarget string `envconfig:"FELIX_DIAL_TARGET"`
	// Location of the envoy log files to read from
	EnvoyLogPath string `envconfig:"ENVOY_LOG_PATH"`
	// How long the collector will wait in seconds to collect
	// logs before sending them as a batch.
	EnvoyLogIntervalSecs int `envconfig:"ENVOY_LOG_INTERVAL_SECONDS"`
	// Number requests sent in the batch of logs from the collector.
	// A negative number will return as many requests as possible.
	EnvoyRequestsPerInterval int `envconfig:"ENVOY_LOG_REQUESTS_PER_INTERVAL"`

	// Configuration for tests
	// Sets where the log file should be read from.
	// Defaulted to 2 (end of the file).
	TailWhence int

	// Parsed values
	ParsedLogLevel log.Level
}

func MustLoadConfig() *Config {
	c, err := LoadConfig()
	if err != nil {
		log.Panicf("Error loading configuration: %v", err)
	}
	return c
}

func LoadConfig() (*Config, error) {
	var err error
	config := &Config{}
	err = envconfig.Process("", config)
	if err != nil {
		return nil, err
	}

	// Parse log level.
	config.ParsedLogLevel = logutils.SafeParseLogLevel(config.LogLevel)

	// Default the EnvoyLogPath to /tmp/envoy.log
	if config.EnvoyLogPath == "" {
		config.EnvoyLogPath = "/tmp/envoy.log"
	}

	// Default the EnvoyLogInterval to 5 seconds
	if config.EnvoyLogIntervalSecs == 0 {
		config.EnvoyLogIntervalSecs = 5
	}

	// Default the EnvoyLogBatchSize to negative. This will make the batch size unlimited
	if config.EnvoyRequestsPerInterval == 0 {
		config.EnvoyRequestsPerInterval = -1
	}

	// Make sure that the tail reads from the end of the envoy log.
	config.TailWhence = 2

	return config, nil
}

func (c *Config) InitializeLogging() {
	log.SetFormatter(&logutils.Formatter{})
	log.AddHook(&logutils.ContextHook{})
	log.SetLevel(c.ParsedLogLevel)
}
