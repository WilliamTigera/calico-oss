// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// +k8s:openapi-gen=true
// +kubebuilder:resource:scope=Cluster
type UISettingsGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              v3.UISettingsGroupSpec `json:"spec,omitempty"`
}
