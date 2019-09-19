// Copyright 2019 Tigera Inc. All rights reserved.

package events

import (
	"github.com/tigera/intrusion-detection/controller/pkg/elastic"
)

// Based on github.com/tigera/felix-private/collector/dns_log_types.go

type DNSLog struct {
	StartTime       elastic.Time      `json:"start_time"`
	EndTime         elastic.Time      `json:"end_time"`
	Type            string            `json:"type"`
	Count           uint              `json:"count"`
	ClientName      string            `json:"client_name"`
	ClientNameAggr  string            `json:"client_name_aggr"`
	ClientNamespace string            `json:"client_namespace"`
	ClientIP        *string           `json:"client_ip"`
	ClientLabels    map[string]string `json:"client_labels"`
	Servers         []DNSServer       `json:"servers"`
	QName           string            `json:"qname"`
	QClass          string            `json:"qclass"`
	QType           string            `json:"qtype"`
	RCode           string            `json:"rcode"`
	RRSets          []DNSRRSet        `json:"rrsets"`
}

type DNSServer struct {
	Name      string `json:"name"`
	NameAggr  string `json:"name_aggr"`
	Namespace string `json:"namespace"`
	IP        string `json:"ip"`
}

type DNSRRSet struct {
	Name  string      `json:"name"`
	Class interface{} `json:"class"`
	Type  interface{} `json:"type"`
	RData []string    `json:"rdata"`
}
