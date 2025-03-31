// Copyright (c) 2025 Tigera, Inc. All rights reserved.

package migrator

import (
	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	"github.com/projectcalico/calico/oiler/pkg/config"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator/audit"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator/bgp"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator/compliance"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator/dns"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator/events"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator/flow"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator/l7"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator/runtime"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator/threatfeeds"
	"github.com/projectcalico/calico/oiler/pkg/migrator/operator/waf"
)

func NewAuditEEMigrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.AuditLog] {
	return newAuditMigrator(cluster, cfg, v1.AuditLogTypeEE, primary, secondary, queryByGeneratedTime)
}

func newAuditMigrator(cluster string, cfg config.Config, auditType v1.AuditLogType, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.AuditLog] {
	return Migrator[v1.AuditLog]{
		Primary:   audit.NewOperator(auditType, primary.AuditBackend, primaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Secondary: audit.NewOperator(auditType, secondary.AuditBackend, secondaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Cfg:       NewConfig(cluster, cfg),
	}
}

func primaryClusterInfo(cluster string, cfg config.Config) bapi.ClusterInfo {
	return bapi.ClusterInfo{Cluster: cluster, Tenant: cfg.PrimaryTenantID}
}

func secondaryClusterInfo(cluster string, cfg config.Config) bapi.ClusterInfo {
	return bapi.ClusterInfo{Cluster: cluster, Tenant: cfg.SecondaryTenantID}
}

func NewAuditKubeMigrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.AuditLog] {
	return newAuditMigrator(cluster, cfg, v1.AuditLogTypeKube, primary, secondary, queryByGeneratedTime)
}

func NewBGPMigrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.BGPLog] {
	return Migrator[v1.BGPLog]{
		Primary:   bgp.NewOperator(primary.BGPBackend, primaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Secondary: bgp.NewOperator(secondary.BGPBackend, secondaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Cfg:       NewConfig(cluster, cfg),
	}
}

func NewBenchmarksMigrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.Benchmarks] {
	return Migrator[v1.Benchmarks]{
		Primary:   compliance.NewBenchmarksOperator(primary.BenchmarksBackend, primaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Secondary: compliance.NewBenchmarksOperator(secondary.BenchmarksBackend, secondaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Cfg:       NewConfig(cluster, cfg),
	}
}

func NewReportsMigrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.ReportData] {
	return Migrator[v1.ReportData]{
		Primary:   compliance.NewReportsOperator(primary.ReportsBackend, primaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Secondary: compliance.NewReportsOperator(secondary.ReportsBackend, secondaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Cfg:       NewConfig(cluster, cfg),
	}
}

func NewSnapshotsMigrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.Snapshot] {
	return Migrator[v1.Snapshot]{
		Primary:   compliance.NewSnapshotsOperator(primary.SnapshotsBackend, primaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Secondary: compliance.NewSnapshotsOperator(secondary.SnapshotsBackend, secondaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Cfg:       NewConfig(cluster, cfg),
	}
}

func NewDNSMigrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.DNSLog] {
	return Migrator[v1.DNSLog]{
		Primary:   dns.NewOperator(primary.DNSLogBackend, primaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Secondary: dns.NewOperator(secondary.DNSLogBackend, secondaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Cfg:       NewConfig(cluster, cfg),
	}
}

func NewEventsMigrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.Event] {
	return Migrator[v1.Event]{
		Primary:   events.NewOperator(primary.EventBackend, primaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Secondary: events.NewOperator(secondary.EventBackend, secondaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Cfg:       NewConfig(cluster, cfg),
	}
}

func NewFlowMigrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.FlowLog] {
	return Migrator[v1.FlowLog]{
		Primary:   flow.NewOperator(primary.FlowLogBackend, primaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Secondary: flow.NewOperator(secondary.FlowLogBackend, secondaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Cfg:       NewConfig(cluster, cfg),
	}
}

func NewL7Migrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.L7Log] {
	return Migrator[v1.L7Log]{
		Primary:   l7.NewOperator(primary.L7LogBackend, primaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Secondary: l7.NewOperator(secondary.L7LogBackend, secondaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Cfg:       NewConfig(cluster, cfg),
	}
}

func NewRuntimeMigrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.RuntimeReport] {
	return Migrator[v1.RuntimeReport]{
		Primary:   runtime.NewOperator(primary.RuntimeBackend, primaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Secondary: runtime.NewOperator(secondary.RuntimeBackend, secondaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Cfg:       NewConfig(cluster, cfg),
	}
}

func NewWAFMigrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.WAFLog] {
	return Migrator[v1.WAFLog]{
		Primary:   waf.NewOperator(primary.WAFBackend, primaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Secondary: waf.NewOperator(secondary.WAFBackend, secondaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Cfg:       NewConfig(cluster, cfg),
	}
}

func NewDomainNameSetMigrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.DomainNameSetThreatFeed] {
	return Migrator[v1.DomainNameSetThreatFeed]{
		Primary:   threatfeeds.NewDomainNameSetOperator(primary.DomainNameSetBackend, primaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Secondary: threatfeeds.NewDomainNameSetOperator(secondary.DomainNameSetBackend, secondaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Cfg:       NewConfig(cluster, cfg),
	}
}

func NewIPSetMigrator(cluster string, cfg config.Config, primary BackendCatalogue, secondary BackendCatalogue, queryByGeneratedTime bool) Migrator[v1.IPSetThreatFeed] {
	return Migrator[v1.IPSetThreatFeed]{
		Primary:   threatfeeds.NewIPSetOperator(primary.IPSetBackend, primaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Secondary: threatfeeds.NewIPSetOperator(secondary.IPSetBackend, secondaryClusterInfo(cluster, cfg), queryByGeneratedTime),
		Cfg:       NewConfig(cluster, cfg),
	}
}
