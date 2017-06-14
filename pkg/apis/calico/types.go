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

package calico

import (
	calico "github.com/projectcalico/libcalico-go/lib/backend/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PolicyList is a list of Policy objects.
type PolicyList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []Policy
}

type PolicyStatus struct {
}

// +genclient=true

type Policy struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   calico.Policy
	Status PolicyStatus
}

// TierList is a list of Policy objects.
type TierList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []Tier
}

type Tier struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.Tier
}
