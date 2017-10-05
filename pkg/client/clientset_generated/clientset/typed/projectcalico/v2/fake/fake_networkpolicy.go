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

package fake

import (
	v2 "github.com/tigera/calico-k8sapiserver/pkg/apis/calico/v2"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeNetworkPolicies implements NetworkPolicyInterface
type FakeNetworkPolicies struct {
	Fake *FakeProjectcalicoV2
	ns   string
}

var networkpoliciesResource = schema.GroupVersionResource{Group: "projectcalico.org", Version: "v2", Resource: "networkpolicies"}

var networkpoliciesKind = schema.GroupVersionKind{Group: "projectcalico.org", Version: "v2", Kind: "NetworkPolicy"}

func (c *FakeNetworkPolicies) Create(networkPolicy *v2.NetworkPolicy) (result *v2.NetworkPolicy, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewCreateAction(networkpoliciesResource, c.ns, networkPolicy), &v2.NetworkPolicy{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v2.NetworkPolicy), err
}

func (c *FakeNetworkPolicies) Update(networkPolicy *v2.NetworkPolicy) (result *v2.NetworkPolicy, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateAction(networkpoliciesResource, c.ns, networkPolicy), &v2.NetworkPolicy{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v2.NetworkPolicy), err
}

func (c *FakeNetworkPolicies) Delete(name string, options *v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewDeleteAction(networkpoliciesResource, c.ns, name), &v2.NetworkPolicy{})

	return err
}

func (c *FakeNetworkPolicies) DeleteCollection(options *v1.DeleteOptions, listOptions v1.ListOptions) error {
	action := testing.NewDeleteCollectionAction(networkpoliciesResource, c.ns, listOptions)

	_, err := c.Fake.Invokes(action, &v2.NetworkPolicyList{})
	return err
}

func (c *FakeNetworkPolicies) Get(name string, options v1.GetOptions) (result *v2.NetworkPolicy, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewGetAction(networkpoliciesResource, c.ns, name), &v2.NetworkPolicy{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v2.NetworkPolicy), err
}

func (c *FakeNetworkPolicies) List(opts v1.ListOptions) (result *v2.NetworkPolicyList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewListAction(networkpoliciesResource, networkpoliciesKind, c.ns, opts), &v2.NetworkPolicyList{})

	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v2.NetworkPolicyList{}
	for _, item := range obj.(*v2.NetworkPolicyList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested networkPolicies.
func (c *FakeNetworkPolicies) Watch(opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewWatchAction(networkpoliciesResource, c.ns, opts))

}

// Patch applies the patch and returns the patched networkPolicy.
func (c *FakeNetworkPolicies) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v2.NetworkPolicy, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewPatchSubresourceAction(networkpoliciesResource, c.ns, name, data, subresources...), &v2.NetworkPolicy{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v2.NetworkPolicy), err
}
