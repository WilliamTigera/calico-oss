// Copyright (c) 2017 Tigera, Inc. All rights reserved.

package collector

import (
	"encoding/json"
	"fmt"
	"log/syslog"
	"net/http"
	"strconv"
	"time"

	log "github.com/Sirupsen/logrus"
	logrus_syslog "github.com/Sirupsen/logrus/hooks/syslog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/projectcalico/felix/set"
)

var (
	gaugeDeniedPackets = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "calico_denied_packets",
		Help: "Total number of packets denied by calico policies.",
	},
		[]string{"srcIP", "policy"},
	)
	gaugeDeniedBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "calico_denied_bytes",
		Help: "Total number of bytes denied by calico policies.",
	},
		[]string{"srcIP", "policy"},
	)
)

type MetricUpdate struct {
	policy       string
	tuple        Tuple
	packets      int
	bytes        int
	deltaPackets int
	deltaBytes   int
}

func NewMetricUpdateFromRuleTrace(t Tuple, rt *RuleTrace) *MetricUpdate {
	p, b := rt.ctr.Values()
	dp, db := rt.ctr.DeltaValues()
	return &MetricUpdate{
		policy:       rt.ToString(),
		tuple:        t,
		packets:      p,
		bytes:        b,
		deltaPackets: dp,
		deltaBytes:   db,
	}
}

type MetricsReporter interface {
	Start()
	Report(mu *MetricUpdate) error
	Expire(mu *MetricUpdate) error
}

// TODO(doublek): When we want different ways of aggregating, this will
// need to be dynamic and a KeyType.
type AggregateKey struct {
	policy string
	srcIP  string
}

type AggregateValue struct {
	labels  prometheus.Labels
	packets prometheus.Gauge
	bytes   prometheus.Gauge
	refs    set.Set
}

type ReporterManager struct {
	ReportChan chan *MetricUpdate
	ExpireChan chan *MetricUpdate
	reporters  []MetricsReporter
}

func NewReporterManager() *ReporterManager {
	return &ReporterManager{
		ReportChan: make(chan *MetricUpdate),
		ExpireChan: make(chan *MetricUpdate),
	}
}

func (r *ReporterManager) RegisterMetricsReporter(mr MetricsReporter) {
	r.reporters = append(r.reporters, mr)
}

func (r *ReporterManager) Start() {
	for _, reporter := range r.reporters {
		reporter.Start()
	}
	go r.startManaging()
}

func (r *ReporterManager) startManaging() {
	log.Info("Staring ReporterManager")
	for {
		// TODO(doublek): Channel for stopping the reporter.
		select {
		case mu := <-r.ReportChan:
			log.Debugf("Reporting metric update %+v", mu)
			for _, reporter := range r.reporters {
				reporter.Report(mu)
			}
		case mu := <-r.ExpireChan:
			log.Debugf("Expiring metric update %+v", mu)
			for _, reporter := range r.reporters {
				reporter.Expire(mu)
			}
		}
	}
}

// PrometheusReporter records denied packets and bytes statistics in prometheus metrics.
type PrometheusReporter struct {
	port             int
	registry         *prometheus.Registry
	aggStats         map[AggregateKey]AggregateValue
	deleteCandidates set.Set
	deleteChan       chan AggregateKey
	reportChan       chan *MetricUpdate
	expireChan       chan *MetricUpdate
	retentionTimers  map[AggregateKey]*time.Timer
	retentionTime    time.Duration
}

func NewPrometheusReporter(port int, rTime time.Duration) *PrometheusReporter {
	registry := prometheus.NewRegistry()
	registry.MustRegister(gaugeDeniedPackets)
	registry.MustRegister(gaugeDeniedBytes)
	return &PrometheusReporter{
		port:             port,
		registry:         registry,
		aggStats:         make(map[AggregateKey]AggregateValue),
		deleteCandidates: set.New(),
		deleteChan:       make(chan AggregateKey),
		reportChan:       make(chan *MetricUpdate),
		expireChan:       make(chan *MetricUpdate),
		retentionTimers:  make(map[AggregateKey]*time.Timer),
		retentionTime:    rTime,
	}
}

func (pr *PrometheusReporter) Start() {
	log.Info("Staring PrometheusReporter")
	go pr.servePrometheusMetrics()
	go pr.startReporter()
}

func (pr *PrometheusReporter) servePrometheusMetrics() {
	for {
		mux := http.NewServeMux()
		handler := promhttp.HandlerFor(pr.registry, promhttp.HandlerOpts{})
		mux.Handle("/metrics", handler)
		err := http.ListenAndServe(fmt.Sprintf(":%v", pr.port), handler)
		log.WithError(err).Error(
			"Prometheus reporter metrics endpoint failed, trying to restart it...")
		time.Sleep(1 * time.Second)
	}
}

func (pr *PrometheusReporter) startReporter() {

	for {
		select {
		case key := <-pr.deleteChan:
			// If a timer was stopped by us, then the key will not exist. Delete only
			// when a timer was fired rather than stopped.
			if _, exists := pr.retentionTimers[key]; exists {
				pr.deleteMetric(key)
			}
		case mu := <-pr.reportChan:
			pr.reportMetric(mu)
		case mu := <-pr.expireChan:
			pr.expireMetric(mu)
		}
	}
}

func (pr *PrometheusReporter) Report(mu *MetricUpdate) error {
	pr.reportChan <- mu
	return nil
}

func (pr *PrometheusReporter) reportMetric(mu *MetricUpdate) {
	key := AggregateKey{mu.policy, mu.tuple.src}
	value, ok := pr.aggStats[key]
	if ok {
		if pr.deleteCandidates.Contains(key) {
			pr.deleteCandidates.Discard(key)
			pr.cleanupRetentionTimer(key)
		}
		value.refs.Add(mu.tuple)
	} else {
		l := prometheus.Labels{
			"srcIP":  key.srcIP,
			"policy": key.policy,
		}
		value = AggregateValue{
			labels:  l,
			packets: gaugeDeniedPackets.With(l),
			bytes:   gaugeDeniedBytes.With(l),
			refs:    set.FromArray([]Tuple{mu.tuple}),
		}
	}
	value.packets.Add(float64(mu.deltaPackets))
	value.bytes.Add(float64(mu.deltaBytes))
	pr.aggStats[key] = value
	return
}

func (pr *PrometheusReporter) Expire(mu *MetricUpdate) error {
	pr.expireChan <- mu
	return nil
}

func (pr *PrometheusReporter) expireMetric(mu *MetricUpdate) {
	key := AggregateKey{mu.policy, mu.tuple.src}
	value, ok := pr.aggStats[key]
	if !ok || !value.refs.Contains(mu.tuple) {
		return
	}
	// If the metric update has updated counters this is the time to update our counters.
	// We retain deleted metric for a little bit so that prometheus can get a chance
	// to scrape the metric.
	if mu.deltaPackets != 0 && mu.deltaBytes != 0 {
		value.packets.Add(float64(mu.deltaPackets))
		value.bytes.Add(float64(mu.deltaBytes))
		pr.aggStats[key] = value
	}
	value.refs.Discard(mu.tuple)
	pr.aggStats[key] = value
	if value.refs.Len() == 0 {
		pr.markForDeletion(key)
	}
	return
}

func (pr *PrometheusReporter) markForDeletion(key AggregateKey) {
	log.WithField("key", key).Debug("Marking metric for deletion.")
	pr.deleteCandidates.Add(key)
	timer := time.NewTimer(pr.retentionTime)
	pr.retentionTimers[key] = timer
	go func() {
		log.Debugf("Starting retention timer for key %+v", key)
		<-timer.C
		pr.deleteChan <- key
	}()
}

func (pr *PrometheusReporter) deleteMetric(key AggregateKey) {
	log.WithField("key", key).Debug("Cleaning up candidate marked to be deleted.")
	value, ok := pr.aggStats[key]
	if ok {
		gaugeDeniedPackets.Delete(value.labels)
		gaugeDeniedBytes.Delete(value.labels)
		delete(pr.aggStats, key)
	}
	pr.deleteCandidates.Discard(key)
	pr.cleanupRetentionTimer(key)
}

func (pr *PrometheusReporter) cleanupRetentionTimer(key AggregateKey) {
	log.Debugf("Cleaning up retention timer for key %+v", key)
	timer, exists := pr.retentionTimers[key]
	if exists {
		delete(pr.retentionTimers, key)
		timer.Stop()
	}
}

type SyslogReporter struct {
	slog *log.Logger
}

// NewSyslogReporter configures and returns a SyslogReporter.
// Network and Address can be used to configure remote syslogging. Leaving both
// of these values empty implies using local syslog such as /dev/log.
func NewSyslogReporter(network, address string) *SyslogReporter {
	slog := log.New()
	priority := syslog.LOG_USER | syslog.LOG_INFO
	tag := "calico-felix"
	hook, err := logrus_syslog.NewSyslogHook(network, address, priority, tag)
	if err != nil {
		log.Errorf("Syslog Reporting is disabled - Syslog Hook could not be configured %v", err)
		return nil
	}
	slog.Hooks.Add(hook)
	slog.Formatter = &DataOnlyJSONFormatter{}
	return &SyslogReporter{
		slog: slog,
	}
}

func (sr *SyslogReporter) Start() {
	log.Info("Staring SyslogReporter")
}

func (sr *SyslogReporter) Report(mu *MetricUpdate) error {
	f := log.Fields{
		"proto":   strconv.Itoa(mu.tuple.proto),
		"srcIP":   mu.tuple.src,
		"srcPort": strconv.Itoa(mu.tuple.l4Src),
		"dstIP":   mu.tuple.dst,
		"dstPort": strconv.Itoa(mu.tuple.l4Dst),
		"policy":  mu.policy,
		"action":  DenyAction,
		"packets": mu.packets,
		"bytes":   mu.bytes,
	}
	sr.slog.WithFields(f).Info("")
	return nil
}

func (sr *SyslogReporter) Expire(mu *MetricUpdate) error {
	return nil
}

// Logrus Formatter that strips the log entry of messages, time and log level and
// outputs *only* entry.Data.
type DataOnlyJSONFormatter struct{}

func (f *DataOnlyJSONFormatter) Format(entry *log.Entry) ([]byte, error) {
	serialized, err := json.Marshal(entry.Data)
	if err != nil {
		return nil, fmt.Errorf("Failed to marshal data to JSON %v", err)
	}
	return append(serialized, '\n'), nil
}
