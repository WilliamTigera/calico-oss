// Copyright (c) 2018-2021 Tigera, Inc. All rights reserved.

package collector

import (
	"time"

	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/projectcalico/felix/collector/testutil"
)

var _ = Describe("CloudWatch Dispatcher verification", func() {
	var (
		batcher *cloudWatchEventsBatcher
	)

	Context("Events batcher", func() {
		msg1 := "hello"
		msg2 := "world"
		It("Batches events", func() {
			By("flushing out events even when total input array size less than batch size")
			testBatchSize := 50 // bytes
			batchChan := make(chan eventsBatch)
			batcher = newCloudWatchEventsBatcher(testBatchSize, batchChan)
			storeMsg := map[string]bool{msg1: true, msg2: true}
			go batcher.batch([]*string{&msg1, &msg2})

			batchCount := 0
			for {
				events, more := <-batcher.eventsBatchChan
				if !more {
					break
				}
				batchCount++
				for _, e := range events {
					delete(storeMsg, *e.Message)
				}
			}
			// At the end of it, all messages should have been seen
			Expect(len(storeMsg)).Should(Equal(0))
			Expect(batchCount).Should(Equal(1))

			By("flushing out events in batches when total input array size more than batch size")
			testBatchSize = 5 // bytes
			batchChan = make(chan eventsBatch)
			batcher = newCloudWatchEventsBatcher(testBatchSize, batchChan)
			storeMsg = map[string]bool{msg1: true, msg2: true}
			go batcher.batch([]*string{&msg1, &msg2})

			batchCount = 0
			for {
				events, more := <-batcher.eventsBatchChan
				if !more {
					break
				}
				batchCount++
				for _, e := range events {
					delete(storeMsg, *e.Message)
				}
			}
			// At the end of it, all messages should have been seen
			Expect(len(storeMsg)).Should(Equal(0))
			Expect(batchCount).Should(Equal(2))
		})
	})

	Context("Events Uploader", func() {
		It("Uploads batches to cloudwatchlogs", func() {
			msg1 := "hello"
			msg2 := "world"
			By("Multiple batches")
			cl := testutil.NewMockedCloudWatchLogsClient(logGroupName)
			cd := NewCloudWatchDispatcher(logGroupName, logStreamName, 7, cl).(*cloudWatchDispatcher)

			testBatchSize := 5 // bytes
			batchChan := make(chan eventsBatch)
			batcher := newCloudWatchEventsBatcher(testBatchSize, batchChan)
			storeMsg := map[string]bool{msg1: true, msg2: true}
			go batcher.batch([]*string{&msg1, &msg2})
			cd.uploadEventsBatches(batchChan)

			logEventsInput := &cloudwatchlogs.GetLogEventsInput{
				LogGroupName:  &logGroupName,
				LogStreamName: &logStreamName,
			}
			logEventsOutput, _ := cl.GetLogEvents(logEventsInput)

			for _, e := range logEventsOutput.Events {
				delete(storeMsg, *e.Message)
			}
			// At the end of it, all messages should have been seen
			Expect(len(storeMsg)).Should(Equal(0))

		})
	})

	Context("FlowLog Serialization", func() {
		var flowStats FlowStats
		var flowLabels FlowLabels
		var flowPolicies FlowPolicies
		var flowExtras flowExtrasRef
		var flowLog, expectedFlowLog string
		var flowMeta FlowMeta
		var err error

		It("generates the correct FlowLog string", func() {
			flowStats = FlowStats{}
			startTime := time.Date(2017, 11, 17, 20, 1, 0, 0, time.UTC)
			endTime := time.Date(2017, 11, 17, 20, 2, 0, 0, time.UTC)

			By("skipping aggregation, not including service")
			flowMeta, err = NewFlowMeta(muWithEndpointMetaWithService, FlowDefault, false)
			Expect(err).To(BeNil())
			flowLabels = FlowLabels{}
			flowPolicies = make(FlowPolicies)
			flowExtras = flowExtrasRef{}
			flowLog = testSerialize(flowMeta, flowLabels, flowPolicies, &flowStats, flowExtras, startTime, endTime, false, false)
			expectedFlowLog = "1510948860 1510948920 wep kube-system iperf-4235-5623461 iperf-4235-* - wep default nginx-412354-5123451 nginx-412354-* - 10.0.0.1 20.0.0.1 6 54123 80 0 0 0 dst 0 0 0 0 allow - - 0 - - - - 0 - 0 0 0 0 0 0 0 0 0 0 0 0"
			Expect(flowLog).Should(Equal(expectedFlowLog))

			flowLabels = FlowLabels{SrcLabels: map[string]string{"test-app": "true"}, DstLabels: map[string]string{"k8s-app": "true"}}
			flowLog = testSerialize(flowMeta, flowLabels, flowPolicies, &flowStats, flowExtras, startTime, endTime, true, false)
			expectedFlowLog = "1510948860 1510948920 wep kube-system iperf-4235-5623461 iperf-4235-* [test-app=true] wep default nginx-412354-5123451 nginx-412354-* [k8s-app=true] 10.0.0.1 20.0.0.1 6 54123 80 0 0 0 dst 0 0 0 0 allow - - 0 - - - - 0 - 0 0 0 0 0 0 0 0 0 0 0 0"
			Expect(flowLog).Should(Equal(expectedFlowLog))

			By("aggregating on source port")
			flowMeta, err = NewFlowMeta(muWithEndpointMetaWithService, FlowSourcePort, true)
			Expect(err).To(BeNil())
			flowLabels = FlowLabels{}
			flowLog = testSerialize(flowMeta, flowLabels, flowPolicies, &flowStats, flowExtras, startTime, endTime, false, false)
			expectedFlowLog = "1510948860 1510948920 wep kube-system iperf-4235-5623461 iperf-4235-* - wep default nginx-412354-5123451 nginx-412354-* - 10.0.0.1 20.0.0.1 6 - 80 0 0 0 dst 0 0 0 0 allow - - 0 foo-ns foo-svc foo-port - 0 - 0 0 0 0 0 0 0 0 0 0 0 0"
			Expect(flowLog).Should(Equal(expectedFlowLog))

			flowLabels = FlowLabels{SrcLabels: map[string]string{"test-app": "true"}, DstLabels: map[string]string{"k8s-app": "true"}}
			flowLog = testSerialize(flowMeta, flowLabels, flowPolicies, &flowStats, flowExtras, startTime, endTime, true, false)
			expectedFlowLog = "1510948860 1510948920 wep kube-system iperf-4235-5623461 iperf-4235-* [test-app=true] wep default nginx-412354-5123451 nginx-412354-* [k8s-app=true] 10.0.0.1 20.0.0.1 6 - 80 0 0 0 dst 0 0 0 0 allow - - 0 foo-ns foo-svc foo-port - 0 - 0 0 0 0 0 0 0 0 0 0 0 0"
			Expect(flowLog).Should(Equal(expectedFlowLog))

			By("aggregating on prefix name")
			flowMeta, err = NewFlowMeta(muWithEndpointMeta, FlowPrefixName, true)
			Expect(err).To(BeNil())
			flowLabels = FlowLabels{}
			flowLog = testSerialize(flowMeta, flowLabels, flowPolicies, &flowStats, flowExtras, startTime, endTime, false, false)
			expectedFlowLog = "1510948860 1510948920 wep kube-system - iperf-4235-* - wep default - nginx-412354-* - - - 6 - 80 0 0 0 dst 0 0 0 0 allow - - 0 - - - - 0 - 0 0 0 0 0 0 0 0 0 0 0 0"
			Expect(flowLog).Should(Equal(expectedFlowLog))

			flowMeta, err = NewFlowMeta(muWithoutSrcEndpointMeta, FlowPrefixName, true)
			Expect(err).To(BeNil())
			flowLog = testSerialize(flowMeta, flowLabels, flowPolicies, &flowStats, flowExtras, startTime, endTime, false, false)
			expectedFlowLog = "1510948860 1510948920 net - - pvt - wep default - nginx-412354-* - - - 6 - 80 0 0 0 dst 0 0 0 0 allow - - 0 - - - - 0 - 0 0 0 0 0 0 0 0 0 0 0 0"
			Expect(flowLog).Should(Equal(expectedFlowLog))

			muWithoutPublicDstEndpointMeta := muWithoutDstEndpointMeta
			muWithoutPublicDstEndpointMeta.tuple.dst = ipStrTo16Byte("198.17.8.43")
			flowMeta, err = NewFlowMeta(muWithoutPublicDstEndpointMeta, FlowPrefixName, true)
			Expect(err).To(BeNil())
			flowLog = testSerialize(flowMeta, flowLabels, flowPolicies, &flowStats, flowExtras, startTime, endTime, false, false)
			expectedFlowLog = "1510948860 1510948920 wep kube-system - iperf-4235-* - net - - pub - - - 6 - 80 0 0 0 dst 0 0 0 0 allow - - 0 - - - - 0 - 0 0 0 0 0 0 0 0 0 0 0 0"
			Expect(flowLog).Should(Equal(expectedFlowLog))

			By("aggregating on prefix name and reporting original source IPs")
			flowMeta, err = NewFlowMeta(muWithOrigSourceIPs, FlowPrefixName, true)
			Expect(err).To(BeNil())
			flowExtras = NewFlowExtrasRef(muWithOrigSourceIPs, testMaxBoundedSetSize)
			flowLog = testSerialize(flowMeta, flowLabels, flowPolicies, &flowStats, flowExtras, startTime, endTime, false, false)
			expectedFlowLog = "1510948860 1510948920 net - - pvt - wep default - nginx-412354-* - - - 6 - 80 0 0 0 dst 0 0 0 0 allow - [1.0.0.1] 1 - - - - 0 - 0 0 0 0 0 0 0 0 0 0 0 0"
			Expect(flowLog).Should(Equal(expectedFlowLog))
		})
	})
})

func testSerialize(fm FlowMeta, fl FlowLabels, fp FlowPolicies, fs *FlowStats, fe flowExtrasRef, st, et time.Time, labels bool, policies bool) string {
	psbn := make(map[string]*FlowStats)
	psbn["-"] = fs

	fsbyp := FlowStatsByProcess{
		statsByProcessName: psbn,
	}
	spec := &FlowSpec{
		FlowStatsByProcess: fsbyp,
		flowExtrasRef:      fe,
		FlowLabels:         fl,
		FlowPolicies:       fp,
	}
	flows := spec.ToFlowLogs(fm, st, et, labels, policies)
	return serializeCloudWatchFlowLog(flows[0])
}
