// Copyright (c) 2019 Tigera, Inc. All rights reserved.

package managedcluster

import (
	"context"
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
	apivalidation "k8s.io/kubernetes/pkg/apis/core/validation"

	calico "github.com/tigera/api/pkg/apis/projectcalico/v3"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
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
func (apiServerStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	managedCluster := obj.(*calico.ManagedCluster)
	managedCluster.Status = v3.ManagedClusterStatus{
		Conditions: []v3.ManagedClusterStatusCondition{
			{
				Status: v3.ManagedClusterStatusValueUnknown,
				Type:   v3.ManagedClusterStatusTypeConnected,
			},
		},
	}
}

func (apiServerStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (apiServerStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

func (apiServerStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (apiServerStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (apiServerStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return []string{}
}

func (apiServerStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return []string{}
}

func (apiServerStrategy) Canonicalize(obj runtime.Object) {
}

func (apiServerStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return ValidateManagedClusterUpdate(obj.(*calico.ManagedCluster), old.(*calico.ManagedCluster))
}

type apiServerStatusStrategy struct {
	apiServerStrategy
}

func NewStatusStrategy(strategy apiServerStrategy) apiServerStatusStrategy {
	return apiServerStatusStrategy{strategy}
}

func (apiServerStatusStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	newManagedCluster := obj.(*calico.ManagedCluster)
	oldManagedCluster := old.(*calico.ManagedCluster)
	newManagedCluster.Spec = oldManagedCluster.Spec
	newManagedCluster.Labels = oldManagedCluster.Labels
}

// ValidateUpdate is the default update validation for an end user updating status
func (apiServerStatusStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return ValidateManagedClusterUpdate(obj.(*calico.ManagedCluster), old.(*calico.ManagedCluster))
}

func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*calico.ManagedCluster)
	if !ok {
		return nil, nil, fmt.Errorf("given object (type %v) is not a Managed Cluster", reflect.TypeOf(obj))
	}
	return labels.Set(apiserver.ObjectMeta.Labels), ManagedClusterToSelectableFields(apiserver), nil
}

// MatchManagedCluster is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchManagedCluster(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// ManagedClusterToSelectableFields returns a field set that represents the object.
func ManagedClusterToSelectableFields(obj *calico.ManagedCluster) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, false)
}

func ValidateManagedClusterUpdate(update, old *calico.ManagedCluster) field.ErrorList {
	return apivalidation.ValidateObjectMetaUpdate(&update.ObjectMeta, &old.ObjectMeta, field.NewPath("metadata"))
}
