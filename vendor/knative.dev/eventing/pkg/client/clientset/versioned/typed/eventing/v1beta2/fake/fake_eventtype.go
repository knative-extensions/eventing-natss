/*
Copyright 2021 The Knative Authors

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

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
	v1beta2 "knative.dev/eventing/pkg/apis/eventing/v1beta2"
)

// FakeEventTypes implements EventTypeInterface
type FakeEventTypes struct {
	Fake *FakeEventingV1beta2
	ns   string
}

var eventtypesResource = v1beta2.SchemeGroupVersion.WithResource("eventtypes")

var eventtypesKind = v1beta2.SchemeGroupVersion.WithKind("EventType")

// Get takes name of the eventType, and returns the corresponding eventType object, and an error if there is any.
func (c *FakeEventTypes) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1beta2.EventType, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewGetAction(eventtypesResource, c.ns, name), &v1beta2.EventType{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta2.EventType), err
}

// List takes label and field selectors, and returns the list of EventTypes that match those selectors.
func (c *FakeEventTypes) List(ctx context.Context, opts v1.ListOptions) (result *v1beta2.EventTypeList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewListAction(eventtypesResource, eventtypesKind, c.ns, opts), &v1beta2.EventTypeList{})

	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1beta2.EventTypeList{ListMeta: obj.(*v1beta2.EventTypeList).ListMeta}
	for _, item := range obj.(*v1beta2.EventTypeList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested eventTypes.
func (c *FakeEventTypes) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewWatchAction(eventtypesResource, c.ns, opts))

}

// Create takes the representation of a eventType and creates it.  Returns the server's representation of the eventType, and an error, if there is any.
func (c *FakeEventTypes) Create(ctx context.Context, eventType *v1beta2.EventType, opts v1.CreateOptions) (result *v1beta2.EventType, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewCreateAction(eventtypesResource, c.ns, eventType), &v1beta2.EventType{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta2.EventType), err
}

// Update takes the representation of a eventType and updates it. Returns the server's representation of the eventType, and an error, if there is any.
func (c *FakeEventTypes) Update(ctx context.Context, eventType *v1beta2.EventType, opts v1.UpdateOptions) (result *v1beta2.EventType, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateAction(eventtypesResource, c.ns, eventType), &v1beta2.EventType{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta2.EventType), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeEventTypes) UpdateStatus(ctx context.Context, eventType *v1beta2.EventType, opts v1.UpdateOptions) (*v1beta2.EventType, error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateSubresourceAction(eventtypesResource, "status", c.ns, eventType), &v1beta2.EventType{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta2.EventType), err
}

// Delete takes name of the eventType and deletes it. Returns an error if one occurs.
func (c *FakeEventTypes) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewDeleteActionWithOptions(eventtypesResource, c.ns, name, opts), &v1beta2.EventType{})

	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeEventTypes) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	action := testing.NewDeleteCollectionAction(eventtypesResource, c.ns, listOpts)

	_, err := c.Fake.Invokes(action, &v1beta2.EventTypeList{})
	return err
}

// Patch applies the patch and returns the patched eventType.
func (c *FakeEventTypes) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1beta2.EventType, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewPatchSubresourceAction(eventtypesResource, c.ns, name, pt, data, subresources...), &v1beta2.EventType{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta2.EventType), err
}
