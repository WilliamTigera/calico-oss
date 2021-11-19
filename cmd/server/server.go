// Copyright (c) 2021 Tigera. All rights reserved.
package main

import (
	"os"

	"github.com/projectcalico/libcalico-go/lib/logutils"
	config2 "github.com/tigera/prometheus-service/pkg/handler/config"

	log "github.com/sirupsen/logrus"

	server "github.com/tigera/prometheus-service/pkg/server"
)

const (
	LOG_LEVEL_ENV_VAR = "LOG_LEVEL"
)

func main() {
	setLogger()

	config, err := config2.NewConfigFromEnv()

	if err != nil {
		log.WithError(err).Fatal("Configuration Error.")
	}

	server.Start(config)
	server.Wait()
}

func setLogger() {
	logLevel := log.InfoLevel
	logLevelStr := os.Getenv(LOG_LEVEL_ENV_VAR)
	log.SetFormatter(&logutils.Formatter{})
	parsedLogLevel, err := log.ParseLevel(logLevelStr)

	if err == nil {
		logLevel = parsedLogLevel
	} else {
		log.Warnf("Could not parse log level %v, setting log level to %v", logLevelStr, logLevel)
	}
	log.SetLevel(logLevel)

	// Install a hook that adds file/line number information.
	log.AddHook(&logutils.ContextHook{})
}
