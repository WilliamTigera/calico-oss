/*
Copyright 2017-2020 The Kubernetes Authors.

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

package licensekey

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"

	licClient "github.com/projectcalico/calico/licensing/client"

	calico "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/calico/apiserver/pkg/helpers"

	libcalicoapi "github.com/tigera/api/pkg/apis/projectcalico/v3"
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

func (a apiServerStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	lcgLicenseKey := convertToLibcalico(obj)
	lcgLicenseKey.TypeMeta.APIVersion = libcalicoapi.GroupVersionCurrent
	lcgLicenseKey.TypeMeta.Kind = libcalicoapi.KindLicenseKey

	licClaims, err := licClient.Decode(*lcgLicenseKey)
	if err != nil {
		return
	}

	aapiLicenseKey := obj.(*calico.LicenseKey)
	if licClaims.Validate() == licClient.Valid {
		aapiLicenseKey.Status = libcalicoapi.LicenseKeyStatus{
			Expiry:   metav1.Time{Time: licClaims.Expiry.Time()},
			MaxNodes: *licClaims.Nodes, Package: helpers.ConvertToPackageType(*&licClaims.Features)}
	}
}

func (apiServerStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {

	lcgLicenseKey := convertToLibcalico(obj)
	licClaims, err := licClient.Decode(*lcgLicenseKey)
	if err != nil {
		return
	}

	newLicenseKey := obj.(*calico.LicenseKey)
	if licClaims.Validate() == licClient.Valid {
		newLicenseKey.Status = libcalicoapi.LicenseKeyStatus{
			Expiry:   metav1.Time{Time: licClaims.Expiry.Time()},
			MaxNodes: *licClaims.Nodes,
			Package:  helpers.ConvertToPackageType(*&licClaims.Features),
			Features: helpers.ExpandFeatureNames(*&licClaims.Features),
		}
	}
}

func (apiServerStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	return validateLicenseKey(obj)
}

func (apiServerStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (apiServerStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (apiServerStrategy) Canonicalize(obj runtime.Object) {
}

func (apiServerStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return validateLicenseKey(obj)
}

type apiServerStatusStrategy struct {
	apiServerStrategy
}

func NewStatusStrategy(strategy apiServerStrategy) apiServerStatusStrategy {
	return apiServerStatusStrategy{strategy}
}

func (apiServerStatusStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	lcgLicenseKey := convertToLibcalico(obj)
	licClaims, err := licClient.Decode(*lcgLicenseKey)
	if err != nil {
		return
	}
	newLicenseKey := obj.(*calico.LicenseKey)
	newLicenseKey.Status = libcalicoapi.LicenseKeyStatus{
		Expiry:   metav1.Time{Time: licClaims.Expiry.Time()},
		MaxNodes: *licClaims.Nodes,
		Package:  helpers.ConvertToPackageType(*&licClaims.Features),
		Features: helpers.ExpandFeatureNames(*&licClaims.Features),
	}
}

// ValidateUpdate is the default update validation for an end user updating status
func (apiServerStatusStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return validateLicenseKey(obj)
}

func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*calico.LicenseKey)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a License Key")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), LicenseKeyToSelectableFields(apiserver), nil
}

// MatchLicenseKey is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchLicenseKey(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// LicenseKeyToSelectableFields returns a field set that represents the object.
func LicenseKeyToSelectableFields(obj *calico.LicenseKey) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, false)
}

// Convert from aggregated api server runtime object to libcalico-go's licensekey structure
func convertToLibcalico(aapiObj runtime.Object) *libcalicoapi.LicenseKey {
	aapiLicenseKey := aapiObj.(*calico.LicenseKey)
	lcgLicenseKey := &libcalicoapi.LicenseKey{}
	lcgLicenseKey.TypeMeta = aapiLicenseKey.TypeMeta
	lcgLicenseKey.ObjectMeta = aapiLicenseKey.ObjectMeta
	lcgLicenseKey.Spec = aapiLicenseKey.Spec
	return lcgLicenseKey
}

// Ensure licenseKey is decodable and valid (not expired)
func validateLicenseKey(aapiObj runtime.Object) field.ErrorList {
	allErrs := field.ErrorList{}
	lcgLicenseKey := convertToLibcalico(aapiObj)

	// Decode the license to make sure it's not corrupt.
	licClaims, err := licClient.Decode(*lcgLicenseKey)
	if err != nil {
		allErrs = append(allErrs, field.InternalError(field.NewPath("LicenseKeySpec").Child("license"),
			fmt.Errorf("license is corrupted: %s", err)))
	} else {
		// Check if the license is expired
		if licClaims.Validate() != licClient.Valid {
			allErrs = append(allErrs, field.InternalError(field.NewPath("LicenseKeySpec").Child("token"),
				fmt.Errorf("the license you're trying to create expired on %s", licClaims.Expiry.Time().Local())))
		}
	}

	return allErrs
}
