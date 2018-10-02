// Copyright (c) 2018 Tigera, Inc. All rights reserved.
package api

type Endpoint interface {
	GetResource() Resource
	GetNode() string
	GetPolicyCounts() PolicyCounts
	IsProtected() bool
	IsLabelled() bool
}

type EndpointCounts struct {
	NumWorkloadEndpoints int
	NumHostEndpoints     int
}

type EndpointSummary struct {
	Total             int
	NumWithNoLabels   int
	NumWithNoPolicies int
}
