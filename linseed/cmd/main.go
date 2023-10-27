// Copyright (c) 2023 Tigera, Inc. All rights reserved.

package main

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/index"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/authentication/serviceaccount"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"
	rest "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/calico/kube-controllers/pkg/resource"
	"github.com/projectcalico/calico/libcalico-go/lib/health"
	"github.com/projectcalico/calico/linseed/pkg/backend"
	"github.com/projectcalico/calico/linseed/pkg/config"
	"github.com/projectcalico/calico/linseed/pkg/controller/token"
	"github.com/projectcalico/calico/linseed/pkg/handler"
	"github.com/projectcalico/calico/linseed/pkg/handler/audit"
	"github.com/projectcalico/calico/linseed/pkg/handler/bgp"
	"github.com/projectcalico/calico/linseed/pkg/handler/compliance"
	"github.com/projectcalico/calico/linseed/pkg/handler/dns"
	"github.com/projectcalico/calico/linseed/pkg/handler/events"
	"github.com/projectcalico/calico/linseed/pkg/handler/l3"
	"github.com/projectcalico/calico/linseed/pkg/handler/l7"
	"github.com/projectcalico/calico/linseed/pkg/handler/processes"
	"github.com/projectcalico/calico/linseed/pkg/handler/runtime"
	"github.com/projectcalico/calico/linseed/pkg/handler/threatfeeds"
	"github.com/projectcalico/calico/linseed/pkg/handler/waf"
	"github.com/projectcalico/calico/linseed/pkg/middleware"
	"github.com/projectcalico/calico/linseed/pkg/server"
	"github.com/projectcalico/calico/lma/pkg/auth"
	"github.com/projectcalico/calico/lma/pkg/k8s"

	"github.com/projectcalico/calico/linseed/pkg/backend/api"
	auditbackend "github.com/projectcalico/calico/linseed/pkg/backend/legacy/audit"
	bgpbackend "github.com/projectcalico/calico/linseed/pkg/backend/legacy/bgp"
	compliancebackend "github.com/projectcalico/calico/linseed/pkg/backend/legacy/compliance"
	dnsbackend "github.com/projectcalico/calico/linseed/pkg/backend/legacy/dns"
	eventbackend "github.com/projectcalico/calico/linseed/pkg/backend/legacy/events"
	flowbackend "github.com/projectcalico/calico/linseed/pkg/backend/legacy/flows"
	l7backend "github.com/projectcalico/calico/linseed/pkg/backend/legacy/l7"
	procbackend "github.com/projectcalico/calico/linseed/pkg/backend/legacy/processes"
	runtimebackend "github.com/projectcalico/calico/linseed/pkg/backend/legacy/runtime"
	"github.com/projectcalico/calico/linseed/pkg/backend/legacy/templates"
	threatfeedsbackend "github.com/projectcalico/calico/linseed/pkg/backend/legacy/threatfeeds"
	wafbackend "github.com/projectcalico/calico/linseed/pkg/backend/legacy/waf"
)

var (
	ready                   bool
	live                    bool
	configureElasticIndices bool
)

func init() {
	flag.BoolVar(&ready, "ready", false, "Set to get readiness information")
	flag.BoolVar(&live, "live", false, "Set to get liveness information")
	flag.BoolVar(&configureElasticIndices, "configure-elastic-indices", false, "Configure Elastic indices")
}

func main() {
	flag.Parse()

	// Read and reconcile configuration
	cfg := config.Config{}
	if err := envconfig.Process(config.EnvConfigPrefix, &cfg); err != nil {
		panic(err)
	}

	if ready {
		doHealthCheck("readiness", cfg.HealthPort)
	} else if live {
		doHealthCheck("liveness", cfg.HealthPort)
	} else if configureElasticIndices {
		boostrapElasticIndices()
	} else {
		// Just run the server.
		run()
	}
}

func run() {
	// Read and reconcile configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		panic(err)
	}

	// Configure logging
	config.ConfigureLogging(cfg.LogLevel)
	logrus.Debugf("Starting with %#v", cfg)

	// Register for termination signals
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	// Create a health aggregator and mark us as alive.
	// For now, we don't do periodic updates to our health, so don't set a timeout.
	const healthName = "Startup"
	healthAggregator := health.NewHealthAggregator()
	healthAggregator.RegisterReporter(healthName, &health.HealthReport{Live: true}, 0)

	esClient := backend.MustGetElasticClient(*cfg)

	var auditInitializer api.IndexInitializer
	var bgpInitializer api.IndexInitializer
	var defaultInitializer api.IndexInitializer
	var dnsInitializer api.IndexInitializer
	var flowInitializer api.IndexInitializer
	var l7Initializer api.IndexInitializer

	if cfg.Backend == config.BackendTypeSingleIndex && cfg.SingleIndexIndicesCreationEnabled {
		createSingleIndexIndices(cfg, esClient)
	} else if cfg.Backend == config.BackendTypeMultiIndex {
		// Create template caches for indices with special shards / replicas configuration
		defaultInitializer = templates.NewCachedInitializer(esClient, cfg.ElasticShards, cfg.ElasticReplicas)
		flowInitializer = templates.NewCachedInitializer(esClient, cfg.ElasticFlowShards, cfg.ElasticFlowReplicas)
		dnsInitializer = templates.NewCachedInitializer(esClient, cfg.ElasticDNSShards, cfg.ElasticDNSReplicas)
		l7Initializer = templates.NewCachedInitializer(esClient, cfg.ElasticL7Shards, cfg.ElasticL7Replicas)
		auditInitializer = templates.NewCachedInitializer(esClient, cfg.ElasticAuditShards, cfg.ElasticAuditReplicas)
		bgpInitializer = templates.NewCachedInitializer(esClient, cfg.ElasticBGPShards, cfg.ElasticBGPReplicas)
	} else {
		// Create a no op initializer
		defaultInitializer = templates.NewNoOpInitializer()
		flowInitializer = templates.NewNoOpInitializer()
		dnsInitializer = templates.NewNoOpInitializer()
		l7Initializer = templates.NewNoOpInitializer()
		auditInitializer = templates.NewNoOpInitializer()
		bgpInitializer = templates.NewNoOpInitializer()
	}

	// Create all the necessary backends.
	var flowLogBackend api.FlowLogBackend
	var flowBackend api.FlowBackend
	var auditBackend api.AuditBackend
	var bgpBackend api.BGPBackend
	var reportsBackend api.ReportsBackend
	var snapshotsBackend api.SnapshotsBackend
	var benchmarksBackend api.BenchmarksBackend
	var dnsFlowBackend api.DNSFlowBackend
	var dnsLogBackend api.DNSLogBackend
	var l7FlowBackend api.L7FlowBackend
	var l7LogBackend api.L7LogBackend
	var procBackend api.ProcessBackend
	var runtimeBackend api.RuntimeBackend
	var eventBackend api.EventsBackend
	var wafBackend api.WAFBackend
	var ipSetBackend api.IPSetBackend
	var domainNameSetBackend api.DomainNameSetBackend

	switch cfg.Backend {
	case config.BackendTypeMultiIndex:
		flowLogBackend = flowbackend.NewFlowLogBackend(esClient, flowInitializer, cfg.ElasticIndexMaxResultWindow)
		flowBackend = flowbackend.NewFlowBackend(esClient)
		auditBackend = auditbackend.NewBackend(esClient, auditInitializer, cfg.ElasticIndexMaxResultWindow)
		bgpBackend = bgpbackend.NewBackend(esClient, bgpInitializer, cfg.ElasticIndexMaxResultWindow)
		reportsBackend = compliancebackend.NewReportsBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow)
		snapshotsBackend = compliancebackend.NewSnapshotBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow)
		benchmarksBackend = compliancebackend.NewBenchmarksBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow)
		dnsFlowBackend = dnsbackend.NewDNSFlowBackend(esClient)
		dnsLogBackend = dnsbackend.NewDNSLogBackend(esClient, dnsInitializer, cfg.ElasticIndexMaxResultWindow)
		l7FlowBackend = l7backend.NewL7FlowBackend(esClient)
		l7LogBackend = l7backend.NewL7LogBackend(esClient, l7Initializer, cfg.ElasticIndexMaxResultWindow)
		procBackend = procbackend.NewBackend(esClient)
		runtimeBackend = runtimebackend.NewBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow)
		eventBackend = eventbackend.NewBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow)
		wafBackend = wafbackend.NewBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow)
		ipSetBackend = threatfeedsbackend.NewIPSetBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow)
		domainNameSetBackend = threatfeedsbackend.NewDomainNameSetBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow)
	case config.BackendTypeSingleIndex:
		flowLogBackend = flowbackend.NewSingleIndexFlowLogBackend(esClient, flowInitializer, cfg.ElasticIndexMaxResultWindow, index.WithBaseIndexName(cfg.ElasticFlowLogsBaseIndexName))
		flowBackend = flowbackend.NewSingleIndexFlowBackend(esClient, index.WithBaseIndexName(cfg.ElasticFlowLogsBaseIndexName))
		auditBackend = auditbackend.NewSingleIndexBackend(esClient, auditInitializer, cfg.ElasticIndexMaxResultWindow, index.WithBaseIndexName(cfg.ElasticAuditLogsBaseIndexName))
		bgpBackend = bgpbackend.NewSingleIndexBackend(esClient, bgpInitializer, cfg.ElasticIndexMaxResultWindow, index.WithBaseIndexName(cfg.ElasticBGPLogsBaseIndexName))
		reportsBackend = compliancebackend.NewSingleIndexReportsBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow, index.WithBaseIndexName(cfg.ElasticComplianceReportsBaseIndexName))
		snapshotsBackend = compliancebackend.NewSingleIndexSnapshotBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow, index.WithBaseIndexName(cfg.ElasticComplianceSnapshotsBaseIndexName))
		benchmarksBackend = compliancebackend.NewSingleIndexBenchmarksBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow, index.WithBaseIndexName(cfg.ElasticComplianceBenchmarksBaseIndexName))
		dnsFlowBackend = dnsbackend.NewSingleIndexDNSFlowBackend(esClient, index.WithBaseIndexName(cfg.ElasticDNSLogsBaseIndexName))
		dnsLogBackend = dnsbackend.NewSingleIndexDNSLogBackend(esClient, dnsInitializer, cfg.ElasticIndexMaxResultWindow, index.WithBaseIndexName(cfg.ElasticDNSLogsBaseIndexName))
		l7FlowBackend = l7backend.NewSingleIndexL7FlowBackend(esClient, index.WithBaseIndexName(cfg.ElasticL7LogsBaseIndexName))
		l7LogBackend = l7backend.NewSingleIndexL7LogBackend(esClient, l7Initializer, cfg.ElasticIndexMaxResultWindow, index.WithBaseIndexName(cfg.ElasticL7LogsBaseIndexName))
		procBackend = procbackend.NewSingleIndexBackend(esClient, index.WithBaseIndexName(cfg.ElasticFlowLogsBaseIndexName))
		runtimeBackend = runtimebackend.NewSingleIndexBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow, index.WithBaseIndexName(cfg.ElasticRuntimeReportsBaseIndexName))
		eventBackend = eventbackend.NewSingleIndexBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow, index.WithBaseIndexName(cfg.ElasticAlertsBaseIndexName))
		wafBackend = wafbackend.NewSingleIndexBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow, index.WithBaseIndexName(cfg.ElasticWAFLogsBaseIndexName))
		ipSetBackend = threatfeedsbackend.NewSingleIndexIPSetBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow, index.WithBaseIndexName(cfg.ElasticThreatFeedsIPSetBaseIndexName))
		domainNameSetBackend = threatfeedsbackend.NewSingleIndexDomainNameSetBackend(esClient, defaultInitializer, cfg.ElasticIndexMaxResultWindow, index.WithBaseIndexName(cfg.ElasticThreatFeedsDomainSetBaseIndexName))
	default:
		logrus.Fatalf("Invalid backend type: %s", cfg.Backend)
	}

	// Create a Kubernetes client to use for authorization.
	var kc *rest.Config
	if cfg.Kubeconfig == "" {
		// creates the in-cluster k8sConfig
		kc, err = rest.InClusterConfig()
	} else {
		// creates a k8sConfig from supplied kubeconfig
		kc, err = clientcmd.BuildConfigFromFlags("", cfg.Kubeconfig)
	}
	if err != nil {
		logrus.WithError(err).Fatal("Unable to load Kubernetes config")
	}

	// We can only perform authentication / authorization if our Kubernetes configuration
	// has a bearer token present.
	k, err := kubernetes.NewForConfig(kc)
	if err != nil {
		logrus.WithError(err).Fatal("Unable to create Kubernetes client")
	}
	scheme := clientruntime.NewScheme()
	if err = v3.AddToScheme(scheme); err != nil {
		logrus.WithError(err).Fatal("Failed to configure controller runtime client")
	}

	// client is used to get ManagedCluster resources in both single-tenant and multi-tenant modes.
	client, err := ctrlclient.NewWithWatch(kc, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to configure client")
	}

	authOpts := []auth.JWTAuthOption{}
	if cfg.TokenControllerEnabled {
		// Get our token signing key.
		key, err := tokenCredentials(*cfg)
		if err != nil {
			logrus.WithError(err).Fatal("Unable to acquire token signing key")
		}

		// Build a token controller to generate tokens for Linseed clients
		// in managed clusters. We'll create tokens in each managed cluster for the following
		// service account users.
		//
		// Each client that connects from a managed cluster will provide these tokens, which will map
		// back to the permissions assigned to its serviceaccount in the management cluster.
		users := []token.UserInfo{
			{Namespace: "tigera-fluentd", Name: "fluentd-node"},
			{Namespace: "tigera-fluentd", Name: "fluentd-node-windows"},
			{Namespace: "tigera-compliance", Name: "tigera-compliance-benchmarker"},
			{Namespace: "tigera-compliance", Name: "tigera-compliance-controller"},
			{Namespace: "tigera-compliance", Name: "tigera-compliance-reporter"},
			{Namespace: "tigera-compliance", Name: "tigera-compliance-snapshotter"},
			{Namespace: "tigera-dpi", Name: "tigera-dpi"},
			{Namespace: "tigera-intrusion-detection", Name: "intrusion-detection-controller"},
			{Namespace: "tigera-intrusion-detection", Name: "anomaly-detectors"},
		}

		const tokenHealthName = "TokenManager"
		tokenReconcilePeriod := 1 * time.Hour
		tokenExpiry := 24 * time.Hour
		// Register the health report period for token reconcile as double the time of the reconcile period
		healthAggregator.RegisterReporter(tokenHealthName, &health.HealthReport{Live: true}, 2*tokenReconcilePeriod)
		reportHealth := func(h *health.HealthReport) {
			healthAggregator.Report(tokenHealthName, h)
		}

		factory := k8s.NewClientSetFactory(cfg.MultiClusterForwardingCA, cfg.MultiClusterForwardingEndpoint)

		secretsNamespace := cfg.ManagementOperatorNamespace
		if len(cfg.ExpectedTenantID) > 0 {
			secretsNamespace = cfg.TenantNamespace
		}
		secretsToCopy := []corev1.Secret{
			{
				ObjectMeta: v1.ObjectMeta{
					Name:      resource.VoltronLinseedPublicCert,
					Namespace: secretsNamespace,
				},
			},
		}

		opts := []token.ControllerOption{
			token.WithIssuer(token.LinseedIssuer),
			token.WithIssuerName("tigera-linseed"),
			token.WithUserInfos(users),
			token.WithReconcilePeriod(tokenReconcilePeriod),
			token.WithExpiry(tokenExpiry),
			token.WithControllerRuntimeClient(client),
			token.WithK8sClient(k),
			token.WithPrivateKey(key),
			token.WithFactory(factory),
			token.WithTenant(cfg.ExpectedTenantID),
			token.WithHealthReport(reportHealth),
			token.WithSecretsToCopy(secretsToCopy),
		}

		if cfg.ExpectedTenantID != "" {
			impersonationInfo := user.DefaultInfo{
				Name: "system:serviceaccount:tigera-elasticsearch:tigera-linseed",
				Groups: []string{
					serviceaccount.AllServiceAccountsGroup,
					"system:authenticated",
					fmt.Sprintf("%s%s", serviceaccount.ServiceAccountGroupPrefix, "tigera-elasticsearch"),
				},
			}
			opts = append(opts, token.WithImpersonation(&impersonationInfo))
			opts = append(opts, token.WithNamespace(cfg.TenantNamespace))
		}

		tokenController, err := token.NewController(opts...)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to start token controller")
		}

		// Start the token controller.
		stop := make(chan struct{})
		defer close(stop)
		go tokenController.Run(stop)

		// Add an authenticator for JWTs issued by this tenant's Linseed.
		lsa := auth.NewLocalAuthenticator(token.LinseedIssuer, key.Public(), token.ParseClaimsLinseed)
		authOpts = append(authOpts, auth.WithAuthenticator(token.LinseedIssuer, lsa))
	}

	// Create a JWT authenticator for Linseed to use.
	authn, err := auth.NewJWTAuth(kc, k, authOpts...)
	if err != nil {
		logrus.WithError(err).Fatal("Unable to create authenticator")
	}

	// Create an RBAC authorizer to use for authorizing requests.
	authz := auth.NewRBACAuthorizer(k)
	authzHelper := middleware.NewKubernetesAuthzTracker(authz)

	// Create the full list of handlers, and register them
	// for authorization.
	handlers := []handler.Handler{
		l3.New(flowBackend, flowLogBackend),
		l7.New(l7FlowBackend, l7LogBackend),
		dns.New(dnsFlowBackend, dnsLogBackend),
		events.New(eventBackend),
		audit.New(auditBackend),
		bgp.New(bgpBackend),
		processes.New(procBackend),
		waf.New(wafBackend),
		compliance.New(benchmarksBackend, snapshotsBackend, reportsBackend),
		runtime.New(runtimeBackend),
		threatfeeds.New(ipSetBackend, domainNameSetBackend),
	}

	// Configure options used to launch the server.
	opts := []server.Option{
		server.WithMiddlewares(server.Middlewares(*cfg, authn, authzHelper)),
		server.WithAPIVersionRoutes("/api/v1", server.UnpackRoutes(handlers...)...),
		server.WithRoutes(server.UtilityRoutes()...),
	}
	if cfg.CACert != "" {
		opts = append(opts, server.WithClientCACerts(cfg.CACert))
	}

	// Make sure we register our APIs for authorization.
	for _, h := range handlers {
		for _, api := range h.APIS() {
			if api.AuthzAttributes != nil {
				authzHelper.Register(api.Method, api.URL, api.AuthzAttributes)
			}
		}
	}

	// Register the /version endpoint without authorization.
	authzHelper.Disable("GET", "/version")

	// Start the server.
	addr := fmt.Sprintf("%v:%v", cfg.Host, cfg.Port)
	server := server.NewServer(addr, cfg.FIPSModeEnabled, opts...)

	go func() {
		logrus.Infof("Listening for HTTPS requests at %s", addr)
		if err := server.ListenAndServeTLS(cfg.HTTPSCert, cfg.HTTPSKey); err != nil && err != http.ErrServerClosed {
			logrus.WithError(err).Fatal("Failed to listen for new requests for Linseed APIs")
		}
	}()

	if cfg.HealthPort != 0 {
		go func() {
			// We only want the health aggregator to be accessible from within the container.
			// Kubelet will use an exec probe to get status.
			healthAggregator.ServeHTTP(true, "localhost", cfg.HealthPort)
		}()
	}

	if cfg.EnableMetrics {
		go func() {
			metricsAddr := fmt.Sprintf("%v:%v", cfg.Host, cfg.MetricsPort)
			http.Handle("/metrics", promhttp.Handler())
			err := http.ListenAndServeTLS(metricsAddr, cfg.MetricsCert, cfg.MetricsKey, nil)
			if err != nil {
				logrus.WithError(err).Fatal("Failed to listen for new requests to query metrics")
			}
		}()
	}

	if cfg.HealthPort != 0 {
		// Indicate that we're ready to serve requests.
		healthAggregator.Report(healthName, &health.HealthReport{Live: true, Ready: true})
	}

	// Listen for termination signals
	sig := <-signalChan
	logrus.WithField("signal", sig).Info("Received shutdown signal")

	// Graceful shutdown of the server
	shutDownCtx, shutDownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutDownCancel()
	if err := server.Shutdown(shutDownCtx); err != nil {
		logrus.Fatalf("server shutdown failed: %+v", err)
	}
	logrus.Info("Server is shutting down")
}

// doHealthCheck checks the local readiness or liveness endpoint and prints its status.
// It exits with a status code based on the status.
func doHealthCheck(path string, port int) {
	url := fmt.Sprintf("http://localhost:%d/%s", port, path)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		fmt.Printf("failed to build request: %s\n", err)
		os.Exit(1)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("failed to check %s: %s\n", path, err)
		os.Exit(1)
	}
	if resp.StatusCode == http.StatusOK {
		os.Exit(0)
	} else {
		fmt.Printf("bad status code (%d) from %s endpoint\n", resp.StatusCode, path)
		os.Exit(1)
	}
}

func tokenCredentials(cfg config.Config) (*rsa.PrivateKey, error) {
	// Load the signing key.
	bs, err := os.ReadFile(cfg.TokenKey)
	if err != nil {
		return nil, err
	}
	p, _ := pem.Decode(bs)
	if p == nil {
		return nil, fmt.Errorf("failed to decode token signing key")
	}
	return x509.ParsePKCS1PrivateKey(p.Bytes)
}
