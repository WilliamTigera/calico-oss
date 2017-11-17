// Copyright (c) 2017 Tigera, Inc. All rights reserved.

package calico

import (
	"reflect"

	"golang.org/x/net/context"

	libcalicoapi "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/options"
	"github.com/projectcalico/libcalico-go/lib/watch"
	aapi "github.com/tigera/calico-k8sapiserver/pkg/apis/projectcalico"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/etcd"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
)

// NewGlobalNetworkPolicyStorage creates a new libcalico-based storage.Interface implementation for GlobalNetworkPolicies
func NewGlobalNetworkPolicyStorage(opts Options) (storage.Interface, factory.DestroyFunc) {
	c := createClientFromConfig()
	create := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*libcalicoapi.GlobalNetworkPolicy)
		return c.GlobalNetworkPolicies().Create(ctx, res, oso)
	}
	update := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*libcalicoapi.GlobalNetworkPolicy)
		return c.GlobalNetworkPolicies().Update(ctx, res, oso)
	}
	get := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		ogo := opts.(options.GetOptions)
		return c.GlobalNetworkPolicies().Get(ctx, name, ogo)
	}
	delete := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		odo := opts.(options.DeleteOptions)
		return c.GlobalNetworkPolicies().Delete(ctx, name, odo)
	}
	list := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (resourceListObject, error) {
		olo := opts.(options.ListOptions)
		return c.GlobalNetworkPolicies().List(ctx, olo)
	}
	watch := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (watch.Interface, error) {
		olo := opts.(options.ListOptions)
		return c.GlobalNetworkPolicies().Watch(ctx, olo)
	}
	// TODO(doublek): Inject codec, client for nicer testing.
	return &resourceStore{
		client:            c,
		codec:             opts.RESTOptions.StorageConfig.Codec,
		versioner:         etcd.APIObjectVersioner{},
		aapiType:          reflect.TypeOf(aapi.GlobalNetworkPolicy{}),
		aapiListType:      reflect.TypeOf(aapi.GlobalNetworkPolicyList{}),
		libCalicoType:     reflect.TypeOf(libcalicoapi.GlobalNetworkPolicy{}),
		libCalicoListType: reflect.TypeOf(libcalicoapi.GlobalNetworkPolicyList{}),
		isNamespaced:      false,
		create:            create,
		update:            update,
		get:               get,
		delete:            delete,
		list:              list,
		watch:             watch,
		resourceName:      "GlobalNetworkPolicy",
		converter:         GlobalNetworkPolicyConverter{},
	}, func() {}
}

type GlobalNetworkPolicyConverter struct {
}

func (gc GlobalNetworkPolicyConverter) convertToLibcalico(aapiObj runtime.Object) resourceObject {
	aapiGlobalNetworkPolicy := aapiObj.(*aapi.GlobalNetworkPolicy)
	lcgGlobalNetworkPolicy := &libcalicoapi.GlobalNetworkPolicy{}
	lcgGlobalNetworkPolicy.TypeMeta = aapiGlobalNetworkPolicy.TypeMeta
	lcgGlobalNetworkPolicy.ObjectMeta = aapiGlobalNetworkPolicy.ObjectMeta
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

func (gc GlobalNetworkPolicyConverter) convertToAAPIList(libcalicoListObject resourceListObject, aapiListObj runtime.Object, filterFunc storage.FilterFunc) {
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
		if filterFunc != nil && filterFunc(&aapiGlobalNetworkPolicy) {
			aapiGlobalNetworkPolicyList.Items = append(aapiGlobalNetworkPolicyList.Items, aapiGlobalNetworkPolicy)
		}
	}
}
