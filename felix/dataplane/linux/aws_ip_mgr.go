// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package intdataplane

import (
	"errors"
	"fmt"
	"net"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	"github.com/vishvananda/netlink"

	"github.com/projectcalico/calico/felix/ifacemonitor"

	"github.com/projectcalico/calico/felix/aws"

	"github.com/projectcalico/calico/felix/ip"
	"github.com/projectcalico/calico/felix/logutils"
	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/felix/routerule"
	"github.com/projectcalico/calico/felix/routetable"
	"github.com/projectcalico/calico/libcalico-go/lib/set"
)

// awsIPManager tries to provision secondary ENIs and IP addresses in the AWS fabric for any local pods that are
// in an IP pool with an associated AWS subnet.  The work of attaching ENIs and IP addresses is done by a
// background instance of aws.SecondaryIfaceProvisioner.  The work to configure the local dataplane is done
// by this object.
//
// For thread safety, the aws.SecondaryIfaceProvisioner sends its responses via a channel that is read by the
// main loop in int_dataplane.go.
type awsIPManager struct {
	// Indexes of data we've learned from the datastore.

	poolsByID                 map[string]*proto.IPAMPool
	poolIDsBySubnetID         map[string]set.Set
	localAWSRoutesByDst       map[ip.CIDR]*proto.RouteUpdate
	localRouteDestsBySubnetID map[string]set.Set /*ip.CIDR*/
	workloadEndpointsByID     map[proto.WorkloadEndpointID]awsEndpointInfo
	workloadEndpointIDsByCIDR map[ip.CIDR]set.Set /*proto.WorkloadEndpointID; expect one but can have glitches*/
	awsResyncNeeded           bool

	// ifaceProvisioner manages the AWS fabric resources.  It runs in the background to decouple AWS fabric updates
	// from the main thread.  We send it datastore snapshots; in return, it sends back SecondaryIfaceState objects
	// telling us what state the AWS fabric is in.
	ifaceProvisioner awsIfaceProvisioner

	// awsState is the most recent update we've got from the background thread telling us what state it thinks
	// the AWS fabric should be in. <nil> means "don't know", i.e. we're not ready to touch the dataplane yet.
	awsState *aws.LocalAWSNetworkState

	// Dataplane state.

	routeTablesByTableIdx  map[int]routeTable
	routeTablesByIfaceName map[string]routeTable
	freeRouteTableIndexes  []int
	routeRules             routeRules
	routeRulesInDataplane  map[awsRuleKey]*routerule.Rule
	dataplaneResyncNeeded  bool
	allAWSIfacesFound      bool
	ifaceNameToIfaceIdx    map[string]int // name -> linux iface index.
	primaryIfaceMTU        int
	dpConfig               Config
	ifaceNameToPrimaryIP   map[string]string

	opRecorder logutils.OpRecorder

	// Shims for testing.

	nl            awsNetlinkIface
	newRouteTable routeTableNewFn
	newRouteRules routeRulesNewFn
}

type awsEndpointInfo struct {
	IPv4Nets   []ip.CIDR
	ElasticIPs []ip.Addr
}

type awsIfaceProvisioner interface {
	OnDatastoreUpdate(ds aws.DatastoreState)
}

// awsRuleKey is a hashable struct containing the salient aspects of the routing rules that we need to program.
type awsRuleKey struct {
	srcAddr        ip.Addr
	routingTableID int
}

type AWSSubnetManagerOpt func(manager *awsIPManager)

func OptNetlinkOverride(nl awsNetlinkIface) AWSSubnetManagerOpt {
	return func(manager *awsIPManager) {
		manager.nl = nl
	}
}

func OptRouteTableOverride(newRT routeTableNewFn) AWSSubnetManagerOpt {
	return func(manager *awsIPManager) {
		manager.newRouteTable = newRT
	}
}

func OptRouteRulesOverride(newRR routeRulesNewFn) AWSSubnetManagerOpt {
	return func(manager *awsIPManager) {
		manager.newRouteRules = newRR
	}
}

func NewAWSIPManager(
	routeTableIndexes []int,
	dpConfig Config,
	opRecorder logutils.OpRecorder,
	ifaceProvisioner awsIfaceProvisioner,
	opts ...AWSSubnetManagerOpt,
) *awsIPManager {
	logrus.WithField("routeTables", routeTableIndexes).Info("Creating AWS subnet manager.")

	sm := &awsIPManager{
		poolsByID:                 map[string]*proto.IPAMPool{},
		poolIDsBySubnetID:         map[string]set.Set{},
		localAWSRoutesByDst:       map[ip.CIDR]*proto.RouteUpdate{},
		localRouteDestsBySubnetID: map[string]set.Set{},
		workloadEndpointsByID:     map[proto.WorkloadEndpointID]awsEndpointInfo{},
		workloadEndpointIDsByCIDR: map[ip.CIDR]set.Set{},

		freeRouteTableIndexes:  routeTableIndexes,
		routeTablesByIfaceName: map[string]routeTable{},
		routeTablesByTableIdx:  map[int]routeTable{},
		ifaceNameToPrimaryIP:   map[string]string{},
		ifaceNameToIfaceIdx:    map[string]int{},

		routeRulesInDataplane: map[awsRuleKey]*routerule.Rule{},
		dpConfig:              dpConfig,
		opRecorder:            opRecorder,

		ifaceProvisioner: ifaceProvisioner,

		nl:            awsRealNetlink{},
		newRouteRules: realRouteRuleNew,
		newRouteTable: realRouteTableNew,
	}

	for _, o := range opts {
		o(sm)
	}

	var err error
	sm.routeRules, err = sm.newRouteRules(
		4,
		dpConfig.AWSSecondaryIPRoutingRulePriority,
		set.FromArray(routeTableIndexes),
		routerule.RulesMatchPrioSrcTable,
		routerule.RulesMatchPrioSrcTable,
		dpConfig.NetlinkTimeout,
		func() (routerule.HandleIface, error) {
			return netlink.NewHandle(syscall.NETLINK_ROUTE)
		},
		opRecorder,
	)
	if err != nil {
		logrus.WithError(err).Panic("Failed to init routing rules manager.")
	}

	sm.queueAWSResync("first run")
	return sm
}

func (a *awsIPManager) OnUpdate(msg interface{}) {
	switch msg := msg.(type) {
	case *proto.IPAMPoolUpdate:
		a.onPoolUpdate(msg.Id, msg.Pool)
	case *proto.IPAMPoolRemove:
		a.onPoolUpdate(msg.Id, nil)
	case *proto.RouteUpdate:
		a.onRouteUpdate(ip.MustParseCIDROrIP(msg.Dst), msg)
	case *proto.WorkloadEndpointUpdate:
		a.onWorkloadEndpointUpdate(msg)
	case *proto.WorkloadEndpointRemove:
		a.onWorkloadEndpointRemoved(msg)
	case *proto.RouteRemove:
		a.onRouteUpdate(ip.MustParseCIDROrIP(msg.Dst), nil)
	case *ifaceUpdate:
		a.onIfaceUpdate(msg)
	case *ifaceAddrsUpdate:
		a.onIfaceAddrsUpdate(msg)
	}
}

func (a *awsIPManager) OnSecondaryIfaceStateUpdate(msg *aws.LocalAWSNetworkState) {
	if reflect.DeepEqual(msg, a.awsState) {
		// The AWS provisioner resends the snapshot after each timed recheck; avoid a dataplane update
		// in that case.
		logrus.WithField("awsState", msg).Debug("Received AWS state update with no changes.")
		return
	}
	logrus.WithField("awsState", msg).Debug("Received AWS state update.")
	a.queueDataplaneResync("AWS fabric updated")
	a.awsState = msg
}

func (a *awsIPManager) onPoolUpdate(id string, pool *proto.IPAMPool) {
	// Update the index from subnet ID to pool ID.  We do this first so we can look up the
	// old version of the pool (if any).
	oldSubnetID := ""
	newSubnetID := ""
	if oldPool := a.poolsByID[id]; oldPool != nil {
		oldSubnetID = oldPool.AwsSubnetId
	}
	if pool != nil {
		newSubnetID = pool.AwsSubnetId
	}
	if oldSubnetID != "" && oldSubnetID != newSubnetID {
		// Old AWS subnet is no longer correct. clean up the index.
		logrus.WithFields(logrus.Fields{
			"oldSubnet": oldSubnetID,
			"newSubnet": newSubnetID,
			"pool":      id,
		}).Info("IP pool no longer associated with AWS subnet.")
		a.poolIDsBySubnetID[oldSubnetID].Discard(id)
		if a.poolIDsBySubnetID[oldSubnetID].Len() == 0 {
			delete(a.poolIDsBySubnetID, oldSubnetID)
		}
		a.queueAWSResync("IP pool change (old AWS subnet removed)")
	}
	if newSubnetID != "" && oldSubnetID != newSubnetID {
		logrus.WithFields(logrus.Fields{
			"oldSubnet": oldSubnetID,
			"newSubnet": newSubnetID,
			"pool":      id,
		}).Info("IP pool now associated with AWS subnet.")
		if _, ok := a.poolIDsBySubnetID[newSubnetID]; !ok {
			a.poolIDsBySubnetID[newSubnetID] = set.New()
		}
		a.poolIDsBySubnetID[newSubnetID].Add(id)
		a.queueAWSResync("IP pool change (new AWS subnet added)")
	}

	// Store off the pool update itself. We store all pools because we need them to configure the correct
	// routes in the dataplane.
	if pool == nil {
		delete(a.poolsByID, id)
	} else {
		a.poolsByID[id] = pool
	}
	a.queueDataplaneResync("IP pool change")
}

func (a *awsIPManager) onRouteUpdate(dst ip.CIDR, route *proto.RouteUpdate) {
	if route != nil && !route.LocalWorkload {
		route = nil
	}
	if route != nil && route.AwsSubnetId == "" {
		route = nil
	}
	if dst.Version() != 4 || dst.Prefix() != 32 {
		// Don't think we get IPv6 routes from the calc graph but we're not ready for them.  All local workload
		// routes are forced to be /32s by validation.
		// FIXME IPv6
		logrus.Debug("Ignoring non-IPv4 or non /32 route")
		return
	}

	// Update the index from subnet ID to route dest.  We do this first so we can look up the
	// old version of the route (if any).
	oldSubnetID := ""
	newSubnetID := ""

	if oldRoute := a.localAWSRoutesByDst[dst]; oldRoute != nil {
		oldSubnetID = oldRoute.AwsSubnetId
	}
	if route != nil {
		newSubnetID = route.AwsSubnetId
	}

	if oldSubnetID != "" && oldSubnetID != newSubnetID {
		// Old AWS subnet is no longer correct. clean up the index.
		a.localRouteDestsBySubnetID[oldSubnetID].Discard(dst)
		if a.localRouteDestsBySubnetID[oldSubnetID].Len() == 0 {
			delete(a.localRouteDestsBySubnetID, oldSubnetID)
		}
		a.queueAWSResync("route subnet changed")
	}
	if newSubnetID != "" && oldSubnetID != newSubnetID {
		if _, ok := a.localRouteDestsBySubnetID[newSubnetID]; !ok {
			a.localRouteDestsBySubnetID[newSubnetID] = set.New()
		}
		a.localRouteDestsBySubnetID[newSubnetID].Add(dst)
		a.queueAWSResync("route subnet added")
	}

	// Save off the route itself.
	if route == nil {
		if _, ok := a.localAWSRoutesByDst[dst]; !ok {
			return // Not a route we were tracking.
		}
		a.queueAWSResync("route deleted")
		delete(a.localAWSRoutesByDst, dst)
	} else {
		a.localAWSRoutesByDst[dst] = route
		a.queueAWSResync("route updated")
	}
}

func (a *awsIPManager) onIfaceUpdate(msg *ifaceUpdate) {
	// Keep track of what interfaces we've seen so we can trigger a resync if we're waiting for a new
	// ENI to show up.
	if msg.State == "" {
		// Interface deleted.
		delete(a.ifaceNameToIfaceIdx, msg.Name)
	} else if a.ifaceNameToIfaceIdx[msg.Name] != msg.Index {
		// New interface.
		a.ifaceNameToIfaceIdx[msg.Name] = msg.Index
		if !a.allAWSIfacesFound {
			logrus.WithField("update", msg).Debug(
				"New interface appeared while waiting for AWS ENI to appear.")
			a.queueDataplaneResync("New interface appeared")
			return
		}
	}
	if _, ok := a.ifaceNameToPrimaryIP[msg.Name]; ok && msg.State != ifacemonitor.StateUp {
		// Interface that we've already matched with AWS changed state.
		logrus.WithField("update", msg).Debug("Secondary ENI state changed.")
		a.queueDataplaneResync("Interface changed state")
	}
}

func (a *awsIPManager) onIfaceAddrsUpdate(msg *ifaceAddrsUpdate) {
	if expAddr, ok := a.ifaceNameToPrimaryIP[msg.Name]; ok && msg.Addrs != nil {
		// This is an interface that we care about.  Check if the address it has corresponds with what we want.
		logrus.WithField("update", msg).Debug("Secondary ENI addrs changed.")
		seenExpected := false
		seenUnexpected := false
		msg.Addrs.Iter(func(item interface{}) error {
			addrStr := item.(string)
			if strings.Contains(addrStr, ":") {
				return nil // Ignore IPv6
			}
			if expAddr == addrStr {
				seenExpected = true
			} else {
				seenUnexpected = true
			}
			return nil
		})
		if !seenExpected || seenUnexpected {
			a.queueDataplaneResync("IPs out of sync on a secondary interface " + msg.Name)
		}
	}
}

func (a *awsIPManager) onWorkloadEndpointUpdate(msg *proto.WorkloadEndpointUpdate) {
	wepID := *msg.Id
	newEP := awsEndpointInfo{
		IPv4Nets:   parseCIDRSlice(msg.Endpoint.Ipv4Nets),
		ElasticIPs: parseIPSlice(msg.Endpoint.AwsElasticIps),
	}
	logCtx := logrus.WithFields(logrus.Fields{
		"id":    wepID,
		"newEP": newEP,
	})
	changed := a.onWorkloadUpdateOrRemove(logCtx, wepID, &newEP)
	if changed {
		logCtx.Debug("Workload endpoint with elastic IPs updated.")
		a.queueAWSResync("workload update")
	}
}

func (a *awsIPManager) onWorkloadEndpointRemoved(msg *proto.WorkloadEndpointRemove) {
	logCtx := logrus.WithField("id", *msg.Id)
	changed := a.onWorkloadUpdateOrRemove(logCtx, *msg.Id, nil)
	if changed {
		logCtx.Debug("Workload endpoint with elastic IPs removed.")
		a.queueAWSResync("workload removed")
	}
}

func (a *awsIPManager) onWorkloadUpdateOrRemove(logCtx *logrus.Entry, wepID proto.WorkloadEndpointID, newEP *awsEndpointInfo) (changed bool) {
	oldEP := a.workloadEndpointsByID[wepID]
	var newEIPs []ip.Addr
	if newEP == nil {
		delete(a.workloadEndpointsByID, wepID)
	} else {
		a.workloadEndpointsByID[wepID] = *newEP
		newEIPs = newEP.ElasticIPs
	}
	if reflect.DeepEqual(&oldEP, newEP) {
		logCtx.Debug("No-op WEP update, ignoring.")
		return false
	}
	if len(oldEP.ElasticIPs) == 0 && len(newEIPs) == 0 {
		logCtx.Debug("WEP has no elastic IPs, ignoring.")
		return false
	}
	if len(oldEP.ElasticIPs) > 0 {
		for _, cidr := range oldEP.IPv4Nets {
			a.workloadEndpointIDsByCIDR[cidr].Discard(wepID)
			if a.workloadEndpointIDsByCIDR[cidr].Len() == 0 {
				delete(a.workloadEndpointIDsByCIDR, cidr)
			}
		}
	}
	if len(newEIPs) > 0 {
		for _, cidr := range newEP.IPv4Nets {
			if a.workloadEndpointIDsByCIDR[cidr] == nil {
				a.workloadEndpointIDsByCIDR[cidr] = set.New()
			}
			a.workloadEndpointIDsByCIDR[cidr].Add(wepID)
		}
	}
	return true
}

func parseIPSlice(ips []string) (addrs []ip.Addr) {
	for _, addr := range ips {
		parsedAddr := ip.FromString(addr)
		if parsedAddr == nil {
			logrus.WithField("rawAddr", addr).Warn("Failed to parse elastic IP.")
			continue
		}
		addrs = append(addrs, parsedAddr)
	}
	return
}

func parseCIDRSlice(cidrs []string) (addrs []ip.CIDR) {
	for _, addr := range cidrs {
		parsedAddr, err := ip.ParseCIDROrIP(addr)
		if err != nil {
			logrus.WithField("rawAddr", addr).Warn("Failed to parse elastic IP.")
			continue
		}
		addrs = append(addrs, parsedAddr)
	}
	return
}

func (a *awsIPManager) lookUpElasticIPs(privIP ip.CIDR) []ip.Addr {
	weps := a.workloadEndpointIDsByCIDR[privIP]
	if weps == nil {
		return nil
	}

	// It's possible that multiple local pods transiently share an IP address.  Deal with that by
	// returning the intersection of their elastic IPs.  That way we only assign IPs that are valid for all
	// pods sharing the IP.
	var elasticIPs set.Set
	weps.Iter(func(item interface{}) error {
		wepID := item.(proto.WorkloadEndpointID)
		wep := a.workloadEndpointsByID[wepID]
		elasticIPsThisWEP := set.New()
		for _, eip := range wep.ElasticIPs {
			if elasticIPs != nil && !elasticIPs.Contains(eip) {
				logrus.WithFields(logrus.Fields{
					"elasticIP": eip.String(),
					"endpoints": weps,
				}).Warn("Multiple local endpoints share a private IP but have different Elastic IP " +
					"configuration.  Ignoring Elastic IP.")
				continue
			}
			elasticIPsThisWEP.Add(eip)
		}
		elasticIPs = elasticIPsThisWEP
		return nil
	})

	// Convert back to slice.
	var elasticIPsSlice []ip.Addr
	elasticIPs.Iter(func(item interface{}) error {
		addr := item.(ip.Addr)
		elasticIPsSlice = append(elasticIPsSlice, addr)
		return nil
	})

	// Sort for determinism in tests.
	sort.Slice(elasticIPsSlice, func(i, j int) bool {
		return elasticIPsSlice[i].AsBinary() < elasticIPsSlice[j].AsBinary()
	})

	return elasticIPsSlice
}

func (a *awsIPManager) queueAWSResync(reason string) {
	if a.awsResyncNeeded {
		return
	}
	logrus.WithField("reason", reason).Debug("AWS resync needed")
	a.awsResyncNeeded = true
}

func (a *awsIPManager) queueDataplaneResync(reason string) {
	if a.dataplaneResyncNeeded {
		return
	}
	logrus.WithField("reason", reason).Debug("Dataplane resync needed")
	a.dataplaneResyncNeeded = true
}

func (a *awsIPManager) CompleteDeferredWork() error {
	if a.awsResyncNeeded {
		// Datastore has been updated, send a new snapshot to the background thread.  It will configure the AWS
		// fabric appropriately and then send us a SecondaryIfaceState.
		ds := aws.DatastoreState{
			LocalAWSAddrsByDst: map[ip.Addr]aws.AddrInfo{},
			PoolIDsBySubnetID:  map[string]set.Set{},
		}
		for k, v := range a.localAWSRoutesByDst {
			ds.LocalAWSAddrsByDst[k.Addr()] = aws.AddrInfo{
				AWSSubnetId: v.AwsSubnetId,
				Dst:         v.Dst,
				ElasticIPs:  a.lookUpElasticIPs(k),
			}
		}
		for k, v := range a.poolIDsBySubnetID {
			ds.PoolIDsBySubnetID[k] = v.Copy()
		}
		a.ifaceProvisioner.OnDatastoreUpdate(ds)
		a.awsResyncNeeded = false
	}

	if a.dataplaneResyncNeeded {
		err := a.resyncWithDataplane()
		if err != nil {
			return err
		}
		a.dataplaneResyncNeeded = false
	}

	return nil
}

func (a *awsIPManager) resyncWithDataplane() error {
	if a.awsState == nil {
		logrus.Debug("No AWS information yet, not syncing dataplane.")
		return nil
	}
	logrus.Debug("Syncing dataplane secondary ENIs.")
	a.opRecorder.RecordOperation("aws-dataplane-sync")

	// Find all the local NICs and match them up with AWS ENIs.
	ifaces, err := a.nl.LinkList()
	if err != nil {
		return fmt.Errorf("failed to load local interfaces: %w", err)
	}
	activeRules := set.New() /* awsRuleKey */
	activeIfaceNames := set.New()
	var finalErr error

	for _, iface := range ifaces {
		// Skip NICs that don't match anything in AWS.
		mac := iface.Attrs().HardwareAddr.String()
		awsENI, awsENIExists := a.awsState.SecondaryENIsByMAC[mac]
		if !awsENIExists {
			continue
		}
		ifaceName := iface.Attrs().Name
		logrus.WithFields(logrus.Fields{
			"mac":      mac,
			"name":     ifaceName,
			"awsENIID": awsENI.ID,
		}).Debug("Matched local NIC with AWS ENI.")
		activeIfaceNames.Add(ifaceName)

		// Make sure we know the primary ENI's MTU.
		if a.primaryIfaceMTU == 0 {
			mtu, err := a.findPrimaryInterfaceMTU(ifaces)
			if err != nil {
				return err
			}
			logrus.WithField("mtu", mtu).Info("Found primary interface MTU.")
			a.primaryIfaceMTU = mtu
		}

		// Enable the NIC and configure its IPs.
		priAddrStr := awsENI.PrimaryIPv4Addr.String()
		a.ifaceNameToPrimaryIP[ifaceName] = priAddrStr
		err := a.configureNIC(iface, ifaceName, priAddrStr)
		if err != nil {
			finalErr = err
		}

		// Program routes into the NIC-specific routing table.
		rt := a.getOrAllocRoutingTable(ifaceName)
		a.programIfaceRoutes(rt, ifaceName)

		// Accumulate routing rules for all the active IPs.
		a.addIfaceActiveRules(activeRules, awsENI, rt.Index())
	}

	// Record whether we still need to match some interfaces.
	a.allAWSIfacesFound = len(a.awsState.SecondaryENIsByMAC) == activeIfaceNames.Len()

	// Scan for entries in ifaceNameToPrimaryIP that are no longer needed.  We don't bother to remove IPs from
	// interfaces that no longer have a corresponding AWS ENI because the only time that happens is if the ENI
	// is being deleted anyway.
	a.cleanUpPrimaryIPs(activeIfaceNames)

	// Scan for routing tables that are no longer needed.
	a.cleanUpRoutingTables(activeIfaceNames)

	// Queue up delta updates to add/remove routing rules.
	a.updateRouteRules(activeRules)

	return finalErr
}

var (
	errPrimaryMTUNotFound  = errors.New("failed to find primary interface MTU")
	errPrimaryIfaceZeroMTU = errors.New("primary interface had 0 MTU")
)

func (a *awsIPManager) findPrimaryInterfaceMTU(ifaces []netlink.Link) (int, error) {
	for _, iface := range ifaces {
		mac := iface.Attrs().HardwareAddr.String()
		if mac == a.awsState.PrimaryENIMAC {
			// Found the primary interface.
			if iface.Attrs().MTU == 0 { // defensive
				return 0, errPrimaryIfaceZeroMTU
			}
			return iface.Attrs().MTU, nil
		}
	}
	return 0, errPrimaryMTUNotFound
}

func (a *awsIPManager) cleanUpPrimaryIPs(matchedNICs set.Set) {
	if matchedNICs.Len() != len(a.ifaceNameToPrimaryIP) {
		// Clean up primary IPs of interfaces that no longer exist.
		for iface := range a.ifaceNameToPrimaryIP {
			if matchedNICs.Contains(iface) {
				continue
			}
			delete(a.ifaceNameToPrimaryIP, iface)
		}
	}
}

// configureNIC Brings the given NIC up and ensures it has the expected IP assigned.
func (a *awsIPManager) configureNIC(iface netlink.Link, ifaceName string, primaryIPStr string) error {
	a.opRecorder.RecordOperation("aws-configure-" + ifaceName)
	if iface.Attrs().MTU != a.primaryIfaceMTU {
		// Set the MTU on the link to match the MTU of the primary ENI.  This ensures that we don't flap the
		// detected host MTU by bringing up the new NIC.
		err := a.nl.LinkSetMTU(iface, a.primaryIfaceMTU)
		if err != nil {
			logrus.WithError(err).WithField("name", ifaceName).Error("Failed to set secondary ENI MTU.")
			return err
		}
	}

	if iface.Attrs().OperState != netlink.OperUp {
		err := a.nl.LinkSetUp(iface)
		if err != nil {
			logrus.WithError(err).WithField("name", ifaceName).Error("Failed to set secondary ENI MTU 'up'")
			return err
		}
	}
	addrs, err := a.nl.AddrList(iface, netlink.FAMILY_V4)
	if err != nil {
		logrus.WithError(err).WithField("name", ifaceName).Error("Failed to query interface addrs.")
		return err
	}

	var finalErr error

	// Remove any left-over proxy ARP entries that we previously added for ENI-per-workload mode.
	neighs, err := a.nl.NeighList(iface.Attrs().Index, netlink.FAMILY_V4)
	if err != nil {
		logrus.WithError(err).Error("Failed to query netlink for proxy ARP entries.")
		finalErr = err
	}
	primaryNetIP := net.ParseIP(primaryIPStr)
	for _, n := range neighs {
		if n.Flags&netlink.NTF_PROXY != 0 {
			if a.dpConfig.AWSSecondaryIPSupport == v3.AWSSecondaryIPEnabledENIPerWorkload &&
				n.IP.Equal(primaryNetIP) {
				continue
			}

			logrus.WithFields(logrus.Fields{
				"addr":  n.IP.String(),
				"iface": iface.Attrs().Name,
			}).Info("Found left-over proxy ARP entry; removing.")
			err := a.nl.NeighDel(&n)
			if err != nil {
				logrus.WithError(err).WithField("entry", n).Warn(
					"Failed to clean up unwanted proxy ARP entry.")
				finalErr = err
			}
		}
	}

	if a.dpConfig.AWSSecondaryIPSupport == v3.AWSSecondaryIPEnabledENIPerWorkload {
		// The primary IP of the interface belongs to a workload. Configure the host to respond to ARPs even
		// though it doesn't own the IP.
		logrus.Debug("In ENI-per-workload mode.  Adding proxy ARP entry to interface.")
		err := a.nl.NeighSet(&netlink.Neigh{
			LinkIndex: iface.Attrs().Index,
			Family:    netlink.FAMILY_V4,
			Flags:     netlink.NTF_PROXY,
			IP:        primaryNetIP,
		})
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"name": ifaceName,
				"addr": primaryIPStr,
			}).Error("Failed to set a proxy ARP entry for workload IP.")
			finalErr = err
		}
		for _, addr := range addrs {
			// Unexpected address.
			err := a.nl.AddrDel(iface, &addr)
			if err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"name": ifaceName,
					"addr": a,
				}).Error("Failed to clean up old address.")
				finalErr = err
			}
		}
	} else { // v3.AWSSecondaryIPEnabled: secondary IP per workload mode.
		// Make sure the interface has its primary IP.  This is needed for ARP to work.
		logrus.Debug("In secondary IP-per-workload mode.  Adding primary IP to interface.")
		foundPrimaryIP := false

		// Add the primary address as a /32 so that we don't automatically get routes for the subnet in the
		// main routing table.  We need to add the subnet routes to a custom routing table so that they're only
		// used for traffic that belongs on the secondary ENI.
		newAddr, err := a.nl.ParseAddr(primaryIPStr + "/32")
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"name": ifaceName,
				"addr": primaryIPStr,
			}).Error("Failed to parse address.")
			return fmt.Errorf("failed to parse AWS primary IP of secondary ENI %q: %w", primaryIPStr, err)
		}
		// Set the primary address to link scope so the kernel will only pick it for communication on the same
		// subnet.
		newAddr.Scope = int(netlink.SCOPE_LINK)

		for _, addr := range addrs {
			if addr.Equal(*newAddr) {
				foundPrimaryIP = true
				continue
			}

			// Unexpected address.
			err := a.nl.AddrDel(iface, &addr)
			if err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"name": ifaceName,
					"addr": a,
				}).Error("Failed to clean up old address.")
				finalErr = err
			}
		}

		if foundPrimaryIP {
			return nil
		}

		err = a.nl.AddrAdd(iface, newAddr)
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"name": ifaceName,
				"addr": newAddr,
			}).Error("Failed to add new primary IP to secondary interface.")
			finalErr = err
		} else {
			logrus.WithError(err).WithFields(logrus.Fields{
				"name": ifaceName,
				"addr": newAddr,
			}).Info("Added primary address to secondary ENI.")
		}
	}
	return finalErr
}

// addIfaceActiveRules adds awsRuleKey values to activeRules according to the secondary IPs of the AWS ENI.
func (a *awsIPManager) addIfaceActiveRules(activeRules set.Set, awsENI aws.Iface, routingTableID int) {
	// Send traffic from the primary IP of the interface to the dedicated routing table.
	// This is needed because:
	// - We want the primary IP of the ENI to be able to reach remote IPs within the
	//   subnet.
	// - We avoid programming the subnet's route into the main routing table to avoid
	//   routing traffic sourced from the primary ENI's IP over the secondary ENI.
	// - Instead we program the subnet route into the dedicated routing table.
	activeRules.Add(awsRuleKey{
		srcAddr:        awsENI.PrimaryIPv4Addr,
		routingTableID: routingTableID,
	})

	for _, privateIP := range awsENI.SecondaryIPv4Addrs {
		logrus.WithFields(logrus.Fields{"addr": privateIP, "rtID": routingTableID}).Debug("Adding routing rule.")
		activeRules.Add(awsRuleKey{
			srcAddr:        privateIP,
			routingTableID: routingTableID,
		})
	}
}

// programIfaceRoutes updates the routing table for the given interface with the correct routes.
func (a *awsIPManager) programIfaceRoutes(rt routeTable, ifaceName string) {
	// Add a default route via the AWS subnet's gateway.  This is how traffic to the outside world gets
	// routed properly.
	routes := []routetable.Target{
		{
			// Make whole subnet reachable on the link.  This allows for host-to-remote pod traffic using
			// the primary IP of the interface.
			Type: routetable.TargetTypeLinkLocalUnicast,
			CIDR: a.awsState.SubnetCIDR,
		},
		{
			// With gateway via the gateway address.
			Type: routetable.TargetTypeGlobalUnicast,
			CIDR: ip.MustParseCIDROrIP("0.0.0.0/0"),
			GW:   a.awsState.GatewayAddr,
		},
	}
	rt.SetRoutes(ifaceName, routes)

	// Add narrower routes for Calico IP pools that throw the packet back to the main routing tables.
	// this is required to make RPF checks pass when traffic arrives from a Calico tunnel going to an
	// AWS-networked pod.
	var noIFRoutes []routetable.Target
	for _, pool := range a.poolsByID {
		if pool.AwsSubnetId != "" {
			// AWS-backed traffic can flow over the ENI.  (It's not clear what the use case would be for
			// egress gateway to egress gateway or egress gateway to host traffic would be but it seems
			// like the right thing to do.)
			continue
		}
		noIFRoutes = append(noIFRoutes, routetable.Target{
			Type: routetable.TargetTypeThrow,
			CIDR: ip.MustParseCIDROrIP(pool.Cidr),
		})
	}
	rt.SetRoutes(routetable.InterfaceNone, noIFRoutes)
}

// cleanUpRoutingTables scans routeTableIndexByIfaceName for routing tables that are no longer needed (i.e. no
// longer appear in activeIfaceNames and releases them.
func (a *awsIPManager) cleanUpRoutingTables(activeIfaceNames set.Set) {
	for ifaceName, rt := range a.routeTablesByIfaceName {
		if activeIfaceNames.Contains(ifaceName) {
			continue // NIC is known to AWS and the local dataplane.  All good.
		}

		// NIC must have existed before but it no longer does.  Flush any routes from its routing table.
		rt.SetRoutes(ifaceName, nil)
		rt.SetRoutes(routetable.InterfaceNone, nil)

		// Only delete from the a.routeTablesByIfaceName map.  This means that the routing table will live
		// on in a.routeTablesByTableIdx until we reuse its index.  We want the table to live on so that
		// it has a chance to actually apply the flush.  We use a LIFO queue when allocating table indexes so
		// the routing table will be overwritten as soon as a new interface is added.
		delete(a.routeTablesByIfaceName, ifaceName)
		// Free the index so it can be reused.
		a.releaseRoutingTableID(rt.Index())
	}
}

// updateRouteRules calculates route rule deltas between the active rules and the set of rules that we've
// previously programmed.  It sends those to the RouteRules instance.
func (a *awsIPManager) updateRouteRules(activeRuleKeys set.Set /* awsRulesKey */) {
	for k, r := range a.routeRulesInDataplane {
		if activeRuleKeys.Contains(k) {
			continue // Route was present and still wanted; nothing to do.
		}
		// Route no longer wanted, clean it up.
		a.routeRules.RemoveRule(r)
		delete(a.routeRulesInDataplane, k)
	}
	activeRuleKeys.Iter(func(item interface{}) error {
		k := item.(awsRuleKey)
		if _, ok := a.routeRulesInDataplane[k]; ok {
			return nil // Route already present.  Nothing to do.
		}
		rule := routerule.
			NewRule(4, a.dpConfig.AWSSecondaryIPRoutingRulePriority).
			MatchSrcAddress(k.srcAddr.AsCIDR().ToIPNet()).
			GoToTable(k.routingTableID)
		a.routeRules.SetRule(rule)
		a.routeRulesInDataplane[k] = rule
		return nil
	})
}

func (a *awsIPManager) getOrAllocRoutingTable(ifaceName string) routeTable {
	if rt, ok := a.routeTablesByIfaceName[ifaceName]; !ok {
		logrus.WithField("ifaceName", ifaceName).Info("Making routing table for AWS interface.")
		tableIndex := a.claimTableID()
		rt = a.newRouteTable(
			[]string{"^" + regexp.QuoteMeta(ifaceName) + "$", routetable.InterfaceNone},
			4,
			false,
			a.dpConfig.NetlinkTimeout,
			nil,
			a.dpConfig.DeviceRouteProtocol,
			true,
			tableIndex,
			a.opRecorder,
		)
		a.routeTablesByIfaceName[ifaceName] = rt
		a.routeTablesByTableIdx[tableIndex] = rt
	}
	return a.routeTablesByIfaceName[ifaceName]
}

func (a *awsIPManager) claimTableID() int {
	// We use a LIFO queue so that we reuse table indexes eagerly.  This prevents us from allocating more
	// routing tables than needed.
	lastIdx := len(a.freeRouteTableIndexes) - 1
	idx := a.freeRouteTableIndexes[lastIdx]
	a.freeRouteTableIndexes = a.freeRouteTableIndexes[:lastIdx]
	return idx
}

func (a *awsIPManager) releaseRoutingTableID(id int) {
	a.freeRouteTableIndexes = append(a.freeRouteTableIndexes, id)
}

func (a *awsIPManager) GetRouteTableSyncers() []routeTableSyncer {
	var rts []routeTableSyncer
	for _, t := range a.routeTablesByTableIdx {
		rts = append(rts, t)
	}
	return rts
}

func (a *awsIPManager) GetRouteRules() []routeRules {
	return []routeRules{a.routeRules}
}

var _ Manager = (*awsIPManager)(nil)
var _ ManagerWithRouteRules = (*awsIPManager)(nil)
var _ ManagerWithRouteTables = (*awsIPManager)(nil)

type routeRulesNewFn func(
	ipVersion int,
	priority int,
	tableIndexSet set.Set,
	updateFunc routerule.RulesMatchFunc,
	removeFunc routerule.RulesMatchFunc,
	netlinkTimeout time.Duration,
	newNetlinkHandle func() (routerule.HandleIface, error),
	opRecorder logutils.OpRecorder,
) (routeRules, error)

type routeTableNewFn func(
	interfaceRegexes []string,
	ipVersion uint8,
	vxlan bool,
	netlinkTimeout time.Duration,
	deviceRouteSourceAddress net.IP,
	deviceRouteProtocol netlink.RouteProtocol,
	removeExternalRoutes bool,
	tableIndex int,
	opReporter logutils.OpRecorder,
) routeTable

type awsNetlinkIface interface {
	LinkList() ([]netlink.Link, error)
	LinkSetMTU(iface netlink.Link, mtu int) error
	LinkSetUp(iface netlink.Link) error
	AddrList(iface netlink.Link, v4 int) ([]netlink.Addr, error)
	AddrDel(iface netlink.Link, n *netlink.Addr) error
	AddrAdd(iface netlink.Link, addr *netlink.Addr) error
	ParseAddr(s string) (*netlink.Addr, error)
	NeighSet(neigh *netlink.Neigh) error
	NeighDel(neigh *netlink.Neigh) error
	NeighList(linkIndex, family int) ([]netlink.Neigh, error)
}

func realRouteRuleNew(
	version int,
	priority int,
	indexSet set.Set,
	updateFunc routerule.RulesMatchFunc,
	removeFunc routerule.RulesMatchFunc,
	timeout time.Duration,
	handle func() (routerule.HandleIface, error),
	recorder logutils.OpRecorder,
) (routeRules, error) {
	return routerule.New(version, indexSet, updateFunc, removeFunc, timeout, handle, recorder)
}

func realRouteTableNew(
	interfaceRegexes []string,
	ipVersion uint8,
	vxlan bool,
	netlinkTimeout time.Duration,
	deviceRouteSourceAddress net.IP,
	deviceRouteProtocol netlink.RouteProtocol,
	removeExternalRoutes bool,
	tableIndex int,
	opReporter logutils.OpRecorder,
) routeTable {
	return routetable.New(interfaceRegexes, ipVersion, vxlan, netlinkTimeout, deviceRouteSourceAddress,
		deviceRouteProtocol, removeExternalRoutes, tableIndex, opReporter)
}

type awsRealNetlink struct{}

func (a awsRealNetlink) ParseAddr(s string) (*netlink.Addr, error) {
	return netlink.ParseAddr(s)
}

func (a awsRealNetlink) LinkSetMTU(iface netlink.Link, mtu int) error {
	return netlink.LinkSetMTU(iface, mtu)
}

func (a awsRealNetlink) LinkSetUp(iface netlink.Link) error {
	return netlink.LinkSetUp(iface)
}

func (a awsRealNetlink) AddrList(iface netlink.Link, v int) ([]netlink.Addr, error) {
	return netlink.AddrList(iface, v)
}

func (a awsRealNetlink) AddrDel(iface netlink.Link, n *netlink.Addr) error {
	return netlink.AddrDel(iface, n)
}

func (a awsRealNetlink) AddrAdd(iface netlink.Link, addr *netlink.Addr) error {
	return netlink.AddrAdd(iface, addr)
}

func (a awsRealNetlink) LinkList() ([]netlink.Link, error) {
	return netlink.LinkList()
}

func (a awsRealNetlink) NewHandle() (routerule.HandleIface, error) {
	return netlink.NewHandle(syscall.NETLINK_ROUTE)
}

func (a awsRealNetlink) NeighSet(neigh *netlink.Neigh) error {
	return netlink.NeighSet(neigh)
}

func (a awsRealNetlink) NeighDel(neigh *netlink.Neigh) error {
	return netlink.NeighDel(neigh)
}

func (a awsRealNetlink) NeighList(linkIndex, family int) ([]netlink.Neigh, error) {
	return netlink.NeighList(linkIndex, family)
}
