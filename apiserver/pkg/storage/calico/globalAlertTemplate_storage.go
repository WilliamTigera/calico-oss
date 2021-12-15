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

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/calico/libcalico-go/lib/options"
	"github.com/projectcalico/calico/libcalico-go/lib/watch"

	features "github.com/projectcalico/calico/licensing/client/features"
)

// NewGlobalAlertTemplateStorage creates a new libcalico-based storage.Interface implementation for GlobalAlertTemplates
func NewGlobalAlertTemplateStorage(opts Options) (registry.DryRunnableStorage, factory.DestroyFunc) {
	c := CreateClientFromConfig()
	createFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*v3.GlobalAlertTemplate)
		return c.GlobalAlertTemplates().Create(ctx, res, oso)
	}
	updateFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*v3.GlobalAlertTemplate)
		return c.GlobalAlertTemplates().Update(ctx, res, oso)
	}
	getFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		ogo := opts.(options.GetOptions)
		return c.GlobalAlertTemplates().Get(ctx, name, ogo)
	}
	deleteFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		odo := opts.(options.DeleteOptions)
		return c.GlobalAlertTemplates().Delete(ctx, name, odo)
	}
	listFn := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (resourceListObject, error) {
		olo := opts.(options.ListOptions)
		return c.GlobalAlertTemplates().List(ctx, olo)
	}
	watchFn := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (watch.Interface, error) {
		olo := opts.(options.ListOptions)
		return c.GlobalAlertTemplates().Watch(ctx, olo)
	}
	hasRestrictionsFn := func(obj resourceObject) bool {
		return !opts.LicenseMonitor.GetFeatureStatus(features.AlertManagement)
	}
	// TODO(doublek): Inject codec, client for nicer testing.
	dryRunnableStorage := registry.DryRunnableStorage{Storage: &resourceStore{
		client:            c,
		codec:             opts.RESTOptions.StorageConfig.Codec,
		versioner:         etcd.APIObjectVersioner{},
		aapiType:          reflect.TypeOf(v3.GlobalAlertTemplate{}),
		aapiListType:      reflect.TypeOf(v3.GlobalAlertTemplateList{}),
		libCalicoType:     reflect.TypeOf(v3.GlobalAlertTemplate{}),
		libCalicoListType: reflect.TypeOf(v3.GlobalAlertTemplateList{}),
		isNamespaced:      false,
		create:            createFn,
		update:            updateFn,
		get:               getFn,
		delete:            deleteFn,
		list:              listFn,
		watch:             watchFn,
		resourceName:      "GlobalAlertTemplate",
		converter:         GlobalAlertTemplateConverter{},
		hasRestrictions:   hasRestrictionsFn,
	}, Codec: opts.RESTOptions.StorageConfig.Codec}
	return dryRunnableStorage, func() {}
}

type GlobalAlertTemplateConverter struct {
}

func (gc GlobalAlertTemplateConverter) convertToLibcalico(aapiObj runtime.Object) resourceObject {
	aapiGlobalAlertTemplate := aapiObj.(*v3.GlobalAlertTemplate)
	lcgGlobalAlertTemplate := &v3.GlobalAlertTemplate{}
	lcgGlobalAlertTemplate.TypeMeta = aapiGlobalAlertTemplate.TypeMeta
	lcgGlobalAlertTemplate.ObjectMeta = aapiGlobalAlertTemplate.ObjectMeta
	lcgGlobalAlertTemplate.Kind = v3.KindGlobalAlertTemplate
	lcgGlobalAlertTemplate.APIVersion = v3.GroupVersionCurrent
	lcgGlobalAlertTemplate.Spec = aapiGlobalAlertTemplate.Spec
	return lcgGlobalAlertTemplate
}

func (gc GlobalAlertTemplateConverter) convertToAAPI(libcalicoObject resourceObject, aapiObj runtime.Object) {
	lcgGlobalAlertTemplate := libcalicoObject.(*v3.GlobalAlertTemplate)
	aapiGlobalAlertTemplate := aapiObj.(*v3.GlobalAlertTemplate)
	aapiGlobalAlertTemplate.Spec = lcgGlobalAlertTemplate.Spec
	aapiGlobalAlertTemplate.TypeMeta = lcgGlobalAlertTemplate.TypeMeta
	aapiGlobalAlertTemplate.ObjectMeta = lcgGlobalAlertTemplate.ObjectMeta
}

func (gc GlobalAlertTemplateConverter) convertToAAPIList(libcalicoListObject resourceListObject, aapiListObj runtime.Object, pred storage.SelectionPredicate) {
	lcgGlobalAlertTemplateList := libcalicoListObject.(*v3.GlobalAlertTemplateList)
	aapiGlobalAlertTemplateList := aapiListObj.(*v3.GlobalAlertTemplateList)
	if libcalicoListObject == nil {
		aapiGlobalAlertTemplateList.Items = []v3.GlobalAlertTemplate{}
		return
	}
	aapiGlobalAlertTemplateList.TypeMeta = lcgGlobalAlertTemplateList.TypeMeta
	aapiGlobalAlertTemplateList.ListMeta = lcgGlobalAlertTemplateList.ListMeta
	for _, item := range lcgGlobalAlertTemplateList.Items {
		aapiGlobalAlertTemplate := v3.GlobalAlertTemplate{}
		gc.convertToAAPI(&item, &aapiGlobalAlertTemplate)
		if matched, err := pred.Matches(&aapiGlobalAlertTemplate); err == nil && matched {
			aapiGlobalAlertTemplateList.Items = append(aapiGlobalAlertTemplateList.Items, aapiGlobalAlertTemplate)
		}
	}
}
