// Copyright (c) 2017-2021 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/projectcalico/kube-controllers/pkg/elasticsearch"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/projectcalico/kube-controllers/pkg/controllers/authorization"
	"github.com/projectcalico/kube-controllers/pkg/controllers/elasticsearchconfiguration"

	log "github.com/sirupsen/logrus"
	"go.etcd.io/etcd/clientv3"
	"go.etcd.io/etcd/pkg/srv"
	"go.etcd.io/etcd/pkg/transport"
	"k8s.io/apiserver/pkg/storage/etcd3"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	"github.com/projectcalico/libcalico-go/lib/apiconfig"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/logutils"

	"github.com/projectcalico/kube-controllers/pkg/config"
	"github.com/projectcalico/kube-controllers/pkg/controllers/controller"
	"github.com/projectcalico/kube-controllers/pkg/controllers/federatedservices"
	"github.com/projectcalico/kube-controllers/pkg/controllers/flannelmigration"
	"github.com/projectcalico/kube-controllers/pkg/controllers/managedcluster"
	"github.com/projectcalico/kube-controllers/pkg/controllers/namespace"
	"github.com/projectcalico/kube-controllers/pkg/controllers/networkpolicy"
	"github.com/projectcalico/kube-controllers/pkg/controllers/node"
	"github.com/projectcalico/kube-controllers/pkg/controllers/pod"
	"github.com/projectcalico/kube-controllers/pkg/controllers/service"
	"github.com/projectcalico/kube-controllers/pkg/controllers/serviceaccount"
	relasticsearch "github.com/projectcalico/kube-controllers/pkg/resource/elasticsearch"

	tigeraapi "github.com/tigera/api/pkg/client/clientset_generated/clientset"
	lclient "github.com/tigera/licensing/client"
	"github.com/tigera/licensing/client/features"
	"github.com/tigera/licensing/monitor"

	"github.com/projectcalico/kube-controllers/pkg/status"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/k8s"
	"github.com/projectcalico/typha/pkg/cmdwrapper"
)

// backendClientAccessor is an interface to access the backend client from the main v2 client.
type backendClientAccessor interface {
	Backend() bapi.Client
}

// addHeaderRoundTripper implements the http.RoundTripper interface and inserts the headers in headers field
// into the request made with an http.Client that uses this RoundTripper
type addHeaderRoundTripper struct {
	headers map[string][]string
	rt      http.RoundTripper
}

func (ha *addHeaderRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := new(http.Request)
	*r2 = *r

	// To set extra headers, we must make a copy of the Request so
	// that we don't modify the Request we were given. This is required by the
	// specification of http.RoundTripper.
	//
	// Since we are going to modify only req.Header here, we only need a deep copy
	// of req.Header.
	r2.Header = make(http.Header, len(r.Header))
	for k, s := range r.Header {
		r2.Header[k] = append([]string(nil), s...)
	}

	for key, values := range ha.headers {
		r2.Header[key] = values
	}

	return ha.rt.RoundTrip(r2)
}

// VERSION is filled out during the build process (using git describe output)
var VERSION string
var version bool
var statusFile string

func init() {
	// Add a flag to check the version.
	flag.BoolVar(&version, "version", false, "Display version")
	flag.StringVar(&statusFile, "status-file", status.DefaultStatusFile, "File to write status information to")

	// Tell klog to log into STDERR. Otherwise, we risk
	// certain kinds of API errors getting logged into a directory not
	// available in a `FROM scratch` Docker container, causing us to abort
	var flags flag.FlagSet
	klog.InitFlags(&flags)
	err := flags.Set("logtostderr", "true")
	if err != nil {
		log.WithError(err).Fatal("Failed to set klog logging configuration")
	}
	ValidateEnvVars()
}

func main() {
	flag.Parse()
	if version {
		fmt.Println(VERSION)
		os.Exit(0)
	}

	// Configure log formatting.
	log.SetFormatter(&logutils.Formatter{})

	// Install a hook that adds file/line no information.
	log.AddHook(&logutils.ContextHook{})

	// Attempt to load configuration.
	cfg := new(config.Config)
	if err := cfg.Parse(); err != nil {
		log.WithError(err).Fatal("Failed to parse config")
	}
	log.WithField("config", cfg).Info("Loaded configuration from environment")

	// Set the log level based on the loaded configuration.
	logLevel, err := log.ParseLevel(cfg.LogLevel)
	if err != nil {
		log.WithError(err).Warnf("error parsing logLevel: %v", cfg.LogLevel)
		logLevel = log.InfoLevel
	}
	log.SetLevel(logLevel)

	// Build clients to be used by the controllers.
	k8sClientset, calicoClient, err := getClients(cfg.Kubeconfig)
	if err != nil {
		log.WithError(err).Fatal("Failed to start")
	}

	esURL := fmt.Sprintf("https://%s:%s", cfg.ElasticHost, cfg.ElasticPort)
	esClientBuilder := elasticsearch.NewClientBuilder(esURL, cfg.ElasticUsername, cfg.ElasticPassword, cfg.ElasticCA)

	stop := make(chan struct{})

	// Create the context.
	ctx, cancel := context.WithCancel(context.Background())

	if !cfg.DoNotInitializeCalico {
		log.Info("Ensuring Calico datastore is initialized")
		initCtx, cancelInit := context.WithTimeout(ctx, 10*time.Second)
		defer cancelInit()
		err = calicoClient.EnsureInitialized(initCtx, "", "", "k8s")
		if err != nil {
			log.WithError(err).Fatal("Failed to initialize Calico datastore")
		}
	}

	controllerCtrl := &controllerControl{
		ctx:              ctx,
		controllerStates: make(map[string]*controllerState),
		stop:             stop,
		licenseMonitor:   monitor.New(calicoClient.(backendClientAccessor).Backend()),
		restartCntrlChan: make(chan string),
		informers:        make([]cache.SharedIndexInformer, 0),
	}

	var runCfg config.RunConfig
	// flannelmigration doesn't use the datastore config API
	v, ok := os.LookupEnv(config.EnvEnabledControllers)
	if ok && strings.Contains(v, "flannelmigration") {
		if strings.Trim(v, " ,") != "flannelmigration" {
			log.WithField(config.EnvEnabledControllers, v).Fatal("flannelmigration must be the only controller running")
		}
		// Attempt to load Flannel configuration.
		flannelConfig := new(flannelmigration.Config)
		if err := flannelConfig.Parse(); err != nil {
			log.WithError(err).Fatal("Failed to parse Flannel config")
		}
		log.WithField("flannelConfig", flannelConfig).Info("Loaded Flannel configuration from environment")

		flannelMigrationController := flannelmigration.NewFlannelMigrationController(ctx, k8sClientset, calicoClient, flannelConfig)
		controllerCtrl.controllerStates["FlannelMigration"] = &controllerState{controller: flannelMigrationController}

		// Set some global defaults for flannelmigration
		// Note that we now ignore the HEALTH_ENABLED environment variable in the case of flannel migration
		runCfg.HealthEnabled = true
		runCfg.LogLevelScreen = logLevel

		// this channel will never receive, and thus flannelmigration will never
		// restart due to a config change.
		controllerCtrl.restartCfgChan = make(chan config.RunConfig)
	} else {
		log.Info("Getting initial config snapshot from datastore")
		cCtrlr := config.NewRunConfigController(ctx, *cfg, calicoClient.KubeControllersConfiguration())
		runCfg = <-cCtrlr.ConfigChan()
		log.Info("Got initial config snapshot")
		log.Debugf("Initial config: %+v", runCfg)

		// any subsequent changes trigger a restart
		controllerCtrl.restartCfgChan = cCtrlr.ConfigChan()
		controllerCtrl.InitControllers(ctx, runCfg, k8sClientset, calicoClient, esClientBuilder)
	}

	// Create the status file. We will only update it if we have healthchecks enabled.
	s := status.New(statusFile)

	if cfg.DatastoreType == "etcdv3" {
		// If configured to do so, start an etcdv3 compaction.
		go startCompactor(ctx, runCfg.EtcdV3CompactionPeriod)
	}

	// Run the health checks on a separate goroutine.
	if runCfg.HealthEnabled {
		log.Info("Starting status report routine")
		go runHealthChecks(ctx, s, k8sClientset, calicoClient)
	}

	// Set the log level from the merged config.
	log.SetLevel(runCfg.LogLevelScreen)

	if runCfg.PrometheusPort != 0 {
		// Serve prometheus metrics.
		log.Infof("Starting Prometheus metrics server on port %d", runCfg.PrometheusPort)
		go func() {
			http.Handle("/metrics", promhttp.Handler())
			err := http.ListenAndServe(fmt.Sprintf(":%d", runCfg.PrometheusPort), nil)
			if err != nil {
				log.WithError(err).Fatal("Failed to serve prometheus metrics")
			}
		}()
	}

	// Run the controllers. This runs until a config change triggers a restart
	// or a license change triggers a restart.
	controllerCtrl.RunControllers()

	// Shut down compaction, healthChecks, and configController
	cancel()

	// TODO: it would be nice here to wait until everything shuts down cleanly
	//       but many of our controllers are based on cache.ResourceCache which
	//       runs forever once it is started.  It needs to be enhanced to respect
	//       the stop channel passed to the controllers.
	os.Exit(cmdwrapper.RestartReturnCode)
}

// Run the controller health checks.
func runHealthChecks(ctx context.Context, s *status.Status, k8sClientset *kubernetes.Clientset, calicoClient client.Interface) {
	s.SetReady("CalicoDatastore", false, "initialized to false")
	s.SetReady("KubeAPIServer", false, "initialized to false")

	// Loop until context expires and perform healthchecks.
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// carry on
		}

		// skip healthchecks if configured
		// Datastore HealthCheck
		healthCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := calicoClient.EnsureInitialized(healthCtx, "", "", "k8s")
		if err != nil {
			log.WithError(err).Errorf("Failed to verify datastore")
			s.SetReady(
				"CalicoDatastore",
				false,
				fmt.Sprintf("Error verifying datastore: %v", err),
			)
		} else {
			s.SetReady("CalicoDatastore", true, "")
		}
		cancel()

		// Kube-apiserver HealthCheck
		healthStatus := 0
		k8sCheckDone := make(chan interface{}, 1)
		go func(k8sCheckDone <-chan interface{}) {
			time.Sleep(2 * time.Second)
			select {
			case <-k8sCheckDone:
				// The check has completed.
			default:
				// Check is still running, so report not ready.
				s.SetReady(
					"KubeAPIServer",
					false,
					"Error reaching apiserver: taking a long time to check apiserver",
				)
			}
		}(k8sCheckDone)
		k8sClientset.Discovery().RESTClient().Get().AbsPath("/healthz").Do(ctx).StatusCode(&healthStatus)
		k8sCheckDone <- nil
		if healthStatus != http.StatusOK {
			log.WithError(err).Errorf("Failed to reach apiserver")
			s.SetReady(
				"KubeAPIServer",
				false,
				fmt.Sprintf("Error reaching apiserver: %v with http status code: %d", err, healthStatus),
			)
		} else {
			s.SetReady("KubeAPIServer", true, "")
		}

		time.Sleep(10 * time.Second)
	}
}

// Starts an etcdv3 compaction goroutine with the given config.
func startCompactor(ctx context.Context, interval time.Duration) {

	if interval.Nanoseconds() == 0 {
		log.Info("Disabling periodic etcdv3 compaction")
		return
	}

	// Kick off a periodic compaction of etcd, retry until success.
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// carry on
		}
		etcdClient, err := newEtcdV3Client()
		if err != nil {
			log.WithError(err).Error("Failed to start etcd compaction routine, retry in 1m")
			time.Sleep(1 * time.Minute)
			continue
		}

		log.WithField("period", interval).Info("Starting periodic etcdv3 compaction")
		etcd3.StartCompactor(ctx, etcdClient, interval)
		break
	}
}

// getClients builds and returns Kubernetes and Calico clients.
func getClients(kubeconfig string) (*kubernetes.Clientset, client.Interface, error) {
	// Get Calico client
	calicoClient, err := client.NewFromEnv()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build Calico client: %s", err)
	}

	// If the calico client is actually a kubernetes backed client, just return the kubernetes
	// clientset that is in that client. This is minor finesse that is only useful for controllers
	// that may be run on clusters using Kubernetes API for the Calico datastore.
	beca := calicoClient.(backendClientAccessor)
	bec := beca.Backend()
	if kc, ok := bec.(*k8s.KubeClient); ok {
		return kc.ClientSet, calicoClient, err
	}

	// Now build the Kubernetes client, we support in-cluster config and kubeconfig
	// as means of configuring the client.
	k8sconfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build kubernetes client config: %s", err)
	}

	// Get Kubernetes clientset
	k8sClientset, err := kubernetes.NewForConfig(k8sconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build kubernetes client: %s", err)
	}

	return k8sClientset, calicoClient, nil
}

// Returns an etcdv3 client based on the environment. The client will be configured to
// match that in use by the libcalico-go client.
func newEtcdV3Client() (*clientv3.Client, error) {
	config, err := apiconfig.LoadClientConfigFromEnvironment()
	if err != nil {
		return nil, err
	}

	if config.Spec.EtcdEndpoints != "" && config.Spec.EtcdDiscoverySrv != "" {
		log.Warning("Multiple etcd endpoint discovery methods specified in etcdv3 API config")
		return nil, fmt.Errorf("multiple discovery or bootstrap options specified, use either \"etcdEndpoints\" or \"etcdDiscoverySrv\"")
	}

	// Split the endpoints into a location slice.
	etcdLocation := []string{}
	if config.Spec.EtcdEndpoints != "" {
		etcdLocation = strings.Split(config.Spec.EtcdEndpoints, ",")
	}

	if config.Spec.EtcdDiscoverySrv != "" {
		srvs, srvErr := srv.GetClient("etcd-client", config.Spec.EtcdDiscoverySrv, "")
		if srvErr != nil {
			return nil, fmt.Errorf("failed to discover etcd endpoints through SRV discovery: %v", srvErr)
		}
		etcdLocation = srvs.Endpoints
	}

	if len(etcdLocation) == 0 {
		log.Warning("No etcd endpoints specified in etcdv3 API config")
		return nil, fmt.Errorf("no etcd endpoints specified")
	}

	// Create the etcd client
	tlsInfo := &transport.TLSInfo{
		TrustedCAFile: config.Spec.EtcdCACertFile,
		CertFile:      config.Spec.EtcdCertFile,
		KeyFile:       config.Spec.EtcdKeyFile,
	}

	tlsClient, err := tlsInfo.ClientConfig()
	if err != nil {
		return nil, err
	}

	// go 1.13 defaults to TLS 1.3, which we don't support just yet
	tlsClient.MaxVersion = tls.VersionTLS13

	cfg := clientv3.Config{
		Endpoints:   etcdLocation,
		TLS:         tlsClient,
		DialTimeout: 10 * time.Second,
	}

	// Plumb through the username and password if both are configured.
	if config.Spec.EtcdUsername != "" && config.Spec.EtcdPassword != "" {
		cfg.Username = config.Spec.EtcdUsername
		cfg.Password = config.Spec.EtcdPassword
	}

	return clientv3.New(cfg)
}

// Object for keeping track of controller states and statuses.
type controllerControl struct {
	ctx                   context.Context
	controllerStates      map[string]*controllerState
	stop                  chan struct{}
	restartCfgChan        <-chan config.RunConfig
	restartCntrlChan      chan string
	licenseMonitor        monitor.LicenseMonitor
	needLicenseMonitoring bool
	shortLicensePolling   bool
	informers             []cache.SharedIndexInformer
}

// Object for keeping track of Controller information.
type controllerState struct {
	controller     controller.Controller
	running        bool
	licenseFeature string
}

func (cc *controllerControl) InitControllers(ctx context.Context, cfg config.RunConfig,
	k8sClientset *kubernetes.Clientset, calicoClient client.Interface, esClientBuilder elasticsearch.ClientBuilder) {
	cc.shortLicensePolling = cfg.ShortLicensePolling

	// Create a shared informer factory to allow cache sharing between controllers monitoring the
	// same resource.
	factory := informers.NewSharedInformerFactory(k8sClientset, 0)
	podInformer := factory.Core().V1().Pods().Informer()
	nodeInformer := factory.Core().V1().Nodes().Informer()

	if cfg.Controllers.WorkloadEndpoint != nil {
		podController := pod.NewPodController(ctx, k8sClientset, calicoClient, *cfg.Controllers.WorkloadEndpoint, podInformer)
		cc.controllerStates["Pod"] = &controllerState{controller: podController}
		cc.registerInformers(podInformer)
	}

	if cfg.Controllers.Namespace != nil {
		namespaceController := namespace.NewNamespaceController(ctx, k8sClientset, calicoClient, *cfg.Controllers.Namespace)
		cc.controllerStates["Namespace"] = &controllerState{controller: namespaceController}
	}
	if cfg.Controllers.Policy != nil {
		policyController := networkpolicy.NewPolicyController(ctx, k8sClientset, calicoClient, *cfg.Controllers.Policy)
		cc.controllerStates["NetworkPolicy"] = &controllerState{controller: policyController}
	}
	if cfg.Controllers.Node != nil {
		nodeController := node.NewNodeController(ctx, k8sClientset, calicoClient, *cfg.Controllers.Node, nodeInformer, podInformer)
		cc.controllerStates["Node"] = &controllerState{controller: nodeController}
		cc.registerInformers(podInformer, nodeInformer)
	}
	if cfg.Controllers.ServiceAccount != nil {
		serviceAccountController := serviceaccount.NewServiceAccountController(ctx, k8sClientset, calicoClient, *cfg.Controllers.ServiceAccount)
		cc.controllerStates["ServiceAccount"] = &controllerState{controller: serviceAccountController}
	}

	// Calico Enterprise controllers:
	if cfg.Controllers.Service != nil {
		log.Warning("The Service controller is deprecated and will be removed in a future release. Please use the 'services' match field in network policy rules instead")
		serviceController := service.NewServiceController(ctx, k8sClientset, calicoClient, *cfg.Controllers.Service)
		cc.controllerStates["Service"] = &controllerState{controller: serviceController}
	}
	if cfg.Controllers.FederatedServices != nil {
		federatedEndpointsController := federatedservices.NewFederatedServicesController(ctx, k8sClientset, calicoClient, *cfg.Controllers.FederatedServices, cc.restartCntrlChan)
		cc.controllerStates["FederatedServices"] = &controllerState{
			controller:     federatedEndpointsController,
			licenseFeature: features.FederatedServices,
		}
		cc.needLicenseMonitoring = true
	}
	if cfg.Controllers.ElasticsearchConfiguration != nil {
		esK8sREST, err := relasticsearch.NewRESTClient(cfg.Controllers.ElasticsearchConfiguration.RESTConfig)
		if err != nil {
			log.WithError(err).Fatal("failed to build elasticsearch rest client")
		}

		cc.controllerStates["ElasticsearchConfiguration"] = &controllerState{
			controller: elasticsearchconfiguration.New(
				"cluster",
				"",
				k8sClientset,
				k8sClientset,
				esK8sREST,
				esClientBuilder,
				true,
				*cfg.Controllers.ElasticsearchConfiguration),
		}
	}
	if cfg.Controllers.ManagedCluster != nil {
		// We only want these clients created if the managedcluster controller type is enabled
		kubeconfig := cfg.Controllers.ManagedCluster.RESTConfig

		esK8sREST, err := relasticsearch.NewRESTClient(kubeconfig)
		if err != nil {
			log.WithError(err).Fatal("failed to build elasticsearch rest client")
		}

		calicoV3Client, err := tigeraapi.NewForConfig(kubeconfig)
		if err != nil {
			log.WithError(err).Fatal("failed to build calico v3 clientset")
		}

		cc.controllerStates["ManagedCluster"] = &controllerState{
			controller: managedcluster.New(
				func(clustername string) (kubernetes.Interface, *tigeraapi.Clientset, error) {
					kubeconfig.Host = cfg.Controllers.ManagedCluster.MultiClusterForwardingEndpoint
					kubeconfig.CAFile = cfg.Controllers.ManagedCluster.MultiClusterForwardingCA
					kubeconfig.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
						return &addHeaderRoundTripper{
							headers: map[string][]string{"x-cluster-id": {clustername}},
							rt:      rt,
						}
					}
					k8sCLI, err := kubernetes.NewForConfig(kubeconfig)
					if err != nil {
						return k8sCLI, nil, err
					}

					calicoCLI, err := tigeraapi.NewForConfig(kubeconfig)
					if err != nil {
						return k8sCLI, calicoCLI, err
					}

					return k8sCLI, calicoCLI, nil

				},
				k8sClientset,
				calicoV3Client,
				esK8sREST,
				esClientBuilder,
				*cfg.Controllers.ManagedCluster),
			licenseFeature: features.MultiClusterManagement,
		}
		cc.needLicenseMonitoring = true
	}

	if cfg.Controllers.AuthorizationConfiguration != nil {
		cc.controllerStates["Authorization"] = &controllerState{
			controller: authorization.New(
				k8sClientset,
				esClientBuilder,
				cfg.Controllers.AuthorizationConfiguration,
			),
		}
	}
}

// registerInformers registers the given informers, if not already registered. Registered informers
// will be started in RunControllers().
func (cc *controllerControl) registerInformers(infs ...cache.SharedIndexInformer) {
	for _, inf := range infs {
		alreadyRegistered := false
		for _, registeredInf := range cc.informers {
			if inf == registeredInf {
				alreadyRegistered = true
			}
		}

		if !alreadyRegistered {
			cc.informers = append(cc.informers, inf)
		}
	}
}

// Runs all the controllers and blocks until we get a restart.
func (cc *controllerControl) RunControllers() {
	// Instantiate the license monitor values
	lCtx, cancel := context.WithTimeout(cc.ctx, 10*time.Second)
	err := cc.licenseMonitor.RefreshLicense(lCtx)
	cancel()
	if err != nil {
		log.WithError(err).Error("Failed to get license from datastore; continuing without a license")
	}

	if cc.shortLicensePolling {
		log.Info("Using short license poll interval for FV")
		cc.licenseMonitor.SetPollInterval(1 * time.Second)
	}

	licenseChangedChan := make(chan struct{})

	// Define some of the callbacks for the license monitor. Any changes just send a signal back on the license changed channel.
	cc.licenseMonitor.SetFeaturesChangedCallback(func() {
		licenseChangedChan <- struct{}{}
	})

	cc.licenseMonitor.SetStatusChangedCallback(func(newLicenseStatus lclient.LicenseStatus) {
		licenseChangedChan <- struct{}{}
	})

	if cc.needLicenseMonitoring {
		// Start the license monitor, which will trigger the callback above at start of day and then whenever the license
		// status changes.
		// Need to wrap the call to MonitorForever in a function to pass static-checks.
		go func() {
			for {
				err := cc.licenseMonitor.MonitorForever(context.Background())
				if err != nil {
					log.WithError(err).Warn("Error while continuously monitoring the license.")
				}
				time.Sleep(time.Second)
			}
		}()
	}

	// Start any registered informers.
	for _, inf := range cc.informers {
		log.WithField("informer", inf).Info("Starting informer")
		go inf.Run(cc.stop)
	}

	for {
		for controllerType, cs := range cc.controllerStates {
			missingLicense := cs.licenseFeature != "" && !cc.licenseMonitor.GetFeatureStatus(cs.licenseFeature)
			if !cs.running && !missingLicense {
				// Run the controller
				log.Infof("Started the %s controller", controllerType)
				go cs.controller.Run(cc.stop)
				cs.running = true
			} else if cs.running && missingLicense {
				// Restart the controller since the updated license has less functionality than before.
				log.Warn("License was changed, shutting down the controllers")
				close(cc.stop)
				return
			}
		}

		// Block until we are cancelled, get new license info, or get a new configuration
		select {
		case <-cc.ctx.Done():
			log.Warn("context cancelled")
			close(cc.stop)
			return
		case msg := <-cc.restartCntrlChan:
			log.Warnf("controller requested restart: %s", msg)
			// Sleep for a bit to make sure we don't have a tight restart loop
			time.Sleep(3 * time.Second)
			close(cc.stop)
			return
		case <-cc.restartCfgChan:
			log.Warn("configuration changed; restarting")
			// Sleep for a bit to make sure we don't have a tight restart loop
			time.Sleep(3 * time.Second)
			// TODO: handle this more gracefully, like tearing down old controllers and starting new ones
			close(cc.stop)
			return
		case <-licenseChangedChan:
			log.Info("license status has changed")
			// go back to top of loop to compute if the license change requires a restart
			continue
		}
	}
}
