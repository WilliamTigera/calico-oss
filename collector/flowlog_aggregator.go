// Copyright (c) 2018-2020 Tigera, Inc. All rights reserved.

package collector

import (
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/felix/rules"
)

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

// flowLogAggregator builds and implements the FlowLogAggregator and
// FlowLogGetter interfaces.
// The flowLogAggregator is responsible for creating, aggregating, and storing
// aggregated flow logs until the flow logs are exported.
type flowLogAggregator struct {
	current              FlowAggregationKind
	previous             FlowAggregationKind
	initial              FlowAggregationKind
	flowStore            map[FlowMeta]flowEntry
	flMutex              sync.RWMutex
	includeLabels        bool
	includePolicies      bool
	maxOriginalIPsSize   int
	aggregationStartTime time.Time
	handledAction        rules.RuleAction
}

type flowEntry struct {
	spec         FlowSpec
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
		c.previous = c.current
		c.current = newLevel
		log.Debugf("New aggregation level for %v is set to %d from %d", c.handledAction, c.current, c.previous)
	}
}

// NewFlowLogAggregator constructs a FlowLogAggregator
func NewFlowLogAggregator() FlowLogAggregator {
	return &flowLogAggregator{
		current:              FlowDefault,
		initial:              FlowDefault,
		flowStore:            make(map[FlowMeta]flowEntry),
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

func (c *flowLogAggregator) IncludeLabels(b bool) FlowLogAggregator {
	c.includeLabels = b
	return c
}

func (c *flowLogAggregator) IncludePolicies(b bool) FlowLogAggregator {
	c.includePolicies = b
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

// FeedUpdate constructs and aggregates flow logs from MetricUpdates.
func (c *flowLogAggregator) FeedUpdate(mu MetricUpdate) error {
	lastRuleID := mu.GetLastRuleID()
	if lastRuleID == nil {
		log.WithField("metric update", mu).Error("no last rule id present")
		return fmt.Errorf("Invalid metric update")
	}
	// Filter out any action that we aren't configured to handle.
	if c.handledAction != noRuleActionDefined && c.handledAction != lastRuleID.Action {
		log.Debugf("Update %v not handled", mu)
		return nil
	}

	log.WithField("update", mu).Debug("Flow Log Aggregator got Metric Update")
	flowMeta, err := NewFlowMeta(mu, c.current)
	if err != nil {
		return err
	}
	c.flMutex.Lock()
	defer c.flMutex.Unlock()
	fl, ok := c.flowStore[flowMeta]
	if !ok {
		fl = flowEntry{spec: NewFlowSpec(mu, c.maxOriginalIPsSize), aggregation: c.current, shouldExport: true}
		for flowMeta, flowEntry := range c.flowStore {
			//TODO: Instead of iterating through all the entries, we should store the reverse mappings
			if !flowEntry.shouldExport && flowEntry.spec.flowsRefsActive.Contains(mu.tuple) {
				fl.spec.mergeWith(flowEntry.spec)
				delete(c.flowStore, flowMeta)
			}
		}
	} else {
		fl.spec.aggregateMetricUpdate(mu)
		fl.shouldExport = true
	}
	c.flowStore[flowMeta] = fl

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
func (c *flowLogAggregator) GetAndCalibrate(newLevel FlowAggregationKind) []*FlowLog {
	log.Debug("Get from flow log aggregator")
	resp := make([]*FlowLog, 0, len(c.flowStore))
	aggregationEndTime := time.Now()
	c.flMutex.Lock()
	defer c.flMutex.Unlock()

	c.AdjustLevel(newLevel)

	for flowMeta, flowSpecs := range c.flowStore {
		if flowSpecs.shouldExport {
			flowLog := FlowData{flowMeta, flowSpecs.spec}.ToFlowLog(c.aggregationStartTime, aggregationEndTime, c.includeLabels, c.includePolicies)
			resp = append(resp, &flowLog)
		}
		c.calibrateFlowStore(flowMeta, c.current)
	}
	c.aggregationStartTime = aggregationEndTime

	return resp
}

func (c *flowLogAggregator) calibrateFlowStore(flowMeta FlowMeta, newLevel FlowAggregationKind) {
	// discontinue tracking the stats associated with the
	// flow meta if no more associated 5-tuples exist.
	if c.flowStore[flowMeta].spec.getActiveFlowsCount() == 0 {
		log.Debugf("Deleting %v", flowMeta)
		delete(c.flowStore, flowMeta)
		return
	}

	exp := c.flowStore[flowMeta]

	if exp.shouldExport == false {
		return
	}

	if exp.aggregation != newLevel {
		log.Debugf("Marking entry as not exportable")
		exp.shouldExport = false
	}

	log.Debugf("Resetting %v", flowMeta)
	// reset flow stats for the next interval
	c.flowStore[flowMeta] = flowEntry{exp.spec.reset(), exp.aggregation, exp.shouldExport}
}
