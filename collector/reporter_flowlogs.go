// Copyright (c) 2018-2020 Tigera, Inc. All rights reserved.

package collector

import (
	"time"

	"fmt"

	"github.com/gavv/monotime"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/felix/jitter"
	"github.com/projectcalico/felix/rules"
	"github.com/projectcalico/libcalico-go/lib/health"
)

type FlowLogGetter interface {
	GetAndCalibrate(newLevel FlowAggregationKind) []*FlowLog
}

type FlowLogAggregator interface {
	FlowLogGetter
	IncludeLabels(bool) FlowLogAggregator
	IncludePolicies(bool) FlowLogAggregator
	MaxOriginalIPsSize(int) FlowLogAggregator
	AggregateOver(FlowAggregationKind) FlowLogAggregator
	ForAction(rules.RuleAction) FlowLogAggregator
	FeedUpdate(MetricUpdate) error
	HasAggregationLevelChanged() bool
	GetCurrentAggregationLevel() FlowAggregationKind
	GetDefaultAggregationLevel() FlowAggregationKind
	AdjustLevel(newLevel FlowAggregationKind)
}

type LogDispatcher interface {
	Initialize() error
	Dispatch(logSlice interface{}) error
}

type aggregatorRef struct {
	a FlowLogAggregator
	d []LogDispatcher
}

var FlowLogAvg *FlowLogAverage
var flushIntervalDuration float64

type FlowLogAverage struct {
	totalFlows     int
	lastReportTime time.Time
}

// FlowLogsReporter implements the MetricsReporter interface.
type FlowLogsReporter struct {
	dispatchers   map[string]LogDispatcher
	aggregators   []aggregatorRef
	flushInterval time.Duration
	flushTicker   jitter.JitterTicker
	hepEnabled    bool

	healthAggregator *health.HealthAggregator
	logOffset        LogOffset

	// Allow the time function to be mocked for test purposes.
	timeNowFn func() time.Duration
}

const (
	healthName     = "cloud_watch_reporter"
	healthInterval = 10 * time.Second
)

func (f *FlowLogAverage) updateFlowLogs(numFlows int) {
	f.totalFlows += numFlows
}

func (f *FlowLogAverage) ResetFlowLogs() {
	FlowLogAvg = &FlowLogAverage{
		totalFlows:     0,
		lastReportTime: time.Now(),
	}
}

func GetAndResetFlowsPerMinute() (flowsPerMinute float64) {
	if FlowLogAvg != nil && FlowLogAvg.totalFlows != 0 {
		currentTime := time.Now()
		elapsedTime := currentTime.Sub(FlowLogAvg.lastReportTime)

		if elapsedTime.Seconds() < flushIntervalDuration {
			return
		}

		flowsPerMinute = float64(FlowLogAvg.totalFlows) / elapsedTime.Minutes()
		FlowLogAvg.ResetFlowLogs()
	}
	return
}

// NewFlowLogsReporter constructs a FlowLogs MetricsReporter using
// a dispatcher and aggregator.
func NewFlowLogsReporter(dispatchers map[string]LogDispatcher, flushInterval time.Duration, healthAggregator *health.HealthAggregator, hepEnabled bool, logOffset LogOffset) *FlowLogsReporter {
	if healthAggregator != nil {
		healthAggregator.RegisterReporter(healthName, &health.HealthReport{Live: true, Ready: true}, healthInterval*2)
	}

	// Initialize FlowLogAverage struct
	FlowLogAvg.ResetFlowLogs()
	flushIntervalDuration = flushInterval.Seconds()

	return &FlowLogsReporter{
		dispatchers:      dispatchers,
		flushTicker:      jitter.NewTicker(flushInterval, flushInterval/10),
		flushInterval:    flushInterval,
		timeNowFn:        monotime.Now,
		healthAggregator: healthAggregator,
		hepEnabled:       hepEnabled,
		logOffset:        logOffset,
	}
}

func newFlowLogsReporterTest(dispatchers map[string]LogDispatcher, healthAggregator *health.HealthAggregator, hepEnabled bool, flushTicker jitter.JitterTicker, logOffset LogOffset) *FlowLogsReporter {
	if healthAggregator != nil {
		healthAggregator.RegisterReporter(healthName, &health.HealthReport{Live: true, Ready: true}, healthInterval*2)
	}

	// Initialize FlowLogAverage struct
	FlowLogAvg.ResetFlowLogs()

	return &FlowLogsReporter{
		dispatchers:      dispatchers,
		flushTicker:      flushTicker,
		flushInterval:    time.Millisecond,
		timeNowFn:        monotime.Now,
		healthAggregator: healthAggregator,
		hepEnabled:       hepEnabled,
		logOffset:        logOffset,
	}
}

func (c *FlowLogsReporter) AddAggregator(agg FlowLogAggregator, dispatchers []string) {
	var ref aggregatorRef
	ref.a = agg
	for _, d := range dispatchers {
		dis, ok := c.dispatchers[d]
		if !ok {
			// This is a code error and is unrecoverable.
			log.Panic(fmt.Sprintf("unknown dispatcher \"%s\"", d))
		}
		ref.d = append(ref.d, dis)
	}
	c.aggregators = append(c.aggregators, ref)
}

func (c *FlowLogsReporter) Start() {
	log.Info("Starting FlowLogReporter")
	go c.run()
}

func (c *FlowLogsReporter) Report(mu MetricUpdate) error {
	log.Debug("Flow Logs Report got Metric Update")
	if !c.hepEnabled {
		if mu.srcEp != nil && mu.srcEp.IsHostEndpoint() {
			mu.srcEp = nil
		}
		if mu.dstEp != nil && mu.dstEp.IsHostEndpoint() {
			mu.dstEp = nil
		}
	}
	for _, agg := range c.aggregators {
		agg.a.FeedUpdate(mu)
	}
	return nil
}

func (c *FlowLogsReporter) run() {
	healthTicks := time.NewTicker(healthInterval)
	defer healthTicks.Stop()
	c.reportHealth()
	for {
		// TODO(doublek): Stop and flush cases.
		select {
		case <-c.flushTicker.Done():
			log.Debugf("Stopping flush ticker")
			healthTicks.Stop()
			return
		case <-c.flushTicker.Channel():
			// Fetch from different aggregators and then dispatch them to wherever
			// the flow logs need to end up.
			log.Debug("Flow log flush tick")
			var offsets = c.logOffset.Read()
			var isBehind = c.logOffset.IsBehind(offsets)
			var factor = c.logOffset.GetIncreaseFactor(offsets)

			for _, agg := range c.aggregators {
				// Evaluate if the external pipeline is stalled
				// and increase / decrease the aggregation level if needed
				newLevel := c.estimateLevel(agg, FlowAggregationKind(factor), isBehind)

				// Retrieve values from cache and calibrate the cache to the new aggregation level
				fl := agg.a.GetAndCalibrate(newLevel)
				FlowLogAvg.updateFlowLogs(len(fl))
				if len(fl) > 0 {
					// Dispatch logs
					for _, d := range agg.d {
						log.WithFields(log.Fields{
							"size":       len(fl),
							"dispatcher": d,
						}).Debug("Dispatching log buffer")
						d.Dispatch(fl)
					}
				}
			}
		case <-healthTicks.C:
			// Periodically report current health.
			c.reportHealth()
		}
	}
}

func (c *FlowLogsReporter) estimateLevel(agg aggregatorRef, factor FlowAggregationKind, isBehind bool) FlowAggregationKind {
	log.Debugf("Evaluate aggregation level. Logs are marked as behind = %v for level %v", isBehind, agg.a.GetCurrentAggregationLevel())

	var newLevel = agg.a.GetCurrentAggregationLevel()
	if isBehind {
		newLevel = agg.a.GetCurrentAggregationLevel() + factor
	} else if agg.a.HasAggregationLevelChanged() {
		newLevel = agg.a.GetDefaultAggregationLevel()
	}
	log.Debugf("Estimate aggregation level to %d", newLevel)
	return newLevel
}

func (c *FlowLogsReporter) canPublishFlowLogs() bool {
	for name, d := range c.dispatchers {
		err := d.Initialize()
		if err != nil {
			log.WithError(err).
				WithField("name", name).
				Error("dispatcher unable to initialize")
			return false
		}
	}
	return true
}

func (c *FlowLogsReporter) reportHealth() {
	readiness := c.canPublishFlowLogs()
	if c.healthAggregator != nil {
		c.healthAggregator.Report(healthName, &health.HealthReport{
			Live:  true,
			Ready: readiness,
		})
	}
}
