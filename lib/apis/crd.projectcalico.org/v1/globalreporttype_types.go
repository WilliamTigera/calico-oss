// Copyright (c) 2020 Tigera, Inc. All rights reserved.

package v1

import (
	v3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// +k8s:openapi-gen=true
// +kubebuilder:resource:scope=Cluster
type GlobalReportType struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              v3.ReportTypeSpec `json:"spec,omitempty"`
}
