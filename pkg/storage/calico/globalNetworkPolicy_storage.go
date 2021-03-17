// Copyright (c) 2017-2019 Tigera, Inc. All rights reserved.

package calico

import (
	"reflect"

	"golang.org/x/net/context"

	aapi "github.com/tigera/apiserver/pkg/apis/projectcalico"
	"github.com/tigera/licensing/client/features"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/storage"
	etcd "k8s.io/apiserver/pkg/storage/etcd3"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"

	libcalicoapi "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/options"
	"github.com/projectcalico/libcalico-go/lib/watch"
)

// NewGlobalNetworkPolicyStorage creates a new libcalico-based storage.Interface implementation for GlobalNetworkPolicies
func NewGlobalNetworkPolicyStorage(opts Options) (registry.DryRunnableStorage, factory.DestroyFunc) {
	c := CreateClientFromConfig()
	createFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*libcalicoapi.GlobalNetworkPolicy)
		return c.GlobalNetworkPolicies().Create(ctx, res, oso)
	}
	updateFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*libcalicoapi.GlobalNetworkPolicy)
		return c.GlobalNetworkPolicies().Update(ctx, res, oso)
	}
	getFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		ogo := opts.(options.GetOptions)
		return c.GlobalNetworkPolicies().Get(ctx, name, ogo)
	}
	deleteFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		odo := opts.(options.DeleteOptions)
		return c.GlobalNetworkPolicies().Delete(ctx, name, odo)
	}
	listFn := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (resourceListObject, error) {
		olo := opts.(options.ListOptions)
		return c.GlobalNetworkPolicies().List(ctx, olo)
	}
	watchFn := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (watch.Interface, error) {
		olo := opts.(options.ListOptions)
		return c.GlobalNetworkPolicies().Watch(ctx, olo)
	}
	hasRestrictionsFn := func(obj resourceObject, licensedFeatures []string) bool {
		var hasFeatures bool
		for _, k := range licensedFeatures {
			if k == features.EgressAccessControl || k == features.All {
				hasFeatures = true
			}
		}

		if !hasFeatures && HasDNSDomains(obj.GetObjectKind().GroupVersionKind().String(), obj) {
			return true
		}

		return false

	}

	// TODO(doublek): Inject codec, client for nicer testing.
	dryRunnableStorage := registry.DryRunnableStorage{Storage: &resourceStore{
		client:            c,
		codec:             opts.RESTOptions.StorageConfig.Codec,
		versioner:         etcd.APIObjectVersioner{},
		aapiType:          reflect.TypeOf(aapi.GlobalNetworkPolicy{}),
		aapiListType:      reflect.TypeOf(aapi.GlobalNetworkPolicyList{}),
		libCalicoType:     reflect.TypeOf(libcalicoapi.GlobalNetworkPolicy{}),
		libCalicoListType: reflect.TypeOf(libcalicoapi.GlobalNetworkPolicyList{}),
		isNamespaced:      false,
		create:            createFn,
		update:            updateFn,
		get:               getFn,
		delete:            deleteFn,
		list:              listFn,
		watch:             watchFn,
		resourceName:      "GlobalNetworkPolicy",
		converter:         GlobalNetworkPolicyConverter{},
		licenseCache:      opts.LicenseCache,
		hasRestrictions:   hasRestrictionsFn,
	}, Codec: opts.RESTOptions.StorageConfig.Codec}
	return dryRunnableStorage, func() {}
}

type GlobalNetworkPolicyConverter struct {
}

func (gc GlobalNetworkPolicyConverter) convertToLibcalico(aapiObj runtime.Object) resourceObject {
	aapiGlobalNetworkPolicy := aapiObj.(*aapi.GlobalNetworkPolicy)
	lcgGlobalNetworkPolicy := &libcalicoapi.GlobalNetworkPolicy{}
	lcgGlobalNetworkPolicy.TypeMeta = aapiGlobalNetworkPolicy.TypeMeta
	lcgGlobalNetworkPolicy.ObjectMeta = aapiGlobalNetworkPolicy.ObjectMeta
	lcgGlobalNetworkPolicy.Kind = libcalicoapi.KindGlobalNetworkPolicy
	lcgGlobalNetworkPolicy.APIVersion = libcalicoapi.GroupVersionCurrent
	lcgGlobalNetworkPolicy.Spec = aapiGlobalNetworkPolicy.Spec
	return lcgGlobalNetworkPolicy
}

func (gc GlobalNetworkPolicyConverter) convertToAAPI(libcalicoObject resourceObject, aapiObj runtime.Object) {
	lcgGlobalNetworkPolicy := libcalicoObject.(*libcalicoapi.GlobalNetworkPolicy)
	aapiGlobalNetworkPolicy := aapiObj.(*aapi.GlobalNetworkPolicy)
	aapiGlobalNetworkPolicy.Spec = lcgGlobalNetworkPolicy.Spec
	// Tier field maybe left blank when policy created vi OS libcalico.
	// Initialize it to default in that case to make work with field selector.
	if aapiGlobalNetworkPolicy.Spec.Tier == "" {
		aapiGlobalNetworkPolicy.Spec.Tier = "default"
	}
	aapiGlobalNetworkPolicy.TypeMeta = lcgGlobalNetworkPolicy.TypeMeta
	aapiGlobalNetworkPolicy.ObjectMeta = lcgGlobalNetworkPolicy.ObjectMeta
	// Labeling Purely for kubectl purposes. ex: kubectl get globalnetworkpolicies -l projectcalico.org/tier=net-sec
	// kubectl 1.9 should come out with support for field selector.
	// Workflows associated with label "projectcalico.org/tier" should be deprecated thereafter.
	if aapiGlobalNetworkPolicy.Labels == nil {
		aapiGlobalNetworkPolicy.Labels = make(map[string]string)
	}
	aapiGlobalNetworkPolicy.Labels["projectcalico.org/tier"] = aapiGlobalNetworkPolicy.Spec.Tier
}

func (gc GlobalNetworkPolicyConverter) convertToAAPIList(libcalicoListObject resourceListObject, aapiListObj runtime.Object, pred storage.SelectionPredicate) {
	lcgGlobalNetworkPolicyList := libcalicoListObject.(*libcalicoapi.GlobalNetworkPolicyList)
	aapiGlobalNetworkPolicyList := aapiListObj.(*aapi.GlobalNetworkPolicyList)
	if libcalicoListObject == nil {
		aapiGlobalNetworkPolicyList.Items = []aapi.GlobalNetworkPolicy{}
		return
	}
	aapiGlobalNetworkPolicyList.TypeMeta = lcgGlobalNetworkPolicyList.TypeMeta
	aapiGlobalNetworkPolicyList.ListMeta = lcgGlobalNetworkPolicyList.ListMeta
	for _, item := range lcgGlobalNetworkPolicyList.Items {
		aapiGlobalNetworkPolicy := aapi.GlobalNetworkPolicy{}
		gc.convertToAAPI(&item, &aapiGlobalNetworkPolicy)
		if matched, err := pred.Matches(&aapiGlobalNetworkPolicy); err == nil && matched {
			aapiGlobalNetworkPolicyList.Items = append(aapiGlobalNetworkPolicyList.Items, aapiGlobalNetworkPolicy)
		}
	}
}
