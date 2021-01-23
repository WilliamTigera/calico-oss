// Copyright (c) 2017-2021 Tigera, Inc. All rights reserved.

package collector

import (
	"fmt"
	"strconv"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/projectcalico/felix/calc"
	"github.com/projectcalico/felix/rules"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/set"
)

type testProcessInfo struct {
	processName     string
	numProcessIDs   int
	processID       string
	numProcessNames int
}

var (
	noProcessInfo = testProcessInfo{"-", 0, "-", 0}
)

// Common MetricUpdate definitions
var (
	// Metric update without a connection (ingress stats match those of muConn1Rule1AllowUpdate).
	muNoConn1Rule1AllowUpdateWithEndpointMeta = MetricUpdate{
		updateType: UpdateTypeReport,
		tuple:      tuple1,

		srcEp: &calc.EndpointData{
			Key: model.WorkloadEndpointKey{
				Hostname:       "node-01",
				OrchestratorID: "k8s",
				WorkloadID:     "kube-system/iperf-4235-5623461",
				EndpointID:     "4352",
			},
			Endpoint: &model.WorkloadEndpoint{GenerateName: "iperf-4235-", Labels: map[string]string{"test-app": "true"}},
		},

		dstEp: &calc.EndpointData{
			Key: model.WorkloadEndpointKey{
				Hostname:       "node-02",
				OrchestratorID: "k8s",
				WorkloadID:     "default/nginx-412354-5123451",
				EndpointID:     "4352",
			},
			Endpoint: &model.WorkloadEndpoint{GenerateName: "nginx-412354-", Labels: map[string]string{"k8s-app": "true"}},
		},

		ruleIDs:      []*calc.RuleID{ingressRule1Allow},
		isConnection: false,
		inMetric: MetricValue{
			deltaPackets: 1,
			deltaBytes:   20,
		},
	}
)

var _ = Describe("Flow log aggregator tests", func() {
	// TODO(SS): Pull out the convenience functions for re-use.
	expectFlowLog := func(fl FlowLog, t Tuple, nf, nfs, nfc int, a FlowLogAction, fr FlowLogReporter, pi, po, bi, bo int, sm, dm EndpointMetadata, dsvc FlowService, sl, dl map[string]string, fp FlowPolicies, fe FlowExtras, fpi testProcessInfo) {
		expectedFlow := newExpectedFlowLog(t, nf, nfs, nfc, a, fr, pi, po, bi, bo, sm, dm, dsvc, sl, dl, fp, fe, fpi)

		// We don't include the start and end time in the comparison, so copy to a new log without these
		var flNoTime FlowLog
		flNoTime.FlowMeta = fl.FlowMeta
		flNoTime.FlowLabels = fl.FlowLabels
		flNoTime.FlowPolicies = fl.FlowPolicies
		flNoTime.FlowProcessReportedStats = fl.FlowProcessReportedStats
		Expect(flNoTime).Should(Equal(expectedFlow))
	}
	expectFlowLogsMatch := func(actualFlows []*FlowLog, expectedFlows []FlowLog) {
		By("Checking all flowlogs match")
		actualFlowsNoTime := []FlowLog{}
		for _, fl := range actualFlows {
			// We don't include the start and end time in the comparison, so copy to a new log without these
			flNoTime := FlowLog{}
			flNoTime.FlowMeta = fl.FlowMeta
			flNoTime.FlowLabels = fl.FlowLabels
			flNoTime.FlowPolicies = fl.FlowPolicies
			flNoTime.FlowProcessReportedStats = fl.FlowProcessReportedStats
			actualFlowsNoTime = append(actualFlowsNoTime, flNoTime)
		}
		Expect(actualFlowsNoTime).Should(ConsistOf(expectedFlows))
	}
	calculatePacketStats := func(mus ...MetricUpdate) (epi, epo, ebi, ebo int) {
		for _, mu := range mus {
			epi += mu.inMetric.deltaPackets
			epo += mu.outMetric.deltaPackets
			ebi += mu.inMetric.deltaBytes
			ebo += mu.outMetric.deltaBytes
		}
		return
	}
	calculateHTTPRequestStats := func(mus ...MetricUpdate) (allowed, denied int) {
		for _, mu := range mus {
			allowed += mu.inMetric.deltaAllowedHTTPRequests
			denied += mu.inMetric.deltaDeniedHTTPRequests
		}
		return
	}

	extractFlowExtras := func(mus ...MetricUpdate) FlowExtras {
		var ipBs *boundedSet
		for _, mu := range mus {
			if mu.origSourceIPs == nil {
				continue
			}
			if ipBs == nil {
				ipBs = mu.origSourceIPs.Copy()
			} else {
				ipBs.Combine(mu.origSourceIPs)
			}
		}
		if ipBs != nil {
			return FlowExtras{
				OriginalSourceIPs:    ipBs.ToIPSlice(),
				NumOriginalSourceIPs: ipBs.TotalCount(),
			}
		} else {
			return FlowExtras{}
		}
	}

	extractFlowPolicies := func(mus ...MetricUpdate) FlowPolicies {
		fp := make(FlowPolicies)
		for _, mu := range mus {
			for idx, r := range mu.ruleIDs {
				name := fmt.Sprintf("%d|%s|%s.%s|%s", idx,
					r.TierString(),
					r.TierString(),
					r.NameString(),
					r.ActionString())
				fp[name] = emptyValue
			}
		}
		return fp
	}

	extractFlowProcessInfo := func(mus ...MetricUpdate) testProcessInfo {
		fpi := testProcessInfo{}
		procNames := set.New()
		procID := set.New()
		processName := ""
		processID := ""
		for i, mu := range mus {
			if i == 0 {
				processName = mu.processName
				processID = strconv.Itoa(mu.processID)
			}
			procNames.Add(mu.processName)
			procID.Add(mu.processID)
		}
		if procNames.Len() == 1 {
			fpi.processName = processName
			fpi.numProcessNames = 1
		} else {
			fpi.processName = "*"
			fpi.numProcessNames = procNames.Len()
		}

		if procID.Len() == 1 {
			fpi.processID = processID
			fpi.numProcessIDs = 1
		} else {
			fpi.processID = "*"
			fpi.numProcessIDs = procID.Len()
		}
		return fpi
	}

	Context("Flow log aggregator aggregation verification", func() {
		It("aggregates the fed metric updates", func() {
			By("default duration")
			ca := NewFlowLogAggregator()
			ca.IncludePolicies(true)
			ca.FeedUpdate(muNoConn1Rule1AllowUpdate)
			messages := ca.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))
			message := *(messages[0])

			expectedNumFlows := 1
			expectedNumFlowsStarted := 1
			expectedNumFlowsCompleted := 0

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut := calculatePacketStats(muNoConn1Rule1AllowUpdate)
			expectedFP := extractFlowPolicies(muNoConn1Rule1AllowUpdate)
			expectedFlowExtras := extractFlowExtras(muNoConn1Rule1AllowUpdate)
			expectFlowLog(message, tuple1, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, pubMeta, noService, nil, nil, expectedFP, expectedFlowExtras, noProcessInfo)

			By("source port")
			ca = NewFlowLogAggregator().AggregateOver(FlowSourcePort)
			ca.FeedUpdate(muNoConn1Rule1AllowUpdate)
			// Construct a similar update; same tuple but diff src ports.
			muNoConn1Rule1AllowUpdateCopy := muNoConn1Rule1AllowUpdate
			tuple1Copy := tuple1
			tuple1Copy.l4Src = 44123
			muNoConn1Rule1AllowUpdateCopy.tuple = tuple1Copy
			ca.FeedUpdate(muNoConn1Rule1AllowUpdateCopy)
			messages = ca.GetAndCalibrate(FlowSourcePort)
			// Two updates should still result in 1 flow
			Expect(len(messages)).Should(Equal(1))

			By("endpoint prefix names")
			ca = NewFlowLogAggregator().AggregateOver(FlowPrefixName)
			ca.FeedUpdate(muNoConn1Rule1AllowUpdateWithEndpointMeta)
			// Construct a similar update; same tuple but diff src ports.
			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy := muNoConn1Rule1AllowUpdateWithEndpointMeta
			// TODO(SS): Handle and organize these test constants better. Right now they are all over the places
			// like reporter_prometheus_test.go, collector_test.go , etc.
			tuple1Copy = tuple1
			// Everything can change in the 5-tuple except for the dst port.
			tuple1Copy.l4Src = 44123
			tuple1Copy.src = ipStrTo16Byte("10.0.0.3")
			tuple1Copy.dst = ipStrTo16Byte("10.0.0.9")
			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy.tuple = tuple1Copy

			// Updating the Workload IDs for src and dst.
			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy.srcEp = &calc.EndpointData{
				Key: model.WorkloadEndpointKey{
					Hostname:       "node-01",
					OrchestratorID: "k8s",
					WorkloadID:     "kube-system/iperf-4235-5434134",
					EndpointID:     "23456",
				},
				Endpoint: &model.WorkloadEndpoint{GenerateName: "iperf-4235-", Labels: map[string]string{"test-app": "true"}},
			}

			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy.dstEp = &calc.EndpointData{
				Key: model.WorkloadEndpointKey{
					Hostname:       "node-02",
					OrchestratorID: "k8s",
					WorkloadID:     "default/nginx-412354-6543645",
					EndpointID:     "256267",
				},
				Endpoint: &model.WorkloadEndpoint{GenerateName: "nginx-412354-", Labels: map[string]string{"k8s-app": "true"}},
			}

			ca.FeedUpdate(muNoConn1Rule1AllowUpdateWithEndpointMetaCopy)
			messages = ca.GetAndCalibrate(FlowPrefixName)
			// Two updates should still result in 1 flow
			Expect(len(messages)).Should(Equal(1))
			// Updating the Workload IDs and labels for src and dst.
			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy.srcEp = &calc.EndpointData{
				Key: model.WorkloadEndpointKey{
					Hostname:       "node-01",
					OrchestratorID: "k8s",
					WorkloadID:     "kube-system/iperf-4235-5434134",
					EndpointID:     "23456",
				},
				// this new MetricUpdates src endpointMeta has a different label than one currently being tracked.
				Endpoint: &model.WorkloadEndpoint{GenerateName: "iperf-4235-", Labels: map[string]string{"prod-app": "true"}},
			}

			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy.dstEp = &calc.EndpointData{
				Key: model.WorkloadEndpointKey{
					Hostname:       "node-02",
					OrchestratorID: "k8s",
					WorkloadID:     "default/nginx-412354-6543645",
					EndpointID:     "256267",
				},
				// different label on the destination workload than one being tracked.
				Endpoint: &model.WorkloadEndpoint{GenerateName: "nginx-412354-", Labels: map[string]string{"k8s-app": "false"}},
			}

			ca.FeedUpdate(muNoConn1Rule1AllowUpdateWithEndpointMetaCopy)
			messages = ca.GetAndCalibrate(FlowPrefixName)
			// Two updates should still result in 1 flow
			Expect(len(messages)).Should(Equal(1))

			By("by endpoint IP classification as the meta name when meta info is missing")
			ca = NewFlowLogAggregator().AggregateOver(FlowPrefixName)
			endpointMeta := calc.EndpointData{
				Key: model.WorkloadEndpointKey{
					Hostname:       "node-01",
					OrchestratorID: "k8s",
					WorkloadID:     "kube-system/iperf-4235-5623461",
					EndpointID:     "4352",
				},
				Endpoint: &model.WorkloadEndpoint{GenerateName: "iperf-4235-", Labels: map[string]string{"test-app": "true"}},
			}

			muWithoutDstEndpointMeta := MetricUpdate{
				updateType: UpdateTypeReport,
				tuple:      *NewTuple(ipStrTo16Byte("192.168.0.4"), ipStrTo16Byte("192.168.0.14"), proto_tcp, srcPort1, dstPort),

				// src endpoint meta info available
				srcEp: &endpointMeta,

				// dst endpoint meta info not available
				dstEp: nil,

				ruleIDs:      []*calc.RuleID{ingressRule1Allow},
				isConnection: false,
				inMetric: MetricValue{
					deltaPackets: 1,
					deltaBytes:   20,
				},
			}
			ca.FeedUpdate(muWithoutDstEndpointMeta)

			// Another metric update comes in. This time on a different dst private IP
			muWithoutDstEndpointMetaCopy := muWithoutDstEndpointMeta
			muWithoutDstEndpointMetaCopy.tuple.dst = ipStrTo16Byte("192.168.0.17")
			ca.FeedUpdate(muWithoutDstEndpointMetaCopy)
			messages = ca.GetAndCalibrate(FlowPrefixName)
			// One flow expected: srcMeta.GenerateName -> pvt
			// Two updates should still result in 1 flow
			Expect(len(messages)).Should(Equal(1))

			// Initial Update
			ca.FeedUpdate(muWithoutDstEndpointMeta)
			// + metric update comes in. This time on a non-private dst IP
			muWithoutDstEndpointMetaCopy.tuple.dst = ipStrTo16Byte("198.17.8.43")
			ca.FeedUpdate(muWithoutDstEndpointMetaCopy)
			messages = ca.GetAndCalibrate(FlowPrefixName)
			// 2nd flow expected: srcMeta.GenerateName -> pub
			// Three updates so far should result in 2 flows
			Expect(len(messages)).Should(Equal(2)) // Metric Update comes in with a non private as the dst IP

			// Initial Updates
			ca.FeedUpdate(muWithoutDstEndpointMeta)
			ca.FeedUpdate(muWithoutDstEndpointMetaCopy)
			// + metric update comes in. This time with missing src endpointMeta
			muWithoutDstEndpointMetaCopy.srcEp = nil
			muWithoutDstEndpointMetaCopy.dstEp = &endpointMeta
			ca.FeedUpdate(muWithoutDstEndpointMetaCopy)
			messages = ca.GetAndCalibrate(FlowPrefixName)

			// 3rd flow expected: pvt -> dst.GenerateName
			// Four updates so far should result in 3 flows
			Expect(len(messages)).Should(Equal(3)) // Metric Update comes in with a non private as the dst IP

			// Confirm the expected flow metas
			fm1 := FlowMeta{
				Tuple: Tuple{
					src:   [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
					dst:   [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
					proto: 6,
					l4Src: unsetIntField,
					l4Dst: 80,
				},
				SrcMeta: EndpointMetadata{
					Type:           "wep",
					Namespace:      "kube-system",
					Name:           "-",
					AggregatedName: "iperf-4235-*",
				},
				DstMeta: EndpointMetadata{
					Type:           "net",
					Namespace:      "-",
					Name:           "-",
					AggregatedName: "pub",
				},
				DstService: noService,
				Action:     "allow",
				Reporter:   "dst",
			}

			fm2 := FlowMeta{
				Tuple: Tuple{
					src:   [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
					dst:   [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
					proto: 6,
					l4Src: unsetIntField,
					l4Dst: 80,
				},
				SrcMeta: EndpointMetadata{
					Type:           "net",
					Namespace:      "-",
					Name:           "-",
					AggregatedName: "pvt",
				},
				DstMeta: EndpointMetadata{
					Type:           "wep",
					Namespace:      "kube-system",
					Name:           "-",
					AggregatedName: "iperf-4235-*",
				},
				DstService: noService,
				Action:     "allow",
				Reporter:   "dst",
			}

			fm3 := FlowMeta{
				Tuple: Tuple{
					src:   [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
					dst:   [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
					proto: 6,
					l4Src: unsetIntField,
					l4Dst: 80,
				},
				SrcMeta: EndpointMetadata{
					Type:           "wep",
					Namespace:      "kube-system",
					Name:           "-",
					AggregatedName: "iperf-4235-*",
				},
				DstMeta: EndpointMetadata{
					Type:           "net",
					Namespace:      "-",
					Name:           "-",
					AggregatedName: "pvt",
				},
				DstService: noService,
				Action:     "allow",
				Reporter:   "dst",
			}

			flowLogMetas := []FlowMeta{}
			for _, fl := range messages {
				flowLogMetas = append(flowLogMetas, fl.FlowMeta)
			}

			Expect(flowLogMetas).Should(ConsistOf(fm1, fm2, fm3))
		})

		It("aggregates labels from metric updates", func() {
			By("intersecting labels in FlowSpec when IncludeLabels configured")
			ca := NewFlowLogAggregator().IncludeLabels(true)
			ca.FeedUpdate(muNoConn1Rule1AllowUpdateWithEndpointMeta)

			// Construct a similar update; but the endpoints have different labels
			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy := muNoConn1Rule1AllowUpdateWithEndpointMeta
			// Updating the Workload IDs for src and dst.
			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy.srcEp = &calc.EndpointData{
				Key: model.WorkloadEndpointKey{
					Hostname:       "node-01",
					OrchestratorID: "k8s",
					WorkloadID:     "kube-system/iperf-4235-5623461",
					EndpointID:     "4352",
				},
				Endpoint: &model.WorkloadEndpoint{
					GenerateName: "iperf-4235-",
					Labels:       map[string]string{"test-app": "true", "new-label": "true"}, // "new-label" appended
				},
			}

			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy.dstEp = &calc.EndpointData{
				Key: model.WorkloadEndpointKey{
					Hostname:       "node-02",
					OrchestratorID: "k8s",
					WorkloadID:     "default/nginx-412354-5123451",
					EndpointID:     "4352",
				},
				Endpoint: &model.WorkloadEndpoint{
					GenerateName: "nginx-412354-",
					Labels:       map[string]string{"k8s-app": "false"}, // conflicting labels; originally "k8s-app": "true"
				},
			}
			ca.FeedUpdate(muNoConn1Rule1AllowUpdateWithEndpointMetaCopy)
			messages := ca.GetAndCalibrate(FlowDefault)
			// Since the FlowMeta remains the same it should still equal 1.
			Expect(len(messages)).Should(Equal(1))
			message := *(messages[0])

			expectedNumFlows := 1
			expectedNumFlowsStarted := 1
			expectedNumFlowsCompleted := 0
			srcMeta := EndpointMetadata{
				Type:           "wep",
				Namespace:      "kube-system",
				Name:           "iperf-4235-5623461",
				AggregatedName: "iperf-4235-*",
			}
			dstMeta := EndpointMetadata{
				Type:           "wep",
				Namespace:      "default",
				Name:           "nginx-412354-5123451",
				AggregatedName: "nginx-412354-*",
			}
			// The labels should have been intersected correctly.
			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut := calculatePacketStats(muNoConn1Rule1AllowUpdateWithEndpointMetaCopy)
			expectedFlowExtras := extractFlowExtras(muNoConn1Rule1AllowUpdateWithEndpointMetaCopy)
			expectFlowLog(message, tuple1, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn*2, expectedPacketsOut, expectedBytesIn*2, expectedBytesOut, srcMeta, dstMeta, noService, map[string]string{"test-app": "true"}, map[string]string{}, nil, expectedFlowExtras, noProcessInfo)

			By("not affecting flow logs when IncludeLabels is disabled")
			ca = NewFlowLogAggregator().IncludeLabels(false)
			ca.FeedUpdate(muNoConn1Rule1AllowUpdateWithEndpointMeta)

			// Construct a similar update; but the endpoints have different labels
			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy = muNoConn1Rule1AllowUpdateWithEndpointMeta
			// Updating the Workload IDs for src and dst.
			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy.srcEp = &calc.EndpointData{
				Key: model.WorkloadEndpointKey{
					Hostname:       "node-01",
					OrchestratorID: "k8s",
					WorkloadID:     "kube-system/iperf-4235-5623461",
					EndpointID:     "4352",
				},
				Endpoint: &model.WorkloadEndpoint{
					GenerateName: "iperf-4235-",
					Labels:       map[string]string{"test-app": "true", "new-label": "true"}, // "new-label" appended
				},
			}

			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy.dstEp = &calc.EndpointData{
				Key: model.WorkloadEndpointKey{
					Hostname:       "node-02",
					OrchestratorID: "k8s",
					WorkloadID:     "default/nginx-412354-5123451",
					EndpointID:     "4352",
				},
				Endpoint: &model.WorkloadEndpoint{
					GenerateName: "nginx-412354-",
					Labels:       map[string]string{"k8s-app": "false"}, // conflicting labels; originally "k8s-app": "true"
				},
			}
			ca.FeedUpdate(muNoConn1Rule1AllowUpdateWithEndpointMetaCopy)
			messages = ca.GetAndCalibrate(FlowDefault)
			// Since the FlowMeta remains the same it should still equal 1.
			Expect(len(messages)).Should(Equal(1))
			message = *(messages[0])

			expectedNumFlows = 1
			expectedNumFlowsStarted = 1
			expectedNumFlowsCompleted = 0
			srcMeta = EndpointMetadata{
				Type:           "wep",
				Namespace:      "kube-system",
				Name:           "iperf-4235-5623461",
				AggregatedName: "iperf-4235-*",
			}
			dstMeta = EndpointMetadata{
				Type:           "wep",
				Namespace:      "default",
				Name:           "nginx-412354-5123451",
				AggregatedName: "nginx-412354-*",
			}
			// The labels should have been intersected right.
			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut = calculatePacketStats(muNoConn1Rule1AllowUpdateWithEndpointMetaCopy)
			expectedFlowExtras = extractFlowExtras(muNoConn1Rule1AllowUpdateWithEndpointMetaCopy)
			expectFlowLog(message, tuple1, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn*2, expectedPacketsOut, expectedBytesIn*2, expectedBytesOut, srcMeta, dstMeta, noService, nil, nil, nil, expectedFlowExtras, noProcessInfo) // nil & nil for Src and Dst Labels respectively.
		})
	})

	Context("Flow log aggregator service aggregation", func() {
		service := FlowService{Namespace: "foo-ns", Name: "foo-svc", Port: "foo-port"}
		serviceNoPort := FlowService{Namespace: "foo-ns", Name: "foo-svc", Port: "-"}

		It("Does not aggregate endpoints with and without service with Default aggregation", func() {
			By("Creating an aggregator for allow")
			caa := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).AggregateOver(FlowDefault).IncludeService(true)

			By("Feeding two updates one with service, one without (otherwise identical)")
			_ = caa.FeedUpdate(muWithEndpointMeta)
			_ = caa.FeedUpdate(muWithEndpointMetaWithService)

			By("Checking calibration")
			messages := caa.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(2))
			services := []FlowService{messages[0].DstService, messages[1].DstService}
			Expect(services).To(ConsistOf(noService, service))
		})

		It("Does not aggregate endpoints with and without service with FlowSourcePort aggregation", func() {
			By("Creating an aggregator for allow")
			caa := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).AggregateOver(FlowSourcePort).IncludeService(true)

			By("Feeding two updates one with service, one without (otherwise identical)")
			_ = caa.FeedUpdate(muWithEndpointMeta)
			_ = caa.FeedUpdate(muWithEndpointMetaWithService)

			By("Checking calibration")
			messages := caa.GetAndCalibrate(FlowSourcePort)
			Expect(len(messages)).Should(Equal(2))
			services := []FlowService{messages[0].DstService, messages[1].DstService}
			Expect(services).To(ConsistOf(noService, service))
		})

		It("Does not aggregate endpoints with and without service with FlowPrefixName aggregation", func() {
			By("Creating an aggregator for allow")
			caa := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).AggregateOver(FlowPrefixName).IncludeService(true)

			By("Feeding two updates one with service, one without (otherwise identical)")
			_ = caa.FeedUpdate(muWithEndpointMeta)
			_ = caa.FeedUpdate(muWithEndpointMetaWithService)

			By("Checking calibration")
			messages := caa.GetAndCalibrate(FlowPrefixName)
			Expect(len(messages)).Should(Equal(2))
			services := []FlowService{messages[0].DstService, messages[1].DstService}
			Expect(services).To(ConsistOf(noService, service))
		})

		It("Does not aggregate endpoints with and without service with FlowNoDestPorts aggregation", func() {
			By("Creating an aggregator for allow")
			caa := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).AggregateOver(FlowNoDestPorts).IncludeService(true)

			By("Feeding two updates one with service, one without (otherwise identical)")
			_ = caa.FeedUpdate(muWithEndpointMeta)
			_ = caa.FeedUpdate(muWithEndpointMetaWithService)

			By("Checking calibration")
			messages := caa.GetAndCalibrate(FlowNoDestPorts)
			Expect(len(messages)).Should(Equal(2))
			services := []FlowService{messages[0].DstService, messages[1].DstService}
			Expect(services).To(ConsistOf(noService, serviceNoPort))
		})
	})

	Context("Flow log aggregator filter verification", func() {
		It("Filters out MetricUpdate based on filter applied", func() {
			By("Creating 2 aggregators - one for denied packets, and one for allowed packets")
			var caa, cad FlowLogAggregator

			By("Checking that the MetricUpdate with deny action is only processed by the aggregator with the deny filter")
			caa = NewFlowLogAggregator().ForAction(rules.RuleActionAllow)
			cad = NewFlowLogAggregator().ForAction(rules.RuleActionDeny)

			caa.FeedUpdate(muNoConn1Rule2DenyUpdate)
			messages := caa.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(0))
			cad.FeedUpdate(muNoConn1Rule2DenyUpdate)
			messages = cad.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))

			By("Checking that the MetricUpdate with allow action is only processed by the aggregator with the allow filter")
			caa = NewFlowLogAggregator().ForAction(rules.RuleActionAllow)
			cad = NewFlowLogAggregator().ForAction(rules.RuleActionDeny)

			caa.FeedUpdate(muConn1Rule1AllowUpdate)
			messages = caa.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))
			cad.FeedUpdate(muConn1Rule1AllowUpdate)
			messages = cad.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(0))
		})

	})

	Context("Flow log aggregator http request countes", func() {
		It("Aggregates HTTP allowed and denied packets", func() {
			By("Feeding in two updates containing HTTP request counts")
			ca := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).(*flowLogAggregator)
			ca.FeedUpdate(muConn1Rule1HTTPReqAllowUpdate)
			ca.FeedUpdate(muConn1Rule1HTTPReqAllowUpdate)
			messages := ca.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))
			// StartedFlowRefs count should be 1
			flowLog := messages[0]
			Expect(flowLog.NumFlowsStarted).Should(Equal(1))

			hra, hrd := calculateHTTPRequestStats(muConn1Rule1HTTPReqAllowUpdate, muConn1Rule1HTTPReqAllowUpdate)
			Expect(flowLog.HTTPRequestsAllowedIn).To(Equal(hra))
			Expect(flowLog.HTTPRequestsDeniedIn).To(Equal(hrd))
		})
	})

	Context("Flow log aggregator original source IP", func() {
		It("Aggregates original source IPs", func() {
			By("Feeding in two updates containing HTTP request counts")
			ca := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).(*flowLogAggregator)
			ca.FeedUpdate(muWithOrigSourceIPs)
			ca.FeedUpdate(muWithMultipleOrigSourceIPs)
			messages := ca.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))
			// StartedFlowRefs count should be 1
			flowLog := messages[0]
			Expect(flowLog.NumFlowsStarted).Should(Equal(1))

			flowExtras := extractFlowExtras(muWithOrigSourceIPs, muWithMultipleOrigSourceIPs)
			Expect(flowLog.FlowExtras.OriginalSourceIPs).To(ConsistOf(flowExtras.OriginalSourceIPs))
			Expect(flowLog.FlowExtras.NumOriginalSourceIPs).To(Equal(flowExtras.NumOriginalSourceIPs))
		})
		It("Aggregates original source IPs with unknown rule ID", func() {
			By("Feeding in update containing HTTP request counts and unknown RuleID")
			ca := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).(*flowLogAggregator)
			ca.FeedUpdate(muWithOrigSourceIPsUnknownRuleID)
			messages := ca.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))
			// StartedFlowRefs count should be 1
			flowLog := messages[0]
			Expect(flowLog.NumFlowsStarted).Should(Equal(1))

			flowExtras := extractFlowExtras(muWithOrigSourceIPsUnknownRuleID)
			Expect(flowLog.FlowExtras.OriginalSourceIPs).To(ConsistOf(flowExtras.OriginalSourceIPs))
			Expect(flowLog.FlowExtras.NumOriginalSourceIPs).To(Equal(flowExtras.NumOriginalSourceIPs))
		})
	})

	Context("Flow log aggregator flowstore lifecycle", func() {
		It("Purges only the completed non-aggregated flowMetas", func() {
			By("Accounting for only the completed 5-tuple refs when making a purging decision")
			ca := NewFlowLogAggregator().ForAction(rules.RuleActionDeny).(*flowLogAggregator)
			ca.FeedUpdate(muNoConn1Rule2DenyUpdate)
			messages := ca.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))
			// StartedFlowRefs count should be 1
			flowLog := messages[0]
			Expect(flowLog.NumFlowsStarted).Should(Equal(1))

			// flowStore is not purged of the entry since the flowRef hasn't been expired
			Expect(len(ca.flowStore)).Should(Equal(1))

			// Feeding an update again. But StartedFlowRefs count should be 0
			ca.FeedUpdate(muNoConn1Rule2DenyUpdate)
			messages = ca.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))
			flowLog = messages[0]
			Expect(flowLog.NumFlowsStarted).Should(Equal(0))

			// Feeding an expiration of the conn.
			ca.FeedUpdate(muNoConn1Rule2DenyExpire)
			messages = ca.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))
			flowLog = messages[0]
			Expect(flowLog.NumFlowsCompleted).Should(Equal(1))
			Expect(flowLog.NumFlowsStarted).Should(Equal(0))
			Expect(flowLog.NumFlows).Should(Equal(1))

			// flowStore is now purged of the entry since the flowRef has been expired
			Expect(len(ca.flowStore)).Should(Equal(0))
		})

		It("Purges only the completed aggregated flowMetas", func() {
			By("Accounting for only the completed 5-tuple refs when making a purging decision")
			ca := NewFlowLogAggregator().AggregateOver(FlowPrefixName).(*flowLogAggregator)
			ca.FeedUpdate(muNoConn1Rule1AllowUpdateWithEndpointMeta)
			// Construct a similar update; same tuple but diff src ports.
			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy := muNoConn1Rule1AllowUpdateWithEndpointMeta
			tuple1Copy := tuple1
			// Everything can change in the 5-tuple except for the dst port.
			tuple1Copy.l4Src = 44123
			tuple1Copy.src = ipStrTo16Byte("10.0.0.3")
			tuple1Copy.dst = ipStrTo16Byte("10.0.0.9")
			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy.tuple = tuple1Copy

			// Updating the Workload IDs for src and dst.
			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy.srcEp = &calc.EndpointData{
				Key: model.WorkloadEndpointKey{
					Hostname:       "node-01",
					OrchestratorID: "k8s",
					WorkloadID:     "kube-system/iperf-4235-5434134",
					EndpointID:     "23456",
				},
				Endpoint: &model.WorkloadEndpoint{GenerateName: "iperf-4235-", Labels: map[string]string{"test-app": "true"}},
			}

			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy.dstEp = &calc.EndpointData{
				Key: model.WorkloadEndpointKey{
					Hostname:       "node-02",
					OrchestratorID: "k8s",
					WorkloadID:     "default/nginx-412354-6543645",
					EndpointID:     "256267",
				},
				Endpoint: &model.WorkloadEndpoint{GenerateName: "nginx-412354-", Labels: map[string]string{"k8s-app": "true"}},
			}

			ca.FeedUpdate(muNoConn1Rule1AllowUpdateWithEndpointMetaCopy)
			messages := ca.GetAndCalibrate(FlowPrefixName)
			// Two updates should still result in 1 flowMeta
			Expect(len(messages)).Should(Equal(1))
			// flowStore is not purged of the entry since the flowRefs havn't been expired
			Expect(len(ca.flowStore)).Should(Equal(1))
			// And the no. of Started Flows should be 2
			flowLog := messages[0]
			Expect(flowLog.NumFlowsStarted).Should(Equal(2))

			// Update one of the two flows and expire the other.
			ca.FeedUpdate(muNoConn1Rule1AllowUpdateWithEndpointMeta)
			muNoConn1Rule1AllowUpdateWithEndpointMetaCopy.updateType = UpdateTypeExpire
			ca.FeedUpdate(muNoConn1Rule1AllowUpdateWithEndpointMetaCopy)
			messages = ca.GetAndCalibrate(FlowPrefixName)
			Expect(len(messages)).Should(Equal(1))
			// flowStore still carries that 1 flowMeta
			Expect(len(ca.flowStore)).Should(Equal(1))
			flowLog = messages[0]
			Expect(flowLog.NumFlowsStarted).Should(Equal(0))
			Expect(flowLog.NumFlowsCompleted).Should(Equal(1))
			Expect(flowLog.NumFlows).Should(Equal(2))

			// Expire the sole flowRef
			muNoConn1Rule1AllowUpdateWithEndpointMeta.updateType = UpdateTypeExpire
			ca.FeedUpdate(muNoConn1Rule1AllowUpdateWithEndpointMeta)
			// Pre-purge/Dispatch the meta still lingers
			Expect(len(ca.flowStore)).Should(Equal(1))
			// On a dispatch the flowMeta is eventually purged
			messages = ca.GetAndCalibrate(FlowDefault)
			Expect(len(ca.flowStore)).Should(Equal(0))
			flowLog = messages[0]
			Expect(flowLog.NumFlowsStarted).Should(Equal(0))
			Expect(flowLog.NumFlowsCompleted).Should(Equal(1))
			Expect(flowLog.NumFlows).Should(Equal(1))
		})

		It("Updates the stats associated with the flows", func() {
			By("Accounting for only the packet/byte counts as seen during the interval")
			ca := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).(*flowLogAggregator)
			ca.FeedUpdate(muConn1Rule1AllowUpdate)
			messages := ca.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))
			// After the initial update the counts as expected.
			flowLog := messages[0]
			Expect(flowLog.PacketsIn).Should(Equal(2))
			Expect(flowLog.BytesIn).Should(Equal(22))
			Expect(flowLog.PacketsOut).Should(Equal(3))
			Expect(flowLog.BytesOut).Should(Equal(33))

			// The flow doesn't expire. But the Get should reset the stats.
			// A new update on top, then, should result in the same counts
			ca.FeedUpdate(muConn1Rule1AllowUpdate)
			messages = ca.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))
			// After the initial update the counts as expected.
			flowLog = messages[0]
			Expect(flowLog.PacketsIn).Should(Equal(2))
			Expect(flowLog.BytesIn).Should(Equal(22))
			Expect(flowLog.PacketsOut).Should(Equal(3))
			Expect(flowLog.BytesOut).Should(Equal(33))
		})
	})

	Context("FlowLogAggregator changes aggregation levels", func() {
		It("Adjusts aggregation levels", func() {
			var aggregator = NewFlowLogAggregator()
			aggregator.AggregateOver(FlowNoDestPorts)

			By("Changing the level to ")
			aggregator.AdjustLevel(FlowPrefixName)

			Expect(aggregator.HasAggregationLevelChanged()).Should(Equal(true))
			Expect(aggregator.GetCurrentAggregationLevel()).Should(Equal(FlowPrefixName))
			Expect(aggregator.GetDefaultAggregationLevel()).Should(Equal(FlowNoDestPorts))
		})
	})

	Context("Flow log aggregator process information", func() {

		It("Includes process information with default aggregation", func() {
			By("Creating an aggregator for allow")
			caa := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).AggregateOver(FlowDefault).IncludePolicies(true).IncludeProcess(true).PerFlowProcessLimit(2)

			By("Feeding update with process information")
			_ = caa.FeedUpdate(muWithProcessName)

			By("Checking calibration")
			messages := caa.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))
			flowLog := messages[0]

			dstMeta := EndpointMetadata{
				Type:           "wep",
				Namespace:      "default",
				Name:           "nginx-412354-5123451",
				AggregatedName: "nginx-412354-*",
			}

			expectedNumFlows := 1
			expectedNumFlowsStarted := 1
			expectedNumFlowsCompleted := 0

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut := calculatePacketStats(muWithProcessName)
			expectedFP := extractFlowPolicies(muWithProcessName)
			expectedFlowExtras := extractFlowExtras(muWithProcessName)
			expectedFlowProcessInfo := extractFlowProcessInfo(muWithProcessName)
			expectFlowLog(*flowLog, tuple1, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
		})

		It("Includes process information with default aggregation with different processIDs", func() {
			By("Creating an aggregator for allow")
			caa := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).AggregateOver(FlowDefault).IncludePolicies(true).IncludeProcess(true).PerFlowProcessLimit(2)

			By("Feeding update with process information")
			_ = caa.FeedUpdate(muWithProcessName)
			_ = caa.FeedUpdate(muWithProcessNameDifferentIDSameTuple)

			By("Checking calibration")
			messages := caa.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))
			flowLog := messages[0]

			dstMeta := EndpointMetadata{
				Type:           "wep",
				Namespace:      "default",
				Name:           "nginx-412354-5123451",
				AggregatedName: "nginx-412354-*",
			}

			expectedNumFlows := 1
			expectedNumFlowsStarted := 1
			expectedNumFlowsCompleted := 0

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut := calculatePacketStats(muWithProcessName, muWithSameProcessNameDifferentID)
			expectedFP := extractFlowPolicies(muWithProcessName, muWithSameProcessNameDifferentID)
			expectedFlowExtras := extractFlowExtras(muWithProcessName, muWithSameProcessNameDifferentID)
			expectedFlowProcessInfo := extractFlowProcessInfo(muWithProcessName, muWithSameProcessNameDifferentID)
			expectFlowLog(*flowLog, tuple1, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
		})

		It("Includes process information with default aggregation with different processIDs and expiration", func() {
			By("Creating an aggregator for allow")
			caa := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).AggregateOver(FlowDefault).IncludePolicies(true).IncludeProcess(true).PerFlowProcessLimit(2)

			By("Feeding update with process information")
			_ = caa.FeedUpdate(muWithProcessName)
			_ = caa.FeedUpdate(muWithProcessNameDifferentIDSameTuple)
			_ = caa.FeedUpdate(muWithProcessNameExpire)

			By("Checking calibration")
			messages := caa.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(1))
			flowLog := messages[0]

			dstMeta := EndpointMetadata{
				Type:           "wep",
				Namespace:      "default",
				Name:           "nginx-412354-5123451",
				AggregatedName: "nginx-412354-*",
			}

			expectedNumFlows := 1
			expectedNumFlowsStarted := 1
			expectedNumFlowsCompleted := 1

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut := calculatePacketStats(muWithProcessName, muWithSameProcessNameDifferentID, muWithProcessNameExpire)
			expectedFP := extractFlowPolicies(muWithProcessName, muWithSameProcessNameDifferentID, muWithProcessNameExpire)
			expectedFlowExtras := extractFlowExtras(muWithProcessName, muWithSameProcessNameDifferentID, muWithProcessNameExpire)
			expectedFlowProcessInfo := extractFlowProcessInfo(muWithProcessName, muWithSameProcessNameDifferentID, muWithProcessNameExpire)
			expectFlowLog(*flowLog, tuple1, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)

			messages = caa.GetAndCalibrate(FlowDefault)
			Expect(len(messages)).Should(Equal(0))
		})

		It("Includes process information with default aggregation with different process names", func() {
			By("Creating an aggregator for allow")
			caa := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).AggregateOver(FlowDefault).IncludePolicies(true).IncludeProcess(true).PerFlowProcessLimit(2)

			By("Feeding update with process information")
			_ = caa.FeedUpdate(muWithProcessName)
			_ = caa.FeedUpdate(muWithDifferentProcessNameDifferentID)
			_ = caa.FeedUpdate(muWithDifferentProcessNameDifferentIDExpire)

			By("Checking calibration")
			actualFlowLogs := caa.GetAndCalibrate(FlowDefault)
			Expect(len(actualFlowLogs)).Should(Equal(2))

			dstMeta := EndpointMetadata{
				Type:           "wep",
				Namespace:      "default",
				Name:           "nginx-412354-5123451",
				AggregatedName: "nginx-412354-*",
			}

			expectedFlowLogs := []FlowLog{}

			expectedNumFlows := 1
			expectedNumFlowsStarted := 1
			expectedNumFlowsCompleted := 0

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut := calculatePacketStats(muWithProcessName)
			expectedFP := extractFlowPolicies(muWithProcessName)
			expectedFlowExtras := extractFlowExtras(muWithProcessName)
			expectedFlowProcessInfo := extractFlowProcessInfo(muWithProcessName)
			expectedFlowLog := newExpectedFlowLog(tuple1, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
			expectedFlowLogs = append(expectedFlowLogs, expectedFlowLog)

			expectedNumFlows = 1
			expectedNumFlowsStarted = 1
			expectedNumFlowsCompleted = 1

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut = calculatePacketStats(muWithDifferentProcessNameDifferentID, muWithDifferentProcessNameDifferentIDExpire)
			expectedFP = extractFlowPolicies(muWithDifferentProcessNameDifferentID, muWithDifferentProcessNameDifferentIDExpire)
			expectedFlowExtras = extractFlowExtras(muWithDifferentProcessNameDifferentID, muWithDifferentProcessNameDifferentIDExpire)
			expectedFlowProcessInfo = extractFlowProcessInfo(muWithDifferentProcessNameDifferentID, muWithDifferentProcessNameDifferentIDExpire)
			expectedFlowLog = newExpectedFlowLog(tuple3, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
			expectedFlowLogs = append(expectedFlowLogs, expectedFlowLog)

			expectFlowLogsMatch(actualFlowLogs, expectedFlowLogs)

			By("Checking calibration and expired flows is removed")
			actualFlowLogs = caa.GetAndCalibrate(FlowDefault)
			Expect(len(actualFlowLogs)).Should(Equal(1))
		})

		It("Aggregates process information with pod prefix aggregation", func() {
			By("Creating an aggregator for allow")
			caa := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).AggregateOver(FlowPrefixName).IncludePolicies(true).IncludeProcess(true).PerFlowProcessLimit(2)

			By("Feeding update with process information")
			_ = caa.FeedUpdate(muWithProcessName2)
			_ = caa.FeedUpdate(muWithProcessName3)
			_ = caa.FeedUpdate(muWithProcessName4)
			_ = caa.FeedUpdate(muWithProcessName5)

			By("Checking calibration")
			actualFlowLogs := caa.GetAndCalibrate(FlowPrefixName)
			Expect(len(actualFlowLogs)).Should(Equal(3))

			dstMeta := EndpointMetadata{
				Type:           "wep",
				Namespace:      "default",
				Name:           "-",
				AggregatedName: "nginx-412354-*",
			}

			expectedFlowLogs := []FlowLog{}

			By("Constructing the first of three flowlogs")
			expectedNumFlows := 1
			expectedNumFlowsStarted := 1
			expectedNumFlowsCompleted := 0

			tuple3Aggregated := tuple3
			tuple3Aggregated.l4Src = -1
			tuple3Aggregated.src = [16]byte{}
			tuple3Aggregated.dst = [16]byte{}

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut := calculatePacketStats(muWithProcessName2)
			expectedFP := extractFlowPolicies(muWithProcessName2)
			expectedFlowExtras := extractFlowExtras(muWithProcessName2)
			expectedFlowProcessInfo := extractFlowProcessInfo(muWithProcessName2)
			expectedFlowLog := newExpectedFlowLog(tuple3Aggregated, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
			expectedFlowLogs = append(expectedFlowLogs, expectedFlowLog)

			By("Constructing the second of three flowlogs")
			expectedNumFlows = 1
			expectedNumFlowsStarted = 1
			expectedNumFlowsCompleted = 0

			tuple4Aggregated := tuple4
			tuple4Aggregated.l4Src = -1
			tuple4Aggregated.src = [16]byte{}
			tuple4Aggregated.dst = [16]byte{}

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut = calculatePacketStats(muWithProcessName3)
			expectedFP = extractFlowPolicies(muWithProcessName3)
			expectedFlowExtras = extractFlowExtras(muWithProcessName3)
			expectedFlowProcessInfo = extractFlowProcessInfo(muWithProcessName3)
			expectedFlowLog = newExpectedFlowLog(tuple4Aggregated, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
			expectedFlowLogs = append(expectedFlowLogs, expectedFlowLog)

			By("Constructing the third of three flowlogs")
			expectedNumFlows = 2
			expectedNumFlowsStarted = 2
			expectedNumFlowsCompleted = 0

			tuple5Aggregated := tuple5
			tuple5Aggregated.l4Src = -1
			tuple5Aggregated.src = [16]byte{}
			tuple5Aggregated.dst = [16]byte{}

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut = calculatePacketStats(muWithProcessName4, muWithProcessName5)
			expectedFP = extractFlowPolicies(muWithProcessName4, muWithProcessName5)
			expectedFlowExtras = extractFlowExtras(muWithProcessName4, muWithProcessName5)
			expectedFlowProcessInfo = extractFlowProcessInfo(muWithProcessName4, muWithProcessName5)
			expectedFlowLog = newExpectedFlowLog(tuple5Aggregated, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
			expectedFlowLogs = append(expectedFlowLogs, expectedFlowLog)

			expectFlowLogsMatch(actualFlowLogs, expectedFlowLogs)
		})

		It("Doesn't aggregate process information with default aggregation", func() {
			By("Creating an aggregator for allow")
			caa := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).AggregateOver(FlowDefault).IncludePolicies(true).IncludeProcess(true).PerFlowProcessLimit(2)

			By("Feeding update with process information")
			_ = caa.FeedUpdate(muWithProcessName2)
			_ = caa.FeedUpdate(muWithProcessName3)
			_ = caa.FeedUpdate(muWithProcessName4)
			_ = caa.FeedUpdate(muWithProcessName5)

			By("Checking calibration")
			actualFlowLogs := caa.GetAndCalibrate(FlowDefault)
			Expect(len(actualFlowLogs)).Should(Equal(4))

			dstMeta := EndpointMetadata{
				Type:           "wep",
				Namespace:      "default",
				Name:           "nginx-412354-5123451",
				AggregatedName: "nginx-412354-*",
			}

			expectedFlowLogs := []FlowLog{}

			By("Constructing the first of four flowlogs")
			expectedNumFlows := 1
			expectedNumFlowsStarted := 1
			expectedNumFlowsCompleted := 0

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut := calculatePacketStats(muWithProcessName2)
			expectedFP := extractFlowPolicies(muWithProcessName2)
			expectedFlowExtras := extractFlowExtras(muWithProcessName2)
			expectedFlowProcessInfo := extractFlowProcessInfo(muWithProcessName2)
			expectedFlowLog := newExpectedFlowLog(tuple3, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
			expectedFlowLogs = append(expectedFlowLogs, expectedFlowLog)

			By("Constructing the second of four flowlogs")

			expectedNumFlows = 1
			expectedNumFlowsStarted = 1
			expectedNumFlowsCompleted = 0

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut = calculatePacketStats(muWithProcessName3)
			expectedFP = extractFlowPolicies(muWithProcessName3)
			expectedFlowExtras = extractFlowExtras(muWithProcessName3)
			expectedFlowProcessInfo = extractFlowProcessInfo(muWithProcessName3)
			expectedFlowLog = newExpectedFlowLog(tuple4, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
			expectedFlowLogs = append(expectedFlowLogs, expectedFlowLog)

			By("Constructing the third of four flowlogs")

			expectedNumFlows = 1
			expectedNumFlowsStarted = 1
			expectedNumFlowsCompleted = 0

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut = calculatePacketStats(muWithProcessName4)
			expectedFP = extractFlowPolicies(muWithProcessName4)
			expectedFlowExtras = extractFlowExtras(muWithProcessName4)
			expectedFlowProcessInfo = extractFlowProcessInfo(muWithProcessName4)
			expectedFlowLog = newExpectedFlowLog(tuple5, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
			expectedFlowLogs = append(expectedFlowLogs, expectedFlowLog)

			By("Constructing the fourth of four flowlogs")
			expectedNumFlows = 1
			expectedNumFlowsStarted = 1
			expectedNumFlowsCompleted = 0

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut = calculatePacketStats(muWithProcessName5)
			expectedFP = extractFlowPolicies(muWithProcessName5)
			expectedFlowExtras = extractFlowExtras(muWithProcessName5)
			expectedFlowProcessInfo = extractFlowProcessInfo(muWithProcessName5)
			expectedFlowLog = newExpectedFlowLog(tuple6, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
			expectedFlowLogs = append(expectedFlowLogs, expectedFlowLog)

			expectFlowLogsMatch(actualFlowLogs, expectedFlowLogs)
		})

		It("Aggregates process information with source port aggregation", func() {
			By("Creating an aggregator for allow")
			caa := NewFlowLogAggregator().ForAction(rules.RuleActionAllow).AggregateOver(FlowSourcePort).IncludePolicies(true).IncludeProcess(true).PerFlowProcessLimit(2)

			By("Feeding update with process information")
			_ = caa.FeedUpdate(muWithProcessName2)
			_ = caa.FeedUpdate(muWithProcessName3)
			_ = caa.FeedUpdate(muWithProcessName4)
			_ = caa.FeedUpdate(muWithProcessName5)

			By("Checking calibration")
			actualFlowLogs := caa.GetAndCalibrate(FlowPrefixName)
			Expect(len(actualFlowLogs)).Should(Equal(3))

			dstMeta := EndpointMetadata{
				Type:           "wep",
				Namespace:      "default",
				Name:           "nginx-412354-5123451",
				AggregatedName: "nginx-412354-*",
			}

			expectedFlowLogs := []FlowLog{}

			expectedNumFlows := 1
			expectedNumFlowsStarted := 1
			expectedNumFlowsCompleted := 0

			tuple3Aggregated := tuple3
			tuple3Aggregated.l4Src = -1

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut := calculatePacketStats(muWithProcessName2)
			expectedFP := extractFlowPolicies(muWithProcessName2)
			expectedFlowExtras := extractFlowExtras(muWithProcessName2)
			expectedFlowProcessInfo := extractFlowProcessInfo(muWithProcessName2)
			expectedFlowLog := newExpectedFlowLog(tuple3Aggregated, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
			expectedFlowLogs = append(expectedFlowLogs, expectedFlowLog)

			expectedNumFlows = 1
			expectedNumFlowsStarted = 1
			expectedNumFlowsCompleted = 0

			tuple4Aggregated := tuple4
			tuple4Aggregated.l4Src = -1

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut = calculatePacketStats(muWithProcessName3)
			expectedFP = extractFlowPolicies(muWithProcessName3)
			expectedFlowExtras = extractFlowExtras(muWithProcessName3)
			expectedFlowProcessInfo = extractFlowProcessInfo(muWithProcessName3)
			expectedFlowLog = newExpectedFlowLog(tuple4Aggregated, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
			expectedFlowLogs = append(expectedFlowLogs, expectedFlowLog)

			expectedNumFlows = 2
			expectedNumFlowsStarted = 2
			expectedNumFlowsCompleted = 0

			tuple5Aggregated := tuple5
			tuple5Aggregated.l4Src = -1

			expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut = calculatePacketStats(muWithProcessName4, muWithProcessName5)
			expectedFP = extractFlowPolicies(muWithProcessName4, muWithProcessName5)
			expectedFlowExtras = extractFlowExtras(muWithProcessName4, muWithProcessName5)
			expectedFlowProcessInfo = extractFlowProcessInfo(muWithProcessName4, muWithProcessName5)
			expectedFlowLog = newExpectedFlowLog(tuple5Aggregated, expectedNumFlows, expectedNumFlowsStarted, expectedNumFlowsCompleted, FlowLogActionAllow, FlowLogReporterDst,
				expectedPacketsIn, expectedPacketsOut, expectedBytesIn, expectedBytesOut, pvtMeta, dstMeta, noService, nil, nil, expectedFP, expectedFlowExtras, expectedFlowProcessInfo)
			expectedFlowLogs = append(expectedFlowLogs, expectedFlowLog)

			expectFlowLogsMatch(actualFlowLogs, expectedFlowLogs)
		})

	})
})
