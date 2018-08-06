// Copyright (c) 2017 Tigera, Inc. All rights reserved.
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

type Config struct {
	// Minimum log level to emit.
	LogLevel string `default:"info" split_words:"true"`

	// Period to perform reconciliation with the Calico datastore.
	ReconcilerPeriod string `default:"5m" split_words:"true"`

	// etcdv3 compaction period. Set to 0 to disable the compactor.
	CompactionPeriod string `default:"10m" split_words:"true"`

	// Which controllers to run.
	EnabledControllers string `default:"policy,namespace,workloadendpoint,serviceaccount" split_words:"true"`

	// Number of workers to run for each controller.
	WorkloadEndpointWorkers  int `default:"1" split_words:"true"`
	ProfileWorkers           int `default:"1" split_words:"true"`
	PolicyWorkers            int `default:"1" split_words:"true"`
	NodeWorkers              int `default:"1" split_words:"true"`
	FederatedServicesWorkers int `default:"1" split_words:"true"`

	// Path to a kubeconfig file to use for accessing the k8s API.
	Kubeconfig string `default:"" split_words:"false"`

	// Whether the controller should not initialize the Calico datastore (for controllers that do
	// not require this, it allows the service account to access the minimal set of resources).
	DoNotInitializeCalico bool `default:"false" split_words:"true"`

	// Enable healthchecks
	HealthEnabled bool `default:"true"`

	// Option used for testing and debugging to set the license polling interval to a shorter period.
	DebugUseShortPollIntervals bool `default:"false" split_words:"true"`
}

// Parse parses envconfig and stores in Config struct
func (c *Config) Parse() error {
	return envconfig.Process("", c)
}
