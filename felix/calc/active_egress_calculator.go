// Copyright (c) 2021-2023 Tigera, Inc. All rights reserved.

package calc

import (
	"reflect"
	"strings"

	log "github.com/sirupsen/logrus"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/calico/felix/dispatcher"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/k8s/conversion"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/syncersv1/updateprocessors"
	sel "github.com/projectcalico/calico/libcalico-go/lib/selector"
)

// ActiveEgressCalculator tracks and reference counts the egress selectors that are used by active
// local endpoints. It generates an egress IP set ID for each unique egress selector. It calls the
// IP set member index (SelectorAndNamedPortIndex) to get it to calculate the egress gateway pod IPs
// for each selector; and the PolicyResolver to tell it to include the egress IP set ID on the
// WorkloadEndpoint data that is passed to the dataplane implementation.
type ActiveEgressCalculator struct {
	// "EnabledPerNamespaceOrPerPod" or "EnabledPerNamespace".
	supportLevel string

	// Active egress selectors. These are normalized to a standard format in the syncer before
	// they get to Felix, so we can safely use them as keys in the map without fear of duplicates.
	selectors map[string]*esData

	// Active local endpoints.
	endpoints map[model.WorkloadEndpointKey]*epData

	// Known profile egress data.
	profiles map[string]epEgressConfig

	// Known egress policies
	policies map[string][]egressPolicyRule

	// Callbacks.
	OnIPSetActive              func(ipSet *IPSetData)
	OnIPSetInactive            func(ipSet *IPSetData)
	OnEndpointEgressDataUpdate func(key model.WorkloadEndpointKey, egressData []EpEgressData)
}

// Combines the egress selector, maxNextHops and egress policy name.
type epEgressConfig struct {
	selector    string
	maxNextHops int
	policy      string
}

// Combines the egress ip set id and max next hops.
type EpEgressData struct {
	IpSetID     string
	MaxNextHops int
	CIDR        string
}

type egressPolicyRule struct {
	selector    string
	maxNextHops int
	cidr        string
}

// Information that we track for each active local endpoint.
type epData struct {
	// The egress data, if any, configured directly on this endpoint.
	localEpEgressData epEgressConfig

	// The egress data that this endpoint is now using - which could come from one of its
	// profiles.
	activeEpEgressData epEgressConfig

	// This endpoint's profile IDs.
	profileIDs []string

	// Active egress gateway rules for each endpoint
	activeRules []egressPolicyRule
}

// Information that we track for each active egress selector.
type esData struct {
	// Definition as IP set (including parsed selector).
	ipSet *IPSetData

	// Number of active local endpoints using this selector.
	refCount int
}

func NewActiveEgressCalculator(supportLevel string) *ActiveEgressCalculator {
	aec := &ActiveEgressCalculator{
		supportLevel: supportLevel,
		selectors:    map[string]*esData{},
		endpoints:    map[model.WorkloadEndpointKey]*epData{},
		profiles:     map[string]epEgressConfig{},
		policies:     map[string][]egressPolicyRule{},
	}
	return aec
}

func (aec *ActiveEgressCalculator) RegisterWith(localEndpointDispatcher, allUpdDispatcher *dispatcher.Dispatcher) {
	// It needs local workload endpoints
	localEndpointDispatcher.Register(model.WorkloadEndpointKey{}, aec.OnUpdate)
	// ...and profiles, and EgressPolicies.
	allUpdDispatcher.Register(model.ResourceKey{}, aec.OnUpdate)
}

func (aec *ActiveEgressCalculator) OnUpdate(update api.Update) (_ bool) {
	switch key := update.Key.(type) {
	case model.WorkloadEndpointKey:
		if update.Value != nil {
			log.Debugf("Updating AEC with endpoint %v", key)
			endpoint := update.Value.(*model.WorkloadEndpoint)
			if aec.supportLevel == "EnabledPerNamespaceOrPerPod" {
				// Endpoint-level selectors are supported.
				aec.updateEndpoint(key, endpoint.ProfileIDs, epEgressConfig{
					selector:    endpoint.EgressSelector,
					maxNextHops: endpoint.EgressMaxNextHops,
					policy:      endpoint.EgressGatewayPolicy,
				})
			} else {
				// Endpoint-level selectors are not supported.
				aec.updateEndpoint(key, endpoint.ProfileIDs, epEgressConfig{})
			}
		} else {
			log.Debugf("Deleting endpoint %v from AEC", key)
			aec.deleteEndpoint(key)
		}
	case model.ResourceKey:
		switch key.Kind {
		case v3.KindProfile:
			if update.Value != nil {
				log.Debugf("Updating AEC with profile %v", key)
				profile := update.Value.(*v3.Profile)
				aec.updateProfile(key.Name, profile.Spec.EgressGateway)
			} else {
				log.Debugf("Deleting profile %v from AEC", key)
				aec.updateProfile(key.Name, nil)
			}
		case v3.KindEgressGatewayPolicy:
			if update.Value != nil {
				log.Debugf("Updating AEC with egress gateway policy %v", key)
				egressPolicy := update.Value.(*v3.EgressGatewayPolicy)
				aec.updateEgressPolicy(key.Name, egressPolicy.Spec.Rules)
			} else {
				log.Debugf("Deleting egress gateway policy %v from AEC", key)
				aec.updateEgressPolicy(key.Name, nil)
			}
		default:
			// Ignore other kinds of v3 resource.
		}
	default:
		log.Infof("Ignoring unexpected update: %v %#v",
			reflect.TypeOf(update.Key), update)
	}

	return
}

func (aec *ActiveEgressCalculator) updateEgressPolicy(name string, rules []v3.EgressGatewayRule) {
	// Find the existing egress policy for this policy name
	oldPolicy := aec.policies[name]
	newPolicy := aec.v3ResourceToEgressRules(rules)

	if isEqualEgressPolicy(oldPolicy, newPolicy) {
		// Egress gateway policy has no changes, no need to scan the endpoints
		return
	}

	if rules != nil {
		aec.policies[name] = newPolicy
	} else {
		delete(aec.policies, name)
	}

	for key, epData := range aec.endpoints {
		oldEpEgressData := epData.activeEpEgressData
		epData.activeEpEgressData = aec.calculateEgressConfig(epData)

		// If this policy is not used in the active state or does not affect it,
		// this endpoint can safely ignore it.
		if oldEpEgressData.policy != name && epData.activeEpEgressData.policy != name {
			continue
		}

		newRules := aec.calculateEgressRules(epData.activeEpEgressData)
		aec.updateEndpointEgressData(key, epData.activeRules, newRules)
	}
}

func isEqualEgressPolicy(p1, p2 []egressPolicyRule) bool {
	if len(p1) != len(p2) {
		return false
	}
	for i, p := range p1 {
		if p != p2[i] {
			return false
		}
	}
	return true
}

func (aec *ActiveEgressCalculator) egressPolicyIsValid(name string) bool {
	if name == "" {
		return false
	}
	_, exists := aec.policies[name]
	if !exists {
		return false
	}
	return true
}

func (aec *ActiveEgressCalculator) calculateEgressConfig(epData *epData) epEgressConfig {
	var egressGatewayPolicyIsUsed bool
	// Endpoint specifies its own egress policy, so profiles aren't relevant.
	if epData.localEpEgressData.policy != "" {
		egressGatewayPolicyIsUsed = true
		if aec.egressPolicyIsValid(epData.localEpEgressData.policy) {
			return epData.localEpEgressData
		}
	}
	// Spin through profile's egress policies since they have higher priority.

	for _, p := range epData.profileIDs {
		if aec.profiles[p].policy != "" {
			egressGatewayPolicyIsUsed = true
			if aec.egressPolicyIsValid(aec.profiles[p].policy) {
				return aec.profiles[p]
			}
		}
	}

	// Egress Gateway Policy is set, but no valid egress gateway policy exists,
	// so block pod's traffic.
	if egressGatewayPolicyIsUsed {
		return epEgressConfig{
			selector: "!all()",
		}
	}

	// If no egress policy is set, then check all egress selectors.
	if epData.localEpEgressData.selector != "" {
		return epData.localEpEgressData
	}

	// If endpoint does not specify egress selector, then check profiles.
	for _, p := range epData.profileIDs {
		if aec.profiles[p].selector != "" {
			return aec.profiles[p]
		}
	}
	// Neither egress gateway policies nor selectors are set.
	return epEgressConfig{}
}

// Convert egress Selector and NamespaceSelector fields to a single selector
// expression in the same way we do for namespaced policy EntityRule selectors.
func preprocessEgressSelector(gateway *v3.EgressSpec, ns string) string {
	return updateprocessors.GetEgressGatewaySelector(
		gateway,
		strings.TrimPrefix(ns, conversion.NamespaceProfileNamePrefix),
	)
}

func (aec *ActiveEgressCalculator) updateProfile(profileID string, egwSpec *v3.EgressGatewaySpec) {
	// Find the existing selector for this profile.
	oldEpEgressData := aec.profiles[profileID]

	// Calculate the new selector
	newEpEgressData := epEgressConfig{}
	if egwSpec != nil {
		newEpEgressData = epEgressConfig{
			policy: egwSpec.Policy,
		}
		if egwSpec.Gateway != nil {
			newEpEgressData.selector = preprocessEgressSelector(egwSpec.Gateway, profileID)
			newEpEgressData.maxNextHops = egwSpec.Gateway.MaxNextHops
		}
	}

	// If the selector hasn't changed, no need to scan the endpoints.
	if newEpEgressData == oldEpEgressData {
		return
	}

	// Update profile selector map.
	if newEpEgressData.policy != "" || newEpEgressData.selector != "" {
		aec.profiles[profileID] = newEpEgressData
	} else {
		delete(aec.profiles, profileID)
	}

	// Scan endpoints to find those that use this profile and don't specify their own egress
	// selector or egress policy. We follow SelectorAndNamedPortIndex here in using more CPU and less occupancy
	// - i.e. not maintaining a reverse map of profiles to endpoints - because profile changes
	// should be rare and we are only scanning through local endpoints, which scales only with
	// single node capacity, not with overall cluster size.
	for key, epData := range aec.endpoints {
		if epData.localEpEgressData.policy != "" {
			// Endpoint specifies its own egress policy, so profiles aren't relevant.
			continue
		}
		if epData.activeEpEgressData.policy == "" && epData.activeEpEgressData.selector == "" &&
			newEpEgressData.policy == "" && newEpEgressData.selector == "" {
			// Endpoint has no egress selector nor egress policy, and this profile isn't providing one,
			// so can't possibly change the endpoint's situation.
			continue
		}

		oldEpEgressData := epData.activeEpEgressData
		epData.activeEpEgressData = aec.calculateEgressConfig(epData)

		if epData.activeEpEgressData == oldEpEgressData {
			// Nothing has changed for this endpoint.
			continue
		}

		// Push egress data change to IP set member index and policy resolver.
		aec.updateEndpointEgressData(key,
			aec.calculateEgressRules(oldEpEgressData), aec.calculateEgressRules(epData.activeEpEgressData))
	}
}

func (aec *ActiveEgressCalculator) v3ResourceToEgressRules(rules []v3.EgressGatewayRule) []egressPolicyRule {
	var out []egressPolicyRule
	for _, r := range rules {
		sourceData := egressPolicyRule{}
		if r.Destination != nil {
			sourceData.cidr = r.Destination.CIDR
		}
		if r.Gateway != nil {
			sourceData.selector = preprocessEgressSelector(r.Gateway, "")
			sourceData.maxNextHops = r.Gateway.MaxNextHops
		}
		out = append(out, sourceData)
	}
	return out
}

func (aec *ActiveEgressCalculator) calculateEgressRules(config epEgressConfig) []egressPolicyRule {
	// If egress gateway policy is set, and valid data exists, then use it.
	if aec.egressPolicyIsValid(config.policy) {
		return aec.policies[config.policy]
	}
	// Otherwise, switch to egress selectors.
	if config.selector != "" {
		rule := egressPolicyRule{
			selector:    config.selector,
			maxNextHops: config.maxNextHops,
		}
		return []egressPolicyRule{rule}
	}
	return nil
}

func (aec *ActiveEgressCalculator) policyRulesToEgressData(sourceRules []egressPolicyRule) []EpEgressData {
	var out []EpEgressData
	for _, s := range sourceRules {
		newEgressData := EpEgressData{
			CIDR:        s.cidr,
			MaxNextHops: s.maxNextHops,
		}
		if s.selector != "" {
			sel, err := sel.Parse(s.selector)
			if err != nil {
				// Should have been validated further back in the pipeline.
				log.WithField("selector", s.selector).Panic(
					"Failed to parse egress selector that should have been validated already")
			}
			newEgressData.IpSetID = aec.selectors[sel.String()].ipSet.UniqueID()
		}
		out = append(out, newEgressData)
	}
	return out
}

func (aec *ActiveEgressCalculator) updateEndpointEgressData(key model.WorkloadEndpointKey, old, new []egressPolicyRule) {
	if isEqualEgressPolicy(new, old) {
		// endpoint's egress gateway rules has not changed
		return
	}

	// Update endpoint's active egress gateway rules
	aec.endpoints[key].activeRules = new

	// Decref the old one and incref the new one.
	aec.incRefEgressRules(new)
	aec.decRefEgressRules(old)

	aec.OnEndpointEgressDataUpdate(key, aec.policyRulesToEgressData(new))
}

func (aec *ActiveEgressCalculator) updateEndpoint(key model.WorkloadEndpointKey, profileIDs []string, egressData epEgressConfig) {
	// Find or create the data for this endpoint.
	ep, exists := aec.endpoints[key]
	if !exists {
		ep = &epData{}
		aec.endpoints[key] = ep
	}

	// Note the existing active selector, which may be about to be overwritten.
	oldEpEgressData := ep.activeEpEgressData

	// Inherit an egress policy or selector from the profiles, if the endpoint itself doesn't have one.
	ep.localEpEgressData = egressData
	ep.profileIDs = profileIDs
	ep.activeEpEgressData = aec.calculateEgressConfig(ep)

	if ep.activeEpEgressData == oldEpEgressData {
		// Nothing has changed for this endpoint.
		return
	}

	// Push selector change to IP set member index and policy resolver.
	aec.updateEndpointEgressData(key,
		aec.calculateEgressRules(oldEpEgressData), aec.calculateEgressRules(ep.activeEpEgressData))
}

func (aec *ActiveEgressCalculator) deleteEndpoint(key model.WorkloadEndpointKey) {
	// Find and delete the data for this endpoint.
	ep, exists := aec.endpoints[key]
	if !exists {
		return
	}
	delete(aec.endpoints, key)

	// Decref this endpoint's selector(s).
	aec.decRefEgressRules(aec.calculateEgressRules(ep.activeEpEgressData))

	if aec.egressPolicyIsValid(ep.activeEpEgressData.policy) || ep.activeEpEgressData.selector != "" {
		// Ensure downstream components clear any egress IP set ID data for this endpoint
		// key.
		aec.OnEndpointEgressDataUpdate(key, nil)
	}
}

func (aec *ActiveEgressCalculator) incRefEgressRules(rules []egressPolicyRule) {
	for _, r := range rules {
		aec.incRefSelector(r.selector)
	}
}

func (aec *ActiveEgressCalculator) decRefEgressRules(rules []egressPolicyRule) {
	for _, r := range rules {
		aec.decRefSelector(r.selector)
	}
}

func (aec *ActiveEgressCalculator) incRefSelector(selector string) {
	if selector == "" {
		return
	}
	sel, err := sel.Parse(selector)
	if err != nil {
		// Should have been validated further back in the pipeline.
		log.WithField("selector", selector).Panic(
			"Failed to parse egress selector that should have been validated already")
	}
	selData, exists := aec.selectors[sel.String()]
	if !exists {
		log.Debugf("Selector: %v", selector)
		selData = &esData{ipSet: &IPSetData{
			Selector:         sel,
			IsEgressSelector: true,
		}}
		aec.selectors[sel.String()] = selData
		aec.OnIPSetActive(selData.ipSet)
	}
	selData.refCount += 1
}

func (aec *ActiveEgressCalculator) decRefSelector(selector string) {
	if selector == "" {
		return
	}
	sel, err := sel.Parse(selector)
	if err != nil {
		// Should have been validated further back in the pipeline.
		log.WithField("selector", selector).Panic(
			"Failed to parse egress selector that should have been validated already")
	}
	esData, exists := aec.selectors[sel.String()]
	if !exists || esData.refCount <= 0 {
		log.Panicf("Decref for unknown egress selector '%v'", selector)
	}
	esData.refCount -= 1
	if esData.refCount == 0 {
		aec.OnIPSetInactive(esData.ipSet)
		delete(aec.selectors, sel.String())
	}
}
