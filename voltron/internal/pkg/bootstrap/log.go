// Copyright (c) 2019 Tigera, Inc. All rights reserved.

// Package bootstrap contains the configurations that will be applied for
// an application upon starting up
package bootstrap

import (
	"os"

	"github.com/projectcalico/calico/libcalico-go/lib/logutils"

	log "github.com/sirupsen/logrus"
)

// ConfigureLogging configures the logging framework. The logging level that will
// be used is passed in as a parameter. Otherwise, it will default to WARN
// The output will be set to STDOUT and the format is TextFormat
func ConfigureLogging(logLevel string) {
	// Install a hook that adds file/line number information.
	log.AddHook(&logutils.ContextHook{})
	log.SetFormatter(&logutils.Formatter{})
	log.SetOutput(os.Stdout)

	// Override with desired log level
	level, err := log.ParseLevel(logLevel)
	if err != nil {
		log.Error("Invalid logging level passed in. Will use default level set to WARN")
		// Setting default to WARN
		level = log.WarnLevel
	}

	log.SetLevel(level)
}
