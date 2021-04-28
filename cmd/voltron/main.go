// Copyright (c) 2019-2020 Tigera, Inc. All rights reserved.

package main

import (
	"fmt"
	"net"
	"net/url"

	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"
	"github.com/tigera/voltron/internal/pkg/proxy"
	"github.com/tigera/voltron/internal/pkg/regex"
	"github.com/tigera/voltron/internal/pkg/utils"

	"github.com/projectcalico/apiserver/pkg/authentication"
	"github.com/tigera/lma/pkg/auth"
	"github.com/tigera/voltron/internal/pkg/bootstrap"
	"github.com/tigera/voltron/internal/pkg/config"
	"github.com/tigera/voltron/internal/pkg/server"
)

func main() {
	cfg := config.Config{}
	if err := envconfig.Process(config.EnvConfigPrefix, &cfg); err != nil {
		log.Fatal(err)
	}

	bootstrap.ConfigureLogging(cfg.LogLevel)
	log.Infof("Starting %s with %s", config.EnvConfigPrefix, cfg)

	if cfg.PProf {
		go func() {
			err := bootstrap.StartPprof()
			log.WithError(err).Fatal("PProf exited.")
		}()
	}

	addr := fmt.Sprintf("%v:%v", cfg.Host, cfg.Port)

	kubernetesAPITargets, err := regex.CompileRegexStrings([]string{
		`^/api/?`,
		`^/apis/?`,
	})

	if err != nil {
		log.WithError(err).Fatalf("Failed to parse tunnel target whitelist.")
	}

	opts := []server.Option{
		server.WithDefaultAddr(addr),
		server.WithKeepAliveSettings(cfg.KeepAliveEnable, cfg.KeepAliveInterval),
		server.WithExternalCredsFiles(cfg.HTTPSCert, cfg.HTTPSKey),
		server.WithKubernetesAPITargets(kubernetesAPITargets),
	}

	config := bootstrap.NewRestConfig(cfg.K8sConfigPath)
	k8s := bootstrap.NewK8sClientWithConfig(config)

	authn, err := authentication.New()
	if err != nil {
		log.WithError(err).Fatalf("Failed to configure authenticator.")
	}

	if cfg.EnableMultiClusterManagement {
		tunnelX509Cert, tunnelX509Key, err := utils.LoadX509Pair(cfg.TunnelCert, cfg.TunnelKey)
		if err != nil {
			log.WithError(err).Fatal("couldn't load tunnel X509 key pair")
		}

		// With the introduction of Centralized ElasticSearch for Multi-cluster Management,
		// certain categories of requests related to a specific cluster will be proxied
		// within the Management cluster (instead of being sent down a secure tunnel to the
		// actual Managed cluster).
		// In the setup below, we create a list of URI paths that should still go through the
		// tunnel down to a Managed cluster. Requests that do not match this whitelist, will
		// instead be proxied locally (within the Management cluster itself using the
		// defaultProxy that is set up later on in this function). The whitelist is used
		// within the server's clusterMuxer handler.
		tunnelTargetWhitelist, err := regex.CompileRegexStrings([]string{
			`^/api/?`,
			`^/apis/?`,
		})

		if err != nil {
			log.WithError(err).Fatalf("Failed to parse tunnel target whitelist.")
		}

		kibanaURL, err := url.Parse(cfg.KibanaEndpoint)
		if err != nil {
			log.WithError(err).Fatalf("failed to parse Kibana endpoint %s", cfg.KibanaEndpoint)
		}

		sniServiceMap := map[string]string{
			kibanaURL.Hostname(): kibanaURL.Host, // Host includes the port, Hostname does not
		}

		opts = append(opts,
			server.WithInternalCredFiles(cfg.InternalHTTPSCert, cfg.InternalHTTPSKey),
			server.WithPublicAddr(cfg.PublicIP),
			server.WithTunnelCreds(tunnelX509Cert, tunnelX509Key),
			server.WithForwardingEnabled(cfg.ForwardingEnabled),
			server.WithDefaultForwardServer(cfg.DefaultForwardServer, cfg.DefaultForwardDialRetryAttempts, cfg.DefaultForwardDialInterval),
			server.WithTunnelTargetWhitelist(tunnelTargetWhitelist),
			server.WithSNIServiceMap(sniServiceMap),
		)
	}

	targetList := []bootstrap.Target{
		{
			Path:         "/api/",
			Dest:         cfg.K8sEndpoint,
			CABundlePath: "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
		},
		{
			Path:         "/apis/",
			Dest:         cfg.K8sEndpoint,
			CABundlePath: "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
		},
		{
			Path:             "/tigera-elasticsearch/",
			Dest:             cfg.ElasticEndpoint,
			PathRegexp:       []byte("^/tigera-elasticsearch/?"),
			PathReplace:      []byte("/"),
			AllowInsecureTLS: true,
		},
		{
			Path:         cfg.KibanaBasePath,
			Dest:         cfg.KibanaEndpoint,
			CABundlePath: cfg.KibanaCABundlePath,
		},
		{
			Path:             "/",
			Dest:             cfg.NginxEndpoint,
			AllowInsecureTLS: true,
		},
	}

	if cfg.EnableCompliance {
		targetList = append(targetList, bootstrap.Target{
			Path:             "/compliance/",
			Dest:             cfg.ComplianceEndpoint,
			CABundlePath:     cfg.ComplianceCABundlePath,
			AllowInsecureTLS: cfg.ComplianceInsecureTLS,
		})
	}

	if cfg.DexEnabled {

		targetList = append(targetList, bootstrap.Target{
			Path:         cfg.DexBasePath,
			Dest:         cfg.DexURL,
			CABundlePath: cfg.DexCABundlePath,
		})

		opts := []auth.DexOption{
			auth.WithGroupsClaim(cfg.DexGroupsClaim),
			auth.WithJWKSURL(cfg.DexJWKSURL),
			auth.WithUsernamePrefix(cfg.DexUsernamePrefix),
			auth.WithGroupsPrefix(cfg.DexGroupsPrefix),
		}

		dex, err := auth.NewDexAuthenticator(
			cfg.DexIssuer,
			cfg.DexClientID,
			cfg.DexUsernameClaim,
			opts...)
		if err != nil {
			log.WithError(err).Panic("Unable to create dex authenticator")
		}
		// Make an aggregated authenticator that can deal with tokens from different issuers.
		authn = auth.NewAggregateAuthenticator(dex, authn)

	}

	targets, err := bootstrap.ProxyTargets(targetList)

	if err != nil {
		log.WithError(err).Fatal("Failed to parse default proxy targets.")
	}

	defaultProxy, err := proxy.New(targets)
	if err != nil {
		log.WithError(err).Fatalf("Failed to create a default k8s proxy.")
	}
	opts = append(opts, server.WithDefaultProxy(defaultProxy))

	srv, err := server.New(
		k8s,
		config,
		authn,
		opts...,
	)

	if err != nil {
		log.WithError(err).Fatal("Failed to create server.")
	}

	if cfg.EnableMultiClusterManagement {
		lisTun, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.TunnelHost, cfg.TunnelPort))
		if err != nil {
			log.WithError(err).Fatal("Failed to create tunnel listener.")
		}

		go func() {
			err := srv.ServeTunnelsTLS(lisTun)
			log.WithError(err).Fatal("Tunnel server exited.")
		}()

		go func() {
			err := srv.WatchK8s()
			log.WithError(err).Fatal("K8s watcher exited.")
		}()

		log.Infof("Voltron listens for tunnels at %s", lisTun.Addr().String())
	}

	log.Infof("Voltron listens for HTTP request at %s", addr)
	if err := srv.ListenAndServeHTTPS(); err != nil {
		log.Fatal(err)
	}
}
