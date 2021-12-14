// Copyright (c) 2020 Tigera, Inc. All rights reserved.

package calico

import (
	"reflect"

	"golang.org/x/net/context"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/storage"
	etcd "k8s.io/apiserver/pkg/storage/etcd3"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"

	"github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/options"
	"github.com/projectcalico/libcalico-go/lib/watch"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
)

// NewKubeControllersConfigurationStorage creates a new libcalico-based storage.Interface implementation for KubeControllersConfigurations
func NewKubeControllersConfigurationStorage(opts Options) (registry.DryRunnableStorage, factory.DestroyFunc) {
	c := CreateClientFromConfig()
	createFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*v3.KubeControllersConfiguration)
		return c.KubeControllersConfiguration().Create(ctx, res, oso)
	}
	updateFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*v3.KubeControllersConfiguration)
		return c.KubeControllersConfiguration().Update(ctx, res, oso)
	}
	getFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		ogo := opts.(options.GetOptions)
		return c.KubeControllersConfiguration().Get(ctx, name, ogo)
	}
	deleteFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		odo := opts.(options.DeleteOptions)
		return c.KubeControllersConfiguration().Delete(ctx, name, odo)
	}
	listFn := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (resourceListObject, error) {
		olo := opts.(options.ListOptions)
		return c.KubeControllersConfiguration().List(ctx, olo)
	}
	watchFn := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (watch.Interface, error) {
		olo := opts.(options.ListOptions)
		return c.KubeControllersConfiguration().Watch(ctx, olo)
	}
	hasRestrictionsFn := func(obj resourceObject) bool {
		return false
	}

	// TODO(doublek): Inject codec, client for nicer testing.
	dryRunnableStorage := registry.DryRunnableStorage{Storage: &resourceStore{
		client:            c,
		codec:             opts.RESTOptions.StorageConfig.Codec,
		versioner:         etcd.APIObjectVersioner{},
		aapiType:          reflect.TypeOf(v3.KubeControllersConfiguration{}),
		aapiListType:      reflect.TypeOf(v3.KubeControllersConfigurationList{}),
		libCalicoType:     reflect.TypeOf(v3.KubeControllersConfiguration{}),
		libCalicoListType: reflect.TypeOf(v3.KubeControllersConfigurationList{}),
		isNamespaced:      false,
		create:            createFn,
		update:            updateFn,
		get:               getFn,
		delete:            deleteFn,
		list:              listFn,
		watch:             watchFn,
		resourceName:      "KubeControllersConfiguration",
		converter:         KubeControllersConfigurationConverter{},
		hasRestrictions:   hasRestrictionsFn,
	}, Codec: opts.RESTOptions.StorageConfig.Codec}
	return dryRunnableStorage, func() {}
}

type KubeControllersConfigurationConverter struct {
}

func (gc KubeControllersConfigurationConverter) convertToLibcalico(aapiObj runtime.Object) resourceObject {
	aapiKubeControllersConfiguration := aapiObj.(*v3.KubeControllersConfiguration)
	lcgKubeControllersConfiguration := &v3.KubeControllersConfiguration{}
	lcgKubeControllersConfiguration.TypeMeta = aapiKubeControllersConfiguration.TypeMeta
	lcgKubeControllersConfiguration.ObjectMeta = aapiKubeControllersConfiguration.ObjectMeta
	lcgKubeControllersConfiguration.Kind = v3.KindKubeControllersConfiguration
	lcgKubeControllersConfiguration.APIVersion = v3.GroupVersionCurrent
	lcgKubeControllersConfiguration.Spec = aapiKubeControllersConfiguration.Spec
	lcgKubeControllersConfiguration.Status = aapiKubeControllersConfiguration.Status
	return lcgKubeControllersConfiguration
}

func (gc KubeControllersConfigurationConverter) convertToAAPI(libcalicoObject resourceObject, aapiObj runtime.Object) {
	lcgKubeControllersConfiguration := libcalicoObject.(*v3.KubeControllersConfiguration)
	aapiKubeControllersConfiguration := aapiObj.(*v3.KubeControllersConfiguration)
	aapiKubeControllersConfiguration.Spec = lcgKubeControllersConfiguration.Spec
	aapiKubeControllersConfiguration.Status = lcgKubeControllersConfiguration.Status
	aapiKubeControllersConfiguration.TypeMeta = lcgKubeControllersConfiguration.TypeMeta
	aapiKubeControllersConfiguration.ObjectMeta = lcgKubeControllersConfiguration.ObjectMeta
}

func (gc KubeControllersConfigurationConverter) convertToAAPIList(libcalicoListObject resourceListObject, aapiListObj runtime.Object, pred storage.SelectionPredicate) {
	lcgKubeControllersConfigurationList := libcalicoListObject.(*v3.KubeControllersConfigurationList)
	aapiKubeControllersConfigurationList := aapiListObj.(*v3.KubeControllersConfigurationList)
	if libcalicoListObject == nil {
		aapiKubeControllersConfigurationList.Items = []v3.KubeControllersConfiguration{}
		return
	}
	aapiKubeControllersConfigurationList.TypeMeta = lcgKubeControllersConfigurationList.TypeMeta
	aapiKubeControllersConfigurationList.ListMeta = lcgKubeControllersConfigurationList.ListMeta
	for _, item := range lcgKubeControllersConfigurationList.Items {
		aapiKubeControllersConfiguration := v3.KubeControllersConfiguration{}
		gc.convertToAAPI(&item, &aapiKubeControllersConfiguration)
		if matched, err := pred.Matches(&aapiKubeControllersConfiguration); err == nil && matched {
			aapiKubeControllersConfigurationList.Items = append(aapiKubeControllersConfigurationList.Items, aapiKubeControllersConfiguration)
		}
	}
}
