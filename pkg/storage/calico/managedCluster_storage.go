// Copyright (c) 2019 Tigera, Inc. All rights reserved.

package calico

import (
	"reflect"

	"golang.org/x/net/context"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	etcd "k8s.io/apiserver/pkg/storage/etcd3"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
	"k8s.io/apiserver/pkg/registry/generic/registry"
	libcalicoapi "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/options"
	"github.com/projectcalico/libcalico-go/lib/watch"

	aapi "github.com/tigera/calico-k8sapiserver/pkg/apis/projectcalico"
)

// NewManagedClusterStorage creates a new libcalico-based storage.Interface implementation for ManagedClusters
func NewManagedClusterStorage(opts Options) (registry.DryRunnableStorage, factory.DestroyFunc) {
	c := createClientFromConfig()
	createFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*libcalicoapi.ManagedCluster)
		return c.ManagedClusters().Create(ctx, res, oso)
	}
	updateFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*libcalicoapi.ManagedCluster)
		return c.ManagedClusters().Update(ctx, res, oso)
	}
	getFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		ogo := opts.(options.GetOptions)
		return c.ManagedClusters().Get(ctx, name, ogo)
	}
	deleteFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		odo := opts.(options.DeleteOptions)
		return c.ManagedClusters().Delete(ctx, name, odo)
	}
	listFn := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (resourceListObject, error) {
		olo := opts.(options.ListOptions)
		return c.ManagedClusters().List(ctx, olo)
	}
	watchFn := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (watch.Interface, error) {
		olo := opts.(options.ListOptions)
		return c.ManagedClusters().Watch(ctx, olo)
	}
	dryRunnableStorage := registry.DryRunnableStorage{Storage: &resourceStore{
		client:            c,
		codec:             opts.RESTOptions.StorageConfig.Codec,
		versioner:         etcd.APIObjectVersioner{},
		aapiType:          reflect.TypeOf(aapi.ManagedCluster{}),
		aapiListType:      reflect.TypeOf(aapi.ManagedClusterList{}),
		libCalicoType:     reflect.TypeOf(libcalicoapi.ManagedCluster{}),
		libCalicoListType: reflect.TypeOf(libcalicoapi.ManagedClusterList{}),
		isNamespaced:      false,
		create:            createFn,
		update:            updateFn,
		get:               getFn,
		delete:            deleteFn,
		list:              listFn,
		watch:             watchFn,
		resourceName:      "ManagedCluster",
		converter:         ManagedClusterConverter{},
	}, Codec: opts.RESTOptions.StorageConfig.Codec}
	return dryRunnableStorage, func() {}
}

type ManagedClusterConverter struct {
}

func (gc ManagedClusterConverter) convertToLibcalico(aapiObj runtime.Object) resourceObject {
	aapiManagedCluster := aapiObj.(*aapi.ManagedCluster)
	lcgManagedCluster := &libcalicoapi.ManagedCluster{}
	lcgManagedCluster.TypeMeta = aapiManagedCluster.TypeMeta
	lcgManagedCluster.ObjectMeta = aapiManagedCluster.ObjectMeta
	lcgManagedCluster.Spec = aapiManagedCluster.Spec
	lcgManagedCluster.Status = aapiManagedCluster.Status
	return lcgManagedCluster
}

func (gc ManagedClusterConverter) convertToAAPI(libcalicoObject resourceObject, aapiObj runtime.Object) {
	lcgManagedCluster := libcalicoObject.(*libcalicoapi.ManagedCluster)
	aapiManagedCluster := aapiObj.(*aapi.ManagedCluster)
	aapiManagedCluster.Spec = lcgManagedCluster.Spec
	aapiManagedCluster.Status = lcgManagedCluster.Status
	aapiManagedCluster.TypeMeta = lcgManagedCluster.TypeMeta
	aapiManagedCluster.ObjectMeta = lcgManagedCluster.ObjectMeta
}

func (gc ManagedClusterConverter) convertToAAPIList(libcalicoListObject resourceListObject, aapiListObj runtime.Object, pred storage.SelectionPredicate) {
	lcgManagedClusterList := libcalicoListObject.(*libcalicoapi.ManagedClusterList)
	aapiManagedClusterList := aapiListObj.(*aapi.ManagedClusterList)
	if libcalicoListObject == nil {
		aapiManagedClusterList.Items = []aapi.ManagedCluster{}
		return
	}
	aapiManagedClusterList.TypeMeta = lcgManagedClusterList.TypeMeta
	aapiManagedClusterList.ListMeta = lcgManagedClusterList.ListMeta
	for _, item := range lcgManagedClusterList.Items {
		aapiManagedCluster := aapi.ManagedCluster{}
		gc.convertToAAPI(&item, &aapiManagedCluster)
		if matched, err := pred.Matches(&aapiManagedCluster); err == nil && matched {
			aapiManagedClusterList.Items = append(aapiManagedClusterList.Items, aapiManagedCluster)
		}
	}
}
