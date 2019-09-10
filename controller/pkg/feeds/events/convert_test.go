// Copyright 2019 Tigera Inc. All rights reserved.

package events

import (
	"github.com/tigera/intrusion-detection/controller/pkg/db"
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
		SourceNamespace: "mock",
		SourcePort:      util.I64ptr(443),
		SourceType:      "wep",
		SourceLabels: &FlowLogLabelsJSONOutput{
			Labels: []string{"source-label"},
		},
		DestIP:        util.Sptr("2.3.4.5"),
		DestName:      "dest-foo",
		DestNameAggr:  "dest",
		DestNamespace: "internet",
		DestPort:      util.I64ptr(80),
		DestType:      "net",
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
	expected := SuspiciousIPSecurityEvent{
		Time:             123,
		Type:             SuspiciousFlow,
		Description:      "suspicious IP 1.2.3.4 from list testfeed connected to net internet/dest-foo",
		Severity:         Severity,
		FlowLogIndex:     "test_flows_index",
		FlowLogID:        "111-222-333",
		Protocol:         "tcp",
		SourceIP:         util.Sptr("1.2.3.4"),
		SourcePort:       util.I64ptr(443),
		SourceNamespace:  "mock",
		SourceName:       "source-foo",
		DestIP:           util.Sptr("2.3.4.5"),
		DestPort:         util.I64ptr(80),
		DestNamespace:    "internet",
		DestName:         "dest-foo",
		FlowAction:       "allow",
		Feeds:            []string{"testfeed"},
		SuspiciousPrefix: nil,
	}

	actual := ConvertFlowLog(tc, db.QueryKeyFlowLogSourceIP, hit, expected.Feeds...)

	g.Expect(actual).Should(Equal(expected), "Generated SecurityEvent matches expectations")
	g.Expect(actual.ID()).Should(Equal("testfeed_123_tcp_1.2.3.4_443_2.3.4.5_80"))
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
		SourceNamespace: "mock",
		SourcePort:      util.I64ptr(443),
		SourceType:      "wep",
		SourceLabels: &FlowLogLabelsJSONOutput{
			Labels: []string{"source-label"},
		},
		DestIP:        util.Sptr("2.3.4.5"),
		DestName:      "dest-foo",
		DestNameAggr:  "dest",
		DestNamespace: "internet",
		DestPort:      util.I64ptr(80),
		DestType:      "net",
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
	expected := SuspiciousIPSecurityEvent{
		Time:             123,
		Type:             SuspiciousFlow,
		Description:      "wep mock/source-foo connected to suspicious IP 2.3.4.5 from list testfeed",
		Severity:         Severity,
		FlowLogIndex:     "test_flows_index",
		FlowLogID:        "111-222-333",
		Protocol:         "tcp",
		SourceIP:         util.Sptr("1.2.3.4"),
		SourcePort:       util.I64ptr(443),
		SourceNamespace:  "mock",
		SourceName:       "source-foo",
		DestIP:           util.Sptr("2.3.4.5"),
		DestPort:         util.I64ptr(80),
		DestNamespace:    "internet",
		DestName:         "dest-foo",
		FlowAction:       "allow",
		Feeds:            []string{"testfeed"},
		SuspiciousPrefix: nil,
	}

	actual := ConvertFlowLog(tc, db.QueryKeyFlowLogDestIP, hit, expected.Feeds...)

	g.Expect(actual).Should(Equal(expected), "Generated SecurityEvent matches expectations")
	g.Expect(actual.ID()).Should(Equal("testfeed_123_tcp_1.2.3.4_443_2.3.4.5_80"))
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
		SourceNamespace: "mock",
		SourcePort:      util.I64ptr(443),
		SourceType:      "hep",
		SourceLabels: &FlowLogLabelsJSONOutput{
			Labels: []string{"source-label"},
		},
		DestIP:        util.Sptr("2.3.4.5"),
		DestName:      "dest-foo",
		DestNameAggr:  "dest",
		DestNamespace: "internet",
		DestPort:      util.I64ptr(80),
		DestType:      "ns",
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
	expected := SuspiciousIPSecurityEvent{
		Time:             123,
		Type:             SuspiciousFlow,
		Description:      "hep 1.2.3.4 connected to ns 2.3.4.5",
		Severity:         Severity,
		FlowLogIndex:     "test_flows_index",
		FlowLogID:        "111-222-333",
		Protocol:         "tcp",
		SourceIP:         util.Sptr("1.2.3.4"),
		SourcePort:       util.I64ptr(443),
		SourceNamespace:  "mock",
		SourceName:       "source-foo",
		DestIP:           util.Sptr("2.3.4.5"),
		DestPort:         util.I64ptr(80),
		DestNamespace:    "internet",
		DestName:         "dest-foo",
		FlowAction:       "allow",
		Feeds:            []string{"testfeed"},
		SuspiciousPrefix: nil,
	}

	actual := ConvertFlowLog(tc, db.QueryKeyUnknown, hit, expected.Feeds...)

	g.Expect(actual).Should(Equal(expected), "Generated SecurityEvent matches expectations")
	g.Expect(actual.ID()).Should(Equal("testfeed_123_tcp_1.2.3.4_443_2.3.4.5_80"))
}

func TestConvertDNSLog_QName(t *testing.T) {
	g := NewGomegaWithT(t)

	hit := &elastic.SearchHit{
		Index: "test_dns_index",
		Id:    "111-222-333",
	}
	tc := DNSLog{
		StartTime:       1,
		EndTime:         5,
		Count:           1,
		ClientName:      "client-8888-34",
		ClientNameAggr:  "client-8888-*",
		ClientNamespace: "default",
		ClientIP:        util.Sptr("20.21.22.23"),
		ClientLabels:    map[string]string{"foo": "bar"},
		Servers: []DNSServer{
			{
				Name:      "coredns-111111",
				NameAggr:  "coredns-*",
				Namespace: "kube-system",
				IP:        "50.60.70.80",
			},
		},
		QName:  "www.badguys.co.uk",
		QClass: "IN",
		QType:  "A",
		RCode:  "NoError",
		RRSets: []DNSRRSet{
			{
				Name:  "www.badguys.co.uk",
				Class: "IN",
				Type:  "A",
				RData: []string{"100.200.1.1"},
			},
		},
	}
	expected := SuspiciousDomainSecurityEvent{
		Time:              1,
		Type:              SuspiciousDNSQuery,
		Description:       "default/client-8888-34 queried the domain name www.badguys.co.uk from global threat feed(s) test-feed",
		Severity:          Severity,
		DNSLogIndex:       hit.Index,
		DNSLogID:          hit.Id,
		SourceIP:          util.Sptr("20.21.22.23"),
		SourceNamespace:   "default",
		SourceName:        "client-8888-34",
		Feeds:             []string{"test-feed"},
		SuspiciousDomains: []string{"www.badguys.co.uk"},
	}
	actual := ConvertDNSLog(tc, db.QueryKeyDNSLogQName, hit, map[string]struct{}{}, "test-feed")
	g.Expect(actual).To(Equal(expected))
	g.Expect(actual.ID()).To(Equal("test-feed_1_20.21.22.23_www.badguys.co.uk"))
}

func TestConvertDNSLog_RRSetName(t *testing.T) {
	g := NewGomegaWithT(t)

	hit := &elastic.SearchHit{
		Index: "test_dns_index",
		Id:    "111-222-333",
	}
	tc := DNSLog{
		StartTime:       1,
		EndTime:         5,
		Count:           1,
		ClientName:      "-",
		ClientNameAggr:  "client-8888-*",
		ClientNamespace: "default",
		ClientIP:        util.Sptr("20.21.22.23"),
		ClientLabels:    map[string]string{"foo": "bar"},
		Servers: []DNSServer{
			{
				Name:      "coredns-111111",
				NameAggr:  "coredns-*",
				Namespace: "kube-system",
				IP:        "50.60.70.80",
			},
		},
		QName:  "www.badguys.co.uk",
		QClass: "IN",
		QType:  "A",
		RCode:  "NoError",
		RRSets: []DNSRRSet{
			{
				Name:  "www.badguys.co.uk",
				Class: "IN",
				Type:  "CNAME",
				RData: []string{"www1.badguys-backend.co.uk"},
			},
			{
				Name:  "www1.badguys-backend.co.uk",
				Class: "IN",
				Type:  "A",
				RData: []string{"233.1.44.55", "233.1.32.1"},
			},
		},
	}
	expected := SuspiciousDomainSecurityEvent{
		Time:              1,
		Type:              SuspiciousDNSQuery,
		Description:       "default/client-8888-* got DNS query results including suspicious domain(s) www1.badguys-backend.co.uk from global threat feed(s) test-feed, my-feed",
		Severity:          Severity,
		DNSLogIndex:       hit.Index,
		DNSLogID:          hit.Id,
		SourceIP:          util.Sptr("20.21.22.23"),
		SourceNamespace:   "default",
		SourceName:        "client-8888-*",
		Feeds:             []string{"test-feed", "my-feed"},
		SuspiciousDomains: []string{"www1.badguys-backend.co.uk"},
	}
	domains := map[string]struct{}{
		"www1.badguys-backend.co.uk": {},
	}
	actual := ConvertDNSLog(tc, db.QueryKeyDNSLogRRSetsName, hit, domains, "test-feed", "my-feed")
	g.Expect(actual).To(Equal(expected))
	g.Expect(actual.ID()).To(Equal("test-feed~my-feed_1_20.21.22.23_www1.badguys-backend.co.uk"))

	// Multiple matched domains
	expected.Description = "default/client-8888-* got DNS query results including suspicious domain(s) www.badguys.co.uk, www1.badguys-backend.co.uk from global threat feed(s) test-feed, my-feed"
	expected.SuspiciousDomains = []string{"www.badguys.co.uk", "www1.badguys-backend.co.uk"}
	domains["www.badguys.co.uk"] = struct{}{}
	actual = ConvertDNSLog(tc, db.QueryKeyDNSLogRRSetsName, hit, domains, "test-feed", "my-feed")
	g.Expect(actual).To(Equal(expected))
	g.Expect(actual.ID()).To(Equal("test-feed~my-feed_1_20.21.22.23_www.badguys.co.uk~www1.badguys-backend.co.uk"))

	// No matched domains
	expected.Description = "default/client-8888-* got DNS query results including suspicious domain(s)  from global threat feed(s) test-feed, my-feed"
	expected.SuspiciousDomains = nil
	actual = ConvertDNSLog(tc, db.QueryKeyDNSLogRRSetsName, hit, map[string]struct{}{}, "test-feed", "my-feed")
	g.Expect(actual).To(Equal(expected))
}

func TestConvertDNSLog_RRSetRData(t *testing.T) {
	g := NewGomegaWithT(t)

	hit := &elastic.SearchHit{
		Index: "test_dns_index",
		Id:    "111-222-333",
	}
	tc := DNSLog{
		StartTime:       1,
		EndTime:         5,
		Count:           1,
		ClientName:      "-",
		ClientNameAggr:  "client-8888-*",
		ClientNamespace: "default",
		ClientIP:        util.Sptr("20.21.22.23"),
		ClientLabels:    map[string]string{"foo": "bar"},
		Servers: []DNSServer{
			{
				Name:      "coredns-111111",
				NameAggr:  "coredns-*",
				Namespace: "kube-system",
				IP:        "50.60.70.80",
			},
		},
		QName:  "www.badguys.co.uk",
		QClass: "IN",
		QType:  "CNAME",
		RCode:  "NoError",
		RRSets: []DNSRRSet{
			{
				Name:  "www.badguys.co.uk",
				Class: "IN",
				Type:  "CNAME",
				RData: []string{"www1.badguys-backend.co.uk"},
			},
			{
				Name:  "www1.badguys-backend.co.uk",
				Class: "IN",
				Type:  "CNAME",
				RData: []string{"uef0.malh0st.io"},
			},
		},
	}
	expected := SuspiciousDomainSecurityEvent{
		Time:              1,
		Type:              SuspiciousDNSQuery,
		Description:       "default/client-8888-* got DNS query results including suspicious domain(s) uef0.malh0st.io from global threat feed(s) test-feed",
		Severity:          Severity,
		DNSLogIndex:       hit.Index,
		DNSLogID:          hit.Id,
		SourceIP:          util.Sptr("20.21.22.23"),
		SourceNamespace:   "default",
		SourceName:        "client-8888-*",
		Feeds:             []string{"test-feed"},
		SuspiciousDomains: []string{"uef0.malh0st.io"},
	}
	domains := map[string]struct{}{
		"uef0.malh0st.io": {},
	}
	actual := ConvertDNSLog(tc, db.QueryKeyDNSLogRRSetsRData, hit, domains, "test-feed")
	g.Expect(actual).To(Equal(expected))

	// Multiple matched domains
	expected.Description = "default/client-8888-* got DNS query results including suspicious domain(s) www1.badguys-backend.co.uk, uef0.malh0st.io from global threat feed(s) test-feed"
	expected.SuspiciousDomains = []string{"www1.badguys-backend.co.uk", "uef0.malh0st.io"}
	domains["www1.badguys-backend.co.uk"] = struct{}{}
	actual = ConvertDNSLog(tc, db.QueryKeyDNSLogRRSetsRData, hit, domains, "test-feed")
	g.Expect(actual).To(Equal(expected))

	// No matched domains
	expected.Description = "default/client-8888-* got DNS query results including suspicious domain(s)  from global threat feed(s) test-feed"
	expected.SuspiciousDomains = nil
	actual = ConvertDNSLog(tc, db.QueryKeyDNSLogRRSetsRData, hit, map[string]struct{}{}, "test-feed")
	g.Expect(actual).To(Equal(expected))
}
