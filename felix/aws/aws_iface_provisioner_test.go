// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package aws

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	nethttp "net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/transport/http"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/clock"

	"github.com/projectcalico/calico/felix/ip"
	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/libcalico-go/lib/set"

	"github.com/projectcalico/calico/libcalico-go/lib/health"
	cnet "github.com/projectcalico/calico/libcalico-go/lib/net"
)

const (
	// We test from the point of view of "test-node" in the "us-west-1" AZ.

	nodeName   = "test-node"
	instanceID = "i-ca1ic000000000001"
	testVPC    = "vpc-01234567890123456"

	awsSubnetsFilename = "/tmp/aws-subnets"

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
	wl1IP      = "100.64.1.64"
	wl1Addr    = wl1IP + "/32"
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
	t3LargeCapacityPerENI   = 11
	t3LargeNumSecondaryENIs = 2
	t3LargeCapacity         = t3LargeCapacityPerENI * t3LargeNumSecondaryENIs
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
		LocalAWSAddrsByDst:        nil,
		LocalRouteDestsBySubnetID: nil,
		PoolIDsBySubnetID:         defaultPools,
	}
	noWorkloadDatastoreAltPools = DatastoreState{
		LocalAWSAddrsByDst:        nil,
		LocalRouteDestsBySubnetID: nil,
		PoolIDsBySubnetID:         alternatePools,
	}
	singleWorkloadDatastore = DatastoreState{
		LocalAWSAddrsByDst: map[ip.CIDR]AddrInfo{
			wl1CIDR: {
				Dst:         wl1Addr,
				AWSSubnetId: subnetIDWest1Calico,
			},
		},
		LocalRouteDestsBySubnetID: map[string]set.Set{
			subnetIDWest1Calico: set.FromArray([]ip.CIDR{wl1CIDR}),
		},
		PoolIDsBySubnetID: defaultPools,
	}
	twoWorkloadsDatastore = DatastoreState{
		LocalAWSAddrsByDst: map[ip.CIDR]AddrInfo{
			wl1CIDR: {
				Dst:         wl1Addr,
				AWSSubnetId: subnetIDWest1Calico,
			},
			wl2CIDR: {
				Dst:         wl2Addr,
				AWSSubnetId: subnetIDWest1Calico,
			},
		},
		LocalRouteDestsBySubnetID: map[string]set.Set{
			subnetIDWest1Calico: set.FromArray([]ip.CIDR{wl1CIDR, wl2CIDR}),
		},
		PoolIDsBySubnetID: defaultPools,
	}
	// workloadInWrongSubnetDatastore has one workload that's in the local subnet and one that is in
	// a subnet that's not in our AZ.
	workloadInWrongSubnetDatastore = DatastoreState{
		LocalAWSAddrsByDst: map[ip.CIDR]AddrInfo{
			wl1CIDR: {
				Dst:         wl1Addr,
				AWSSubnetId: subnetIDWest1Calico,
			},
			west2WlCIDR: {
				Dst:         west2WlIP,
				AWSSubnetId: subnetIDWest2Calico,
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
		LocalAWSAddrsByDst: map[ip.CIDR]AddrInfo{
			wl1CIDR: {
				Dst:         wl1Addr,
				AWSSubnetId: subnetIDWest1Calico,
			},
			wl1CIDRAlt: {
				Dst:         wl1AddrAlt,
				AWSSubnetId: subnetIDWest1CalicoAlt,
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
		LocalAWSAddrsByDst: map[ip.CIDR]AddrInfo{
			wl1CIDR: {
				Dst:         wl1Addr,
				AWSSubnetId: subnetIDWest1Calico,
			},
			calicoHostCIDR1: {
				Dst:         calicoHostCIDR1.String(),
				AWSSubnetId: subnetIDWest1Calico,
			},
		},
		LocalRouteDestsBySubnetID: map[string]set.Set{
			subnetIDWest1Calico: set.FromArray([]ip.CIDR{wl1CIDR}),
			subnetIDWest2Calico: set.FromArray([]ip.CIDR{west2WlCIDR}),
		},
		PoolIDsBySubnetID: defaultPools,
	}
	singleWorkloadDatastoreAltPool = DatastoreState{
		LocalAWSAddrsByDst: map[ip.CIDR]AddrInfo{
			wl1CIDRAlt: {
				Dst:         wl1AddrAlt,
				AWSSubnetId: subnetIDWest1CalicoAlt,
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

func TestSecondaryIfaceProvisioner_OnDatastoreUpdateShouldNotBlock(t *testing.T) {
	sip, _ := setup(t)

	// Hit on-update many times without starting the main loop, it should never block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for x := 0; x < 1000; x++ {
			sip.OnDatastoreUpdate(DatastoreState{
				LocalAWSAddrsByDst:        nil,
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
		LocalAWSAddrsByDst:        nil,
		LocalRouteDestsBySubnetID: nil,
		PoolIDsBySubnetID:         nil,
	})

	// Should get an empty response.
	Eventually(sip.ResponseC()).Should(Receive(Equal(&LocalAWSNetworkState{})))
	Eventually(fake.CapacityC).Should(Receive(Equal(SecondaryIfaceCapacities{
		MaxCalicoSecondaryIPs: t3LargeCapacity,
	})))
}

func TestSecondaryIfaceProvisioner_Liveness(t *testing.T) {
	_, fake, tearDown := setupAndStart(t, OptLivenessEnabled(true))
	defer tearDown()

	// Initial registration and report should happen synchronously.
	Expect(fake.Health.getRegistrations()).To(HaveKeyWithValue(
		healthNameAWSProvisioner,
		registration{
			Reports: health.HealthReport{Live: true, Ready: true},
			Timeout: 300 * time.Second,
		}))
	Expect(fake.Health.getLastReports()).To(HaveKeyWithValue(
		healthNameAWSProvisioner,
		health.HealthReport{Live: true, Ready: true},
	))

	// Next report after 30s...
	Eventually(fake.BackoffClock.HasWaiters).Should(BeTrue())
	fake.Health.clearReports()
	fake.BackoffClock.Step(29 * time.Second)
	Consistently(fake.Health.getLastReports).ShouldNot(HaveKey(
		healthNameAWSProvisioner,
	))
	fake.BackoffClock.Step(2 * time.Second)
	Eventually(fake.Health.getLastReports).Should(HaveKeyWithValue(
		healthNameAWSProvisioner,
		health.HealthReport{Live: true, Ready: true},
	))

	// Then every 30s.
	Eventually(fake.BackoffClock.HasWaiters).Should(BeTrue())
	fake.Health.clearReports()
	fake.BackoffClock.Step(30 * time.Second)
	Eventually(fake.Health.getLastReports).Should(HaveKeyWithValue(
		healthNameAWSProvisioner,
		health.HealthReport{Live: true, Ready: true},
	))
}

func TestSecondaryIfaceProvisioner_AWSPoolsButNoWorkloadsMainline(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	sip.OnDatastoreUpdate(DatastoreState{
		LocalAWSAddrsByDst:        nil,
		LocalRouteDestsBySubnetID: nil,
		PoolIDsBySubnetID: map[string]set.Set{
			subnetIDWest1Calico: set.FromArray([]string{ipPoolIDWest1Hosts, ipPoolIDWest1Gateways}),
			subnetIDWest2Calico: set.FromArray([]string{ipPoolIDWest2Hosts, ipPoolIDWest2Gateways}),
		},
	})

	// Should respond with the Calico subnet details for the node's AZ.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responsePoolsNoENIs)))

	// Should write out the aws-subnets file.
	rawSubnets, err := ioutil.ReadFile(awsSubnetsFilename)
	Expect(err).NotTo(HaveOccurred())
	Expect(rawSubnets).To(MatchJSON(fmt.Sprintf(`{"aws_subnet_ids": ["%s", "%s", "%s"]}`,
		subnetIDWest1Calico, subnetIDWest1CalicoAlt, subnetIDWest1Default)))

	// After a success, there should be a recheck scheduled but no backoff.
	Eventually(fake.RecheckClock.HasWaiters).Should(BeTrue(), "expected a pending recheck")
	Eventually(fake.BackoffClock.HasWaiters).Should(BeFalse(), "expected no backoff scheduled")

	// Initial backoff should be between 30s and 33s.
	fake.RecheckClock.Step(29999 * time.Millisecond)
	Consistently(sip.ResponseC()).ShouldNot(Receive())
	Expect(fake.RecheckClock.HasWaiters()).Should(BeTrue(), "expected a pending recheck")
	Expect(fake.BackoffClock.HasWaiters()).Should(BeFalse(), "expected no backoff scheduled")

	fake.RecheckClock.Step(3002 * time.Millisecond)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responsePoolsNoENIs)))
	Expect(fake.RecheckClock.HasWaiters()).Should(BeTrue(), "expected a pending recheck")
	Expect(fake.BackoffClock.HasWaiters()).Should(BeFalse(), "expected no backoff scheduled")
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
	Expect(*eni.Attachment.InstanceId).To(Equal(instanceID), "Expected ENI to be attached to correct instance.")
	Expect(eni.Attachment.DeleteOnTermination).ToNot(BeNil(), "Expected DeleteOnTermination to be set.")
	Expect(*eni.Attachment.DeleteOnTermination).To(BeTrue(), "Expected DeleteOnTermination to be true.")
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

func TestSecondaryIfaceProvisioner_AWSPoolsSingleWorkload_AWSLostAssign(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Simulate a silent failure to add an IP.  We've seen these in practice as a result of high churn; likely
	// due to a race between a slow deletion and a second add of the same IP address.
	fake.EC2.IgnoreNextAssignPrivateIpAddresses = true

	// Send snapshot with single workload.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)

	// Should fail to respond: the ignored assign should be detected and cause a backoff.
	Consistently(sip.ResponseC()).ShouldNot(Receive())

	// Advance time to trigger the backoff.
	fake.expectSingleBackoffAndStep()

	// Since this is a fresh system with only one ENI being allocated, everything is deterministic and we should
	// always get the same result.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))
	Eventually(fake.CapacityC).Should(Receive(Equal(SecondaryIfaceCapacities{
		MaxCalicoSecondaryIPs: t3LargeCapacity,
	})))
}

func TestSecondaryIfaceProvisioner_AWSPoolsSingleWorkload_AWSLostUnassign(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Simulate a silent failure to remove an IP.  We've seen these in practice as a result of high churn; likely
	// due to a race between a slow deletion and a second add of the same IP address.
	fake.EC2.IgnoreNextUnassignPrivateIpAddresses = true

	// Send snapshot with single workload.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)

	// Since this is a fresh system with only one ENI being allocated, everything is deterministic and we should
	// always get the same result.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))

	// Remove the workload again, IP should be released.
	sip.OnDatastoreUpdate(noWorkloadDatastore)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseENIAfterWorkloadsDeleted)))
}

func TestSecondaryIfaceProvisioner_AWSRecheckAfterAction(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Send snapshot with single workload.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)

	// Since this is a fresh system with only one ENI being allocated, everything is deterministic and we should
	// always get the same result.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))

	// After a success, there should be a recheck scheduled but no backoff.
	Eventually(fake.RecheckClock.HasWaiters).Should(BeTrue(), "expected a pending recheck")
	Eventually(fake.BackoffClock.HasWaiters).Should(BeFalse(), "expected no backoff scheduled")

	// Initial backoff should be between 30s and 33s.
	fake.RecheckClock.Step(29999 * time.Millisecond)
	Consistently(sip.ResponseC()).ShouldNot(Receive())
	Expect(fake.RecheckClock.HasWaiters()).Should(BeTrue(), "expected a pending recheck")
	Expect(fake.BackoffClock.HasWaiters()).Should(BeFalse(), "expected no backoff scheduled")

	fake.RecheckClock.Step(3002 * time.Millisecond)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))
	Expect(fake.RecheckClock.HasWaiters()).Should(BeTrue(), "expected a pending recheck")
	Expect(fake.BackoffClock.HasWaiters()).Should(BeFalse(), "expected no backoff scheduled")

	// Next recheck should be 60-66s
	fake.RecheckClock.Step(59999 * time.Millisecond)
	Consistently(sip.ResponseC()).ShouldNot(Receive())
	Expect(fake.RecheckClock.HasWaiters()).Should(BeTrue(), "expected a pending recheck")
	Expect(fake.BackoffClock.HasWaiters()).Should(BeFalse(), "expected no backoff scheduled")

	fake.RecheckClock.Step(6002 * time.Millisecond)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))
	Expect(fake.RecheckClock.HasWaiters()).Should(BeTrue(), "expected a pending recheck")
	Expect(fake.BackoffClock.HasWaiters()).Should(BeFalse(), "expected no backoff scheduled")
}

func TestSecondaryIfaceProvisioner_AWSRecheckDetectsProblem(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Send snapshot with single workload.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)

	// Since this is a fresh system with only one ENI being allocated, everything is deterministic and we should
	// always get the same result.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))

	// After a success, there should be a recheck scheduled but no backoff.
	Eventually(fake.RecheckClock.HasWaiters).Should(BeTrue(), "expected a pending recheck")
	Eventually(fake.BackoffClock.HasWaiters).Should(BeFalse(), "expected no backoff scheduled")

	// Simulate a problem: delete an IP address.
	_, err := fake.EC2.UnassignPrivateIpAddresses(context.TODO(), &ec2.UnassignPrivateIpAddressesInput{
		NetworkInterfaceId: stringPtr(firstAllocatedENIID),
		PrivateIpAddresses: []string{wl1IP},
	})
	Expect(err).NotTo(HaveOccurred(), "Bug in test: failed to remove IP")

	// Initial recheck backoff should be between 30s and 33s.
	fake.RecheckClock.Step(33002 * time.Millisecond)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))
	Expect(fake.RecheckClock.HasWaiters()).Should(BeTrue(), "expected a pending recheck")
	Expect(fake.BackoffClock.HasWaiters()).Should(BeFalse(), "expected no backoff scheduled")

	// Since the recheck found/fixed a problem, the recheck backoff should go back to 30s again.
	fake.RecheckClock.Step(29999 * time.Millisecond)
	Consistently(sip.ResponseC()).ShouldNot(Receive())
	Expect(fake.RecheckClock.HasWaiters()).Should(BeTrue(), "expected a pending recheck")
	Expect(fake.BackoffClock.HasWaiters()).Should(BeFalse(), "expected no backoff scheduled")

	fake.RecheckClock.Step(3002 * time.Millisecond)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))
	Expect(fake.RecheckClock.HasWaiters()).Should(BeTrue(), "expected a pending recheck")
	Expect(fake.BackoffClock.HasWaiters()).Should(BeFalse(), "expected no backoff scheduled")
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
		"ModifyNetworkInterfaceAttribute",
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
	Eventually(fake.BackoffClock.HasWaiters).Should(BeTrue())

	// Send a datastore update, should trigger the backoff to be abandoned.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)

	// Since this is a fresh system with only one ENI being allocated, everything is deterministic and we should
	// always get the same result.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))
	Expect(fake.BackoffClock.HasWaiters()).To(BeFalse())
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

	// Remove the workload again, IP should be released but ENI should stick around (so that we have a "warm" ENI
	// in case another workload shows up).
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
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Send snapshot with single workload.  Should get expected result.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))

	// Add second workload, should get added to same ENI.
	sip.OnDatastoreUpdate(twoWorkloadsDatastore)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseTwoWorkloads)))
	eni := fake.EC2.GetENI(firstAllocatedENIID)
	Expect(eni.PrivateIpAddresses).To(ConsistOf(
		types.NetworkInterfacePrivateIpAddress{
			Primary:          boolPtr(true),
			PrivateIpAddress: stringPtr(calicoHostIP1),
		},
		types.NetworkInterfacePrivateIpAddress{
			Primary:          boolPtr(false),
			PrivateIpAddress: stringPtr(wl1CIDR.Addr().String()),
		},
		types.NetworkInterfacePrivateIpAddress{
			Primary:          boolPtr(false),
			PrivateIpAddress: stringPtr(wl2CIDR.Addr().String()),
		},
	))

	// Remove the workloads again, workload IPs should be unattached from the ENIs.
	sip.OnDatastoreUpdate(noWorkloadDatastore)
	// Should get a message to that effect...
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseENIAfterWorkloadsDeleted)))
	// And EC2 should agree.
	Expect(fake.EC2.NumENIs()).To(BeNumerically("==", 2))
	eni = fake.EC2.GetENI(firstAllocatedENIID)
	Expect(eni.PrivateIpAddresses).To(ConsistOf(types.NetworkInterfacePrivateIpAddress{
		Primary:          boolPtr(true),
		PrivateIpAddress: stringPtr(calicoHostIP1),
	}))
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
	for numWorkloads := 1; numWorkloads <= secondaryIPsPerENI; numWorkloads++ {
		ds, addrs := nWorkloadDatastore(numWorkloads)
		sip.OnDatastoreUpdate(ds)
		var response *LocalAWSNetworkState
		Eventually(sip.ResponseC()).Should(Receive(&response))

		// Check all the IPs ended up on the first ENI.
		Expect(response.SecondaryENIsByMAC).To(HaveLen(1), "Expected only one AWS interface")
		iface := response.SecondaryENIsByMAC[firstAllocatedMAC.String()]
		Expect(iface.SecondaryIPv4Addrs).To(ConsistOf(addrs))
	}
	// Now send in even more IPs, progressively filling up the second interface.
	for numWorkloads := secondaryIPsPerENI + 1; numWorkloads <= secondaryIPsPerENI*2; numWorkloads++ {
		ds, addrs := nWorkloadDatastore(numWorkloads)
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
		// Check the first ENI keeps the first few IPs.
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
	expectAllIPs(response, addrs)
}

func TestSecondaryIfaceProvisioner_TestAssignmentAfterFillingNode(t *testing.T) {
	sip, _, tearDown := setupAndStart(t)
	defer tearDown()

	// Blast in the maximum number of IPs in one shot.
	ds, addrs := nWorkloadDatastore(t3LargeCapacity)
	sip.OnDatastoreUpdate(ds)
	var response *LocalAWSNetworkState
	Eventually(sip.ResponseC()).Should(Receive(&response))
	Expect(response.SecondaryENIsByMAC).To(HaveLen(2), "Expected exactly two AWS ENIs.")
	expectAllIPs(response, addrs)

	// Drop back down to 0 IPs.
	ds, addrs = nWorkloadDatastore(0)
	sip.OnDatastoreUpdate(ds)
	Eventually(sip.ResponseC()).Should(Receive(&response))
	// Still expect two ENIs.
	Expect(response.SecondaryENIsByMAC).To(HaveLen(2), "Expected exactly two AWS ENIs.")
	expectAllIPs(response, addrs)

	// Jump back up to fill exactly one ENI.
	ds, addrs = nWorkloadDatastore(t3LargeCapacityPerENI)
	sip.OnDatastoreUpdate(ds)
	Eventually(sip.ResponseC()).Should(Receive(&response))
	// Still expect two ENIs.
	Expect(response.SecondaryENIsByMAC).To(HaveLen(2), "Expected exactly two AWS ENIs.")
	expectAllIPs(response, addrs)
}

func expectAllIPs(response *LocalAWSNetworkState, addrs []ip.Addr) {
	firstIface := response.SecondaryENIsByMAC[firstAllocatedMAC.String()]
	secondIface := response.SecondaryENIsByMAC[secondAllocatedMAC.String()]
	allIPs := append([]ip.Addr{}, firstIface.SecondaryIPv4Addrs...)
	allIPs = append(allIPs, secondIface.SecondaryIPv4Addrs...)
	ExpectWithOffset(1, allIPs).To(ConsistOf(addrs))
}

func nWorkloadDatastore(n int) (DatastoreState, []ip.Addr) {
	ds := DatastoreState{
		LocalAWSAddrsByDst: map[ip.CIDR]AddrInfo{},
		LocalRouteDestsBySubnetID: map[string]set.Set{
			subnetIDWest1Calico: set.New(),
		},
		PoolIDsBySubnetID: defaultPools,
	}
	var addrs []ip.Addr

	for i := 0; i < n; i++ {
		addr := ip.V4Addr{100, 64, 1, byte(64 + i)}
		addrs = append(addrs, addr)
		ds.LocalAWSAddrsByDst[addr.AsCIDR()] = AddrInfo{
			Dst:         addr.AsCIDR().String(),
			AWSSubnetId: subnetIDWest1Calico,
		}
		ds.LocalRouteDestsBySubnetID[subnetIDWest1Calico].Add(addr.AsCIDR())
	}
	return ds, addrs
}

// TestSecondaryIfaceProvisioner_WrongSubnetWorkload verifies handling of workloads from the wrong subnet. They
// Should be ignored.
func TestSecondaryIfaceProvisioner_WrongSubnetWorkload(t *testing.T) {
	sip, _, tearDown := setupAndStart(t)
	defer tearDown()

	// Send snapshot with one workload in a local subnet and one in a remote one.
	sip.OnDatastoreUpdate(workloadInWrongSubnetDatastore)
	// Should act like remote subnet is not there.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))
}

// TestSecondaryIfaceProvisioner_WorkloadMixedSubnets verifies handling of multiple workloads in different subnets.
// The first workload that arrives should "lock in" the subnet and the second should be ignored.
func TestSecondaryIfaceProvisioner_WorkloadMixedSubnets(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Start with one local workload.  This will cement its subnet as the valid one for this node.
	sip.OnDatastoreUpdate(singleWorkloadDatastore)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))

	// Check Felix allocated from the correct subnet.
	allocs := fake.IPAM.Allocations()
	Expect(allocs).To(HaveLen(1))
	Expect(allocs[0].Args.AWSSubnetIDs).To(ConsistOf(subnetIDWest1Calico))

	// Then add a second workload on a different subnet, it should be ignored.
	logrus.Info("Sending mixed-subnet datastore snapshot")
	sip.OnDatastoreUpdate(mixedSubnetDatastore)

	// Should act like remote subnet is not there.
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseSingleWorkload)))

	// Now send a snapshot that doesn't include the first workload.  Now the "alternative" IP pool will be chosen as
	// the "best" one and everything should swap over.
	fake.IPAM.setFreeIPs(calicoHostIP1Alt) // Our mock IPAM is too dumb to handle node selectors.
	logrus.Info("Sending single-subnet alt pool datastore snapshot")
	sip.OnDatastoreUpdate(singleWorkloadDatastoreAltPool)
	Eventually(sip.ResponseC()).Should(Receive(Equal(responseAltPoolSingleWorkload)))
	Expect(fake.EC2.NumENIs()).To(BeNumerically("==", 2))

	// Check Felix allocated from the correct subnet.
	allocs = fake.IPAM.Allocations()
	Expect(allocs).To(HaveLen(1))
	Expect(allocs[0].Args.AWSSubnetIDs).To(ConsistOf(subnetIDWest1CalicoAlt))

	// Add the first workload back, now the "alternative" wins.
	logrus.Info("Sending mixed-subnet datastore snapshot")
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
	Eventually(fake.BackoffClock.HasWaiters).Should(BeTrue())
	fake.BackoffClock.Step(1200 * time.Millisecond)
	Consistently(sip.ResponseC()).ShouldNot(Receive())
	Eventually(fake.BackoffClock.HasWaiters).Should(BeTrue()) // Should keep backing off
}

func TestSecondaryIfaceProvisioner_IPAMCleanup(t *testing.T) {
	sip, fake, tearDown := setupAndStart(t)
	defer tearDown()

	// Pre-assign an IP to the node.  It should appear to be leaked and get cleaned up.
	_, _, err := fake.IPAM.AutoAssign(context.TODO(), sip.ipamAssignArgs(1, subnetIDWest1Calico))
	Expect(err).NotTo(HaveOccurred())
	// Check we allocated exactly what we expected.
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
			_, _, err := fake.IPAM.AutoAssign(context.TODO(), sip.ipamAssignArgs(1, subnetIDWest1Calico))
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

type sipTestFakes struct {
	IPAM         *fakeIPAM
	EC2          *fakeEC2
	BackoffClock *clock.FakeClock
	RecheckClock *clock.FakeClock
	Health       *fakeHealth
	CapacityC    chan SecondaryIfaceCapacities
}

func (f sipTestFakes) expectSingleBackoffAndStep() {
	// Initial backoff should be between 1000 and 1100 ms (due to jitter).
	logrus.Info("Expecting single backoff and step...")
	Eventually(f.BackoffClock.HasWaiters).Should(BeTrue(), "expected a backoff to be scheduled")
	Expect(f.RecheckClock.HasWaiters()).Should(BeFalse(), "when backoff is scheduled, recheck should not be")
	f.BackoffClock.Step(999 * time.Millisecond)
	Expect(f.BackoffClock.HasWaiters()).To(BeTrue(), "expected a backoff to be scheduled after >999ms")
	f.BackoffClock.Step(102 * time.Millisecond)
	Eventually(f.RecheckClock.HasWaiters).Should(BeTrue(), "when backoff is not scheduled, recheck should be")
	Expect(f.BackoffClock.HasWaiters()).To(BeFalse(), "expected backoff to be cleared")
}

func setup(t *testing.T, opts ...IfaceProvOpt) (*SecondaryIfaceProvisioner, *sipTestFakes) {
	RegisterTestingT(t)

	cleanUpAWSSubnetsFile()

	fakeIPAM := newFakeIPAM()
	theTime, err := time.Parse("2006-01-02 15:04:05.000", "2021-09-15 16:00:00.000")
	Expect(err).NotTo(HaveOccurred())
	fakeBackoffClock := clock.NewFakeClock(theTime)
	fakeRecheckClock := clock.NewFakeClock(theTime)
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

	defaultOpts := []IfaceProvOpt{
		OptClockOverrides(fakeBackoffClock, fakeRecheckClock),
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
		// Disable the watchdog by default so that we can more easily check other timers.
		OptLivenessEnabled(false),
		OptSubnetsFileOverride(awsSubnetsFilename),
	}

	opts = append(defaultOpts, opts...)

	fakeHealth := NewFakeHealth()
	sip := NewSecondaryIfaceProvisioner(
		nodeName,
		fakeHealth,
		fakeIPAM,
		opts...,
	)

	return sip, &sipTestFakes{
		IPAM:         fakeIPAM,
		EC2:          fakeEC2,
		BackoffClock: fakeBackoffClock,
		RecheckClock: fakeRecheckClock,
		Health:       fakeHealth,
		CapacityC:    capacityC,
	}
}

func cleanUpAWSSubnetsFile() {
	if _, err := os.Stat(awsSubnetsFilename); err == nil {
		err := os.Remove(awsSubnetsFilename)
		Expect(err).NotTo(HaveOccurred())
	}
}

func setupAndStart(t *testing.T, opts ...IfaceProvOpt) (*SecondaryIfaceProvisioner, *sipTestFakes, func()) {
	sip, fake := setup(t, opts...)
	ctx, cancel := context.WithCancel(context.Background())
	doneC := sip.Start(ctx)
	return sip, fake, func() {
		defer cleanUpAWSSubnetsFile()
		cancel()
		Eventually(doneC).Should(BeClosed())
		fake.EC2.Errors.ExpectAllErrorsConsumed()
	}
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

type fakeHealth struct {
	lock          sync.Mutex
	registrations map[string]registration
	lastReport    map[string]health.HealthReport
}

func NewFakeHealth() *fakeHealth {
	return &fakeHealth{
		registrations: map[string]registration{},
		lastReport:    map[string]health.HealthReport{},
	}
}

type registration struct {
	Reports health.HealthReport
	Timeout time.Duration
}

func (f *fakeHealth) RegisterReporter(name string, reports *health.HealthReport, timeout time.Duration) {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.registrations[name] = registration{
		Reports: *reports,
		Timeout: timeout,
	}
}

func (f *fakeHealth) Report(name string, report *health.HealthReport) {
	f.lock.Lock()
	defer f.lock.Unlock()
	if _, ok := f.registrations[name]; !ok {
		panic("missing registration " + name)
	}
	f.lastReport[name] = *report
}

func (f *fakeHealth) getRegistrations() map[string]registration {
	f.lock.Lock()
	defer f.lock.Unlock()
	cp := make(map[string]registration)
	for k, v := range f.registrations {
		cp[k] = v
	}
	return cp
}

func (f *fakeHealth) getLastReports() map[string]health.HealthReport {
	f.lock.Lock()
	defer f.lock.Unlock()
	cp := make(map[string]health.HealthReport)
	for k, v := range f.lastReport {
		cp[k] = v
	}
	return cp
}

func (f *fakeHealth) clearReports() {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.lastReport = map[string]health.HealthReport{}
}
