// Copyright (c) 2021 Tigera, Inc. All rights reserved.
package servicegraph

import (
	"context"
	"net/http"
	"sync"

	"github.com/tigera/lma/pkg/httputils"
	"k8s.io/apiserver/pkg/endpoints/request"

	v3 "github.com/projectcalico/libcalico-go/lib/apis/v3"

	log "github.com/sirupsen/logrus"

	v1 "github.com/tigera/es-proxy/pkg/apis/v1"
	esauth "github.com/tigera/es-proxy/pkg/auth"
	lmaauth "github.com/tigera/lma/pkg/auth"
	"github.com/tigera/lma/pkg/k8s"
)

// This file implements an RBAC flow filter. It parses the AuthorizedResourceVerbs returned by a authorization
// review to determine which endpoint types are listable. At least one endpoint in a flow should be listable for the
// flow to be included.

type RBACFilter interface {
	// --- Whether we can access the various elastic logs. Flows are not included here because we have already validated
	//     access to flow logs (in server.go handler chaining).

	// IncludeL7Logs returns true if the user is permitted to view L7 logs.
	IncludeL7Logs() bool

	// IncludeDNSLogs returns true if the user is permitted to view DNS logs.
	IncludeDNSLogs() bool

	// IncludeAlerts returns true if the user is permitted to view alerts.
	IncludeAlerts() bool

	// --- Whether we can access the specific details of the elastic logs.

	// IncludeFlow returns true if the user is permitted a specific flow
	IncludeFlow(f FlowEdge) bool

	// IncludeEndpoint returns true if the user is permitted to list a specific endpoint.
	IncludeEndpoint(f FlowEndpoint) bool

	// IncludeHostEndpoints returns true if the user is permitted to list host endpoints.
	IncludeHostEndpoints() bool

	// IncludeGlobalNetworkSets returns true if the user is permitted to list global network sets.
	IncludeGlobalNetworkSets() bool

	// IncludeNetworkSets returns true if the user is permitted to list network sets in the specified namespace.
	IncludeNetworkSets(namespace string) bool

	// IncludePods returns true if the user is permitted to list pods in the specific namespace.
	IncludePods(namespace string) bool
}

// NewRBACFilter performs an authorization review and uses the response to construct an RBAC filter.
func NewRBACFilter(
	ctx context.Context, authz lmaauth.RBACAuthorizer, csFactory k8s.ClientSetFactory, req *http.Request, cluster string,
) (RBACFilter, error) {

	var verbs []v3.AuthorizedResourceVerbs
	var l7Permitted, dnsPermitted, alertsPermitted bool
	var verbsErr, l7Err, dnsErr, alertsErr error
	wg := sync.WaitGroup{}

	usr, ok := request.UserFrom(ctx)
	if !ok {
		// There should be user info on the request context. If not this is is server error since an earlier handler
		// should have authenticated.
		log.Debug("No user information on request")
		return nil, &httputils.HttpStatusError{
			Status: http.StatusInternalServerError,
			Msg:    "No user request on request",
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		verbs, verbsErr = lmaauth.PerformUserAuthorizationReviewForElasticLogs(ctx, csFactory, req, cluster)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		l7Permitted, l7Err = authz.Authorize(usr, esauth.CreateLMAResourceAttributes(cluster, "l7"), nil)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		dnsPermitted, dnsErr = authz.Authorize(usr, esauth.CreateLMAResourceAttributes(cluster, "dns"), nil)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		alertsPermitted, alertsErr = authz.Authorize(usr, esauth.CreateLMAResourceAttributes(cluster, "events"), nil)
	}()
	wg.Wait()

	if verbsErr != nil {
		return nil, verbsErr
	} else if l7Err != nil {
		return nil, l7Err
	} else if dnsErr != nil {
		return nil, dnsErr
	} else if alertsErr != nil {
		return nil, alertsErr
	}

	f := &rbacFilter{
		includeL7Logs:            l7Permitted,
		includeDNSLogs:           dnsPermitted,
		includeAlerts:            alertsPermitted,
		listPodNamespaces:        make(map[string]bool),
		listNetworkSetNamespaces: make(map[string]bool),
	}

	for _, r := range verbs {
		for _, v := range r.Verbs {
			if v.Verb != "list" {
				// Only interested in the list verbs.
				continue
			}
			for _, rg := range v.ResourceGroups {
				switch r.Resource {
				case "hostendpoints":
					f.listAllHostEndpoints = true
				case "networksets":
					if rg.Namespace == "" {
						f.listAllNetworkSets = true
					} else {
						f.listNetworkSetNamespaces[rg.Namespace] = true
					}
				case "globalnetworksets":
					f.listAllGlobalNetworkSets = true
				case "pods":
					if rg.Namespace == "" {
						f.listAllPods = true
					} else {
						f.listPodNamespaces[rg.Namespace] = true
					}
				}
			}
		}
	}

	return f, nil
}

// rbacFilter implements the RBACFilter interface.
type rbacFilter struct {
	includeL7Logs            bool
	includeDNSLogs           bool
	includeAlerts            bool
	listAllPods              bool
	listAllHostEndpoints     bool
	listAllGlobalNetworkSets bool
	listAllNetworkSets       bool
	listPodNamespaces        map[string]bool
	listNetworkSetNamespaces map[string]bool
}

func (f *rbacFilter) IncludeL7Logs() bool {
	return f.includeL7Logs
}

func (f *rbacFilter) IncludeDNSLogs() bool {
	return f.includeDNSLogs
}

func (f *rbacFilter) IncludeAlerts() bool {
	return f.includeAlerts
}

func (f *rbacFilter) IncludeFlow(e FlowEdge) bool {
	if f.IncludeEndpoint(e.Source) {
		return true
	}
	if f.IncludeEndpoint(e.Dest) {
		return true
	}
	return false
}

func (f *rbacFilter) IncludeEndpoint(e FlowEndpoint) bool {
	// L3Flow data should only consists of the endpoint types contained in the flow logs, and not any of the generated
	// types for the graph.
	switch e.Type {
	case v1.GraphNodeTypeWorkload, v1.GraphNodeTypeReplicaSet:
		return f.IncludePods(e.Namespace)
	case v1.GraphNodeTypeNetwork:
		return false
	case v1.GraphNodeTypeNetworkSet:
		if e.Namespace == "" {
			return f.IncludeGlobalNetworkSets()
		}
		return f.IncludeNetworkSets(e.Namespace)
	case v1.GraphNodeTypeHost:
		return f.IncludeHostEndpoints()
	case v1.GraphNodeTypeUnknown:
		// The L7 summary logs will not contain an endpoint type.
		return false
	default:
		// Anything else
		log.Debugf("Unexpected endpoint type in parsed flows: %s", e.Type)
		return false
	}
}

func (f *rbacFilter) IncludeHostEndpoints() bool {
	return f.listAllHostEndpoints
}

func (f *rbacFilter) IncludeGlobalNetworkSets() bool {
	return f.listAllGlobalNetworkSets
}

func (f *rbacFilter) IncludeNetworkSets(namespace string) bool {
	return f.listAllNetworkSets || f.listNetworkSetNamespaces[namespace]
}

func (f *rbacFilter) IncludePods(namespace string) bool {
	return f.listAllPods || f.listPodNamespaces[namespace]
}

// ---- Mock filters for testing ----
type RBACFilterIncludeAll struct{}

func (m RBACFilterIncludeAll) IncludeFlowLogs() bool                    { return true }
func (m RBACFilterIncludeAll) IncludeL7Logs() bool                      { return true }
func (m RBACFilterIncludeAll) IncludeDNSLogs() bool                     { return true }
func (m RBACFilterIncludeAll) IncludeAlerts() bool                      { return true }
func (m RBACFilterIncludeAll) IncludeFlow(f FlowEdge) bool              { return true }
func (m RBACFilterIncludeAll) IncludeEndpoint(f FlowEndpoint) bool      { return true }
func (m RBACFilterIncludeAll) IncludeHostEndpoints() bool               { return true }
func (m RBACFilterIncludeAll) IncludeGlobalNetworkSets() bool           { return true }
func (m RBACFilterIncludeAll) IncludeNetworkSets(namespace string) bool { return true }
func (m RBACFilterIncludeAll) IncludePods(namespace string) bool        { return true }

type RBACFilterIncludeNone struct{}

func (m RBACFilterIncludeNone) IncludeFlowLogs() bool                    { return false }
func (m RBACFilterIncludeNone) IncludeL7Logs() bool                      { return false }
func (m RBACFilterIncludeNone) IncludeDNSLogs() bool                     { return false }
func (m RBACFilterIncludeNone) IncludeAlerts() bool                      { return false }
func (m RBACFilterIncludeNone) IncludeFlow(f FlowEdge) bool              { return false }
func (m RBACFilterIncludeNone) IncludeEndpoint(f FlowEndpoint) bool      { return false }
func (m RBACFilterIncludeNone) IncludeHostEndpoints() bool               { return false }
func (m RBACFilterIncludeNone) IncludeGlobalNetworkSets() bool           { return false }
func (m RBACFilterIncludeNone) IncludeNetworkSets(namespace string) bool { return false }
func (m RBACFilterIncludeNone) IncludePods(namespace string) bool        { return false }
