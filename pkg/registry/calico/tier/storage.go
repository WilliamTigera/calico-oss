/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing persmissions and
limitations under the License.
*/

package tier

import (
	"github.com/tigera/calico-k8sapiserver/pkg/apis/calico"
	"github.com/tigera/calico-k8sapiserver/pkg/registry/calico/server"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/generic/registry"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/client-go/pkg/api"
)

// REST implements a RESTStorage for API services against etcd
type REST struct {
	*genericregistry.Store
}

// EmptyObject returns an empty instance
func EmptyObject() runtime.Object {
	return &calico.Tier{}
}

// NewList returns a new shell of a binding list
func NewList() runtime.Object {
	return &calico.TierList{
	//TypeMeta: metav1.TypeMeta{},
	//Items:    []calico.Tier{},
	}
}

// NewREST returns a RESTStorage object that will work against API services.
func NewREST(opts server.Options) *REST {
	prefix := "/" + opts.ResourcePrefix()
	// We adapt the store's keyFunc so that we can use it with the StorageDecorator
	// without making any assumptions about where objects are stored in etcd
	keyFunc := func(obj runtime.Object) (string, error) {
		accessor, err := meta.Accessor(obj)
		if err != nil {
			return "", err
		}
		return registry.NamespaceKeyFunc(genericapirequest.WithNamespace(genericapirequest.NewContext(), accessor.GetNamespace()), prefix, accessor.GetName())
	}
	storageInterface, dFunc := opts.GetStorage(
		1000,
		&calico.NetworkPolicy{},
		prefix,
		keyFunc,
		Strategy,
		func() runtime.Object { return &calico.TierList{} },
		GetAttrs,
		storage.NoTriggerPublisher,
	)
	store := &genericregistry.Store{
		Copier:      api.Scheme,
		NewFunc:     func() runtime.Object { return &calico.Tier{} },
		NewListFunc: func() runtime.Object { return &calico.TierList{} },
		KeyRootFunc: opts.KeyRootFunc(true),
		KeyFunc:     opts.KeyFunc(true),
		ObjectNameFunc: func(obj runtime.Object) (string, error) {
			return obj.(*calico.Tier).Name, nil
		},
		PredicateFunc:     MatchTier,
		QualifiedResource: calico.Resource("tiers"),

		CreateStrategy:          Strategy,
		UpdateStrategy:          Strategy,
		DeleteStrategy:          Strategy,
		EnableGarbageCollection: true,

		Storage:     storageInterface,
		DestroyFunc: dFunc,
	}

	return &REST{store}
}
