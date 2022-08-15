// Copyright (c) 2020 Tigera, Inc. All rights reserved.

package collector

import (
	"context"

	"github.com/tigera/l7-collector/pkg/config"
)

const (
	ProtocolTCP string = "tcp"
)

const (
	LogTypeTCP string = "tcp"
	LogTypeTLS string = "tls"
)

type EnvoyCollector interface {
	ReadLogs(context.Context)
	Report() <-chan EnvoyInfo
	ParseRawLogs(string) (EnvoyLog, error)
}

func NewEnvoyCollector(cfg *config.Config) EnvoyCollector {
	// Currently it will only return a log file collector but
	// this should inspect the config to return other collectors
	// once they need to be implemented.
	return EnvoyCollectorNew(cfg)
}

type EnvoyInfo struct {
	Logs        map[EnvoyLogKey]EnvoyLog
	Connections map[TupleKey]int
}

type EnvoyLog struct {
	// some of the fields are relevant for collector only and are not sent to felix Ex. RequestId, StartTime etc
	// for the information that is sent to felix check HttpData proto
	Reporter      string `json:"reporter"`
	StartTime     string `json:"start_time"`
	Duration      int32  `json:"duration"`
	ResponseCode  int32  `json:"response_code"`
	BytesSent     int32  `json:"bytes_sent"`
	BytesReceived int32  `json:"bytes_received"`
	UserAgent     string `json:"user_agent"`
	RequestPath   string `json:"request_path"`
	RequestMethod string `json:"request_method"`
	RequestId     string `json:"request_id"`
	Type          string `json:"type"`
	Domain        string `json:"domain"`

	// these are the addresses we extract 5 tuple information from
	DSRemoteAddress string `json:"downstream_remote_address"`
	DSLocalAddress  string `json:"downstream_local_address"`

	// used to calculate latency
	UpstreamServiceTime string `json:"upstream_service_time"`

	Protocol    string `json:"protocol"`
	SrcIp       string
	DstIp       string
	SrcPort     int32
	DstPort     int32
	Count       int32
	DurationMax int32
	Latency     int32
}

// TupleKey is an object just for holding the
// Envoy log's 5 tuple information. Since the protocol is always tcp its replaced by type
type TupleKey struct {
	SrcIp   string
	DstIp   string
	SrcPort int32
	DstPort int32
	Type    string
}

// EnvoyLogKey is an object that contains all the distinct information we get from logs
// used as a key for de-duplication for http and tcp logs
type EnvoyLogKey struct {
	TupleKey TupleKey

	UserAgent     string
	RequestPath   string
	RequestMethod string
	ResponseCode  int32
	Domain        string
}

type BatchEnvoyLog struct {
	logs map[EnvoyLogKey]EnvoyLog
	size int
}

func NewBatchEnvoyLog(size int) *BatchEnvoyLog {
	return &BatchEnvoyLog{
		logs: make(map[EnvoyLogKey]EnvoyLog),
		size: size,
	}
}

func (b BatchEnvoyLog) Insert(log EnvoyLog) {
	logKey := GetEnvoyLogKey(log)
	// for tcp and tls types we don't get much information so we treat this as a single connection and
	// add the duration, bytes_sent, bytes_received.
	// same goes for cases where http logs comes with same EnvoyLogKey (same l7 fields) for multiple requests
	// this happens even when the batch is full
	if val, ok := b.logs[logKey]; ok {
		// set max duration per request level
		if log.Duration > val.DurationMax {
			val.DurationMax = log.Duration
		}

		val.Duration = val.Duration + log.Duration
		val.Latency = val.Latency + log.Latency
		val.BytesReceived = val.BytesReceived + log.BytesReceived
		val.BytesSent = val.BytesSent + log.BytesSent
		val.Count++
		b.logs[logKey] = val
	} else {
		// add unique logs ony to the batch, if there is space otherwise we drop it
		if !b.Full() {
			log.Count = 1
			log.DurationMax = log.Duration
			b.logs[logKey] = log
		}
	}
}

func GetEnvoyLogKey(log EnvoyLog) EnvoyLogKey {
	// We create a key using all the distinct values we get for each type
	// for TCP it's just the 5 tuple information, for http we include all the other l7 info fields
	return EnvoyLogKey{
		TupleKey:      TupleKeyFromEnvoyLog(log),
		UserAgent:     log.UserAgent,
		RequestPath:   log.RequestPath,
		RequestMethod: log.RequestMethod,
		ResponseCode:  log.ResponseCode,
		Domain:        log.Domain,
	}
}

func (b BatchEnvoyLog) Full() bool {
	if b.size < 0 {
		return false
	}
	return len(b.logs) == b.size
}

func TupleKeyFromEnvoyLog(log EnvoyLog) TupleKey {
	return TupleKey{
		SrcIp:   log.SrcIp,
		DstIp:   log.DstIp,
		SrcPort: log.SrcPort,
		DstPort: log.DstPort,
		Type:    log.Type,
	}
}

func EnvoyLogFromTupleKey(key TupleKey) EnvoyLog {
	return EnvoyLog{
		SrcIp:    key.SrcIp,
		DstIp:    key.DstIp,
		SrcPort:  key.SrcPort,
		DstPort:  key.DstPort,
		Type:     key.Type,
		Protocol: ProtocolTCP,
	}
}
