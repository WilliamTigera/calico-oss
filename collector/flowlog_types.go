// Copyright (c) 2018 Tigera, Inc. All rights reserved.

package collector

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	unsetIntField = -1
)

type FlowLogEndpointType string
type FlowLogAction string
type FlowLogReporter string
type FlowLogSubnetType string

type EndpointMetadata struct {
	Type      FlowLogEndpointType `json:"type"`
	Namespace string              `json:"namespace"`
	Name      string              `json:"name"`
}

type FlowMeta struct {
	Tuple    Tuple            `json:"tuple"`
	SrcMeta  EndpointMetadata `json:"sourceMeta"`
	DstMeta  EndpointMetadata `json:"destinationMeta"`
	Action   FlowLogAction    `json:"action"`
	Reporter FlowLogReporter  `json:"flowReporter"`
}

func newFlowMeta(mu MetricUpdate) (FlowMeta, error) {
	f := FlowMeta{}

	// Extract Tuple Info
	f.Tuple = mu.tuple

	// Extract EndpointMetadata info
	var (
		srcMeta, dstMeta EndpointMetadata
		err              error
	)
	if mu.srcEp != nil {
		srcMeta, err = getFlowLogEndpointMetadata(mu.srcEp)
		if err != nil {
			return FlowMeta{}, fmt.Errorf("Could not extract metadata for source %v", mu.srcEp)
		}
	} else {
		srcMeta = EndpointMetadata{Type: FlowLogEndpointTypeNet,
			Namespace: flowLogFieldNotIncluded,
			Name:      string(getSubnetType(mu.tuple.src)),
		}
	}
	if mu.dstEp != nil {
		dstMeta, err = getFlowLogEndpointMetadata(mu.dstEp)
		if err != nil {
			return FlowMeta{}, fmt.Errorf("Could not extract metadata for destination %v", mu.dstEp)
		}
	} else {
		dstMeta = EndpointMetadata{Type: FlowLogEndpointTypeNet,
			Namespace: flowLogFieldNotIncluded,
			Name:      string(getSubnetType(mu.tuple.dst)),
		}
	}

	f.SrcMeta = srcMeta
	f.DstMeta = dstMeta

	lastRuleID := mu.GetLastRuleID()
	if lastRuleID == nil {
		log.WithField("metric update", mu).Error("no rule id present")
		return f, fmt.Errorf("Invalid metric update")
	}

	action, direction := getFlowLogActionAndReporterFromRuleID(lastRuleID)
	f.Action = action
	f.Reporter = direction

	return f, nil
}

func newFlowMetaWithSourcePortAggregation(mu MetricUpdate) (FlowMeta, error) {
	f, err := newFlowMeta(mu)
	if err != nil {
		return FlowMeta{}, err
	}
	f.Tuple.l4Src = unsetIntField

	return f, nil
}

func newFlowMetaWithPrefixNameAggregation(mu MetricUpdate) (FlowMeta, error) {
	f, err := newFlowMeta(mu)
	if err != nil {
		return FlowMeta{}, err
	}

	f.Tuple.src = [16]byte{}
	f.Tuple.l4Src = unsetIntField
	f.Tuple.dst = [16]byte{}

	if mu.srcEp != nil {
		prefixName := getEndpointNamePrefix(mu.srcEp)
		if prefixName != "" {
			f.SrcMeta.Name = prefixName + "*"
		}
	}
	if mu.dstEp != nil {
		prefixName := getEndpointNamePrefix(mu.dstEp)
		if prefixName != "" {
			f.DstMeta.Name = prefixName + "*"
		}
	}

	return f, nil
}

func NewFlowMeta(mu MetricUpdate, kind AggregationKind) (FlowMeta, error) {
	switch kind {
	case Default:
		return newFlowMeta(mu)
	case SourcePort:
		return newFlowMetaWithSourcePortAggregation(mu)
	case PrefixName:
		return newFlowMetaWithPrefixNameAggregation(mu)
	}

	return FlowMeta{}, fmt.Errorf("aggregation kind %v not recognized", kind)
}

type FlowSpec struct {
	FlowLabels
	FlowStats
}

func NewFlowSpec(mu MetricUpdate) FlowSpec {
	return FlowSpec{
		FlowLabels: NewFlowLabels(mu),
		FlowStats:  NewFlowStats(mu),
	}
}

func (f *FlowSpec) aggregateMetricUpdate(mu MetricUpdate) {
	f.aggregateFlowLabels(mu)
	f.aggregateFlowStats(mu)
}

// FlowSpec has FlowStats that are stats assocated with a given FlowMeta
// These stats are to be refreshed everytime the FlowData
// {FlowMeta->FlowStats} is published so as to account
// for correct no. of started flows in a given aggregation
// interval.
func (f FlowSpec) reset() FlowSpec {
	f.flowsStartedRefs = NewTupleSet()
	f.flowsCompletedRefs = NewTupleSet()
	f.flowsRefs = f.flowsRefsActive.Copy()
	f.PacketsIn = 0
	f.BytesIn = 0
	f.PacketsOut = 0
	f.BytesOut = 0

	return f
}

type FlowLabels struct {
	SrcLabels map[string]string
	DstLabels map[string]string
}

func NewFlowLabels(mu MetricUpdate) FlowLabels {
	return FlowLabels{
		SrcLabels: getFlowLogEndpointLabels(mu.srcEp),
		DstLabels: getFlowLogEndpointLabels(mu.dstEp),
	}
}

func intersectLabels(in, out map[string]string) map[string]string {
	common := map[string]string{}
	for k := range out {
		if v, ok := in[k]; ok && v == out[k] {
			common[k] = v
		}
	}
	return common
}

func (f *FlowLabels) aggregateFlowLabels(mu MetricUpdate) {
	srcLabels := getFlowLogEndpointLabels(mu.srcEp)
	dstLabels := getFlowLogEndpointLabels(mu.dstEp)

	f.SrcLabels = intersectLabels(srcLabels, f.SrcLabels)
	f.DstLabels = intersectLabels(dstLabels, f.DstLabels)
}

// FlowStats captures stats associated with a given FlowMeta
type FlowStats struct {
	FlowReportedStats
	flowReferences
}

// FlowReportedStats are the statistics we actually report out in flow logs.
type FlowReportedStats struct {
	PacketsIn         int `json:"packetsIn"`
	PacketsOut        int `json:"packetsOut"`
	BytesIn           int `json:"bytesIn"`
	BytesOut          int `json:"bytesOut"`
	NumFlows          int `json:"numFlows"`
	NumFlowsStarted   int `json:"numFlowsStarted"`
	NumFlowsCompleted int `json:"numFlowsCompleted"`
}

// flowReferences are internal only stats used for computing numbers of flows
type flowReferences struct {
	flowsRefs          tupleSet
	flowsStartedRefs   tupleSet
	flowsCompletedRefs tupleSet
	flowsRefsActive    tupleSet
}

func NewFlowStats(mu MetricUpdate) FlowStats {
	flowsRefs := NewTupleSet()
	flowsRefs.Add(mu.tuple)
	flowsStartedRefs := NewTupleSet()
	flowsCompletedRefs := NewTupleSet()
	flowsRefsActive := NewTupleSet()

	switch mu.updateType {
	case UpdateTypeReport:
		flowsStartedRefs.Add(mu.tuple)
		flowsRefsActive.Add(mu.tuple)
	case UpdateTypeExpire:
		flowsCompletedRefs.Add(mu.tuple)
	}

	return FlowStats{
		FlowReportedStats: FlowReportedStats{
			NumFlows:          flowsRefs.Len(),
			NumFlowsStarted:   flowsStartedRefs.Len(),
			NumFlowsCompleted: flowsCompletedRefs.Len(),
			PacketsIn:         mu.inMetric.deltaPackets,
			BytesIn:           mu.inMetric.deltaBytes,
			PacketsOut:        mu.outMetric.deltaPackets,
			BytesOut:          mu.outMetric.deltaBytes,
		},
		flowReferences: flowReferences{
			// flowsRefs track the flows that were tracked
			// in the give interval
			flowsRefs:          flowsRefs,
			flowsStartedRefs:   flowsStartedRefs,
			flowsCompletedRefs: flowsCompletedRefs,
			// flowsRefsActive tracks the active (non-completed)
			// flows associated with the flowMeta
			flowsRefsActive: flowsRefsActive,
		},
	}
}

func (f *FlowStats) aggregateFlowStats(mu MetricUpdate) {
	// TODO(doublek): Handle metadata updates.
	switch {
	case mu.updateType == UpdateTypeReport && !f.flowsRefsActive.Contains(mu.tuple):
		f.flowsStartedRefs.Add(mu.tuple)
		f.flowsRefsActive.Add(mu.tuple)
	case mu.updateType == UpdateTypeExpire:
		f.flowsCompletedRefs.Add(mu.tuple)
		f.flowsRefsActive.Discard(mu.tuple)
	}

	// If this is the first time we are seeing this tuple.
	if !f.flowsRefs.Contains(mu.tuple) || (mu.updateType == UpdateTypeReport && f.flowsCompletedRefs.Contains(mu.tuple)) {
		f.flowsRefs.Add(mu.tuple)
	}

	f.NumFlows = f.flowsRefs.Len()
	f.NumFlowsStarted = f.flowsStartedRefs.Len()
	f.NumFlowsCompleted = f.flowsCompletedRefs.Len()
	f.PacketsIn += mu.inMetric.deltaPackets
	f.BytesIn += mu.inMetric.deltaBytes
	f.PacketsOut += mu.outMetric.deltaPackets
	f.BytesOut += mu.outMetric.deltaBytes
}

func (f FlowStats) getActiveFlowsCount() int {
	return len(f.flowsRefsActive)
}

// FlowData is metadata and stats about a flow (or aggregated group of flows).
// This is an internal structure for book keeping; FlowLog is what actually gets
// passed to dispatchers or serialized.
type FlowData struct {
	FlowMeta
	FlowSpec
}

// FlowLog is a record of flow data (metadata & reported stats) including
// timestamps. A FlowLog is ready to be serialized to an output format.
type FlowLog struct {
	StartTime, EndTime time.Time
	FlowMeta
	FlowLabels
	FlowReportedStats
}

// ToFlowLog converts a FlowData to a FlowLog
func (f FlowData) ToFlowLog(startTime, endTime time.Time, includeLabels bool) FlowLog {
	var fl FlowLog
	fl.FlowMeta = f.FlowMeta
	fl.FlowReportedStats = f.FlowReportedStats
	fl.StartTime = startTime
	fl.EndTime = endTime

	if includeLabels {
		fl.SrcLabels = f.SrcLabels
		fl.DstLabels = f.DstLabels
	}

	return fl
}

func (f *FlowLog) Deserialize(fl string) error {
	// Format is
	// startTime endTime srcType srcNamespace srcName srcLabels dstType dstNamespace dstName dstLabels srcIP dstIP proto srcPort dstPort numFlows numFlowsStarted numFlowsCompleted flowReporter packetsIn packetsOut bytesIn bytesOut action
	// Sample entry with no aggregation and no labels.
	// 1529529591 1529529892 wep policy-demo nginx-7d98456675-2mcs4 - wep kube-system kube-dns-7cc87d595-pxvxb - 192.168.224.225 192.168.135.53 17 36486 53 1 1 1 in 1 1 73 119 allow

	var (
		srcType, dstType FlowLogEndpointType
	)

	parts := strings.Split(fl, " ")
	if len(parts) < 24 {
		return fmt.Errorf("log %v cant be processed", fl)
	}

	switch parts[2] {
	case "wep":
		srcType = FlowLogEndpointTypeWep
	case "hep":
		srcType = FlowLogEndpointTypeHep
	case "ns":
		srcType = FlowLogEndpointTypeNs
	case "net":
		srcType = FlowLogEndpointTypeNet
	}

	f.SrcMeta = EndpointMetadata{
		Type:      srcType,
		Namespace: parts[3],
		Name:      parts[4],
	}

	srcLabels := map[string]string{}
	if parts[5] != "-" {
		err := json.Unmarshal([]byte(parts[5]), &srcLabels)
		if err != nil {
			return fmt.Errorf("Failed parsing source labels. %f", err)
		}
	}
	f.SrcLabels = srcLabels

	switch parts[6] {
	case "wep":
		dstType = FlowLogEndpointTypeWep
	case "hep":
		dstType = FlowLogEndpointTypeHep
	case "ns":
		dstType = FlowLogEndpointTypeNs
	case "net":
		dstType = FlowLogEndpointTypeNet
	}

	f.DstMeta = EndpointMetadata{
		Type:      dstType,
		Namespace: parts[7],
		Name:      parts[8],
	}

	dstLabels := map[string]string{}
	if parts[9] != "-" {
		err := json.Unmarshal([]byte(parts[9]), &dstLabels)
		if err != nil {
			return fmt.Errorf("Failed parsing destination labels. %f", err)
		}
	}
	f.DstLabels = dstLabels

	var sip, dip [16]byte
	if parts[10] != "-" {
		sip = ipStrTo16Byte(parts[10])
	}
	if parts[11] != "-" {
		dip = ipStrTo16Byte(parts[11])
	}
	p, _ := strconv.Atoi(parts[12])
	sp, _ := strconv.Atoi(parts[13])
	dp, _ := strconv.Atoi(parts[14])
	f.Tuple = *NewTuple(sip, dip, p, sp, dp)

	f.NumFlows, _ = strconv.Atoi(parts[15])
	f.NumFlowsStarted, _ = strconv.Atoi(parts[16])
	f.NumFlowsCompleted, _ = strconv.Atoi(parts[17])

	switch parts[18] {
	case "src":
		f.Reporter = FlowLogReporterSrc
	case "dst":
		f.Reporter = FlowLogReporterDst
	}

	f.PacketsIn, _ = strconv.Atoi(parts[19])
	f.PacketsOut, _ = strconv.Atoi(parts[20])
	f.BytesIn, _ = strconv.Atoi(parts[21])
	f.BytesOut, _ = strconv.Atoi(parts[22])

	switch parts[23] {
	case "allow":
		f.Action = FlowLogActionAllow
	case "deny":
		f.Action = FlowLogActionDeny
	}

	return nil
}
