// Copyright (c) 2025 Tigera, Inc. All rights reserved.

package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/libcalico-go/lib/health"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/oiler/pkg/checkpoint"
	"github.com/projectcalico/calico/oiler/pkg/config"
	"github.com/projectcalico/calico/oiler/pkg/migrator"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator"
)

const (
	retentionPeriod      = 12 * interval
	auditRetentionPeriod = 180 * interval
	interval             = 24 * time.Hour
)

var (
	ready bool
	live  bool
)

func init() {
	flag.BoolVar(&ready, "ready", false, "Set to get readiness information")
	flag.BoolVar(&live, "live", false, "Set to get liveness information")
}

func main() {
	flag.Parse()

	// Read and reconcile configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		panic(err)
	}

	// Configure logging
	config.ConfigureLogging(cfg.LogLevel)
	logrus.Debugf("Starting with %#v", cfg)

	// Create a health aggregator and mark us as alive.
	// For now, we don't do periodic updates to our health, so don't set a timeout.
	const healthName = "Oiler"
	healthAggregator := health.NewHealthAggregator()
	healthAggregator.RegisterReporter(healthName, &health.HealthReport{Live: true}, 0)

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.MetricsPort), nil)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to listen for new requests to query metrics")
		}
	}()

	// Listen for OS signals (Ctrl+C)
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	// Create the primary backend catalogue
	primary := migrator.MustGetCatalogue(*cfg.PrimaryElasticClient, cfg.PrimaryBackend, cfg.LogLevel, migrator.Primary)
	//Create the secondary backend catalogue
	secondary := migrator.MustGetCatalogue(*cfg.SecondaryElasticClient, cfg.SecondaryBackend, cfg.LogLevel, migrator.Secondary)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		switch cfg.Mode {
		case config.OilerMigrateMode:
			for _, cluster := range cfg.Clusters {
				go func() {
					logrus.Infof("Starting migration process for cluster %s", cluster)
					migrate(ctx, cluster, *cfg, primary, secondary)
				}()
			}
		case config.OilerValidateMode:
			for _, cluster := range cfg.Clusters {
				go func() {
					validationEndTime := time.Now().UTC()
					logrus.Infof("[VALIDATE] Starting validation process for cluster %s until %v", cluster, validationEndTime)
					startRead := validationEndTime.Add(-1 * getMaxRetentionPeriod(cfg.DataType)).Truncate(interval)
					intervalBehind := int(math.Ceil(getMaxRetentionPeriod(cfg.DataType).Seconds() / interval.Seconds()))
					for ; startRead.Before(validationEndTime); startRead = startRead.Add(interval) {
						endRead := startRead.Add(interval)
						if endRead.After(validationEndTime) {
							endRead = validationEndTime.UTC()
						}
						validate(ctx, cluster, *cfg, primary, secondary, startRead, endRead, intervalBehind)
						intervalBehind--
					}
				}()
			}
		default:
			logrus.Fatal("Unrecognized mode")
		}
	}()

	// Listen for termination signals
	sig := <-signalChan

	logrus.Info("Shutting down gracefully. Waiting another 30 seconds for metrics to be picked up")
	time.Sleep(30 * time.Second)
	logrus.WithField("signal", sig).Info("Received shutdown signal")

	ctx.Done()
}

func getMaxRetentionPeriod(dataType bapi.DataType) time.Duration {
	if dataType == bapi.AuditEELogs || dataType == bapi.AuditKubeLogs {
		return auditRetentionPeriod
	}
	return retentionPeriod
}

func migrate(ctx context.Context, cluster string, cfg config.Config, primary migrator.BackendCatalogue, secondary migrator.BackendCatalogue) {
	k8sClient, err := checkpoint.NewRealK8sClient(cfg.KubeConfig)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting kubernetes client")
	}
	storage := checkpoint.NewConfigMapStorage(k8sClient, cfg.Namespace, checkpoint.ConfigMapName(cfg.DataType, cluster, cfg.PrimaryTenantID))
	start, err := storage.Read(ctx)
	if err != nil {
		logrus.WithError(err).Fatal("Error reading checkpoint")
	}
	checkpoints := make(chan operator.TimeInterval)
	coordinator := checkpoint.NewCoordinator(checkpoints, storage)
	go func() {
		defer close(checkpoints)
		coordinator.Run(ctx, cluster)
	}()

	switch cfg.DataType {
	case bapi.AuditEELogs:
		migrator.NewAuditEEMigrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)
	case bapi.AuditKubeLogs:
		migrator.NewAuditKubeMigrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)
	case bapi.BGPLogs:
		migrator.NewBGPMigrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)
	case bapi.Benchmarks:
		migrator.NewBenchmarksMigrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)
	case bapi.ReportData:
		migrator.NewReportsMigrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)
	case bapi.Snapshots:
		migrator.NewSnapshotsMigrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)
	case bapi.DNSLogs:
		migrator.NewDNSMigrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)
	case bapi.Events:
		migrator.NewEventsMigrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)
	case bapi.FlowLogs:
		migrator.NewFlowMigrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)
	case bapi.L7Logs:
		migrator.NewL7Migrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)
	case bapi.RuntimeReports:
		migrator.NewRuntimeMigrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)
	case bapi.DomainNameSet:
		migrator.NewDomainNameSetMigrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)
	case bapi.IPSet:
		migrator.NewIPSetMigrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)
	case bapi.WAFLogs:
		migrator.NewWAFMigrator(cluster, cfg, primary, secondary, true).Run(ctx, start, checkpoints)

	default:
		logrus.Fatal("Unrecognized data type")
	}
}

func validate(ctx context.Context, cluster string, cfg config.Config, primary migrator.BackendCatalogue, secondary migrator.BackendCatalogue, read time.Time, endRead time.Time, interval int) {
	switch cfg.DataType {
	case bapi.AuditEELogs:
		migrator.NewAuditEEMigrator(cluster, cfg, primary, secondary, false).Validate(ctx, read, endRead, interval)
	case bapi.AuditKubeLogs:
		migrator.NewAuditKubeMigrator(cluster, cfg, primary, secondary, false).Validate(ctx, read, endRead, interval)
	case bapi.BGPLogs:
		migrator.NewBGPMigrator(cluster, cfg, primary, secondary, false).Validate(ctx, read, endRead, interval)
	case bapi.Benchmarks:
		migrator.NewBenchmarksMigrator(cluster, cfg, primary, secondary, false).Validate(ctx, read, endRead, interval)
	case bapi.ReportData:
		migrator.NewReportsMigrator(cluster, cfg, primary, secondary, false).Validate(ctx, read, endRead, interval)
	case bapi.Snapshots:
		migrator.NewSnapshotsMigrator(cluster, cfg, primary, secondary, false).Validate(ctx, read, endRead, interval)
	case bapi.DNSLogs:
		migrator.NewDNSMigrator(cluster, cfg, primary, secondary, false).Validate(ctx, read, endRead, interval)
	case bapi.Events:
		migrator.NewEventsMigrator(cluster, cfg, primary, secondary, false).Validate(ctx, read, endRead, interval)
	case bapi.FlowLogs:
		migrator.NewFlowMigrator(cluster, cfg, primary, secondary, false).Validate(ctx, read, endRead, interval)
	case bapi.L7Logs:
		migrator.NewL7Migrator(cluster, cfg, primary, secondary, false).Validate(ctx, read, endRead, interval)
	case bapi.RuntimeReports:
		// RuntimeReports have a different query mechanism that the rest of the data. Will default to generated_time
		migrator.NewRuntimeMigrator(cluster, cfg, primary, secondary, true).Validate(ctx, read, endRead, interval)
	case bapi.DomainNameSet:
		migrator.NewDomainNameSetMigrator(cluster, cfg, primary, secondary, false).Validate(ctx, read, endRead, interval)
	case bapi.IPSet:
		migrator.NewIPSetMigrator(cluster, cfg, primary, secondary, false).Validate(ctx, read, endRead, interval)
	case bapi.WAFLogs:
		migrator.NewWAFMigrator(cluster, cfg, primary, secondary, false).Validate(ctx, read, endRead, interval)

	default:
		logrus.Fatal("Unrecognized data type")
	}
}
