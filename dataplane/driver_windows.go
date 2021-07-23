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

package dataplane

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"

	"github.com/projectcalico/felix/calc"
	"github.com/projectcalico/felix/collector"
	"github.com/projectcalico/felix/config"
	windataplane "github.com/projectcalico/felix/dataplane/windows"
	"github.com/projectcalico/felix/dataplane/windows/hns"
	"github.com/projectcalico/libcalico-go/lib/health"
	"github.com/projectcalico/libcalico-go/lib/security"

	"github.com/prometheus/client_golang/prometheus"
)

func StartDataplaneDriver(configParams *config.Config,
	healthAggregator *health.HealthAggregator,
	collector collector.Collector,
	configChangedRestartCallback func(),
	fatalErrorCallback func(error),
	childExitedRestartCallback func(),
	k8sClientSet *kubernetes.Clientset,
	lookupsCache *calc.LookupsCache) (DataplaneDriver, *exec.Cmd, chan *sync.WaitGroup) {
	log.Info("Using Windows dataplane driver.")

	dpConfig := windataplane.Config{
		IPv6Enabled:      configParams.Ipv6Support,
		HealthAggregator: healthAggregator,

		Hostname:     configParams.FelixHostname,
		VXLANEnabled: configParams.VXLANEnabled,
		VXLANID:      configParams.VXLANVNI,
		VXLANPort:    configParams.VXLANPort,

		Collector:    collector,
		LookupsCache: lookupsCache,

		DNSCacheFile:         configParams.GetDNSCacheFile(),
		DNSCacheSaveInterval: configParams.DNSCacheSaveInterval,
		DNSCacheEpoch:        configParams.DNSCacheEpoch,
		DNSExtraTTL:          configParams.GetDNSExtraTTL(),
		DNSLogsLatency:       configParams.DNSLogsLatency,
		DNSTrustedServers:    configParams.DNSTrustedServers,
		PktMonStartArgs:      configParams.DebugWindowsPktMonStartArgs,
	}

	stopChan := make(chan *sync.WaitGroup, 1)
	winDP := windataplane.NewWinDataplaneDriver(hns.API{}, dpConfig, stopChan)
	winDP.Start()

	return winDP, nil, stopChan
}

func SupportsBPF() error {
	return fmt.Errorf("BPF dataplane is not supported on Windows")
}

func SupportsBPFKprobe() error {
	return fmt.Errorf("BPF Kprobe is not supported on Windows")
}

func ServePrometheusMetrics(configParams *config.Config) {
	for {
		log.WithFields(log.Fields{
			"host": configParams.PrometheusMetricsHost,
			"port": configParams.PrometheusMetricsPort,
		}).Info("Starting prometheus metrics endpoint")
		if configParams.PrometheusGoMetricsEnabled && configParams.PrometheusProcessMetricsEnabled {
			log.Info("Including Golang, and Process metrics")
		} else {
			if !configParams.PrometheusGoMetricsEnabled {
				log.Info("Discarding Golang metrics")
				prometheus.Unregister(prometheus.NewGoCollector())
			}
			if !configParams.PrometheusProcessMetricsEnabled {
				log.Info("Discarding process metrics")
				prometheus.Unregister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
			}
		}

		err := security.ServePrometheusMetrics(
			prometheus.DefaultGatherer,
			"",
			configParams.PrometheusMetricsPort,
			configParams.PrometheusMetricsCertFile,
			configParams.PrometheusMetricsKeyFile,
			configParams.PrometheusMetricsCAFile,
		)

		log.WithError(err).Error(
			"Prometheus metrics endpoint failed, trying to restart it...")
		time.Sleep(1 * time.Second)
	}
}
