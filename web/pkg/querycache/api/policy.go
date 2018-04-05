// Copyright (c) 2018 Tigera, Inc. All rights reserved.
package api

type Policy interface {
	GetResource() Resource
	GetTier() string
	GetEndpointCounts() EndpointCounts
	GetRuleEndpointCounts() Rule
	IsUnmatched() bool
}

type PolicyCounts struct {
	NumGlobalNetworkPolicies int
	NumNetworkPolicies       int
}

type Rule struct {
	Ingress []RuleDirection
	Egress  []RuleDirection
}

type RuleDirection struct {
	Source      EndpointCounts
	Destination EndpointCounts
}
