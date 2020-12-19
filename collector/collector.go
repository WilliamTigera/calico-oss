// +build !windows

// Copyright (c) 2016-2020 Tigera, Inc. All rights reserved.

package collector

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/gavv/monotime"
	"github.com/google/gopacket/layers"
	"github.com/mipearson/rfw"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/felix/calc"
	"github.com/projectcalico/felix/jitter"
	"github.com/projectcalico/felix/proto"
	"github.com/projectcalico/felix/rules"

	"github.com/tigera/nfnetlink"
)

const (
	expectedLocalEither byte = iota
	expectedLocalDestination
	expectedLocalSource
)

type Config struct {
	StatsDumpFilePath string

	AgeTimeout            time.Duration
	InitialReportingDelay time.Duration
	ExportingInterval     time.Duration
	EnableNetworkSets     bool
	EnableServices        bool

	MaxOriginalSourceIPsIncluded int
}

// A collector (a StatsManager really) collects StatUpdates from data sources
// and stores them as a Data object in a map keyed by Tuple.
// All data source channels must be specified when creating the
//
// Note that the dataplane statistics channel (ds) is currently just used for the
// policy syncer but will eventually also include NFLOG stats as well.
type collector struct {
	packetInfoReader    PacketInfoReader
	conntrackInfoReader ConntrackInfoReader
	luc                 *calc.LookupsCache
	epStats             map[Tuple]*Data
	ticker              jitter.JitterTicker
	sigChan             chan os.Signal
	config              *Config
	dumpLog             *log.Logger
	reporterMgr         *ReporterManager
	ds                  chan *proto.DataplaneStats
	dnsLogReporter      DNSLogReporterInterface
	l7LogReporter       L7LogReporterInterface
}

// newCollector instantiates a new collector. The StartDataplaneStatsCollector function is the only public
// function for collector instantiation.
func newCollector(lc *calc.LookupsCache, rm *ReporterManager, cfg *Config) Collector {
	return &collector{
		luc:         lc,
		epStats:     make(map[Tuple]*Data),
		ticker:      jitter.NewTicker(cfg.ExportingInterval, cfg.ExportingInterval/10),
		sigChan:     make(chan os.Signal, 1),
		config:      cfg,
		dumpLog:     log.New(),
		reporterMgr: rm,
		ds:          make(chan *proto.DataplaneStats, 1000),
	}
}

// ReportingChannel returns the channel used to report dataplane statistics.
func (c *collector) ReportingChannel() chan<- *proto.DataplaneStats {
	return c.ds
}

func (c *collector) Start() error {
	if c.packetInfoReader == nil {
		return fmt.Errorf("missing PacketInfoReader")
	}

	if err := c.packetInfoReader.Start(); err != nil {
		return fmt.Errorf("PacketInfoReader failed to start: %w", err)
	}

	if c.conntrackInfoReader == nil {
		return fmt.Errorf("missing ConntrackInfoReader")
	}

	if err := c.conntrackInfoReader.Start(); err != nil {
		return fmt.Errorf("ConntrackInfoReader failed to start: %w", err)
	}

	go c.startStatsCollectionAndReporting()
	c.setupStatsDumping()
	if c.dnsLogReporter != nil {
		c.dnsLogReporter.Start()
	}

	if c.l7LogReporter != nil {
		c.l7LogReporter.Start()
	}

	return nil
}

func (c *collector) SetPacketInfoReader(pir PacketInfoReader) {
	c.packetInfoReader = pir
}

func (c *collector) SetConntrackInfoReader(cir ConntrackInfoReader) {
	c.conntrackInfoReader = cir
}

func (c *collector) startStatsCollectionAndReporting() {
	pktInfoC := c.packetInfoReader.PacketInfoChan()
	ctInfoC := c.conntrackInfoReader.ConntrackInfoChan()

	// When a collector is started, we respond to the following events:
	// 1. StatUpdates for incoming datasources (chan c.mux).
	// 2. A signal handler that will dump logs on receiving SIGUSR2.
	// 3. A done channel for stopping and cleaning up collector (TODO).
	for {
		select {
		case ctInfo := <-ctInfoC:
			c.handleCtInfo(ctInfo)
		case pktInfo := <-pktInfoC:
			c.ApplyPacketInfo(pktInfo)
		case <-c.ticker.Channel():
			c.checkEpStats()
		case <-c.sigChan:
			c.dumpStats()
		case ds := <-c.ds:
			c.convertDataplaneStatsAndApplyUpdate(ds)
		}
	}
}

func (c *collector) setupStatsDumping() {
	// TODO (doublek): This may not be the best place to put this. Consider
	// moving the signal handler and logging to file logic out of the collector
	// and simply out to appropriate sink on different messages.
	signal.Notify(c.sigChan, syscall.SIGUSR2)

	err := os.MkdirAll(path.Dir(c.config.StatsDumpFilePath), 0755)
	if err != nil {
		log.WithError(err).Fatal("Failed to create log dir")
	}

	rotAwareFile, err := rfw.Open(c.config.StatsDumpFilePath, 0644)
	if err != nil {
		log.WithError(err).Fatal("Failed to open log file")
	}

	// Attributes have to be directly set for instantiated logger as opposed
	// to the module level log object.
	c.dumpLog.Formatter = &MessageOnlyFormatter{}
	c.dumpLog.Level = log.InfoLevel
	c.dumpLog.Out = rotAwareFile
}

// getDataAndUpdateEndpoints returns a pointer to the data structure keyed off the supplied tuple.  If there
// is no entry and the tuple is for an active flow then an entry is created.
//
// This may return nil if the endpoint data does not match up with the requested data type.
//
// This method also updates the endpoint data from the cache, so beware - it is not as lightweight as a
// simple map lookup.
func (c *collector) getDataAndUpdateEndpoints(tuple Tuple, expired bool) *Data {
	data, okData := c.epStats[tuple]
	if expired {
		// If the connection has expired then return the data as is. If there is no entry, that's fine too.
		return data
	}
	srcEp, dstEp := c.lookupEndpoint(tuple.src), c.lookupEndpoint(tuple.dst)
	if !okData {
		// For new entries, check that at least one of the endpoints is local.
		if (srcEp == nil || !srcEp.IsLocal) && (dstEp == nil || !dstEp.IsLocal) {
			return nil
		}

		// The entry does not exist. Go ahead and create a new one and add it to the map.
		data = NewData(tuple, srcEp, dstEp, c.config.MaxOriginalSourceIPsIncluded)
		c.epStats[tuple] = data

		// Return the new entry.
		return data
	}

	if data.reported && (endpointChanged(data.srcEp, srcEp) || endpointChanged(data.dstEp, dstEp)) {
		// The endpoint information has now changed. Handle the endpoint changes.
		c.handleDataEndpointOrRulesChanged(data)

		// For updated entries, check that at least one of the endpoints is still local. If not delete the entry.
		if (srcEp == nil || !srcEp.IsLocal) && (dstEp == nil || !dstEp.IsLocal) {
			c.deleteData(data)
			return nil
		}
	}

	// Update endpoint info in data.
	data.srcEp, data.dstEp = srcEp, dstEp
	return data
}

// endpointChanged determines if the endpoint has changed.
func endpointChanged(ep1, ep2 *calc.EndpointData) bool {
	if ep1 == ep2 {
		return false
	}
	if ep1 == nil || ep2 == nil {
		return true
	}
	return ep1.Key != ep2.Key
}

func (c *collector) lookupEndpoint(ip [16]byte) *calc.EndpointData {
	// Get the endpoint data for this entry, preferentially using a real endpoint over a NetworkSet.
	if ep, ok := c.luc.GetEndpoint(ip); ok {
		return ep
	} else if c.config.EnableNetworkSets {
		ep, _ = c.luc.GetNetworkset(ip)
		return ep
	}
	return nil
}

// applyConntrackStatUpdate applies a stats update from a conn track poll.
// If entryExpired is set then, this means that the update is for a recently
// expired entry. One of the following will be done:
// - If we already track the tuple, then the stats will be updated and will
//   then be expired from epStats.
// - If we don't track the tuple, this call will be a no-op as this update
//   is just waiting for the conntrack entry to timeout.
func (c *collector) applyConntrackStatUpdate(
	data *Data, packets int, bytes int, reversePackets int, reverseBytes int, entryExpired bool,
) {
	if data != nil {
		data.SetConntrackCounters(packets, bytes)
		data.SetConntrackCountersReverse(reversePackets, reverseBytes)

		if entryExpired {
			// The connection has expired. if the metrics can be reported then report and expire them now.
			// Otherwise, flag as expired and allow the export timer to process the connection - this allows additional
			// time for asynchronous meta data to be gathered (such as service info and process info).
			if c.reportMetrics(data, false) {
				c.expireMetrics(data)
				c.deleteData(data)
			} else {
				data.SetExpired()
			}
		}
	}
}

// applyNflogStatUpdate applies a stats update from an NFLOG.
func (c *collector) applyNflogStatUpdate(data *Data, ruleID *calc.RuleID, matchIdx, numPkts, numBytes int) {
	if ru := data.AddRuleID(ruleID, matchIdx, numPkts, numBytes); ru == RuleMatchIsDifferent {
		c.handleDataEndpointOrRulesChanged(data)
		data.ReplaceRuleID(ruleID, matchIdx, numPkts, numBytes)
	}

	// The rule has just been set, update the last rule update time. This provides a window during which we can
	// gather any remaining rule hits.
	data.ruleUpdatedAt = monotime.Now()
}

func (c *collector) handleDataEndpointOrRulesChanged(data *Data) {
	// The endpoints or rule matched have changed. If reported then expire the metrics and update the
	// endpoint data.
	if c.reportMetrics(data, false) {
		// We only need to expire metric entries that've probably been reported
		// in the first place.
		c.expireMetrics(data)

		// Reset counters and replace the rule.
		data.ResetConntrackCounters()
		data.ResetApplicationCounters()

		// Sett reported to false so the data can be updated without further reports.
		data.reported = false
	}
}

func (c *collector) checkEpStats() {
	// We report stats at initial reporting delay after the last rule update. This aims to ensure we have the full set
	// of data before we report the stats. As a minor finesse, pre-calculate the latest update time to consider reporting.
	minLastRuleUpdatedAt := monotime.Now() - c.config.InitialReportingDelay
	minExpirationAt := monotime.Now() - c.config.AgeTimeout

	// For each entry
	// - report metrics.  Metrics reported through the ticker processing will wait for the initial reporting delay
	//   before reporting.  Note that this may be short-circuited by conntrack events or nflog events that inidicate
	//   the flow is terminated or has changed.
	// - check age and expire the entry if needed.
	for _, data := range c.epStats {
		if data.IsDirty() && (data.reported || data.RuleUpdatedAt() < minLastRuleUpdatedAt) {
			c.reportMetrics(data, true)
		}
		if data.UpdatedAt() < minExpirationAt {
			c.expireMetrics(data)
			c.deleteData(data)
		}
	}
}

// reportMetrics reports the metrics if all required data is present, or returns false if not reported.
// Set the force flag to true if the data should be reported before all asynchronous data is collected.
func (c *collector) reportMetrics(data *Data, force bool) bool {
	foundService := true
	if !data.reported {
		// Check if the destination was accessed via a service. Once reported, this will not be updated again.
		if data.dstSvc.Name == "" {
			if data.isDNAT {
				// Destination is NATed, look up service from the pre-DNAT record.
				data.dstSvc, foundService = c.luc.GetServiceFromPreDNATDest(data.preDNATAddr, data.preDNATPort, data.Tuple.proto)
			} else if _, ok := c.luc.GetNode(data.Tuple.dst); ok {
				// Destination is a node, so could be a node port service.
				data.dstSvc, foundService = c.luc.GetNodePortService(data.Tuple.l4Dst, data.Tuple.proto)
			}
		}
	}

	if !force {
		// If not forcing then return if:
		// - There may be a service to report
		// - The verdict rules have not been found for the local endpoints.
		// In this case data will be reported later during ticker processing.
		if !foundService || !data.VerdictFound() {
			log.Infof("Service not found")
			return false
		}
	}

	// Send the metrics.
	c.sendMetrics(data, false)
	data.reported = true
	return true
}

func (c *collector) expireMetrics(data *Data) {
	if data.reported {
		c.sendMetrics(data, true)
	}
}

func (c *collector) deleteData(data *Data) {
	delete(c.epStats, data.Tuple)
}

func (c *collector) sendMetrics(data *Data, expired bool) {
	ut := UpdateTypeReport
	if expired {
		ut = UpdateTypeExpire
	}
	// For connections and non-connections, we only send ingress and egress updates if:
	// -  There is something to report, i.e.
	//    -  flow is expired, or
	//    -  associated stats are dirty
	// -  The policy verdict rule has been determined. Note that for connections the policy verdict may be "Deny" due
	//    to DropActionOverride setting (e.g. if set to ALLOW, then we'll get connection stats, but the metrics will
	//    indicate Denied).
	// Only clear the associated stats and dirty flag once the metrics are reported.
	if data.isConnection {
		// Report connection stats.
		if expired || data.IsDirty() {
			// Track if we need to send a separate expire metric update. This is required when we are only
			// reporting Original IP metric updates and want to send a corresponding expiration metric update.
			// When they are correlated with regular metric updates and connection metrics, we don't need to
			// send this.
			sendOrigSourceIPsExpire := true
			if data.EgressRuleTrace.FoundVerdict() {
				c.reporterMgr.ReportChan <- data.metricUpdateEgressConn(ut)
			}
			if data.IngressRuleTrace.FoundVerdict() {
				sendOrigSourceIPsExpire = false
				c.reporterMgr.ReportChan <- data.metricUpdateIngressConn(ut)
			}

			// We may receive HTTP Request data after we've flushed the connection counters.
			if (expired && data.origSourceIPsActive && sendOrigSourceIPsExpire) || data.NumUniqueOriginalSourceIPs() != 0 {
				data.origSourceIPsActive = !expired
				c.reporterMgr.ReportChan <- data.metricUpdateOrigSourceIPs(ut)
			}

			// Clear the connection dirty flag once the stats have been reported. Note that we also clear the
			// rule trace stats here too since any data stored in them has been superceded by the connection
			// stats.
			data.clearConnDirtyFlag()
			data.EgressRuleTrace.ClearDirtyFlag()
			data.IngressRuleTrace.ClearDirtyFlag()
		}
	} else {
		// Report rule trace stats.
		if (expired || data.EgressRuleTrace.IsDirty()) && data.EgressRuleTrace.FoundVerdict() {
			c.reporterMgr.ReportChan <- data.metricUpdateEgressNoConn(ut)
			data.EgressRuleTrace.ClearDirtyFlag()
		}
		if (expired || data.IngressRuleTrace.IsDirty()) && data.IngressRuleTrace.FoundVerdict() {
			c.reporterMgr.ReportChan <- data.metricUpdateIngressNoConn(ut)
			data.IngressRuleTrace.ClearDirtyFlag()
		}

		// We do not need to clear the connection stats here. Connection stats are fully reset if the Data moves
		// from a connection to non-connection state.
	}
}

// handleCtInfo handles an update from conntrack
// We expect and process connections (conntrack entries) of 3 different flavors.
//
// - Connections that *neither* begin *nor* terminate locally.
// - Connections that either begin or terminate locally.
// - Connections that begin *and* terminate locally.
//
// When processing these, we also check if the connection is flagged as a
// destination NAT (DNAT) connection. If it is a DNAT-ed connection, we
// process the conntrack entry after we figure out the DNAT-ed destination and port.
// This is important for services where the connection will have the cluster IP as the
// pre-DNAT-ed destination, but we want the post-DNAT workload IP and port.
// The pre-DNAT entry will also be used to lookup service related information.
func (c *collector) handleCtInfo(ctInfo ConntrackInfo) {
	// Get or create a data entry and update the counters. If no entry is returned then neither source nor dest are
	// calico managed endpoints. A relevant conntrack entry requires at least one of the endpoints to be a local
	// Calico managed endpoint.
	if data := c.getDataAndUpdateEndpoints(ctInfo.Tuple, ctInfo.Expired); data != nil {

		if !data.isDNAT && ctInfo.IsDNAT {
			originalTuple := ctInfo.PreDNATTuple
			data.isDNAT = true
			data.preDNATAddr = originalTuple.dst
			data.preDNATPort = originalTuple.l4Dst
		}

		c.applyConntrackStatUpdate(data,
			ctInfo.Counters.Packets, ctInfo.Counters.Bytes,
			ctInfo.ReplyCounters.Packets, ctInfo.ReplyCounters.Bytes,
			ctInfo.Expired)
	}
}

func (c *collector) ApplyPacketInfo(pktInfo PacketInfo) {
	var (
		localEp        *calc.EndpointData
		localMatchData *calc.MatchData
		data           *Data
	)

	tuple := pktInfo.Tuple

	if data = c.getDataAndUpdateEndpoints(tuple, false); data == nil {
		// Data is nil, so the destination endpoint cannot be managed by local Calico.
		return
	}

	if !data.isDNAT && pktInfo.IsDNAT {
		originalTuple := pktInfo.PreDNATTuple
		data.isDNAT = true
		data.preDNATAddr = originalTuple.dst
		data.preDNATPort = originalTuple.l4Dst
	}

	// Determine the local endpoint for this update.
	switch pktInfo.Direction {
	case rules.RuleDirIngress:
		// The local destination should be local.
		if localEp = data.dstEp; localEp == nil || !localEp.IsLocal {
			return
		}
		localMatchData = localEp.Ingress
	case rules.RuleDirEgress:
		// The cache will return nil for egress if the source endpoint is not local.
		if localEp = data.srcEp; localEp == nil || !localEp.IsLocal {
			return
		}
		localMatchData = localEp.Egress
	default:
		return
	}

	for _, rule := range pktInfo.RuleHits {
		ruleID := rule.RuleID

		if ruleID.IsProfile() {
			// This is a profile verdict. Apply the rule unchanged, but at the profile match index (which is at the
			// very end of the match slice).
			c.applyNflogStatUpdate(data, ruleID, localMatchData.ProfileMatchIndex, rule.Hits, rule.Bytes)
			continue
		}

		if ruleID.IsEndOfTier() {
			// This is an end-of-tier action.
			// -  For deny convert the ruleID to the implicit drop rule
			// -  For pass leave the rule unchanged. We never return this to the user, but instead use it to determine
			//    whether we add staged policy end-of-tier denies.
			// For both deny and pass, add the rule at the end of tier match index.
			tier, ok := localMatchData.TierData[ruleID.Tier]
			if !ok {
				continue
			}

			switch ruleID.Action {
			case rules.RuleActionDeny:
				c.applyNflogStatUpdate(
					data, tier.ImplicitDropRuleID, tier.EndOfTierMatchIndex,
					rule.Hits, rule.Bytes,
				)
			case rules.RuleActionPass:
				c.applyNflogStatUpdate(
					data, ruleID, tier.EndOfTierMatchIndex,
					rule.Hits, rule.Bytes,
				)
			}
			continue
		}

		// This is one of:
		// -  An enforced rule match
		// -  A staged policy match
		// -  A staged policy miss
		// -  An end-of-tier pass (from tiers only containing staged policies)
		//
		// For all these cases simply add the unchanged ruleID using the match index reserved for that policy.
		// Extract the policy data from the ruleID.
		policyIdx, ok := localMatchData.PolicyMatches[ruleID.PolicyID]
		if !ok {
			continue
		}

		c.applyNflogStatUpdate(data, ruleID, policyIdx, rule.Hits, rule.Bytes)
	}

	if data.IsExpired() && c.reportMetrics(data, false) {
		// If the data is expired then attempt to report it now so that we can remove the connection entry. If reported
		// the data can be expired and deleted immediately, otherwise it will get exported during ticker processing.
		c.expireMetrics(data)
		c.deleteData(data)
	}
}

// convertDataplaneStatsAndApplyUpdate merges the proto.DataplaneStatistics into the current
// data stored for the specific connection tuple.
func (c *collector) convertDataplaneStatsAndApplyUpdate(d *proto.DataplaneStats) {
	log.Debugf("Received dataplane stats update %+v", d)
	// Create a Tuple representing the DataplaneStats.
	t, err := extractTupleFromDataplaneStats(d)
	if err != nil {
		log.Errorf("unable to extract 5-tuple from DataplaneStats: %v", err)
		return
	}

	// Locate the data for this connection, creating if not yet available (it's possible to get an update
	// from the dataplane before nflogs or conntrack).
	data := c.getDataAndUpdateEndpoints(t, false)

	var httpDataCount int
	for _, s := range d.Stats {
		if s.Relativity != proto.Statistic_DELTA {
			// Currently we only expect delta HTTP requests from the dataplane statistics API.
			log.WithField("relativity", s.Relativity.String()).Warning("Received a statistic from the dataplane that Felix cannot process")
			continue
		}
		switch s.Kind {
		case proto.Statistic_HTTP_REQUESTS:
			switch s.Action {
			case proto.Action_ALLOWED:
				data.IncreaseHTTPRequestAllowedCounter(int(s.Value))
			case proto.Action_DENIED:
				data.IncreaseHTTPRequestDeniedCounter(int(s.Value))
			}
		case proto.Statistic_HTTP_DATA:
			httpDataCount = int(s.Value)
		default:
			log.WithField("kind", s.Kind.String()).Warnf("Received a statistic from the dataplane that Felix cannot process")
			continue
		}

	}
	ips := make([]net.IP, 0, len(d.HttpData))
	for _, hd := range d.HttpData {
		if c.l7LogReporter != nil && hd.Type != "" {
			// If the l7LogReporter has been set, then L7 logs are configured to be run.
			// If the HttpData has a type, then this is an L7 log.
			c.LogL7(hd, data, t, httpDataCount)
		} else {
			var origSrcIP string
			if len(hd.XRealIp) != 0 {
				origSrcIP = hd.XRealIp
			} else if len(hd.XForwardedFor) != 0 {
				origSrcIP = hd.XForwardedFor
			} else {
				continue
			}
			sip := net.ParseIP(origSrcIP)
			if sip == nil {
				log.WithField("IP", origSrcIP).Warn("bad source IP")
				continue
			}
			ips = append(ips, sip)
		}
	}
	if len(ips) != 0 {
		if httpDataCount == 0 {
			httpDataCount = len(ips)
		}
		bs := NewBoundedSetFromSliceWithTotalCount(c.config.MaxOriginalSourceIPsIncluded, ips, httpDataCount)
		data.AddOriginalSourceIPs(bs)
	} else if httpDataCount != 0 {
		data.IncreaseNumUniqueOriginalSourceIPs(httpDataCount)
	}
}

func extractTupleFromNflogTuple(nflogTuple nfnetlink.NflogPacketTuple) Tuple {
	var l4Src, l4Dst int
	if nflogTuple.Proto == 1 {
		l4Src = nflogTuple.L4Src.Id
		l4Dst = int(uint16(nflogTuple.L4Dst.Type)<<8 | uint16(nflogTuple.L4Dst.Code))
	} else {
		l4Src = nflogTuple.L4Src.Port
		l4Dst = nflogTuple.L4Dst.Port
	}
	return MakeTuple(nflogTuple.Src, nflogTuple.Dst, nflogTuple.Proto, l4Src, l4Dst)
}

func extractTupleFromCtEntryTuple(ctTuple nfnetlink.CtTuple) Tuple {
	var l4Src, l4Dst int
	if ctTuple.ProtoNum == 1 {
		l4Src = ctTuple.L4Src.Id
		l4Dst = int(uint16(ctTuple.L4Dst.Type)<<8 | uint16(ctTuple.L4Dst.Code))
	} else {
		l4Src = ctTuple.L4Src.Port
		l4Dst = ctTuple.L4Dst.Port
	}
	return MakeTuple(ctTuple.Src, ctTuple.Dst, ctTuple.ProtoNum, l4Src, l4Dst)
}

func extractTupleFromDataplaneStats(d *proto.DataplaneStats) (Tuple, error) {
	var protocol int32
	switch n := d.Protocol.GetNumberOrName().(type) {
	case *proto.Protocol_Number:
		protocol = n.Number
	case *proto.Protocol_Name:
		switch strings.ToLower(n.Name) {
		case "tcp":
			protocol = 6
		case "udp":
			protocol = 17
		default:
			return Tuple{}, fmt.Errorf("unhandled protocol: %s", n)
		}
	}

	// Use the standard go net library to parse the IP since this always returns IPs as 16 bytes.
	srcIP := net.ParseIP(d.SrcIp)
	if srcIP == nil {
		return Tuple{}, fmt.Errorf("bad source IP: %s", d.SrcIp)
	}
	dstIP := net.ParseIP(d.DstIp)
	if dstIP == nil {
		return Tuple{}, fmt.Errorf("bad destination IP: %s", d.DstIp)
	}

	// But invoke the To16() just to be sure.
	var srcArray, dstArray [16]byte
	copy(srcArray[:], srcIP.To16())
	copy(dstArray[:], dstIP.To16())

	// Locate the data for this connection, creating if not yet available (it's possible to get an update
	// before nflogs or conntrack).
	return MakeTuple(srcArray, dstArray, int(protocol), int(d.SrcPort), int(d.DstPort)), nil
}

// Write stats to file pointed by Config.StatsDumpFilePath.
// When called, clear the contents of the file Config.StatsDumpFilePath before
// writing the stats to it.
func (c *collector) dumpStats() {
	log.Debugf("Dumping Stats to %v", c.config.StatsDumpFilePath)

	_ = os.Truncate(c.config.StatsDumpFilePath, 0)
	c.dumpLog.Infof("Stats Dump Started: %v", time.Now().Format("2006-01-02 15:04:05.000"))
	c.dumpLog.Infof("Number of Entries: %v", len(c.epStats))
	for _, v := range c.epStats {
		c.dumpLog.Info(fmtEntry(v))
	}
	c.dumpLog.Infof("Stats Dump Completed: %v", time.Now().Format("2006-01-02 15:04:05.000"))
}

func fmtEntry(data *Data) string {
	return fmt.Sprintf("%v", data)
}

// Logrus Formatter that strips the log entry of formatting such as time, log
// level and simply outputs *only* the message.
type MessageOnlyFormatter struct{}

func (f *MessageOnlyFormatter) Format(entry *log.Entry) ([]byte, error) {
	b := &bytes.Buffer{}
	b.WriteString(entry.Message)
	b.WriteByte('\n')
	return b.Bytes(), nil
}

// DNS activity logging.
func (c *collector) SetDNSLogReporter(reporter DNSLogReporterInterface) {
	c.dnsLogReporter = reporter
}

func (c *collector) LogDNS(src, dst net.IP, dns *layers.DNS, latencyIfKnown *time.Duration) {
	if c.dnsLogReporter == nil {
		return
	}
	// DNS responses come through here, so the source IP is the DNS server and the dest IP is
	// the client.
	serverEP, _ := c.luc.GetEndpoint(ipTo16Byte(src))
	clientEP, _ := c.luc.GetEndpoint(ipTo16Byte(dst))
	if serverEP == nil {
		serverEP, _ = c.luc.GetNetworkset(ipTo16Byte(src))
	}
	log.Debugf("Src %v -> Server %v", src, serverEP)
	log.Debugf("Dst %v -> Client %v", dst, clientEP)
	if latencyIfKnown != nil {
		log.Debugf("DNS-LATENCY: Log %v", *latencyIfKnown)
	}
	update := DNSUpdate{
		ClientIP:       dst,
		ClientEP:       clientEP,
		ServerIP:       src,
		ServerEP:       serverEP,
		DNS:            dns,
		LatencyIfKnown: latencyIfKnown,
	}
	if err := c.dnsLogReporter.Log(update); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"src": src,
			"dst": dst,
			"dns": dns,
		}).Error("Failed to log DNS packet")
	}
}

func (c *collector) SetL7LogReporter(reporter L7LogReporterInterface) {
	c.l7LogReporter = reporter
}

func (c *collector) LogL7(hd *proto.HTTPData, data *Data, tuple Tuple, httpDataCount int) {
	// Translate endpoint data into L7Update
	update := L7Update{
		Tuple:         tuple,
		SrcEp:         data.srcEp,
		DstEp:         data.dstEp,
		Duration:      int(hd.Duration),
		DurationMax:   int(hd.DurationMax),
		BytesReceived: int(hd.BytesReceived),
		BytesSent:     int(hd.BytesSent),
		ResponseCode:  int(hd.ResponseCode),
		Method:        hd.RequestMethod,
		Path:          hd.RequestPath,
		UserAgent:     hd.UserAgent,
		Type:          hd.Type,
		Count:         int(hd.Count),
		Domain:        hd.Domain,
	}

	// Send the update to the reporter
	if err := c.l7LogReporter.Log(update); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"src": tuple.src,
			"dst": tuple.src,
		}).Error("Failed to log request")
	}
}
