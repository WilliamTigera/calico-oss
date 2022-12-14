// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"

	"k8s.io/client-go/kubernetes"

	"github.com/projectcalico/calico/crypto/pkg/tls"
	lmaauth "github.com/projectcalico/calico/lma/pkg/auth"
	lmak8s "github.com/projectcalico/calico/lma/pkg/k8s"
	cache2 "github.com/projectcalico/calico/packetcapture/pkg/cache"
	"github.com/projectcalico/calico/packetcapture/pkg/capture"
	"github.com/projectcalico/calico/packetcapture/pkg/config"
	"github.com/projectcalico/calico/packetcapture/pkg/handlers"
	"github.com/projectcalico/calico/packetcapture/pkg/middleware"
	"github.com/projectcalico/calico/packetcapture/pkg/version"
)

var (
	versionFlag = flag.Bool("version", false, "Print version information")
)

func main() {
	// Parse all command-line flags
	flag.Parse()

	// For --version use case
	if *versionFlag {
		version.Version()
		os.Exit(0)
	}

	cfg := &config.Config{}
	if err := envconfig.Process(config.EnvConfigPrefix, cfg); err != nil {
		log.Fatal(err)
	}

	// Configure logging
	config.ConfigureLogging(cfg.LogLevel)

	// Boostrap components
	var addr = fmt.Sprintf("%v:%v", cfg.Host, cfg.Port)
	var csFactory = lmak8s.NewClientSetFactory(
		cfg.MultiClusterForwardingCA,
		cfg.MultiClusterForwardingEndpoint)
	var cache = cache2.NewClientCache(csFactory)

	var stop = make(chan struct{})
	defer close(stop)
	go func() {
		// Init the client cache with a default client
		var err = cache.Init()
		if err != nil {
			log.WithError(err).Fatal("Cannot init client cache")
		}
	}()
	authn := mustGetAuthenticator(csFactory, cfg)
	authz := middleware.NewAuthZ(cache)
	var k8sCommands = capture.NewK8sCommands(cache)
	var fileCommands = capture.NewFileCommands(cache)
	var files = handlers.NewFiles(cache, k8sCommands, fileCommands)

	log.Infof("PacketCapture API listening for HTTPS requests at %s", addr)
	// Define handlers
	http.Handle("/version", http.HandlerFunc(version.Handler))
	http.Handle("/health", http.HandlerFunc(handlers.Health))
	http.Handle("/download/", middleware.Parse(middleware.AuthenticationHandler(authn, authz.Authorize(files.Download))))
	http.Handle("/files/", middleware.Parse(middleware.AuthenticationHandler(authn, authz.Authorize(files.Delete))))

	// Start server
	server := &http.Server{
		Addr:      addr,
		TLSConfig: tls.NewTLSConfig(cfg.FIPSModeEnabled),
	}

	log.Fatal(server.ListenAndServeTLS(cfg.HTTPSCert, cfg.HTTPSKey))
}

func mustGetAuthenticator(cs lmak8s.ClientSetFactory, cfg *config.Config) lmaauth.JWTAuth {
	restConfig := cs.NewRestConfigForApplication(lmak8s.DefaultCluster)

	clientSet, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		log.WithError(err).Fatal("Failed to configure k8s client")
	}

	var options []lmaauth.JWTAuthOption
	if cfg.DexEnabled {
		opts := []lmaauth.DexOption{
			lmaauth.WithGroupsClaim(cfg.OIDCAuthGroupsClaim),
			lmaauth.WithJWKSURL(cfg.OIDCAuthJWKSURL),
			lmaauth.WithUsernamePrefix(cfg.OIDCAuthUsernamePrefix),
			lmaauth.WithGroupsPrefix(cfg.OIDCAuthGroupsPrefix),
		}
		if cfg.CalicoCloudRequireTenantClaim {
			if cfg.CalicoCloudTenantClaim == "" {
				log.Panic("Tenant claim not specified")
			}
			opts = append(opts, lmaauth.WithCalicoCloudTenantClaim(cfg.CalicoCloudTenantClaim))
		}
		oidcAuth, err := lmaauth.NewDexAuthenticator(
			cfg.OIDCAuthIssuer,
			cfg.OIDCAuthClientID,
			cfg.OIDCAuthUsernameClaim,
			opts...)
		if err != nil {
			log.WithError(err).Panic("Unable to create dex authenticator")
		}

		options = append(options, lmaauth.WithAuthenticator(cfg.OIDCAuthIssuer, oidcAuth))
	}
	authn, err := lmaauth.NewJWTAuth(restConfig, clientSet, options...)
	if err != nil {
		log.WithError(err).Fatal("Unable to create authn configuration")
	}

	return authn
}
