// Copyright (c) 2016-2019 Tigera, Inc. All rights reserved.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package calc

import (
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/felix/config"
	"github.com/projectcalico/felix/dispatcher"
	"github.com/projectcalico/felix/ip"
	"github.com/projectcalico/felix/labelindex"
	"github.com/projectcalico/felix/proto"
	"github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/net"
)

var (
	gaugeNumActiveSelectors = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "felix_active_local_selectors",
		Help: "Number of active selectors on this host.",
	})
	gaugeNumActiveTags = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "felix_active_local_tags",
		Help: "Number of active tags on this host.",
	})
)

func init() {
	prometheus.MustRegister(gaugeNumActiveTags)
	prometheus.MustRegister(gaugeNumActiveSelectors)
}

type ipSetUpdateCallbacks interface {
	OnIPSetAdded(setID string, ipSetType proto.IPSetUpdate_IPSetType)
	OnIPSetMemberAdded(setID string, ip labelindex.IPSetMember)
	OnIPSetMemberRemoved(setID string, ip labelindex.IPSetMember)
	OnIPSetRemoved(setID string)
}

type rulesUpdateCallbacks interface {
	OnPolicyActive(model.PolicyKey, *ParsedRules)
	OnPolicyInactive(model.PolicyKey)
	OnProfileActive(model.ProfileRulesKey, *ParsedRules)
	OnProfileInactive(model.ProfileRulesKey)
}

type endpointCallbacks interface {
	OnEndpointTierUpdate(endpointKey model.Key,
		endpoint interface{},
		filteredTiers []tierInfo)
}

type configCallbacks interface {
	OnConfigUpdate(globalConfig, hostConfig map[string]string)
	OnDatastoreNotReady()
}

type passthruCallbacks interface {
	OnHostIPUpdate(hostname string, ip *net.IP)
	OnHostIPRemove(hostname string)
	OnIPPoolUpdate(model.IPPoolKey, *model.IPPool)
	OnIPPoolRemove(model.IPPoolKey)
	OnServiceAccountUpdate(*proto.ServiceAccountUpdate)
	OnServiceAccountRemove(proto.ServiceAccountID)
	OnNamespaceUpdate(*proto.NamespaceUpdate)
	OnNamespaceRemove(proto.NamespaceID)
}

type routeCallbacks interface {
	OnRouteUpdate(update *proto.RouteUpdate)
	OnRouteRemove(dst string)
}

type vxlanCallbacks interface {
	OnVTEPUpdate(update *proto.VXLANTunnelEndpointUpdate)
	OnVTEPRemove(node string)
}

type ipsecCallbacks interface {
	OnIPSecBindingAdded(b IPSecBinding)
	OnIPSecBindingRemoved(b IPSecBinding)
	OnIPSecBlacklistAdded(workloadAddr ip.Addr)
	OnIPSecBlacklistRemoved(workloadAddr ip.Addr)
	OnIPSecTunnelAdded(tunnelAddr ip.Addr)
	OnIPSecTunnelRemoved(tunnelAddr ip.Addr)
}

type PipelineCallbacks interface {
	ipSetUpdateCallbacks
	rulesUpdateCallbacks
	endpointCallbacks
	configCallbacks
	passthruCallbacks
	routeCallbacks
	vxlanCallbacks
	ipsecCallbacks
}

type endpointPolicyCache interface {
	endpointCallbacks
	ruleScanner
}

type CalcGraph struct {
	// AllUpdDispatcher is the input node to the calculation graph.
	AllUpdDispatcher      *dispatcher.Dispatcher
	activeRulesCalculator *ActiveRulesCalculator
}

func NewCalculationGraph(callbacks PipelineCallbacks, cache *LookupsCache, conf *config.Config, tiersEnabled bool) *CalcGraph {
	hostname := conf.FelixHostname
	log.Infof("Creating calculation graph, filtered to hostname %v", hostname)

	// The source of the processing graph, this dispatcher will be fed all the updates from the
	// datastore, fanning them out to the registered receivers.
	//
	//               Syncer
	//                 ||
	//                 || All updates
	//                 \/
	//             Dispatcher (all updates)
	//                / | \
	//               /  |  \  Updates filtered by type
	//              /   |   \
	//     receiver_1  ...  receiver_n
	//
	allUpdDispatcher := dispatcher.NewDispatcher()

	// Some of the receivers only need to know about local endpoints. Create a second dispatcher
	// that will filter out non-local endpoints.
	//
	//          ...
	//       Dispatcher (all updates)
	//          ... \
	//               \  All Host/Workload Endpoints
	//                \
	//              Dispatcher (local updates)
	//               <filter>
	//                / | \
	//               /  |  \  Local Host/Workload Endpoints only
	//              /   |   \
	//     receiver_1  ...  receiver_n
	//
	localEndpointDispatcher := dispatcher.NewDispatcher()
	(*localEndpointDispatcherReg)(localEndpointDispatcher).RegisterWith(allUpdDispatcher)
	localEndpointFilter := &endpointHostnameFilter{hostname: hostname}
	localEndpointFilter.RegisterWith(localEndpointDispatcher)

	// The tier filter examines tier and policy updates, potentially filtering out tiers and policies
	// associated with unlicensed tiers. When tiersEnabled is true, all policies and tiers are allowed.
	// When tiersEnabled is false, only licensed tiers are allowed, i.e. "default", "sg-remote",
	// "sg-local", and "metadata".
	tierDispatcher := dispatcher.NewDispatcher()
	(*tierDispatcherReg)(tierDispatcher).RegisterWith(allUpdDispatcher)
	tierFilter := &tierFilter{tiersEnabled}
	tierFilter.RegisterWith(tierDispatcher)

	// The active rules calculator matches local endpoints against policies and profiles to figure
	// out which policies/profiles are active on this host.  Limiting to policies that apply to
	// local endpoints significantly cuts down the number of policies that Felix has to
	// render into the dataplane.
	//           Dispatcher (all updates)
	//                /         \
	//               /           \  All Host/Workload Endpoints
	//              /             \
	//             /            Dispatcher (local updates)
	//            /                      |
	//            |                       \  Local Host/Workload
	//            |                        \ Endpoints only
	//           / \                        \
	// Profiles /   \ All Policies           \
	//         /     \                        \
	//         \      \                        \
	//          \   Dispatcher (tier updates)  |
	//           \       |                     /
	//            \      | Policies for       /
	//             \     | licensed tiers    /
	//              \    |                  /
	//              Active Rules Calculator
	//                   |
	//                   | Locally active policies/profiles
	//                  ...
	//
	activeRulesCalc := NewActiveRulesCalculator()
	activeRulesCalc.RegisterWith(localEndpointDispatcher, allUpdDispatcher, tierDispatcher)

	// The active rules calculator only figures out which rules are active, it doesn't extract
	// any information from the rules.  The rule scanner takes the output from the active rules
	// calculator and scans the individual rules for selectors, tags, and named ports.  It
	// generates events when a new selector/tag/named port starts/stops being used.
	//
	//             ...
	//     Active Rules Calculator
	//              |
	//              | Locally active policies/profiles
	//              |
	//         Rule scanner
	//          |    \
	//          |     \ Locally active tags/selectors/named ports
	//          |      \
	//          |      ...
	//          |
	//          | IP set active/inactive
	//          |
	//     <dataplane>
	//
	ruleScanner := NewRuleScanner()
	// Wire up the rule scanner's inputs.
	activeRulesCalc.RuleScanner = ruleScanner
	// Send IP set added/removed events to the dataplane.  We'll hook up the other outputs
	// below.
	ruleScanner.RulesUpdateCallbacks = callbacks

	// The rule scanner only goes as far as figuring out which tags/selectors/named ports are
	// active. Next we need to figure out which endpoints (and hence which IP addresses/ports) are
	// in each tag/selector/named port. The IP set member index calculates the set of IPs and named
	// ports that should be in each IP set.  To do that, it matches the active selectors/tags/named
	// ports extracted by the rule scanner against all the endpoints.
	//
	//        ...
	//     Dispatcher (all updates)
	//      |
	//      | All endpoints
	//      |
	//      |       ...
	//      |    Rule scanner
	//      |     |       \
	//      |    ...       \ Locally active tags/selectors/named ports
	//       \              |
	//        \_____        |
	//              \       |
	//            IP set member index
	//                   |
	//                   | IP set member added/removed
	//                   |
	//               <dataplane>
	//
	ipsetMemberIndex := labelindex.NewSelectorAndNamedPortIndex()
	// Wire up the inputs to the IP set member index.
	ipsetMemberIndex.RegisterWith(allUpdDispatcher)
	ruleScanner.OnIPSetActive = func(ipSet *IPSetData) {
		log.WithField("ipSet", ipSet).Info("IPSet now active")
		callbacks.OnIPSetAdded(ipSet.UniqueID(), ipSet.DataplaneProtocolType())
		if ipSet.Selector != nil {
			if !ipSet.isDomainSet {
				defer gaugeNumActiveSelectors.Inc()
			}
			ipsetMemberIndex.UpdateIPSet(ipSet.UniqueID(), ipSet.Selector, ipSet.NamedPortProtocol, ipSet.NamedPort)
		}
	}
	ruleScanner.OnIPSetInactive = func(ipSet *IPSetData) {
		log.WithField("ipSet", ipSet).Info("IPSet now inactive")
		if ipSet.Selector != nil {
			if !ipSet.isDomainSet {
				defer gaugeNumActiveSelectors.Dec()
			}
			ipsetMemberIndex.DeleteIPSet(ipSet.UniqueID())
		}
		callbacks.OnIPSetRemoved(ipSet.UniqueID())
	}
	// Send the IP set member index's outputs to the dataplane.
	ipsetMemberIndex.OnMemberAdded = func(ipSetID string, member labelindex.IPSetMember) {
		if log.GetLevel() >= log.DebugLevel {
			log.WithFields(log.Fields{
				"ipSetID": ipSetID,
				"member":  member,
			}).Debug("Member added to IP set.")
		}
		callbacks.OnIPSetMemberAdded(ipSetID, member)
	}
	ipsetMemberIndex.OnMemberRemoved = func(ipSetID string, member labelindex.IPSetMember) {
		if log.GetLevel() >= log.DebugLevel {
			log.WithFields(log.Fields{
				"ipSetID": ipSetID,
				"member":  member,
			}).Debug("Member removed from IP set.")
		}
		callbacks.OnIPSetMemberRemoved(ipSetID, member)
	}
	ruleScanner.OnIPSetMemberAdded = ipsetMemberIndex.OnMemberAdded

	// The endpoint policy resolver marries up the active policies with local endpoints and
	// calculates the complete, ordered set of policies that apply to each endpoint.
	//
	//        ...
	//     Dispatcher (all updates)
	//      |
	//     Tier Dispatcher
	//      |
	//      | All policies (with licensed tiers)
	//      |
	//      |       ...
	//       \   Active rules calculator
	//        \       \
	//         \       \
	//          \       | Policy X matches endpoint Y
	//           \      | Policy Z matches endpoint Y
	//            \     |
	//           Policy resolver
	//                  |
	//                  | Endpoint Y has policies [Z, X] in that order
	//                  |
	//             <dataplane>
	//
	polResolver := NewPolicyResolver()
	// Hook up the inputs to the policy resolver.
	activeRulesCalc.PolicyMatchListener = polResolver
	polResolver.RegisterWith(allUpdDispatcher, localEndpointDispatcher, tierDispatcher)
	// And hook its output to the callbacks.
	polResolver.RegisterCallback(callbacks)

	// Register for host IP updates.
	//
	//        ...
	//     Dispatcher (all updates)
	//         |
	//         | host IPs
	//         |
	//       passthru
	//         |
	//         |
	//         |
	//      <dataplane>
	//
	hostIPPassthru := NewDataplanePassthru(callbacks)
	hostIPPassthru.RegisterWith(allUpdDispatcher)

	// Calculate VXLAN routes.
	//        ...
	//     Dispatcher (all updates)
	//         |
	//         | host IPs, host config, IP pools, IPAM blocks
	//         |
	//       vxlan resolver
	//         |
	//         | VTEPs, routes
	//         |
	//      <dataplane>
	//
	if conf.VXLANEnabled {
		vxlanResolver := NewVXLANResolver(hostname, callbacks)
		vxlanResolver.RegisterWith(allUpdDispatcher)
	}

	// Register for config updates.
	//
	//        ...
	//     Dispatcher (all updates)
	//         |
	//         | separate config updates foo=bar, baz=biff
	//         |
	//       config batcher
	//         |
	//         | combined config {foo=bar, bax=biff}
	//         |
	//      <dataplane>
	//
	configBatcher := NewConfigBatcher(hostname, callbacks)
	configBatcher.RegisterWith(allUpdDispatcher)

	// The profile decoder identifies objects with special dataplane significance which have
	// been encoded as profiles by libcalico-go. At present this includes Kubernetes Service
	// Accounts and Kubernetes Namespaces.
	//        ...
	//     Dispatcher (all updates)
	//         |
	//         | Profiles
	//         |
	//       profile decoder
	//         |
	//         |
	//         |
	//      <dataplane>
	//
	profileDecoder := NewProfileDecoder(callbacks)
	profileDecoder.RegisterWith(allUpdDispatcher)

	// The remote endpoint reverse lookup receiver only need to know about non-local endpoints.
	// Create another dispatcher that will filter out non-local endpoints.
	//
	//          ...
	//       Dispatcher (all updates)
	//         / ...
	//        / All Host/Workload Endpoints
	//       /
	//   Dispatcher (remote updates)
	//     <filter>
	remoteEndpointDispatcher := dispatcher.NewDispatcher()
	(*remoteEndpointDispatcherReg)(remoteEndpointDispatcher).RegisterWith(allUpdDispatcher)
	remoteEndpointFilter := &remoteEndpointFilter{hostname: hostname}
	remoteEndpointFilter.RegisterWith(remoteEndpointDispatcher)

	if cache != nil {

		// The lookup cache, caches endpoint and policy information.
		//        ...
		//     Dispatcher (remote updates)
		//         |
		//         | Workload and host endpoints
		//         |
		//       lookup cache
		//
		cache.epCache.RegisterWith(remoteEndpointDispatcher)

		// The lookup cache, caches policy information for prefix lookups. Hook into the
		// ActiveRulesCalculator to receive local active policy/profile information.
		activeRulesCalc.PolicyLookupCache = cache.polCache

		// The lookup cache, also provides local endpoint lookups and corresponding tier information.
		// Hook into the PolicyResolver to receive this information.
		polResolver.RegisterCallback(cache.epCache)

		// The lookup cache also caches networkset information for flow log reporting.
		cache.nsCache.RegisterWith(allUpdDispatcher)
	} else {
		log.Debug("lookup cache is nil on windows platform")
	}

	return &CalcGraph{
		AllUpdDispatcher:      allUpdDispatcher,
		activeRulesCalculator: activeRulesCalc,
	}
}

func (c *CalcGraph) EnableIPSec(callbacks ipsecCallbacks) {
	// The IPSecBindingCalculator calculates the bindings between IPsec tunnels and workload IPs.
	ipSecBindingCalc := NewIPSecBindingCalculator()
	ipSecBindingCalc.RegisterWith(c.AllUpdDispatcher)
	ipSecBindingCalc.OnTunnelAdded = callbacks.OnIPSecTunnelAdded
	ipSecBindingCalc.OnTunnelRemoved = callbacks.OnIPSecTunnelRemoved
	ipSecBindingCalc.OnBindingAdded = callbacks.OnIPSecBindingAdded
	ipSecBindingCalc.OnBindingRemoved = callbacks.OnIPSecBindingRemoved
	ipSecBindingCalc.OnBlacklistAdded = callbacks.OnIPSecBlacklistAdded
	ipSecBindingCalc.OnBlacklistRemoved = callbacks.OnIPSecBlacklistRemoved
}

type localEndpointDispatcherReg dispatcher.Dispatcher

func (l *localEndpointDispatcherReg) RegisterWith(disp *dispatcher.Dispatcher) {
	led := (*dispatcher.Dispatcher)(l)
	disp.Register(model.WorkloadEndpointKey{}, led.OnUpdate)
	disp.Register(model.HostEndpointKey{}, led.OnUpdate)
	disp.RegisterStatusHandler(led.OnDatamodelStatus)
}

// endpointHostnameFilter provides an UpdateHandler that filters out endpoints
// that are not on the given host.
type endpointHostnameFilter struct {
	hostname string
}

func (f *endpointHostnameFilter) RegisterWith(localEndpointDisp *dispatcher.Dispatcher) {
	localEndpointDisp.Register(model.WorkloadEndpointKey{}, f.OnUpdate)
	localEndpointDisp.Register(model.HostEndpointKey{}, f.OnUpdate)
}

func (f *endpointHostnameFilter) OnUpdate(update api.Update) (filterOut bool) {
	switch key := update.Key.(type) {
	case model.WorkloadEndpointKey:
		if key.Hostname != f.hostname {
			filterOut = true
		}
	case model.HostEndpointKey:
		if key.Hostname != f.hostname {
			filterOut = true
		}
	}
	if !filterOut {
		// To keep log spam down, log only for local endpoints.
		if update.Value == nil {
			log.WithField("id", update.Key).Info("Local endpoint deleted")
		} else {
			log.WithField("id", update.Key).Info("Local endpoint updated")
		}
	}
	return
}

type remoteEndpointDispatcherReg dispatcher.Dispatcher

func (l *remoteEndpointDispatcherReg) RegisterWith(disp *dispatcher.Dispatcher) {
	red := (*dispatcher.Dispatcher)(l)
	disp.Register(model.WorkloadEndpointKey{}, red.OnUpdate)
	disp.Register(model.HostEndpointKey{}, red.OnUpdate)
	disp.RegisterStatusHandler(red.OnDatamodelStatus)
}

// remoteEndpointFilter provides an UpdateHandler that filters out endpoints
// that are on the given host.
type remoteEndpointFilter struct {
	hostname string
}

func (f *remoteEndpointFilter) RegisterWith(remoteEndpointDisp *dispatcher.Dispatcher) {
	remoteEndpointDisp.Register(model.WorkloadEndpointKey{}, f.OnUpdate)
	remoteEndpointDisp.Register(model.HostEndpointKey{}, f.OnUpdate)
}

func (f *remoteEndpointFilter) OnUpdate(update api.Update) (filterOut bool) {
	switch key := update.Key.(type) {
	case model.WorkloadEndpointKey:
		if key.Hostname == f.hostname {
			filterOut = true
		}
	case model.HostEndpointKey:
		if key.Hostname == f.hostname {
			filterOut = true
		}
	}
	if !filterOut {
		if update.Value == nil {
			log.WithField("id", update.Key).Info("Remote endpoint deleted")
		} else {
			log.WithField("id", update.Key).Info("Remote endpoint updated")
		}
	}
	return
}

// tierFilter provides an UpdateHandler that optionally filters out unlicensed tiers.
type tierDispatcherReg dispatcher.Dispatcher

func (l *tierDispatcherReg) RegisterWith(disp *dispatcher.Dispatcher) {
	td := (*dispatcher.Dispatcher)(l)
	disp.Register(model.TierKey{}, td.OnUpdate)
	disp.Register(model.PolicyKey{}, td.OnUpdate)
	disp.RegisterStatusHandler(td.OnDatamodelStatus)
}

// tierFilter provides an UpdateHandler that filters out unlicensed tiers. When tiersEnabled is true
// all tiers are considered licensed. When tiersEnabled is false, only the following tiers are considered
// licensed: "metadata", "sg-remote", "sg-local", and "default".
type tierFilter struct {
	tiersEnabled bool
}

// Filter out tiers as well as policies that are associated with unlicensed tiers
func (f *tierFilter) RegisterWith(tierDisp *dispatcher.Dispatcher) {
	tierDisp.Register(model.TierKey{}, f.OnUpdate)
	tierDisp.Register(model.PolicyKey{}, f.OnUpdate)
}

func (f *tierFilter) OnUpdate(update api.Update) (filterOut bool) {
	if f.tiersEnabled {
		return
	}

	// Tier names which are always considered "licensed", even when the license feature is disabled
	const (
		metaBlockerTier = "metadata"
		remoteTier      = "sg-remote"
		localTier       = "sg-local"
		defaultTier     = "default"
	)
	var tierName string
	switch key := update.Key.(type) {
	case model.PolicyKey:
		tierName = key.Tier
	case model.TierKey:
		tierName = key.Name
	default: // ignore any (unintentional) non-policy/tier updates
		return
	}
	if tierName == metaBlockerTier || tierName == remoteTier || tierName == localTier || tierName == defaultTier {
		return
	} else {
		filterOut = true
		log.Warn("Tier/policy deleted: ", tierName)
	}
	return
}
