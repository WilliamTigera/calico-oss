// Copyright (c) 2019 Tigera, Inc. All rights reserved.

package remoteclusterconfig

import (
	calico "github.com/projectcalico/apiserver/pkg/apis/projectcalico"
	"github.com/projectcalico/apiserver/pkg/registry/projectcalico/server"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/generic/registry"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
)

// rest implements a RESTStorage for API services against etcd
type REST struct {
	*genericregistry.Store
}

// EmptyObject returns an empty instance
func EmptyObject() runtime.Object {
	return &calico.RemoteClusterConfiguration{}
}

// NewList returns a new shell of a binding list
func NewList() runtime.Object {
	return &calico.RemoteClusterConfigurationList{}
}

// NewREST returns a RESTStorage object that will work against API services.
func NewREST(scheme *runtime.Scheme, opts server.Options) (*REST, error) {
	strategy := NewStrategy(scheme)

	prefix := "/" + opts.ResourcePrefix()
	// We adapt the store's keyFunc so that we can use it with the StorageDecorator
	// without making any assumptions about where objects are stored in etcd
	keyFunc := func(obj runtime.Object) (string, error) {
		accessor, err := meta.Accessor(obj)
		if err != nil {
			return "", err
		}
		return registry.NoNamespaceKeyFunc(
			genericapirequest.NewContext(),
			prefix,
			accessor.GetName(),
		)
	}
	storageInterface, dFunc, err := opts.GetStorage(
		prefix,
		keyFunc,
		strategy,
		func() runtime.Object { return &calico.RemoteClusterConfiguration{} },
		func() runtime.Object { return &calico.RemoteClusterConfigurationList{} },
		GetAttrs,
		nil,
		nil,
	)
	if err != nil {
		return nil, err
	}
	store := &genericregistry.Store{
		NewFunc:     func() runtime.Object { return &calico.RemoteClusterConfiguration{} },
		NewListFunc: func() runtime.Object { return &calico.RemoteClusterConfigurationList{} },
		KeyRootFunc: opts.KeyRootFunc(false),
		KeyFunc:     opts.KeyFunc(false),
		ObjectNameFunc: func(obj runtime.Object) (string, error) {
			return obj.(*calico.RemoteClusterConfiguration).Name, nil
		},
		PredicateFunc:            MatchRemoteClusterConfiguration,
		DefaultQualifiedResource: calico.Resource("remoteclusterconfigurations"),

		CreateStrategy:          strategy,
		UpdateStrategy:          strategy,
		DeleteStrategy:          strategy,
		EnableGarbageCollection: true,

		Storage:     storageInterface,
		DestroyFunc: dFunc,
	}

	return &REST{store}, nil
}
