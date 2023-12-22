// Copyright (c) 2020 Tigera, Inc. All rights reserved.
package elastic

import (
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/lma/pkg/api"
)

const (
	// Indexes into the API flow data.
	FlowCompositeSourcesIdxSourceType      = 0
	FlowCompositeSourcesIdxSourceNamespace = 1
	FlowCompositeSourcesIdxSourceNameAggr  = 2
	FlowCompositeSourcesIdxDestType        = 3
	FlowCompositeSourcesIdxDestNamespace   = 4
	FlowCompositeSourcesIdxDestNameAggr    = 5
	FlowCompositeSourcesIdxReporter        = 6
	FlowCompositeSourcesIdxAction          = 7
	FlowCompositeSourcesIdxSourceAction    = 8
	FlowCompositeSourcesIdxImpacted        = 9 // This is a PIP specific parameter, but part of the API, so defined here.
	FlowCompositeSourcesNum                = 10
)

const (
	FlowAggregatedTermsNamePolicies = "policies"
)

var (
	FlowAggregatedTerms = []AggNestedTermInfo{
		{"policies", "policies", "by_tiered_policy", "policies.all_policies"},
		{"dest_labels", "dest_labels", "by_kvpair", "dest_labels.labels"},
		{"source_labels", "source_labels", "by_kvpair", "source_labels.labels"},
	}

	FlowAggregationSums = []AggSumInfo{
		{"sum_num_flows_started", "num_flows_started"},
		{"sum_num_flows_completed", "num_flows_completed"},
		{"sum_packets_in", "packets_in"},
		{"sum_bytes_in", "bytes_in"},
		{"sum_packets_out", "packets_out"},
		{"sum_bytes_out", "bytes_out"},
		{"sum_http_requests_allowed_in", "http_requests_allowed_in"},
		{"sum_http_requests_denied_in", "http_requests_denied_in"},
	}
)

// ---- Helper methods to convert the raw flow data into the flows.Flow data. ----

// GetFlowEndpointType extracts the flow endpoint type from the composite aggregation key.
func GetFlowEndpointTypeFromCompAggKey(k CompositeAggregationKey, idx int) api.EndpointType {
	return api.EndpointType(k[idx].String())
}

// GetFlowPolicies extracts the flow policies that were applied reporter-side from the composite aggregation key.
func GetFlowPoliciesFromAggTerm(t *AggregatedTerm) []api.PolicyHit {
	if t == nil {
		return nil
	}
	// Extract the policies from the raw data, protecting against multiple occurrences of the same policy with different
	// actions.
	var p []api.PolicyHit
	for k, v := range t.Buckets {
		if s, ok := k.(string); !ok {
			log.Errorf("aggregated term policy log is not a string: %#v", s)
			continue
		} else if h, err := api.PolicyHitFromFlowLogPolicyString(s, v); err == nil {
			p = append(p, h)
		} else {
			log.WithError(err).Errorf("failed to parse policy log '%s' as PolicyHit", s)
		}
	}
	return p
}

// EmptyToDash converts an empty string to a "-".
// Linseed returns fields such as namespaces as an empty string for global resources,
// whereas the UI expects a "-".
func EmptyToDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
