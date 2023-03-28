// Copyright 2019 Tigera Inc. All rights reserved.

package events

import (
	"testing"
	"time"

	"github.com/google/gopacket/layers"

	. "github.com/onsi/gomega"

	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/storage"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/util"

	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
)

func TestConvertFlowLogSourceIP(t *testing.T) {
	g := NewGomegaWithT(t)

	tc := v1.FlowLog{
		ID:              "111-222-333",
		StartTime:       123,
		EndTime:         456,
		SourceIP:        util.Sptr("1.2.3.4"),
		SourceName:      "source-foo",
		SourceNameAggr:  "source",
		SourceNamespace: "mock",
		SourcePort:      util.I64ptr(443),
		SourceType:      "wep",
		SourceLabels: &v1.FlowLogLabels{
			Labels: []string{"source-label"},
		},
		DestIP:        util.Sptr("2.3.4.5"),
		DestName:      "dest-foo",
		DestNameAggr:  "dest",
		DestNamespace: "internet",
		DestPort:      util.I64ptr(80),
		DestType:      "net",
		DestLabels: &v1.FlowLogLabels{
			Labels: []string{"dest-label"},
		},
		Protocol: "tcp",
		Action:   "allow",
		Reporter: "felix",
		Policies: &v1.FlowLogPolicy{
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
	record := v1.SuspiciousIPEventRecord{
		FlowAction:       "allow",
		FlowLogID:        "111-222-333",
		Protocol:         "tcp",
		Feeds:            []string{"testfeed"},
		SuspiciousPrefix: nil,
	}
	expected := v1.Event{
		ID:              "testfeed_123_tcp_1.2.3.4_443_2.3.4.5_80",
		Time:            123,
		Type:            SuspiciousFlow,
		Description:     "suspicious IP 1.2.3.4 from list testfeed connected to net internet/dest-foo",
		Severity:        Severity,
		Origin:          "testfeed",
		SourceIP:        util.Sptr("1.2.3.4"),
		SourcePort:      util.I64ptr(443),
		SourceNamespace: "mock",
		SourceName:      "source-foo",
		SourceNameAggr:  "source",
		DestIP:          util.Sptr("2.3.4.5"),
		DestPort:        util.I64ptr(80),
		DestNamespace:   "internet",
		DestName:        "dest-foo",
		DestNameAggr:    "dest",
		Record:          record,
	}

	actual := ConvertFlowLog(tc, storage.QueryKeyFlowLogSourceIP, record.Feeds...)

	g.Expect(actual).Should(Equal(expected), "Generated SecurityEvent matches expectations")
}

func TestConvertFlowLogDestIP(t *testing.T) {
	g := NewGomegaWithT(t)

	tc := v1.FlowLog{
		ID:              "111-222-333",
		StartTime:       123,
		EndTime:         456,
		SourceIP:        util.Sptr("1.2.3.4"),
		SourceName:      "source-foo",
		SourceNameAggr:  "source",
		SourceNamespace: "mock",
		SourcePort:      util.I64ptr(443),
		SourceType:      "wep",
		SourceLabels: &v1.FlowLogLabels{
			Labels: []string{"source-label"},
		},
		DestIP:        util.Sptr("2.3.4.5"),
		DestName:      "dest-foo",
		DestNameAggr:  "dest",
		DestNamespace: "internet",
		DestPort:      util.I64ptr(80),
		DestType:      "net",
		DestLabels: &v1.FlowLogLabels{
			Labels: []string{"dest-label"},
		},
		Protocol: "tcp",
		Action:   "allow",
		Reporter: "felix",
		Policies: &v1.FlowLogPolicy{
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
	record := v1.SuspiciousIPEventRecord{
		FlowAction:       "allow",
		FlowLogID:        "111-222-333",
		Protocol:         "tcp",
		Feeds:            []string{"testfeed"},
		SuspiciousPrefix: nil,
	}
	expected := v1.Event{
		ID:              "testfeed_123_tcp_1.2.3.4_443_2.3.4.5_80",
		Time:            123,
		Type:            SuspiciousFlow,
		Description:     "wep mock/source-foo connected to suspicious IP 2.3.4.5 from list testfeed",
		Severity:        Severity,
		Origin:          "testfeed",
		SourceIP:        util.Sptr("1.2.3.4"),
		SourcePort:      util.I64ptr(443),
		SourceNamespace: "mock",
		SourceName:      "source-foo",
		SourceNameAggr:  "source",
		DestIP:          util.Sptr("2.3.4.5"),
		DestPort:        util.I64ptr(80),
		DestNamespace:   "internet",
		DestName:        "dest-foo",
		DestNameAggr:    "dest",
		Record:          record,
	}

	actual := ConvertFlowLog(tc, storage.QueryKeyFlowLogDestIP, record.Feeds...)

	g.Expect(actual).Should(Equal(expected), "Generated SecurityEvent matches expectations")
}

func TestConvertFlowLogUnknown(t *testing.T) {
	g := NewGomegaWithT(t)

	tc := v1.FlowLog{
		ID:              "111-222-333",
		StartTime:       123,
		EndTime:         456,
		SourceIP:        util.Sptr("1.2.3.4"),
		SourceName:      "source-foo",
		SourceNameAggr:  "source",
		SourceNamespace: "mock",
		SourcePort:      util.I64ptr(443),
		SourceType:      "hep",
		SourceLabels: &v1.FlowLogLabels{
			Labels: []string{"source-label"},
		},
		DestIP:        util.Sptr("2.3.4.5"),
		DestName:      "dest-foo",
		DestNameAggr:  "dest",
		DestNamespace: "internet",
		DestPort:      util.I64ptr(80),
		DestType:      "ns",
		DestLabels: &v1.FlowLogLabels{
			Labels: []string{"dest-label"},
		},
		Protocol: "tcp",
		Action:   "allow",
		Reporter: "felix",
		Policies: &v1.FlowLogPolicy{
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
	record := v1.SuspiciousIPEventRecord{
		FlowAction:       "allow",
		FlowLogID:        "111-222-333",
		Protocol:         "tcp",
		Feeds:            []string{"testfeed"},
		SuspiciousPrefix: nil,
	}
	expected := v1.Event{
		ID:              "testfeed_123_tcp_1.2.3.4_443_2.3.4.5_80",
		Time:            123,
		Type:            SuspiciousFlow,
		Description:     "hep 1.2.3.4 connected to ns 2.3.4.5",
		Severity:        Severity,
		Origin:          "testfeed",
		SourceIP:        util.Sptr("1.2.3.4"),
		SourcePort:      util.I64ptr(443),
		SourceNamespace: "mock",
		SourceName:      "source-foo",
		SourceNameAggr:  "source",
		DestIP:          util.Sptr("2.3.4.5"),
		DestPort:        util.I64ptr(80),
		DestNamespace:   "internet",
		DestName:        "dest-foo",
		DestNameAggr:    "dest",
		Record:          record,
	}

	actual := ConvertFlowLog(tc, storage.QueryKeyUnknown, record.Feeds...)

	g.Expect(actual).Should(Equal(expected), "Generated SecurityEvent matches expectations")
}

func TestConvertDNSLog_QName(t *testing.T) {
	g := NewGomegaWithT(t)

	tc := v1.DNSLog{
		ID:              "111-222-333",
		StartTime:       time.Unix(1, 0),
		EndTime:         time.Unix(5, 0),
		Count:           1,
		ClientName:      "client-8888-34",
		ClientNameAggr:  "client-8888-*",
		ClientNamespace: "default",
		ClientIP:        util.IPPtr("20.21.22.23"),
		ClientLabels:    map[string]string{"foo": "bar"},
		Servers: []v1.DNSServer{
			{
				Endpoint: v1.Endpoint{
					Name:           "coredns-111111",
					AggregatedName: "coredns-*",
					Namespace:      "kube-system",
				},
				IP: *util.IPPtr("50.60.70.80"),
			},
		},
		QName:  "www.badguys.co.uk",
		QClass: v1.DNSClass(layers.DNSClassIN),
		QType:  v1.DNSType(layers.DNSTypeA),
		RCode:  v1.DNSResponseCode(layers.DNSResponseCodeNoErr),
		RRSets: v1.DNSRRSets{
			v1.DNSName{
				Name:  "www.badguys.co.uk",
				Class: v1.DNSClass(layers.DNSClassIN),
				Type:  v1.DNSType(layers.DNSTypeA),
			}: []v1.DNSRData{
				{Raw: []byte("100.200.1.1")},
			},
		},
	}
	expected := v1.Event{
		ID:              "test-feed_1_20.21.22.23_www.badguys.co.uk",
		Time:            1,
		Type:            SuspiciousDNSQuery,
		Description:     "default/client-8888-34 queried the domain name www.badguys.co.uk from global threat feed(s) test-feed",
		Severity:        Severity,
		Origin:          "test-feed",
		SourceIP:        util.Sptr("20.21.22.23"),
		SourceNamespace: "default",
		SourceName:      "client-8888-34",
		SourceNameAggr:  "client-8888-*",
		Host:            "",
		Record: v1.SuspiciousDomainEventRecord{
			DNSLogID:          "111-222-333",
			Feeds:             []string{"test-feed"},
			SuspiciousDomains: []string{"www.badguys.co.uk"},
		},
	}
	actual := ConvertDNSLog(tc, storage.QueryKeyDNSLogQName, map[string]struct{}{"www.badguys.co.uk": {}}, "test-feed")
	g.Expect(actual).To(Equal(expected))
}

func TestConvertDNSLog_RRSetName(t *testing.T) {
	g := NewGomegaWithT(t)

	tc := v1.DNSLog{
		ID:              "111-222-333",
		StartTime:       time.Unix(1, 0),
		EndTime:         time.Unix(5, 0),
		Count:           1,
		ClientName:      "-",
		ClientNameAggr:  "client-8888-*",
		ClientNamespace: "default",
		ClientIP:        util.IPPtr("20.21.22.23"),
		ClientLabels:    map[string]string{"foo": "bar"},
		Servers: []v1.DNSServer{
			{
				Endpoint: v1.Endpoint{
					Name:           "coredns-111111",
					AggregatedName: "coredns-*",
					Namespace:      "kube-system",
				},
				IP: *util.IPPtr("50.60.70.80"),
			},
		},
		QName:  "www.badguys.co.uk",
		QClass: v1.DNSClass(layers.DNSClassIN),
		QType:  v1.DNSType(layers.DNSTypeA),
		RCode:  v1.DNSResponseCode(layers.DNSResponseCodeNoErr),
		RRSets: v1.DNSRRSets{
			v1.DNSName{
				Name:  "www.badguys.co.uk",
				Class: v1.DNSClass(layers.DNSClassIN),
				Type:  v1.DNSType(layers.DNSTypeCNAME),
			}: []v1.DNSRData{
				{Raw: []byte("www1.badguys-backend.co.uk")},
			},
			v1.DNSName{
				Name:  "www1.badguys-backend.co.uk",
				Class: v1.DNSClass(layers.DNSClassIN),
				Type:  v1.DNSType(layers.DNSTypeA),
			}: []v1.DNSRData{
				{Raw: []byte("233.1.44.55")},
				{Raw: []byte("233.1.32.1")},
			},
		},
	}
	record := v1.SuspiciousDomainEventRecord{
		DNSLogID:          "111-222-333",
		Feeds:             []string{"test-feed", "my-feed"},
		SuspiciousDomains: []string{"www1.badguys-backend.co.uk"},
	}
	expected := v1.Event{
		ID:              "test-feed~my-feed_1_20.21.22.23_www1.badguys-backend.co.uk",
		Time:            1,
		Type:            SuspiciousDNSQuery,
		Description:     "default/client-8888-* got DNS query results including suspicious domain(s) www1.badguys-backend.co.uk from global threat feed(s) test-feed, my-feed",
		Severity:        Severity,
		Origin:          "test-feed",
		SourceIP:        util.Sptr("20.21.22.23"),
		SourceNamespace: "default",
		SourceName:      "-",
		SourceNameAggr:  "client-8888-*",
		Host:            "",
		Record:          record,
	}

	domains := map[string]struct{}{
		"www1.badguys-backend.co.uk": {},
	}
	actual := ConvertDNSLog(tc, storage.QueryKeyDNSLogRRSetsName, domains, "test-feed", "my-feed")
	g.Expect(actual).To(Equal(expected))

	// Multiple matched domains
	expected.ID = "test-feed~my-feed_1_20.21.22.23_www.badguys.co.uk~www1.badguys-backend.co.uk"
	expected.Description = "default/client-8888-* got DNS query results including suspicious domain(s) www.badguys.co.uk, www1.badguys-backend.co.uk from global threat feed(s) test-feed, my-feed"
	record.SuspiciousDomains = []string{"www.badguys.co.uk", "www1.badguys-backend.co.uk"}
	expected.Record = record
	domains["www.badguys.co.uk"] = struct{}{}
	actual = ConvertDNSLog(tc, storage.QueryKeyDNSLogRRSetsName, domains, "test-feed", "my-feed")
	g.Expect(actual).To(Equal(expected))

	// No matched domains
	expected.ID = "test-feed~my-feed_1_20.21.22.23_unknown"
	expected.Description = "default/client-8888-* got DNS query results including suspicious domain(s)  from global threat feed(s) test-feed, my-feed"
	record.SuspiciousDomains = nil
	expected.Record = record
	actual = ConvertDNSLog(tc, storage.QueryKeyDNSLogRRSetsName, map[string]struct{}{}, "test-feed", "my-feed")
	g.Expect(actual).To(Equal(expected))
}

func TestConvertDNSLog_RRSetRData(t *testing.T) {
	g := NewGomegaWithT(t)

	tc := v1.DNSLog{
		ID:              "111-222-333",
		StartTime:       time.Unix(1, 0),
		EndTime:         time.Unix(5, 0),
		Count:           1,
		ClientName:      "-",
		ClientNameAggr:  "client-8888-*",
		ClientNamespace: "default",
		ClientIP:        util.IPPtr("20.21.22.23"),
		ClientLabels:    map[string]string{"foo": "bar"},
		Servers: []v1.DNSServer{
			{
				Endpoint: v1.Endpoint{
					Name:           "coredns-111111",
					AggregatedName: "coredns-*",
					Namespace:      "kube-system",
				},
				IP: *util.IPPtr("50.60.70.80"),
			},
		},
		QName:  "www.badguys.co.uk",
		QClass: v1.DNSClass(layers.DNSClassIN),
		QType:  v1.DNSType(layers.DNSTypeCNAME),
		RCode:  v1.DNSResponseCode(layers.DNSResponseCodeNoErr),
		RRSets: v1.DNSRRSets{
			v1.DNSName{
				Name:  "www.badguys.co.uk",
				Class: v1.DNSClass(layers.DNSClassIN),
				Type:  v1.DNSType(layers.DNSTypeCNAME),
			}: []v1.DNSRData{
				{Raw: []byte("www1.badguys-backend.co.uk")},
			},
			v1.DNSName{
				Name:  "www1.badguys-backend.co.uk",
				Class: v1.DNSClass(layers.DNSClassIN),
				Type:  v1.DNSType(layers.DNSTypeA),
			}: []v1.DNSRData{
				{Raw: []byte("uef0.malh0st.io")},
			},
		},
	}
	record := v1.SuspiciousDomainEventRecord{
		DNSLogID:          "111-222-333",
		Feeds:             []string{"test-feed"},
		SuspiciousDomains: []string{"uef0.malh0st.io"},
	}
	expected := v1.Event{
		ID:              "test-feed_1_20.21.22.23_uef0.malh0st.io",
		Time:            1,
		Type:            SuspiciousDNSQuery,
		Description:     "default/client-8888-* got DNS query results including suspicious domain(s) uef0.malh0st.io from global threat feed(s) test-feed",
		Severity:        Severity,
		Origin:          "test-feed",
		SourceIP:        util.Sptr("20.21.22.23"),
		SourceNamespace: "default",
		SourceName:      "-",
		SourceNameAggr:  "client-8888-*",
		Host:            "",
		Record:          record,
	}

	domains := map[string]struct{}{
		"uef0.malh0st.io": {},
	}
	actual := ConvertDNSLog(tc, storage.QueryKeyDNSLogRRSetsRData, domains, "test-feed")
	g.Expect(actual).To(Equal(expected))

	// Multiple matched domains
	expected.ID = "test-feed_1_20.21.22.23_uef0.malh0st.io~www1.badguys-backend.co.uk"
	expected.Description = "default/client-8888-* got DNS query results including suspicious domain(s) uef0.malh0st.io, www1.badguys-backend.co.uk from global threat feed(s) test-feed"
	record.SuspiciousDomains = []string{"uef0.malh0st.io", "www1.badguys-backend.co.uk"}
	expected.Record = record
	domains["www1.badguys-backend.co.uk"] = struct{}{}
	actual = ConvertDNSLog(tc, storage.QueryKeyDNSLogRRSetsRData, domains, "test-feed")
	g.Expect(actual).To(Equal(expected))

	// No matched domains
	expected.ID = "test-feed_1_20.21.22.23_unknown"
	expected.Description = "default/client-8888-* got DNS query results including suspicious domain(s)  from global threat feed(s) test-feed"
	record.SuspiciousDomains = nil
	expected.Record = record
	actual = ConvertDNSLog(tc, storage.QueryKeyDNSLogRRSetsRData, map[string]struct{}{}, "test-feed")
	g.Expect(actual).To(Equal(expected))
}
