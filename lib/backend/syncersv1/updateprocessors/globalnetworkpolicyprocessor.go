// Copyright (c) 2017-2018 Tigera, Inc. All rights reserved.

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

package updateprocessors

import (
	"errors"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/backend/watchersyncer"
	"github.com/projectcalico/libcalico-go/lib/names"
)

// Create a new SyncerUpdateProcessor to sync GlobalNetworkPolicy data in v1 format for
// consumption by Felix.
func NewGlobalNetworkPolicyUpdateProcessor() watchersyncer.SyncerUpdateProcessor {
	return NewSimpleUpdateProcessor(apiv3.KindGlobalNetworkPolicy, convertGlobalNetworkPolicyV2ToV1Key, convertGlobalNetworkPolicyV2ToV1Value)
}

func convertGlobalNetworkPolicyV2ToV1Key(v3key model.ResourceKey) (model.Key, error) {
	if v3key.Name == "" {
		return model.PolicyKey{}, errors.New("Missing Name field to create a v1 NetworkPolicy Key")
	}
	tier, err := names.TierFromPolicyName(v3key.Name)
	if err != nil {
		return model.PolicyKey{}, err
	}
	return model.PolicyKey{
		Name: v3key.Name,
		Tier: tier,
	}, nil

}

func convertGlobalNetworkPolicyV2ToV1Value(val interface{}) (interface{}, error) {
	v3res, ok := val.(*apiv3.GlobalNetworkPolicy)
	if !ok {
		return nil, errors.New("Value is not a valid GlobalNetworkPolicy resource value")
	}
	return convertGlobalPolicyV2ToV1Spec(v3res.Spec)
}

func convertGlobalPolicyV2ToV1Spec(spec apiv3.GlobalNetworkPolicySpec) (*model.Policy, error) {
	v1value := &model.Policy{
		Namespace:      "", // Empty string used to signal a GlobalNetworkPolicy.
		Order:          spec.Order,
		InboundRules:   RulesAPIV2ToBackend(spec.Ingress, "", false),
		OutboundRules:  RulesAPIV2ToBackend(spec.Egress, "", false),
		Selector:       spec.Selector,
		Types:          policyTypesAPIV2ToBackend(spec.Types),
		DoNotTrack:     spec.DoNotTrack,
		PreDNAT:        spec.PreDNAT,
		ApplyOnForward: spec.ApplyOnForward,
	}

	return v1value, nil
}
