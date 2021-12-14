// Copyright (c) 2019 Tigera, Inc. All rights reserved.

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

// NewProfileStorage creates a new libcalico-based storage.Interface implementation for Profiles
func NewProfileStorage(opts Options) (registry.DryRunnableStorage, factory.DestroyFunc) {
	c := CreateClientFromConfig()
	createFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*v3.Profile)
		return c.Profiles().Create(ctx, res, oso)
	}
	updateFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*v3.Profile)
		return c.Profiles().Update(ctx, res, oso)
	}
	getFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		ogo := opts.(options.GetOptions)
		return c.Profiles().Get(ctx, name, ogo)
	}
	deleteFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		odo := opts.(options.DeleteOptions)
		return c.Profiles().Delete(ctx, name, odo)
	}
	listFn := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (resourceListObject, error) {
		olo := opts.(options.ListOptions)
		return c.Profiles().List(ctx, olo)
	}
	watchFn := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (watch.Interface, error) {
		olo := opts.(options.ListOptions)
		return c.Profiles().Watch(ctx, olo)
	}
	hasRestrictionsFn := func(obj resourceObject) bool {
		return false
	}

	dryRunnableStorage := registry.DryRunnableStorage{Storage: &resourceStore{
		client:            c,
		codec:             opts.RESTOptions.StorageConfig.Codec,
		versioner:         etcd.APIObjectVersioner{},
		aapiType:          reflect.TypeOf(v3.Profile{}),
		aapiListType:      reflect.TypeOf(v3.ProfileList{}),
		libCalicoType:     reflect.TypeOf(v3.Profile{}),
		libCalicoListType: reflect.TypeOf(v3.ProfileList{}),
		isNamespaced:      false,
		create:            createFn,
		update:            updateFn,
		get:               getFn,
		delete:            deleteFn,
		list:              listFn,
		watch:             watchFn,
		resourceName:      "Profile",
		converter:         ProfileConverter{},
		hasRestrictions:   hasRestrictionsFn,
	}, Codec: opts.RESTOptions.StorageConfig.Codec}
	return dryRunnableStorage, func() {}
}

type ProfileConverter struct {
}

func (gc ProfileConverter) convertToLibcalico(aapiObj runtime.Object) resourceObject {
	aapiProfile := aapiObj.(*v3.Profile)
	lcgProfile := &v3.Profile{}
	lcgProfile.TypeMeta = aapiProfile.TypeMeta
	lcgProfile.ObjectMeta = aapiProfile.ObjectMeta
	lcgProfile.Kind = v3.KindProfile
	lcgProfile.APIVersion = v3.GroupVersionCurrent
	lcgProfile.Spec = aapiProfile.Spec
	return lcgProfile
}

func (gc ProfileConverter) convertToAAPI(libcalicoObject resourceObject, aapiObj runtime.Object) {
	lcgProfile := libcalicoObject.(*v3.Profile)
	aapiProfile := aapiObj.(*v3.Profile)
	aapiProfile.Spec = lcgProfile.Spec
	aapiProfile.TypeMeta = lcgProfile.TypeMeta
	aapiProfile.ObjectMeta = lcgProfile.ObjectMeta
}

func (gc ProfileConverter) convertToAAPIList(libcalicoListObject resourceListObject, aapiListObj runtime.Object, pred storage.SelectionPredicate) {
	lcgProfileList := libcalicoListObject.(*v3.ProfileList)
	aapiProfileList := aapiListObj.(*v3.ProfileList)
	if libcalicoListObject == nil {
		aapiProfileList.Items = []v3.Profile{}
		return
	}
	aapiProfileList.TypeMeta = lcgProfileList.TypeMeta
	aapiProfileList.ListMeta = lcgProfileList.ListMeta
	for _, item := range lcgProfileList.Items {
		aapiProfile := v3.Profile{}
		gc.convertToAAPI(&item, &aapiProfile)
		if matched, err := pred.Matches(&aapiProfile); err == nil && matched {
			aapiProfileList.Items = append(aapiProfileList.Items, aapiProfile)
		}
	}
}
