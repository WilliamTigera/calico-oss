// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package aws

import (
	"context"
	"net"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/transport/http"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/clock"

	"github.com/projectcalico/felix/ip"
	"github.com/projectcalico/felix/proto"
	"github.com/projectcalico/libcalico-go/lib/set"

	"github.com/projectcalico/libcalico-go/lib/health"
	cnet "github.com/projectcalico/libcalico-go/lib/net"
)

const (
	// We test from the point of view of "test-node" in the "us-west-1" AZ.

	nodeName   = "test-node"
	instanceID = "i-ca1ic000000000001"
	testVPC    = "vpc-01234567890123456"

	primaryENIID       = "eni-00000000000000001"
	primaryENIAttachID = "attach-00000000000000001"
	primaryENIMAC      = "00:00:00:00:00:01"

	azWest1 = "us-west-1"
	azWest2 = "us-west-2"

	// AWS Subnets that are in the same AZ as the test node. The node can only accept secondary interfaces
	// from these subnets.

	subnetIDWest1Calico    = "subnet-ca100000000000001"
	subnetIDWest1CalicoAlt = "subnet-ca100000000000011"
	subnetIDWest1Default   = "subnet-def00000000000001"

	// AWS subnet IDs for subnets in another AZ.  These should be ignored.

	subnetIDWest2Calico  = "subnet-ca100000000000002"
	subnetIDWest2Default = "subnet-def00000000000002"

	// CIDRs of the various subnets.

	subnetWest1CIDRCalico       = "100.64.1.0/24"
	subnetWest1CIDRCalicoAlt    = "100.64.3.0/24"
	subnetWest1GatewayCalico    = "100.64.1.1"
	subnetWest1GatewayCalicoAlt = "100.64.3.1"

	subnetWest2CIDRCalico = "100.64.2.0/24"

	// IPs that IPAM will hand out to hosts by default.

	calicoHostIP1 = "100.64.1.5"
	calicoHostIP2 = "100.64.1.6"

	// IP that we swap into IPAM when using the alternate IP pools.
	calicoHostIP1Alt = "100.64.3.5"

	// Workload addresses in the main and alternate pools.
	wl1Addr    = "100.64.1.64/32"
	wl1AddrAlt = "100.64.3.64/32"
	wl2Addr    = "100.64.1.65/32"

	// Workload from non-local subnet.
	west2WlIP = "100.64.2.5"

	// IP pool IDs.

	ipPoolIDWest1Hosts       = "pool-west-1-hosts"
	ipPoolIDWest1HostsAlt    = "pool-west-1-hosts-alt"
	ipPoolIDWest1Gateways    = "pool-west-1-gateways"
	ipPoolIDWest1GatewaysAlt = "pool-west-1-gateways-alt"

	ipPoolIDWest2Hosts    = "pool-west-2-hosts"
	ipPoolIDWest2Gateways = "pool-west-2-gateways"

	// t3LargeCapacity Expected secondary IP capacity of a t3.large instance.
	t3LargeCapacity = 22
)

var (
	// Parsed CIDr versions of the various IPs.

	wl1CIDR         = ip.MustParseCIDROrIP(wl1Addr)
	wl1CIDRAlt      = ip.MustParseCIDROrIP(wl1AddrAlt)
	wl2CIDR         = ip.MustParseCIDROrIP(wl2Addr)
	west2WlCIDR     = ip.MustParseCIDROrIP(west2WlIP)
	calicoHostCIDR1 = ip.MustParseCIDROrIP(calicoHostIP1)

	// Default set of IP pools that we use for simple tests.  Contains a host and workload pool for
	// the local same-AZ subnet and a remote one.
	defaultPools = map[string]set.Set{
		subnetIDWest1Calico: set.FromArray([]string{ipPoolIDWest1Hosts, ipPoolIDWest1Gateways}),
		subnetIDWest2Calico: set.FromArray([]string{ipPoolIDWest2Hosts, ipPoolIDWest2Gateways}),
	}
	// alternatePools is like defaultPools but it has a different local subnet and associated pools.
	// When switching from defaultPools to alternatePools we expect Felix to clean up the state assocaited
	// with the default pools.
	alternatePools = map[string]set.Set{
		subnetIDWest1CalicoAlt: set.FromArray([]string{ipPoolIDWest1HostsAlt, ipPoolIDWest1GatewaysAlt}),
		subnetIDWest2Calico:    set.FromArray([]string{ipPoolIDWest2Hosts, ipPoolIDWest2Gateways}),
	}
	// mixedPools has both local subnets so we can test what Felix does when there's a choice (which would
	// be a misconfiguration!)
	mixedPools = map[string]set.Set{
		subnetIDWest1Calico:    set.FromArray([]string{ipPoolIDWest1Hosts, ipPoolIDWest1Gateways}),
		subnetIDWest1CalicoAlt: set.FromArray([]string{ipPoolIDWest1HostsAlt, ipPoolIDWest1GatewaysAlt}),
		subnetIDWest2Calico:    set.FromArray([]string{ipPoolIDWest2Hosts, ipPoolIDWest2Gateways}),
	}

	// Canned datastore snapshots.

	noWorkloadDatastore = DatastoreState{
		LocalAWSRoutesByDst:       nil,
		LocalRouteDestsBySubnetID: nil,
		PoolIDsBySubnetID:         defaultPools,
	}
	noWorkloadDatastoreAltPools = DatastoreState{
		LocalAWSRoutesByDst:       nil,
		LocalRouteDestsBySubnetID: nil,
		PoolIDsBySubnetID:         alternatePools,
	}
	singleWorkloadDatastore = DatastoreState{
		LocalAWSRoutesByDst: map[ip.CIDR]*proto.RouteUpdate{
			wl1CIDR: {
				Dst:           wl1Addr,
				LocalWorkload: true,
				AwsSubnetId:   subnetIDWest1Calico,
			},
		},
		LocalRouteDestsBySubnetID: map[string]set.Set{
			subnetIDWest1Calico: set.FromArray([]ip.CIDR{wl1CIDR}),
		},
		PoolIDsBySubnetID: defaultPools,
	}
	twoWorkloadsDatastore = DatastoreState{
		LocalAWSRoutesByDst: map[ip.CIDR]*proto.RouteUpdate{
			wl1CIDR: {
				Dst:           wl1Addr,
				LocalWorkload: true,
				AwsSubnetId:   subnetIDWest1Calico,
			},
			wl2CIDR: {
				Dst:           wl2Addr,
				LocalWorkload: true,
				AwsSubnetId:   subnetIDWest1Calico,
			},
		},
		LocalRouteDestsBySubnetID: map[string]set.Set{
			subnetIDWest1Calico: set.FromArray([]ip.CIDR{wl1CIDR, wl2CIDR}),
		},
		PoolIDsBySubnetID: defaultPools,
	}
	// nonLocalWorkloadDatastore has one workload that's in the local subnet and one that is in
	// a subnet that's not in our AZ.
	nonLocalWorkloadDatastore = DatastoreState{
		LocalAWSRoutesByDst: map[ip.CIDR]*proto.RouteUpdate{
			wl1CIDR: {
				Dst:           wl1Addr,
				LocalWorkload: true,
				AwsSubnetId:   subnetIDWest1Calico,
			},
			west2WlCIDR: {
				Dst:           west2WlIP,
				LocalWorkload: true,
				AwsSubnetId:   subnetIDWest2Calico,
			},
		},
		LocalRouteDestsBySubnetID: map[string]set.Set{
			subnetIDWest1Calico: set.FromArray([]ip.CIDR{wl1CIDR}),
			subnetIDWest2Calico: set.FromArray([]ip.CIDR{west2WlCIDR}),
		},
		PoolIDsBySubnetID: defaultPools,
	}
	// mixedSubnetDatastore has two workloads, each of which is in a different subnet, both of which are
	// in our AZ.
	mixedSubnetDatastore = DatastoreState{
		LocalAWSRoutesByDst: map[ip.CIDR]*proto.RouteUpdate{
			wl1CIDR: {
				Dst:           wl1Addr,
				LocalWorkload: true,
				AwsSubnetId:   subnetIDWest1Calico,
			},
			west2WlCIDR: {
				Dst:           wl1AddrAlt,
				LocalWorkload: true,
				AwsSubnetId:   subnetIDWest1CalicoAlt,
			},
		},
		LocalRouteDestsBySubnetID: map[string]set.Set{
			subnetIDWest1Calico:    set.FromArray([]ip.CIDR{wl1CIDR}),
			subnetIDWest1CalicoAlt: set.FromArray([]ip.CIDR{wl1CIDRAlt}),
		},
		PoolIDsBySubnetID: mixedPools,
	}
	// hostClashWorkloadDatastore has a clash between a workload IP and the host IP that will be assigned to
	// the secondary ENI.
	hostClashWorkloadDatastore = DatastoreState{
		LocalAWSRoutesByDst: map[ip.CIDR]*proto.RouteUpdate{
			wl1CIDR: {
				Dst:           wl1Addr,
				LocalWorkload: true,
				AwsSubnetId:   subnetIDWest1Calico,
			},
			calicoHostCIDR1: {
				Dst:           calicoHostCIDR1.String(),
				LocalWorkload: true,
				AwsSubnetId:   subnetIDWest1Calico,
			},
		},
		LocalRouteDestsBySubnetID: map[string]set.Set{
			subnetIDWest1Calico: set.FromArray([]ip.CIDR{wl1CIDR}),
			subnetIDWest2Calico: set.FromArray([]ip.CIDR{west2WlCIDR}),
		},
		PoolIDsBySubnetID: defaultPools,
	}
	singleWorkloadDatastoreAltPool = DatastoreState{
		LocalAWSRoutesByDst: map[ip.CIDR]*proto.RouteUpdate{
			wl1CIDR: {
				Dst:           wl1AddrAlt,
				LocalWorkload: true,
				AwsSubnetId:   subnetIDWest1CalicoAlt,
			},
		},
		LocalRouteDestsBySubnetID: map[string]set.Set{
			subnetIDWest1CalicoAlt: set.FromArray([]ip.CIDR{wl1CIDRAlt}),
		},
		PoolIDsBySubnetID: alternatePools,
	}

	// Canned MAC addresses and IDs.  The fake EC2 allocates MACs in sequence so, by asserting the MAC and the
	// ENI ID we can be sure that the expected number of allocations took place (at the cost of having
	// different expected return values depending on how many have taken place).

	firstAllocatedENIID   = "eni-00000000000001000"
	firstAllocatedMAC, _  = net.ParseMAC("00:00:00:00:10:00")
	secondAllocatedENIID  = "eni-00000000000001001"
	secondAllocatedMAC, _ = net.ParseMAC("00:00:00:00:10:01")

	// Canned responses.

	responsePoolsNoENIs = &LocalAWSNetworkState{
		PrimaryENIMAC:      primaryENIMAC,
		SecondaryENIsByMAC: map[string]Iface{},
		SubnetCIDR:         ip.MustParseCIDROrIP(subnetWest1CIDRCalico),
		GatewayAddr:        ip.FromString(subnetWest1GatewayCalico),
	}
	responseSingleWorkload = &LocalAWSNetworkState{
		PrimaryENIMAC: primaryENIMAC,
		SecondaryENIsByMAC: map[string]Iface{
			firstAllocatedMAC.String(): {
				ID:                 firstAllocatedENIID,
				MAC:                firstAllocatedMAC,
				PrimaryIPv4Addr:    ip.FromString(calicoHostIP1),
				SecondaryIPv4Addrs: []ip.Addr{ip.MustParseCIDROrIP(wl1Addr).Addr()},
			},
		},
		SubnetCIDR:  ip.MustParseCIDROrIP(subnetWest1CIDRCalico),
		GatewayAddr: ip.FromString(subnetWest1GatewayCalico),
	}
	responseTwoWorkloads = &LocalAWSNetworkState{
		PrimaryENIMAC: primaryENIMAC,
		SecondaryENIsByMAC: map[string]Iface{
			firstAllocatedMAC.String(): {
				ID:              firstAllocatedENIID,
				MAC:             firstAllocatedMAC,
				PrimaryIPv4Addr: ip.FromString(calicoHostIP1),
				SecondaryIPv4Addrs: []ip.Addr{
					// Note: we assume the order here, which is only guaranteed if we first add wl1, then wl2.
					ip.MustParseCIDROrIP(wl1Addr).Addr(),
					ip.MustParseCIDROrIP(wl2Addr).Addr(),
				},
			},
		},
		SubnetCIDR:  ip.MustParseCIDROrIP(subnetWest1CIDRCalico),
		GatewayAddr: ip.FromString(subnetWest1GatewayCalico),
	}
	responseENIAfterWorkloadsDeleted = &LocalAWSNetworkState{
		PrimaryENIMAC: primaryENIMAC,
		SecondaryENIsByMAC: map[string]Iface{
			firstAllocatedMAC.String(): {
				ID:                 firstAllocatedENIID,
				MAC:                firstAllocatedMAC,
				PrimaryIPv4Addr:    ip.FromString(calicoHostIP1),
				SecondaryIPv4Addrs: nil,
			},
		},
		SubnetCIDR:  ip.MustParseCIDROrIP(subnetWest1CIDRCalico),
		GatewayAddr: ip.FromString(subnetWest1GatewayCalico),
	}
	responseSingleWorkloadOtherHostIP = &LocalAWSNetworkState{
		PrimaryENIMAC: primaryENIMAC,
		SecondaryENIsByMAC: map[string]Iface{
			firstAllocatedMAC.String(): {
				ID:                 firstAllocatedENIID,
				MAC:                firstAllocatedMAC,
				PrimaryIPv4Addr:    ip.FromString(calicoHostIP2), // Different IP
				SecondaryIPv4Addrs: []ip.Addr{ip.MustParseCIDROrIP(wl1Addr).Addr()},
			},
		},
		SubnetCIDR:  ip.MustParseCIDROrIP(subnetWest1CIDRCalico),
		GatewayAddr: ip.FromString(subnetWest1GatewayCalico),
	}

	responseAltPoolsNoENIs = &LocalAWSNetworkState{
		PrimaryENIMAC:      primaryENIMAC,
		SecondaryENIsByMAC: map[string]Iface{},
		SubnetCIDR:         ip.MustParseCIDROrIP(subnetWest1CIDRCalicoAlt),
		GatewayAddr:        ip.FromString(subnetWest1GatewayCalicoAlt),
	}
	responseAltPoolsAfterWorkloadsDeleted = &LocalAWSNetworkState{
		PrimaryENIMAC: primaryENIMAC,
		SecondaryENIsByMAC: map[string]Iface{
			secondAllocatedMAC.String(): {
				ID:                 secondAllocatedENIID,
				MAC:                secondAllocatedMAC,
				PrimaryIPv4Addr:    ip.FromString(calicoHostIP1Alt),
				SecondaryIPv4Addrs: nil,
			},
		},
		SubnetCIDR:  ip.MustParseCIDROrIP(subnetWest1CIDRCalicoAlt),
		GatewayAddr: ip.FromString(subnetWest1GatewayCalicoAlt),
	}
	responseAltPoolSingleWorkload = &LocalAWSNetworkState{
		PrimaryENIMAC: primaryENIMAC,
		SecondaryENIsByMAC: map[string]Iface{
			secondAllocatedMAC.String(): {
				ID:                 secondAllocatedENIID,
				MAC:                secondAllocatedMAC,
				PrimaryIPv4Addr:    ip.FromString(calicoHostIP1Alt),
				SecondaryIPv4Addrs: []ip.Addr{ip.MustParseCIDROrIP(wl1AddrAlt).Addr()},
			},
		},
		SubnetCIDR:  ip.MustParseCIDROrIP(subnetWest1CIDRCalicoAlt),
		GatewayAddr: ip.FromString(subnetWest1GatewayCalicoAlt),
	}
)

type sipTestFakes struct {
	IPAM      *fakeIPAM
	EC2       *fakeEC2
	Clock     *clock.FakeClock
	CapacityC chan SecondaryIfaceCapacities
}

func (f sipTestFakes) expectSingleBackoffAndStep() {
	// Initial backoff should be between 1000 and 1100 ms (due to jitter).
	Eventually(f.Clock.HasWaiters).Should(BeTrue())
	f.Clock.Step(999 * time.Millisecond)
	Expect(f.Clock.HasWaiters()).To(BeTrue())
	f.Clock.Step(102 * time.Millisecond)
	Expect(f.Clock.HasWaiters()).To(BeFalse())
}

func setup(t *testing.T) (*SecondaryIfaceProvisioner, *sipTestFakes) {
	RegisterTestingT(t)
	fakeIPAM := newFakeIPAM()
	theTime, err := time.Parse("2006-01-02 15:04:05.000", "2021-09-15 16:00:00.000")
	Expect(err).NotTo(HaveOccurred())
	fakeClock := clock.NewFakeClock(theTime)
	capacityC := make(chan SecondaryIfaceCapacities, 1)
	ec2Client, fakeEC2 := newFakeEC2Client()

	fakeEC2.InstancesByID[instanceID] = types.Instance{
		InstanceId:   stringPtr(instanceID),
		InstanceType: types.InstanceTypeT3Large,
		Placement: &types.Placement{
			AvailabilityZone: stringPtr(azWest1),
		},
		VpcId: stringPtr(testVPC),
	}
	fakeEC2.addSubnet(subnetIDWest1Default, azWest1, "192.164.1.0/24")
	fakeEC2.addSubnet(subnetIDWest2Default, azWest2, "192.164.2.0/24")
	fakeEC2.addSubnet(subnetIDWest1Calico, azWest1, subnetWest1CIDRCalico)
	fakeEC2.addSubnet(subnetIDWest1CalicoAlt, azWest1, subnetWest1CIDRCalicoAlt)
	fakeEC2.addSubnet(subnetIDWest2Calico, azWest2, subnetWest2CIDRCalico)

	fakeEC2.ENIsByID[primaryENIID] = types.NetworkInterface{
		NetworkInterfaceId: stringPtr(primaryENIID),
		Attachment: &types.NetworkInterfaceAttachment{
			DeviceIndex:      int32Ptr(0),
			NetworkCardIndex: int32Ptr(0),
			AttachmentId:     stringPtr(primaryENIAttachID),
			InstanceId:       stringPtr(instanceID),
		},
		SubnetId: stringPtr(subnetIDWest1Default),
		PrivateIpAddresses: []types.NetworkInterfacePrivateIpAddress{
			{
				Primary:          boolPtr(true),
				PrivateIpAddress: stringPtr("192.164.1.5"),
			},
		},
		PrivateIpAddress: stringPtr("192.164.1.5"),
		MacAddress:       stringPtr(primaryENIMAC),
		Groups: []types.GroupIdentifier{
			{
				GroupId:   stringPtr("sg-01234567890123456"),
				GroupName: stringPtr("sg-01234567890123456 name"),
			},
			{
				GroupId:   stringPtr("sg-01234567890123457"),
				GroupName: stringPtr("sg-01234567890123457 name"),
			},
		},
	}

	sip := NewSecondaryIfaceProvisioner(
		nodeName,
		health.NewHealthAggregator(),
		fakeIPAM,
		OptClockOverride(fakeClock),
		OptCapacityCallback(func(capacities SecondaryIfaceCapacities) {
			// Drain any previous message.
			select {
			case <-capacityC:
			default:
			}
			capacityC <- capacities
		}),
		OptNewEC2ClientOverride(func(ctx context.Context) (*EC2Client, error) {
			return ec2Client, nil
		}),
	)

	return sip, &sipTestFakes{
		IPAM:      fakeIPAM,
		EC2:       fakeEC2,
		Clock:     fakeClock,
		CapacityC: capacityC,
	}
}

func setupAndStart(t *testing.T) (*SecondaryIfaceProvisioner, *sipTestFakes, func()) {
	sip, fake := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	doneC := sip.Start(ctx)
	return sip, fake, func() {
		cancel()
		Eventually(doneC).Should(BeClosed())
		fake.EC2.Errors.ExpectAllErrorsConsumed()
	}
}

func TestSecondaryIfaceProvisioner_OnDatastoreUpdateShouldNotBlock(t *testing.T) {
	sip, _ := setup(t)

	// Hit on-update many times without starting the main loop, it should never block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for x := 0; x < 1000; x++ {
			sip.OnDatastoreUpdate(DatastoreState{
				LocalAWSRoutesByDst:       nil,
				LocalRouteDestsBySubnetID: nil,
				PoolIDsBySubnetID:         nil,
			})
		}
	}()

	Eventually(done).Should(BeClosed())
}

func TestSecondaryIfaceProvisioner_NoPoolsOrWorkloadsStartOfDay(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Send an empty snapshot.
	sip.OnDatastoreUpdate(DatastoreState{
		LocalAWSRoutesByDst:       nil,
		LocalRouteDestsBySubnetID: nil,
		PoolIDsBySubnetID:         nil,
	})

	// Should get an empty response.
	Eventually(sip.ResponseC()).Should(Receive(Equal(&LocalAWSNetworkState{})))
	Eventually(fake.CapacityC).Should(Receive(Equal(SecondaryIfaceCapacities{
		MaxCalicoSecondaryIPs: t3LargeCapacity,
	})))
}

func TestSecondaryIfaceProvisioner_AWSPoolsButNoWorkloadsMainline(t *testing.T) {
	sip, _, tearDown := setupAndStart(t)
	defer tearDown()

	sip.OnDatastoreUpdate(DatastoreState{
		LocalAWSRoutesByDst:       nil,
		LocalRouteDestsBySubnetID: nil,
		PoolIDsBySubnetID: map[string]set.Set{
			subnetIDWest1Calico: set.FromArray([]string{ipPoolIDWest1Hosts, ipPoolIDWest1Gateways}),
			subnetIDWest2Calico: set.FromArray([]string{ipPoolIDWest2Hosts, ipPoolIDWest2Gateways}),
		},
	})

	// Should respond with the Calico subnet details for the node's AZ..
	Eventually(sip.ResponseC()).Should(Receive(Equal(responsePoolsNoENIs)))
}

func TestSecondaryIfaceProvisioner_AWSPoolsSingleWorkload_Mainline(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Send snapshot with single workload.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)

	// Since this is a fresh system with only one ENI being allocated, everything is deterministic and we should
	// always get the same result.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))
	Eventually(fake.CapacityC).Should(Receive(Equal(SecondaryIfaceCapacities{
		MaxCalicoSecondaryIPs: t3LargeCapacity,
	})))

	// Check the ENI looks right on the AWS side.
	eni := fake.EC2.GetENI(firstAllocatedENIID)
	Expect(eni.Groups).To(ConsistOf(
		types.GroupIdentifier{
			GroupId:   stringPtr("sg-01234567890123456"),
			GroupName: stringPtr("sg-01234567890123456 name"),
		},
		types.GroupIdentifier{
			GroupId:   stringPtr("sg-01234567890123457"),
			GroupName: stringPtr("sg-01234567890123457 name"),
		},
	), "ENI should have same security groups as primary ENI")
	Expect(eni.Status).To(Equal(types.NetworkInterfaceStatusAssociated), "Expected ENI to be attached.")
	Expect(eni.Attachment).ToNot(BeNil(), "Expected ENI to be attached.")
	Expect(*eni.Attachment.InstanceId).To(Equal(instanceID), "Expected ENI to be attached to correct isntance.")
	Expect(eni.TagSet).To(ConsistOf([]types.Tag{
		{
			Key:   stringPtr("calico:instance"),
			Value: stringPtr("i-ca1ic000000000001"),
		},
		{
			Key:   stringPtr("calico:use"),
			Value: stringPtr("secondary"),
		},
	}))

	// Remove the workload again, IP should be released.
	sip.OnDatastoreUpdate(noWorkloadDatastore)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseENIAfterWorkloadsDeleted)))
}

func TestSecondaryIfaceProvisioner_AWSPoolsSingleWorkload_ErrBackoff(t *testing.T) {
	// Test that a range of different errors all result in a successful retry with backoff.
	// The fakeEC2 methods are all instrumented with the ErrorProducer so that we can make them fail
	// on command >:)

	for _, callToFail := range []string{
		"DescribeInstances",
		"DescribeNetworkInterfaces",
		"DescribeSubnets",
		"DescribeInstanceTypes",
		"DescribeNetworkInterfaces",
		"CreateNetworkInterface",
		"AttachNetworkInterface",
		"AssignPrivateIpAddresses",
	} {
		t.Run(callToFail, func(t *testing.T) {
			sip, fake, tearDown := setupAndStart(t)
			defer tearDown()

			// Queue up an error on a key AWS call. Note: tearDown() checks that all queued errors
			// were consumed so any typo in the name would be caught.
			fake.EC2.Errors.QueueError(callToFail)

			sip.OnDatastoreUpdate(singleWorkloadDatastore)

			// Should fail to respond.
			Consistently(sip.ResponseC()).ShouldNot(Receive())

			// Advance time to trigger the backoff.
			fake.expectSingleBackoffAndStep()

			// With only one ENI being added, FakeIPAM and FakeEC2 are deterministic.
			expResponse := responseSingleWorkload
			if callToFail == "CreateNetworkInterface" {
				// Failing CreateNetworkInterface triggers the allocated IP to be released and then a second
				// allocation performed.
				expResponse = responseSingleWorkloadOtherHostIP
			}
			Eventually(sip.ResponseC()).Should(Receive(Equal(expResponse)))

			// Whether we did an IPAM reallocation or not, we should have only one IP in use at the end.
			Expect(fake.IPAM.NumUsedIPs()).To(BeNumerically("==", 1))
		})
	}
}

func TestSecondaryIfaceProvisioner_AWSPoolsSingleWorkload_ErrBackoffInterrupted(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Queue up an error on a key AWS call.
	fake.EC2.Errors.QueueError("DescribeNetworkInterfaces")

	sip.OnDatastoreUpdate(singleWorkloadDatastore)

	// Should fail to respond.
	Consistently(sip.ResponseC()).ShouldNot(Receive())

	// Should be a timer waiting for backoff.
	Eventually(fake.Clock.HasWaiters).Should(BeTrue())

	// Send a datastore update, should trigger the backoff to be abandoned.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)

	// Since this is a fresh system with only one ENI being allocated, everything is deterministic and we should
	// always get the same result.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))
	Expect(fake.Clock.HasWaiters()).To(BeFalse())
}

// TestSecondaryIfaceProvisioner_PoolChange Checks that changing the IP pools to use a different subnet causes the
// provisioner to release ENIs and provision the new ones.
func TestSecondaryIfaceProvisioner_PoolChange(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Send snapshot with single workload on the original subnet.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)

	// Since this is a fresh system with only one ENI being allocated, everything is deterministic and we should
	// always get the same result.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))
	Eventually(fake.CapacityC).Should(Receive(Equal(SecondaryIfaceCapacities{
		MaxCalicoSecondaryIPs: t3LargeCapacity,
	})))

	// Remove the workload again, IP should be released but ENI should stick around.
	sip.OnDatastoreUpdate(noWorkloadDatastore)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseENIAfterWorkloadsDeleted)))

	// Change the pools.
	sip.OnDatastoreUpdate(noWorkloadDatastoreAltPools)
	// Should get a response with updated gateway addresses _but_ no secondary ENI (because there was no workload
	// to trigger addition of the secondary ENI).
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseAltPoolsNoENIs)))

	// Swap IPAM to prefer the alt host pool.  Normally the label selector on the pool would ensure the right
	// pool is used but we don't have that much function here.
	fake.IPAM.setFreeIPs(calicoHostIP1Alt)

	// Add a workload in the alt pool, should get a secondary ENI using the alt pool.
	sip.OnDatastoreUpdate(singleWorkloadDatastoreAltPool)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseAltPoolSingleWorkload)))

	// Delete the workload.  Should keep the ENI but remove the secondary IP.
	sip.OnDatastoreUpdate(noWorkloadDatastoreAltPools)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseAltPoolsAfterWorkloadsDeleted)))
}

func TestSecondaryIfaceProvisioner_PoolChangeWithFailure(t *testing.T) {
	for _, callToFail := range []string{
		"DetachNetworkInterface",
		"DeleteNetworkInterface",
	} {
		t.Run(callToFail, func(t *testing.T) {
			sip, fake, tearDown := setupAndStart(t)
			defer tearDown()

			fake.EC2.Errors.QueueError(callToFail)

			// Send the usual snapshot with single workload on the original subnet.
			sip.OnDatastoreUpdate(singleWorkloadDatastore)
			Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))

			// Change the pools.
			fake.IPAM.setFreeIPs(calicoHostIP1Alt)
			sip.OnDatastoreUpdate(singleWorkloadDatastoreAltPool)

			// Advance time to trigger the backoff.
			fake.expectSingleBackoffAndStep()

			// After backoff, should get the expected result.
			Eventually(sip.ResponseC()).Should(Receive(Equal(responseAltPoolSingleWorkload)))

			Expect(fake.EC2.NumENIs()).To(BeNumerically("==", 2 /* one primary, one secondary*/))
		})
	}
}

func TestSecondaryIfaceProvisioner_SecondWorkload(t *testing.T) {
	sip, _, tearDown := setupAndStart(t)
	defer tearDown()

	// Send snapshot with single workload.  Should get expected result.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))

	// Add second workload, should get added to same ENI.
	sip.OnDatastoreUpdate(twoWorkloadsDatastore)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseTwoWorkloads)))

	// Remove the workloads again, IPs should be released.
	sip.OnDatastoreUpdate(noWorkloadDatastore)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseENIAfterWorkloadsDeleted)))
}

func TestSecondaryIfaceProvisioner_UnassignIPFail(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Queue up a transient failure.
	fake.EC2.Errors.QueueError("UnassignPrivateIpAddresses")

	// Add two workloads.
	sip.OnDatastoreUpdate(twoWorkloadsDatastore)
	Eventually(sip.ResponseC()).Should(Receive())

	// Remove the workloads again, should try to release IPs, triggering backoff.
	sip.OnDatastoreUpdate(noWorkloadDatastore)
	fake.expectSingleBackoffAndStep()

	// After backoff, should get the expected result.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseENIAfterWorkloadsDeleted)))
}

// TestSecondaryIfaceProvisioner_MultiENI ramps up the number of AWS IPs needed until it forces multiple AWS
// ENIs to be added.  It then tests what happens if the limit on IPs is exceeded.
func TestSecondaryIfaceProvisioner_MultiENI(t *testing.T) {
	sip, _, tearDown := setupAndStart(t)
	defer tearDown()

	// Fill up the first interface with progressively more IPs.
	const secondaryIPsPerENI = 11
	for numENIs := 1; numENIs <= secondaryIPsPerENI; numENIs++ {
		ds, addrs := nWorkloadDatastore(numENIs)
		sip.OnDatastoreUpdate(ds)
		var response *LocalAWSNetworkState
		Eventually(sip.ResponseC()).Should(Receive(&response))

		// Check all the IPs ended up on the first ENI.
		Expect(response.SecondaryENIsByMAC).To(HaveLen(1), "Expected only one AWS interface")
		iface := response.SecondaryENIsByMAC[firstAllocatedMAC.String()]
		Expect(iface.SecondaryIPv4Addrs).To(ConsistOf(addrs))
	}
	// Now send in even more IPs, progressively filling up the second interface.
	for numENIs := secondaryIPsPerENI + 1; numENIs <= secondaryIPsPerENI*2; numENIs++ {
		ds, addrs := nWorkloadDatastore(numENIs)
		sip.OnDatastoreUpdate(ds)
		var response *LocalAWSNetworkState
		Eventually(sip.ResponseC()).Should(Receive(&response))

		Expect(response.SecondaryENIsByMAC).To(HaveLen(2), "Expected exactly two AWS ENIs.")
		// Check the first ENI keep the first few IPs.
		firstIface := response.SecondaryENIsByMAC[firstAllocatedMAC.String()]
		Expect(firstIface.SecondaryIPv4Addrs).To(ConsistOf(addrs[:secondaryIPsPerENI]))
		// Second interface should have the remainder.
		secondIface := response.SecondaryENIsByMAC[secondAllocatedMAC.String()]
		Expect(secondIface.SecondaryIPv4Addrs).To(ConsistOf(addrs[secondaryIPsPerENI:]))
	}
	{
		// Add one more IP, it should have nowhere to go because this instance type only supports 2 secondary ENIs.
		ds, addrs := nWorkloadDatastore(secondaryIPsPerENI*2 + 1)
		sip.OnDatastoreUpdate(ds)
		var response *LocalAWSNetworkState
		Eventually(sip.ResponseC()).Should(Receive(&response))
		Expect(response.SecondaryENIsByMAC).To(HaveLen(2), "Expected exactly two AWS ENIs.")
		// Check the first ENI keep the first few IPs.
		firstIface := response.SecondaryENIsByMAC[firstAllocatedMAC.String()]
		Expect(firstIface.SecondaryIPv4Addrs).To(ConsistOf(addrs[:secondaryIPsPerENI]))
		// Second interface should have the remainder.
		secondIface := response.SecondaryENIsByMAC[secondAllocatedMAC.String()]
		Expect(secondIface.SecondaryIPv4Addrs).To(ConsistOf(addrs[secondaryIPsPerENI : secondaryIPsPerENI*2]))
	}
	{
		// Drop back down to 1 IP.
		ds, addrs := nWorkloadDatastore(1)
		sip.OnDatastoreUpdate(ds)
		var response *LocalAWSNetworkState
		Eventually(sip.ResponseC()).Should(Receive(&response))

		// Should keep the second ENI but with no IPs.
		Expect(response.SecondaryENIsByMAC).To(HaveLen(2), "Expected exactly two AWS ENIs.")
		// Check the first ENI keep the first few IPs.
		firstIface := response.SecondaryENIsByMAC[firstAllocatedMAC.String()]
		Expect(firstIface.SecondaryIPv4Addrs).To(ConsistOf(addrs))
		// Second interface should have the remainder.
		secondIface := response.SecondaryENIsByMAC[secondAllocatedMAC.String()]
		Expect(secondIface.SecondaryIPv4Addrs).To(HaveLen(0))
	}
}

func TestSecondaryIfaceProvisioner_MultiENISingleShot(t *testing.T) {
	sip, _, tearDown := setupAndStart(t)
	defer tearDown()

	// Blast in the maximum number of IPs in one shot.
	ds, addrs := nWorkloadDatastore(t3LargeCapacity)
	sip.OnDatastoreUpdate(ds)
	var response *LocalAWSNetworkState
	Eventually(sip.ResponseC()).Should(Receive(&response))

	// Verify the result.
	Expect(response.SecondaryENIsByMAC).To(HaveLen(2), "Expected exactly two AWS ENIs.")

	// IPs will be assigned randomly to the two ENIs so grab and compare the full list.
	firstIface := response.SecondaryENIsByMAC[firstAllocatedMAC.String()]
	secondIface := response.SecondaryENIsByMAC[secondAllocatedMAC.String()]
	allIPs := append([]ip.Addr{}, firstIface.SecondaryIPv4Addrs...)
	allIPs = append(allIPs, secondIface.SecondaryIPv4Addrs...)
	Expect(allIPs).To(ConsistOf(addrs))
}

func nWorkloadDatastore(n int) (DatastoreState, []ip.Addr) {
	ds := DatastoreState{
		LocalAWSRoutesByDst: map[ip.CIDR]*proto.RouteUpdate{},
		LocalRouteDestsBySubnetID: map[string]set.Set{
			subnetIDWest1Calico: set.New(),
		},
		PoolIDsBySubnetID: defaultPools,
	}
	var addrs []ip.Addr

	for i := 0; i < n; i++ {
		addr := ip.V4Addr{100, 64, 1, byte(64 + i)}
		addrs = append(addrs, addr)
		ds.LocalAWSRoutesByDst[addr.AsCIDR()] = &proto.RouteUpdate{
			Dst:           addr.AsCIDR().String(),
			LocalWorkload: true,
			AwsSubnetId:   subnetIDWest1Calico,
		}
		ds.LocalRouteDestsBySubnetID[subnetIDWest1Calico].Add(addr.AsCIDR())
	}
	return ds, addrs
}

// TestSecondaryIfaceProvisioner_NonLocalWorkload verifies handling of workloads from the wrong subnet. They
// Should be ignored.
func TestSecondaryIfaceProvisioner_NonLocalWorkload(t *testing.T) {
	sip, _, tearDown := setupAndStart(t)
	defer tearDown()

	// Send snapshot with one workload in a local subnet and one in a remote one.
	sip.OnDatastoreUpdate(nonLocalWorkloadDatastore)
	// Should act like remote subnet is not there.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))
}

// TestSecondaryIfaceProvisioner_NonLocalWorkload verifies handling of workloads from the wrong subnet. They
// Should be ignored.
func TestSecondaryIfaceProvisioner_WorkloadMixedSubnets(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Start with one local workload.  This will cement its subnet as the valid one for this node.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))

	// Then add a second workload on a different subnet, it should be ignored.
	sip.OnDatastoreUpdate(mixedSubnetDatastore)

	// Should act like remote subnet is not there.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))

	// Now send a snapshot that doesn't include the first workload.  Now the "alternative" IP pool will be chosen as
	// the "best" one and everything should swap over.
	fake.IPAM.setFreeIPs(calicoHostIP1Alt) // Our mock IPAM is too dumb to handle node selectors.
	sip.OnDatastoreUpdate(singleWorkloadDatastoreAltPool)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseAltPoolSingleWorkload)))
	Expect(fake.EC2.NumENIs()).To(BeNumerically("==", 2))

	// Add the first workload back, now the "alternative" wins.
	sip.OnDatastoreUpdate(mixedSubnetDatastore)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseAltPoolSingleWorkload)))
	Expect(fake.EC2.NumENIs()).To(BeNumerically("==", 2))
}

// TestSecondaryIfaceProvisioner_WorkloadHostIPClash tests that workloads that try to use the host's primary
// IP are ignores.
func TestSecondaryIfaceProvisioner_WorkloadHostIPClash(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Send snapshot with one workload in a local subnet and one in a remote one.
	sip.OnDatastoreUpdate(hostClashWorkloadDatastore)

	// Since the IP is only assigned to the ENI after we check the routes, it only gets picked up after the
	// first failure triggers a backoff.
	fake.expectSingleBackoffAndStep()

	// Should act like remote subnet is not there.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))
}

func TestSecondaryIfaceProvisioner_NoSecondaryIPsPossible(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Make our instance type tiny, with no available secondary IPs.  Note: AWS actually doesn't have any
	// instance types with _no_ secondary ENIs at all so this is made up.
	inst := fake.EC2.InstancesByID[instanceID]
	inst.InstanceType = instanceTypeT0Pico
	fake.EC2.InstancesByID[instanceID] = inst

	// Try to add a workload.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)
	Eventually(fake.Clock.HasWaiters).Should(BeTrue())
	fake.Clock.Step(1200 * time.Millisecond)
	Consistently(sip.ResponseC()).ShouldNot(Receive())
	Eventually(fake.Clock.HasWaiters).Should(BeTrue()) // Should keep backing off
}

func TestSecondaryIfaceProvisioner_IPAMCleanup(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Pre-assign an IP to the node.  It should appear to be leaked and get cleaned up.
	_, _, err := fake.IPAM.AutoAssign(context.TODO(), sip.ipamAssignArgs(1))
	Expect(err).NotTo(HaveOccurred())
	// Check we allocatred exactly what we expected.
	addrs, err := fake.IPAM.IPsByHandle(context.TODO(), sip.ipamHandle())
	Expect(err).NotTo(HaveOccurred())
	Expect(addrs).To(ConsistOf(cnet.MustParseIP(calicoHostIP1)))

	// Send snapshot with single workload.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)
	// The IP we leaked gets released _first_ so we expect the second IP to get used for the new ENI.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkloadOtherHostIP)))

	// Check that the leaked IP was freed.
	addrs, err = fake.IPAM.IPsByHandle(context.TODO(), sip.ipamHandle())
	Expect(err).NotTo(HaveOccurred())
	Expect(addrs).To(ConsistOf(cnet.MustParseIP(calicoHostIP2)))
}

func TestSecondaryIfaceProvisioner_IPAMCleanupFailure(t *testing.T) {
	for _, callToFail := range []string{"ReleaseIPs", "IPsByHandle"} {
		t.Run(callToFail, func(t *testing.T) {
			sip, fake, tearDown := setupAndStart(t)
			defer tearDown()
			fake.IPAM.Errors.QueueError(callToFail)

			// Pre-assign an IP to the node.  It should appear to be leaked and get cleaned up.
			_, _, err := fake.IPAM.AutoAssign(context.TODO(), sip.ipamAssignArgs(1))
			Expect(err).NotTo(HaveOccurred())

			// Send snapshot with single workload.
			sip.OnDatastoreUpdate(singleWorkloadDatastore)

			// Failure should trigger a backoff/retry.
			fake.expectSingleBackoffAndStep()

			// The IP we leaked gets released _first_ so we expect the second IP to get used for the new ENI.
			Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkloadOtherHostIP)))

			// Check that the leaked IP was freed.
			addrs, err := fake.IPAM.IPsByHandle(context.TODO(), sip.ipamHandle())
			Expect(err).NotTo(HaveOccurred())
			Expect(addrs).To(ConsistOf(cnet.MustParseIP(calicoHostIP2)))
		})
	}
}

func TestSecondaryIfaceProvisioner_IPAMAssignFailure(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	fake.IPAM.Errors.QueueError("AutoAssign")

	// Send snapshot with single workload.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)
	fake.expectSingleBackoffAndStep()
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))
}

// errNotFound returns an error with the same structure as the AWSv2 client returns.  The code under test
// unwraps errors with errors.As() so it's important that we return something that's the right shape.
func errNotFound(op string, code string) error {
	return &smithy.OperationError{
		ServiceID:     "EC2",
		OperationName: op,
		Err: &http.ResponseError{
			Response: &http.Response{
				Response: &nethttp.Response{
					StatusCode: 403,
				},
			},
			Err: &smithy.GenericAPIError{
				Code:    code,
				Message: "The XXX does not exist",
				Fault:   0,
			},
		},
	}
}

func errBadParam(op string, code string) error {
	return &smithy.OperationError{
		ServiceID:     "EC2",
		OperationName: op,
		Err: &http.ResponseError{
			Response: &http.Response{
				Response: &nethttp.Response{
					StatusCode: 400,
				},
			},
			Err: &smithy.GenericAPIError{
				Code:    code,
				Message: "Bad paremeter",
				Fault:   0,
			},
		},
	}
}

func errUnauthorized(op string) error {
	return &smithy.OperationError{
		ServiceID:     "EC2",
		OperationName: op,
		Err: &http.ResponseError{
			Response: &http.Response{
				Response: &nethttp.Response{
					StatusCode: 403,
				},
			},
			Err: &smithy.GenericAPIError{
				Code:    "UnauthorizedOperation",
				Message: "You are not authorized to perform this operation",
				Fault:   0,
			},
		},
	}
}
