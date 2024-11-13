// Copyright (c) 2024 Tigera, Inc. All rights reserved.
package checker

import (
	"net"
	"strings"

	log "github.com/sirupsen/logrus"

	authz "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
)

// CheckRequestToFlowAdapter adapts CheckRequest to the l4 and l7 flow interfaces for use in the
// matchers.
type CheckRequestToFlowAdapter struct {
	flow *authz.CheckRequest
}

func NewCheckRequestToFlowAdapter(req *authz.CheckRequest) *CheckRequestToFlowAdapter {
	return &CheckRequestToFlowAdapter{flow: req}
}

func (a *CheckRequestToFlowAdapter) getSourceIP() net.IP {
	if a.flow == nil || a.flow.GetAttributes().GetSource().GetAddress().GetSocketAddress() == nil {
		return nil
	}
	return net.ParseIP(a.flow.GetAttributes().GetSource().GetAddress().GetSocketAddress().GetAddress())
}

func (a *CheckRequestToFlowAdapter) getDestIP() net.IP {
	if a.flow == nil || a.flow.GetAttributes().GetDestination().GetAddress().GetSocketAddress() == nil {
		return nil
	}
	return net.ParseIP(a.flow.GetAttributes().GetDestination().GetAddress().GetSocketAddress().GetAddress())
}

func (a *CheckRequestToFlowAdapter) getSourcePort() int {
	if a.flow == nil || a.flow.GetAttributes().GetSource().GetAddress().GetSocketAddress() == nil {
		return 0
	}
	return int(a.flow.GetAttributes().GetSource().GetAddress().GetSocketAddress().GetPortValue())
}

func (a *CheckRequestToFlowAdapter) getDestPort() int {
	if a.flow == nil || a.flow.GetAttributes().GetDestination().GetAddress().GetSocketAddress() == nil {
		return 0
	}
	return int(a.flow.GetAttributes().GetDestination().GetAddress().GetSocketAddress().GetPortValue())
}

func (a *CheckRequestToFlowAdapter) getProtocol() int {
	if a.flow == nil || a.flow.GetAttributes().GetDestination().GetAddress().GetSocketAddress() == nil {
		// Default to TCP if protocol is not set.
		return 6
	}
	protocol := a.flow.GetAttributes().GetDestination().GetAddress().GetSocketAddress().GetProtocol().String()
	if p, ok := protocolMap[strings.ToLower(protocol)]; ok {
		return p
	}
	log.Warnf("unsupported protocol: %s, defaulting to TCP", protocol)
	return 6
}

func (a *CheckRequestToFlowAdapter) getHttpMethod() *string {
	if a.flow == nil || a.flow.GetAttributes().GetRequest().GetHttp() == nil {
		return nil
	}
	method := a.flow.GetAttributes().GetRequest().GetHttp().GetMethod()
	return &method
}

func (a *CheckRequestToFlowAdapter) getHttpPath() *string {
	if a.flow == nil || a.flow.GetAttributes().GetRequest().GetHttp() == nil {
		return nil
	}
	path := a.flow.GetAttributes().GetRequest().GetHttp().GetPath()
	return &path
}

func (a *CheckRequestToFlowAdapter) getSourcePrincipal() *string {
	if a.flow == nil {
		return nil
	}
	principal := a.flow.GetAttributes().GetSource().GetPrincipal()
	return &principal
}

func (a *CheckRequestToFlowAdapter) getDestPrincipal() *string {
	if a.flow == nil {
		return nil
	}
	principal := a.flow.GetAttributes().GetDestination().GetPrincipal()
	return &principal
}

func (a *CheckRequestToFlowAdapter) getSourceLabels() map[string]string {
	if a.flow == nil {
		return nil
	}
	return a.flow.GetAttributes().GetSource().GetLabels()
}

func (a *CheckRequestToFlowAdapter) getDestLabels() map[string]string {
	if a.flow == nil {
		return nil
	}
	return a.flow.GetAttributes().GetDestination().GetLabels()
}
