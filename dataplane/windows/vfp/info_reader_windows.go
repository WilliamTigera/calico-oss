// Copyright (c) 2020-2021 Tigera, Inc. All rights reserved.
package vfp

import (
	"strings"
	"sync"
	"time"

	"github.com/projectcalico/libcalico-go/lib/set"

	"github.com/projectcalico/felix/collector"

	"sigs.k8s.io/kind/pkg/errors"

	log "github.com/sirupsen/logrus"

	"github.com/tigera/windows-networking/pkg/etw"
	"github.com/tigera/windows-networking/pkg/vfpctrl"

	"github.com/projectcalico/felix/calc"
	"github.com/projectcalico/felix/jitter"
	"github.com/projectcalico/felix/rules"
)

const (
	windowsCollectorETWSession = "tigera-calico-etw-vfp"
	maxBufferedEvents          = 500000
	maxBufferedConntracks      = 500000
)

// EndpointEventHandler implements endPointEventListener interface.
// It also caches event updates so they can be processed in batches.
type EndpointEventHandler struct {
	// endpoints stores latest snapshot of endpoints in the system
	endpoints []string

	// epSetWithPolicyUpdate stores set of endpoint ids whose policies has been updated.
	epSetWithPolicyUpdate set.Set

	inSync bool

	mutex sync.Mutex
}

// Cache endpoint updates.
func (h *EndpointEventHandler) HandleEndpointsUpdate(ids []string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.endpoints = ids
	h.inSync = false
}

// Cache policy updates.
func (h *EndpointEventHandler) HandlePolicyUpdate(id string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.epSetWithPolicyUpdate.Add(id)
	h.inSync = false
}

// Process updates.
func (h *EndpointEventHandler) processUpdates(vfpOps *vfpctrl.VfpOperations) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if h.inSync {
		return
	}

	vfpOps.HandleEndpointEvent(vfpctrl.DPEventEndpointsUpdated(h.endpoints))

	h.epSetWithPolicyUpdate.Iter(func(item interface{}) error {
		vfpOps.HandleEndpointEvent(vfpctrl.DPEventPolicyUpdated(item.(string)))
		return set.RemoveItem
	})
	h.inSync = true
}

// InfoReader implements collector.PacketInfoReader and collector.ConntrackInfoReader.
// It makes sense to have a single goroutine handling VFP events/flows to avoid possible race
// on same endpoints cache of underlying structure.
type InfoReader struct {
	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup
	stopC     chan struct{}

	luc *calc.LookupsCache

	eventAggrC chan *etw.EventAggregate
	eventDoneC chan struct{}

	etwOps *etw.EtwOperations
	vfpOps *vfpctrl.VfpOperations

	packetInfoC    chan collector.PacketInfo
	bufferedEvents []*collector.PacketInfo

	ticker             jitter.JitterTicker
	conntrackInfoC     chan collector.ConntrackInfo
	bufferedConntracks []*collector.ConntrackInfo

	epEventHandler *EndpointEventHandler
}

func NewInfoReader(lookupsCache *calc.LookupsCache, period time.Duration) *InfoReader {
	etwOps, err := etw.NewEtwOperations([]int{etw.VFP_EVENT_ID_ENDPOINT_ACL}, windowsCollectorETWSession)
	if err != nil {
		log.WithError(err).Fatalf("Failed to create ETW operations")
	}

	vfpOps := vfpctrl.NewVfpOperations()

	return &InfoReader{
		stopC:              make(chan struct{}),
		luc:                lookupsCache,
		etwOps:             etwOps,
		vfpOps:             vfpOps,
		eventAggrC:         make(chan *etw.EventAggregate, 1000),
		eventDoneC:         make(chan struct{}, 1),
		packetInfoC:        make(chan collector.PacketInfo, 1000),
		ticker:             jitter.NewTicker(period, period/10),
		conntrackInfoC:     make(chan collector.ConntrackInfo, 1000),
		bufferedEvents:     []*collector.PacketInfo{},
		bufferedConntracks: []*collector.ConntrackInfo{},
		epEventHandler: &EndpointEventHandler{
			endpoints:             []string{},
			epSetWithPolicyUpdate: set.New(),
		},
	}
}

func (r *InfoReader) Start() error {
	var ret error
	r.startOnce.Do(func() {
		if err := r.subscribe(); err != nil {
			ret = err
			return
		}

		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.run()
		}()
	})

	return ret
}

func (r *InfoReader) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopC)
		r.wg.Wait()
	})
}

// PacketInfoChan returns the channel with converted PacketInfo.
func (r *InfoReader) PacketInfoChan() <-chan collector.PacketInfo {
	return r.packetInfoC
}

// ConntrackInfoChan returns the channel with converted ConntrackInfo.
func (r *InfoReader) ConntrackInfoChan() <-chan collector.ConntrackInfo {
	return r.conntrackInfoC
}

func (r *InfoReader) EndpointEventHandler() *EndpointEventHandler {
	return r.epEventHandler
}

// Subscribe subscribes the reader to the ETW event stream.
func (r *InfoReader) subscribe() error {
	return r.etwOps.Subscribe(r.eventAggrC, r.eventDoneC)
}

func (r *InfoReader) run() {

	var (
		packetInfoC    chan collector.PacketInfo
		conntrackInfoC chan collector.ConntrackInfo
		nextPktToSend  collector.PacketInfo
		nextCTToSend   collector.ConntrackInfo
	)

	for {
		select {
		case <-r.stopC:
			return
		case packetInfoC <- nextPktToSend:
			r.bufferedEvents = r.bufferedEvents[1:]
			if len(r.bufferedEvents) == 0 {
				packetInfoC = nil // Disable this case until we have events to send.
			} else {
				nextPktToSend = *r.bufferedEvents[0] // Make sure value is updated.
			}
		case eventAggr := <-r.eventAggrC:
			infoPointer, err := r.convertEventAggrPkt(eventAggr)
			if err == nil {
				if len(r.bufferedEvents) > maxBufferedEvents {
					log.Warnf("VFP info reader reaches maximum number of buffered events.")
				} else {
					r.bufferedEvents = append(r.bufferedEvents, infoPointer)
				}
				nextPktToSend = *r.bufferedEvents[0] // Make sure value is updated.
				packetInfoC = r.packetInfoC          // Make sure the packetInfoC case is enabled.
			}
		case conntrackInfoC <- nextCTToSend:
			r.bufferedConntracks = r.bufferedConntracks[1:]
			if len(r.bufferedConntracks) == 0 {
				conntrackInfoC = nil // Disable this case until we have conntrack info to send.
			} else {
				nextCTToSend = *r.bufferedConntracks[0] // Make sure value is updated.
			}
		case <-r.ticker.Channel():
			r.vfpOps.ListFlows(r.handleFlowEntry)
			if len(r.bufferedConntracks) > 0 {
				nextCTToSend = *r.bufferedConntracks[0] // Make sure value is updated.
				conntrackInfoC = r.conntrackInfoC       // Make sure the conntrackInfoC is enabled.
			}
		}

		r.epEventHandler.processUpdates(r.vfpOps)
	}
}

func (r *InfoReader) convertEventAggrPkt(ea *etw.EventAggregate) (*collector.PacketInfo, error) {
	var dir rules.RuleDir

	log.Debugf("Collector: Handle EventAggr tuple %s rule <%s> count <%d> %#v",
		ea.Event.TupleString(), ea.Count, ea.Event)

	tuple, err := extractTupleFromEventAggr(ea)
	if err != nil {
		log.WithError(err).Errorf("failed to get tuple from ETW event")
		return nil, err
	}

	if ea.Event.IsIngress() {
		dir = rules.RuleDirIngress
	} else {
		dir = rules.RuleDirEgress
	}

	// Event could happen on an endpoint before we get a notification from Felix endpoint manager.
	r.vfpOps.MayAddNewEndpoint(ea.Event.EndpointID())

	ruleName, err := r.vfpOps.GetRuleFriendlyNameForEvent(ea.Event.EndpointID(), ea.Event.RuleID(), ea.Event.IsIngress())
	if err != nil {
		log.WithError(err).Warnf("failed to get rule name from ETW event")
		return nil, err
	}

	// Lookup the ruleID from the prefix.
	var prefixArr [64]byte
	prefixStr := extractPrefixStrFromRuleName(ruleName)
	copy(prefixArr[:], prefixStr)
	ruleID := r.luc.GetRuleIDFromNFLOGPrefix(prefixArr)
	if ruleID == nil {
		return nil, errors.New("failed to get rule id by policy lookup")
	}

	// Etw Event has one RuleHits prefix.
	// It has no service ip information (DNAT).
	// It has no bytes information.
	info := collector.PacketInfo{
		IsDNAT:    false,
		Direction: dir,
		RuleHits:  make([]collector.RuleHit, 0, 1),
		Tuple:     *tuple,
	}

	info.RuleHits = append(info.RuleHits, collector.RuleHit{
		RuleID: ruleID,
		Hits:   ea.Count,
		Bytes:  0,
	})

	return &info, nil
}

func convertFlowEntry(fe *vfpctrl.FlowEntry) (*collector.ConntrackInfo, error) {
	tuple, err := extractTupleFromFlowEntry(fe)
	if err != nil {
		return nil, err
	}

	// In the case of TCP, check if we can expire the entry early. We try to expire
	// entries early so that we don't send any spurious MetricUpdates for an expiring
	// conntrack entry.
	entryExpired := fe.ConnectionClosed()

	// Work out counters and reply counters based on flow direction.
	var pktCounters, bytesCounters, pktReplyCounters, bytesReplyCounters int
	if fe.IsInbound() {
		pktCounters = fe.PktsIn
		bytesCounters = fe.BytesIn
		pktReplyCounters = fe.PktsOut
		bytesReplyCounters = fe.BytesOut
	} else {
		pktCounters = fe.PktsOut
		bytesCounters = fe.BytesOut
		pktReplyCounters = fe.PktsIn
		bytesReplyCounters = fe.BytesIn
	}

	ctInfo := collector.ConntrackInfo{
		Tuple:   *tuple,
		Expired: entryExpired,
		Counters: collector.ConntrackCounters{
			Packets: pktCounters,
			Bytes:   bytesCounters,
		},
		ReplyCounters: collector.ConntrackCounters{
			Packets: pktReplyCounters,
			Bytes:   bytesReplyCounters,
		},
	}

	if fe.IsDNAT() {
		vTuple, err := extractPreDNATTupleFromFlowEntry(fe)
		if err != nil {
			return nil, err
		}
		ctInfo.IsDNAT = true
		ctInfo.PreDNATTuple = *vTuple
	}

	return &ctInfo, nil
}

func (r *InfoReader) handleFlowEntry(fe *vfpctrl.FlowEntry) {
	ctInfoPointer, err := convertFlowEntry(fe)
	if err != nil {
		log.WithError(err).Warnf("failed to convert flow entry")
		return
	}

	log.Debugf("Collector: Handle FlowEntry tuple %s, IN<%d,%d> OUT <%d,%d> Flow %#v",
		fe.TupleID, fe.PktsIn, fe.BytesIn, fe.PktsOut, fe.BytesOut, fe)

	if len(r.bufferedConntracks) > maxBufferedConntracks {
		log.Warnf("VFP info reader reaches maximum number of buffered conntracks.")
		return
	}
	r.bufferedConntracks = append(r.bufferedConntracks, ctInfoPointer)
}

func extractPrefixStrFromRuleName(name string) string {
	// Windows dataplane programs hns rules with three types of format for HNS rule Id.
	// prefix---rule name---sequence number   This is when rule name is not empty.
	// prefix---sequence number               This is when rule name is empty.
	// prefix                                 This is used for default deny rules.
	strs := strings.Split(name, rules.WindowsHnsRuleNameDelimeter)
	return strs[0]
}

func extractTupleFromEventAggr(ea *etw.EventAggregate) (*collector.Tuple, error) {
	tuple, err := ea.Event.Tuple()
	if err != nil {
		return nil, err
	}
	return collector.NewTuple(tuple.Src, tuple.Dst, tuple.Proto, tuple.L4SrcPort, tuple.L4DstPort), nil
}

func extractTupleFromFlowEntry(fe *vfpctrl.FlowEntry) (*collector.Tuple, error) {
	tuple, err := fe.Tuple()
	if err != nil {
		return nil, err
	}
	return collector.NewTuple(tuple.Src, tuple.Dst, tuple.Proto, tuple.L4SrcPort, tuple.L4DstPort), nil
}

func extractPreDNATTupleFromFlowEntry(fe *vfpctrl.FlowEntry) (*collector.Tuple, error) {
	tuple, err := fe.TuplePreDNAT()
	if err != nil {
		return nil, err
	}
	return collector.NewTuple(tuple.Src, tuple.Dst, tuple.Proto, tuple.L4SrcPort, tuple.L4DstPort), nil
}
