// Copyright (c) 2019 Tigera, Inc. All rights reserved.
package main

import (
	"fmt"
	"net"
	"os"

	log "github.com/sirupsen/logrus"
	certutil "k8s.io/client-go/util/cert"

	"github.com/tigera/es-proxy/pkg/server"
)

// Certificate file paths. If explicit certificates aren't provided
// then self-signed certificates are generated and stored on these
// paths.
const (
	defaultCertFilePath = "/etc/es-proxy/ssl/cert"
	defaultKeyFilePath  = "/etc/es-proxy/ssl/key"
)

func main() {
	logLevel := log.InfoLevel
	logLevelStr := os.Getenv("LOG_LEVEL")
	parsedLogLevel, err := log.ParseLevel(logLevelStr)
	if err == nil {
		logLevel = parsedLogLevel
	} else {
		log.Warnf("Could not parse log level %v, setting log level to %v", logLevelStr, logLevel)
	}
	log.SetLevel(logLevel)

	config, err := server.NewConfigFromEnv()
	if err != nil {
		log.WithError(err).Fatal("Configuration Error.")
	}

	// If configuration for certificates isn't provided, then generate one ourseles and
	// set the correct paths.
	if config.CertFile == "" || config.KeyFile == "" {
		config.CertFile = defaultCertFilePath
		config.KeyFile = defaultKeyFilePath
		if err := MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
			log.WithError(err).Fatal("Error creating self-signed certificates", err)
		}
	}

	server.Start(config)

	server.Wait()
}

// Copied from "k8s.io/apiserver/pkg/server/options" for self signed certificate generation.
func MaybeDefaultWithSelfSignedCerts(publicAddress string, alternateDNS []string, alternateIPs []net.IP) error {
	canReadCertAndKey, err := certutil.CanReadCertAndKey(defaultCertFilePath, defaultKeyFilePath)
	if err != nil {
		return err
	}
	if !canReadCertAndKey {
		// add localhost to the valid alternates
		alternateDNS = append(alternateDNS, "localhost")

		if cert, key, err := certutil.GenerateSelfSignedCertKey(publicAddress, alternateIPs, alternateDNS); err != nil {
			return fmt.Errorf("unable to generate self signed cert: %v", err)
		} else {
			if err := certutil.WriteCert(defaultCertFilePath, cert); err != nil {
				return err
			}

			if err := certutil.WriteKey(defaultKeyFilePath, key); err != nil {
				return err
			}
			log.Infof("Generated self-signed cert (%s, %s)", defaultCertFilePath, defaultKeyFilePath)
		}
	}

	return nil
}
