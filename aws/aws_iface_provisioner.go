// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/sirupsen/logrus"
	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/projectcalico/felix/ip"
	"github.com/projectcalico/felix/logutils"
	calierrors "github.com/projectcalico/libcalico-go/lib/errors"
	"github.com/projectcalico/libcalico-go/lib/health"
	"github.com/projectcalico/libcalico-go/lib/ipam"
	calinet "github.com/projectcalico/libcalico-go/lib/net"
	"github.com/projectcalico/libcalico-go/lib/set"
)

const (
	// MaxInterfacesPerInstance is the current maximum total number of ENIs supported by any AWS instance type.
	// We only support the first network card on an instance right now so limiting to the maximum that one network
	// card can support.
	MaxInterfacesPerInstance = 15

	// SecondaryInterfaceCap is the maximum number of Calico secondary ENIs that we support.  The only reason to
	// cap this right now is so that we can pre-allocate one routing table per possible secondary ENI.
	SecondaryInterfaceCap = MaxInterfacesPerInstance - 1
)

// ipamInterface is just the parts of the IPAM interface that we need.
type ipamInterface interface {
	AutoAssign(ctx context.Context, args ipam.AutoAssignArgs) (*ipam.IPAMAssignments, *ipam.IPAMAssignments, error)
	ReleaseIPs(ctx context.Context, ips []calinet.IP) ([]calinet.IP, error)
	IPsByHandle(ctx context.Context, handleID string) ([]calinet.IP, error)
}

// Compile-time assert: ipamInterface should match the real interface.
var _ ipamInterface = ipam.Interface(nil)

// SecondaryIfaceProvisioner manages the AWS resources required to route certain local workload traffic
// (for example, the outbound traffic from egress gateways) over the AWS fabric with a "real" AWS IP.
//
// As an API, it accepts snapshots of the current state of the relevant local workloads via the
// OnDatastoreUpdate method.  That method queues the update to the background goroutine via a channel.
// When the AWS state has converged, the background loop sends a response back on the ResponseC() channel.
// This contains the state of the AWS interfaces attached to this host in order for the main dataplane
// goroutine to program appropriate local routes.
//
// The background goroutine's main loop:
// - Waits for a new snapshot.
// - When a new snapshot arrives, it triggers a resync against AWS.
// - On AWS or IPAM failure, it does exponential backoff.
//
// Resyncs consist of:
// - Reading the capacity limits of our node type.
// - Reading the current state of our instance and ENIs over the AWS API. If the node has additional
//   non-calico ENIs they are taken into account when calculating the capacity available to Calico
// - Call the "capacityCallback" to tell other components how many IPs we can support.
// - Analysing the datastore state to choose a single "best" AWS subnet.  We only support a single AWS
//   subnet per node to avoid having to balance IPs between multiple ENIs on different subnets.
//   (It's a misconfiguration to have multiple valid subnets but we need to tolerate it to do IP pool
//   migrations.)
// - Comparing the set of local workload routes against the IPs that we've already assigned to ENIs.
// - Remove any AWS IPs that are no longer needed.
// - If there are more IPs than the existing ENIs can handle, try to allocate additional host IPs in
//   calico IPAM and then create and attach new AWS ENIs with those IPs.
// - Allocate the new IPs to ENIs and assign them in AWS.
// - Respond to the main thread.
//
// Since failures can occur at any stage, we check for
// - Leaked IPs
// - Created but unattached ENIs
// - etc
// and clean those up pro-actively.
//
// If the number of local workloads we need exceeds the capacity of the node for secondary ENIs then we
// make a best effort; assigning as many as possible and signaling the problem through a health report.
//
// To ensure that we can spot _our_ ENIs even if we fail to attach them, we label them with an "owned by
// Calico" tag and a second tag that contains the instance ID of this node.
type SecondaryIfaceProvisioner struct {
	nodeName           string
	awsSubnetsFilename string
	timeout            time.Duration
	// Separate Clock shims for the two timers so the UTs can monitor/trigger the timers separately.
	backoffClock               clock.Clock
	recheckClock               clock.Clock
	recheckIntervalResetNeeded bool
	newEC2Client               func(ctx context.Context) (*EC2Client, error)

	healthAgg       healthAggregator
	livenessEnabled bool
	opRecorder      *logutils.Summarizer
	ipamClient      ipamInterface

	// resyncNeeded is set to true if we need to do any kind of resync.
	resyncNeeded bool
	// orphanENIResyncNeeded is set to true if the next resync should also check for orphaned ENIs.  I.e. ones
	// that this node previously created but which never got attached.  (For example, because felix restarted.)
	orphanENIResyncNeeded bool
	// hostIPAMResyncNeeded is set to true if the next resync should also check for IPs that are assigned to this
	// node but not in use for one of our ENIs.
	hostIPAMResyncNeeded bool

	cachedEC2Client     *EC2Client
	networkCapabilities *NetworkCapabilities
	awsGatewayAddr      ip.Addr
	awsSubnetCIDR       ip.CIDR

	// datastoreUpdateC carries updates from Felix's main dataplane loop to our loop.
	datastoreUpdateC chan DatastoreState
	// ds is the most recent datastore state we've received.
	ds DatastoreState

	// ResponseC is our channel back to the main dataplane loop.
	responseC        chan *LocalAWSNetworkState
	capacityCallback func(SecondaryIfaceCapacities)
}

type DatastoreState struct {
	LocalAWSAddrsByDst        map[ip.CIDR]AddrInfo
	LocalRouteDestsBySubnetID map[string]set.Set /*ip.CIDR*/
	PoolIDsBySubnetID         map[string]set.Set /*string*/
}

type AddrInfo struct {
	AWSSubnetId string
	Dst         string
}

const (
	healthNameAWSProvisioner = "aws-eni-provisioner"
	healthNameENICapacity    = "aws-eni-capacity"
	healthNameAWSInSync      = "aws-eni-addresses-in-sync"
	defaultTimeout           = 30 * time.Second

	livenessReportInterval = 30 * time.Second
	livenessTimeout        = 300 * time.Second
)

type IfaceProvOpt func(provisioner *SecondaryIfaceProvisioner)

func OptTimeout(to time.Duration) IfaceProvOpt {
	return func(provisioner *SecondaryIfaceProvisioner) {
		provisioner.timeout = to
	}
}

func OptLivenessEnabled(livenessEnabled bool) IfaceProvOpt {
	return func(provisioner *SecondaryIfaceProvisioner) {
		provisioner.livenessEnabled = livenessEnabled
	}
}

func OptCapacityCallback(cb func(SecondaryIfaceCapacities)) IfaceProvOpt {
	return func(provisioner *SecondaryIfaceProvisioner) {
		provisioner.capacityCallback = cb
	}
}

func OptClockOverrides(backoffClock, recheckClock clock.Clock) IfaceProvOpt {
	return func(provisioner *SecondaryIfaceProvisioner) {
		provisioner.backoffClock = backoffClock
		provisioner.recheckClock = recheckClock
	}
}

func OptSubnetsFileOverride(filename string) IfaceProvOpt {
	return func(provisioner *SecondaryIfaceProvisioner) {
		provisioner.awsSubnetsFilename = filename
	}
}

func OptNewEC2ClientOverride(f func(ctx context.Context) (*EC2Client, error)) IfaceProvOpt {
	return func(provisioner *SecondaryIfaceProvisioner) {
		provisioner.newEC2Client = f
	}
}

type SecondaryIfaceCapacities struct {
	MaxCalicoSecondaryIPs int
}

func (c SecondaryIfaceCapacities) Equals(caps SecondaryIfaceCapacities) bool {
	return c == caps
}

type healthAggregator interface {
	RegisterReporter(name string, reports *health.HealthReport, timeout time.Duration)
	Report(name string, report *health.HealthReport)
}

func NewSecondaryIfaceProvisioner(
	nodeName string,
	healthAgg healthAggregator,
	ipamClient ipamInterface,
	options ...IfaceProvOpt,
) *SecondaryIfaceProvisioner {
	sip := &SecondaryIfaceProvisioner{
		healthAgg:          healthAgg,
		livenessEnabled:    true,
		ipamClient:         ipamClient,
		nodeName:           nodeName,
		awsSubnetsFilename: "/var/lib/calico/aws-subnets",
		timeout:            defaultTimeout,
		opRecorder:         logutils.NewSummarizer("AWS secondary IP reconciliation loop"),

		// Do the extra scans on first run.
		orphanENIResyncNeeded: true,
		hostIPAMResyncNeeded:  true,

		datastoreUpdateC: make(chan DatastoreState, 1),
		responseC:        make(chan *LocalAWSNetworkState, 1),
		backoffClock:     clock.RealClock{},
		recheckClock:     clock.RealClock{},
		capacityCallback: func(c SecondaryIfaceCapacities) {
			logrus.WithField("cap", c).Debug("Capacity updated but no callback configured.")
		},
		newEC2Client: NewEC2Client,
	}

	for _, o := range options {
		o(sip)
	}

	// Readiness flag used to indicate if we've got enough ENI capacity to handle all the local workloads
	// that need it. No liveness, we reserve that for the main loop watchdog (set up below).
	healthAgg.RegisterReporter(healthNameENICapacity, &health.HealthReport{Ready: true}, 0)
	healthAgg.Report(healthNameENICapacity, &health.HealthReport{Ready: true})
	// Similarly, readiness flag to report whether we succeeded in syncing with AWS.
	healthAgg.RegisterReporter(healthNameAWSInSync, &health.HealthReport{Ready: true}, 0)
	healthAgg.Report(healthNameAWSInSync, &health.HealthReport{Ready: true})
	if sip.livenessEnabled {
		// Health/liveness watchdog for our main loop.  We let this be disabled for ease of UT.
		healthAgg.RegisterReporter(
			healthNameAWSProvisioner,
			&health.HealthReport{Ready: true, Live: true},
			livenessTimeout,
		)
		healthAgg.Report(healthNameAWSProvisioner, &health.HealthReport{Ready: true, Live: true})
	}

	return sip
}

func (m *SecondaryIfaceProvisioner) Start(ctx context.Context) (done chan struct{}) {
	logrus.Info("Starting AWS secondary interface provisioner.")
	done = make(chan struct{})
	go m.loopKeepingAWSInSync(ctx, done)
	return
}

// LocalAWSNetworkState contains a snapshot of the current state of this node's networking (according to the AWS API).
type LocalAWSNetworkState struct {
	PrimaryENIMAC      string
	SecondaryENIsByMAC map[string]Iface
	SubnetCIDR         ip.CIDR
	GatewayAddr        ip.Addr
}

type Iface struct {
	ID                 string
	MAC                net.HardwareAddr
	PrimaryIPv4Addr    ip.Addr
	SecondaryIPv4Addrs []ip.Addr
}

func (m *SecondaryIfaceProvisioner) loopKeepingAWSInSync(ctx context.Context, doneC chan struct{}) {
	defer close(doneC)
	logrus.Info("AWS secondary interface provisioner running in background.")

	// Response channel is masked (nil) until we're ready to send something.
	var responseC chan *LocalAWSNetworkState
	var response *LocalAWSNetworkState

	// Set ourselves up for exponential backoff after a failure.  backoffMgr.Backoff() returns the same Timer
	// on each call so we need to stop it properly when cancelling it.
	var backoffTimer clock.Timer
	var backoffC <-chan time.Time
	backoffMgr := m.newBackoffManager()
	stopBackoffTimer := func() {
		if backoffTimer != nil {
			// New snapshot arrived, ignore the backoff since the new snapshot might resolve whatever issue
			// caused us to fail to resync.  We also must reset the timer before calling Backoff() again for
			// correct behaviour. This is the standard time.Timer.Stop() dance...
			if !backoffTimer.Stop() {
				<-backoffTimer.C()
			}
			backoffTimer = nil
			backoffC = nil
		}
	}
	defer stopBackoffTimer()

	// Create a simple backoff manager that can be used to schedule checks of the AWS API at
	// exponentially increasing intervals.  The k8s backoff machinery isn't quite suitable because
	// it doesn't provide a way to reset (and it remembers recent failures).  We just want a
	// way to queue up extra resyncs at 30s, 60s, 120s... after any given successful resync.
	const defaultRecheckInterval = 30 * time.Second
	const maxRecheckInterval = 30 * time.Minute
	recheckBackoffMgr := NewResettableBackoff(m.recheckClock, defaultRecheckInterval, maxRecheckInterval, 0.1)

	var livenessC <-chan time.Time
	if m.livenessEnabled {
		livenessTicker := m.backoffClock.NewTicker(livenessReportInterval)
		livenessC = livenessTicker.C()
	}

	for {
		// Thread safety: we receive messages _from_, and, send messages _to_ the dataplane main loop.
		// To avoid deadlock,
		// - Sends on datastoreUpdateC never block the main loop.  We ensure this by draining the capacity one
		//   channel before sending in OnDatastoreUpdate.
		// - We do our receives and sends in the same select block so that we never block a send op on a receive op
		//   or vice versa.
		thisIsARetry := false
		recheckTimerFired := false
		select {
		case <-ctx.Done():
			logrus.Info("SecondaryIfaceManager stopping, context canceled.")
			return
		case snapshot := <-m.datastoreUpdateC:
			logrus.Debug("New datastore snapshot received")
			m.resyncNeeded = true
			m.ds = snapshot
		case responseC <- response:
			// Mask the response channel so we don't resend again and again.
			logrus.WithField("response", response).Debug("Sent AWS state back to main goroutine")
			responseC = nil
			continue // Don't want sending a response to trigger an early resync.
		case <-livenessC:
			m.healthAgg.Report(healthNameAWSProvisioner, &health.HealthReport{Ready: true, Live: true})
			continue // Don't want liveness to trigger early resync.
		case <-backoffC:
			// Important: nil out the timer so that stopBackoffTimer() won't try to stop it again (and deadlock).
			backoffC = nil
			backoffTimer = nil
			logrus.Warn("Retrying AWS resync after backoff.")
			thisIsARetry = true
			m.opRecorder.RecordOperation("aws-retry")
		case <-recheckBackoffMgr.C():
			// AWS sometimes returns stale data and sometimes loses writes.  Recheck at increasing intervals
			// after a successful update.
			logrus.Debug("Recheck timer fired, checking AWS state is still correct.")
			m.resyncNeeded = true
			recheckTimerFired = true
			m.opRecorder.RecordOperation("aws-recheck")
		}

		startTime := time.Now()

		// Either backoff has done its job or another update has come along.  Clear any pending backoff.
		stopBackoffTimer()

		if m.resyncNeeded {
			// Only stop the recheck timer if we're actually doing a resync.
			recheckBackoffMgr.Stop(recheckTimerFired)
			recheckTimerFired = false

			var err error
			response, err = m.resync(ctx)
			m.healthAgg.Report(healthNameAWSInSync, &health.HealthReport{Ready: err == nil})
			if err != nil {
				logrus.WithError(err).Warning("Failed to resync with AWS. Will retry after backoff.")
				backoffTimer = backoffMgr.Backoff()
				backoffC = backoffTimer.C()
				// We don't reschedule the recheck timer here since we've already got a retry backoff timer
				// queued up but we do reset the interval for next time.
				m.resetRecheckInterval("resync-failure")
			} else {
				// Success, we're now in sync.
				if thisIsARetry {
					logrus.Info("Retry successful, now in sync with AWS.")
				}
				// However, AWS can sometimes lose updates, schedule a recheck on an exponential backoff.
				if m.recheckIntervalResetNeeded {
					// We just made a change to the AWS dataplane (or hit an error), reset the time to the
					// next recheck.
					logrus.Debug("Resetting time to next AWS recheck.")
					recheckBackoffMgr.ResetInterval()
					m.recheckIntervalResetNeeded = false
				}
				recheckBackoffMgr.Reschedule(false /*we already reset the timer above*/)
			}
			if response == nil {
				responseC = nil
			} else {
				responseC = m.responseC
			}
		}

		m.opRecorder.EndOfIteration(time.Since(startTime))
	}
}

func (m *SecondaryIfaceProvisioner) newBackoffManager() wait.BackoffManager {
	const (
		initBackoff   = 1 * time.Second
		maxBackoff    = 1 * time.Minute
		resetDuration = 10 * time.Minute
		backoffFactor = 2.0
		jitter        = 0.1
	)
	backoffMgr := wait.NewExponentialBackoffManager(initBackoff, maxBackoff, resetDuration, backoffFactor, jitter, m.backoffClock)
	return backoffMgr
}

func (m *SecondaryIfaceProvisioner) ResponseC() <-chan *LocalAWSNetworkState {
	return m.responseC
}

func (m *SecondaryIfaceProvisioner) OnDatastoreUpdate(snapshot DatastoreState) {
	// To make sure we don't block, drain any pending update from the channel.
	select {
	case <-m.datastoreUpdateC:
		// Discarded previous snapshot, channel now has capacity for new one.
	default:
		// No pending update.  We're ready to send a new one.
	}
	// Should have capacity in the channel now to send without blocking.
	m.datastoreUpdateC <- snapshot
}

var errResyncNeeded = errors.New("resync needed")
var errStaleRead = errors.New("AWS API returned stale data")

func (m *SecondaryIfaceProvisioner) resync(ctx context.Context) (*LocalAWSNetworkState, error) {
	var awsResyncErr error
	m.opRecorder.RecordOperation("aws-fabric-resync")
	var response *LocalAWSNetworkState

	// attemptResync() returns two types of error:
	//
	// - errResyncNeeded, which indicates that _we_ made a change to AWS state and need to restart the resync
	//   to pick up the results of the change.
	// - General AWS/IPAM/etc errors.
	//
	// We want to retry the first type immediately; they're part of our normal processing. The other type can
	// mean that AWS is overloaded, or throttling us, so we bubble up the error and trigger backoff.
	//
	// attemptResync() should only return errResyncNeeded at most a handful of times so, if we see lots of those
	// we also bubble up the error and start backing off.
	for attempt := 0; attempt < 5; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, awsResyncErr = m.attemptResync()
		if errors.Is(awsResyncErr, errResyncNeeded) {
			// Expected retry needed for some more complex cases...
			logrus.Debug("Restarting resync after modifying AWS state.")
			continue
		} else if awsResyncErr != nil {
			m.cachedEC2Client = nil // Maybe something wrong with client?
			break
		}
		m.resyncNeeded = false
		break
	}
	return response, awsResyncErr
}

func (m *SecondaryIfaceProvisioner) attemptResync() (*LocalAWSNetworkState, error) {
	if m.networkCapabilities == nil {
		// Figure out what kind of instance we are and how many ENIs and IPs we can support.
		netCaps, err := m.getMyNetworkCapabilities()
		if err != nil {
			logrus.WithError(err).Error("Failed to get this node's network capabilities from the AWS API; " +
				"are AWS API permissions properly configured?")
			return nil, err
		}
		logrus.WithField("netCaps", netCaps).Info("Retrieved my instance's network capabilities")
		// Cache off the network capabilities since this shouldn't change during the lifetime of an instance.
		m.networkCapabilities = netCaps
	}

	// Collect the current state of this instance and our ENIs according to AWS.
	awsSnapshot, resyncState, err := m.loadAWSENIsState()
	if err != nil {
		return nil, err
	}

	// Let the kubernetes Node updater know our capacity.
	m.capacityCallback(SecondaryIfaceCapacities{
		MaxCalicoSecondaryIPs: m.calculateMaxCalicoSecondaryIPs(awsSnapshot),
	})

	// Scan for ENIs that don't have their "delete on termination" flag set and fix up.
	err = m.ensureCalicoENIsDelOnTerminate(awsSnapshot)
	if err != nil {
		return nil, err
	}

	// Scan for IPs that are present on our AWS ENIs but no longer required by Calico.
	awsIPsToRelease := m.findUnusedAWSIPs(awsSnapshot)

	// Release any AWS IPs that are no longer required.
	err = m.unassignAWSIPs(awsIPsToRelease, awsSnapshot)
	if err != nil {
		return nil, err // errResyncNeeded if there were any IPs released to trigger a refresh of AWS state.
	}

	// Scan for ENIs that are in a subnet that no longer matches an IP pool. We don't currently release
	// ENIs just because they have no associated pods.  This helps to reduce AWS API churn as pods
	// come and go: only the IP addresses need to be added/removed, not a whole ENI.
	enisToRelease := m.findENIsWithNoPool(awsSnapshot)

	// Release any AWS ENIs that are no longer needed.
	err = m.releaseAWSENIs(enisToRelease, awsSnapshot)
	if err != nil {
		return nil, err // errResyncNeeded if there were any ENIs released to trigger a refresh of AWS state.
	}

	// Figure out the AWS subnets that live in our AZ.  We can only create ENIs within these subnets.
	localSubnetsByID, err := m.loadLocalAWSSubnets()
	if err != nil {
		return nil, err
	}

	// Let the CNI plugin know our local subnets.  Do this now before the possible early return if there's no
	// "best" subnet below.
	err = m.updateAWSSubnetFile(localSubnetsByID)
	if err != nil {
		return nil, fmt.Errorf("failed to write AWS subnets to file: %w", err)
	}

	// Figure out which Calico IPs are not present on our AWS ENIs.
	allCalicoRoutesNotInAWS := m.findRoutesWithNoAWSAddr(awsSnapshot, localSubnetsByID)

	// We only support a single local subnet, choose one based on some heuristics.
	bestSubnetID := m.calculateBestSubnet(awsSnapshot, localSubnetsByID)
	if bestSubnetID == "" {
		logrus.Debug("No AWS subnets needed.")
		return &LocalAWSNetworkState{}, nil
	}

	// Record the gateway address of the best subnet.
	bestSubnet := localSubnetsByID[bestSubnetID]
	subnetCIDR, gatewayAddr, err := m.subnetCIDRAndGW(bestSubnet)
	if err != nil {
		return nil, err
	}
	if m.awsGatewayAddr != gatewayAddr || m.awsSubnetCIDR != subnetCIDR {
		logrus.WithFields(logrus.Fields{
			"addr":   gatewayAddr,
			"subnet": subnetCIDR,
		}).Info("Calculated new AWS subnet CIDR/gateway.")
		m.awsGatewayAddr = gatewayAddr
		m.awsSubnetCIDR = subnetCIDR
	}

	// Given the selected subnet, filter down the routes to only those that we can support.
	subnetCalicoRoutesNotInAWS := filterRoutesByAWSSubnet(allCalicoRoutesNotInAWS, bestSubnetID)
	if len(subnetCalicoRoutesNotInAWS) == 0 {
		logrus.Debug("No new AWS IPs to program")
		return m.calculateResponse(awsSnapshot)
	}

	if m.orphanENIResyncNeeded {
		// Look for and attach any AWS interfaces that belong to this node but that are not already attached.
		// We identify such interfaces by Calico-specific tags that we add to the interfaces at creation time.
		err = m.attachOrphanENIs(resyncState, bestSubnetID)
		if err != nil {
			return nil, err
		}
		// We won't need to do this again unless we fail to attach an ENI in the future.
		m.orphanENIResyncNeeded = false
	}

	if m.hostIPAMResyncNeeded {
		// Now we've cleaned up any unneeded ENIs. Free any IPs that are assigned to us in IPAM but not in use for
		// one of our ENIs.
		err = m.freeUnusedHostCalicoIPs(awsSnapshot)
		if err != nil {
			return nil, fmt.Errorf("failed to release unused secondary interface IP in Calico IPAM: %w", err)
		}
		// Won't need to do this again unless we hit an IPAM error.
		m.hostIPAMResyncNeeded = false
	}

	// Figure out if we need to add any new ENIs to the host.
	numENIsNeeded, err := m.calculateNumNewENIsNeeded(awsSnapshot, bestSubnetID)
	if err != nil {
		return nil, err
	}

	numENIsToCreate := numENIsNeeded
	if numENIsNeeded > 0 {
		// Check if we _can_ create that many ENIs.
		numENIsPossible := resyncState.calculateUnusedENICapacity(m.networkCapabilities)
		haveENICapacity := numENIsToCreate <= numENIsPossible
		m.healthAgg.Report(healthNameENICapacity, &health.HealthReport{Ready: haveENICapacity})
		if !haveENICapacity {
			logrus.Warnf("Need %d more AWS secondary ENIs to support local workloads but only %d are "+
				"available.  Some local workloads (typically egress gateways) will not have connectivity on "+
				"the AWS fabric.", numENIsToCreate, numENIsPossible)
			numENIsToCreate = numENIsPossible // Avoid trying to create ENIs that we know will fail.
		}
	}

	if numENIsToCreate > 0 {
		logrus.WithField("num", numENIsToCreate).Info("Allocating IPs for new AWS ENIs.")
		v4addrs, err := m.allocateCalicoHostIPs(numENIsToCreate, bestSubnetID)
		if err != nil {
			// Queue up a clean up of any IPs we may have leaked.
			m.hostIPAMResyncNeeded = true
			return nil, err
		}
		logrus.WithField("addrs", v4addrs.IPs).Info("Allocated IPs; creating AWS ENIs...")
		err = m.createAWSENIs(awsSnapshot, resyncState, bestSubnetID, v4addrs.IPs)
		if err != nil {
			// Queue up a cleanup of any IPs we may have leaked.
			m.hostIPAMResyncNeeded = true
			return nil, err
		}
	}

	// Tell AWS to assign the needed Calico IPs to the secondary ENIs as best we can.  (It's possible we weren't able
	// to allocate enough IPs or ENIs above.)
	err = m.assignSecondaryIPsToENIs(resyncState, subnetCalicoRoutesNotInAWS)
	if errors.Is(err, errResyncNeeded) {
		// This is the mainline after we assign an IP, avoid doing a full resync and just reload the snapshot.
		logrus.Debug("Rechecking AWS state after making updates...")
		awsSnapshot, _, err = m.loadAWSENIsState()
		if err != nil {
			return nil, err
		}

		// The AWS API is eventually consistent, meaning that the updates we just made may not show up in the
		// new snapshot.  Verify the snapshot before we accept it.  We've also seen AWS lose writes, for example,
		// if we unassign an IP and then assign the same IP 30s later, the assign may get lost.
		missingRoutes := m.findRoutesWithNoAWSAddr(awsSnapshot, localSubnetsByID)
		missingRoutesOurSubnet := filterRoutesByAWSSubnet(missingRoutes, bestSubnetID)
		unusedAWSIPs := m.findUnusedAWSIPs(awsSnapshot)
		if len(missingRoutesOurSubnet) == 0 && unusedAWSIPs.Len() == 0 {
			logrus.Debug("Read back good AWS state.")
			err = nil
		} else {
			var missingIPs []string
			for _, r := range missingRoutesOurSubnet {
				missingIPs = append(missingIPs, r.Dst)
			}
			var extraIPs []string
			unusedAWSIPs.Iter(func(item interface{}) error {
				extraIPs = append(extraIPs, item.(ip.CIDR).Addr().String())
				return nil
			})
			logrus.WithFields(logrus.Fields{"missingIPs": missingIPs, "extraIPs": extraIPs}).Info(
				"After updating AWS, AWS API returned stale data, triggering another resync to " +
					"re-check the state.")
			// Dedicated error to trigger backoff as recommended by AWS docs.
			err = errStaleRead
		}
	}
	if err != nil {
		return nil, err
	}
	return m.calculateResponse(awsSnapshot)
}

// subnetsFileData contents of the aws-subnets file.  We write a JSON dict for extensibility.
// Must match the definition in the CNI plugin's utils.go.
type subnetsFileData struct {
	AWSSubnetIDs []string `json:"aws_subnet_ids"`
}

func (m *SecondaryIfaceProvisioner) updateAWSSubnetFile(subnets map[string]ec2types.Subnet) error {
	var data subnetsFileData
	for id := range subnets {
		data.AWSSubnetIDs = append(data.AWSSubnetIDs, id)
	}
	sort.Strings(data.AWSSubnetIDs)
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}

	// Avoid rewriting the file if it hasn't changed.  Since subnet updates are rare, this reduces the chance
	// of the CNI plugin seeing a partially-written file.  If the file is missing/partial then the CNI plugin
	// will fail to read/parse the file and bail out.  The CNI plugin only tries to read this file if the pod
	// has the aws-secondary-ip resource request so only AWS-backed pods are in danger of failing.
	oldData, err := ioutil.ReadFile(m.awsSubnetsFilename)
	if err == nil {
		if bytes.Equal(oldData, encoded) {
			logrus.Debug("AWS subnets file already correct.")
			return nil
		}
	} else {
		logrus.WithError(err).Debug("Failed to read old aws-subnets file.  Rewriting it...")
	}

	err = ioutil.WriteFile(m.awsSubnetsFilename, encoded, 0644)
	if err != nil {
		return err
	}
	return nil
}

func (m *SecondaryIfaceProvisioner) calculateResponse(awsENIState *eniSnapshot) (*LocalAWSNetworkState, error) {
	// Index the AWS ENIs on MAC.
	ifacesByMAC := map[string]Iface{}
	for _, awsENI := range awsENIState.calicoOwnedENIsByID {
		iface, err := m.ec2ENIToIface(&awsENI)
		if err != nil {
			logrus.WithError(err).Warn("Failed to convert AWS ENI.")
			continue
		}
		ifacesByMAC[iface.MAC.String()] = *iface
	}
	primaryENI, err := m.ec2ENIToIface(awsENIState.primaryENI)
	if err != nil {
		logrus.WithError(err).Error("Failed to convert primary ENI.")
		return nil, err
	}
	return &LocalAWSNetworkState{
		PrimaryENIMAC:      primaryENI.MAC.String(),
		SecondaryENIsByMAC: ifacesByMAC,
		SubnetCIDR:         m.awsSubnetCIDR,
		GatewayAddr:        m.awsGatewayAddr,
	}, nil
}

var errNoMAC = errors.New("AWS ENI missing MAC")

func (m *SecondaryIfaceProvisioner) ec2ENIToIface(awsENI *ec2types.NetworkInterface) (*Iface, error) {
	if awsENI.MacAddress == nil {
		return nil, errNoMAC
	}
	hwAddr, err := net.ParseMAC(*awsENI.MacAddress)
	if err != nil {
		logrus.WithError(err).Error("Failed to parse MAC address of AWS ENI.")
		return nil, fmt.Errorf("AWS ENI's MAC address was malformed: %w", err)
	}
	var primary ip.Addr
	var secondaryAddrs []ip.Addr
	for _, pa := range awsENI.PrivateIpAddresses {
		if pa.PrivateIpAddress == nil {
			continue
		}
		addr := ip.FromString(*pa.PrivateIpAddress)
		if pa.Primary != nil && *pa.Primary {
			primary = addr
		} else {
			secondaryAddrs = append(secondaryAddrs, addr)
		}
	}
	iface := &Iface{
		ID:                 safeReadString(awsENI.NetworkInterfaceId),
		MAC:                hwAddr,
		PrimaryIPv4Addr:    primary,
		SecondaryIPv4Addrs: secondaryAddrs,
	}
	return iface, nil
}

// getMyNetworkCapabilities looks up the network capabilities of this host; this includes the number of ENIs
// and IPs per ENI.
func (m *SecondaryIfaceProvisioner) getMyNetworkCapabilities() (*NetworkCapabilities, error) {
	ctx, cancel := m.newContext()
	defer cancel()
	ec2Client, err := m.ec2Client()
	if err != nil {
		return nil, err
	}
	netCaps, err := ec2Client.GetMyNetworkCapabilities(ctx)
	if err != nil {
		return nil, err
	}

	if netCaps.MaxNetworkInterfaces > MaxInterfacesPerInstance {
		logrus.Infof("Instance type supports %v interfaces, limiting to our interface cap (%v)",
			netCaps.MaxNetworkInterfaces, MaxInterfacesPerInstance)
		netCaps.MaxNetworkInterfaces = MaxInterfacesPerInstance
	}
	return &netCaps, nil
}

// eniSnapshot captures the current state of the AWS ENIs attached to this host, indexed in various ways.
type eniSnapshot struct {
	primaryENI             *ec2types.NetworkInterface
	calicoOwnedENIsByID    map[string]ec2types.NetworkInterface
	nonCalicoOwnedENIsByID map[string]ec2types.NetworkInterface
	eniIDsBySubnet         map[string][]string
	eniIDByIP              map[ip.CIDR]string
	eniIDByPrimaryIP       map[ip.CIDR]string
	attachmentIDByENIID    map[string]string
}

func (s *eniSnapshot) PrimaryENISecurityGroups() []string {
	var securityGroups []string
	for _, sg := range s.primaryENI.Groups {
		if sg.GroupId == nil {
			continue
		}
		securityGroups = append(securityGroups, *sg.GroupId)
	}
	return securityGroups
}

// eniResyncState is the working state of the in-progress resync.  We update this as the resync progresses.
type eniResyncState struct {
	inUseDeviceIndexes      map[int32]bool
	freeIPv4CapacityByENIID map[string]int
}

func (r *eniResyncState) calculateUnusedENICapacity(netCaps *NetworkCapabilities) int {
	// For now, only supporting the first network card.
	numPossibleENIs := netCaps.MaxENIsForCard(0)
	numExistingENIs := len(r.inUseDeviceIndexes)
	return numPossibleENIs - numExistingENIs
}

func (r *eniResyncState) FindFreeDeviceIdx() int32 {
	devIdx := int32(0)
	for r.inUseDeviceIndexes[devIdx] {
		devIdx++
	}
	return devIdx
}

func (r *eniResyncState) ClaimDeviceIdx(devIdx int32) {
	r.inUseDeviceIndexes[devIdx] = true
}

// loadAWSENIsState looks up all the ENIs attached to this host and creates an eniSnapshot to index them.
func (m *SecondaryIfaceProvisioner) loadAWSENIsState() (s *eniSnapshot, r *eniResyncState, err error) {
	ctx, cancel := m.newContext()
	defer cancel()
	ec2Client, err := m.ec2Client()
	if err != nil {
		return nil, nil, err
	}

	myENIs, err := ec2Client.GetMyEC2NetworkInterfaces(ctx)
	if err != nil {
		return
	}

	s = &eniSnapshot{
		calicoOwnedENIsByID:    map[string]ec2types.NetworkInterface{},
		nonCalicoOwnedENIsByID: map[string]ec2types.NetworkInterface{},
		eniIDsBySubnet:         map[string][]string{},
		eniIDByIP:              map[ip.CIDR]string{},
		eniIDByPrimaryIP:       map[ip.CIDR]string{},
		attachmentIDByENIID:    map[string]string{},
	}

	r = &eniResyncState{
		inUseDeviceIndexes:      map[int32]bool{},
		freeIPv4CapacityByENIID: map[string]int{},
	}

	for _, eni := range myENIs {
		eni := eni
		if eni.NetworkInterfaceId == nil {
			// This feels like it'd be a bug in the AWS API.
			logrus.WithField("eni", eni).Debug("AWS returned ENI with no NetworkInterfaceId, ignoring.")
			continue
		}
		if eni.Attachment != nil {
			if eni.Attachment.DeviceIndex != nil {
				r.inUseDeviceIndexes[*eni.Attachment.DeviceIndex] = true
			}
			if eni.Attachment.NetworkCardIndex != nil && *eni.Attachment.NetworkCardIndex != 0 {
				// Ignore ENIs that aren't on the primary network card.  We only support one network card for now.
				logrus.Debugf("Ignoring ENI on non-primary network card: %d.", *eni.Attachment.NetworkCardIndex)
				continue
			}
			if eni.Attachment.AttachmentId != nil {
				s.attachmentIDByENIID[*eni.NetworkInterfaceId] = *eni.Attachment.AttachmentId
			}
		}
		if !NetworkInterfaceIsCalicoSecondary(eni) {
			if s.primaryENI == nil || eni.Attachment != nil && eni.Attachment.DeviceIndex != nil && *eni.Attachment.DeviceIndex == 0 {
				s.primaryENI = &eni
			}
			s.nonCalicoOwnedENIsByID[*eni.NetworkInterfaceId] = eni
			continue
		}
		// Found one of our managed interfaces; collect its IPs.
		logCtx := logrus.WithField("id", *eni.NetworkInterfaceId)
		logCtx.Debug("Found Calico ENI")
		s.calicoOwnedENIsByID[*eni.NetworkInterfaceId] = eni
		s.eniIDsBySubnet[*eni.SubnetId] = append(s.eniIDsBySubnet[*eni.SubnetId], *eni.NetworkInterfaceId)
		for _, addr := range eni.PrivateIpAddresses {
			if addr.PrivateIpAddress == nil {
				continue
			}
			cidr := ip.MustParseCIDROrIP(*addr.PrivateIpAddress)
			if addr.Primary != nil && *addr.Primary {
				logCtx.WithField("ip", *addr.PrivateIpAddress).Debug("Found primary IP on Calico ENI")
				s.eniIDByPrimaryIP[cidr] = *eni.NetworkInterfaceId
			} else {
				logCtx.WithField("ip", *addr.PrivateIpAddress).Debug("Found secondary IP on Calico ENI")
				s.eniIDByIP[cidr] = *eni.NetworkInterfaceId
			}
		}

		r.freeIPv4CapacityByENIID[*eni.NetworkInterfaceId] = m.networkCapabilities.MaxIPv4PerInterface - len(eni.PrivateIpAddresses)
		logCtx.WithField("availableIPs", r.freeIPv4CapacityByENIID[*eni.NetworkInterfaceId]).Debug("Calculated available IPs")
		if r.freeIPv4CapacityByENIID[*eni.NetworkInterfaceId] < 0 {
			logCtx.Errorf("ENI appears to have more IPs (%v) that it should (%v)", len(eni.PrivateIpAddresses), m.networkCapabilities.MaxIPv4PerInterface)
			r.freeIPv4CapacityByENIID[*eni.NetworkInterfaceId] = 0
		}
	}

	return
}

// findUnusedAWSIPs scans the AWS state for secondary IPs that are not assigned in Calico IPAM.
func (m *SecondaryIfaceProvisioner) findUnusedAWSIPs(awsState *eniSnapshot) set.Set /* ip.CIDR */ {
	awsIPsToRelease := set.New()
	summary := map[string][]string{}
	for addr, eniID := range awsState.eniIDByIP {
		if _, ok := m.ds.LocalAWSAddrsByDst[addr]; !ok {
			awsIPsToRelease.Add(addr)
			summary[eniID] = append(summary[eniID], addr.Addr().String())
		}
	}
	if len(summary) > 0 && logrus.GetLevel() >= logrus.InfoLevel {
		for eni, addrs := range summary {
			logrus.WithFields(logrus.Fields{
				"eniID": eni,
				"addrs": strings.Join(addrs, ","),
			}).Info("Found unneeded AWS secondary IPs.")
		}
	}
	return awsIPsToRelease
}

// loadLocalAWSSubnets looks up all the AWS Subnets that are in this host's VPC and availability zone.
func (m *SecondaryIfaceProvisioner) loadLocalAWSSubnets() (map[string]ec2types.Subnet, error) {
	ctx, cancel := m.newContext()
	defer cancel()
	ec2Client, err := m.ec2Client()
	if err != nil {
		return nil, err
	}

	localSubnets, err := ec2Client.GetAZLocalSubnets(ctx)
	if err != nil {
		return nil, err
	}
	localSubnetsByID := map[string]ec2types.Subnet{}
	for _, s := range localSubnets {
		if s.SubnetId == nil {
			continue
		}
		localSubnetsByID[*s.SubnetId] = s
	}
	return localSubnetsByID, nil
}

// findENIsWithNoPool scans the eniSnapshot for secondary AWS ENIs that were created by Calico but no longer
// have an associated IP pool.
func (m *SecondaryIfaceProvisioner) findENIsWithNoPool(awsENIState *eniSnapshot) set.Set {
	enisToRelease := set.New()
	for eniID, eni := range awsENIState.calicoOwnedENIsByID {
		if _, ok := m.ds.PoolIDsBySubnetID[*eni.SubnetId]; ok {
			continue
		}
		// No longer have an IP pool for this ENI.
		logrus.WithFields(logrus.Fields{
			"eniID":  eniID,
			"subnet": *eni.SubnetId,
		}).Info("AWS ENI belongs to subnet with no matching Calico IP pool, ENI should be released")
		enisToRelease.Add(eniID)
	}
	return enisToRelease
}

// findRoutesWithNoAWSAddr Scans our local Calico workload routes for routes with no corresponding AWS IP.
func (m *SecondaryIfaceProvisioner) findRoutesWithNoAWSAddr(awsENIState *eniSnapshot, localSubnetsByID map[string]ec2types.Subnet) []AddrInfo {
	var missingRoutes []AddrInfo
	var missingIPSummary []string
	for addr, route := range m.ds.LocalAWSAddrsByDst {
		if _, ok := localSubnetsByID[route.AWSSubnetId]; !ok {
			logrus.WithFields(logrus.Fields{
				"addr":           addr,
				"requiredSubnet": route.AWSSubnetId,
			}).Warn("Local workload needs an IP from an AWS subnet that is not accessible from this " +
				"availability zone. Unable to allocate an AWS IP for it.")
			continue
		}
		if eniID, ok := awsENIState.eniIDByPrimaryIP[addr]; ok {
			logrus.WithFields(logrus.Fields{
				"addr": addr,
				"eni":  eniID,
			}).Warn("Local workload IP clashes with host's primary IP on one of its secondary interfaces. " +
				"Workload will not be properly networked.")
			continue
		}
		if eniID, ok := awsENIState.eniIDByIP[addr]; ok {
			logrus.WithFields(logrus.Fields{
				"addr": addr,
				"eni":  eniID,
			}).Debug("Local workload IP is already present on one of our AWS ENIs.")
			continue
		}
		missingRoutes = append(missingRoutes, route)
		missingIPSummary = append(missingIPSummary, addr.Addr().String())
	}
	if len(missingIPSummary) > 0 {
		logrus.WithField("addrs", missingIPSummary).Info(
			"Found local workload IPs that should be added to AWS ENI(s).")
	}
	return missingRoutes
}

// unassignAWSIPs unassigns (releases) the given IPs in the AWS fabric.  It updates the free IP counters
// in the eniSnapshot (but it does not refresh the AWS ENI data itself).
func (m *SecondaryIfaceProvisioner) unassignAWSIPs(awsIPsToRelease set.Set, awsENIState *eniSnapshot) error {
	if awsIPsToRelease.Len() == 0 {
		return nil
	}

	// About to change AWS state, queue up a recheck.
	m.resetRecheckInterval("unassign-ips")

	ctx, cancel := m.newContext()
	defer cancel()
	ec2Client, err := m.ec2Client()
	if err != nil {
		return err
	}

	// Batch up the IPs by ENI; the AWS API lets us release multiple IPs from the same ENI in one shot.
	ipsToReleaseByENIID := map[string][]string{}
	awsIPsToRelease.Iter(func(item interface{}) error {
		addr := item.(ip.CIDR)
		eniID := awsENIState.eniIDByIP[addr]
		ipsToReleaseByENIID[eniID] = append(ipsToReleaseByENIID[eniID], addr.Addr().String())
		return nil
	})

	var finalErr error
	for eniID, ipsToRelease := range ipsToReleaseByENIID {
		eniID := eniID
		_, err := ec2Client.EC2Svc.UnassignPrivateIpAddresses(ctx, &ec2.UnassignPrivateIpAddressesInput{
			NetworkInterfaceId: &eniID,
			PrivateIpAddresses: ipsToRelease,
		})
		if err != nil {
			logrus.WithError(err).WithField("eniID", eniID).Error("Failed to release AWS IPs.")
			finalErr = fmt.Errorf("failed to release some AWS IPs: %w", err)
		}
		if finalErr == nil {
			finalErr = errResyncNeeded
		}
	}

	return finalErr
}

// releaseAWSENIs tries to unattach and release the given ENIs.  Returns errResyncNeeded if the eniSnapshot now needs
// to be refreshed.
func (m *SecondaryIfaceProvisioner) releaseAWSENIs(enisToRelease set.Set, awsENIState *eniSnapshot) error {
	if enisToRelease.Len() == 0 {
		return nil
	}
	// About to release some ENIs, queue up a check of our IPAM handle and a general AWS recheck.
	m.hostIPAMResyncNeeded = true
	m.resetRecheckInterval("release-eni")

	ctx, cancel := m.newContext()
	defer cancel()
	ec2Client, err := m.ec2Client()
	if err != nil {
		return err
	}

	// Release any ENIs we no longer want.
	finalErr := errResyncNeeded
	enisToRelease.Iter(func(item interface{}) error {
		eniID := item.(string)
		attachID := awsENIState.attachmentIDByENIID[eniID]
		_, err := ec2Client.EC2Svc.DetachNetworkInterface(ctx, &ec2.DetachNetworkInterfaceInput{
			AttachmentId: &attachID,
			Force:        boolPtr(true),
		})
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"eniID":    eniID,
				"attachID": attachID,
			}).Error("Failed to detach unneeded ENI")
			// Not setting finalErr here since the following deletion might solve the problem.
		}
		// Worth trying this even if detach fails.  Possible the failure was caused by it already
		// being detached.
		_, err = ec2Client.EC2Svc.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{
			NetworkInterfaceId: &eniID,
		})
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"eniID":    eniID,
				"attachID": attachID,
			}).Error("Failed to delete unneeded ENI; triggering retry/backoff.")
			finalErr = err // Trigger retry/backoff.
			m.orphanENIResyncNeeded = true
		}
		return nil
	})
	return finalErr
}

// calculateBestSubnet Tries to calculate a single "best" AWS subnet for this host.  When we're configured correctly
// there should only be one subnet in use on this host but we try to pick a sensible one if the IP pools have conflicting
// information.
func (m *SecondaryIfaceProvisioner) calculateBestSubnet(awsENIState *eniSnapshot, localSubnetsByID map[string]ec2types.Subnet) string {
	// Match AWS subnets against our IP pools.
	localIPPoolSubnetIDs := set.New()
	for subnetID := range m.ds.PoolIDsBySubnetID {
		if _, ok := localSubnetsByID[subnetID]; ok {
			localIPPoolSubnetIDs.Add(subnetID)
		}
	}
	logrus.WithField("subnets", localIPPoolSubnetIDs).Debug("AWS Subnets with associated Calico IP pool.")

	// If the IP pools only name one then that is preferred.  If there's more than one in the IP pools but we've
	// already got a local ENI, that one is preferred.  If there's a tie, pick the one with the most routes.
	subnetScores := map[string]int{}
	localIPPoolSubnetIDs.Iter(func(item interface{}) error {
		subnetID := item.(string)
		subnetScores[subnetID] += 1000000
		return nil
	})
	for subnet, eniIDs := range awsENIState.eniIDsBySubnet {
		subnetScores[subnet] += 10000 * len(eniIDs)
	}
	for _, r := range m.ds.LocalAWSAddrsByDst {
		subnetScores[r.AWSSubnetId] += 1
	}
	var bestSubnet string
	var bestScore int
	for subnet, score := range subnetScores {
		if score > bestScore ||
			score == bestScore && subnet > bestSubnet {
			bestSubnet = subnet
			bestScore = score
		}
	}
	return bestSubnet
}

// subnetCIDRAndGW extracts the subnet's CIDR and gateway address from the given AWS subnet.
func (m *SecondaryIfaceProvisioner) subnetCIDRAndGW(subnet ec2types.Subnet) (ip.CIDR, ip.Addr, error) {
	subnetID := safeReadString(subnet.SubnetId)
	if subnet.CidrBlock == nil {
		return nil, nil, fmt.Errorf("our subnet missing its CIDR id=%s", subnetID) // AWS bug?
	}
	ourCIDR, err := ip.ParseCIDROrIP(*subnet.CidrBlock)
	if err != nil {
		return nil, nil, fmt.Errorf("our subnet had malformed CIDR %q: %w", *subnet.CidrBlock, err)
	}
	// The AWS Subnet gateway is always the ".1" address in the subnet.
	addr := ourCIDR.Addr().Add(1)
	return ourCIDR, addr, nil
}

// filterRoutesByAWSSubnet returns the subset of the given routes that belong to the given AWS subnet.
func filterRoutesByAWSSubnet(missingRoutes []AddrInfo, bestSubnet string) []AddrInfo {
	var filteredRoutes []AddrInfo
	for _, r := range missingRoutes {
		if r.AWSSubnetId != bestSubnet {
			logrus.WithFields(logrus.Fields{
				"route":        r,
				"activeSubnet": bestSubnet,
			}).Warn("Cannot program route into AWS fabric; only one AWS subnet is supported per node. All " +
				"workloads on the same node that use AWS networking (typically egress gateways) must use the " +
				"same AWS subnet.")
			continue
		}
		filteredRoutes = append(filteredRoutes, r)
	}
	return filteredRoutes
}

// attachOrphanENIs looks for any unattached Calico-created ENIs that should be attached to this host and tries
// to attach them.
func (m *SecondaryIfaceProvisioner) attachOrphanENIs(resyncState *eniResyncState, bestSubnetID string) error {
	ctx, cancel := m.newContext()
	defer cancel()
	ec2Client, err := m.ec2Client()
	if err != nil {
		return err
	}

	dio, err := ec2Client.EC2Svc.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{
			{
				// We label all our ENIs at creation time with the instance they belong to.
				Name:   stringPtr("tag:" + CalicoNetworkInterfaceTagOwningInstance),
				Values: []string{ec2Client.InstanceID},
			},
			{
				Name:   stringPtr("status"),
				Values: []string{"available" /* Not attached to the instance */},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to list unattached ENIs that belong to this node: %w", err)
	}

	attachedOrphan := false
	numLeakedENIs := 0
	var lastLeakErr error
	for _, eni := range dio.NetworkInterfaces {
		// About to change AWS state, queue up a recheck.
		m.resetRecheckInterval("attach-orphan-eni")

		// Find next free device index.
		devIdx := resyncState.FindFreeDeviceIdx()

		subnetID := safeReadString(eni.SubnetId)
		eniID := safeReadString(eni.NetworkInterfaceId)
		logCtx := logrus.WithFields(logrus.Fields{
			"eniID":        eniID,
			"activeSubnet": bestSubnetID,
			"eniSubnet":    subnetID,
		})
		if subnetID != bestSubnetID || int(devIdx) >= m.networkCapabilities.MaxENIsForCard(0) {
			if subnetID != bestSubnetID {
				logCtx.Info("Found unattached ENI belonging to this node but not from our active subnet. " +
					"Deleting.")
			} else {
				logCtx.Info("Found unattached ENI belonging to this node but node doesn't have enough " +
					"capacity to attach it. Deleting.")
			}
			_, err = ec2Client.EC2Svc.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{
				NetworkInterfaceId: eni.NetworkInterfaceId,
			})
			if err != nil {
				logCtx.WithError(err).Error("Failed to delete unattached ENI")
				// Could bail out here but having an orphaned ENI doesn't stop us from getting _our_ state right.
				numLeakedENIs++
				lastLeakErr = err
			}
			continue
		}

		logCtx.Info("Found unattached ENI that belongs to this node; trying to attach it.")
		attOut, err := ec2Client.EC2Svc.AttachNetworkInterface(ctx, &ec2.AttachNetworkInterfaceInput{
			DeviceIndex:        &devIdx,
			InstanceId:         &ec2Client.InstanceID,
			NetworkInterfaceId: eni.NetworkInterfaceId,
			// For now, only support the first network card.  There's only one type of AWS instance with >1
			// NetworkCard.
			NetworkCardIndex: int32Ptr(0),
		})
		if err != nil {
			logCtx.WithError(err).Error("Failed to attach interface to host, trying to delete it.")
			_, err = ec2Client.EC2Svc.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{
				NetworkInterfaceId: eni.NetworkInterfaceId,
			})
			if err != nil {
				logCtx.WithError(err).Error("Failed to delete unattached ENI (after failing to attach it)")
				numLeakedENIs++
				lastLeakErr = err
			}
			continue
		}
		resyncState.ClaimDeviceIdx(devIdx) // Mark the device index as used.
		logCtx.WithFields(logrus.Fields{
			"attachmentID": safeReadString(attOut.AttachmentId),
			"networkCard":  safeReadInt32(attOut.NetworkCardIndex),
		}).Info("Attached orphaned AWS ENI to this host.")
		attachedOrphan = true
	}
	// Set some limit on how many ENIs we'll leak before we say "no more".
	if numLeakedENIs > m.networkCapabilities.MaxNetworkInterfaces {
		return fmt.Errorf("detected multiple ENIs that belong to this node but cannot be attached or deleted, "+
			"backing off to prevent further leaks: %w", lastLeakErr)
	}
	if attachedOrphan {
		return errResyncNeeded
	}
	return nil
}

// freeUnusedHostCalicoIPs finds any IPs assign to this host for a secondary ENI that are not actually in use
// and then frees those IPs.
func (m *SecondaryIfaceProvisioner) freeUnusedHostCalicoIPs(awsENIState *eniSnapshot) error {
	ctx, cancel := m.newContext()
	defer cancel()
	ourIPs, err := m.ipamClient.IPsByHandle(ctx, m.ipamHandle())
	if err != nil {
		if _, ok := err.(calierrors.ErrorResourceDoesNotExist); ok {
			logrus.Debug("No host IPs in IPAM.  Nothing to free.")
			return nil
		}
		return fmt.Errorf("failed to look up our existing IPs: %w", err)
	}

	var finalErr error
	for _, addr := range ourIPs {
		cidr := ip.CIDRFromNetIP(addr.IP)
		if _, ok := awsENIState.eniIDByPrimaryIP[cidr]; !ok {
			// IP is not assigned to any of our local ENIs and, if we got this far, we've already attached
			// any orphaned ENIs or deleted them.  Clean up the IP.
			logrus.WithField("addr", addr).Info(
				"Found IP assigned to this node in IPAM but not in use for an AWS ENI, freeing it.")
			_, err := m.ipamClient.ReleaseIPs(ctx, []calinet.IP{addr})
			if err != nil {
				logrus.WithError(err).WithField("ip", addr).Error(
					"Failed to free host IP that we no longer need.")
				finalErr = err
			}
		}
	}

	return finalErr
}

// calculateNumNewENIsNeeded does the maths to figure out how many ENIs we need to add given the number of
// IPs we need and the spare capacity of existing ENIs.
func (m *SecondaryIfaceProvisioner) calculateNumNewENIsNeeded(awsENIState *eniSnapshot, bestSubnetID string) (int, error) {
	totalIPs := m.ds.LocalRouteDestsBySubnetID[bestSubnetID].Len()
	if m.networkCapabilities.MaxIPv4PerInterface <= 1 {
		logrus.Error("Instance type doesn't support secondary IPs")
		return 0, fmt.Errorf("instance type doesn't support secondary IPs")
	}
	secondaryIPsPerIface := m.networkCapabilities.MaxIPv4PerInterface - 1
	totalENIsNeeded := (totalIPs + secondaryIPsPerIface - 1) / secondaryIPsPerIface
	enisAlreadyAllocated := len(awsENIState.eniIDsBySubnet[bestSubnetID])
	numENIsNeeded := totalENIsNeeded - enisAlreadyAllocated

	return numENIsNeeded, nil
}

// allocateCalicoHostIPs allocates the given number of IPPoolAllowedUseHostSecondary IPs to this host in Calico IPAM.
func (m *SecondaryIfaceProvisioner) allocateCalicoHostIPs(numENIsNeeded int, subnetID string) (*ipam.IPAMAssignments, error) {
	ipamCtx, ipamCancel := m.newContext()

	v4addrs, _, err := m.ipamClient.AutoAssign(ipamCtx, m.ipamAssignArgs(numENIsNeeded, subnetID))
	ipamCancel()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate primary IP for secondary interface: %w", err)
	}
	logrus.WithField("ips", v4addrs.IPs).Info("Allocated primary IPs for secondary interfaces")
	if len(v4addrs.IPs) < numENIsNeeded {
		logrus.WithFields(logrus.Fields{
			"needed":    numENIsNeeded,
			"allocated": len(v4addrs.IPs),
			"reasons":   v4addrs.Msgs, // Contains messages like "pool X is full"
		}).Warn("Wasn't able to allocate enough ENI primary IPs. IP pool may be full.")
	}
	return v4addrs, nil
}

// ipamAssignArgs is mainly broken out for testing.
func (m *SecondaryIfaceProvisioner) ipamAssignArgs(numENIsNeeded int, subnetID string) ipam.AutoAssignArgs {
	return ipam.AutoAssignArgs{
		Num4:     numENIsNeeded,
		HandleID: stringPtr(m.ipamHandle()),
		Attrs: map[string]string{
			ipam.AttributeType: ipam.AttributeTypeAWSSecondary,
			ipam.AttributeNode: m.nodeName,
		},
		Hostname:    m.nodeName,
		IntendedUse: v3.IPPoolAllowedUseHostSecondary,
		// Make sure we get an IP from the right subnet.
		AWSSubnetIDs: []string{subnetID},
	}
}

// createAWSENIs creates one AWS secondary ENI in the given subnet for each given IP address and attempts to
// attach the newly created ENI to this host.
func (m *SecondaryIfaceProvisioner) createAWSENIs(awsENIState *eniSnapshot, resyncState *eniResyncState, subnetID string, v4addrs []calinet.IPNet) error {
	if len(v4addrs) == 0 {
		return nil
	}

	// About to change AWS state, queue up a recheck.
	m.resetRecheckInterval("create-eni")

	ctx, cancel := m.newContext()
	defer cancel()
	ec2Client, err := m.ec2Client()
	if err != nil {
		return err
	}

	// Figure out the security groups of our primary ENI, we'll copy these to the new interfaces that we create.
	secGroups := awsENIState.PrimaryENISecurityGroups()

	// Create the new ENIs for the IPs we were able to get.
	var finalErr error
	for _, addr := range v4addrs {
		ipStr := addr.IP.String()
		cno, err := ec2Client.EC2Svc.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
			SubnetId:         &subnetID,
			Description:      stringPtr(fmt.Sprintf("Calico secondary ENI for instance %s", ec2Client.InstanceID)),
			Groups:           secGroups,
			Ipv6AddressCount: int32Ptr(0),
			PrivateIpAddress: stringPtr(ipStr),
			TagSpecifications: []ec2types.TagSpecification{
				{
					ResourceType: ec2types.ResourceTypeNetworkInterface,
					Tags: []ec2types.Tag{
						{
							Key:   stringPtr(CalicoNetworkInterfaceTagUse),
							Value: stringPtr(CalicoNetworkInterfaceUseSecondary),
						},
						{
							Key:   stringPtr(CalicoNetworkInterfaceTagOwningInstance),
							Value: stringPtr(ec2Client.InstanceID),
						},
					},
				},
			},
		})
		if err != nil {
			logrus.WithError(err).Error("Failed to create interface.")
			finalErr = fmt.Errorf("failed to create ENI: %w", err)
			continue // Carry on and try the other interfaces before we give up.
		}

		// Find a free device index.
		devIdx := resyncState.FindFreeDeviceIdx()
		resyncState.ClaimDeviceIdx(devIdx)
		attOut, err := ec2Client.EC2Svc.AttachNetworkInterface(ctx, &ec2.AttachNetworkInterfaceInput{
			DeviceIndex:        &devIdx,
			InstanceId:         &ec2Client.InstanceID,
			NetworkInterfaceId: cno.NetworkInterface.NetworkInterfaceId,
			// For now, only support the first network card.  There's only one type of AWS instance with >1
			// NetworkCard.
			NetworkCardIndex: int32Ptr(0),
		})
		if err != nil {
			logrus.WithError(err).Error("Failed to attach interface to host.")
			finalErr = fmt.Errorf("failed to attach ENI to instance: %w", err)
			continue // Carry on and try the other interfaces before we give up.
		}
		logrus.WithFields(logrus.Fields{
			"attachmentID": safeReadString(attOut.AttachmentId),
			"networkCard":  safeReadInt32(attOut.NetworkCardIndex),
		}).Info("Attached ENI.")

		_, err = ec2Client.EC2Svc.ModifyNetworkInterfaceAttribute(ctx, &ec2.ModifyNetworkInterfaceAttributeInput{
			NetworkInterfaceId: cno.NetworkInterface.NetworkInterfaceId,
			Attachment: &ec2types.NetworkInterfaceAttachmentChanges{
				AttachmentId:        attOut.AttachmentId,
				DeleteOnTermination: boolPtr(true),
			},
		})
		if err != nil {
			logrus.WithError(err).Error("Failed to set interface delete-on-termination flag")
			finalErr = fmt.Errorf("failed to set interface delete-on-termination flag: %w", err)
			continue // Carry on and try the other interfaces before we give up.
		}

		// Calculate the free IPs from the output. Once we add an idempotency token, it'll be possible to have
		// >1 IP in place already.
		resyncState.freeIPv4CapacityByENIID[*cno.NetworkInterface.NetworkInterfaceId] =
			m.networkCapabilities.MaxIPv4PerInterface - len(cno.NetworkInterface.PrivateIpAddresses)
	}

	if finalErr != nil {
		logrus.Info("Some AWS ENI operations failed; queueing a scan for orphaned ENIs/IPAM resources.")
		m.hostIPAMResyncNeeded = true
		m.orphanENIResyncNeeded = true
	}

	return finalErr
}

func (m *SecondaryIfaceProvisioner) assignSecondaryIPsToENIs(resyncState *eniResyncState, filteredRoutes []AddrInfo) error {
	if len(filteredRoutes) == 0 {
		return nil
	}

	// About to change AWS state, queue up a recheck.
	m.resetRecheckInterval("assign-ips")

	ctx, cancel := m.newContext()
	defer cancel()
	ec2Client, err := m.ec2Client()
	if err != nil {
		return err
	}

	attemptedSomeAssignments := false
	remainingRoutes := filteredRoutes
	var fatalErr error
	for eniID, freeIPs := range resyncState.freeIPv4CapacityByENIID {
		if len(remainingRoutes) == 0 {
			// We're done.
			break
		}
		if freeIPs == 0 {
			continue
		}
		routesToAdd := remainingRoutes
		if len(routesToAdd) > freeIPs {
			routesToAdd = routesToAdd[:freeIPs]
		}
		remainingRoutes = remainingRoutes[len(routesToAdd):]

		var ipAddrs []string
		for _, r := range routesToAdd {
			ipAddrs = append(ipAddrs, trimPrefixLen(r.Dst))
		}

		logrus.WithFields(logrus.Fields{"eni": eniID, "addrs": ipAddrs})
		attemptedSomeAssignments = true
		_, err := ec2Client.EC2Svc.AssignPrivateIpAddresses(ctx, &ec2.AssignPrivateIpAddressesInput{
			NetworkInterfaceId: &eniID,
			AllowReassignment:  boolPtr(true),
			PrivateIpAddresses: ipAddrs,
		})
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"eniID": eniID,
				"addrs": ipAddrs,
			}).Error("Failed to assign IPs to my ENI.")
			fatalErr = fmt.Errorf("failed to assign workload IPs to secondary ENI: %w", err)
			continue // Carry on trying to assign more IPs.
		}
		logrus.WithFields(logrus.Fields{
			"eniID": eniID,
			"addrs": strings.Join(ipAddrs, ","),
		}).Info("Assigned IPs to secondary ENI.")
	}

	if len(remainingRoutes) > 0 {
		logrus.Warn("Failed to assign all Calico IPs to local ENIs.  Insufficient secondary IP capacity on the available ENIs.")
	}

	if fatalErr != nil {
		return fatalErr
	}

	if attemptedSomeAssignments {
		return errResyncNeeded
	}

	return nil
}

func (m *SecondaryIfaceProvisioner) ipamHandle() string {
	// Using the node name here for consistency with tunnel IPs.
	return fmt.Sprintf("aws-secondary-ifaces-%s", m.nodeName)
}

func (m *SecondaryIfaceProvisioner) newContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), m.timeout)
}

func (m *SecondaryIfaceProvisioner) ec2Client() (*EC2Client, error) {
	if m.cachedEC2Client != nil {
		return m.cachedEC2Client, nil
	}

	ctx, cancel := m.newContext() // Context only for creation of the client, it doesn't get stored.
	defer cancel()
	c, err := m.newEC2Client(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create an AWS client: %w", err)
	}
	m.cachedEC2Client = c
	return m.cachedEC2Client, nil
}

func (m *SecondaryIfaceProvisioner) calculateMaxCalicoSecondaryIPs(snapshot *eniSnapshot) int {
	caps := m.networkCapabilities
	maxSecondaryIPsPerENI := caps.MaxIPv4PerInterface - 1
	maxCalicoENIs := caps.MaxNetworkInterfaces - len(snapshot.nonCalicoOwnedENIsByID)
	maxCapacity := maxCalicoENIs * maxSecondaryIPsPerENI
	return maxCapacity
}

func (m *SecondaryIfaceProvisioner) ensureCalicoENIsDelOnTerminate(snapshot *eniSnapshot) error {
	ctx, cancel := m.newContext()
	defer cancel()
	ec2Client, err := m.ec2Client()
	if err != nil {
		return err
	}

	var finalErr error
	for eniID, eni := range snapshot.calicoOwnedENIsByID {
		if eni.Attachment == nil {
			logrus.WithField("eniID", eniID).Warn("ENI has no attachment specified (but it should be attached to this node).")
			finalErr = fmt.Errorf("ENI %s has no attachment (but it should be attached to this node)", eniID)
			continue // Try to deal with the other ENIs.
		}
		if eni.Attachment.DeleteOnTermination == nil || !*eni.Attachment.DeleteOnTermination {
			logrus.WithField("eniID", eniID).Info(
				"Calico secondary ENI doesn't have delete-on-termination flag enabled; enabling it...")
			// About to change AWS state, queue up a recheck.
			m.resetRecheckInterval("set-eni-delete-on-term")
			_, err = ec2Client.EC2Svc.ModifyNetworkInterfaceAttribute(ctx, &ec2.ModifyNetworkInterfaceAttributeInput{
				NetworkInterfaceId: eni.NetworkInterfaceId,
				Attachment: &ec2types.NetworkInterfaceAttachmentChanges{
					AttachmentId:        eni.Attachment.AttachmentId,
					DeleteOnTermination: boolPtr(true),
				},
			})
			if err != nil {
				logrus.WithError(err).Error("Failed to set interface delete-on-termination flag")
				finalErr = fmt.Errorf("failed to set interface delete-on-termination flag: %w", err)
				continue // Carry on and try the other interfaces before we give up.
			}
		}
	}
	return finalErr
}

func (m *SecondaryIfaceProvisioner) resetRecheckInterval(operation string) {
	m.opRecorder.RecordOperation(operation)
	m.recheckIntervalResetNeeded = true
}

func trimPrefixLen(cidr string) string {
	parts := strings.Split(cidr, "/")
	return parts[0]
}

func safeReadInt32(iptr *int32) string {
	if iptr == nil {
		return "<nil>"
	}
	return fmt.Sprint(*iptr)
}
func safeReadString(sptr *string) string {
	if sptr == nil {
		return "<nil>"
	}
	return *sptr
}

func boolPtr(b bool) *bool {
	return &b
}
func int32Ptr(i int32) *int32 {
	return &i
}

func stringPtr(s string) *string {
	return &s
}
