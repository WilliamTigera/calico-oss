// Copyright (c) 2018-2021 Tigera, Inc. All rights reserved.

package collector

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/felix/rules"
)

type FlowLogGetter interface {
	GetAndCalibrate(newLevel FlowAggregationKind) []*FlowLog
}

type FlowLogAggregator interface {
	FlowLogGetter
	IncludeLabels(bool) FlowLogAggregator
	IncludePolicies(bool) FlowLogAggregator
	IncludeService(bool) FlowLogAggregator
	IncludeProcess(bool) FlowLogAggregator
	IncludeTcpStats(bool) FlowLogAggregator
	MaxOriginalIPsSize(int) FlowLogAggregator
	AggregateOver(FlowAggregationKind) FlowLogAggregator
	ForAction(rules.RuleAction) FlowLogAggregator
	PerFlowProcessLimit(limit int) FlowLogAggregator
	PerFlowProcessArgsLimit(limit int) FlowLogAggregator
	FeedUpdate(*MetricUpdate) error
	HasAggregationLevelChanged() bool
	GetCurrentAggregationLevel() FlowAggregationKind
	GetDefaultAggregationLevel() FlowAggregationKind
	AdjustLevel(newLevel FlowAggregationKind)
}

// FlowAggregationKind determines the flow log key
type FlowAggregationKind int

const (
	// FlowDefault is based on purely duration.
	FlowDefault FlowAggregationKind = iota
	// FlowSourcePort accumulates tuples with everything same but the source port
	FlowSourcePort
	// FlowPrefixName accumulates tuples with everything same but the prefix name
	FlowPrefixName
	// FlowNoDestPorts accumulates tuples with everything same but the prefix name, source ports and destination ports
	FlowNoDestPorts
)

const MaxAggregationLevel = FlowNoDestPorts
const MinAggregationLevel = FlowDefault

const (
	noRuleActionDefined  = 0
	defaultMaxOrigIPSize = 50
)

var (
	gaugeFlowStoreCacheSizeLength = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "felix_collector_allowed_flowlog_aggregator_store",
		Help: "Total number of FlowEntries with action=allow currently residing in the FlowStore cache used by the aggregator.",
	},
		[]string{"action"})
)

func init() {
	prometheus.MustRegister(gaugeFlowStoreCacheSizeLength)
}

// flowLogAggregator builds and implements the FlowLogAggregator and
// FlowLogGetter interfaces.
// The flowLogAggregator is responsible for creating, aggregating, and storing
// aggregated flow logs until the flow logs are exported.
type flowLogAggregator struct {
	current                 FlowAggregationKind
	previous                FlowAggregationKind
	initial                 FlowAggregationKind
	flowStore               map[FlowMeta]*flowEntry
	flMutex                 sync.RWMutex
	includeLabels           bool
	includePolicies         bool
	includeService          bool
	includeProcess          bool
	maxOriginalIPsSize      int
	aggregationStartTime    time.Time
	handledAction           rules.RuleAction
	perFlowProcessLimit     int
	includeTcpStats         bool
	perFlowProcessArgsLimit int
}

type flowEntry struct {
	spec         *FlowSpec
	aggregation  FlowAggregationKind
	shouldExport bool
}

func (c *flowLogAggregator) GetCurrentAggregationLevel() FlowAggregationKind {
	return c.current
}

func (c *flowLogAggregator) GetDefaultAggregationLevel() FlowAggregationKind {
	return c.initial
}

func (c *flowLogAggregator) HasAggregationLevelChanged() bool {
	return c.current != c.previous
}

func (c *flowLogAggregator) AdjustLevel(newLevel FlowAggregationKind) {
	if c.current != newLevel {
		var value = newLevel
		if newLevel > MaxAggregationLevel {
			value = MaxAggregationLevel
		}

		if newLevel < MinAggregationLevel {
			value = MinAggregationLevel
		}
		c.previous = c.current
		c.current = value
		log.Debugf("New aggregation level for %v is set to %d from %d", c.handledAction, c.current, c.previous)
	}
}

// NewFlowLogAggregator constructs a FlowLogAggregator
func NewFlowLogAggregator() FlowLogAggregator {
	return &flowLogAggregator{
		current:              FlowDefault,
		initial:              FlowDefault,
		flowStore:            make(map[FlowMeta]*flowEntry),
		flMutex:              sync.RWMutex{},
		maxOriginalIPsSize:   defaultMaxOrigIPSize,
		aggregationStartTime: time.Now(),
	}
}

func (c *flowLogAggregator) AggregateOver(kind FlowAggregationKind) FlowLogAggregator {
	c.initial = kind
	c.current = kind
	c.previous = kind
	return c
}
func (c *flowLogAggregator) IncludeTcpStats(b bool) FlowLogAggregator {
	c.includeTcpStats = b
	return c
}

func (c *flowLogAggregator) IncludeLabels(b bool) FlowLogAggregator {
	c.includeLabels = b
	return c
}

func (c *flowLogAggregator) IncludePolicies(b bool) FlowLogAggregator {
	c.includePolicies = b
	return c
}

func (c *flowLogAggregator) IncludeService(b bool) FlowLogAggregator {
	c.includeService = b
	return c
}

func (c *flowLogAggregator) IncludeProcess(b bool) FlowLogAggregator {
	c.includeProcess = b
	return c
}

func (c *flowLogAggregator) MaxOriginalIPsSize(s int) FlowLogAggregator {
	c.maxOriginalIPsSize = s
	return c
}

func (c *flowLogAggregator) ForAction(ra rules.RuleAction) FlowLogAggregator {
	c.handledAction = ra
	return c
}

func (c *flowLogAggregator) PerFlowProcessLimit(n int) FlowLogAggregator {
	c.perFlowProcessLimit = n
	return c
}

func (c *flowLogAggregator) PerFlowProcessArgsLimit(n int) FlowLogAggregator {
	c.perFlowProcessArgsLimit = n
	return c
}

// FeedUpdate constructs and aggregates flow logs from MetricUpdates.
func (fa *flowLogAggregator) FeedUpdate(mu *MetricUpdate) error {
	defer fa.reportFlowLogStoreMetrics()

	lastRuleID := mu.GetLastRuleID()
	if lastRuleID == nil {
		log.WithField("metric update", mu).Error("no last rule id present")
		return fmt.Errorf("invalid metric update")
	}
	// Filter out any action that we aren't configured to handle.
	if fa.handledAction != noRuleActionDefined && fa.handledAction != lastRuleID.Action {
		log.Debugf("Update %v not handled", *mu)
		return nil
	}

	flowMeta, err := NewFlowMeta(*mu, fa.current, fa.includeService)
	if err != nil {
		return err
	}

	fa.flMutex.Lock()
	defer fa.flMutex.Unlock()

	log.Debugf("Flow Log Aggregator got Metric Update: %+v", *mu)

	fl, ok := fa.flowStore[flowMeta]

	if !ok {
		log.Debugf("flowMeta %+v not found, creating new flowspec for metric update %+v", flowMeta, *mu)
		spec := NewFlowSpec(mu, fa.maxOriginalIPsSize, fa.includeProcess, fa.perFlowProcessLimit, fa.perFlowProcessArgsLimit)

		newEntry := &flowEntry{
			spec:         spec,
			aggregation:  fa.current,
			shouldExport: true,
		}
		if fa.HasAggregationLevelChanged() {
			for flowMeta, flowEntry := range fa.flowStore {
				//TODO: Instead of iterating through all the entries, we should store the reverse mappings
				if !flowEntry.shouldExport && flowEntry.spec.ContainsActiveRefs(mu) {
					newEntry.spec.MergeWith(*mu, flowEntry.spec)
					delete(fa.flowStore, flowMeta)
				}
			}
		}

		fa.flowStore[flowMeta] = newEntry
	} else {
		log.Debugf("flowMeta %+v found, aggregating flowspec with metric update %+v", flowMeta, *mu)

		fl.spec.AggregateMetricUpdate(mu)
		fl.shouldExport = true
		fa.flowStore[flowMeta] = fl
	}

	return nil
}

// GetAndCalibrate returns all aggregated flow logs, as a list of string pointers, since the last time a GetAndCalibrate
// was called. Calling GetAndCalibrate will also clear the stored flow logs once the flow logs are returned.
// Clearing the stored flow logs may imply resetting the statistics for a flow log identified using
// its FlowMeta or flushing out the entry of FlowMeta altogether. If no active flow count are recorded
// a flush operation will be applied instead of a reset. In addition to this, a new level of aggregation will
// be set. By changing aggregation levels, all previous entries with a different level will be marked accordingly as not
// be exported at the next call for GetAndCalibrate().They will be kept in the store flow in order to provide an
// accurate number for numFlowCounts.
func (fa *flowLogAggregator) GetAndCalibrate(newLevel FlowAggregationKind) []*FlowLog {
	log.Debug("Get from flow log aggregator")
	aggregationEndTime := time.Now()

	fa.flMutex.Lock()
	defer fa.flMutex.Unlock()

	resp := make([]*FlowLog, 0, len(fa.flowStore))
	fa.AdjustLevel(newLevel)

	for flowMeta, flowEntry := range fa.flowStore {
		if flowEntry.shouldExport {
			log.Debug("Converting to flowlogs")
			flowLogs := flowEntry.spec.ToFlowLogs(flowMeta, fa.aggregationStartTime, aggregationEndTime, fa.includeLabels, fa.includePolicies)
			resp = append(resp, flowLogs...)
		}
		fa.calibrateFlowStore(flowMeta, fa.current)
	}

	fa.aggregationStartTime = aggregationEndTime

	return resp
}

func (fa *flowLogAggregator) calibrateFlowStore(flowMeta FlowMeta, newLevel FlowAggregationKind) {
	defer fa.reportFlowLogStoreMetrics()
	entry, ok := fa.flowStore[flowMeta]
	if !ok {
		// This should never happen as calibrateFlowStore is called right after we
		// generate flow logs using the entry.
		log.Warnf("Missing entry for flowMeta %+v", flowMeta)
		return
	}

	// Some specs might contain process names with no active flows. We garbage collect
	// them here so that if there are no other processes tracked, the flow meta can
	// be removed.
	remainingActiveFlowsCount := entry.spec.GarbageCollect()

	// discontinue tracking the stats associated with the
	// flow meta if no more associated 5-tuples exist.
	if remainingActiveFlowsCount == 0 {
		log.Debugf("Deleting %v", flowMeta)
		delete(fa.flowStore, flowMeta)

		return
	}

	if !entry.shouldExport {
		return
	}

	if entry.aggregation != newLevel {
		log.Debugf("Marking entry as not exportable")
		entry.shouldExport = false
	}

	log.Debugf("Resetting %v", flowMeta)

	// reset flow stats for the next interval
	entry.spec.Reset()

	fa.flowStore[flowMeta] = &flowEntry{
		spec:         entry.spec,
		aggregation:  entry.aggregation,
		shouldExport: entry.shouldExport,
	}
}

// reportFlowLogStoreMetrics reporting of current FlowStore cache metrics to Prometheus
func (fa *flowLogAggregator) reportFlowLogStoreMetrics() {
	gaugeFlowStoreCacheSizeLength.WithLabelValues(strings.ToLower(fa.handledAction.String())).Set(float64(len(fa.flowStore)))
}
