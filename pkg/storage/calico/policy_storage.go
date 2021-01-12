// Copyright (c) 2017-2019 Tigera, Inc. All rights reserved.

package calico

import (
	"reflect"
	"strings"

	"golang.org/x/net/context"

	aapi "github.com/tigera/apiserver/pkg/apis/projectcalico"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/storage"
	etcd "k8s.io/apiserver/pkg/storage/etcd3"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"

	libcalicoapi "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/backend/k8s/conversion"
	"github.com/projectcalico/libcalico-go/lib/clientv3"
	cerrors "github.com/projectcalico/libcalico-go/lib/errors"
	"github.com/projectcalico/libcalico-go/lib/options"
	"github.com/projectcalico/libcalico-go/lib/watch"
)

// NewNetworkPolicyStorage creates a new libcalico-based storage.Interface implementation for Policy
func NewNetworkPolicyStorage(opts Options) (registry.DryRunnableStorage, factory.DestroyFunc) {
	c := CreateClientFromConfig()
	createFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*libcalicoapi.NetworkPolicy)
		if strings.HasPrefix(res.Name, conversion.K8sNetworkPolicyNamePrefix) {
			return nil, cerrors.ErrorOperationNotSupported{
				Operation:  "create or apply",
				Identifier: obj,
				Reason:     "kubernetes network policies must be managed through the kubernetes API",
			}
		}
		return c.NetworkPolicies().Create(ctx, res, oso)
	}
	updateFn := func(ctx context.Context, c clientv3.Interface, obj resourceObject, opts clientOpts) (resourceObject, error) {
		oso := opts.(options.SetOptions)
		res := obj.(*libcalicoapi.NetworkPolicy)
		if strings.HasPrefix(res.Name, conversion.K8sNetworkPolicyNamePrefix) {
			return nil, cerrors.ErrorOperationNotSupported{
				Operation:  "update or apply",
				Identifier: obj,
				Reason:     "kubernetes network policies must be managed through the kubernetes API",
			}
		}
		return c.NetworkPolicies().Update(ctx, res, oso)
	}
	getFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		ogo := opts.(options.GetOptions)
		return c.NetworkPolicies().Get(ctx, ns, name, ogo)
	}
	deleteFn := func(ctx context.Context, c clientv3.Interface, ns string, name string, opts clientOpts) (resourceObject, error) {
		odo := opts.(options.DeleteOptions)
		if strings.HasPrefix(name, conversion.K8sNetworkPolicyNamePrefix) {
			return nil, cerrors.ErrorOperationNotSupported{
				Operation:  "delete",
				Identifier: name,
				Reason:     "kubernetes network policies must be managed through the kubernetes API",
			}
		}
		return c.NetworkPolicies().Delete(ctx, ns, name, odo)
	}
	listFn := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (resourceListObject, error) {
		olo := opts.(options.ListOptions)
		return c.NetworkPolicies().List(ctx, olo)
	}
	watchFn := func(ctx context.Context, c clientv3.Interface, opts clientOpts) (watch.Interface, error) {
		olo := opts.(options.ListOptions)
		return c.NetworkPolicies().Watch(ctx, olo)
	}
	// TODO(doublek): Inject codec, client for nicer testing.
	dryRunnableStorage := registry.DryRunnableStorage{Storage: &resourceStore{
		client:            c,
		codec:             opts.RESTOptions.StorageConfig.Codec,
		versioner:         APIObjectVersioner{&etcd.APIObjectVersioner{}},
		aapiType:          reflect.TypeOf(aapi.NetworkPolicy{}),
		aapiListType:      reflect.TypeOf(aapi.NetworkPolicyList{}),
		libCalicoType:     reflect.TypeOf(libcalicoapi.NetworkPolicy{}),
		libCalicoListType: reflect.TypeOf(libcalicoapi.NetworkPolicyList{}),
		isNamespaced:      true,
		create:            createFn,
		update:            updateFn,
		get:               getFn,
		delete:            deleteFn,
		list:              listFn,
		watch:             watchFn,
		resourceName:      "NetworkPolicy",
		converter:         NetworkPolicyConverter{},
		licenseCache:      opts.LicenseCache,
	}, Codec: opts.RESTOptions.StorageConfig.Codec}
	return dryRunnableStorage, func() {}
}

type NetworkPolicyConverter struct {
}

func (rc NetworkPolicyConverter) convertToLibcalico(aapiObj runtime.Object) resourceObject {
	aapiPolicy := aapiObj.(*aapi.NetworkPolicy)
	lcgPolicy := &libcalicoapi.NetworkPolicy{}
	lcgPolicy.TypeMeta = aapiPolicy.TypeMeta
	lcgPolicy.ObjectMeta = aapiPolicy.ObjectMeta
	lcgPolicy.Kind = libcalicoapi.KindNetworkPolicy
	lcgPolicy.APIVersion = libcalicoapi.GroupVersionCurrent
	lcgPolicy.Spec = aapiPolicy.Spec
	return lcgPolicy
}

func (rc NetworkPolicyConverter) convertToAAPI(libcalicoObject resourceObject, aapiObj runtime.Object) {
	lcgPolicy := libcalicoObject.(*libcalicoapi.NetworkPolicy)
	aapiPolicy := aapiObj.(*aapi.NetworkPolicy)
	aapiPolicy.Spec = lcgPolicy.Spec
	// Tier field maybe left blank when policy created vi OS libcalico.
	// Initialize it to default in that case to make work with field selector.
	if aapiPolicy.Spec.Tier == "" {
		aapiPolicy.Spec.Tier = "default"
	}
	aapiPolicy.TypeMeta = lcgPolicy.TypeMeta
	aapiPolicy.ObjectMeta = lcgPolicy.ObjectMeta
	// Labeling Purely for kubectl purposes. ex: kubectl get globalnetworkpolicies -l projectcalico.org/tier=net-sec
	// kubectl 1.9 should come out with support for field selector.
	// Workflows associated with label "projectcalico.org/tier" should be deprecated thereafter.
	if aapiPolicy.Labels == nil {
		aapiPolicy.Labels = make(map[string]string)
	}
	aapiPolicy.Labels["projectcalico.org/tier"] = aapiPolicy.Spec.Tier
}

func (rc NetworkPolicyConverter) convertToAAPIList(libcalicoListObject resourceListObject, aapiListObj runtime.Object, pred storage.SelectionPredicate) {
	lcgPolicyList := libcalicoListObject.(*libcalicoapi.NetworkPolicyList)
	aapiPolicyList := aapiListObj.(*aapi.NetworkPolicyList)
	if libcalicoListObject == nil {
		aapiPolicyList.Items = []aapi.NetworkPolicy{}
		return
	}
	aapiPolicyList.TypeMeta = lcgPolicyList.TypeMeta
	aapiPolicyList.ListMeta = lcgPolicyList.ListMeta
	for _, item := range lcgPolicyList.Items {
		aapiPolicy := aapi.NetworkPolicy{}
		rc.convertToAAPI(&item, &aapiPolicy)
		if matched, err := pred.Matches(&aapiPolicy); err == nil && matched {
			aapiPolicyList.Items = append(aapiPolicyList.Items, aapiPolicy)
		}
	}
}
