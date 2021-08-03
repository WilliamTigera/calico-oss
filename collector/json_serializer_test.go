// Copyright (c) 2018-2021 Tigera, Inc. All rights reserved.

package collector

import (
	"fmt"
	"net"
	"reflect"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("FlowLog JSON serialization", func() {
	argList := []string{"arg1", "arg2"}
	emptyList := []string{"-"}
	Describe("should set every field", func() {
		policies := FlowPolicies{
			"0|tier.policy|pass":                      emptyValue,
			"1|default.knp.default.default-deny|deny": emptyValue,
		}
		flowLog := FlowLog{
			StartTime: time.Now(),
			EndTime:   time.Now(),
			FlowMeta: FlowMeta{
				Tuple: Tuple{
					proto: 6,
					src:   [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
					dst:   [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
					l4Src: 345,
					l4Dst: 80,
				},
				SrcMeta: EndpointMetadata{
					Type:           "wep",
					Namespace:      "test",
					Name:           "test",
					AggregatedName: "test-*",
				},
				DstMeta: EndpointMetadata{
					Type:           "wep",
					Namespace:      "test",
					Name:           "test",
					AggregatedName: "test-*",
				},
				DstService: FlowService{
					Name:      "svc2",
					Namespace: "svc2-ns",
					PortName:  "*",
					PortNum:   80,
				},
				Action:   "allow",
				Reporter: "src",
			},
			FlowLabels: FlowLabels{
				SrcLabels: map[string]string{"foo": "bar", "foo2": "bar2"},
				DstLabels: map[string]string{"foo": "bar", "foo2": "bar2"},
			},
			FlowPolicies: policies,
			FlowExtras: FlowExtras{
				OriginalSourceIPs:    []net.IP{net.ParseIP("10.0.1.1")},
				NumOriginalSourceIPs: 1,
			},
			FlowProcessReportedStats: FlowProcessReportedStats{
				ProcessName:     "*",
				NumProcessNames: 2,
				ProcessID:       "*",
				NumProcessIDs:   2,
				ProcessArgs:     argList,
				NumProcessArgs:  2,
				FlowReportedStats: FlowReportedStats{
					PacketsIn:             1,
					PacketsOut:            2,
					BytesIn:               3,
					BytesOut:              4,
					NumFlowsStarted:       5,
					NumFlowsCompleted:     6,
					NumFlows:              7,
					HTTPRequestsAllowedIn: 8,
					HTTPRequestsDeniedIn:  9,
				},
				FlowReportedTCPStats: FlowReportedTCPStats{
					SendCongestionWnd: TCPWnd{
						Mean: 2,
						Min:  3,
					},
					SmoothRtt: TCPRtt{
						Mean: 2,
						Max:  3,
					},
					MinRtt: TCPRtt{
						Mean: 2,
						Max:  3,
					},
					Mss: TCPMss{
						Mean: 2,
						Min:  3,
					},
					TotalRetrans:   7,
					LostOut:        8,
					UnrecoveredRTO: 9,
					Count:          1,
				},
			},
		}

		out := toOutput(&flowLog)
		// Use reflection to loop over the fields and ensure they all have non
		// zero values
		oType := reflect.TypeOf(out)
		oVal := reflect.ValueOf(out)
		for i := 0; i < oType.NumField(); i++ {
			field := oType.Field(i)
			zeroVal := reflect.Zero(field.Type)
			actualVal := oVal.Field(i)
			It(fmt.Sprintf("should set %s", field.Name), func() {
				Expect(actualVal.Interface()).ToNot(Equal(zeroVal.Interface()))
			})
		}
	})

	Describe("should handle empty fields", func() {
		flowLog := FlowLog{
			StartTime: time.Now(),
			EndTime:   time.Now(),
			FlowMeta: FlowMeta{
				Tuple: Tuple{
					proto: 6,
					src:   [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
					dst:   [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
					l4Src: unsetIntField,
					l4Dst: 80,
				},
				SrcMeta: EndpointMetadata{
					Type:           "wep",
					Namespace:      "test",
					Name:           "test",
					AggregatedName: "test-*",
				},
				DstMeta: EndpointMetadata{
					Type:           "wep",
					Namespace:      "test",
					Name:           "test",
					AggregatedName: "test-*",
				},
				Action:   "allow",
				Reporter: "src",
			},
			FlowLabels: FlowLabels{
				SrcLabels: nil,
				DstLabels: nil,
			},
			FlowExtras: FlowExtras{
				OriginalSourceIPs:    []net.IP{},
				NumOriginalSourceIPs: 0,
			},
			FlowProcessReportedStats: FlowProcessReportedStats{
				ProcessName:     "-",
				NumProcessNames: 0,
				ProcessID:       "-",
				NumProcessIDs:   0,
				ProcessArgs:     emptyList,
				NumProcessArgs:  0,
				FlowReportedStats: FlowReportedStats{
					PacketsIn:             1,
					PacketsOut:            2,
					BytesIn:               3,
					BytesOut:              4,
					NumFlowsStarted:       5,
					NumFlowsCompleted:     6,
					NumFlows:              7,
					HTTPRequestsAllowedIn: 8,
					HTTPRequestsDeniedIn:  9,
				},
				FlowReportedTCPStats: FlowReportedTCPStats{
					SendCongestionWnd: TCPWnd{
						Mean: 2,
						Min:  3,
					},
					SmoothRtt: TCPRtt{
						Mean: 2,
						Max:  3,
					},
					MinRtt: TCPRtt{
						Mean: 2,
						Max:  3,
					},
					Mss: TCPMss{
						Mean: 2,
						Min:  3,
					},
					TotalRetrans:   7,
					LostOut:        8,
					UnrecoveredRTO: 9,
					Count:          1,
				},
			},
		}

		out := toOutput(&flowLog)

		zeroFieldNames := map[string]interface{}{
			"SourceIP":             nil,
			"DestIP":               nil,
			"SourcePortNum":        nil,
			"SourceLabels":         nil,
			"DestServiceNamespace": nil,
			"DestServiceName":      nil,
			"DestServicePortName":  nil,
			"DestServicePortNum":   0,
			"DestLabels":           nil,
			"Policies":             nil,
			"OrigSourceIPs":        nil,
			"NumOrigSourceIPs":     nil,
			"NumProcessNames":      0,
			"NumProcessIDs":        0,
			"NumProcessArgs":       0,
		}
		// Use reflection to loop over the fields and ensure they all have non
		// zero values
		oType := reflect.TypeOf(out)
		oVal := reflect.ValueOf(out)
		for i := 0; i < oType.NumField(); i++ {
			field := oType.Field(i)
			zeroVal := reflect.Zero(field.Type)
			actualVal := oVal.Field(i)
			if _, ok := zeroFieldNames[field.Name]; ok {
				It(fmt.Sprintf("should not set %s", field.Name), func() {
					Expect(actualVal.Interface()).To(Equal(zeroVal.Interface()))
				})
			} else {
				It(fmt.Sprintf("should set %s", field.Name), func() {
					Expect(actualVal.Interface()).ToNot(Equal(zeroVal.Interface()))
				})
			}
		}
	})

	Describe("should not set source and destination ports for icmp flow", func() {
		flowLog := FlowLog{
			StartTime: time.Now(),
			EndTime:   time.Now(),
			FlowMeta: FlowMeta{
				Tuple: Tuple{
					proto: 1,
					src:   [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
					dst:   [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
					l4Src: 1234,
					l4Dst: 2948,
				},
				SrcMeta: EndpointMetadata{
					Type:           "wep",
					Namespace:      "test",
					Name:           "test",
					AggregatedName: "test-*",
				},
				DstMeta: EndpointMetadata{
					Type:           "wep",
					Namespace:      "test",
					Name:           "test",
					AggregatedName: "test-*",
				},
				Action:   "allow",
				Reporter: "src",
			},
			FlowLabels: FlowLabels{
				SrcLabels: nil,
				DstLabels: nil,
			},
			FlowExtras: FlowExtras{
				OriginalSourceIPs:    []net.IP{},
				NumOriginalSourceIPs: 0,
			},
			FlowProcessReportedStats: FlowProcessReportedStats{
				ProcessName:     "felix",
				NumProcessNames: 2,
				ProcessID:       "1234",
				NumProcessIDs:   2,
				ProcessArgs:     argList,
				NumProcessArgs:  2,
				FlowReportedStats: FlowReportedStats{
					PacketsIn:             1,
					PacketsOut:            2,
					BytesIn:               3,
					BytesOut:              4,
					NumFlowsStarted:       5,
					NumFlowsCompleted:     6,
					NumFlows:              7,
					HTTPRequestsAllowedIn: 8,
					HTTPRequestsDeniedIn:  9,
				},
				FlowReportedTCPStats: FlowReportedTCPStats{
					SendCongestionWnd: TCPWnd{
						Mean: 2,
						Min:  3,
					},
					SmoothRtt: TCPRtt{
						Mean: 2,
						Max:  3,
					},
					MinRtt: TCPRtt{
						Mean: 2,
						Max:  3,
					},
					Mss: TCPMss{
						Mean: 2,
						Min:  3,
					},
					TotalRetrans:   7,
					LostOut:        8,
					UnrecoveredRTO: 9,
					Count:          1,
				},
			},
		}

		out := toOutput(&flowLog)

		zeroFieldNames := map[string]interface{}{
			"SourceIP":             nil,
			"DestIP":               nil,
			"SourcePortNum":        nil,
			"DestPortNum":          nil,
			"DestServiceNamespace": nil,
			"DestServiceName":      nil,
			"DestServicePortName":  nil,
			"DestServicePortNum":   0,
			"SourceLabels":         nil,
			"DestLabels":           nil,
			"Policies":             nil,
			"OrigSourceIPs":        nil,
			"NumOrigSourceIPs":     nil,
		}
		// Use reflection to loop over the fields and ensure they all have non
		// zero values
		oType := reflect.TypeOf(out)
		oVal := reflect.ValueOf(out)
		for i := 0; i < oType.NumField(); i++ {
			field := oType.Field(i)
			zeroVal := reflect.Zero(field.Type)
			actualVal := oVal.Field(i)
			if _, ok := zeroFieldNames[field.Name]; ok {
				It(fmt.Sprintf("should not set %s", field.Name), func() {
					Expect(actualVal.Interface()).To(Equal(zeroVal.Interface()))
				})
			} else {
				It(fmt.Sprintf("should set %s", field.Name), func() {
					Expect(actualVal.Interface()).ToNot(Equal(zeroVal.Interface()))
				})
			}
		}
	})
})
