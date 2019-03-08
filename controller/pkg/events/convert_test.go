package events

import (
	"testing"

	"github.com/olivere/elastic"
	. "github.com/onsi/gomega"

	"github.com/tigera/intrusion-detection/controller/pkg/util"
)

func TestConvertFlowLogSourceIP(t *testing.T) {
	g := NewGomegaWithT(t)

	hit := &elastic.SearchHit{
		Index: "test_flows_index",
		Id:    "111-222-333",
	}
	tc := FlowLogJSONOutput{
		StartTime:       123,
		EndTime:         456,
		SourceIP:        util.Sptr("1.2.3.4"),
		SourceName:      "source-foo",
		SourceNameAggr:  "source",
		SourceNamespace: "test",
		SourcePort:      util.I64ptr(443),
		SourceType:      "pod",
		SourceLabels: &FlowLogLabelsJSONOutput{
			Labels: []string{"source-label"},
		},
		DestIP:        util.Sptr("2.3.4.5"),
		DestName:      "dest-foo",
		DestNameAggr:  "dest",
		DestNamespace: "internet",
		DestPort:      util.I64ptr(80),
		DestType:      "public",
		DestLabels: &FlowLogLabelsJSONOutput{
			Labels: []string{"dest-label"},
		},
		Proto:    "tcp",
		Action:   "allow",
		Reporter: "felix",
		Policies: &FlowLogPoliciesJSONOutput{
			AllPolicies: []string{"a policy"},
		},
		BytesIn:               1,
		BytesOut:              2,
		NumFlows:              3,
		NumFlowsStarted:       4,
		NumFlowsCompleted:     5,
		PacketsIn:             6,
		PacketsOut:            7,
		HTTPRequestsAllowedIn: 8,
		HTTPRequestsDeniedIn:  9,
	}
	expected := SecurityEvent{
		Time:             123,
		Type:             SuspiciousFlow,
		Description:      "Suspicious IP 1.2.3.4 from list testfeed connected to pod test/source-foo",
		Severity:         Severity,
		FlowLogIndex:     "test_flows_index",
		FlowLogID:        "111-222-333",
		Protocol:         "tcp",
		SourceIP:         util.Sptr("1.2.3.4"),
		SourcePort:       util.I64ptr(443),
		SourceNamespace:  "test",
		SourceName:       "source-foo",
		DestIP:           util.Sptr("2.3.4.5"),
		DestPort:         util.I64ptr(80),
		DestNamespace:    "internet",
		DestName:         "dest-foo",
		FlowAction:       "allow",
		Feeds:            []string{"testfeed"},
		SuspiciousPrefix: nil,
	}

	actual := ConvertFlowLog(tc, "source_ip", hit, expected.Feeds...)

	g.Expect(actual).Should(Equal(expected), "Generated SecurityEvent matches expectations")
	g.Expect(actual.ID()).Should(Equal("123-tcp-1.2.3.4-443-2.3.4.5-80"))
}

func TestConvertFlowLogDestIP(t *testing.T) {
	g := NewGomegaWithT(t)

	hit := &elastic.SearchHit{
		Index: "test_flows_index",
		Id:    "111-222-333",
	}
	tc := FlowLogJSONOutput{
		StartTime:       123,
		EndTime:         456,
		SourceIP:        util.Sptr("1.2.3.4"),
		SourceName:      "source-foo",
		SourceNameAggr:  "source",
		SourceNamespace: "test",
		SourcePort:      util.I64ptr(443),
		SourceType:      "pod",
		SourceLabels: &FlowLogLabelsJSONOutput{
			Labels: []string{"source-label"},
		},
		DestIP:        util.Sptr("2.3.4.5"),
		DestName:      "dest-foo",
		DestNameAggr:  "dest",
		DestNamespace: "internet",
		DestPort:      util.I64ptr(80),
		DestType:      "public",
		DestLabels: &FlowLogLabelsJSONOutput{
			Labels: []string{"dest-label"},
		},
		Proto:    "tcp",
		Action:   "allow",
		Reporter: "felix",
		Policies: &FlowLogPoliciesJSONOutput{
			AllPolicies: []string{"a policy"},
		},
		BytesIn:               1,
		BytesOut:              2,
		NumFlows:              3,
		NumFlowsStarted:       4,
		NumFlowsCompleted:     5,
		PacketsIn:             6,
		PacketsOut:            7,
		HTTPRequestsAllowedIn: 8,
		HTTPRequestsDeniedIn:  9,
	}
	expected := SecurityEvent{
		Time:             123,
		Type:             SuspiciousFlow,
		Description:      "Pod internet/dest-foo connected to suspicious IP 2.3.4.5 from list testfeed",
		Severity:         Severity,
		FlowLogIndex:     "test_flows_index",
		FlowLogID:        "111-222-333",
		Protocol:         "tcp",
		SourceIP:         util.Sptr("1.2.3.4"),
		SourcePort:       util.I64ptr(443),
		SourceNamespace:  "test",
		SourceName:       "source-foo",
		DestIP:           util.Sptr("2.3.4.5"),
		DestPort:         util.I64ptr(80),
		DestNamespace:    "internet",
		DestName:         "dest-foo",
		FlowAction:       "allow",
		Feeds:            []string{"testfeed"},
		SuspiciousPrefix: nil,
	}

	actual := ConvertFlowLog(tc, "dest_ip", hit, expected.Feeds...)

	g.Expect(actual).Should(Equal(expected), "Generated SecurityEvent matches expectations")
	g.Expect(actual.ID()).Should(Equal("123-tcp-1.2.3.4-443-2.3.4.5-80"))
}

func TestConvertFlowLogUnknown(t *testing.T) {
	g := NewGomegaWithT(t)

	hit := &elastic.SearchHit{
		Index: "test_flows_index",
		Id:    "111-222-333",
	}
	tc := FlowLogJSONOutput{
		StartTime:       123,
		EndTime:         456,
		SourceIP:        util.Sptr("1.2.3.4"),
		SourceName:      "source-foo",
		SourceNameAggr:  "source",
		SourceNamespace: "test",
		SourcePort:      util.I64ptr(443),
		SourceType:      "pod",
		SourceLabels: &FlowLogLabelsJSONOutput{
			Labels: []string{"source-label"},
		},
		DestIP:        util.Sptr("2.3.4.5"),
		DestName:      "dest-foo",
		DestNameAggr:  "dest",
		DestNamespace: "internet",
		DestPort:      util.I64ptr(80),
		DestType:      "public",
		DestLabels: &FlowLogLabelsJSONOutput{
			Labels: []string{"dest-label"},
		},
		Proto:    "tcp",
		Action:   "allow",
		Reporter: "felix",
		Policies: &FlowLogPoliciesJSONOutput{
			AllPolicies: []string{"a policy"},
		},
		BytesIn:               1,
		BytesOut:              2,
		NumFlows:              3,
		NumFlowsStarted:       4,
		NumFlowsCompleted:     5,
		PacketsIn:             6,
		PacketsOut:            7,
		HTTPRequestsAllowedIn: 8,
		HTTPRequestsDeniedIn:  9,
	}
	expected := SecurityEvent{
		Time:             123,
		Type:             SuspiciousFlow,
		Description:      "1.2.3.4 connected to 2.3.4.5",
		Severity:         Severity,
		FlowLogIndex:     "test_flows_index",
		FlowLogID:        "111-222-333",
		Protocol:         "tcp",
		SourceIP:         util.Sptr("1.2.3.4"),
		SourcePort:       util.I64ptr(443),
		SourceNamespace:  "test",
		SourceName:       "source-foo",
		DestIP:           util.Sptr("2.3.4.5"),
		DestPort:         util.I64ptr(80),
		DestNamespace:    "internet",
		DestName:         "dest-foo",
		FlowAction:       "allow",
		Feeds:            []string{"testfeed"},
		SuspiciousPrefix: nil,
	}

	actual := ConvertFlowLog(tc, "unknown", hit, expected.Feeds...)

	g.Expect(actual).Should(Equal(expected), "Generated SecurityEvent matches expectations")
	g.Expect(actual.ID()).Should(Equal("123-tcp-1.2.3.4-443-2.3.4.5-80"))
}
