// Copyright (c) 2020 Tigera, Inc. All rights reserved.
package api

import (
	"strconv"

	"github.com/sirupsen/logrus"
	apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	"github.com/tigera/api/pkg/lib/numorstring"

	"github.com/projectcalico/calico/libcalico-go/lib/net"
	lapi "github.com/projectcalico/calico/linseed/pkg/apis/v1"
)

const (
	FlowlogBuckets = "flog_buckets"
)

const (
	FlowLogGlobalNamespace        = "-"
	FlowLogEndpointTypeInvalid    = ""
	FlowLogEndpointTypeWEP        = "wep"
	FlowLogEndpointTypeHEP        = "hep"
	FlowLogEndpointTypeNetworkSet = "ns"
	FlowLogEndpointTypeNetwork    = "net"
	FlowLogNetworkPublic          = "pub"
	FlowLogNetworkPrivate         = "pvt"
)

// Container type to hold the EndpointsReportFlow and/or an error.
type FlowLogResult struct {
	*apiv3.EndpointsReportFlow
	Err error
}

type EndpointType string

const (
	GlobalEndpointType = "-"

	EndpointTypeInvalid EndpointType = ""
	EndpointTypeWep     EndpointType = "wep"
	EndpointTypeHep     EndpointType = "hep"
	EndpointTypeNs      EndpointType = "ns"
	EndpointTypeNet     EndpointType = "net"
)

func StringToEndpointType(str string) EndpointType {
	for _, et := range []EndpointType{EndpointTypeWep, EndpointTypeHep, EndpointTypeNs, EndpointTypeNet} {
		if str == string(et) {
			return et
		}
	}

	return EndpointTypeInvalid
}

type ReporterType string

const (
	ReporterTypeInvalid     ReporterType = ""
	ReporterTypeSource      ReporterType = "src"
	ReporterTypeDestination ReporterType = "dst"
)

func FromLinseedFlow(lsf lapi.L3Flow) *Flow {
	// Ports and protocol.
	srcPort := uint16(lsf.Key.Source.Port)
	dstPort := uint16(lsf.Key.Destination.Port)

	// If the protocol already parses as an int, just
	// use that.
	var proto *uint8
	pn, err := strconv.Atoi(lsf.Key.Protocol)
	if err != nil {
		p := numorstring.ProtocolFromString(lsf.Key.Protocol)
		proto = GetProtocolNumber(&p)
	} else {
		uipn := uint8(pn)
		proto = &uipn
	}

	flow := &Flow{
		Reporter: ReporterType(lsf.Key.Reporter),
		Source: FlowEndpointData{
			Type:      EndpointType(lsf.Key.Source.Type),
			Name:      lsf.Key.Source.Name,
			Namespace: lsf.Key.Source.Namespace,
			IP:        nil, // TODO: We don't return this from Linseed!
			Port:      &srcPort,
			Labels:    GetLinseedFlowLabels(lsf.SourceLabels),
		},
		Destination: FlowEndpointData{
			Type:      EndpointType(lsf.Key.Destination.Type),
			Name:      lsf.Key.Destination.Name,
			Namespace: lsf.Key.Destination.Namespace,
			IP:        nil, // TODO: We don't return this from Linseed!
			Port:      &dstPort,
			Labels:    GetLinseedFlowLabels(lsf.DestinationLabels),
		},
		ActionFlag: ActionFlagFromString(string(lsf.Key.Action)),
		Proto:      proto,
		Policies:   GetPolicyHits(lsf.Policies),
	}

	// Set IP version based on source IP, defaulting to v4.
	ipVersion := 4
	if flow.Source.IP != nil {
		ipVersion = flow.Source.IP.Version()
	} else if flow.Destination.IP != nil {
		ipVersion = flow.Source.IP.Version()
	}
	flow.IPVersion = &ipVersion

	return flow
}

// GetFlowEndpointLabels extracts the flow endpoint labels from the composite aggregation key.
func GetLinseedFlowLabels(labels []lapi.FlowLabels) map[string]string {
	// Find the most frequently seen label value.
	l := make(map[string]string)
	for _, labelKey := range labels {
		weight := int64(0)
		val := ""
		for _, v := range labelKey.Values {
			if v.Count > weight {
				val = v.Value
			}
		}
		l[labelKey.Key] = val
	}
	return l
}

func GetPolicyHits(pols []lapi.Policy) []PolicyHit {
	hits := []PolicyHit{}
	for i, p := range pols {
		hit, err := NewPolicyHit(Action(p.Action), p.Count, i, p.IsStaged, p.Name, p.Namespace, p.Tier, p.RuleID)
		if err != nil {
			logrus.WithError(err).Warn("Skipping invalid policy")
			continue
		}
		hits = append(hits, hit)
	}
	return hits
}

type Flow struct {
	// Reporter
	Reporter ReporterType

	// Source endpoint data for the flow.
	Source FlowEndpointData

	// Destination endpoint data for the flow.
	Destination FlowEndpointData

	// Original action for the flow.
	ActionFlag ActionFlag

	// The protocol of the flow. Nil if unknown.
	Proto *uint8

	// The IP version of the flow. Nil if unknown.
	IPVersion *int

	// Policies applied to the reporting endpoint.
	Policies []PolicyHit
}

// FlowEndpointData can be used to describe the source or destination
// of a flow log.
type FlowEndpointData struct {
	// Endpoint type.
	Type EndpointType

	// Name.
	Name string

	// Namespace - should only be set for namespaces endpoints.
	Namespace string

	// Labels - only relevant for Calico endpoints.
	Labels map[string]string

	// IP, or nil if unknown.
	IP *net.IP

	// Port, or nil if unknown.
	Port *uint16

	// ServiceAccount, or nil if unknown.
	ServiceAccount *string

	// NamedPorts is the set of named ports for this endpoint.
	NamedPorts []EndpointNamedPort
}

// IsCalicoManagedEndpoint returns if the endpoint is managed by Calico.
func (e *FlowEndpointData) IsCalicoManagedEndpoint() bool {
	switch e.Type {
	// Only HEPs and WEPs are calico-managed endpoints.  NetworkSets are handled by Calico, but are not endpoints in
	// the sense that policy is not applied directly to them.
	case EndpointTypeHep, EndpointTypeWep:
		return true
	default:
		return false
	}
}

// IsLabelledEndpoint returns if the endpoint represents a labelled endpoint (i.e. one that can be matched with
// selectors).
func (e *FlowEndpointData) IsLabelledEndpoint() bool {
	switch e.Type {
	// HEPs, WEPs and NetworkSets are all labelled endpoint types that may be selected by calico selectors.
	case EndpointTypeHep, EndpointTypeWep, EndpointTypeNs:
		return true
	default:
		return false
	}
}

// Implement the label Get method for use with the selector processing. This allows us to inject additional labels
// without having to update the dictionary.
func (e *FlowEndpointData) Get(labelName string) (value string, present bool) {
	switch labelName {
	case apiv3.LabelNamespace:
		return e.Namespace, e.Namespace != ""
	case apiv3.LabelOrchestrator:
		return apiv3.OrchestratorKubernetes, e.Namespace != ""
	default:
		if e.Labels != nil {
			val, ok := e.Labels[labelName]
			return val, ok
		}
	}
	return "", false
}

// EndpointNamedPort encapsulates details about a named port on an endpoint.
type EndpointNamedPort struct {
	Name     string
	Protocol uint8
	Port     uint16
}
