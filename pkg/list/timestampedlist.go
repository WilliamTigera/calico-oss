// Copyright (c) 2019 Tigera, Inc. All rights reserved.
package list

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/projectcalico/libcalico-go/lib/resources"
)

// TimestampedResourceList is simply a resource list with additional timestamps indicating the request/response
// times of the list.
type TimestampedResourceList struct {
	resources.ResourceList    `json:",inline"`
	RequestStartedTimestamp   metav1.Time `json:"requestStartedTimestamp"`
	RequestCompletedTimestamp metav1.Time `json:"requestCompletedTimestamp"`
}

func (l *TimestampedResourceList) String() string {
	gvk := l.GetObjectKind().GroupVersionKind()
	return fmt.Sprintf("%s::%s::%s", l.RequestCompletedTimestamp.Format(time.RFC3339), gvk.GroupVersion().String(), gvk.Kind)
}

// UnmarshalJSON implements the unmarshalling interface for JSON. We need to implement this explicitly because the
// resource list is an interface but needs to be a specific type to allow for unmarshalling. We can determine the actual
// type by unmarshalling the TypeMeta first.
func (l *TimestampedResourceList) UnmarshalJSON(b []byte) error {
	var err error

	// Just extract the timestamp and kind fields from the blob.
	meta := new(struct {
		metav1.TypeMeta           `json:",inline"`
		RequestStartedTimestamp   metav1.Time `json:"requestStartedTimestamp"`
		RequestCompletedTimestamp metav1.Time `json:"requestCompletedTimestamp"`
	})
	if err = json.Unmarshal(b, meta); err != nil {
		return err
	}

	// Generate the appropriate list resource.
	l.ResourceList = resources.NewResourceList(meta.TypeMeta)
	if l.ResourceList == nil {
		return fmt.Errorf("unable to process resource: %s", meta.TypeMeta.GroupVersionKind())
	}

	// Unmarshal the full list object.
	if err = json.Unmarshal(b, &l.ResourceList); err != nil {
		return err
	}
	l.RequestStartedTimestamp = meta.RequestStartedTimestamp
	l.RequestCompletedTimestamp = meta.RequestCompletedTimestamp
	return nil
}

// MarshalJSON implements the marshalling interface for JSON. We need to implement this explicitly because the default
// implementation doesn't honor the "inline" directive when the parameter is an interface type.
func (l *TimestampedResourceList) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(l.ResourceList)
	if err != nil {
		return nil, err
	}

	buf := bytes.NewBuffer(bytes.TrimSuffix(b, []byte("}")))
	rst, err := l.RequestStartedTimestamp.MarshalJSON()
	if err != nil {
		return nil, err
	}
	rct, err := l.RequestCompletedTimestamp.MarshalJSON()
	if err != nil {
		return nil, err
	}
	buf.WriteString(fmt.Sprintf(`,"requestStartedTimestamp":%s,"requestCompletedTimestamp":%s}`, rst, rct))
	return buf.Bytes(), nil
}
