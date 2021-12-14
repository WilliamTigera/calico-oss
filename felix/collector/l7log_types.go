// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package collector

import (
	"fmt"
	"time"

	"github.com/projectcalico/felix/calc"
)

// L7 Update represents the data that is sent to us straight from the envoy logs?
type L7Update struct {
	Tuple Tuple
	SrcEp *calc.EndpointData
	DstEp *calc.EndpointData

	Duration      int
	DurationMax   int
	BytesReceived int
	BytesSent     int

	ServiceName      string
	ServiceNamespace string
	ServicePortName  string
	ServicePortNum   int

	ResponseCode string
	Method       string
	Domain       string
	Path         string
	UserAgent    string
	Type         string
	Count        int
}

// L7Log represents the  the JSON representation of a L7Log we are pushing to fluentd/elastic.
type L7Log struct {
	StartTime    int64         `json:"start_time"`
	EndTime      int64         `json:"end_time"`
	DurationMean time.Duration `json:"duration_mean"`
	DurationMax  time.Duration `json:"duration_max"`
	BytesIn      int           `json:"bytes_in"`
	BytesOut     int           `json:"bytes_out"`
	Count        int           `json:"count"`

	// TODO: make a breaking change on the elastic schema + re-index to change these src_ fields to be prefixed by source_
	SourceNameAggr  string              `json:"src_name_aggr"` // aliased as source_name_aggr on ee_l7.template
	SourceNamespace string              `json:"src_namespace"` // aliased as source_namespace on ee_l7.template
	SourceType      FlowLogEndpointType `json:"src_type"`      // aliased as source_type on ee_l7.template
	SourcePortNum   int                 `json:"source_port_num"`

	DestNameAggr  string              `json:"dest_name_aggr"`
	DestNamespace string              `json:"dest_namespace"`
	DestType      FlowLogEndpointType `json:"dest_type"`
	DestPortNum   int                 `json:"dest_port_num"`

	DestServiceName      string `json:"dest_service_name"`
	DestServiceNamespace string `json:"dest_service_namespace"`
	// TODO: make a breaking change on the elastic schema + re-index to change this field to dest_service_port_name
	DestServicePortName string `json:"dest_service_port_name"`
	DestServicePortNum  int    `json:"dest_service_port"` // aliased as dest_service_port_num on ee_l7.template

	Method       string `json:"method"`
	UserAgent    string `json:"user_agent"`
	URL          string `json:"url"`
	ResponseCode string `json:"response_code"`
	Type         string `json:"type"`
}

// L7Meta represents the identifiable information for an L7 log.
type L7Meta struct {
	SrcNameAggr   string
	SrcNamespace  string
	SrcType       FlowLogEndpointType
	SourcePortNum int

	DestNameAggr  string
	DestNamespace string
	DestType      FlowLogEndpointType
	DestPortNum   int

	ServiceName      string
	ServiceNamespace string
	ServicePortName  string
	ServicePortNum   int

	ResponseCode string
	Method       string
	Domain       string
	Path         string
	UserAgent    string
	Type         string
}

// L7Spec represents the stats and collections of L7 data
type L7Spec struct {
	Duration      int
	DurationMax   int
	BytesReceived int
	BytesSent     int
	Count         int
}

func (a *L7Spec) Merge(b L7Spec) {
	a.Duration = a.Duration + b.Duration
	if b.DurationMax > a.DurationMax {
		a.DurationMax = b.DurationMax
	}
	a.BytesReceived = a.BytesReceived + b.BytesReceived
	a.BytesSent = a.BytesSent + b.BytesSent
	a.Count = a.Count + b.Count
}

type L7Data struct {
	L7Meta
	L7Spec
}

const (
	L7LogTypeUnLogged string = "unlogged"
)

func (ld L7Data) ToL7Log(startTime, endTime time.Time) *L7Log {
	res := &L7Log{
		StartTime: startTime.Unix(),
		EndTime:   endTime.Unix(),
		BytesIn:   ld.BytesReceived,
		BytesOut:  ld.BytesSent,
		Count:     ld.Count,

		SourceNameAggr:  ld.SrcNameAggr,
		SourceNamespace: ld.SrcNamespace,
		SourceType:      ld.SrcType,
		SourcePortNum:   ld.SourcePortNum,

		DestNameAggr:  ld.DestNameAggr,
		DestNamespace: ld.DestNamespace,
		DestType:      ld.DestType,
		DestPortNum:   ld.DestPortNum,

		DestServiceName:      ld.ServiceName,
		DestServiceNamespace: ld.ServiceNamespace,
		DestServicePortName:  ld.ServicePortName,
		DestServicePortNum:   ld.ServicePortNum,

		Method:       ld.Method,
		UserAgent:    ld.UserAgent,
		ResponseCode: ld.ResponseCode,
		Type:         ld.Type,
	}

	// Calculate and convert durations
	res.DurationMean = (time.Duration(ld.Duration) * time.Millisecond) / time.Duration(ld.Count)
	res.DurationMax = time.Duration(ld.DurationMax) * time.Millisecond

	// Create the URL from the domain and path
	// Path is expected to have a leading "/" character.
	if ld.Domain != flowLogFieldNotIncluded && ld.Path != flowLogFieldNotIncluded {
		res.URL = fmt.Sprintf("%s%s", ld.Domain, ld.Path)
	} else if ld.Domain != flowLogFieldNotIncluded && ld.Path == flowLogFieldNotIncluded {
		res.URL = ld.Domain
	}

	return res
}
