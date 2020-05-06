// Copyright (c) 2019 Tigera, Inc. All rights reserved.

package main

import (
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"sync"
	"time"

	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"

	"github.com/tigera/voltron/internal/pkg/bootstrap"
	"github.com/tigera/voltron/internal/pkg/client"
	"github.com/tigera/voltron/pkg/version"
)

const (
	// EnvConfigPrefix represents the prefix used to load ENV variables required for startup
	EnvConfigPrefix = "GUARDIAN"
)

var (
	versionFlag = flag.Bool("version", false, "Print version information")
)

// Config is a configuration used for Guardian
type config struct {
	LogLevel   string `default:"INFO"`
	CertPath   string `default:"/certs" split_words:"true" json:"-"`
	VoltronURL string `required:"true" split_words:"true"`

	KeepAliveEnable   bool `default:"true" split_words:"true"`
	KeepAliveInterval int  `default:"100" split_words:"true"`
	PProf             bool `default:"false"`

	K8sEndpoint string `default:"https://kubernetes.default" split_words:"true"`

	TunnelDialRetryAttempts         int           `default:"20" split_words:"true"`
	TunnelDialRetryInterval         time.Duration `default:"5s" split_words:"true"`
	TunnelDialRecreateOnTunnelClose bool          `default:"true" split_words:"true"`

	Listen     bool   `default:"true"`
	ListenHost string `default:"" split_words:"true"`
	ListenPort string `default:"8080" split_words:"true"`
}

func (cfg config) String() string {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func main() {
	// Parse all command-line flags
	flag.Parse()

	// For --version use case
	if *versionFlag {
		version.Version()
		os.Exit(0)
	}

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

	cert := fmt.Sprintf("%s/managed-cluster.crt", cfg.CertPath)
	key := fmt.Sprintf("%s/managed-cluster.key", cfg.CertPath)
	serverCrt := fmt.Sprintf("%s/management-cluster.crt", cfg.CertPath)
	log.Infof("Voltron Address: %s", cfg.VoltronURL)

	pemCert, err := ioutil.ReadFile(cert)
	if err != nil {
		log.Fatalf("Failed to load cert: %s", err)
	}
	pemKey, err := ioutil.ReadFile(key)
	if err != nil {
		log.Fatalf("Failed to load key: %s", err)
	}

	ca := x509.NewCertPool()
	content, _ := ioutil.ReadFile(serverCrt)
	if ok := ca.AppendCertsFromPEM(content); !ok {
		log.Fatalf("Cannot append the certificate to ca pool: %s", err)
	}

	health, err := client.NewHealth()

	if err != nil {
		log.Fatalf("Failed to create health server: %s", err)
	}

	targets, err := bootstrap.ProxyTargets([]bootstrap.Target{
		{
			Path:         "/api/",
			Dest:         cfg.K8sEndpoint,
			TokenPath:    "/var/run/secrets/kubernetes.io/serviceaccount/token",
			CABundlePath: "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
		},
		{
			Path:         "/apis/",
			Dest:         cfg.K8sEndpoint,
			TokenPath:    "/var/run/secrets/kubernetes.io/serviceaccount/token",
			CABundlePath: "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
		},
	})
	if err != nil {
		log.Fatalf("Failed to parse default proxy targets: %s", err)
	}

	cli, err := client.New(
		cfg.VoltronURL,
		client.WithKeepAliveSettings(cfg.KeepAliveEnable, cfg.KeepAliveInterval),
		client.WithProxyTargets(targets),
		client.WithTunnelCreds(pemCert, pemKey, ca),
		client.WithTunnelDialRetryAttempts(cfg.TunnelDialRetryAttempts),
		client.WithTunnelDialRetryInterval(cfg.TunnelDialRetryInterval),
	)

	if err != nil {
		log.Fatalf("Failed to create server: %s", err)
	}

	go func() {
		// Health checks start meaning everything before has worked.
		if err = health.ListenAndServeHTTP(); err != nil {
			log.Fatalf("Health exited with error: %s", err)
		}
	}()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := cli.ServeTunnelHTTP(); err != nil {
			log.WithError(err).Fatal("Serving the tunnel exited")
		}
	}()

	if cfg.Listen {
		log.Infof("Listening on %s:%s for connections to proxy to voltron", cfg.ListenHost, cfg.ListenPort)

		listener, err := net.Listen("tcp", fmt.Sprintf("%s:%s", cfg.ListenHost, cfg.ListenPort))
		if err != nil {
			log.WithError(err).Fatalf("Failed to listen on %s:%s", cfg.ListenHost, cfg.ListenPort)
		}

		wg.Add(1)
		go func() {
			defer wg.Done()

			if err := cli.AcceptAndProxy(listener); err != nil {
				log.WithError(err).Error("AcceptAndProxy returned with an error")
			}
		}()
	}

	wg.Wait()
}
