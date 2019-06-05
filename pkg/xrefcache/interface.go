// Copyright (c) 2019 Tigera, Inc. All rights reserved.
package xrefcache

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"

	"github.com/tigera/compliance/pkg/dispatcher"
	"github.com/tigera/compliance/pkg/resources"
	"github.com/tigera/compliance/pkg/syncer"
)

// Callback for notification that a particular resource type is in-scope. The x-ref cache will guarantee that this
// callback is made before the OnUpdate that triggered it.
// Note that the x-ref cache does not identify when a resource goes out of scope.
type InScopeCallback (apiv3.ResourceID)

// XrefCache interface.
//
// This interface implements the SyncerCallbacks which is used to populate the cache from the raw K8s and Calico resource
// events.
//
// Important notes to consumers of the cache:
// - The cache is not synchronized. Calls into any of the interface methods should be synchronized.
// - The CacheEntry returned on an OnUpdate callback is a pointer to the data stored in the cache. Any information
//   required from the update should be copied during the OnUpdate callback.
type XrefCache interface {
	// Callbacks for the datastore, used to populate this cache.
	syncer.SyncerCallbacks

	// Get returns the current CacheEntry for a particular resource, or nil if the entry is not cached.
	Get(res apiv3.ResourceID) CacheEntry

	// RegisterOnStatusUpdateHandler registers for status update events.
	RegisterOnStatusUpdateHandler(callback dispatcher.DispatcherOnStatusUpdate)

	// RegisterOnUpdateHandler registers for update events. Note that no updates are send until the cache is in-sync.
	// When in-sync the entire cache is dumped as updates, after which all further updates that occur as the result of
	// datastore actions will be sent.
	RegisterOnUpdateHandler(kind metav1.TypeMeta, updateTypes syncer.UpdateType, callback dispatcher.DispatcherOnUpdate)

	// RegisterInScopeEndpoints registers an endpoints selector which selects which endpoints are considered in-scope.
	// The EventInScope flag will be set on all updates for events from in-scope endpoints.
	RegisterInScopeEndpoints(selection *apiv3.EndpointsSelection) error

	// GetCachedResourceIDs is a helper method (primarily used for testing), to obtain the current list of resource
	// IDs cached for a particular resource kind.
	GetCachedResourceIDs(kind metav1.TypeMeta) []apiv3.ResourceID
}

// All internal caches store types that implement the CacheEntry interface.
type CacheEntry interface {
	VersionedResource
	setInscope()
	getVersionedResource() VersionedResource
	setVersionedResource(r VersionedResource)
	getUpdateTypes() syncer.UpdateType
	addUpdateTypes(syncer.UpdateType)
	resetUpdateTypes()
	getInScopeFlag() syncer.UpdateType
	isDeleted() bool
	setDeleted()
	setResourceID(id apiv3.ResourceID)
	getResourceID() apiv3.ResourceID
}

// VersionedResource is an extension to the Resource interface to add some additional versioning
// (converting the original resource into the v3 Calico model and then the v1 Calico model).
type VersionedResource interface {
	resources.Resource
	getV3() resources.Resource
	getV1() interface{}
}
