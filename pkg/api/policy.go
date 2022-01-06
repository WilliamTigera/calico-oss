// Copyright (c) 2020-2021 Tigera, Inc. All rights reserved.
package api

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/projectcalico/calico/libcalico-go/lib/backend/model"
)

const (
	knpPrefix = "knp"
	knsPrefix = "kns"

	// Backward compatible - handle mixed entries of policy strings,
	// old format "index|tier|name|action" (count=4) and
	// new format "index|tier|name|aciton|ruleidindex" (count=5).
	oldPolicyPartsCount = 4
	newPolicyPartsCount = 5

	policyStrIndexIdx       = 0
	policyStrTierIdx        = 1
	policyStrNameIdx        = 2
	policyStrActionIdx      = 3
	policyStrRuleIdIndexIdx = 4
)

// PolicyHit represents a policy log in a flow log. This interface is used to make a the implementation read only, as the
// implementation is a representation of a log that is not changing. Certain Set actions have bee added, however they
// return a changed copy of the underlying policy hit to maintain the immutable properties.
type PolicyHit interface {
	// Action returns the action for this policy hit. See AllActions() for a list of possible values that could be returned.
	Action() Action

	// Count returns the number of flow logs that this policy hit was applied to.
	Count() int64

	// FlowLogName returns the name as it would appear in the flow log. This is unique for a specific policy instance.
	// -  <tier>.<name>
	// -  <namespace>/<tier>.<name>
	// -  <namespace>/<tier>.staged:<name>
	// -  <namespace>/knp.default.<name>
	// -  <namespace>/staged:knp.default.<name>
	// -  <namespace>/staged:knp.default.<name>
	// -  __PROFILE__.kns.<namespace>
	FlowLogName() string

	// FullName returns the full policy name, which includes the tier prefix for calico policy or the
	// "knp.default" prefix
	// for Kubernetes policies.
	FullName() string

	// Index returns the index for this hit.
	Index() int

	// IsKubernetes returns whether or not this policy is a staged policy.
	IsKubernetes() bool

	// IsProfile returns whether or not this policy is a profile.
	IsProfile() bool

	// IsStaged returns whether or not this policy is a staged policy.
	IsStaged() bool

	// Name returns the raw name of the policy without any tier or knp prefixes.
	Name() string

	// Namespace returns the policy namespace (if namespaced). An empty string is returned if the
	// policy is not namespaced.
	Namespace() string

	// SetAction sets the action on a copy of the underlying PolicyHit and returns it.
	SetAction(Action) PolicyHit

	// SetCount sets the count on a copy of the underlying PolicyHit and returns it.
	SetCount(int64) PolicyHit

	// SetIndex sets the index on a copy of the underlying PolicyHit and returns it.
	SetIndex(int) PolicyHit

	// Tier returns the tier name (or __PROFILE__ for profile match)
	Tier() string

	// ToFlowLogPolicyString returns a flow log policy string. Implementations of this must ensure that the value returned
	// from ToFlowLogPolicyString matches the input string passed to PolicyHitFromFlowLogPolicyString used to create
	// the PolicyHit (if it was used) exactly.
	ToFlowLogPolicyString() string

	// RuleIdIndex returns the rule id index pointer for this hit.
	RuleIdIndex() *int
}

// PolicyHitKey identifies a policy.
type policyHit struct {
	// The action for this policy hit.
	action Action

	// The document count.
	count int64

	// The index for this hit.
	index int

	// Whether or not this is a kubernetes policy.
	isKNP bool

	// Whether or not this is a profile.
	isProfile bool

	// Whether or not this is a staged policy.
	isStaged bool

	// The policy name. This is the raw policy name, and will not contain tier or knp prefixes.
	name string

	// The policy namespace (if namespaced).
	namespace string

	// The tier name (or __PROFILE__ for profile match)
	tier string

	// The pointer to a rule id index for this hit.
	ruleIdIndex *int
}

// Action returns the action for this policy hit. See AllActions() for a list of possible values
// that could be returned.
func (p policyHit) Action() Action {
	return p.action
}

// Count returns the number of flow logs that this policy hit was applied to.
func (p policyHit) Count() int64 {
	return p.count
}

// FlowLogName returns the name as it would appear in the flow log. This is unique for a specific
// policy instance.
// -  <tier>.<name>
// -  <namespace>/<tier>.<name>
// -  <namespace>/<tier>.staged:<name>
// -  <namespace>/knp.default.<name>
// -  <namespace>/staged:knp.default.<name>
// -  <namespace>/staged:knp.default.<name>
// -  __PROFILE__.kns.<namespace>
func (p policyHit) FlowLogName() string {
	name := p.name

	if p.isProfile {
		name = fmt.Sprintf("%s.%s.%s", p.tier, knsPrefix, name)
	} else if p.isKNP {
		name = fmt.Sprintf("%s.%s.%s", knpPrefix, p.tier, name)
		if p.isStaged {
			name = fmt.Sprintf("%s%s", model.PolicyNamePrefixStaged, name)
		}
	} else {
		if p.isStaged {
			name = fmt.Sprintf("%s%s", model.PolicyNamePrefixStaged, name)
		}
		name = fmt.Sprintf("%s.%s", p.tier, name)
	}

	if len(p.namespace) > 0 {
		name = fmt.Sprintf("%s/%s", p.namespace, name)
	}

	return name
}

// FullName returns the full policy name, which includes the tier prefix for calico policy or the
// "knp.default" prefix
// for Kubernetes policies.
func (p policyHit) FullName() string {
	if p.isProfile {
		return fmt.Sprintf("%s.%s.%s", p.tier, knsPrefix, p.name)
	} else if p.isKNP {
		return fmt.Sprintf("%s.%s.%s", knpPrefix, p.tier, p.name)
	}

	return fmt.Sprintf("%s.%s", p.tier, p.name)
}

// Index returns the index for this hit.
func (p policyHit) Index() int {
	return p.index
}

// IsKubernetes returns whether or not this policy is a staged policy.
func (p policyHit) IsKubernetes() bool {
	return p.isKNP
}

// IsProfile returns whether or not this policy is a profile.
func (p policyHit) IsProfile() bool {
	return p.isProfile
}

// IsStaged returns whether or not this policy is a staged policy.
func (p policyHit) IsStaged() bool {
	return p.isStaged
}

// Name returns the raw name of the policy without any tier or knp prefixes.
func (p policyHit) Name() string {
	return p.name
}

// Namespace returns the policy namespace (if namespaced). An empty string is returned if the
// policy is not namespaced.
func (p policyHit) Namespace() string {
	return p.namespace
}

// parseName parses the given full policy name (which includes tier, knp, or kns prefixes and may
// or may not contain the staged: pre / mid fix) and sets the appropriate policy hit fields
// (isKNP, isProfile...).
func (p *policyHit) parseName(name string) {
	// kubernetes network policies have the staged prefix before the tier name
	if strings.HasPrefix(name, model.PolicyNamePrefixStaged) {
		p.isStaged = true
		name = strings.TrimPrefix(name, model.PolicyNamePrefixStaged)
	}

	if strings.HasPrefix(name, fmt.Sprintf("%s.", knpPrefix)) {
		p.isKNP = true
		name = strings.TrimPrefix(name, fmt.Sprintf("%s.", knpPrefix))
	}

	if p.tier != "" {
		name = strings.TrimPrefix(name, fmt.Sprintf("%s.", p.tier))
	}

	if strings.HasPrefix(name, fmt.Sprintf("%s.", knsPrefix)) {
		p.isProfile = true
		name = strings.TrimPrefix(name, fmt.Sprintf("%s.", knsPrefix))
	}

	// calico network policies have the staged prefix after the tier name
	if strings.HasPrefix(name, model.PolicyNamePrefixStaged) {
		p.isStaged = true
		name = strings.TrimPrefix(name, model.PolicyNamePrefixStaged)
	}

	p.name = name
}

// SetAction sets the action on a copy of the underlying PolicyHit and returns it.
func (p policyHit) SetAction(action Action) PolicyHit {
	p.action = action
	return &p
}

// SetCount sets the count on a copy of the underlying PolicyHit and returns it.
func (p policyHit) SetCount(count int64) PolicyHit {
	p.count = count
	return &p
}

// SetIndex sets the index on a copy of the underlying PolicyHit and returns it.
func (p policyHit) SetIndex(index int) PolicyHit {
	p.index = index
	return &p
}

// Tier returns the tier name (or __PROFILE__ for profile match)
func (p policyHit) Tier() string {
	return p.tier
}

// RuleIdIndex returns the rule id index for this hit.
func (p policyHit) RuleIdIndex() *int {
	return p.ruleIdIndex
}

// ruleIdIndexString returns the rule id index as a string for this hit.
func (p policyHit) ruleIdIndexString() string {
	if p.ruleIdIndex != nil {
		return strconv.Itoa(*p.ruleIdIndex)
	}
	return "-"
}

// ToFlowLogPolicyString returns a flow log policy string. If PolicyHitFromFlowLogPolicyString was
// used to create the PolicyHit the return value of ToFlowLogPolicyString will exactly match the
// string given to PolicyHitFromFlowLogPolicyString.
func (p policyHit) ToFlowLogPolicyString() string {
	return fmt.Sprintf(
		"%d|%s|%s|%s|%s", p.index, p.tier, p.FlowLogName(), p.action, p.ruleIdIndexString(),
	)
}

// NewPolicyHit creates and returns a new PolicyHit. This will mainly be used for PIP, where we
// "generate" policy hit logsfor the user to see how their flows change with new policies.
func NewPolicyHit(
	action Action, count int64, index int, isStaged bool, name, namespace, tier string,
	ruleIdIndex *int,
) (PolicyHit, error) {
	if action == ActionInvalid {
		return nil, fmt.Errorf("a none empty Action must be provided")
	}
	if index < 0 {
		return nil, fmt.Errorf("index must be a positive integer")
	}
	if count < 0 {
		return nil, fmt.Errorf("count must be a positive integer")
	}
	if ruleIdIndex != nil && *ruleIdIndex != -1 && *ruleIdIndex < 0 {
		return nil, fmt.Errorf("rule id index must be a positive integer or -1")
	}

	p := &policyHit{
		action:      action,
		count:       count,
		index:       index,
		isStaged:    isStaged,
		tier:        tier,
		namespace:   namespace,
		ruleIdIndex: ruleIdIndex,
	}

	p.parseName(name)

	return p, nil
}

// PolicyHitFromFlowLogPolicyString creates a PolicyHit from a flow log policy string.
func PolicyHitFromFlowLogPolicyString(policyString string, count int64) (PolicyHit, error) {
	parts := strings.Split(policyString, "|")
	// Backward compatible to handle an old policy string, where the parts count is equal to
	// oldPolicyPartsCount==4.
	if len(parts) != newPolicyPartsCount && len(parts) != oldPolicyPartsCount {
		return nil,
			fmt.Errorf("invalid policy string '%s': pipe count must equal %d for a new or "+
				"%d for an old version of the policy string",
				policyString, newPolicyPartsCount, oldPolicyPartsCount)
	}

	p := &policyHit{
		count: count,
	}

	var err error
	p.index, err = strconv.Atoi(parts[policyStrIndexIdx])
	if err != nil {
		return nil, fmt.Errorf("invalid policy index: %w", err)
	}
	p.tier = parts[policyStrTierIdx]

	var name string
	nameParts := strings.SplitN(parts[policyStrNameIdx], "/", 2)
	if len(nameParts) == 2 {
		p.namespace = nameParts[0]
		name = nameParts[1]
	} else {
		name = nameParts[0]
	}

	p.parseName(name)

	p.action = ActionFromString(parts[policyStrActionIdx])
	if p.action == ActionInvalid {
		return nil, fmt.Errorf("invalid action '%s'", parts[policyStrActionIdx])
	}

	// If the rule id index string is '-', set the hit RuleIdIndex to nil.
	if len(parts) == newPolicyPartsCount && parts[policyStrRuleIdIndexIdx] != "-" {
		p.ruleIdIndex = new(int)
		if *p.ruleIdIndex, err = strconv.Atoi(parts[policyStrRuleIdIndexIdx]); err != nil {
			return nil, fmt.Errorf("invalid policy rule id index: %w", err)
		}
	}

	return p, nil
}

// SortablePolicyHits is a sortable slice of PolicyHits.
type SortablePolicyHits []PolicyHit

func (s SortablePolicyHits) Len() int { return len(s) }

func (s SortablePolicyHits) Less(i, j int) bool {
	if s[i].Index() != s[j].Index() {
		return s[i].Index() < s[j].Index()
	}
	if s[i].Namespace() != s[j].Namespace() {
		return s[i].Namespace() < s[j].Namespace()
	}
	if s[i].FullName() != s[j].FullName() {
		return s[i].FullName() < s[j].FullName()
	}
	if s[i].Action() != s[j].Action() {
		return s[i].Action() < s[j].Action()
	}
	if s[i].RuleIdIndex() == nil && s[j].RuleIdIndex() != nil {
		return true
	} else if s[i].RuleIdIndex() != nil && s[j].RuleIdIndex() == nil {
		return false
	} else if s[i].RuleIdIndex() != nil && s[j].RuleIdIndex() != nil &&
		*s[i].RuleIdIndex() != *s[j].RuleIdIndex() {
		return *s[i].RuleIdIndex() < *s[j].RuleIdIndex()
	}
	return s[i].IsStaged() && !s[j].IsStaged()
}

func (s SortablePolicyHits) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// SortAndRenumber sorts the PolicyHit slice and renumbers to be monotonically increasing.
func (s SortablePolicyHits) SortAndRenumber() {
	sort.Sort(s)
	for i := range s {
		s[i] = s[i].SetIndex(i)
	}
}

// PolicyHitsEqual compares two sets of PolicyHits to see if both order and values are identical.
func PolicyHitsEqual(p1, p2 []PolicyHit) bool {
	if len(p1) != len(p2) {
		return false
	}

	for i := range p1 {
		if !reflect.DeepEqual(p1[i], p2[i]) {
			return false
		}
	}
	return true
}

// ObfuscatedPolicyString creates the flow log policy string indicating an obfuscated policy.
func ObfuscatedPolicyString(matchIdx int, action Action) string {
	return fmt.Sprintf("%d|*|*|%s|*", matchIdx, action)
}
