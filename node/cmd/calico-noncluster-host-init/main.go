// Copyright (c) 2025 Tigera, Inc. All rights reserved.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/felix/config"
)

var flagSet = flag.NewFlagSet("CalicoNonClusterHostInit", flag.ContinueOnError)

var felixConfig = flagSet.String("felix-config", "/etc/calico/calico-node/calico-node.conf", "Path to the Felix config file")
var renewalThreshold = flagSet.Duration("renewal-threshold", 90*24*time.Hour, "Threshold for certificate renewal")
var timeout = flagSet.Duration("timeout", 3*time.Minute, "Timeout for the certificate request")

func main() {
	err := flagSet.Parse(os.Args[1:])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fileConfig, err := config.LoadConfigFile(*felixConfig)
	if err != nil {
		logrus.WithError(err).WithField("configFile", *felixConfig).Fatal("Failed to load configuration file")
	}

	caFile, ok := fileConfig["TyphaCAFile"]
	if !ok {
		logrus.Fatal("TyphaCAFile not found in configuration file")
	}
	pkFile, ok := fileConfig["TyphaKeyFile"]
	if !ok {
		logrus.Fatal("TyphaKeyFile not found in configuration file")
	}
	certFile, ok := fileConfig["TyphaCertFile"]
	if !ok {
		logrus.Fatal("TyphaCertFile not found in configuration file")
	}

	ctx, cancel := context.WithTimeout(context.TODO(), *timeout)
	defer cancel()

	resCh := make(chan error, 1)
	defer close(resCh)

	go func() {
		certManager, err := newCertificateManager(ctx, caFile, pkFile, certFile)
		if err != nil {
			resCh <- err
			return
		}

		certValid, err := certManager.isCertificateValid(*renewalThreshold)
		if err != nil || !certValid {
			// Rotate private key and request a new certificate when the current certificate is expired.
			if err := certManager.requestAndWriteCertificate(); err != nil {
				resCh <- err
			}
		}
		resCh <- nil
	}()

	select {
	case err := <-resCh:
		if err != nil {
			logrus.WithError(err).Fatal("Failed to obtain certificate")
		}
	case <-ctx.Done():
		if err := ctx.Err(); err != nil {
			logrus.WithError(err).Fatal("Context canceled while obtaining certificate")
		}
	}

	os.Exit(0)
}
