// Copyright (c) 2016-2018 Tigera, Inc. All rights reserved.

package collector

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/gavv/monotime"

	"github.com/projectcalico/felix/calc"
	"github.com/projectcalico/felix/rules"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
)

type TrafficDirection int

const (
	TrafficDirInbound TrafficDirection = iota
	TrafficDirOutbound
)

const (
	TrafficDirInboundStr  = "inbound"
	TrafficDirOutboundStr = "outbound"
)

func (t TrafficDirection) String() string {
	if t == TrafficDirInbound {
		return TrafficDirInboundStr
	}
	return TrafficDirOutboundStr
}

// ruleDirToTrafficDir converts the rule direction to the equivalent traffic direction
// (useful for NFLOG based updates where ingress/inbound and egress/outbound are
// tied).
func ruleDirToTrafficDir(r rules.RuleDir) TrafficDirection {
	if r == rules.RuleDirIngress {
		return TrafficDirInbound
	}
	return TrafficDirOutbound
}

const RuleTraceInitLen = 10

// RuleTrace represents the list of rules (i.e, a Trace) that a packet hits.
// The action of a RuleTrace object is the final action that is not a
// next-Tier/pass action. A RuleTrace also contains a endpoint that the rule
// trace applied to,
type RuleTrace struct {
	path   []*calc.RuleID
	action rules.RuleAction

	// Counters to store the packets and byte counts for the RuleTrace
	pktsCtr  Counter
	bytesCtr Counter
	dirty    bool

	// Stores the Index of the RuleID that has a RuleAction Allow or Deny
	verdictIdx int

	// Optimization Note: When initializing a RuleTrace object, the pathArray
	// array is used as the backing array that has been pre-allocated. This avoids
	// one allocation when creating a RuleTrace object. More info on this here:
	// https://github.com/golang/go/wiki/Performance#memory-profiler
	// (Search for "slice array preallocation").
	pathArray [RuleTraceInitLen]*calc.RuleID
}

func NewRuleTrace() RuleTrace {
	rt := RuleTrace{verdictIdx: -1}
	rt.path = rt.pathArray[:]
	return rt
}

func (t *RuleTrace) String() string {
	rtParts := make([]string, 0)
	for _, tp := range t.path {
		if tp == nil {
			continue
		}
		rtParts = append(rtParts, fmt.Sprintf("(%s)", tp))
	}
	return fmt.Sprintf(
		"path=[%v], action=%v ctr={packets=%v bytes=%v}",
		strings.Join(rtParts, ", "), t.Action(), t.pktsCtr.Absolute(), t.bytesCtr.Absolute(),
	)
}

func (t *RuleTrace) Len() int {
	return len(t.path)
}

func (t *RuleTrace) Path() []*calc.RuleID {
	if t.verdictIdx >= 0 {
		return t.path[:t.verdictIdx+1]
	} else {
		return nil
	}
}

func (t *RuleTrace) ToString() string {
	ruleID := t.VerdictRuleID()
	if ruleID == nil {
		return ""
	}
	return fmt.Sprintf("%s/%s/%d/%v", ruleID.Tier, ruleID.Name, ruleID.Index, ruleID.Action)
}

func (t *RuleTrace) Action() rules.RuleAction {
	ruleID := t.VerdictRuleID()
	if ruleID == nil {
		// We don't know the verdict RuleID yet.
		return rules.RuleActionPass
	}
	return ruleID.Action
}

func (t *RuleTrace) IsDirty() bool {
	return t.dirty
}

// FoundVerdict returns true if the verdict rule has been found, that is the rule that contains
// the final allow or deny action.
func (t *RuleTrace) FoundVerdict() bool {
	return t.verdictIdx >= 0
}

// VerdictRuleID returns the RuleID that contains either ActionAllow or
// DenyAction in a RuleTrace or nil if we haven't seen either of these yet.
func (t *RuleTrace) VerdictRuleID() *calc.RuleID {
	if t.verdictIdx >= 0 {
		return t.path[t.verdictIdx]
	} else {
		return nil
	}
}

func (t *RuleTrace) ClearDirtyFlag() {
	t.dirty = false
	t.pktsCtr.ResetDelta()
	t.bytesCtr.ResetDelta()
}

func (t *RuleTrace) addRuleID(rid *calc.RuleID, tierIdx, numPkts, numBytes int) bool {
	if tierIdx >= t.Len() {
		// Insertion Index is beyond than current length. Grow the path slice as long
		// as necessary.
		incSize := (tierIdx / RuleTraceInitLen) * RuleTraceInitLen
		newPath := make([]*calc.RuleID, t.Len()+incSize)
		copy(newPath, t.path)
		t.path = newPath
		t.path[tierIdx] = rid
	} else {
		existingRuleID := t.path[tierIdx]
		switch {
		case existingRuleID == nil:
			// Position is empty, insert and be done.
			t.path[tierIdx] = rid
		case !existingRuleID.Equals(rid):
			return false
		}
	}
	if rid.Action != rules.RuleActionPass {
		t.pktsCtr.Increase(numPkts)
		t.bytesCtr.Increase(numBytes)
		t.verdictIdx = tierIdx
	}
	t.dirty = true
	return true
}

func (t *RuleTrace) replaceRuleID(rid *calc.RuleID, tierIdx, numPkts, numBytes int) {
	if rid.Action == rules.RuleActionPass {
		t.path[tierIdx] = rid
		return
	}
	// New tracepoint is not a next-Tier action truncate at this Index.
	t.path[tierIdx] = rid
	t.pktsCtr = *NewCounter(numPkts)
	t.bytesCtr = *NewCounter(numBytes)
	t.dirty = true
	t.verdictIdx = tierIdx
}

// Tuple represents a 5-Tuple value that identifies a connection/flow of packets
// with an implicit notion of Direction that comes with the use of a source and
// destination. This is a hashable object and can be used as a map's key.
type Tuple struct {
	src   [16]byte
	dst   [16]byte
	proto int
	l4Src int
	l4Dst int
}

func NewTuple(src [16]byte, dst [16]byte, proto int, l4Src int, l4Dst int) *Tuple {
	t := &Tuple{
		src:   src,
		dst:   dst,
		proto: proto,
		l4Src: l4Src,
		l4Dst: l4Dst,
	}
	return t
}

func (t *Tuple) String() string {
	return fmt.Sprintf("src=%v dst=%v proto=%v sport=%v dport=%v", net.IP(t.src[:16]).String(), net.IP(t.dst[:16]).String(), t.proto, t.l4Src, t.l4Dst)
}

func (t *Tuple) GetSourcePort() int {
	return t.l4Src
}

func (t *Tuple) SetSourcePort(port int) {
	t.l4Src = port
}

func (t *Tuple) GetDestPort() int {
	return t.l4Dst
}

// Data contains metadata and statistics such as rule counters and age of a
// connection(Tuple). Each Data object contains:
// - 2 RuleTrace's - Ingress and Egress - each providing information on the
// where the Policy was applied, with additional information on corresponding
// workload endpoint. The EgressRuleTrace and the IngressRuleTrace record the
// policies that this tuple can hit - egress from the workload that is the
// source of this connection and ingress into a workload that terminated this.
// - Connection based counters (e.g, for conntrack packets/bytes and HTTP requests).
type Data struct {
	Tuple Tuple

	// Contains endpoint information corresponding to source and
	// destination endpoints. Either of these values can be nil
	// if we don't have information about the endpoint.
	srcEp *calc.EndpointData
	dstEp *calc.EndpointData

	// Indicates if this is a connection
	isConnection bool

	// Connection related counters.
	conntrackPktsCtr         Counter
	conntrackPktsCtrReverse  Counter
	conntrackBytesCtr        Counter
	conntrackBytesCtrReverse Counter
	httpReqAllowedCtr        Counter
	httpReqDeniedCtr         Counter

	// These contain the aggregated counts per tuple per rule.
	IngressRuleTrace RuleTrace
	EgressRuleTrace  RuleTrace

	createdAt  time.Duration
	updatedAt  time.Duration
	ageTimeout time.Duration
	dirty      bool
}

func NewData(tuple Tuple, duration time.Duration) *Data {
	now := monotime.Now()
	return &Data{
		Tuple:            tuple,
		IngressRuleTrace: NewRuleTrace(),
		EgressRuleTrace:  NewRuleTrace(),
		createdAt:        now,
		updatedAt:        now,
		ageTimeout:       duration,
		dirty:            true,
	}
}

func (d *Data) String() string {
	var srcName, dstName string
	if d.srcEp != nil {
		srcName = endpointName(d.srcEp.Key)
	} else {
		srcName = "<unknown>"
	}
	if d.dstEp != nil {
		dstName = endpointName(d.dstEp.Key)
	} else {
		dstName = "<unknown>"
	}
	return fmt.Sprintf(
		"tuple={%v}, srcEp={%v} dstEp={%v} connTrackCtr={packets=%v bytes=%v}, "+
			"connTrackCtrReverse={packets=%v bytes=%v}, updatedAt=%v ingressRuleTrace={%v} egressRuleTrace={%v}",
		&(d.Tuple), srcName, dstName, d.conntrackPktsCtr.Absolute(), d.conntrackBytesCtr.Absolute(),
		d.conntrackPktsCtrReverse.Absolute(), d.conntrackBytesCtrReverse.Absolute(), d.updatedAt, d.IngressRuleTrace, d.EgressRuleTrace)
}

func (d *Data) touch() {
	d.updatedAt = monotime.Now()
}

func (d *Data) setDirtyFlag() {
	d.dirty = true
}

func (d *Data) clearDirtyFlag() {
	d.dirty = false
	d.httpReqAllowedCtr.ResetDelta()
	d.httpReqDeniedCtr.ResetDelta()
	d.conntrackPktsCtr.ResetDelta()
	d.conntrackBytesCtr.ResetDelta()
	d.conntrackPktsCtrReverse.ResetDelta()
	d.conntrackBytesCtrReverse.ResetDelta()
	d.IngressRuleTrace.ClearDirtyFlag()
	d.EgressRuleTrace.ClearDirtyFlag()
}

func (d *Data) IsDirty() bool {
	return d.dirty
}

func (d *Data) CreatedAt() time.Duration {
	return d.createdAt
}

func (d *Data) UpdatedAt() time.Duration {
	return d.updatedAt
}

func (d *Data) DurationSinceLastUpdate() time.Duration {
	return monotime.Since(d.updatedAt)
}

func (d *Data) DurationSinceCreate() time.Duration {
	return monotime.Since(d.createdAt)
}

// Returns the final action of the RuleTrace
func (d *Data) IngressAction() rules.RuleAction {
	return d.IngressRuleTrace.Action()
}

// Returns the final action of the RuleTrace
func (d *Data) EgressAction() rules.RuleAction {
	return d.EgressRuleTrace.Action()
}

func (d *Data) ConntrackPacketsCounter() Counter {
	return d.conntrackPktsCtr
}

func (d *Data) ConntrackBytesCounter() Counter {
	return d.conntrackBytesCtr
}

func (d *Data) ConntrackPacketsCounterReverse() Counter {
	return d.conntrackPktsCtrReverse
}

func (d *Data) ConntrackBytesCounterReverse() Counter {
	return d.conntrackBytesCtrReverse
}

func (d *Data) HTTPRequestsAllowed() Counter {
	return d.httpReqAllowedCtr
}

func (d *Data) HTTPRequestsDenied() Counter {
	return d.httpReqDeniedCtr
}

// Set In Counters' values to packets and bytes. Use the SetConntrackCounters* methods
// when the source if packets/bytes are absolute values.
func (d *Data) SetConntrackCounters(packets int, bytes int) {
	if d.conntrackPktsCtr.Set(packets) && d.conntrackBytesCtr.Set(bytes) {
		d.setDirtyFlag()
	}
	d.isConnection = true
	d.touch()
}

// Set In Counters' values to packets and bytes. Use the SetConntrackCounters* methods
// when the source if packets/bytes are absolute values.
func (d *Data) SetConntrackCountersReverse(packets int, bytes int) {
	if d.conntrackPktsCtrReverse.Set(packets) && d.conntrackBytesCtrReverse.Set(bytes) {
		d.setDirtyFlag()
	}
	d.isConnection = true
	d.touch()
}

// Increment the HTTP Request allowed count.
func (d *Data) IncreaseHTTPRequestAllowedCounter(delta int) {
	if delta == 0 {
		return
	}
	d.httpReqAllowedCtr.Increase(delta)
	d.setDirtyFlag()
	d.touch()
}

// Increment the HTTP Request denied count.
func (d *Data) IncreaseHTTPRequestDeniedCounter(delta int) {
	if delta == 0 {
		return
	}
	d.httpReqDeniedCtr.Increase(delta)
	d.setDirtyFlag()
	d.touch()
}

// ResetConntrackCounters resets the counters associated with the tracked connection for
// the data.
func (d *Data) ResetConntrackCounters() {
	d.isConnection = false
	d.conntrackPktsCtr.Reset()
	d.conntrackBytesCtr.Reset()
	d.conntrackPktsCtrReverse.Reset()
	d.conntrackBytesCtrReverse.Reset()
}

// ResetApplicationCounters resets the counters associated with application layer statistics.
func (d *Data) ResetApplicationCounters() {
	d.httpReqAllowedCtr.Reset()
	d.httpReqDeniedCtr.Reset()
}

func (d *Data) SetSourceEndpointData(sep *calc.EndpointData) {
	d.srcEp = sep
}

func (d *Data) SetDestinationEndpointData(dep *calc.EndpointData) {
	d.dstEp = dep
}

func (d *Data) AddRuleID(ruleID *calc.RuleID, tierIdx, numPkts, numBytes int) bool {
	var ok bool
	switch ruleID.Direction {
	case rules.RuleDirIngress:
		ok = d.IngressRuleTrace.addRuleID(ruleID, tierIdx, numPkts, numBytes)
	case rules.RuleDirEgress:
		ok = d.EgressRuleTrace.addRuleID(ruleID, tierIdx, numPkts, numBytes)
	}

	if ok {
		d.touch()
		d.setDirtyFlag()
	}
	return ok
}

func (d *Data) ReplaceRuleID(ruleID *calc.RuleID, tierIdx, numPkts, numBytes int) bool {
	switch ruleID.Direction {
	case rules.RuleDirIngress:
		d.IngressRuleTrace.replaceRuleID(ruleID, tierIdx, numPkts, numBytes)
	case rules.RuleDirEgress:
		d.EgressRuleTrace.replaceRuleID(ruleID, tierIdx, numPkts, numBytes)
	}

	d.touch()
	d.setDirtyFlag()
	return true
}

func (d *Data) Report(c chan<- MetricUpdate, expired bool) {
	ut := UpdateTypeReport
	if expired {
		ut = UpdateTypeExpire
	}
	if d.isConnection {
		// For connections, we only send ingress and egress updates if:
		// -  There is something to report, i.e.
		//    -  flow is expired, or
		//    -  connection related stats are dirty
		// -  The policy verdict rule is an Allow (since Deny would suggest either an old connection or policy
		//    updates that have not yet filtered up through NFLogs).
		if expired || d.IsDirty() {
			if d.EgressRuleTrace.Action() == rules.RuleActionAllow {
				c <- d.metricUpdateEgressConn(ut)
			}
			if d.IngressRuleTrace.Action() == rules.RuleActionAllow {
				c <- d.metricUpdateIngressConn(ut)
			}
		}
	} else {
		// For non-connections, we only send ingress and egress updates if:
		// -  There is something to report, i.e.
		//    -  flow is expired, or
		//    -  rule trace related stats are dirty
		// -  The policy verdict rule has been determined.
		if (expired || d.EgressRuleTrace.IsDirty()) && d.EgressRuleTrace.FoundVerdict() {
			c <- d.metricUpdateEgressNoConn(ut)
		}
		if (expired || d.IngressRuleTrace.IsDirty()) && d.IngressRuleTrace.FoundVerdict() {
			c <- d.metricUpdateIngressNoConn(ut)
		}
	}

	// Metrics have been reported, so acknowledge the stored data by resetting the dirty
	// flag and resetting the delta counts.
	d.clearDirtyFlag()
}

// metricUpdateIngressConn creates a metric update for Inbound connection traffic
func (d *Data) metricUpdateIngressConn(ut UpdateType) MetricUpdate {
	return MetricUpdate{
		updateType:   ut,
		tuple:        d.Tuple,
		srcEp:        d.srcEp,
		dstEp:        d.dstEp,
		ruleIDs:      d.IngressRuleTrace.Path(),
		isConnection: d.isConnection,
		inMetric: MetricValue{
			deltaPackets:             d.conntrackPktsCtr.Delta(),
			deltaBytes:               d.conntrackBytesCtr.Delta(),
			deltaAllowedHTTPRequests: d.httpReqAllowedCtr.Delta(),
			deltaDeniedHTTPRequests:  d.httpReqDeniedCtr.Delta(),
		},
		outMetric: MetricValue{
			deltaPackets: d.conntrackPktsCtrReverse.Delta(),
			deltaBytes:   d.conntrackBytesCtrReverse.Delta(),
		},
	}
}

// metricUpdateEgressConn creates a metric update for Outbound connection traffic
func (d *Data) metricUpdateEgressConn(ut UpdateType) MetricUpdate {
	return MetricUpdate{
		updateType:   ut,
		tuple:        d.Tuple,
		srcEp:        d.srcEp,
		dstEp:        d.dstEp,
		ruleIDs:      d.EgressRuleTrace.Path(),
		isConnection: d.isConnection,
		inMetric: MetricValue{
			deltaPackets: d.conntrackPktsCtrReverse.Delta(),
			deltaBytes:   d.conntrackBytesCtrReverse.Delta(),
		},
		outMetric: MetricValue{
			deltaPackets: d.conntrackPktsCtr.Delta(),
			deltaBytes:   d.conntrackBytesCtr.Delta(),
		},
	}
}

// metricUpdateIngressNoConn creates a metric update for Inbound non-connection traffic
func (d *Data) metricUpdateIngressNoConn(ut UpdateType) MetricUpdate {
	return MetricUpdate{
		updateType:   ut,
		tuple:        d.Tuple,
		srcEp:        d.srcEp,
		dstEp:        d.dstEp,
		ruleIDs:      d.IngressRuleTrace.Path(),
		isConnection: d.isConnection,
		inMetric: MetricValue{
			deltaPackets: d.IngressRuleTrace.pktsCtr.Delta(),
			deltaBytes:   d.IngressRuleTrace.bytesCtr.Delta(),
		},
	}
}

// metricUpdateEgressNoConn creates a metric update for Outbound non-connection traffic
func (d *Data) metricUpdateEgressNoConn(ut UpdateType) MetricUpdate {
	return MetricUpdate{
		updateType:   ut,
		tuple:        d.Tuple,
		srcEp:        d.srcEp,
		dstEp:        d.dstEp,
		ruleIDs:      d.EgressRuleTrace.Path(),
		isConnection: d.isConnection,
		outMetric: MetricValue{
			deltaPackets: d.EgressRuleTrace.pktsCtr.Delta(),
			deltaBytes:   d.EgressRuleTrace.bytesCtr.Delta(),
		},
	}
}

// endpointName is a convenience function to return a printable name for an endpoint.
func endpointName(key model.Key) (name string) {
	switch k := key.(type) {
	case model.WorkloadEndpointKey:
		name = workloadEndpointName(k)
	case model.HostEndpointKey:
		name = hostEndpointName(k)
	}
	return
}

func workloadEndpointName(wep model.WorkloadEndpointKey) string {
	return "WEP(" + wep.Hostname + "/" + wep.OrchestratorID + "/" + wep.WorkloadID + "/" + wep.EndpointID + ")"
}

func hostEndpointName(hep model.HostEndpointKey) string {
	return "HEP(" + hep.Hostname + "/" + hep.EndpointID + ")"
}
