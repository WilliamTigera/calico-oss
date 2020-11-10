// Copyright (c) 2020 Tigera, Inc. All rights reserved.
package rbac

import (
	"github.com/tigera/lma/pkg/api"
	"github.com/tigera/lma/pkg/auth"
	"k8s.io/apiserver/pkg/authentication/user"

	log "github.com/sirupsen/logrus"

	authzv1 "k8s.io/api/authorization/v1"

	"github.com/projectcalico/libcalico-go/lib/resources"
)

var (
	// Grab all the resource helpers that we care about.
	podHelper  = resources.GetResourceHelperByTypeMeta(resources.TypeK8sPods)
	hepHelper  = resources.GetResourceHelperByTypeMeta(resources.TypeCalicoHostEndpoints)
	knpHelper  = resources.GetResourceHelperByTypeMeta(resources.TypeK8sNetworkPolicies)
	sknpHelper = resources.GetResourceHelperByTypeMeta(resources.TypeCalicoStagedKubernetesNetworkPolicies)
	gnpHelper  = resources.GetResourceHelperByTypeMeta(resources.TypeCalicoGlobalNetworkPolicies)
	sgnpHelper = resources.GetResourceHelperByTypeMeta(resources.TypeCalicoStagedGlobalNetworkPolicies)
	npHelper   = resources.GetResourceHelperByTypeMeta(resources.TypeCalicoNetworkPolicies)
	snpHelper  = resources.GetResourceHelperByTypeMeta(resources.TypeCalicoStagedNetworkPolicies)
	gnsHelper  = resources.GetResourceHelperByTypeMeta(resources.TypeCalicoGlobalNetworkSets)
	nsHelper   = resources.GetResourceHelperByTypeMeta(resources.TypeCalicoNetworkSets)
	tierHelper = resources.GetResourceHelperByTypeMeta(resources.TypeCalicoTiers)
)

// FlowHelper interface provides methods for consumers of Flows to perform RBAC checks on what the user should
// be able to see.
type FlowHelper interface {
	// Whether the namespace should be included as an option for flow requests.
	// pods or network sets can be listed in that namespace.
	IncludeNamespace(namespace string) (bool, error)

	// Whether the global (cluster) scoped should be included as an option for flow requests.
	IncludeGlobalNamespace() (bool, error)

	// Whether the user can list host endpoints
	CanListHostEndpoints() (bool, error)

	// Whether the user can list pods
	CanListPods(namespace string) (bool, error)

	// Whether the user can list global network sets
	CanListGlobalNetworkSets() (bool, error)

	// Whether the user can list network sets
	CanListNetworkSets(namespace string) (bool, error)

	// Whether the user can list the policy represented by the PolicyHit.
	CanListPolicy(p *api.PolicyHit) (bool, error)
}

func NewCachedFlowHelper(usr user.Info, authorizer auth.RBACAuthorizer) FlowHelper {
	return &flowHelper{
		usr:             usr,
		authorizer:      authorizer,
		authorizedCache: make(map[authzv1.ResourceAttributes]bool),
	}
}

// Whether the namespace should be included as an option for flow requests.
// pods or network sets can be listed in that namespace.
func (r flowHelper) IncludeNamespace(namespace string) (bool, error) {
	// Can the user list pods in this namespace, if so include the namespace.
	if canList, err := r.CanListPods(namespace); err != nil {
		return false, err
	} else if canList {
		log.Debug("User is able to list pods")
		return true, nil
	}

	// If they can't list pods, check network sets.
	if canList, err := r.CanListNetworkSets(namespace); err != nil {
		return false, err
	} else if canList {
		return true, nil
	}

	// If neither pods nor network sets can be listed then exclude the namespace.
	return false, nil
}

// Whether the global (cluster) scoped should be included as an option for flow requests.
func (r flowHelper) IncludeGlobalNamespace() (bool, error) {
	// Can the user list host endpoints in this namespace, if so include the namespace.
	if canList, err := r.CanListHostEndpoints(); err != nil {
		return false, err
	} else if canList {
		return true, nil
	}

	// If they can't list hot endpoints, check global network sets.
	if canList, err := r.CanListGlobalNetworkSets(); err != nil {
		return false, err
	} else if canList {
		return true, nil
	}

	// If neither host endpoints nor global network sets can be listed then exclude the global namespace.
	return false, nil
}

// CanListHostEndpoints implements the FlowHelper interface.
func (r flowHelper) CanListHostEndpoints() (bool, error) {
	return r.authorized(hepHelper, "list", "", "")
}

// CanListPods implements the FlowHelper interface.
func (r flowHelper) CanListPods(namespace string) (bool, error) {
	return r.authorized(podHelper, "list", namespace, "")
}

// CanListGlobalNetworkSets implements the FlowHelper interface.
func (r flowHelper) CanListGlobalNetworkSets() (bool, error) {
	return r.authorized(gnsHelper, "list", "", "")
}

// CanListNetworkSets implements the FlowHelper interface.
func (r flowHelper) CanListNetworkSets(namespace string) (bool, error) {
	return r.authorized(nsHelper, "list", namespace, "")
}

// flowHelper implements the FlowHelper interface.
type flowHelper struct {
	usr             user.Info
	authorizer      auth.RBACAuthorizer
	authorizedCache map[authzv1.ResourceAttributes]bool
}

// CanListPolicy determines if a policy can be listed.
func (r flowHelper) CanListPolicy(p *api.PolicyHit) (bool, error) {
	ns := p.Namespace
	switch p.Staged {
	case true:
		switch {
		case p.IsKubernetes():
			// Staged kubernetes policy. Ability to list this is just based on the namespace.
			log.Debug("Check staged kubernetes policy")
			return r.authorized(sknpHelper, "list", ns, "")
		case ns == "":
			// Staged Calico GlobalNetworkPolicy. Ability to list this is based on tier and namespace.
			log.Debug("Check staged global network policy")
			return r.canListTieredPolicy(sgnpHelper, p.Tier, "")
		default:
			// Staged Calico NetworkPolicy. Ability to list this is based on tier and namespace.
			log.Debug("Check staged network policy")
			return r.canListTieredPolicy(snpHelper, p.Tier, ns)
		}
	case false:
		switch {
		case p.Tier == "__PROFILE__":
			// Profile matches are always included.
			log.Debug("Profile match is always included")
			return true, nil
		case p.IsKubernetes():
			// Kubernetes policy. Ability to list this is just based on the namespace.
			log.Debug("Check kubernetes policy")
			return r.authorized(knpHelper, "list", ns, "")
		case ns == "":
			// Calico GlobalNetworkPolicy. Ability to list this is based on tier and namespace. Drop through to the
			// tiered policy processing.
			log.Debug("Check global network policy")
			return r.canListTieredPolicy(gnpHelper, p.Tier, "")
		default:
			// Calico NetworkPolicy. Ability to list this is based on tier and namespace.
			log.Debug("Check network policy")
			return r.canListTieredPolicy(npHelper, p.Tier, ns)
		}
	}

	return false, nil
}

// canListTieredPolicy determines if a Calico tiered policy can be listed.
func (r flowHelper) canListTieredPolicy(rh resources.ResourceHelper, tier, namespace string) (bool, error) {
	// This is a tiered policy type. First check the user can get the tier.
	if canGetTier, err := r.authorized(tierHelper, "get", "", tier); err != nil {
		return false, err
	} else if !canGetTier {
		return false, nil
	}

	// Check if the user can list the policy type in any tier.
	log.Debug("User can get tier, check ability to list all tiers")
	if canList, err := r.authorized(rh, "list", namespace, ""); err != nil {
		return false, err
	} else if canList {
		return true, nil
	}

	// If can't list across all tiers, check specific tier.
	log.Debug("User cannot list all tiers, check specific tier")
	if canList, err := r.authorized(rh, "list", namespace, tier+".*"); err != nil {
		return false, err
	} else if canList {
		return true, nil
	}

	return false, nil
}

// authorized determines if an action can be performed on a particular resource, and caches the result.
func (r flowHelper) authorized(rh resources.ResourceHelper, verb, namespace, name string) (bool, error) {
	ra := authzv1.ResourceAttributes{
		Namespace: namespace,
		Verb:      verb,
		Group:     rh.Group(),
		Resource:  rh.RbacPlural(),
		Name:      name,
	}

	if canDo, ok := r.authorizedCache[ra]; ok {
		log.Debugf("Using cached authorization for %v; authorized=%v", ra, canDo)
		return canDo, nil
	}

	// Check if the user is authorized to perform the action.
	log.Debugf("Checking if user action is authorized: %v", ra)
	status, err := r.authorizer.Authorize(r.usr, &ra, nil)
	if err != nil {
		log.WithError(err).Info("Unable to check permissions")
		return false, err
	}
	canDo := status == 200

	log.Debugf("Authorized=%v", canDo)
	r.authorizedCache[ra] = canDo
	return canDo, nil
}

type alwaysAllowAuthorizer struct{}

func (m *alwaysAllowAuthorizer) Authorize(usr user.Info, resources *authzv1.ResourceAttributes, nonResources *authzv1.NonResourceAttributes) (status int, err error) {
	return 200, nil
}

// NewAlwaysAllowFlowHelper returns an flow helper that always authorizes a request.
func NewAlwaysAllowFlowHelper() FlowHelper {
	return NewCachedFlowHelper(&user.DefaultInfo{Name: "Always Authenticated"}, &alwaysAllowAuthorizer{})
}
