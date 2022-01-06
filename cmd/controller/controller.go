// Copyright 2019 Tigera Inc. All rights reserved.

package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	calicoclient "github.com/tigera/api/pkg/client/clientset_generated/clientset"
	"github.com/tigera/intrusion-detection/controller/pkg/elastic"
	"github.com/tigera/intrusion-detection/controller/pkg/feeds/events"
	"github.com/tigera/intrusion-detection/controller/pkg/feeds/rbac"
	syncElastic "github.com/tigera/intrusion-detection/controller/pkg/feeds/sync/elastic"
	"github.com/tigera/intrusion-detection/controller/pkg/feeds/sync/globalnetworksets"
	feedsWatcher "github.com/tigera/intrusion-detection/controller/pkg/feeds/watcher"
	"github.com/tigera/intrusion-detection/controller/pkg/forwarder"
	"github.com/tigera/intrusion-detection/controller/pkg/globalalert/controllers/alert"
	"github.com/tigera/intrusion-detection/controller/pkg/globalalert/controllers/controller"
	"github.com/tigera/intrusion-detection/controller/pkg/globalalert/controllers/managedcluster"
	"github.com/tigera/intrusion-detection/controller/pkg/health"
	"github.com/tigera/intrusion-detection/controller/pkg/util"
	lma "github.com/tigera/lma/pkg/elastic"

	bapi "github.com/projectcalico/calico/libcalico-go/lib/backend/api"
	client "github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	lclient "github.com/projectcalico/calico/licensing/client"
	"github.com/projectcalico/calico/licensing/client/features"
	"github.com/projectcalico/calico/licensing/monitor"
)

const (
	DefaultConfigMapNamespace             = "tigera-intrusion-detection"
	DefaultSecretsNamespace               = "tigera-intrusion-detection"
	DefaultMultiClusterForwardingEndpoint = "https://tigera-manager.tigera-manager.svc:9443"
	DefaultMultiClusterForwardingCA       = "/manager-tls/cert"
)

// backendClientAccessor is an interface to access the backend client from the main v2 client.
type backendClientAccessor interface {
	Backend() bapi.Client
}

func init() {
	ValidateEnvVars()
}

func main() {
	var ver, debug bool
	var healthzSockPort int

	flag.BoolVar(&ver, "version", false, "Print version information")
	flag.BoolVar(&debug, "debug", false, "Debug mode")
	flag.IntVar(&healthzSockPort, "port", health.DefaultHealthzSockPort, "Healthz port")
	// enable klog flags for API call logging (to stderr).
	klog.InitFlags(flag.CommandLine)
	flag.Parse()

	if ver {
		Version()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	kubeconfig := os.Getenv("KUBECONFIG")
	var config *rest.Config
	var err error
	if kubeconfig == "" {
		// creates the in-cluster config
		config, err = rest.InClusterConfig()
		if err != nil {
			panic(err.Error())
		}
	} else {
		// creates a config from supplied kubeconfig
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			panic(err.Error())
		}
	}
	k8sClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	calicoClient, err := calicoclient.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	// This allows us to use "calico-monitoring" in helm if we want to
	configMapNamespace := getStrEnvOrDefault("CONFIG_MAP_NAMESPACE", DefaultConfigMapNamespace)
	secretsNamespace := getStrEnvOrDefault("SECRETS_NAMESPACE", DefaultSecretsNamespace)

	envCfg := lma.MustLoadConfig()
	lmaESClient, err := lma.NewFromConfig(envCfg)
	if err != nil {
		log.WithError(err).Fatal("Could not connect to Elasticsearch")
	}
	if err := lmaESClient.CreateEventsIndex(ctx); err != nil {
		log.WithError(err).Fatal("Failed to create events index")
	}

	indexSettings := elastic.IndexSettings{Replicas: envCfg.ElasticReplicas, Shards: envCfg.ElasticShards}
	e := elastic.NewService(lmaESClient, indexSettings)
	e.Run(ctx)
	defer e.Close()

	clientCalico, err := client.NewFromEnv()
	if err != nil {
		log.WithError(err).Fatal("Failed to build calico client")
	}

	licenseMonitor := monitor.New(clientCalico.(backendClientAccessor).Backend())
	err = licenseMonitor.RefreshLicense(ctx)
	if err != nil {
		log.WithError(err).Error("Failed to get license from datastore; continuing without a license")
	}

	licenseChangedChan := make(chan struct{})

	// Define some of the callbacks for the license monitor. Any changes just send a signal back on the license changed channel.
	licenseMonitor.SetFeaturesChangedCallback(func() {
		licenseChangedChan <- struct{}{}
	})

	licenseMonitor.SetStatusChangedCallback(func(newLicenseStatus lclient.LicenseStatus) {
		licenseChangedChan <- struct{}{}
	})

	// Start the license monitor, which will trigger the callback above at start of day and then whenever the license
	// status changes.
	go func() {
		err := licenseMonitor.MonitorForever(context.Background())
		if err != nil {
			log.WithError(err).Warn("Error while continuously monitoring the license.")
		}
	}()

	gns := globalnetworksets.NewController(calicoClient.ProjectcalicoV3().GlobalNetworkSets())
	eip := syncElastic.NewIPSetController(e)
	edn := syncElastic.NewDomainNameSetController(e)
	sIP := events.NewSuspiciousIP(e)
	sDN := events.NewSuspiciousDomainNameSet(e)

	s := feedsWatcher.NewWatcher(
		k8sClient.CoreV1().ConfigMaps(configMapNamespace),
		rbac.RestrictedSecretsClient{k8sClient.CoreV1().Secrets(secretsNamespace)},
		calicoClient.ProjectcalicoV3().GlobalThreatFeeds(),
		gns,
		eip,
		edn,
		&http.Client{},
		e, e, sIP, sDN, e)
	valueEnableForwarding, err := strconv.ParseBool(os.Getenv("IDS_ENABLE_EVENT_FORWARDING"))
	var enableForwarding = (err == nil && valueEnableForwarding)
	var healthPingers health.Pingers
	var enableFeeds = (os.Getenv("DISABLE_FEEDS") != "yes")
	if enableFeeds {
		healthPingers = append(healthPingers, s)
	}

	var managementAlertController, managedClusterController controller.Controller
	var alertHealthPinger, managedClusterHealthPinger health.Pingers
	enableAlerts := os.Getenv("DISABLE_ALERTS") != "yes"
	if enableAlerts {
		managementAlertController, alertHealthPinger = alert.NewGlobalAlertController(calicoClient, lmaESClient, getStrEnvOrDefault("CLUSTER_NAME", "cluster"))
		healthPingers = append(healthPingers, &alertHealthPinger)

		multiClusterForwardingEndpoint := getStrEnvOrDefault("MULTI_CLUSTER_FORWARDING_ENDPOINT", DefaultMultiClusterForwardingEndpoint)
		multiClusterForwardingCA := getStrEnvOrDefault("MULTI_CLUSTER_FORWARDING_CA", DefaultMultiClusterForwardingCA)
		managedClusterController, managedClusterHealthPinger = managedcluster.NewManagedClusterController(calicoClient, lmaESClient, indexSettings,
			util.ManagedClusterClient(config, multiClusterForwardingEndpoint, multiClusterForwardingCA))
		healthPingers = append(healthPingers, &managedClusterHealthPinger)
	}

	f := forwarder.NewEventForwarder("eventforwarder-1", e)

	hs := health.NewServer(healthPingers, health.Readiers{health.AlwaysReady{}}, healthzSockPort)
	go func() {
		err := hs.Serve()
		if err != nil {
			log.WithError(err).Error("failed to start healthz server")
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	var runningControllers bool
	for {
		hasLicense := licenseMonitor.GetFeatureStatus(features.ThreatDefense)
		if hasLicense && !runningControllers {
			log.Info("Starting watchers and controllers for intrusion detection.")
			if enableFeeds {
				s.Run(ctx)
				defer s.Close()
			}

			if enableAlerts {
				managementAlertController.Run(ctx)
				defer managementAlertController.Close()
				managedClusterController.Run(ctx)
				defer managedClusterController.Close()
			}

			if enableForwarding {
				f.Run(ctx)
				defer f.Close()
			}

			runningControllers = true
		} else if !hasLicense && runningControllers {
			log.Info("License is no longer active/feature is disabled.")

			if enableFeeds {
				s.Close()
			}

			if enableAlerts {
				managedClusterController.Close()
				managementAlertController.Close()
			}

			if enableForwarding {
				f.Close()
			}

			runningControllers = false
		}

		select {
		case <-sig:
			log.Info("got signal; shutting down")
			err = hs.Close()
			if err != nil {
				log.WithError(err).Error("failed to stop healthz server")
			}
			return
		case <-licenseChangedChan:
			log.Info("License status has changed")
			continue
		}
	}
}

// getStrEnvOrDefault returns the environment variable named by the key if it is not empty, else returns the defaultValue
func getStrEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value != "" {
		return value
	}
	return defaultValue
}
