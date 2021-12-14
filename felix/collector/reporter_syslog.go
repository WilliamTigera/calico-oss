// +build !windows

// Copyright (c) 2017-2018 Tigera, Inc. All rights reserved.

package collector

import (
	"encoding/json"
	"fmt"
	"log/syslog"
	"net"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/libcalico-go/lib/logutils"
)

const logQueueSize = 100
const DebugDisableLogDropping = false

type SyslogReporter struct {
	slog *log.Logger
}

// Felix Metrics
var (
	counterDroppedLogs = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "felix_reporter_logs_dropped",
		Help: "Number of logs dropped because the output stream was blocked in the Syslog reporter.",
	})
	counterLogErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "felix_reporter_log_errors",
		Help: "Number of errors encountered while logging in the Syslog reporter.",
	})
)

func init() {
	prometheus.MustRegister(
		counterDroppedLogs,
		counterLogErrors,
	)
}

// NewSyslogReporter configures and returns a SyslogReporter.
// Network and Address can be used to configure remote syslogging. Leaving both
// of these values empty implies using local syslog such as /dev/log.
func NewSyslogReporter(network, address string) *SyslogReporter {
	slog := log.New()
	priority := syslog.LOG_USER | syslog.LOG_INFO
	tag := "calico-felix"
	w, err := syslog.Dial(network, address, priority, tag)
	if err != nil {
		log.Warnf("Syslog Reporting is disabled - Syslog Hook could not be configured %v", err)
		return nil
	}
	syslogDest := logutils.NewSyslogDestination(
		log.InfoLevel,
		w,
		make(chan logutils.QueuedLog, logQueueSize),
		DebugDisableLogDropping,
		counterLogErrors,
	)

	hook := logutils.NewBackgroundHook([]log.Level{log.InfoLevel}, log.InfoLevel, []*logutils.Destination{syslogDest}, counterDroppedLogs)
	hook.Start()
	slog.Hooks.Add(hook)
	slog.Formatter = &DataOnlyJSONFormatter{}
	return &SyslogReporter{
		slog: slog,
	}
}

func (sr *SyslogReporter) Start() {
	log.Info("Starting SyslogReporter")
}

func (sr *SyslogReporter) Report(mu MetricUpdate) error {
	if (mu.inMetric.deltaPackets == 0 && mu.inMetric.deltaBytes == 0) &&
		(mu.outMetric.deltaPackets == 0 && mu.outMetric.deltaBytes == 0) {
		// No update. It isn't an error.
		return nil
	}
	lastRuleID := mu.GetLastRuleID()
	if lastRuleID == nil {
		log.WithField("metric update", mu).Error("no rule id present")
		return fmt.Errorf("Invalid metric update")
	}
	f := log.Fields{
		"proto":      strconv.Itoa(mu.tuple.proto),
		"srcIP":      net.IP(mu.tuple.src[:16]).String(),
		"srcPort":    strconv.Itoa(mu.tuple.l4Src),
		"dstIP":      net.IP(mu.tuple.dst[:16]).String(),
		"dstPort":    strconv.Itoa(mu.tuple.l4Dst),
		"tier":       lastRuleID.TierString(),
		"policy":     lastRuleID.NameString(),
		"rule":       lastRuleID.IndexStr,
		"action":     lastRuleID.ActionString(),
		"ruleDir":    lastRuleID.DirectionString(),
		"trafficDir": ruleDirToTrafficDir(lastRuleID.Direction).String(),
		"inPackets":  mu.inMetric.deltaPackets,
		"inBytes":    mu.inMetric.deltaBytes,
		"outPackets": mu.outMetric.deltaPackets,
		"outBytes":   mu.outMetric.deltaBytes,
		"updateType": mu.updateType,
	}
	sr.slog.WithFields(f).Info("")
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
