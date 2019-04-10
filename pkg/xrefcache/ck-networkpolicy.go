// Copyright (c) 2019 Tigera, Inc. All rights reserved.
package xrefcache

import (
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/backend/k8s/conversion"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/backend/syncersv1/updateprocessors"

	"github.com/tigera/compliance/pkg/internet"
	"github.com/tigera/compliance/pkg/resources"
	"github.com/tigera/compliance/pkg/syncer"
)

var (
	// The network policy cache is populated by both Kubernetes and Calico policy types. Include KindSelector in here so
	// the queued recalculation processing knows where to send those updates.
	KindsNetworkPolicy = []metav1.TypeMeta{
		resources.TypeCalicoGlobalNetworkPolicies,
		resources.TypeCalicoNetworkPolicies,
		resources.TypeK8sNetworkPolicies,
	}
)

// VersionedPolicyResource is an extension to the VersionedResource interface with some NetworkPolicy specific
// helper methods.
type VersionedPolicyResource interface {
	VersionedResource
	getV1Policy() *model.Policy
	getV3IngressRules() []apiv3.Rule
	getV3EgressRules() []apiv3.Rule
	isNamespaced() bool
}

// CacheEntryNetworkPolicy is a cache entry in the NetworkPolicy cache. Each entry implements the CacheEntry
// interface.
type CacheEntryNetworkPolicy struct {
	// The versioned policy resource.
	VersionedPolicyResource

	// Boolean values associated with this pod. Valid flags defined by CacheEntryFlagsNetworkPolicy.
	Flags CacheEntryFlags

	// The matching rules.
	AllowRuleSelectors resources.Set

	// The pods matching this policy selector.
	SelectedPods          resources.Set
	SelectedHostEndpoints resources.Set

	// --- Internal data ---
	cacheEntryCommon
	clog *log.Entry
}

// getVersionedResource implements the CacheEntry interface.
func (c *CacheEntryNetworkPolicy) getVersionedResource() VersionedResource {
	return c.VersionedPolicyResource
}

// setVersionedResource implements the CacheEntry interface.
func (c *CacheEntryNetworkPolicy) setVersionedResource(r VersionedResource) {
	c.VersionedPolicyResource = r.(VersionedPolicyResource)
}

// versionedCalicoNetworkPolicy implements the VersionedNetworkSetResource for a Calico NetworkPolicy kind.
type versionedCalicoNetworkPolicy struct {
	*apiv3.NetworkPolicy
	v1 *model.Policy
}

// getV3 implements the VersionedPolicyResource interface.
func (v *versionedCalicoNetworkPolicy) getV3() resources.Resource {
	return v.NetworkPolicy
}

// getV3IngressRules implements the VersionedPolicyResource interface.
func (v *versionedCalicoNetworkPolicy) getV3IngressRules() []apiv3.Rule {
	return v.NetworkPolicy.Spec.Ingress
}

// getV3EgressRules implements the VersionedPolicyResource interface.
func (v *versionedCalicoNetworkPolicy) getV3EgressRules() []apiv3.Rule {
	return v.NetworkPolicy.Spec.Egress
}

// getV1 implements the VersionedPolicyResource interface.
func (v *versionedCalicoNetworkPolicy) getV1() interface{} {
	return v.v1
}

// getV1Policy implements the VersionedPolicyResource interface.
func (v *versionedCalicoNetworkPolicy) getV1Policy() *model.Policy {
	return v.v1
}

// isNamespaced implements the VersionedPolicyResource interface.
func (v *versionedCalicoNetworkPolicy) isNamespaced() bool {
	return true
}

// versionedCalicoNetworkPolicy implements the VersionedNetworkSetResource for a Calico GlobalNetworkPolicy kind.
type versionedCalicoGlobalNetworkPolicy struct {
	*apiv3.GlobalNetworkPolicy
	v1 *model.Policy
}

// getV3 implements the VersionedPolicyResource interface.
func (v *versionedCalicoGlobalNetworkPolicy) getV3() resources.Resource {
	return v.GlobalNetworkPolicy
}

// getV3IngressRules implements the VersionedPolicyResource interface.
func (v *versionedCalicoGlobalNetworkPolicy) getV3IngressRules() []apiv3.Rule {
	return v.GlobalNetworkPolicy.Spec.Ingress
}

// getV3EgressRules implements the VersionedPolicyResource interface.
func (v *versionedCalicoGlobalNetworkPolicy) getV3EgressRules() []apiv3.Rule {
	return v.GlobalNetworkPolicy.Spec.Egress
}

// getV1 implements the VersionedPolicyResource interface.
func (v *versionedCalicoGlobalNetworkPolicy) getV1() interface{} {
	return v.v1
}

// getV1Policy implements the VersionedPolicyResource interface.
func (v *versionedCalicoGlobalNetworkPolicy) getV1Policy() *model.Policy {
	return v.v1
}

// isNamespaced implements the VersionedPolicyResource interface.
func (v *versionedCalicoGlobalNetworkPolicy) isNamespaced() bool {
	return false
}

// versionedCalicoNetworkPolicy implements the VersionedNetworkSetResource for a K8s NetworkPolicy kind.
type versionedK8sNetworkPolicy struct {
	*networkingv1.NetworkPolicy
	v3 *apiv3.NetworkPolicy
	v1 *model.Policy
}

// getV3 implements the VersionedPolicyResource interface.
func (v *versionedK8sNetworkPolicy) getV3() resources.Resource {
	return v.v3
}

// getV3IngressRules implements the VersionedPolicyResource interface.
func (v *versionedK8sNetworkPolicy) getV3IngressRules() []apiv3.Rule {
	return v.v3.Spec.Ingress
}

// getV3EgressRules implements the VersionedPolicyResource interface.
func (v *versionedK8sNetworkPolicy) getV3EgressRules() []apiv3.Rule {
	return v.v3.Spec.Egress
}

// getV1 implements the VersionedPolicyResource interface.
func (v *versionedK8sNetworkPolicy) getV1() interface{} {
	return v.v1
}

// getV1Policy implements the VersionedPolicyResource interface.
func (v *versionedK8sNetworkPolicy) getV1Policy() *model.Policy {
	return v.v1
}

// isNamespaced implements the VersionedPolicyResource interface.
func (v *versionedK8sNetworkPolicy) isNamespaced() bool {
	return true
}

// newNetworkPoliciesEngine creates a new engine used for the NetworkPolicy cache.
func newNetworkPoliciesEngine() resourceCacheEngine {
	return &networkPolicyEngine{}
}

// networkPolicyEngine implements the resourceCacheEngine interface for the NetworkPolicy cache.
type networkPolicyEngine struct {
	engineCache
	converter conversion.Converter
}

// register implements the resourceCacheEngine interface.
func (c *networkPolicyEngine) register(cache engineCache) {
	c.engineCache = cache

	// Register with the endpoint and netset label selectors for notification of match start/stops.
	c.EndpointLabelSelector().RegisterCallbacks(c.kinds(), c.endpointMatchStarted, c.endpointMatchStopped)
	c.NetworkPolicyRuleSelectorManager().RegisterCallbacks(c.ruleSelectorMatchStarted, c.ruleSelectorMatchStopped)

	// Register for updates for all RuleSelector events (which is currently the same as the available flags for the
	// CacheEntryFlagsNetworkSets). We don't care about Added/Deleted/Updated events as any changes to the
	// cross-referencing will result in a notification here where we will requeue any changed policies.
	for _, kind := range KindsNetworkPolicyRuleSelectors {
		c.RegisterOnUpdateHandler(
			kind,
			syncer.UpdateType(CacheEntryFlagsNetworkSets),
			c.queuePoliciesForRecalculation,
		)
	}
}

// register implements the resourceCacheEngine interface.
func (c *networkPolicyEngine) kinds() []metav1.TypeMeta {
	return KindsNetworkPolicy
}

// newCacheEntry implements the resourceCacheEngine interface.
func (c *networkPolicyEngine) newCacheEntry() CacheEntry {
	return &CacheEntryNetworkPolicy{
		AllowRuleSelectors:    resources.NewSet(),
		SelectedPods:          resources.NewSet(),
		SelectedHostEndpoints: resources.NewSet(),
	}
}

// resourceAdded implements the resourceCacheEngine interface.
func (c *networkPolicyEngine) resourceAdded(id apiv3.ResourceID, entry CacheEntry) {
	// Set the context log.
	entry.(*CacheEntryNetworkPolicy).clog = log.WithField("policy", id)

	// Just call through to our update processsing.
	c.resourceUpdated(id, entry, nil)
}

// resourceUpdated implements the resourceCacheEngine interface.
func (c *networkPolicyEngine) resourceUpdated(id apiv3.ResourceID, entry CacheEntry, prev VersionedResource) {
	// Get the augmented resource data.
	x := entry.(*CacheEntryNetworkPolicy)

	// Update the label selector for this policy. This may result in callbacks that will update the links between the
	// policy and the selected endpoints.
	c.EndpointLabelSelector().UpdateSelector(id, x.getV1Policy().Selector)

	// Update the label selectors for the policy rules.
	c.updateRuleSelectors(id, x)
}

// resourceDeleted implements the resourceCacheEngine interface.
func (c *networkPolicyEngine) resourceDeleted(id apiv3.ResourceID, res CacheEntry) {
	// Delete the label selector for this policy.
	c.EndpointLabelSelector().DeleteSelector(id)

	// Delete the rule selectors associated with this policy.
	c.NetworkPolicyRuleSelectorManager().DeletePolicy(id)
}

// recalculate implements the resourceCacheEngine interface.
func (c *networkPolicyEngine) recalculate(id apiv3.ResourceID, entry CacheEntry) syncer.UpdateType {
	// Async recalculation is required due to any rule/selector updates.
	x := entry.(*CacheEntryNetworkPolicy)

	// Update the internal view of our data.
	changed := c.scanProtected(id, x)
	changed |= c.scanIngressRules(x)
	changed |= c.scanEgressRules(x)

	x.clog.Debugf("Recalculated, returning update %d, flags now: %d", changed, x.Flags)
	return syncer.UpdateType(changed)
}

// convertToVersioned implements the resourceCacheEngine interface.
func (c *networkPolicyEngine) convertToVersioned(res resources.Resource) (VersionedResource, error) {
	switch in := res.(type) {
	case *apiv3.NetworkPolicy:
		v1, err := updateprocessors.ConvertNetworkPolicyV3ToV1Value(in)
		if err != nil {
			return nil, err
		}
		return &versionedCalicoNetworkPolicy{
			NetworkPolicy: in,
			v1:            v1.(*model.Policy),
		}, nil
	case *apiv3.GlobalNetworkPolicy:
		v1, err := updateprocessors.ConvertGlobalNetworkPolicyV3ToV1Value(in)
		if err != nil {
			return nil, err
		}
		return &versionedCalicoGlobalNetworkPolicy{
			GlobalNetworkPolicy: in,
			v1:                  v1.(*model.Policy),
		}, nil
	case *networkingv1.NetworkPolicy:
		kvp, err := c.converter.K8sNetworkPolicyToCalico(in)
		if err != nil {
			return nil, err
		}
		v3 := kvp.Value.(*apiv3.NetworkPolicy)
		v1, err := updateprocessors.ConvertNetworkPolicyV3ToV1Value(v3)
		if err != nil {
			return nil, err
		}
		return &versionedK8sNetworkPolicy{
			NetworkPolicy: in,
			v3:            v3,
			v1:            v1.(*model.Policy),
		}, nil
	}

	return nil, fmt.Errorf("unhandled resource type: %v", res)
}

// updateRuleSelectors reads the set of policy rule selectors and tracks any allow rules selectors (since these are the
// only ones that could cause exposure to IPs via network sets). To reduce churn, we group identical selector values
// across all rules and all Policies (so there is a little book keeping required here).
func (c *networkPolicyEngine) updateRuleSelectors(id apiv3.ResourceID, x *CacheEntryNetworkPolicy) {
	// We care about newSelectors on Allow rules, so lets get the set of newSelectors that we care about for this policy.
	newSelectors := resources.NewSet()

	// Loop through the rules to check if exposed to another namespace. This is determined by checking allow rules to
	// see if any Namespace newSelectors have been specified.
	ingressV3 := x.getV3IngressRules()
	ingressV1 := x.getV1Policy().InboundRules

	for i, irV3 := range ingressV3 {
		if irV3.Action == apiv3.Allow && ingressV1[i].SrcSelector != "" {
			newSelectors.Add(selectorToSelectorID(ingressV1[i].SrcSelector))
		}
	}

	egressV3 := x.getV3EgressRules()
	egressV1 := x.getV1Policy().OutboundRules

	for i, erV3 := range egressV3 {
		if erV3.Action == apiv3.Allow && egressV1[i].DstSelector != "" {
			newSelectors.Add(selectorToSelectorID(egressV1[i].DstSelector))
		}
	}

	// Reference with the rule selector manager the updated set of rule selectors for this policy.
	c.NetworkPolicyRuleSelectorManager().SetPolicyRuleSelectors(id, newSelectors)
}

// scanIngressRules scans the ingress rules and updates the augmented data for a policy.
func (c *networkPolicyEngine) scanIngressRules(x *CacheEntryNetworkPolicy) syncer.UpdateType {
	oldFlags := x.Flags

	// Reset ingress stats based on rules
	x.Flags &^= CacheEntryInternetExposedIngress | CacheEntryOtherNamespaceExposedIngress

	// Loop through the rules to check if exposed to another namespace. This is determined by checking allow rules to
	// see if any Namespace selectors have been specified.
	ingressV3 := x.getV3IngressRules()
	ingressV1 := x.getV1Policy().InboundRules

	for i, irV3 := range ingressV3 {
		// Only allow rules can impact our exposure.
		if irV3.Action != apiv3.Allow {
			x.clog.Debugf("Skipping non-allow rule")
			continue
		}

		//TODO (rlb): Nets may contain "other namespace"
		irV1 := ingressV1[i]

		// Use the v3 settings to check if there is a NamespaceSelector specified. It is hard to do this with the v1
		// settings since the selectors are munged together.
		if !x.isNamespaced() || irV3.Source.NamespaceSelector != "" {
			x.clog.WithField("nsSelector", irV3.Source.NamespaceSelector).Debugf("Policy is not namespaced, or namespace selector is configured")
			if len(irV1.SrcNets) == 0 {
				x.clog.Debugf("Not matching on nets, therefore exposed to other namespaces")
				x.Flags |= CacheEntryOtherNamespaceExposedIngress
			}
		}
		if x.Flags&CacheEntryInternetExposedIngress == 0 {
			x.clog.Debugf("Checking if exposed to internet")
			if irV1.SrcSelector == "" {
				// There is no v1 source selector. Check the nets to see if we are exposed. Note that for ingress
				// we don't care about the dest selector since that would simply further limit which endpoints
				// the policy applies to rather than where traffic originated.
				x.clog.Debugf("No source selector")
				if len(irV1.SrcNets) == 0 {
					x.clog.Debugf("No match on source nets - exposed to all addresses")
					x.Flags |= CacheEntryInternetExposedIngress | CacheEntryOtherNamespaceExposedIngress
				} else if internet.NetPointersContainInternetAddr(irV1.SrcNets) {
					x.clog.Debugf("Source nets contain an internet address")
					x.Flags |= CacheEntryInternetExposedIngress
				}
			} else if sel := c.GetFromXrefCache(selectorToSelectorID(irV1.SrcSelector)).(*CacheEntryNetworkPolicyRuleSelector); sel != nil {
				// Found the selector in the cache.  If the effective network set settings for this selector indicate
				// internet exposure then update our flags.
				x.clog.Debugf("Source selector is specified, found cached selector details")
				if sel.NetworkSetFlags&CacheEntryInternetExposed != 0 {
					x.clog.Debugf("Policy ingress allow rule selector references netset exposed to internet: %s", irV1.SrcSelector)
					x.Flags |= CacheEntryInternetExposedIngress
				}
			} else {
				x.clog.Errorf("Allow rule selector is not in cache: %s", irV1.SrcSelector)
			}
		}
	}

	return syncer.UpdateType(x.Flags ^ oldFlags)
}

// scanEgressRules scans the egress rules and updates the augmented data for a policy.
func (c *networkPolicyEngine) scanEgressRules(x *CacheEntryNetworkPolicy) syncer.UpdateType {
	oldFlags := x.Flags

	// Reset egress stats based on rules
	x.Flags &^= CacheEntryInternetExposedEgress | CacheEntryOtherNamespaceExposedEgress

	// Loop through the rules to check if exposed to another namespace. This is determined by checking allow rules to
	// see if any Namespace selectors have been specified.
	egressV3 := x.getV3EgressRules()
	egressV1 := x.getV1Policy().OutboundRules

	for i, erV3 := range egressV3 {
		// Only allow rules can impact our exposure.
		if erV3.Action != apiv3.Allow {
			x.clog.Debugf("Skipping non-allow rule")
			continue
		}

		//TODO (rlb): Nets may contain "other namespace"
		erV1 := egressV1[i]

		// Use the v3 settings to check if there is a NamespaceSelector specified. It is hard to do this with the v1
		// settings since the selectors are munged together.
		if !x.isNamespaced() || erV3.Destination.NamespaceSelector != "" {
			x.clog.Debugf("Policy is not namespaced, or namespace selector is configured")
			if len(erV1.DstNets) == 0 {
				x.clog.Debugf("Not matching on nets, therefore exposed to other namespaces")
				x.Flags |= CacheEntryOtherNamespaceExposedEgress
			}
		}
		if x.Flags&CacheEntryInternetExposedEgress == 0 {
			x.clog.Debugf("Checking if exposed to internet")
			if erV1.DstSelector == "" {
				// There is no v1 destination selector. Check the nets to see if we are exposed. Note that for egress
				// we don't care about the dest selector since that would simply further limit which endpoints
				// the policy applies to rather than where traffic was destined.
				x.clog.Debugf("No destination selector")
				if len(erV1.DstNets) == 0 {
					x.clog.Debugf("No match on destination nets - exposed to all addresses")
					x.Flags |= CacheEntryInternetExposedEgress | CacheEntryOtherNamespaceExposedEgress
				} else if internet.NetPointersContainInternetAddr(erV1.DstNets) {
					x.clog.Debugf("Destination nets contain an internet address")
					x.Flags |= CacheEntryInternetExposedEgress
				}
			} else if sel := c.GetFromXrefCache(selectorToSelectorID(erV1.DstSelector)).(*CacheEntryNetworkPolicyRuleSelector); sel != nil {
				// Found the selector in the cache.  If the effective network set settings for this selector indicate
				// internet exposure then update our flags.
				x.clog.Debugf("Destination selector is specified, found cached selector details")
				if sel.NetworkSetFlags&CacheEntryInternetExposed != 0 {
					x.clog.Debugf("Policy egress allow rule selector references netset exposed to internet: %s", erV1.DstSelector)
					x.Flags |= CacheEntryInternetExposedEgress
				}
			} else {
				x.clog.Errorf("Allow rule selector is not in cache: %s", erV1.DstSelector)
			}
		}
	}

	return syncer.UpdateType(x.Flags ^ oldFlags)
}

// scanProtected scans whether the policy has ingress or egress protection and updates its augmented data. This is
// independent of other resources and may therefore be calculated as part of the resourceAdded or resourceUpdated call.
func (c *networkPolicyEngine) scanProtected(id apiv3.ResourceID, x *CacheEntryNetworkPolicy) syncer.UpdateType {
	oldFlags := x.Flags

	// The policy type can be ingress and/or egress. In terms of statistics, this equates to ingress and/or egress
	// protected. Assume both are unprotected unless we determine otherwise.
	x.Flags &^= CacheEntryProtectedEgress | CacheEntryProtectedIngress

	for _, t := range x.getV1Policy().Types {
		switch strings.ToLower(t) {
		case "ingress":
			x.clog.Debug("Flagging as ingress protected")
			x.Flags |= CacheEntryProtectedIngress
		case "egress":
			x.clog.Debug("Flagging as egress protected")
			x.Flags |= CacheEntryProtectedEgress
		}
	}

	return syncer.UpdateType(x.Flags ^ oldFlags)
}

func (c *networkPolicyEngine) queuePoliciesForRecalculation(update syncer.Update) {
	// We have only registered for notifications from NetworkSets and for changes to configuration that we care about.
	x := update.Resource.(*CacheEntryNetworkPolicyRuleSelector)

	x.Policies.Iter(func(id apiv3.ResourceID) error {
		c.QueueUpdate(id, nil, update.Type)
		return nil
	})
}

func (c *networkPolicyEngine) ruleSelectorMatchStarted(polId, selId apiv3.ResourceID) {
	x, ok := c.GetFromOurCache(polId).(*CacheEntryNetworkPolicy)
	if !ok {
		log.Errorf("Match started on policy, but policy is not in cache: %s matches %s", polId, selId)
		return
	}
	x.clog.Debugf("Adding %s to allowRuleSelectors for %s", selId, polId)
	x.AllowRuleSelectors.Add(selId)
	c.QueueUpdate(polId, nil, EventPolicyRuleSelectorMatchStarted)
}

func (c *networkPolicyEngine) ruleSelectorMatchStopped(polId, selId apiv3.ResourceID) {
	x, ok := c.GetFromOurCache(polId).(*CacheEntryNetworkPolicy)
	if !ok {
		log.Errorf("Match stopped on policy, but policy is not in cache: %s matches %s", polId, selId)
		return
	}
	x.clog.Debugf("Removing %s from allowRuleSelectors for %s", selId, polId)
	x.AllowRuleSelectors.Discard(selId)
	c.QueueUpdate(polId, nil, EventPolicyRuleSelectorMatchStopped)
}

func (c *networkPolicyEngine) endpointMatchStarted(policyId, epId apiv3.ResourceID) {
	x, ok := c.GetFromOurCache(policyId).(*CacheEntryNetworkPolicy)
	if !ok {
		log.Errorf("Match started on policy, but policy is not in cache: %s matches %s", policyId, epId)
		return
	}
	switch epId.TypeMeta {
	case resources.TypeK8sPods:
		// Update the pod list in our policy data. No need to queue any policy recalculations since the endpoint
		// data does not directly affect it.
		x.clog.Debugf("Adding %s to pods for %s", epId, policyId)
		x.SelectedPods.Add(epId)
	case resources.TypeCalicoHostEndpoints:
		// Update the HEP list in our policy data. No need to queue any policy recalculations since the endpoint
		// data does not directly affect it.
		x.clog.Debugf("Adding %s to heps for %s", epId, policyId)
		x.SelectedHostEndpoints.Add(epId)
	}
}

func (c *networkPolicyEngine) endpointMatchStopped(policyId, epId apiv3.ResourceID) {
	x, ok := c.GetFromOurCache(policyId).(*CacheEntryNetworkPolicy)
	if !ok {
		log.Errorf("Match stopped on policy, but policy is not in cache: %s matches %s", policyId, epId)
		return
	}
	switch epId.TypeMeta {
	case resources.TypeK8sPods:
		// Update the pod list in our policy data. No need to queue any policy recalculations since the endpoint
		// data does not directly affect it.
		x.clog.Debugf("Removing %s from pods for %s", epId, policyId)
		x.SelectedPods.Discard(epId)
	case resources.TypeCalicoHostEndpoints:
		// Update the HEP list in our policy data. No need to queue any policy recalculations since the endpoint
		// data does not directly affect it.
		x.clog.Debugf("Removing %s from heps for %s", epId, policyId)
		x.SelectedHostEndpoints.Discard(epId)
	}
}
