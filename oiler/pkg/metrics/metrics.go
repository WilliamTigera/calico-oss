// Copyright (c) 2025 Tigera, Inc. All rights reserved.

package metrics

import "github.com/prometheus/client_golang/prometheus"

func init() {
	prometheus.MustRegister(DocsReadPerClusterIDAndTenantID)
	prometheus.MustRegister(DocsWrittenPerClusterIDAndTenantID)
	prometheus.MustRegister(DocsMismatchPerClusterIDAndTenantID)
	prometheus.MustRegister(FailedDocsWrittenPerClusterIDAndTenantID)
	prometheus.MustRegister(WriteDurationPerClusterIDAndTenantID)
	prometheus.MustRegister(ReadDurationPerClusterIDAndTenantID)
	prometheus.MustRegister(MigrationLag)
	prometheus.MustRegister(LastReadGeneratedTimestamp)
	prometheus.MustRegister(LastWrittenGeneratedTimestamp)
	prometheus.MustRegister(WaitForData)
	prometheus.MustRegister(MissingDocs)
}

const (
	LabelClusterID = "cluster_id"
	LabelTenantID  = "tenant_id"
	JobName        = "job_name"
	Source         = "source"
	IntervalBehind = "interval_behind"
)

var (
	histogramBuckets = []float64{.1, .25, .5, 1, 5, 10}

	DocsWrittenPerClusterIDAndTenantID = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tigera",
			Subsystem: "oiler",
			Name:      "docs_writes_successful",
			Help:      "Number of docs ingested into ElasticSearch broken down per cluster id and tenant id.",
		}, []string{LabelClusterID, LabelTenantID, JobName, Source})

	FailedDocsWrittenPerClusterIDAndTenantID = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tigera",
			Subsystem: "oiler",
			Name:      "docs_writes_failed",
			Help:      "Number of failed ingested docs into ElasticSearch broken down per cluster id and tenant id.",
		}, []string{LabelClusterID, LabelTenantID, JobName, Source})

	DocsReadPerClusterIDAndTenantID = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tigera",
			Subsystem: "oiler",
			Name:      "docs_read",
			Help:      "Number of docs read from ElasticSearch broken down per cluster id and tenant id.",
		}, []string{LabelClusterID, LabelTenantID, JobName, Source})

	LastReadGeneratedTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "tigera",
		Subsystem: "oiler",
		Name:      "last_read_generated_timestamp",
		Help:      "Last generated timestamp read from primary.",
	}, []string{JobName, LabelClusterID})

	LastWrittenGeneratedTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "tigera",
		Subsystem: "oiler",
		Name:      "last_written_generated_timestamp",
		Help:      "Last written timestamp read to secondary.",
	}, []string{JobName, LabelClusterID})

	MigrationLag = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "tigera",
		Subsystem: "oiler",
		Name:      "migration_lag",
		Help:      "Difference between last written document to the secondary source and last read document from primary source in seconds",
	}, []string{JobName, LabelClusterID})

	WaitForData = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "tigera",
		Subsystem: "oiler",
		Name:      "wait_for_data",
		Help:      "How much we are waiting for data.",
	}, []string{JobName, LabelClusterID})

	MissingDocs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "tigera",
		Subsystem: "oiler",
		Name:      "missing_docs_count",
		Help:      "How many documents are missing from primary/secondary.",
	}, []string{JobName, LabelClusterID, Source, IntervalBehind})

	ReadDurationPerClusterIDAndTenantID = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "tigera",
		Subsystem: "oiler",
		Name:      "docs_read_duration_seconds",
		Help:      "Duration of read operations.",
		Buckets:   histogramBuckets,
	}, []string{LabelClusterID, LabelTenantID, JobName, Source})

	WriteDurationPerClusterIDAndTenantID = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "tigera",
		Subsystem: "oiler",
		Name:      "docs_write_duration_seconds",
		Help:      "Duration of write operations.",
		Buckets:   histogramBuckets,
	}, []string{LabelClusterID, LabelTenantID, JobName, Source})

	DocsMismatchPerClusterIDAndTenantID = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tigera",
			Subsystem: "oiler",
			Name:      "docs_mismatch",
			Help:      "Number of mismatched docs broken down per cluster id and tenant id.",
		}, []string{LabelClusterID, LabelTenantID, JobName, Source})
)
