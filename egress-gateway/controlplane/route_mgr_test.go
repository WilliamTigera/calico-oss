package controlplane

import (
	"context"
	"net"
	"syscall"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/clock"

	"github.com/projectcalico/calico/egress-gateway/controlplane/mock"
	"github.com/projectcalico/calico/egress-gateway/netlinkshim"
	mocknetlink "github.com/projectcalico/calico/egress-gateway/netlinkshim/mock"
	netutil "github.com/projectcalico/calico/egress-gateway/util/net"
	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/libcalico-go/lib/health"
)

const (
	dummyIfaceName = "dummyvxlan0"
	vni            = 4097
)

// TestProgramsKernel ensures routeManager correctly programs kernel routes/L2, and updates existing entries
func TestProgramsKernel(test *testing.T) {
	RegisterTestingT(test)
	log.SetLevel(log.DebugLevel)

	log.Info("creating mock route store...")
	routesByWorkloadCIDR := make(map[string]*proto.RouteUpdate)
	routesByWorkloadCIDR["10.0.1.0/24"] = &proto.RouteUpdate{
		Types:       proto.RouteType_REMOTE_WORKLOAD,
		IpPoolType:  proto.IPPoolType_NO_ENCAP,
		Dst:         "10.0.1.0/24",
		DstNodeName: "example.foo",
		DstNodeIp:   "192.168.1.1",
	}
	gatewayUpdate := proto.RouteUpdate{
		Types:       proto.RouteType_LOCAL_WORKLOAD,
		IpPoolType:  proto.IPPoolType_NO_ENCAP,
		Dst:         "10.0.2.0/31",
		DstNodeName: "example.local",
		DstNodeIp:   "192.168.2.2",
	}
	store := mock.Store{
		WorkloadsByDst: routesByWorkloadCIDR,
		GatewayUpdate:  &gatewayUpdate,
	}

	log.Info("initialising mock netlink handle...")
	nl := mocknetlink.New()
	link := createDummyLink(test, nl)
	defer destroyDummyLink(test, nl, link)

	log.Info("adding mock default route...")
	createDefaultLinkAndRoute(test, nl) // necessary for route manager init
	healthAgg := health.NewHealthAggregator()
	routeManager := NewRouteManager(
		store,
		dummyIfaceName,
		vni,
		healthAgg,
		OptNetlink(nl),
	)

	log.Info("starting route manager...")
	ctx := context.Background()
	go routeManager.Start(ctx)
	log.Info("notifying route manager of store resync...")
	routeManager.NotifyResync(store)

	// allow the routeManager some time to program the 'kernel'. (nice to have: a subscription for route updates)
	time.Sleep(1 * time.Second)
	l3ExistsForRouteUpdate(test, nl, link, routesByWorkloadCIDR["10.0.1.0/24"])
	l2ExistsForRouteUpdate(test, nl, link, routesByWorkloadCIDR["10.0.1.0/24"], "192.168.1.1")

	// reset mock netlink metrics for the next test
	nl.ResetDeltas()

	// delete a route and add a new one
	delete(store.WorkloadsByDst, "10.0.1.0/24")
	store.WorkloadsByDst["10.0.2.0/24"] = &proto.RouteUpdate{
		Types:       proto.RouteType_REMOTE_WORKLOAD,
		IpPoolType:  proto.IPPoolType_NO_ENCAP,
		Dst:         "10.0.2.0/24",
		DstNodeName: "example.foo",
		DstNodeIp:   "192.168.1.1",
	}
	routeManager.NotifyResync(store)

	// the route manager should have deleted a stale route, and added a new one
	time.Sleep(1 * time.Second)
	l3ExistsForRouteUpdate(test, nl, link, routesByWorkloadCIDR["10.0.2.0/24"])
	l2ExistsForRouteUpdate(test, nl, link, routesByWorkloadCIDR["10.0.2.0/24"], "192.168.1.1")
	Expect(len(nl.DeletedRoutesByKey)).To(BeEquivalentTo(1))
	Expect(len(nl.RoutesByKey)).To(BeEquivalentTo(2)) // one programmed by routemananger, plus the default route
	Expect(len(nl.NeighsByKey)).To(BeEquivalentTo(2)) // one ARP and one FDB entry
	// the underlying node of the old and new routes are the same, so we expect no change to L2
	Expect(len(nl.UpdatedNeighsByKey)).To(BeEquivalentTo(0))
	Expect(len(nl.DeletedNeighsByKey)).To(BeEquivalentTo(0))

	// reset mock netlink metrics for the next test
	nl.ResetDeltas()
	log.Info("making a change that should result in a route and neigh update...")
	// change the node that the IP pool lives on
	store.WorkloadsByDst["10.0.2.0/24"].DstNodeIp = "192.168.1.2"
	// routemanager should update a stale neighs, and update the gateway of the 10.0.2.0/24 route
	routeManager.NotifyResync(store)
	time.Sleep(1 * time.Second)
	l3ExistsForRouteUpdate(test, nl, link, routesByWorkloadCIDR["10.0.2.0/24"])
	l2ExistsForRouteUpdate(test, nl, link, routesByWorkloadCIDR["10.0.2.0/24"], "192.168.1.2")

	Expect(len(nl.DeletedRoutesByKey)).To(BeEquivalentTo(0))
	Expect(len(nl.UpdatedRoutesByKey)).To(BeEquivalentTo(1))
	Expect(len(nl.DeletedNeighsByKey)).To(BeEquivalentTo(2))
	Expect(len(nl.UpdatedNeighsByKey)).To(BeEquivalentTo(4))
}

// TestHandlesFailures ensures that if a transient netlink error occurrs, the manager will handle the error gracefully and queue a retry
func TestHandlesFailures(test *testing.T) {
	log.SetLevel(log.DebugLevel)
	RegisterTestingT(test)

	log.Info("creating mock route store...")
	routesByWorkloadCIDR := make(map[string]*proto.RouteUpdate)
	routesByWorkloadCIDR["10.0.1.0/24"] = &proto.RouteUpdate{
		Types:       proto.RouteType_REMOTE_WORKLOAD,
		IpPoolType:  proto.IPPoolType_NO_ENCAP,
		Dst:         "10.0.1.0/24",
		DstNodeName: "example.foo",
		DstNodeIp:   "192.168.1.1",
	}
	gatewayUpdate := proto.RouteUpdate{
		Types:       proto.RouteType_LOCAL_WORKLOAD,
		IpPoolType:  proto.IPPoolType_NO_ENCAP,
		Dst:         "10.0.2.0/31",
		DstNodeName: "example.local",
		DstNodeIp:   "192.168.2.2",
	}
	store := mock.Store{
		WorkloadsByDst: routesByWorkloadCIDR,
		GatewayUpdate:  &gatewayUpdate,
	}

	log.Info("initialising mock netlink handle...")
	nl := mocknetlink.New()
	createDefaultLinkAndRoute(test, nl)
	link := createDummyLink(test, nl)
	defer destroyDummyLink(test, nl, link)

	//nolint:staticcheck // Ignore SA1019 deprecated
	backoffMgr := wait.NewJitteredBackoffManager(5*time.Second, 0, clock.RealClock{})
	healthAgg := health.NewHealthAggregator()
	routeManager := NewRouteManager(
		store,
		dummyIfaceName,
		vni,
		healthAgg,
		OptNetlink(nl),
		OptBackoffManager(backoffMgr),
	)

	log.Info("starting route manager...")
	ctx := context.Background()
	go routeManager.Start(ctx)

	// simulate a failure when the routemanager tries to add a route
	nl.Failures |= mocknetlink.OpRouteAdd

	// fire routemanager - it *should* encounter an error
	log.Info("notifying route manager of store resync...")
	routeManager.NotifyResync(store)

	// wait a time *less* than the manager's retry time to ensure the retry interval actually works
	time.Sleep(3 * time.Second)

	log.Info("ensuring route manager waits before retrying...")
	Expect(len(nl.RoutesByKey)).To(BeEquivalentTo(1)) // only the default route should be present

	time.Sleep(3 * time.Second)
	// the retry should have occurred now
	Expect(len(nl.RoutesByKey)).To(BeEquivalentTo(2)) // default route plus the route programmed should now be present
}

// TestHandlesTunnels ensures routeManager correctly programs escape routes and FDB entries pointing to host-ns tunnels
func TestHandlesTunnels(test *testing.T) {
	RegisterTestingT(test)
	log.SetLevel(log.DebugLevel)

	log.Info("creating mock route store...")
	routesByWorkloadCIDR := make(map[string]*proto.RouteUpdate)
	tunnelsByDst := make(map[string]*proto.RouteUpdate)
	routesByWorkloadCIDR["10.0.1.0/24"] = &proto.RouteUpdate{
		Types:       proto.RouteType_REMOTE_WORKLOAD,
		IpPoolType:  proto.IPPoolType_NO_ENCAP,
		Dst:         "10.0.1.0/24",
		DstNodeName: "example.foo",
		DstNodeIp:   "192.168.1.1",
	}
	// add in a wireguard-ish tunnel update with an overlapping Dst CIDR
	tunnelsByDst["10.0.1.23/32"] = &proto.RouteUpdate{
		Types: proto.RouteType_REMOTE_TUNNEL,
		TunnelType: &proto.TunnelType{
			Wireguard: true,
		},
		IpPoolType:  proto.IPPoolType_IPIP,
		Dst:         "10.0.1.23/32",
		DstNodeName: "example.foo",
		DstNodeIp:   "192.168.1.1",
	}
	// also add a wireguard tunnel on the "gateway's host" so it thinks the node's are peered
	tunnelsByDst["10.0.3.1/32"] = &proto.RouteUpdate{
		Types: proto.RouteType_LOCAL_TUNNEL,
		TunnelType: &proto.TunnelType{
			Wireguard: true,
		},
		IpPoolType:  proto.IPPoolType_IPIP,
		Dst:         "10.0.3.1/32",
		DstNodeName: "example.local",
		DstNodeIp:   "192.168.2.2",
	}
	gatewayUpdate := proto.RouteUpdate{
		Types:       proto.RouteType_LOCAL_WORKLOAD,
		IpPoolType:  proto.IPPoolType_NO_ENCAP,
		Dst:         "10.0.2.0/31",
		DstNodeName: "example.local",
		DstNodeIp:   "192.168.2.2",
	}
	store := mock.Store{
		WorkloadsByDst: routesByWorkloadCIDR,
		TunnelsByDst:   tunnelsByDst,
		GatewayUpdate:  &gatewayUpdate,
	}

	log.Info("initialising mock netlink handle...")
	nl := mocknetlink.New()
	link := createDummyLink(test, nl)
	defer destroyDummyLink(test, nl, link)

	log.Info("adding mock default route...")
	defaultLink, defaultRoute := createDefaultLinkAndRoute(test, nl) // necessary for route manager init
	healthAgg := health.NewHealthAggregator()
	routeManager := NewRouteManager(
		store,
		dummyIfaceName,
		vni,
		healthAgg,
		OptNetlink(nl),
	)

	log.Info("starting route manager...")
	ctx := context.Background()
	go routeManager.Start(ctx)
	log.Info("notifying route manager of store resync...")
	routeManager.NotifyResync(store)

	// allow the routeManager some time to program the 'kernel'. (nice to have: a subscription for route updates)
	time.Sleep(1 * time.Second)
	l3ExistsForRouteUpdate(test, nl, link, routesByWorkloadCIDR["10.0.1.0/24"])
	exitRouteExistsForRouteUpdate(test, nl, defaultLink, defaultRoute.Gw.String(), tunnelsByDst["10.0.1.23/32"])
	l2ExistsForRouteUpdate(test, nl, link, routesByWorkloadCIDR["10.0.1.0/24"], "10.0.1.23")
}

func createDefaultLinkAndRoute(test *testing.T, nl netlinkshim.Handle) (netlink.Link, netlink.Route) {
	log.Info("creating dummy default link...")

	linkAttrs := netlink.NewLinkAttrs()
	linkAttrs.Name = "eth0"
	link := &mocknetlink.MockLink{
		LinkAttrs: linkAttrs,
	}
	err := nl.LinkAdd(link)
	if err != nil {
		test.Fatalf("could not add mock default link for test: %v", err)
	}

	route := netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       nil,
		Gw:        net.ParseIP("169.254.1.1"),
		Table:     254,
	}

	err = nl.RouteAdd(&route)
	if err != nil {
		test.Fatalf("could not add mock default route for test: %v", err)
	}

	return link, route
}

func createDummyLink(test *testing.T, nl netlinkshim.Handle) *netlink.Vxlan {
	log.Info("creating dummy link...")

	la := netlink.NewLinkAttrs()
	la.Name = dummyIfaceName
	link := &netlink.Vxlan{
		LinkAttrs: la,
		Port:      4790,
		VxlanId:   vni,
	}
	err := nl.LinkAdd(link)
	if err != nil {
		test.Fatalf("test couldn't add mock interface to kernel: %v", err)
		return nil
	}
	err = nl.LinkSetUp(link)
	if err != nil {
		test.Fatalf("test couldn't set link to UP: %v", err)
		return nil
	}

	return link
}

func destroyDummyLink(test *testing.T, nl netlinkshim.Handle, link *netlink.Vxlan) {
	log.Info("destroying dummy link...")
	err := nl.LinkDel(link)
	if err != nil {
		log.Infof("Could not destroy dummy link: %v", err)
	}
}

// l3ExistsForRouteUpdate checks the existence of a routeUpdate's parsed data in the kernel's L3 tables
func l3ExistsForRouteUpdate(test *testing.T, nl netlinkshim.Handle, link netlink.Link, expected *proto.RouteUpdate) {
	log.Info("verifying route was programmed...")
	kernelRoutes, err := nl.RouteList(link, netlink.FAMILY_V4)
	if err != nil {
		test.Errorf("test failed to fetch link routes: %v", err)
		return
	}

	Expect(len(kernelRoutes)).NotTo(BeEquivalentTo(0))

	log.Debugf("currently-programmed kernel routes: %+v", kernelRoutes)

	for _, kr := range kernelRoutes {
		if kr.Dst != nil && kr.Dst.String() == expected.Dst {
			log.Info("found kernel route with expected destination, verifying gateway...")
			actualGW := kr.Gw.String()
			expectedGW := expected.DstNodeIp
			Expect(actualGW).To(BeEquivalentTo(expectedGW))
			return
		}
	}

	test.Errorf("route for destination %s not found", expected.Dst)
}

// exitRouteExistsForRouteUpdate checks the existence of a routeUpdate's parsed data in the kernel's L3 tables
func exitRouteExistsForRouteUpdate(test *testing.T, nl netlinkshim.Handle, link netlink.Link, defaultGW string, expected *proto.RouteUpdate) {
	log.Info("verifying exit route was programmed...")
	kernelRoutes, err := nl.RouteList(link, netlink.FAMILY_V4)
	if err != nil {
		test.Errorf("test failed to fetch link exit routes: %+v", err)
		return
	}

	Expect(len(kernelRoutes)).NotTo(BeEquivalentTo(0))

	for _, kr := range kernelRoutes {
		if kr.Dst != nil && kr.Dst.String() == expected.Dst {
			log.Info("found kernel exit route with expected destination, verifying gateway...")
			exitGW := kr.Gw.String()
			Expect(exitGW).To(BeEquivalentTo(defaultGW))
			return
		}
	}

	test.Errorf("route for destination %s not found", expected.Dst)
}

// l2ExistsForRouteUpdate checks the existence of a routeUpdate's parsed data in the kernel's L2 tables (ARP and FDB)
func l2ExistsForRouteUpdate(test *testing.T, nl netlinkshim.Handle, link *netlink.Vxlan, expected *proto.RouteUpdate, bridgeIP string) {
	macBuilder := netutil.NewMACBuilder()
	log.Info("verifying neigh was programmed...")
	kernelNeighs, err := nl.NeighList(link.Index, netlink.FAMILY_ALL)
	if err != nil {
		test.Errorf("test failed to fetch link neighbours")
		return
	}

	Expect(len(kernelNeighs)).NotTo(BeEquivalentTo(0))

	log.Debugf("currently-programmed kernel neighs: %+v", kernelNeighs)

	foundARPEntry := false
	foundFDBEntry := false
	expectedMAC, err := macBuilder.GenerateMAC(expected.DstNodeName)
	if err != nil {
		test.Errorf("could not parse MAC address for expected route update")
	}
	for _, kn := range kernelNeighs {
		if kn.HardwareAddr.String() == expectedMAC.String() {
			if kn.Family == syscall.AF_BRIDGE {
				Expect(kn.IP.String()).To(Equal(bridgeIP))
				foundFDBEntry = true
			} else {
				Expect(kn.IP.String()).To(Equal(expected.DstNodeIp))
				foundARPEntry = true
			}
		}
	}

	Expect(foundARPEntry).To(BeTrue())
	Expect(foundFDBEntry).To(BeTrue())
}
