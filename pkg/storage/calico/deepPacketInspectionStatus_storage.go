// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package calico

import (
	"reflect"

	"golang.org/x/net/context"

	features "github.com/tigera/licensing/client/features"
	"k8s.io/apiserver/pkg/registry/generic/registry"
	etcd "k8s.io/apiserver/pkg/storage/etcd3"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/options"
)

// NewDeepPacketInspectionStatusStorage creates a new libcalico-based storage.Interface implementation for DeepPacketInspections
func NewDeepPacketInspectionStatusStorage(opts Options) (registry.DryRunnableStorage, factory.DestroyFunc) {
	c := CreateClientFromConfig()
	updateFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*v3.DeepPacketInspection)
		return c.DeepPacketInspections().UpdateStatus(ctx, res, oso)
	}
	getFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		ogo := opts.(options.GetOptions)
		return c.DeepPacketInspections().Get(ctx, ns, name, ogo)
	}
	hasRestrictionsFn := func(obj resourceObject) bool {
		return !opts.LicenseMonitor.GetFeatureStatus(features.ThreatDefense)
	}
	// TODO(doublek): Inject codec, client for nicer testing.
	dryRunnableStorage := registry.DryRunnableStorage{Storage: &resourceStore{
		client:            c,
		codec:             opts.RESTOptions.StorageConfig.Codec,
		versioner:         etcd.APIObjectVersioner{},
		aapiType:          reflect.TypeOf(v3.DeepPacketInspection{}),
		aapiListType:      reflect.TypeOf(v3.DeepPacketInspectionList{}),
		libCalicoType:     reflect.TypeOf(v3.DeepPacketInspection{}),
		libCalicoListType: reflect.TypeOf(v3.DeepPacketInspectionList{}),
		isNamespaced:      true,
		update:            updateFn,
		get:               getFn,
		resourceName:      "DeepPacketInspectionStatus",
		converter:         DeepPacketInspectionConverter{},
		hasRestrictions:   hasRestrictionsFn,
	}, Codec: opts.RESTOptions.StorageConfig.Codec}
	return dryRunnableStorage, func() {}
}
