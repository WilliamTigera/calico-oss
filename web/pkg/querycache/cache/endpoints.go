// Copyright (c) 2018 Tigera, Inc. All rights reserved.
package cache

import (
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/libcalico-go/lib/apis/v3"
	bapi "github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/set"
	"github.com/tigera/calicoq/web/pkg/querycache/api"
	"github.com/tigera/calicoq/web/pkg/querycache/dispatcherv1v3"
	"github.com/tigera/calicoq/web/pkg/querycache/labelhandler"
)

var (
	matchTypeToDelta = map[labelhandler.MatchType]int{
		labelhandler.MatchStarted: 1,
		labelhandler.MatchStopped: -1,
	}
)

// EndpointsCache implements the cache interface for both WorkloadEndpoint and HostEndpoint resource types collectively.
// This interface consists of both the query and the event update interface.
type EndpointsCache interface {
	TotalEndpoints() api.EndpointCounts
	EndpointsWithNoLabels() api.EndpointCounts
	EndpointsWithNoPolicies() api.EndpointCounts
	GetEndpoint(model.Key) api.Endpoint
	RegisterWithDispatcher(dispatcher dispatcherv1v3.Interface)
	RegisterWithLabelHandler(handler labelhandler.Interface)
}

// NewEndpointsCache creates a new instance of an EndpointsCache.
func NewEndpointsCache() EndpointsCache {
	return &endpointsCache{
		workloadEndpoints: newEndpointCache(),
		hostEndpoints:     newEndpointCache(),
	}
}

// endpointsCache implements the EndpointsCache interface.  It separates out the workload and host endpoints into
// separate sub-caches. Events and requests are handled using the appropriate sub-cache.
type endpointsCache struct {
	workloadEndpoints *endpointCache
	hostEndpoints     *endpointCache
}

// newEndpointCache creates a new endpointCache.
func newEndpointCache() *endpointCache {
	return &endpointCache{
		endpoints:            make(map[model.Key]*endpointData),
		unprotectedEndpoints: set.New(),
	}
}

// endpointCache is the sub-cache for a specific endpoint type.
type endpointCache struct {
	// The endpoints keyed off the resource key.
	endpoints map[model.Key]*endpointData

	// The number of unlabelled (that is explicitly added labels rather than implicitly
	// added) endpoints in this cache.
	numUnlabelled int

	// Stores endpoint keys that have no policies associated (i.e., "unprotected").
	unprotectedEndpoints set.Set
}

func (c *endpointsCache) TotalEndpoints() api.EndpointCounts {
	return api.EndpointCounts{
		NumWorkloadEndpoints: len(c.workloadEndpoints.endpoints),
		NumHostEndpoints:     len(c.hostEndpoints.endpoints),
	}
}

func (c *endpointsCache) EndpointsWithNoLabels() api.EndpointCounts {
	return api.EndpointCounts{
		NumWorkloadEndpoints: c.workloadEndpoints.numUnlabelled,
		NumHostEndpoints:     c.hostEndpoints.numUnlabelled,
	}
}

func (c *endpointsCache) EndpointsWithNoPolicies() api.EndpointCounts {
	return api.EndpointCounts{
		NumWorkloadEndpoints: c.workloadEndpoints.unprotectedEndpoints.Len(),
		NumHostEndpoints:     c.hostEndpoints.unprotectedEndpoints.Len(),
	}
}

func (c *endpointsCache) onUpdate(update dispatcherv1v3.Update) {
	uv3 := update.UpdateV3
	ec := c.getEndpointCache(uv3.Key)
	if ec == nil {
		return
	}
	switch uv3.UpdateType {
	case bapi.UpdateTypeKVNew:
		ed := &endpointData{resource: uv3.Value.(api.Resource)}
		ec.updateHasLabelsCounts(false, !ed.IsLabelled())
		ec.endpoints[uv3.Key] = ed
		// All endpoints are unprotected initially. policyEndpointMatch() will
		// remove them from this set if policies apply on this endpoint.
		ec.unprotectedEndpoints.Add(uv3.Key)
	case bapi.UpdateTypeKVUpdated:
		ed := ec.endpoints[uv3.Key]
		wasUnlabelled := !ed.IsLabelled()
		ed.resource = uv3.Value.(api.Resource)
		ec.updateHasLabelsCounts(wasUnlabelled, !ed.IsLabelled())
	case bapi.UpdateTypeKVDeleted:
		ed := ec.endpoints[uv3.Key]
		ec.unprotectedEndpoints.Discard(uv3.Key)
		ec.updateHasLabelsCounts(!ed.IsLabelled(), false)
		delete(ec.endpoints, uv3.Key)
	}
}

func (c *endpointsCache) GetEndpoint(key model.Key) api.Endpoint {
	if ep := c.getEndpoint(key); ep != nil {
		return ep
	}
	return nil
}

func (c *endpointsCache) RegisterWithDispatcher(dispatcher dispatcherv1v3.Interface) {
	dispatcher.RegisterHandler(v3.KindWorkloadEndpoint, c.onUpdate)
	dispatcher.RegisterHandler(v3.KindHostEndpoint, c.onUpdate)
}

func (c *endpointsCache) RegisterWithLabelHandler(handler labelhandler.Interface) {
	handler.RegisterPolicyHandler(c.policyEndpointMatch)
}

func (c *endpointsCache) policyEndpointMatch(matchType labelhandler.MatchType, polKey model.Key, epKey model.Key) {
	epd := c.getEndpoint(epKey)
	if epd == nil {
		// The endpoint has been deleted. Since the endpoint cache is updated before the index handler is updated this is
		// a valid scenario, and should be treated as a no-op.
		return
	}
	prk := polKey.(model.ResourceKey)
	switch prk.Kind {
	case v3.KindGlobalNetworkPolicy:
		epd.policies.NumGlobalNetworkPolicies += matchTypeToDelta[matchType]
	case v3.KindNetworkPolicy:
		epd.policies.NumNetworkPolicies += matchTypeToDelta[matchType]
	default:
		log.WithField("key", prk).Error("Unexpected resource in event type, expecting a v3 policy type")
	}

	ec := c.getEndpointCache(epKey)
	if epd.IsProtected() {
		ec.unprotectedEndpoints.Discard(epKey)
	} else {
		ec.unprotectedEndpoints.Add(epKey)
	}
}

func (c *endpointCache) updateHasLabelsCounts(before, after bool) {
	if before == after {
		return
	}
	if after {
		c.numUnlabelled++
	} else {
		c.numUnlabelled--
	}
}

func (c *endpointsCache) getEndpoint(key model.Key) *endpointData {
	ec := c.getEndpointCache(key)
	if ec == nil {
		return nil
	}
	return ec.endpoints[key]
}

func (c *endpointsCache) getEndpointCache(epKey model.Key) *endpointCache {
	rKey := epKey.(model.ResourceKey)
	switch rKey.Kind {
	case v3.KindWorkloadEndpoint:
		return c.workloadEndpoints
	case v3.KindHostEndpoint:
		return c.hostEndpoints
	default:
		log.WithField("kind", rKey.Kind).Fatal("unexpected resource kind")
		return nil
	}
}

type endpointData struct {
	resource api.Resource
	policies api.PolicyCounts
}

func (e *endpointData) GetPolicyCounts() api.PolicyCounts {
	return e.policies
}

func (e *endpointData) GetResource() api.Resource {
	return e.resource
}

func (e *endpointData) GetNode() string {
	switch r := e.resource.(type) {
	case *v3.WorkloadEndpoint:
		return r.Spec.Node
	case *v3.HostEndpoint:
		return r.Spec.Node
	}
	return ""
}

// IsProtected returns true when an endpoint has one or more GlobalNetworkPolicies
// or NetworkPolicies that apply to it.
func (e *endpointData) IsProtected() bool {
	return e.policies.NumGlobalNetworkPolicies > 0 || e.policies.NumNetworkPolicies > 0
}

// IsLabelled returns true when there are explicitly configured labels on the endpoint.
// This ignores implicitly added labels such as projectcalico/org/namespace, or labels
// inherited through a profile.
func (e *endpointData) IsLabelled() bool {
	switch e.resource.GetObjectKind().GroupVersionKind().Kind {
	case v3.KindWorkloadEndpoint:
		// WEPs automatically have a namespace and orchestrator label added to them.
		return len(e.resource.GetObjectMeta().GetLabels()) > 2
	case v3.KindHostEndpoint:
		return len(e.resource.GetObjectMeta().GetLabels()) > 0
	}
	return false
}
