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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"k8s.io/klog/v2"

	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/feeds/sync"

	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/config"

	lsclient "github.com/projectcalico/calico/linseed/pkg/client"
	lsrest "github.com/projectcalico/calico/linseed/pkg/client/rest"

	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	calicoclient "github.com/tigera/api/pkg/client/clientset_generated/clientset"

	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/feeds/events"
	geo "github.com/projectcalico/calico/intrusion-detection-controller/pkg/feeds/geodb"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/feeds/rbac"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/feeds/sync/globalnetworksets"
	feedsWatcher "github.com/projectcalico/calico/intrusion-detection-controller/pkg/feeds/watcher"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/forwarder"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/globalalert/controllers/alert"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/globalalert/controllers/anomalydetection"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/globalalert/controllers/controller"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/globalalert/controllers/managedcluster"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/globalalert/controllers/waf"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/health"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/storage"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/util"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/version"
	bapi "github.com/projectcalico/calico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	lclient "github.com/projectcalico/calico/licensing/client"
	"github.com/projectcalico/calico/licensing/client/features"
	"github.com/projectcalico/calico/licensing/monitor"
)

const (
	TigeraIntrusionDetectionNamespace = "tigera-intrusion-detection"

	DefaultConfigMapNamespace = TigeraIntrusionDetectionNamespace
	DefaultSecretsNamespace   = TigeraIntrusionDetectionNamespace
	DefaultMaxLinseedTimeSkew = 1 // minute
)

// backendClientAccessor is an interface to access the backend client from the main v2 client.
type backendClientAccessor interface {
	Backend() bapi.Client
}

func main() {
	var ver, debug bool
	var healthzSockPort, maxLinseedTimeSkew int

	flag.BoolVar(&ver, "version", false, "Print version information")
	flag.BoolVar(&debug, "debug", false, "Debug mode")
	flag.IntVar(&healthzSockPort, "port", health.DefaultHealthzSockPort, "Healthz port")
	flag.IntVar(&maxLinseedTimeSkew, "maxLinseedTimeSkew", DefaultMaxLinseedTimeSkew, "Max time for time skew with linseed")
	// enable klog flags for API call logging (to stderr).
	klog.InitFlags(flag.CommandLine)
	flag.Parse()

	if ver {
		version.Version()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	kubeconfig := os.Getenv("KUBECONFIG")
	var k8sConfig *rest.Config
	var err error
	if kubeconfig == "" {
		// creates the in-cluster k8sConfig
		k8sConfig, err = rest.InClusterConfig()
		if err != nil {
			panic(err.Error())
		}
	} else {
		// creates a k8sConfig from supplied kubeconfig
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			panic(err.Error())
		}
	}
	kubeClientSet, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		log.WithError(err).Fatal("Failed to create kubernetes client set")
	}
	calicoClientSet, err := calicoclient.NewForConfig(k8sConfig)
	if err != nil {
		log.WithError(err).Fatal("Failed to create calico client set")
	}

	scheme := runtime.NewScheme()
	if err = v3.AddToScheme(scheme); err != nil {
		log.WithError(err).Fatal("Failed to configure controller runtime client")
	}
	if err = corev1.AddToScheme(scheme); err != nil {
		log.WithError(err).Fatal("Failed to configure controller runtime client")
	}

	client, err := ctrlclient.NewWithWatch(k8sConfig, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		log.WithError(err).Fatal("Failed to configure controller runtime client with watch")
	}

	// This allows us to use "calico-monitoring" in helm if we want to
	configMapNamespace := getStrEnvOrDefault("CONFIG_MAP_NAMESPACE", DefaultConfigMapNamespace)
	secretsNamespace := getStrEnvOrDefault("SECRETS_NAMESPACE", DefaultSecretsNamespace)

	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatal(err)
	}

	// Create linseed Client.
	lsConfig := lsrest.Config{
		URL:            cfg.LinseedURL,
		CACertPath:     cfg.LinseedCA,
		ClientKeyPath:  cfg.LinseedClientKey,
		ClientCertPath: cfg.LinseedClientCert,
	}
	linseedClient, err := lsclient.NewClient(cfg.TenantID, lsConfig, lsrest.WithTokenPath(cfg.LinseedToken))
	if err != nil {
		log.WithError(err).Fatal("failed to create linseed client")
	}

	e := storage.NewService(linseedClient, client, "")
	e.Run(ctx)
	defer e.Close()

	clientCalico, err := clientv3.NewFromEnv()
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

	gns := globalnetworksets.NewController(calicoClientSet.ProjectcalicoV3().GlobalNetworkSets())
	eip := sync.NewIPSetController(e)
	edn := sync.NewDomainNameSetController(e)
	sIP := events.NewSuspiciousIP(e)
	sDN := events.NewSuspiciousDomainNameSet(e)
	g, err := geo.NewGeoDB()
	if err != nil {
		log.WithError(err).Error("Error while opening Geo IP database.")
	}
	defer g.Close()

	maxLinseedTimeSkewiDuration := time.Duration(maxLinseedTimeSkew * int(time.Minute))

	s := feedsWatcher.NewWatcher(
		kubeClientSet.CoreV1().ConfigMaps(configMapNamespace),
		rbac.RestrictedSecretsClient{
			Client: kubeClientSet.CoreV1().Secrets(secretsNamespace),
		},
		calicoClientSet.ProjectcalicoV3().GlobalThreatFeeds(),
		gns,
		eip,
		edn,
		&http.Client{},
		e, e, sIP, sDN, e, g, maxLinseedTimeSkewiDuration)

	valueEnableForwarding, err := strconv.ParseBool(os.Getenv("IDS_ENABLE_EVENT_FORWARDING"))

	enableForwarding := (err == nil && valueEnableForwarding)
	var healthPingers health.Pingers

	enableFeeds := (os.Getenv("DISABLE_FEEDS") != "yes")
	if enableFeeds {
		healthPingers = append(healthPingers, s)
	}

	var managementAlertController, managedClusterController, wafEventController controller.Controller
	var alertHealthPinger health.Pingers

	enableAlerts := os.Getenv("DISABLE_ALERTS") != "yes"

	// anomaly detection cleanup controllers
	var anomalyTrainingController, anomalyDetectionController controller.Controller

	if enableAlerts {

		// Initialize controllers to clean up cron jobs for anomaly detection
		anomalyTrainingController = anomalydetection.NewADJobTrainingController(kubeClientSet, TigeraIntrusionDetectionNamespace)
		anomalyDetectionController = anomalydetection.NewADJobDetectionController(kubeClientSet, TigeraIntrusionDetectionNamespace)

		// This will manage global alerts inside the management cluster
		managementAlertController, alertHealthPinger = alert.NewGlobalAlertController(calicoClientSet, linseedClient, kubeClientSet, "cluster", cfg.TenantID, TigeraIntrusionDetectionNamespace, cfg.TenantNamespace)
		healthPingers = append(healthPingers, &alertHealthPinger)

		// This will manage all waf logs inside the management cluster
		wafEventController = waf.NewWafAlertController(linseedClient, "cluster", cfg.TenantID, TigeraIntrusionDetectionNamespace)

		// This controller will monitor managed cluster updated from K8S and create a NewGlobalAlertController per managed cluster
		managedClusterController = managedcluster.NewManagedClusterController(calicoClientSet, linseedClient, kubeClientSet, client, TigeraIntrusionDetectionNamespace, util.ManagedClusterClient(k8sConfig, cfg.MultiClusterForwardingEndpoint, cfg.MultiClusterForwardingCA), cfg.TenantID, cfg.TenantNamespace)
	}

	f := forwarder.NewEventForwarder(e)

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
				anomalyTrainingController.Run(ctx)
				defer anomalyTrainingController.Close()
				anomalyDetectionController.Run(ctx)
				defer anomalyDetectionController.Close()

				managedClusterController.Run(ctx)
				defer managedClusterController.Close()
				managementAlertController.Run(ctx)
				defer managementAlertController.Close()

				wafEventController.Run(ctx)
				defer wafEventController.Close()
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
				anomalyTrainingController.Close()
				anomalyDetectionController.Close()

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
