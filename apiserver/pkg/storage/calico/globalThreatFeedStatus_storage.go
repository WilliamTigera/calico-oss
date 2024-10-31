// Copyright (c) 2019 Tigera, Inc. All rights reserved.

package calico

import (
	"reflect"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	"golang.org/x/net/context"
	"k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"

	"github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/calico/libcalico-go/lib/options"
	"github.com/projectcalico/calico/licensing/client/features"
)

// NewGlobalThreatFeedStatusStorage creates a new libcalico-based storage.Interface implementation for GlobalThreatFeedsStatus
func NewGlobalThreatFeedStatusStorage(opts Options) (registry.DryRunnableStorage, factory.DestroyFunc) {
	c := CreateClientFromConfig()
	updateFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*v3.GlobalThreatFeed)
		return c.GlobalThreatFeeds().UpdateStatus(ctx, res, oso)
	}
	getFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		ogo := opts.(options.GetOptions)
		return c.GlobalThreatFeeds().Get(ctx, name, ogo)
	}
	hasRestrictionsFn := func(obj resourceObject) bool {
		return !opts.LicenseMonitor.GetFeatureStatus(features.ThreatDefense)
	}
	// TODO(doublek): Inject codec, client for nicer testing.
	dryRunnableStorage := registry.DryRunnableStorage{Storage: &resourceStore{
		client:          c,
		codec:           opts.RESTOptions.StorageConfig.Codec,
		versioner:       APIObjectVersioner{},
		aapiType:        reflect.TypeOf(v3.GlobalThreatFeed{}),
		isNamespaced:    false,
		update:          updateFn,
		get:             getFn,
		resourceName:    "GlobalThreatFeedStatus",
		converter:       GlobalThreatFeedConverter{},
		hasRestrictions: hasRestrictionsFn,
	}, Codec: opts.RESTOptions.StorageConfig.Codec}
	return dryRunnableStorage, func() {}
}
