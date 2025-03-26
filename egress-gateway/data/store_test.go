package data

import (
	"context"
	"net"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/libcalico-go/lib/health"
)

// TestStoreWaitsTillInSync ensures no store consumers are notified of updates until the first in-sync msg of a connection is received
func TestStoreWaitsTillInSync(test *testing.T) {
	var store *routeStore
	RegisterTestingT(test)

	// our "gRPC" pipeline that the store will pull it's data from
	mockUpdatesChannel := make(chan *proto.ToDataplane)
	newMockUpdatesPipeline := func() <-chan *proto.ToDataplane {
		return mockUpdatesChannel
	}

	// some mock updates to send to the route store
	update1 := newRouteUpdate("10.0.0.1/0", "192.168.1.1", "foo.bar", proto.RouteType_REMOTE_WORKLOAD)
	update2 := newRouteUpdate("10.0.1.0/24", "192.168.1.2", "foo2.bar", proto.RouteType_LOCAL_WORKLOAD)
	update3 := newInSync()
	mockUpdates := []*proto.ToDataplane{update1, update2, update3}

	// instantiate the store and a mock observer to notify (observer registers itself with the store when constructed)
	aggregator := health.NewHealthAggregator()
	healthTimeout := 10 * time.Second
	store = NewRouteStore(newMockUpdatesPipeline, net.ParseIP("10.10.10.0"), aggregator, healthTimeout)
	observer := NewMockObserver(store)

	ctx := context.Background()
	go store.SyncForever(ctx)

	// begin sending updates - after each, check if the store notifies when it should
	for _, u := range mockUpdates {
		mockUpdatesChannel <- u
		time.Sleep(200 * time.Millisecond)
		if _, ok := u.Payload.(*proto.ToDataplane_InSync); ok {
			// the store has now been given an in-sync msg, so we expect it to have notified observers
			Eventually(observer.NumNotifications).Should(BeEquivalentTo(1))
		} else {
			Eventually(observer.NumNotifications).Should(BeZero())
		}
	}

	// the store should now believe it is in-sync, so every new update should notify observers
	update4 := newRouteUpdate("10.0.2.0/24", "192.168.1.3", "foo3.bar", proto.RouteType_REMOTE_WORKLOAD)
	mockUpdatesChannel <- update4
	time.Sleep(200 * time.Millisecond)
	Eventually(observer.NumNotifications).Should(BeEquivalentTo(2))

	update5 := newRouteRemove("10.0.0.1/0")
	mockUpdatesChannel <- update5
	time.Sleep(200 * time.Millisecond)
	Eventually(observer.NumNotifications).Should(BeEquivalentTo(3))
}

// TestStoreResyncsAfterClosedConnection ensures that when an updates channel is closed, the store fetches a new one, and resets its inSync status
func TestStoreResyncsAfterClosedConnection(test *testing.T) {
	var store *routeStore
	RegisterTestingT(test)

	// our "gRPC" pipeline that the store will pull it's data from
	numChannelRefreshes := 0
	mockUpdatesChannel := make(chan *proto.ToDataplane)
	newMockUpdatesPipeline := func() <-chan *proto.ToDataplane {
		numChannelRefreshes++
		mockUpdatesChannel = make(chan *proto.ToDataplane)
		return mockUpdatesChannel
	}

	// some mock updates to send to the route store
	updateInSync := newInSync()
	update2 := newRouteUpdate("10.0.0.1/0", "192.168.1.1", "foo.bar", proto.RouteType_REMOTE_WORKLOAD)

	// instantiate the store and a mock observer to notify (observer registers itself with the store when constructed)
	aggregator := health.NewHealthAggregator()
	healthTimeout := 10 * time.Second
	store = NewRouteStore(newMockUpdatesPipeline, net.ParseIP("10.10.10.0"), aggregator, healthTimeout)
	observer := NewMockObserver(store)

	ctx := context.Background()
	go store.SyncForever(ctx)
	time.Sleep(200 * time.Millisecond)
	Eventually(numChannelRefreshes).Should(BeIdenticalTo(1))

	mockUpdatesChannel <- updateInSync
	mockUpdatesChannel <- update2
	time.Sleep(200 * time.Millisecond)
	Eventually(observer.NumNotifications).Should(BeIdenticalTo(2))

	close(mockUpdatesChannel)
	// wait while the store refreshes its updates channel
	time.Sleep(200 * time.Millisecond)
	Eventually(numChannelRefreshes).Should(BeIdenticalTo(2))

	mockUpdatesChannel <- update2
	time.Sleep(200 * time.Millisecond)
	Eventually(observer.NumNotifications).Should(BeIdenticalTo(2)) // should not have changed since last time

	// now fire another inSync and see if the store notifies observers
	mockUpdatesChannel <- updateInSync
	time.Sleep(200 * time.Millisecond)
	Eventually(observer.NumNotifications).Should(BeIdenticalTo(3))
}

func newRouteUpdate(workloadCIDR, nodeIP, nodeName string, t proto.RouteType) *proto.ToDataplane {
	tdp := &proto.ToDataplane{}
	tdru := &proto.ToDataplane_RouteUpdate{}
	ru := proto.RouteUpdate{
		Types:         t,
		IpPoolType:    proto.IPPoolType_NO_ENCAP,
		Dst:           workloadCIDR,
		DstNodeName:   nodeName,
		DstNodeIp:     nodeIP,
		SameSubnet:    true,
		NatOutgoing:   false,
		LocalWorkload: false,
		TunnelType:    &proto.TunnelType{},
	}

	tdru.RouteUpdate = &ru
	tdp.Payload = tdru
	return tdp
}

func newInSync() *proto.ToDataplane {
	tdp := &proto.ToDataplane{}
	tdis := proto.ToDataplane_InSync{}
	is := proto.InSync{}

	tdis.InSync = &is
	tdp.Payload = &tdis

	return tdp
}

func newRouteRemove(dst string) *proto.ToDataplane {
	tdp := &proto.ToDataplane{}
	tdrr := proto.ToDataplane_RouteRemove{}
	rr := proto.RouteRemove{
		Dst: dst,
	}

	tdrr.RouteRemove = &rr
	tdp.Payload = &tdrr
	return tdp
}
