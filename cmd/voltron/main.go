// Copyright (c) 2019 Tigera, Inc. All rights reserved.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/tigera/voltron/internal/pkg/regex"
	"github.com/tigera/voltron/internal/pkg/utils"
	"github.com/tigera/voltron/pkg/version"

	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"

	"github.com/tigera/voltron/internal/pkg/bootstrap"
	"github.com/tigera/voltron/internal/pkg/proxy"
	"github.com/tigera/voltron/internal/pkg/server"
)

const (
	// EnvConfigPrefix represents the prefix used to load ENV variables required for startup
	EnvConfigPrefix = "VOLTRON"
)

var (
	versionFlag = flag.Bool("version", false, "Print version information")
)

// Config is a configuration used for Voltron
type config struct {
	Port                         int `default:"5555"`
	Host                         string
	TunnelPort                   int    `default:"5566" split_words:"true"`
	TunnelHost                   string `split_words:"true"`
	TunnelCert                   string `default:"/certs/tunnel/cert" split_words:"true" json:"-"`
	TunnelKey                    string `default:"/certs/tunnel/key" split_words:"true" json:"-"`
	LogLevel                     string `default:"INFO"`
	TemplatePath                 string `default:"/tmp/guardian.yaml.tmpl" split_words:"true"`
	PublicIP                     string `default:"127.0.0.1:32453" split_words:"true"`

	// HTTPSCert, HTTPSKey - path to an x509 certificate and its private key used
	// for external communication (Tigera UI <-> Voltron)
	HTTPSCert                    string `default:"/certs/https/cert" split_words:"true" json:"-"`
	HTTPSKey                     string `default:"/certs/https/key" split_words:"true" json:"-"`
	// InternalHTTPSCert, InternalHTTPSKey - path to an x509 certificate and its private key used
	//for internal communication within the K8S cluster
	InternalHTTPSCert            string `default:"/certs/internal/cert" split_words:"true" json:"-"`
	InternalHTTPSKey             string `default:"/certs/internal/key" split_words:"true" json:"-"`

	K8sConfigPath                string `split_words:"true"`
	KeepAliveEnable              bool   `default:"true" split_words:"true"`
	KeepAliveInterval            int    `default:"100" split_words:"true"`
	K8sEndpoint                  string `default:"https://kubernetes.default" split_words:"true"`
	ComplianceEndpoint           string `default:"https://compliance.tigera-compliance.svc.cluster.local" split_words:"true"`
	ComplianceCABundlePath       string `default:"/certs/compliance/tls.crt" split_words:"true"`
	ComplianceInsecureTLS        bool   `default:"false" split_words:"true"`
	ElasticEndpoint              string `default:"https://127.0.0.1:8443" split_words:"true"`
	NginxEndpoint                string `default:"http://127.0.0.1:8080" split_words:"true"`
	PProf                        bool   `default:"false"`
	EnableMultiClusterManagement bool   `default:"false" split_words:"true"`
	KibanaEndpoint               string `default:"https://tigera-secure-kb-http.tigera-kibana.svc:5601" split_words:"true"`
	KibanaBasePath               string `default:"/tigera-kibana" split_words:"true"`
	KibanaCABundlePath           string `default:"/certs/kibana/tls.crt" split_words:"true"`

	// The DefaultForward parameters configure where connections from guardian should be forwarded to by default
	ForwardingEnabled               bool          `default:"true" split_words:"true"`
	DefaultForwardServer            string        `default:"tigera-secure-es-http.tigera-elasticsearch.svc:9200" split_words:"true"`
	DefaultForwardDialRetryAttempts int           `default:"5" split_words:"true"`
	DefaultForwardDialInterval      time.Duration `default:"2s" split_words:"true"`
}

func (cfg config) String() string {
	// Parse all command-line flags
	flag.Parse()

	// For --version use case
	if *versionFlag {
		version.Version()
		os.Exit(0)
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func main() {
	cfg := config{}
	if err := envconfig.Process(EnvConfigPrefix, &cfg); err != nil {
		log.Fatal(err)
	}

	bootstrap.ConfigureLogging(cfg.LogLevel)
	log.Infof("Starting %s with %s", EnvConfigPrefix, cfg)

	if cfg.PProf {
		go func() {
			err := bootstrap.StartPprof()
			log.Fatalf("PProf exited: %s", err)
		}()
	}

	addr := fmt.Sprintf("%v:%v", cfg.Host, cfg.Port)

	opts := []server.Option{
		server.WithDefaultAddr(addr),
		server.WithKeepAliveSettings(cfg.KeepAliveEnable, cfg.KeepAliveInterval),
		server.WithExternalCredsFiles(cfg.HTTPSCert, cfg.HTTPSKey),
		server.WithInternalCredFiles(cfg.InternalHTTPSCert, cfg.InternalHTTPSKey),
	}

	k8s, config := bootstrap.ConfigureK8sClient(cfg.K8sConfigPath)

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
			log.Fatalf("Failed to parse tunnel target whitelist: %s", err)
		}
		opts = append(opts,
			server.WithTemplate(cfg.TemplatePath),
			server.WithPublicAddr(cfg.PublicIP),
			server.WithKeepClusterKeys(),
			server.WithTunnelCreds(tunnelX509Cert, tunnelX509Key),
			server.WithAuthentication(config),
			server.WithForwardingEnabled(cfg.ForwardingEnabled),
			server.WithDefaultForwardServer(cfg.DefaultForwardServer, cfg.DefaultForwardDialRetryAttempts, cfg.DefaultForwardDialInterval),
			server.WithTunnelTargetWhitelist(tunnelTargetWhitelist),
		)
	}

	targets, err := bootstrap.ProxyTargets([]bootstrap.Target{
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
			Path:             "/compliance/",
			Dest:             cfg.ComplianceEndpoint,
			CABundlePath:     cfg.ComplianceCABundlePath,
			AllowInsecureTLS: cfg.ComplianceInsecureTLS,
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
	})

	if err != nil {
		log.Fatalf("Failed to parse default proxy targets: %s", err)
	}

	defaultProxy, err := proxy.New(targets)
	if err != nil {
		log.Fatalf("Failed to create a default k8s proxy: %s", err)
	}
	opts = append(opts, server.WithDefaultProxy(defaultProxy))

	srv, err := server.New(
		k8s,
		opts...,
	)

	if err != nil {
		log.Fatalf("Failed to create server: %s", err)
	}

	if cfg.EnableMultiClusterManagement {
		lisTun, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.TunnelHost, cfg.TunnelPort))
		if err != nil {
			log.Fatalf("Failed to create tunnel listener: %s", err)
		}

		go func() {
			err := srv.ServeTunnelsTLS(lisTun)
			log.Fatalf("Tunnel server exited: %s", err)
		}()

		go func() {
			err := srv.WatchK8s()
			log.Fatalf("K8s watcher exited: %s", err)
		}()

		log.Infof("Voltron listens for tunnels at %s", lisTun.Addr().String())
	}

	log.Infof("Voltron listens for HTTP request at %s", addr)
	if err := srv.ListenAndServeHTTPS(); err != nil {
		log.Fatal(err)
	}
}
