/*
Copyright 2021 Terway Authors.

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
// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	"context"

	v1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeNodeRuntimes implements NodeRuntimeInterface
type FakeNodeRuntimes struct {
	Fake *FakeNetworkV1beta1
}

var noderuntimesResource = schema.GroupVersionResource{Group: "network.alibabacloud.com", Version: "v1beta1", Resource: "noderuntimes"}

var noderuntimesKind = schema.GroupVersionKind{Group: "network.alibabacloud.com", Version: "v1beta1", Kind: "NodeRuntime"}

// Get takes name of the nodeRuntime, and returns the corresponding nodeRuntime object, and an error if there is any.
func (c *FakeNodeRuntimes) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1beta1.NodeRuntime, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootGetAction(noderuntimesResource, name), &v1beta1.NodeRuntime{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.NodeRuntime), err
}

// List takes label and field selectors, and returns the list of NodeRuntimes that match those selectors.
func (c *FakeNodeRuntimes) List(ctx context.Context, opts v1.ListOptions) (result *v1beta1.NodeRuntimeList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootListAction(noderuntimesResource, noderuntimesKind, opts), &v1beta1.NodeRuntimeList{})
	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1beta1.NodeRuntimeList{ListMeta: obj.(*v1beta1.NodeRuntimeList).ListMeta}
	for _, item := range obj.(*v1beta1.NodeRuntimeList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested nodeRuntimes.
func (c *FakeNodeRuntimes) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewRootWatchAction(noderuntimesResource, opts))
}

// Create takes the representation of a nodeRuntime and creates it.  Returns the server's representation of the nodeRuntime, and an error, if there is any.
func (c *FakeNodeRuntimes) Create(ctx context.Context, nodeRuntime *v1beta1.NodeRuntime, opts v1.CreateOptions) (result *v1beta1.NodeRuntime, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootCreateAction(noderuntimesResource, nodeRuntime), &v1beta1.NodeRuntime{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.NodeRuntime), err
}

// Update takes the representation of a nodeRuntime and updates it. Returns the server's representation of the nodeRuntime, and an error, if there is any.
func (c *FakeNodeRuntimes) Update(ctx context.Context, nodeRuntime *v1beta1.NodeRuntime, opts v1.UpdateOptions) (result *v1beta1.NodeRuntime, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateAction(noderuntimesResource, nodeRuntime), &v1beta1.NodeRuntime{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.NodeRuntime), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeNodeRuntimes) UpdateStatus(ctx context.Context, nodeRuntime *v1beta1.NodeRuntime, opts v1.UpdateOptions) (*v1beta1.NodeRuntime, error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateSubresourceAction(noderuntimesResource, "status", nodeRuntime), &v1beta1.NodeRuntime{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.NodeRuntime), err
}

// Delete takes name of the nodeRuntime and deletes it. Returns an error if one occurs.
func (c *FakeNodeRuntimes) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewRootDeleteActionWithOptions(noderuntimesResource, name, opts), &v1beta1.NodeRuntime{})
	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeNodeRuntimes) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	action := testing.NewRootDeleteCollectionAction(noderuntimesResource, listOpts)

	_, err := c.Fake.Invokes(action, &v1beta1.NodeRuntimeList{})
	return err
}

// Patch applies the patch and returns the patched nodeRuntime.
func (c *FakeNodeRuntimes) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1beta1.NodeRuntime, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootPatchSubresourceAction(noderuntimesResource, name, pt, data, subresources...), &v1beta1.NodeRuntime{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.NodeRuntime), err
}