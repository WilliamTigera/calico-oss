// Copyright (c) 2023 Tigera, Inc. All rights reserved.
package labels

import apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

// AddKindandNameLabels adds the NetworkSet Kind and Name labels.
func AddKindandNameLabels(name string, labels map[string]string) map[string]string {
	// Create the map if it is nil
	if labels == nil {
		labels = make(map[string]string, 2)
	}
	labels[apiv3.LabelKind] = apiv3.KindNetworkSet
	labels[apiv3.LabelName] = name

	return labels
}

// ValidateNetworkSetLabels returns true if the labels contain NetworkSet key-value pairs Kind and
// Name.
func ValidateNetworkSetLabels(name string, labels map[string]string) bool {
	if len(labels) == 0 {
		return false
	}

	return labels[apiv3.LabelKind] == apiv3.KindNetworkSet && labels[apiv3.LabelName] == name
}
