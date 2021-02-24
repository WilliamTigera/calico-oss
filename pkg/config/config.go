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

package config

import (
	"github.com/kelseyhightower/envconfig"
)

const (
	EnvLogLevel           = "LOG_LEVEL"
	EnvReconcilerPeriod   = "RECONCILER_PERIOD"
	EnvEnabledControllers = "ENABLED_CONTROLLERS"
	EnvCompactionPeriod   = "COMPACTION_PERIOD"
	EnvHealthEnabled      = "HEALTH_ENABLED"
	EnvSyncNodeLabels     = "SYNC_NODE_LABELS"
	EnvAutoHostEndpoints  = "AUTO_HOST_ENDPOINTS"
)

var AllEnvs = []string{EnvLogLevel, EnvReconcilerPeriod, EnvEnabledControllers, EnvCompactionPeriod, EnvHealthEnabled, EnvSyncNodeLabels, EnvAutoHostEndpoints}

// Config represents the configuration we load from the environment variables
type Config struct {
	// Minimum log level to emit.
	LogLevel string `default:"info" split_words:"true"`

	// Period to perform reconciliation with the Calico datastore.
	// DEPRECATED see mergeConfig()
	//ReconcilerPeriod string `default:"5m" split_words:"true"`

	// etcdv3 compaction period. Set to 0 to disable the compactor.
	// DEPRECATED see mergeConfig()
	//CompactionPeriod string `default:"10m" split_words:"true"`

	// Which controllers to run.
	// DEPRECATED see mergeConfig()
	//EnabledControllers string `default:"node,policy,namespace,workloadendpoint,serviceaccount" split_words:"true"`

	// Number of workers to run for each controller.
	WorkloadEndpointWorkers                         int `default:"1" split_words:"true"`
	ProfileWorkers                                  int `default:"1" split_words:"true"`
	PolicyWorkers                                   int `default:"1" split_words:"true"`
	ServiceWorkers                                  int `default:"1" split_words:"true"`
	NodeWorkers                                     int `default:"1" split_words:"true"`
	FederatedServicesWorkers                        int `default:"1" split_words:"true"`
	AuthorizationWorkers                            int `default:"1" split_words:"true"`
	ManagedClusterWorkers                           int `default:"1" split_words:"true"`
	ManagedClusterElasticsearchConfigurationWorkers int `default:"1" split_words:"true"`

	// Path to a kubeconfig file to use for accessing the k8s API.
	Kubeconfig string `default:"" split_words:"false"`

	// Whether the controller should not initialize the Calico datastore (for controllers that do
	// not require this, it allows the service account to access the minimal set of resources).
	DoNotInitializeCalico bool `default:"false" split_words:"true"`

	// Enable healthchecks
	// DEPRECATED see mergeConfig()
	//HealthEnabled bool `default:"true"`

	// Enable syncing of node labels
	// DEPRECATED see mergeConfig()
	//SyncNodeLabels bool `default:"true" split_words:"true"`

	// Enable creating hostendpoints for nodes
	// DEPRECATED see mergeConfig()
	//AutoHostEndpoints string `default:"disabled" split_words:"true"`

	// etcdv3 or kubernetes
	DatastoreType string `default:"etcdv3" split_words:"true"`

	// Option used for testing and debugging to set the license polling interval to a shorter period.
	DebugUseShortPollIntervals bool `default:"false" split_words:"true"`

	MultiClusterForwardingEndpoint string `default:"https://tigera-manager.tigera-manager.svc:9443" split_words:"true"`
	MultiClusterForwardingCA       string `default:"/manager-tls/cert" split_words:"true"`

	OIDCAuthUsernamePrefix string `default:"" split_words:"true"`
	OIDCAuthGroupPrefix    string `default:"" split_words:"true"`

	ElasticLicenseType string `default:"" split_words:"true"`
	ElasticUsername    string `default:"" split_words:"true"`
	ElasticPassword    string `default:"" split_words:"true"`
	ElasticHost        string `default:"" split_words:"true"`
	ElasticPort        string `default:"9200" split_words:"true"`
	ElasticCA          string `default:"" split_words:"true"`
}

// Parse parses envconfig and stores in Config struct
func (c *Config) Parse() error {
	return envconfig.Process("", c)
}
