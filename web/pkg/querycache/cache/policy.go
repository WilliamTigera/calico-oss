// Copyright (c) 2018 Tigera, Inc. All rights reserved.
package cache

import (
	log "github.com/sirupsen/logrus"

	"strings"

	"github.com/projectcalico/felix/calc"
	"github.com/projectcalico/libcalico-go/lib/apis/v3"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/set"
	"github.com/tigera/calicoq/web/pkg/querycache/api"
	"github.com/tigera/calicoq/web/pkg/querycache/dispatcherv1v3"
	"github.com/tigera/calicoq/web/pkg/querycache/labelhandler"
)

type PoliciesCache interface {
	TotalPolicies() api.PolicyCounts
	UnmatchedPolicies() api.PolicyCounts
	GetPolicy(model.Key) api.Policy
	GetTier(model.Key) api.Tier
	GetOrderedPolicies(set.Set) []api.Tier
	RegisterWithDispatcher(dispatcher dispatcherv1v3.Interface)
	RegisterWithLabelHandler(handler labelhandler.Interface)
	GetPolicyKeySetByRuleSelector(string) set.Set
}

func NewPoliciesCache() PoliciesCache {
	return &policiesCache{
		globalNetworkPolicies: newPolicyCache(),
		networkPolicies:       newPolicyCache(),
		tiers:                 make(map[string]*tierData, 0),
		policySorter:          calc.NewPolicySorter(),
		ruleSelectors:         make(map[string]*ruleSelectorInfo),
	}
}

type policiesCache struct {
	globalNetworkPolicies *policyCache
	networkPolicies       *policyCache
	tiers                 map[string]*tierData
	policySorter          *calc.PolicySorter
	orderedTiers          []*tierData

	// Rule selectors are consolidated to reduce occupancy. We register with the label handler which
	// selectors we require for the rules.
	ruleRegistration labelhandler.RuleRegistrationInterface
	ruleSelectors    map[string]*ruleSelectorInfo
}

type policyCache struct {
	policies          map[model.Key]*policyData
	unmatchedPolicies set.Set
}

func newPolicyCache() *policyCache {
	return &policyCache{
		policies:          make(map[model.Key]*policyData, 0),
		unmatchedPolicies: set.New(),
	}
}

func (c *policiesCache) TotalPolicies() api.PolicyCounts {
	return api.PolicyCounts{
		NumGlobalNetworkPolicies: len(c.globalNetworkPolicies.policies),
		NumNetworkPolicies:       len(c.networkPolicies.policies),
	}
}

func (c *policiesCache) UnmatchedPolicies() api.PolicyCounts {
	return api.PolicyCounts{
		NumGlobalNetworkPolicies: c.globalNetworkPolicies.unmatchedPolicies.Len(),
		NumNetworkPolicies:       c.networkPolicies.unmatchedPolicies.Len(),
	}
}

func (c *policiesCache) GetPolicy(key model.Key) api.Policy {
	if policy := c.getPolicy(key); policy != nil {
		return c.combinePolicyDataWithRules(policy)
	}
	return nil
}

func (c *policiesCache) GetTier(key model.Key) api.Tier {
	c.orderPolicies()
	t := c.tiers[key.(model.ResourceKey).Name]
	if t == nil {
		return nil
	}
	return c.combineTierDataWithRules(t)
}

func (c *policiesCache) GetOrderedPolicies(keys set.Set) []api.Tier {
	c.orderPolicies()
	var tierDatas []*tierData
	if keys == nil {
		tierDatas = c.orderedTiers
	} else {
		tierDatas = make([]*tierData, 0)
		for _, t := range c.orderedTiers {
			td := &tierData{
				resource: t.resource,
				name:     t.name,
			}
			for _, p := range t.orderedPolicies {
				if keys.Contains(p.getKey()) {
					td.orderedPolicies = append(td.orderedPolicies, p)
				}
			}
			if len(td.orderedPolicies) > 0 {
				tierDatas = append(tierDatas, td)
			}
		}
	}

	// Add the rule information to the tiers before returning.
	tiers := make([]api.Tier, len(tierDatas))
	for i, td := range tierDatas {
		tiers[i] = c.combineTierDataWithRules(td)
	}

	return tiers
}

func (c *policiesCache) GetPolicyKeySetByRuleSelector(selector string) set.Set {
	if rs := c.ruleSelectors[selector]; rs != nil {
		return rs.policies
	}
	return set.New()
}

func (c *policiesCache) RegisterWithDispatcher(dispatcher dispatcherv1v3.Interface) {
	dispatcher.RegisterHandler(v3.KindGlobalNetworkPolicy, c.onUpdate)
	dispatcher.RegisterHandler(v3.KindNetworkPolicy, c.onUpdate)
	dispatcher.RegisterHandler(v3.KindTier, c.onUpdate)
}

func (c *policiesCache) RegisterWithLabelHandler(handler labelhandler.Interface) {
	handler.RegisterPolicyHandler(c.policyEndpointMatch)
	c.ruleRegistration = handler.RegisterRuleHandler(c.ruleEndpointMatch)
}

func (c *policiesCache) policyEndpointMatch(matchType labelhandler.MatchType, polKey model.Key, epKey model.Key) {
	erk := epKey.(model.ResourceKey)
	pc := c.getPolicyCache(polKey)
	pd := pc.policies[polKey]
	if pd == nil {
		// The policy has been deleted. Since the policy cache is updated before the index handler is updated this is
		// a valid scenario, and should be treated as a no-op.
		return
	}
	switch erk.Kind {
	case v3.KindHostEndpoint:
		pd.endpoints.NumHostEndpoints += matchTypeToDelta[matchType]
	case v3.KindWorkloadEndpoint:
		pd.endpoints.NumWorkloadEndpoints += matchTypeToDelta[matchType]
	default:
		log.WithField("key", erk).Error("Unexpected resource in event type, expecting a v3 endpoint type")
	}

	if pd.IsUnmatched() {
		pc.unmatchedPolicies.Add(polKey)
	} else {
		pc.unmatchedPolicies.Discard(polKey)
	}
}

func (c *policiesCache) ruleEndpointMatch(matchType labelhandler.MatchType, selector string, epKey model.Key) {
	erk := epKey.(model.ResourceKey)
	rsi := c.ruleSelectors[selector]
	// The current rule selector may not be registered if the rule was modified or deleted.  No worries
	// - just skip this match update.
	if rsi == nil {
		return
	}

	switch erk.Kind {
	case v3.KindHostEndpoint:
		rsi.endpoints.NumHostEndpoints += matchTypeToDelta[matchType]
	case v3.KindWorkloadEndpoint:
		rsi.endpoints.NumWorkloadEndpoints += matchTypeToDelta[matchType]
	default:
		log.WithField("key", erk).Error("Unexpected resource in event type, expecting a v3 endpoint type")
	}
}

func (c *policiesCache) onUpdate(update dispatcherv1v3.Update) {
	uv1 := update.UpdateV1
	uv3 := update.UpdateV3

	// Manage our internal tier and policy cache first.
	switch v1k := uv1.Key.(type) {
	case model.TierKey:
		name := v1k.Name
		switch uv3.UpdateType {
		case bapi.UpdateTypeKVNew:
			c.tiers[name] = &tierData{
				name:     name,
				resource: uv3.Value.(api.Resource),
			}
		case bapi.UpdateTypeKVUpdated:
			c.tiers[name].resource = uv3.Value.(api.Resource)
		case bapi.UpdateTypeKVDeleted:
			delete(c.tiers, name)
		}
	case model.PolicyKey:
		pc := c.getPolicyCache(uv3.Key)
		if pc == nil {
			return
		}
		switch uv3.UpdateType {
		case bapi.UpdateTypeKVNew:
			pv1 := uv1.Value.(*model.Policy)
			pd := &policyData{
				resource: uv3.Value.(api.Resource),
				v1Policy: pv1,
			}
			pc.policies[uv3.Key] = pd
			pc.unmatchedPolicies.Add(uv3.Key)
			// Add rule selectors for this new policy
			c.addPolicyRuleSelectors(pv1, uv3.Key)
		case bapi.UpdateTypeKVUpdated:
			pv1 := uv1.Value.(*model.Policy)
			existing := pc.policies[uv3.Key]
			existing.resource = uv3.Value.(api.Resource)
			// Remove references to the policy from its current set of rule selectors.
			// We have to remove these references since they are possibly outdated with
			// any changes to the rule selectors. The policy references will be added
			// back to all applicable rule selectors in addPolicyRuleSelectors.
			c.deleteRuleSelectorPolicyReferences(existing.v1Policy, uv3.Key)
			// Update rule selectors for this policy. We add the new ones first and then unregister
			// the old ones - that prevents us potentially removing and adding back in a selector.
			c.addPolicyRuleSelectors(pv1, uv3.Key)
			c.deletePolicyRuleSelectors(existing.v1Policy)
			existing.v1Policy = pv1
		case bapi.UpdateTypeKVDeleted:
			existing := pc.policies[uv3.Key]
			delete(pc.policies, uv3.Key)
			pc.unmatchedPolicies.Discard(uv3.Key)
			// Remove references to this policy from rule selectors
			c.deleteRuleSelectorPolicyReferences(existing.v1Policy, uv3.Key)
			// Remove the rule selectors for this policy.
			c.deletePolicyRuleSelectors(existing.v1Policy)
		}
	}

	// Update the policy sorter, invalidating our ordered tiers if the policy order needs
	// recalculating.
	if c.policySorter.OnUpdate(*uv1) {
		c.orderedTiers = nil
	}
}

// addPolicyRuleSelectors ensures we are tracking the rule selectors in the policy. This tracks
// based on the selector string and ensures we track identical selectors only once.
func (c *policiesCache) addPolicyRuleSelectors(p *model.Policy, polKey model.Key) {
	add := func(s string) {
		if s == "" {
			// Empty rule selectors are not tracked since we only care about endpoints and network sets that are
			// explicitly selected rather than included in the "everywhere" empty selector.
			return
		}
		rsi := c.ruleSelectors[s]
		if rsi == nil {
			rsi = &ruleSelectorInfo{
				policies: set.New(),
			}
			c.ruleSelectors[s] = rsi
		}
		rsi.numRuleRefs++
		if rsi.numRuleRefs == 1 {
			c.ruleRegistration.AddRuleSelector(s)
		}
		rsi.policies.Add(polKey)
	}

	for i := range p.InboundRules {
		r := &p.InboundRules[i]
		add(c.getSrcSelector(r))
		add(c.getDstSelector(r))
	}
	for i := range p.OutboundRules {
		r := &p.OutboundRules[i]
		add(c.getSrcSelector(r))
		add(c.getDstSelector(r))
	}
}

// deletePolicyRuleSelectors deletes the tracking of the rule selectors in the policy.
func (c *policiesCache) deletePolicyRuleSelectors(p *model.Policy) {
	del := func(s string) {
		if s == "" {
			// Empty rule selectors are not tracked since we only care about endpoints and network sets that are
			// explicitly selected rather than included in the "everywhere" empty selector.
			return
		}
		rsi := c.ruleSelectors[s]
		rsi.numRuleRefs--
		if rsi.numRuleRefs == 0 {
			delete(c.ruleSelectors, s)
			c.ruleRegistration.RemoveRuleSelector(s)
		}
	}

	for i := range p.InboundRules {
		r := &p.InboundRules[i]
		del(c.getSrcSelector(r))
		del(c.getDstSelector(r))
	}
	for i := range p.OutboundRules {
		r := &p.OutboundRules[i]
		del(c.getSrcSelector(r))
		del(c.getDstSelector(r))
	}
}

// deleteRuleSelectorPolicyReferences deletes the policy references that denote which policies
// contain a rule selector on the rule selector info.
func (c *policiesCache) deleteRuleSelectorPolicyReferences(p *model.Policy, polKey model.Key) {
	del := func(s string) {
		if s == "" {
			// Empty rule selectors are not tracked since we only care about endpoints and network sets that are
			// explicitly selected rather than included in the "everywhere" empty selector.
			return
		}
		rsi := c.ruleSelectors[s]
		rsi.policies.Discard(polKey)
	}

	for i := range p.InboundRules {
		r := &p.InboundRules[i]
		del(c.getSrcSelector(r))
		del(c.getDstSelector(r))
	}
	for i := range p.OutboundRules {
		r := &p.OutboundRules[i]
		del(c.getSrcSelector(r))
		del(c.getDstSelector(r))
	}
}

// combinePolicyDataWithRules combines the policyData with the cached rule data. The rule data
// is looked up from the effective selector string for each rule. An empty selector is not tracked
// and any associated endpoint counts should be zeroed.
func (c *policiesCache) combinePolicyDataWithRules(p *policyData) *policyDataWithRuleData {
	prd := &policyDataWithRuleData{
		policyData: p,
		ruleEndpoints: api.Rule{
			Ingress: make([]api.RuleDirection, len(p.v1Policy.InboundRules)),
			Egress:  make([]api.RuleDirection, len(p.v1Policy.OutboundRules)),
		},
	}

	setEndpoints := func(v1r *model.Rule, r *api.RuleDirection) {
		if s := c.getDstSelector(v1r); s != "" {
			r.Destination = c.ruleSelectors[s].endpoints
		} else {
			r.Destination = api.EndpointCounts{}
		}
		if s := c.getSrcSelector(v1r); s != "" {
			r.Source = c.ruleSelectors[s].endpoints
		} else {
			r.Source = api.EndpointCounts{}
		}
	}

	for i := range prd.ruleEndpoints.Ingress {
		setEndpoints(&p.v1Policy.InboundRules[i], &prd.ruleEndpoints.Ingress[i])
	}
	for i := range prd.ruleEndpoints.Egress {
		setEndpoints(&p.v1Policy.OutboundRules[i], &prd.ruleEndpoints.Egress[i])
	}

	return prd
}

// getSrcSelector returns the effective source selector by combining the positive and negative
// selectors.
func (c *policiesCache) getSrcSelector(r *model.Rule) string {
	return c.combineSelector(r.SrcSelector, r.NotSrcSelector)
}

// getSrcSelector returns the effective destination selector by combining the positive and negative
// selectors.
func (c *policiesCache) getDstSelector(r *model.Rule) string {
	return c.combineSelector(r.DstSelector, r.NotDstSelector)
}

// combineSelector combines the positive and negative selectors into a single selector string.
// This is slightly different from Felix which only combines the selectors provided the positive
// selector is not empty (since that means "anywhere"), but since we are only interested in
// endpoint counts, we can treat and empty positive selector as "all()" which means we can
// always combine the two selectors into a single selector.
func (c *policiesCache) combineSelector(sel, notSel string) string {
	if sel == "" {
		if notSel == "" {
			return ""
		}
		return "!(" + notSel + ")"
	}
	if notSel == "" {
		return sel
	}
	return "(" + sel + ") && !(" + notSel + ")"
}

// combineTierDataWithRules returns the tier data with the cached rule data.
func (c *policiesCache) combineTierDataWithRules(t *tierData) *tierDataWithRuleData {
	tdr := &tierDataWithRuleData{
		tierData:                t,
		orderedPoliciesWithData: make([]api.Policy, len(t.orderedPolicies)),
	}

	for i := range t.orderedPolicies {
		tdr.orderedPoliciesWithData[i] = c.combinePolicyDataWithRules(t.orderedPolicies[i])
	}

	return tdr
}

// orderPolicies orders the tierData and policyData within each Tier based on the order of
// application by Felix.
func (c *policiesCache) orderPolicies() {
	if c.orderedTiers != nil {
		return
	}
	tiers := c.policySorter.Sorted()
	c.orderedTiers = make([]*tierData, 0, len(tiers))
	for _, tier := range tiers {
		td := c.tiers[tier.Name]
		if td == nil {
			td = &tierData{name: tier.Name}
		}
		c.orderedTiers = append(c.orderedTiers, td)

		// Reset and reconstruct the ordered policies slice.
		td.orderedPolicies = nil
		for _, policy := range tier.OrderedPolicies {
			policyData := c.getPolicyFromV1Key(policy.Key)
			td.orderedPolicies = append(td.orderedPolicies, policyData)
		}
	}
}

func (c *policiesCache) getPolicyFromV1Key(key model.PolicyKey) *policyData {
	parts := strings.Split(key.Name, "/")
	if len(parts) == 1 {
		return c.globalNetworkPolicies.policies[model.ResourceKey{
			Kind: v3.KindGlobalNetworkPolicy,
			Name: parts[0],
		}]
	}
	return c.networkPolicies.policies[model.ResourceKey{
		Kind:      v3.KindNetworkPolicy,
		Namespace: parts[0],
		Name:      parts[1],
	}]
}

func (c *policiesCache) getPolicy(key model.Key) *policyData {
	pc := c.getPolicyCache(key)
	if pc == nil {
		return nil
	}
	return pc.policies[key]
}

func (c *policiesCache) getPolicyCache(polKey model.Key) *policyCache {
	if rKey, ok := polKey.(model.ResourceKey); ok {
		switch rKey.Kind {
		case v3.KindGlobalNetworkPolicy:
			return c.globalNetworkPolicies
		case v3.KindNetworkPolicy:
			return c.networkPolicies
		}
	}
	log.WithField("key", polKey).Error("Unexpected resource in event type, expecting a v3 policy type")
	return nil
}

// policyData is used to hold policy data in the cache, and also implements the Policy interface
// for returning on queries. The v1 data model is maintained to enable us to track rule selector
// references.
type policyData struct {
	resource  api.Resource
	endpoints api.EndpointCounts
	v1Policy  *model.Policy
}

func (d *policyData) GetEndpointCounts() api.EndpointCounts {
	return d.endpoints
}

func (d *policyData) GetResource() api.Resource {
	return d.resource
}

func (d *policyData) GetTier() string {
	switch r := d.resource.(type) {
	case *v3.NetworkPolicy:
		return r.Spec.Tier
	case *v3.GlobalNetworkPolicy:
		return r.Spec.Tier
	}
	return ""
}

func (d *policyData) IsUnmatched() bool {
	return d.endpoints.NumWorkloadEndpoints == 0 && d.endpoints.NumHostEndpoints == 0
}

func (d *policyData) getKey() model.Key {
	return model.ResourceKey{
		Kind:      d.resource.GetObjectKind().GroupVersionKind().Kind,
		Name:      d.resource.GetObjectMeta().GetName(),
		Namespace: d.resource.GetObjectMeta().GetNamespace(),
	}
}

// tierData is used to hold policy data in the cache, and also implements the Policy interface
// for returning on queries.
type tierData struct {
	name            string
	resource        api.Resource
	orderedPolicies []*policyData
}

func (d *tierData) GetName() string {
	return d.name
}

func (d *tierData) GetResource() api.Resource {
	return d.resource
}

type ruleSelectorInfo struct {
	numRuleRefs int
	endpoints   api.EndpointCounts
	policies    set.Set
}

// policyDataWithRuleData is a non-cached version of the policy data, but it includes
// the rule endpoint stats that are dynamically created.
type policyDataWithRuleData struct {
	*policyData
	ruleEndpoints api.Rule
}

func (d *policyDataWithRuleData) GetRuleEndpointCounts() api.Rule {
	return d.ruleEndpoints
}

type tierDataWithRuleData struct {
	*tierData
	orderedPoliciesWithData []api.Policy
}

func (d *tierDataWithRuleData) GetOrderedPolicies() []api.Policy {
	return d.orderedPoliciesWithData
}
