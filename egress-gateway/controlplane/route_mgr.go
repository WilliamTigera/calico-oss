// Package controlplane is responsible for programming the kernel.
// In the case of the egress gateway, this means maintaining the
// machine's ARP and routing tables.
package controlplane

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/clock"

	"github.com/projectcalico/calico/egress-gateway/data"
	"github.com/projectcalico/calico/egress-gateway/netlinkshim"
	netutil "github.com/projectcalico/calico/egress-gateway/util/net"
	netlinkutil "github.com/projectcalico/calico/egress-gateway/util/netlink"
	protoutil "github.com/projectcalico/calico/egress-gateway/util/proto"
	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/libcalico-go/lib/health"
)

// RouteManager is responsible for programming return-routes back to workloads over VXLAN.
// After VXLAN-encap, packets are sent back to the originating workload via that workload's host.
// The MAC address of the returning VXLAN packets targets the host-ns 'egress.calico' VTEP which performs de-encap
// The dest-IP of the returning VXLAN packet may target either the host itself, or a host-ns tunnel device, e.g 'wireguard.cali'
type RouteManager struct {
	macBuilder     *netutil.MACBuilder // 'egress.calico' MAC addresses are programmatically generated using a builder
	backoffManager wait.BackoffManager
	healthAgg      *health.HealthAggregator
	// latest snapshot of the datastore's route updates - old updates are overwritten if a new one comes in
	latestUpdate chan data.RouteStore

	// can refer to an actual kernel netlink, or to a mock
	nlHandle netlinkshim.Handle

	// the VXLAN device used for tunnelling to/from cluster hosts
	egressTunnelIface netlink.Link
	vni               int

	// default interface of the egress gateway workload, normally 'eth0' or similar
	defaultRouteIface   netlink.Link
	defaultRouteGateway net.IP

	// Routes to cluster workloads which force matching packets through the gateway's VXLAN device for encap.
	// packets to a workload are sent via that workload's host, where VXLAN de-encap happens
	egressTunnelWorkloadRoutesByDstCIDR map[string]netlink.Route

	// ARP entries for neighbouring hosts. The MAC addresses of these entries target host 'egress.calico' devices (for de-encap)
	egressTunnelNeighsByKey map[string]netlink.Neigh

	// FDB entries for this gateway's VXLAN device. The IPs here represent the dst-IP for the final VXLAN packet.
	// These could be the actual host IP's, or tunnel IP's in the host-ns
	egressTunnelBridgeNeighsByKey map[string]netlink.Neigh

	// Some host-ns device IPs (such as WireGuard tunnel IPs) can overlap with workload CIDRs.
	// To avoid infinite VXLAN-encapping loops (and martian packets), we must program explicit exit routes for them
	exitRoutesByDstCIDR map[string]netlink.Route
}

// backoff defaults
const (
	backoffDuration = 10 * time.Second
	jitter          = 0.1
)

const healthName = "Dataplane"

// NewRouteManager constructs a new route manager, registered with RouteStore 's', which will tunnel packets through the provided interface
func NewRouteManager(s data.RouteStore, egressTunnelIfaceName string, vni int, healthAgg *health.HealthAggregator, opts ...RouteManagerOpt) *RouteManager {
	healthAgg.RegisterReporter(healthName, &health.HealthReport{Ready: true}, 0)
	m := &RouteManager{
		macBuilder: netutil.NewMACBuilder(),
		//nolint:staticcheck // Ignore SA1019 deprecated
		backoffManager:                      wait.NewJitteredBackoffManager(backoffDuration, jitter, clock.RealClock{}),
		latestUpdate:                        make(chan data.RouteStore, 1), // channel of size one, if it gets full, older updates will be discarded
		egressTunnelWorkloadRoutesByDstCIDR: make(map[string]netlink.Route),
		egressTunnelNeighsByKey:             make(map[string]netlink.Neigh),
		egressTunnelBridgeNeighsByKey:       make(map[string]netlink.Neigh),
		exitRoutesByDstCIDR:                 make(map[string]netlink.Route),
		healthAgg:                           healthAgg,
	}

	// apply options
	for _, o := range opts {
		o(m)
	}

	if m.vni == 0 {
		m.vni = vni
	}

	if m.nlHandle == nil {
		nl, err := netlink.NewHandle(syscall.NETLINK_GENERIC)
		if err != nil {
			logrus.Fatalf("error while fetching netlink handle: %v", err)
		}
		m.nlHandle = nl
	}

	defaultRoute, err := netutil.GetDefaultRoute(m.nlHandle)
	if err != nil {
		logrus.Fatalf("error fetching default routing from kernel: %v", err)
	}

	if m.defaultRouteGateway == nil {
		m.defaultRouteGateway = defaultRoute.Gw
	}

	if m.defaultRouteIface == nil {
		iface, err := m.nlHandle.LinkByIndex(defaultRoute.LinkIndex)
		if err != nil {
			logrus.Fatalf("error fetching default interface from kernel: %v", err)
		}
		m.defaultRouteIface = iface
	}

	if m.egressTunnelIface == nil {
		// find the designated link
		link, err := m.nlHandle.LinkByName(egressTunnelIfaceName)
		if err != nil {
			logrus.Fatalf("could not find tunnel interface '%s'", egressTunnelIfaceName)
		}
		m.egressTunnelIface = link
	}

	// subscribe to datastore updates
	s.Subscribe(m)
	logrus.Debugf("new RouteManager constructed: %+v\n", m)
	return m
}

// RouteManagerOpt functions are passed to and executed by NewRouteManager - can be used to customise the RouteManager
type RouteManagerOpt func(*RouteManager)

// OptMockNetlink builds a RouteManagerOpt that replaces the netlink handle being used
func OptNetlink(n netlinkshim.Handle) RouteManagerOpt {
	return func(m *RouteManager) {
		m.nlHandle = n
	}
}

// OptBackoffManager allows for custom retry/backoff timings
func OptBackoffManager(bom wait.BackoffManager) RouteManagerOpt {
	return func(m *RouteManager) {
		m.backoffManager = bom
	}
}

// Start starts the main route manager process, running a reconciliation loop between datastore config and the kernel
func (m *RouteManager) Start(ctx context.Context) {
	// Prepare a timer and timer-notification channel for backoff-retries after kernel failures.
	var backoffTimer clock.Timer
	var retry <-chan time.Time

	// boilerplate function for cancelling / resetting a backoff timer
	stopBackoffTimer := func() {
		if backoffTimer != nil {
			// we must stop the timer and drain the notification channel
			if !backoffTimer.Stop() {
				<-backoffTimer.C()
			}
			backoffTimer = nil
			retry = nil
		}
	}
	defer stopBackoffTimer()

	// begin our kernel-reconcilliation loop
	var s data.RouteStore
	var incompleteTunnelUpdateKeys, incompleteWorkloadUpdateKeys []string
	var hadIncompleteTunnelUpdates, hadIncompleteWorkloadUpdates bool
	for {
		// track if the kernel is successfully sync'd for each iteration
		var inSync bool
		select {
		case <-ctx.Done():
			m.healthAgg.Report(healthName, &health.HealthReport{Ready: false})
			return
		// pull in the latest snapshot of the routeStore if it exists, to build netlink structs from its routes
		case s = <-m.latestUpdate:
			// in order to maintain symmetrical traffic paths over all types of cluster encap,
			// we must know what tunnel device IP's to target when sending back packets
			thisWorkload, workloadsByNodeName, tunnelsByNodeName := s.Routes()
			if thisWorkload == nil {
				logrus.Error("could not find RouteUpdate for this egress-gateway workload")
				inSync = false
				break
			}

			activeTunnelsByNodeName := calculateActiveTunnelsForNodes(
				thisWorkload,
				workloadsByNodeName,
				tunnelsByNodeName,
			)

			// now that we know what src-IP outbound egress packets will be coming in with,
			// we can begin building routes back to the nodes via the same tunnels/IP's.
			// we'll first create default routes and FDB entries for remote tunnels
			var fdbNeighsByHWAddr map[string]netlink.Neigh
			var exitRoutesByDstCIDR map[string]netlink.Route
			fdbNeighsByHWAddr, exitRoutesByDstCIDR, incompleteTunnelUpdateKeys = m.createTunnelNeighsAndRoutes(activeTunnelsByNodeName)

			// fill in remaining FDB entries, ARP entries, and encap routes,
			var routesByDstCIDR map[string]netlink.Route
			var arpNeighsByKey, fdbNeighsByKey map[string]netlink.Neigh
			routesByDstCIDR, arpNeighsByKey, fdbNeighsByKey, incompleteWorkloadUpdateKeys = m.createWorkloadNeighsAndRoutes(workloadsByNodeName, fdbNeighsByHWAddr)

			// overwrite route manager state in prep for ensuring networking
			m.exitRoutesByDstCIDR = exitRoutesByDstCIDR
			m.egressTunnelWorkloadRoutesByDstCIDR = routesByDstCIDR
			m.egressTunnelNeighsByKey = arpNeighsByKey
			m.egressTunnelBridgeNeighsByKey = fdbNeighsByKey

			// attempt to program the new routemanager state to the kernel
			inSync = m.ensureNetworking()
		// if a retry timer has fired, attempt to ensure networking without a new update
		case <-retry:
			// first discard the stopped timer (calling stop again would otherwise block forever)
			retry = nil
			backoffTimer = nil
			logrus.Info("retrying kernel sync...")
			inSync = m.ensureNetworking()
		}
		m.healthAgg.Report(healthName, &health.HealthReport{Ready: inSync})

		// either an update or retry has occurred so our retry timer is now stale
		stopBackoffTimer()

		// if the kernel is still not inSync, queue a retry
		if !inSync {
			// calling backoff returns the timer set to go off at the next wait-interval according to the backoff manager
			backoffTimer = m.backoffManager.Backoff()
			retry = backoffTimer.C() // get our notification channel for that timer to watch in parallel with new updates
		} else {
			// Only bother to fire this log if we're not in a retry loop (avoiding spam).
			if hasIncompleteTunnelUpdates := len(incompleteTunnelUpdateKeys) > 0; hasIncompleteTunnelUpdates {
				logrus.WithField("updates", incompleteTunnelUpdateKeys).Warn("Unable to set up tunnel routing to some nodes due to missing MAC or VTEP information. This should resolve when node / tunnel IPs and MAC addresses are allocated")
				hadIncompleteTunnelUpdates = true
			} else if hadIncompleteTunnelUpdates {
				hadIncompleteTunnelUpdates = false
				logrus.Info("Successfully resolved tunnel routing that had previously failed")
			}
			if hasIncompleteWorkloadUpdates := len(incompleteWorkloadUpdateKeys) > 0; hasIncompleteWorkloadUpdates {
				logrus.WithField("updates", incompleteWorkloadUpdateKeys).Warn("Unable to set up return-routing to workloads due to missing MAC or workload information. This should resolve when node / workload IPs and MAC addresses are allocated")
				hadIncompleteWorkloadUpdates = true
			} else if hadIncompleteWorkloadUpdates {
				hadIncompleteWorkloadUpdates = false
				logrus.Info("Successfully resolved workload return-routing that had previously failed")
			}
		}
	}
}

// NotifyResync is an implementation of the data.Observer interface - it queue's an update from the datastore, dropping any older, unhandled ones
func (m *RouteManager) NotifyResync(s data.RouteStore) {
	logrus.Info("route manager notified of datastore resync, queueing update...")

	select {
	case <-m.latestUpdate:
		logrus.Info("dropping stale store update")
	default:
	}

	m.latestUpdate <- s
}

// calculateActiveTunnelsForNodes guesses which tunnel on each node is most-likely to be used when sending traffic to this workload
// data is returned as a map of nodeName:tunnel key-pairs
func calculateActiveTunnelsForNodes(
	thisWorkload *proto.RouteUpdate,
	workloadsByNodeName map[string][]*proto.RouteUpdate,
	tunnelsByNodeName map[string][]*proto.RouteUpdate,
) map[string]*proto.RouteUpdate {
	gatewayEncapType := thisWorkload.IpPoolType // will the gateway receive packets that have been sNAT'd by the node's tunnel?
	gatewayNodeHasWireguard := false            // wireguard tunnels will take precedence if nodes are peered - we search for this device next

	// first check if the egress gateway's node has a wireguard device - if not, we can ignore all wireguard tunnels on other hosts
	gatewayNodeTunnels := tunnelsByNodeName[thisWorkload.DstNodeName]
	logrus.Debugf("searching gateway's host tunnels for wireguard devices: %+v", gatewayNodeTunnels)
	for _, tunnel := range gatewayNodeTunnels {
		if protoutil.IsWireguardTunnel(tunnel) {
			gatewayNodeHasWireguard = true
			logrus.Debug("wireguard device found on gateway host")
		}
	}

	// find the correct tunnel to target for each node
	activeTunnelsByNodeName := make(map[string]*proto.RouteUpdate)
	for nodeName, tunnels := range tunnelsByNodeName {
		// skip our local node
		if nodeName == thisWorkload.DstNodeName {
			continue
		}

		nodeWorkloads, ok := workloadsByNodeName[nodeName]
		// if there are no workloads for this node, skip it
		if !ok || len(nodeWorkloads) == 0 {
			logrus.Debugf("skipping tunnel checks for node '%s' due to no active workloads...", nodeName)
			continue
		}

		var activeTunnel *proto.RouteUpdate

		// guess which tunnel traffic will be arriving over
		// wireguard tunnels take precedence over overlay tunnels - if both are enabled, wireguard is used
		for _, tunnel := range tunnels {
			// this is our best guess that wireguard will be used
			if gatewayNodeHasWireguard && protoutil.IsWireguardTunnel(tunnel) {
				logrus.Debugf("found wireguard peer: %+v", tunnel)
				activeTunnel = tunnel
				break // highest priority tunnel matched, no need to check any more
			} else if gatewayEncapType == tunnel.IpPoolType {
				// the remote host has an encap tunnel, and our workload has encap enabled.
				// we must now check if our encap setting is cross-subnet, since a remote host
				// in our subnet will not use encap in that case
				if thisWorkload.SameSubnet {
					logrus.Debugf("egress-gateway ippool is in cross-subnet mode: %+v", thisWorkload)
					// we are in cross-subnet mode, is this tunnel across a subnet?
					// if it's in the same subnet, this tunnel won't be active; skip
					if tunnel.SameSubnet {
						logrus.Debugf("skipping tunnel in same subnet...")
						continue
					}
				}
				activeTunnel = tunnel
			}
		}
		logrus.Debugf("added tunnel %+v", activeTunnel)
		activeTunnelsByNodeName[nodeName] = activeTunnel
	}

	return activeTunnelsByNodeName
}

// createWorkloadNeighsAndRoutes creates all netlink objects necessary to route packets from this
// workload to all other workloads via VXLAN. Special FDB cases arising from the use of additional tunnels in neighbouring
// hosts are not covered, and are expected to be passed in via fdbNeighsByHWAddr
func (m *RouteManager) createWorkloadNeighsAndRoutes(
	workloadsByNodeName map[string][]*proto.RouteUpdate,
	fdbNeighsByHWAddr map[string]netlink.Neigh,
) (
	routesByDstCIDR map[string]netlink.Route,
	arpNeighsByKey map[string]netlink.Neigh,
	fdbNeighsByKey map[string]netlink.Neigh,
	incompleteUpdates []string,
) {
	// we now have any special-case routes/neighs created, so create the rest normally to fill in the blanks
	routesByDstCIDR = make(map[string]netlink.Route)
	arpNeighsByKey = make(map[string]netlink.Neigh)
	incompleteUpdates = make([]string, 0)
	for _, workloads := range workloadsByNodeName {
		for _, wl := range workloads {
			// create an ARP entry for this workload's host (ARP entries dont need to know about tunnels)
			arpNeigh, err := m.newNeigh(wl, false, netlink.FAMILY_V4)
			if err != nil {
				logrus.WithError(err).WithField("update", wl).Debug("could not parse ARP neigh from RouteUpdate")
				incompleteUpdates = append(incompleteUpdates, wl.Dst)
				continue
			} else {
				arpNeighsByKey[netlinkutil.KeyForNeigh(arpNeigh)] = arpNeigh
			}

			// fill in default FDB neighs if a tunnel entry isnt already added
			if _, ok := fdbNeighsByHWAddr[arpNeigh.HardwareAddr.String()]; !ok {
				fdbNeigh, err := m.newNeigh(wl, false, syscall.AF_BRIDGE)
				if err != nil {
					logrus.WithError(err).WithField("update", wl).Debug("could not parse FDB neigh from RouteUpdate")
					incompleteUpdates = appendUntil(incompleteUpdates, wl.Dst, 10)
					continue
				} else {
					fdbNeighsByHWAddr[fdbNeigh.HardwareAddr.String()] = fdbNeigh
				}
			}

			// finally, create the route that will encap returning egress packets for this CIDR
			encapRoute, err := m.newRoute(wl, false)
			if err != nil {
				logrus.WithError(err).WithField("update", wl).Debug("could not parse encap route from RouteUpdate")
				incompleteUpdates = appendUntil(incompleteUpdates, wl.Dst, 10)
				continue
			} else {
				routesByDstCIDR[wl.Dst] = encapRoute
			}
		}
	}

	// we originally keyed fdb neighs by hwaddr so that we would never have more than one entry per host
	// but to match how the dataplane keys neighs, we must now convert the map
	fdbNeighsByKey = make(map[string]netlink.Neigh)
	for _, n := range fdbNeighsByHWAddr {
		key := netlinkutil.KeyForNeigh(n)
		fdbNeighsByKey[key] = n
	}

	return routesByDstCIDR, arpNeighsByKey, fdbNeighsByKey, incompleteUpdates
}

func (m *RouteManager) createTunnelNeighsAndRoutes(activeTunnelsByNodeName map[string]*proto.RouteUpdate) (
	map[string]netlink.Neigh,
	map[string]netlink.Route,
	[]string,
) {
	fdbNeighsByHWAddr := make(map[string]netlink.Neigh)
	exitRoutesByDstCIDR := make(map[string]netlink.Route)
	var incompleteUpdates []string
	for _, tunnel := range activeTunnelsByNodeName {
		errorsOccurred := false
		if tunnel != nil {
			fdbNeigh, err := m.newNeigh(tunnel, true, syscall.AF_BRIDGE)
			if err != nil {
				logrus.WithError(err).WithField("update", tunnel).Debug("could not parse neigh from RouteUpdate")
				errorsOccurred = true
			} else {
				fdbNeighsByHWAddr[fdbNeigh.HardwareAddr.String()] = fdbNeigh
			}

			defaultRoute, err := m.newRoute(tunnel, true)
			if err != nil {
				logrus.WithError(err).WithField("update", tunnel).Debug("could not parse default route from RouteUpdate")
				errorsOccurred = true
			} else {
				exitRoutesByDstCIDR[tunnel.Dst] = defaultRoute
			}
		}

		if errorsOccurred {
			incompleteUpdates = appendUntil(incompleteUpdates, tunnel.Dst, 10)
		}
	}

	return fdbNeighsByHWAddr, exitRoutesByDstCIDR, incompleteUpdates
}

// newRoute creates a new netlink.Route from a Felix RouteUpdate
// isExitRoute determines whether the resulting route should route packets
// out of the machine, or through the encap device
func (m *RouteManager) newRoute(ru *proto.RouteUpdate, isExitRoute bool) (route netlink.Route, err error) {
	_, dst, err := net.ParseCIDR(ru.Dst)
	if err != nil {
		return route, err
	}

	// manually pushing packets through an IP-less VXLAN device is confusing for Linux...
	// 'onlink' lets Linux know this link will work for the given dest
	route.Flags = int(netlink.FLAG_ONLINK)
	route.Dst = dst

	if !isExitRoute {
		// requests to pod IP's will be routed via their node IP's VTEP
		nodeIP := net.ParseIP(ru.DstNodeIp)
		if nodeIP == nil {
			return route, fmt.Errorf("could not parse workload's node IP from value '%s'", ru.DstNodeIp)
		}
		route.Gw = nodeIP
		route.LinkIndex = m.egressTunnelIface.Attrs().Index
	} else {
		// prep an "exit-route" for known host-ns devices with workload-like IP's (like wireguard devices)
		// this avoids an infintite routing loop in scenarios where an encapped VXLAN packet has a dest IP from a workload CIDR block
		route.LinkIndex = m.defaultRouteIface.Attrs().Index
		route.Gw = m.defaultRouteGateway
	}
	return route, nil
}

// newNeigh creates a new netlink.Neigh from a RouteUpdate
// isHostTunnel flags whether the route update passed represents a host-ns tunnel device
// in that case, the returned neigh's IP will be that of the tunnel, rather than the node IP
func (m *RouteManager) newNeigh(ru *proto.RouteUpdate, isHostTunnel bool, family int) (neigh netlink.Neigh, err error) {
	neigh.LinkIndex = m.egressTunnelIface.Attrs().Index
	neigh.State = netlink.NUD_PERMANENT
	neigh.VNI = m.vni
	neigh.Family = family
	neigh.Flags = netlink.NTF_SELF

	mac, err := m.macBuilder.GenerateMAC(ru.DstNodeName)
	if err != nil {
		return neigh, fmt.Errorf("could not parse MAC address for node '%s': %w", ru.DstNodeName, err)
	}
	neigh.HardwareAddr = mac

	var nodeIP net.IP
	if isHostTunnel {
		nodeIP, _, err = net.ParseCIDR(ru.Dst)
		if err != nil {
			return neigh, err
		}

	} else {
		nodeIP = net.ParseIP(ru.DstNodeIp)
		if nodeIP == nil {
			return neigh, fmt.Errorf("could not parse workload's node IP from value '%s'", ru.DstNodeIp)
		}
	}
	neigh.IP = nodeIP

	return neigh, nil
}

// ensureNetworking attempts to ensure that kernel networking is inSync the manager's desired config, returns bool inSync indicating success or failure
func (m *RouteManager) ensureNetworking() (inSync bool) {
	logrus.Info("Attempting to ensure kernel networking...")
	inSync = true // assume the kernel will be in-sync, flip to false if we see a failure

	kernelOperations := sync.WaitGroup{}
	kernelOperations.Add(2)
	go func() {
		err := ensureNeighs(m.nlHandle, m.egressTunnelNeighsByKey, m.egressTunnelIface, false)
		if err != nil {
			logrus.WithError(err).Warn("error programming kernel ARP")
			inSync = false
		}

		err = ensureNeighs(m.nlHandle, m.egressTunnelBridgeNeighsByKey, m.egressTunnelIface, true)
		if err != nil {
			logrus.WithError(err).Warn("error programming kernel FDB")
			inSync = false
		}
		kernelOperations.Done()
	}()
	go func() {
		err := ensureRouting(m.nlHandle, m.egressTunnelWorkloadRoutesByDstCIDR, m.egressTunnelIface)
		if err != nil {
			logrus.WithError(err).Warn("error programming kernel with VXLAN routes")
			inSync = false
		}
		err = ensureRouting(m.nlHandle, m.exitRoutesByDstCIDR, m.defaultRouteIface)
		if err != nil {
			logrus.WithError(err).Warn("error programming kernel with node routes")
			inSync = false
		}
		kernelOperations.Done()
	}()
	// wait for L2 and L3 operations to finish
	kernelOperations.Wait()

	return inSync
}

// ensureRouting applies in-memory routes (built from store data) to the kernel, deleting stale ones
func ensureRouting(nl netlinkshim.Handle, routesByDstCIDR map[string]netlink.Route, iface netlink.Link) error {
	logrus.Debugf("ensuring kernel with routes: %+v", routesByDstCIDR)
	// get a list of kernel routes filtered by the output interface (e.g. vxlan0)
	ifaceAttrs := iface.Attrs()
	kernelRoutes, err := nl.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{
			LinkIndex: ifaceAttrs.Index,
		},
		netlink.RT_FILTER_OIF,
	)
	if err != nil {
		logrus.WithError(err).Errorf("could not list routes for iface '%s'", ifaceAttrs.Name)
		return err
	}

	// track the last error that occorred for the purpose of retries
	var lastErr error = nil

	// search for routes to delete, simultaneously map kernel routes by dest-CIDR
	kernelRoutesByDst := make(map[string]netlink.Route)
	for _, kr := range kernelRoutes {
		// ignore default routes
		if isDefaultCIDR(kr.Dst) {
			continue
		}
		dstCIDR := kr.Dst.String()
		if _, ok := routesByDstCIDR[dstCIDR]; !ok {
			// the route does not exist in memory, so delete it from the kernel
			if err = nl.RouteDel(&kr); err != nil {
				lastErr = err
				logrus.WithError(err).Warnf("could not delete stale route %+v", kr)
			}
		} else {
			// collect routes that have not been deleted
			kernelRoutesByDst[dstCIDR] = kr
		}
	}

	// program our desired state
	for dst, r := range routesByDstCIDR {
		kr, ok := kernelRoutesByDst[dst]
		// if the route already exists in the kernel, we must check if it's accurate
		if ok {
			if !kr.Equal(r) {
				if err := nl.RouteReplace(&r); err != nil {
					lastErr = err
					logrus.WithError(err).Warnf("could not rectify out-of-sync kernel route")
				}
			}
		} else { // if the route has not yet been programmed, add it
			if err = nl.RouteAdd(&r); err != nil {
				lastErr = err
				logrus.WithError(err).Warnf("could not program kernel with route %+v", r)
			}
		}
	}

	return lastErr
}

func isDefaultCIDR(dst *net.IPNet) bool {
	if dst == nil {
		return true
	}
	if ones, _ := dst.Mask.Size(); ones == 0 {
		return true
	}
	return false
}

// ensureNeighs applies in-memory link data to the kernel, updating or deleting stale kernel entries.
// fdb of true will target FDB/Bridge neigh's rather than ARP neigh's
func ensureNeighs(nl netlinkshim.Handle, neighsByKey map[string]netlink.Neigh, iface netlink.Link, fdb bool) error {
	logrus.Debugf("ensuring kernel with neighs: %+v", neighsByKey)

	family := netlink.FAMILY_V4
	if fdb {
		family = syscall.AF_BRIDGE
	}

	ifaceAttrs := iface.Attrs()
	kernelNeighs, err := nl.NeighList(
		ifaceAttrs.Index,
		family,
	)
	if err != nil {
		logrus.WithError(err).Errorf("error listing neighbors for iface '%s'", ifaceAttrs.Name)
		return err
	}

	// track the last error that occorred for the purpose of retries
	var lastErr error = nil

	// search for kernel neighbours to delete, simultaneously map neighbours by hardware addr
	kernelNeighsByKey := make(map[string]netlink.Neigh)
	for _, kn := range kernelNeighs {
		key := netlinkutil.KeyForNeigh(kn)
		if _, ok := neighsByKey[key]; !ok {
			// kernel neighbor is no longer in memory, delete
			if err := nl.NeighDel(&kn); err != nil {
				if !strings.Contains(err.Error(), "no such file or directory") {
					lastErr = err
					logrus.WithError(err).Warnf("Failed to delete ARP entry %+v", kn)
				}
			}
		} else {
			// collect remaining kernel neigh's which may need updating
			kernelNeighsByKey[key] = kn
		}
	}

	// now program desired state
	for key, n := range neighsByKey {
		// if the neigh doesn't exist in the kernel, or if an out-of-sync version does, program our in-memory version
		kn, ok := kernelNeighsByKey[key]
		if !ok || !netlinkutil.NeighsEqual(kn, n) {
			if err = nl.NeighSet(&n); err != nil {
				lastErr = err
				logrus.WithError(err).Warnf("could not program L2 neighbor: %+v", n)
			}
		}
	}

	return lastErr
}

// appendUntil appends to a slice until the length of the slice reaches 'limit'.
func appendUntil[Slice []Item, Item any](s Slice, i Item, limit int) Slice {
	if len(s) == limit {
		return s
	}

	return append(s, i)
}
