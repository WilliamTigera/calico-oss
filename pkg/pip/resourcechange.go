package pip

import (
	"encoding/json"
	"errors"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/resources"
)

// ResourceChange contains a single resource update that we are previewing.
type ResourceChange struct {
	Action   string             `json:"action"`
	Resource resources.Resource `json:"resource"`
}

// resourceChangeTrial is used to temporarily unmarshal the ResourceChange so that we can extract the TypeMeta from
// the resource definition.
type resourceChangeTrial struct {
	Resource metav1.TypeMeta `json:"resource"`
}

// Defined an alias for the ResourceChange so that we can json unmarshal it from the ResourceChange.UnmarshalJSON
// without causing recursion (since aliased types do not inherit methods).
type AliasedResourceChange *ResourceChange

// UnmarshalJSON allows unmarshalling of a ResourceChange from JSON bytes. This is required because the Resource
// field is an interface, and so it needs to be set with a concrete type before it can be unmarshalled.
func (c *ResourceChange) UnmarshalJSON(b []byte) error {
	// Unmarshal into the "trial" struct that allows us to easily extract the TypeMeta of the resource.
	var r resourceChangeTrial
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	c.Resource = resources.NewResource(r.Resource)
	if err := json.Unmarshal(b, AliasedResourceChange(c)); err != nil {
		return err
	}

	// If this is a Calico tiered network policy, configure an empty tier to be default and verify the name matches
	// the tier.
	var tier *string // Use a pointer so that we can set the defalt value in the policy resource if necessary.
	var name string
	switch np := c.Resource.(type) {
	case *v3.NetworkPolicy:
		tier = &np.Spec.Tier
		name = np.Name
	case *v3.GlobalNetworkPolicy:
		tier = &np.Spec.Tier
		name = np.Name
	default:
		// Not a calico tiered policy, so just exit now, no need to do the extra checks.
		return nil
	}

	// Calico tiered policy. The tier in the spec should also be the prefix of the policy name.
	if *tier == "" {
		// The tier is not set, so set it to be default.
		*tier = "default"
	}
	if !strings.HasPrefix(name, *tier+".") {
		return errors.New("policy name '" + name + "' is not correct for the configured tier '" + *tier + "'")
	}
	return nil
}
