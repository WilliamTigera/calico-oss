// Copyright (c) 2019 Tigera, Inc. All rights reserved.

package v3

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupName is the group name use in this package
const GroupName = "projectcalico.org"

// SchemeGroupVersion is group version used to register these objects
var SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: "v3"}

var (
	// TODO: move SchemeBuilder with zz_generated.deepcopy.go to k8s.io/api.
	// localSchemeBuilder and AddToScheme will stay in k8s.io/kubernetes.
	SchemeBuilder      runtime.SchemeBuilder
	localSchemeBuilder = &SchemeBuilder
	// AddToScheme is exposed for API installation
	AddToScheme = localSchemeBuilder.AddToScheme
)

func init() {
	// We only register manually written functions here. The registration of the
	// generated functions takes place in the generated files. The separation
	// makes the code compile even when the generated files are missing.
	localSchemeBuilder.Register(addKnownTypes, addConversionFuncs)
}

// Adds the list of known types to api.Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&NetworkPolicy{},
		&NetworkPolicyList{},
		&Tier{},
		&TierList{},
		&GlobalNetworkPolicy{},
		&GlobalNetworkPolicyList{},
		&GlobalNetworkSet{},
		&GlobalNetworkSetList{},
		&LicenseKey{},
		&LicenseKeyList{},
		&GlobalThreatFeed{},
		&GlobalThreatFeedList{},
		&HostEndpoint{},
		&HostEndpointList{},
		&GlobalReport{},
		&GlobalReportList{},
		&GlobalReportType{},
		&GlobalReportTypeList{},
<<<<<<< HEAD
		&IPPool{},
		&IPPoolList{},
=======
		&BGPConfiguration{},
		&BGPConfigurationList{},
>>>>>>> 1f9fbe90... Added BGPConfiguration resource to AAPI server
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

// Resource takes an unqualified resource and returns a Group qualified GroupResource
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}
