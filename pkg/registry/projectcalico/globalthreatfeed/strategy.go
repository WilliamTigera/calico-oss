/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package globalthreatfeed

import (
	"fmt"
	"reflect"

	v3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
	apivalidation "k8s.io/kubernetes/pkg/apis/core/validation"

	calico "github.com/tigera/calico-k8sapiserver/pkg/apis/projectcalico"
)

type apiServerStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

// NewStrategy returns a new NamespaceScopedStrategy for instances
func NewStrategy(typer runtime.ObjectTyper) apiServerStrategy {
	return apiServerStrategy{typer, names.SimpleNameGenerator}
}

func (apiServerStrategy) NamespaceScoped() bool {
	return false
}

// PrepareForCreate clears the Status
func (apiServerStrategy) PrepareForCreate(ctx genericapirequest.Context, obj runtime.Object) {
	globalThreatFeed := obj.(*calico.GlobalThreatFeed)
	globalThreatFeed.Status = v3.GlobalThreatFeedStatus{}
}

// PrepareForUpdate copies the Status from old to obj
func (apiServerStrategy) PrepareForUpdate(ctx genericapirequest.Context, obj, old runtime.Object) {
	newGlobalThreatFeed := obj.(*calico.GlobalThreatFeed)
	oldGlobalThreatFeed := old.(*calico.GlobalThreatFeed)
	newGlobalThreatFeed.Status = oldGlobalThreatFeed.Status
}

func (apiServerStrategy) Validate(ctx genericapirequest.Context, obj runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

func (apiServerStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (apiServerStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (apiServerStrategy) Canonicalize(obj runtime.Object) {
}

func (apiServerStrategy) ValidateUpdate(ctx genericapirequest.Context, obj, old runtime.Object) field.ErrorList {
	return ValidateGlobalThreatFeedUpdate(obj.(*calico.GlobalThreatFeed), old.(*calico.GlobalThreatFeed))
}

type apiServerStatusStrategy struct {
	apiServerStrategy
}

func NewStatusStrategy(strategy apiServerStrategy) apiServerStatusStrategy {
	return apiServerStatusStrategy{strategy}
}

func (apiServerStatusStrategy) PrepareForUpdate(ctx genericapirequest.Context, obj, old runtime.Object) {
	newGlobalThreatFeed := obj.(*calico.GlobalThreatFeed)
	oldGlobalThreatFeed := old.(*calico.GlobalThreatFeed)
	newGlobalThreatFeed.Spec = oldGlobalThreatFeed.Spec
	newGlobalThreatFeed.Labels = oldGlobalThreatFeed.Labels
}

// ValidateUpdate is the default update validation for an end user updating status
func (apiServerStatusStrategy) ValidateUpdate(ctx genericapirequest.Context, obj, old runtime.Object) field.ErrorList {
	return ValidateGlobalThreatFeedUpdate(obj.(*calico.GlobalThreatFeed), old.(*calico.GlobalThreatFeed))
}

func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, bool, error) {
	apiserver, ok := obj.(*calico.GlobalThreatFeed)
	if !ok {
		return nil, nil, false, fmt.Errorf("given object (type %v) is not a Global Threat Feed", reflect.TypeOf(obj))
	}
	return labels.Set(apiserver.ObjectMeta.Labels), ThreatFeedToSelectableFields(apiserver), apiserver.Initializers != nil, nil
}

// MatchThreatFeed is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchThreatFeed(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// ThreatFeedToSelectableFields returns a field set that represents the object.
func ThreatFeedToSelectableFields(obj *calico.GlobalThreatFeed) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, false)
}

func ValidateGlobalThreatFeedUpdate(update, old *calico.GlobalThreatFeed) field.ErrorList {
	return apivalidation.ValidateObjectMetaUpdate(&update.ObjectMeta, &old.ObjectMeta, field.NewPath("metadata"))
}
