// Copyright (c) 2020 Tigera, Inc. All rights reserved.

package felixclient

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tigera/envoy-collector/pkg/collector"
	"github.com/tigera/envoy-collector/proto"
)

var (
	httpLog = collector.EnvoyLog{
		Reporter:      "destination",
		StartTime:     "2020-11-24T22:24:29.237Z",
		Duration:      3,
		ResponseCode:  200,
		BytesSent:     33,
		BytesReceived: 0,
		UserAgent:     "curl/7.68.0",
		RequestPath:   "/ip",
		RequestMethod: "GET",
		RequestId:     "e23c0019-36b7-4142-8e86",

		DSRemoteAddress: "192.168.138.208:34368",
		DSLocalAddress:  "192.168.35.210:80",
		// 5 tuple data
		Type:    "HTTP/1.1",
		SrcIp:   "192.168.138.2",
		DstIp:   "192.168.35.210",
		SrcPort: int32(34368),
		DstPort: int32(80),
	}
	httpLog1 = collector.EnvoyLog{
		Reporter:      "destination",
		StartTime:     "2020-11-24T22:24:29.237Z",
		Duration:      3,
		ResponseCode:  501,
		BytesSent:     33,
		BytesReceived: 0,
		UserAgent:     "curl/7.68.0",
		RequestPath:   "/ip",
		RequestMethod: "GET",
		RequestId:     "e23c0019-36b7-4142-8e860019-36b7-4142",

		DSRemoteAddress: "192.168.138.208:34368",
		DSLocalAddress:  "192.168.35.210:80",
		// 5 tuple data
		Type:    "HTTP/1.1",
		SrcIp:   "193.16.18.264",
		DstIp:   "192.168.35.210",
		SrcPort: int32(56748),
		DstPort: int32(8080),
	}
)

var _ = Describe("Felix Client Converting single EnvoyLog to DataplaneStats test", func() {
	testClient := &felixClient{}
	Context("With a log with all fields filled in", func() {
		It("Should create dataplane stats with the correct fields", func() {
			dpStats := testClient.dataplaneStatsFromL7Log(httpLog)
			httpData := dpStats.HttpData[0]
			Expect(dpStats.SrcIp).To(Equal(httpLog.SrcIp))
			Expect(dpStats.DstIp).To(Equal(httpLog.DstIp))
			Expect(dpStats.SrcPort).To(Equal(httpLog.SrcPort))
			Expect(dpStats.DstPort).To(Equal(httpLog.DstPort))
			// protocol should be tcp even when it's not passed in the log
			Expect(dpStats.Protocol).To(Equal(&proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}}))
			Expect(httpData.Type).To(Equal(httpLog.Type))
			Expect(httpData.RequestMethod).To(Equal(httpLog.RequestMethod))
			Expect(httpData.UserAgent).To(Equal(httpLog.UserAgent))
			Expect(httpData.BytesSent).To(Equal(httpLog.BytesSent))
			Expect(httpData.Duration).To(Equal(httpLog.Duration))
			Expect(httpData.ResponseCode).To(Equal(httpLog.ResponseCode))
			Expect(httpData.BytesReceived).To(Equal(httpLog.BytesReceived))
			Expect(httpData.RequestPath).To(Equal(httpLog.RequestPath))
		})
	})
})

var _ = Describe("Felix Client batching multiple EnvoyLogs to DataplaneStats", func() {
	testClient := &felixClient{}
	Context("when same 5 tuple EnvoyLogs are passed in envoy collector", func() {
		logs := map[string]collector.EnvoyLog{"log1": httpLog, "log2": httpLog}
		info := collector.EnvoyInfo{
			Logs: logs,
		}
		It("It Should create a single DataplaneStat with multiple HttpData objects", func() {
			data := testClient.batchAndConvertEnvoyLogs(info)
			value, found := data[collector.TupleKey{
				SrcIp:   "192.168.138.2",
				DstIp:   "192.168.35.210",
				SrcPort: 34368,
				DstPort: 80,
				Type:    "HTTP/1.1",
			}]
			Expect(len(data)).To(Equal(1))
			Expect(found).To(Equal(true))
			Expect(len(value.HttpData)).To(Equal(2))
		})
	})
	Context("when distinct 5 tuple EnvoyLogs are passed in envoy collector", func() {
		logs := map[string]collector.EnvoyLog{"log1": httpLog, "log2": httpLog1, "log3": httpLog1}
		info := collector.EnvoyInfo{
			Logs: logs,
		}
		It("It Should create as many logs as distinct 5 tuple logs passed", func() {
			data := testClient.batchAndConvertEnvoyLogs(info)
			Expect(len(data)).To(Equal(2))
			value, found := data[collector.TupleKey{
				SrcIp:   "192.168.138.2",
				DstIp:   "192.168.35.210",
				SrcPort: 34368,
				DstPort: 80,
				Type:    "HTTP/1.1",
			}]
			Expect(found).To(Equal(true))
			Expect(len(value.HttpData)).To(Equal(1))

			value2, found := data[collector.TupleKey{
				SrcIp:   "193.16.18.264",
				DstIp:   "192.168.35.210",
				SrcPort: 56748,
				DstPort: 8080,
				Type:    "HTTP/1.1",
			}]
			Expect(found).To(Equal(true))
			Expect(len(value2.HttpData)).To(Equal(2))

		})
	})
})
