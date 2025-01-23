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
	v1alpha1 "knative.dev/eventing/pkg/apis/sources/v1alpha1"
)

// FakeIntegrationSources implements IntegrationSourceInterface
type FakeIntegrationSources struct {
	Fake *FakeSourcesV1alpha1
	ns   string
}

var integrationsourcesResource = v1alpha1.SchemeGroupVersion.WithResource("integrationsources")

var integrationsourcesKind = v1alpha1.SchemeGroupVersion.WithKind("IntegrationSource")

// Get takes name of the integrationSource, and returns the corresponding integrationSource object, and an error if there is any.
func (c *FakeIntegrationSources) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha1.IntegrationSource, err error) {
	emptyResult := &v1alpha1.IntegrationSource{}
	obj, err := c.Fake.
		Invokes(testing.NewGetActionWithOptions(integrationsourcesResource, c.ns, name, options), emptyResult)

	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1alpha1.IntegrationSource), err
}

// List takes label and field selectors, and returns the list of IntegrationSources that match those selectors.
func (c *FakeIntegrationSources) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha1.IntegrationSourceList, err error) {
	emptyResult := &v1alpha1.IntegrationSourceList{}
	obj, err := c.Fake.
		Invokes(testing.NewListActionWithOptions(integrationsourcesResource, integrationsourcesKind, c.ns, opts), emptyResult)

	if obj == nil {
		return emptyResult, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1alpha1.IntegrationSourceList{ListMeta: obj.(*v1alpha1.IntegrationSourceList).ListMeta}
	for _, item := range obj.(*v1alpha1.IntegrationSourceList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested integrationSources.
func (c *FakeIntegrationSources) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewWatchActionWithOptions(integrationsourcesResource, c.ns, opts))

}

// Create takes the representation of a integrationSource and creates it.  Returns the server's representation of the integrationSource, and an error, if there is any.
func (c *FakeIntegrationSources) Create(ctx context.Context, integrationSource *v1alpha1.IntegrationSource, opts v1.CreateOptions) (result *v1alpha1.IntegrationSource, err error) {
	emptyResult := &v1alpha1.IntegrationSource{}
	obj, err := c.Fake.
		Invokes(testing.NewCreateActionWithOptions(integrationsourcesResource, c.ns, integrationSource, opts), emptyResult)

	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1alpha1.IntegrationSource), err
}

// Update takes the representation of a integrationSource and updates it. Returns the server's representation of the integrationSource, and an error, if there is any.
func (c *FakeIntegrationSources) Update(ctx context.Context, integrationSource *v1alpha1.IntegrationSource, opts v1.UpdateOptions) (result *v1alpha1.IntegrationSource, err error) {
	emptyResult := &v1alpha1.IntegrationSource{}
	obj, err := c.Fake.
		Invokes(testing.NewUpdateActionWithOptions(integrationsourcesResource, c.ns, integrationSource, opts), emptyResult)

	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1alpha1.IntegrationSource), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeIntegrationSources) UpdateStatus(ctx context.Context, integrationSource *v1alpha1.IntegrationSource, opts v1.UpdateOptions) (result *v1alpha1.IntegrationSource, err error) {
	emptyResult := &v1alpha1.IntegrationSource{}
	obj, err := c.Fake.
		Invokes(testing.NewUpdateSubresourceActionWithOptions(integrationsourcesResource, "status", c.ns, integrationSource, opts), emptyResult)

	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1alpha1.IntegrationSource), err
}

// Delete takes name of the integrationSource and deletes it. Returns an error if one occurs.
func (c *FakeIntegrationSources) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewDeleteActionWithOptions(integrationsourcesResource, c.ns, name, opts), &v1alpha1.IntegrationSource{})

	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeIntegrationSources) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	action := testing.NewDeleteCollectionActionWithOptions(integrationsourcesResource, c.ns, opts, listOpts)

	_, err := c.Fake.Invokes(action, &v1alpha1.IntegrationSourceList{})
	return err
}

// Patch applies the patch and returns the patched integrationSource.
func (c *FakeIntegrationSources) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.IntegrationSource, err error) {
	emptyResult := &v1alpha1.IntegrationSource{}
	obj, err := c.Fake.
		Invokes(testing.NewPatchSubresourceActionWithOptions(integrationsourcesResource, c.ns, name, pt, data, opts, subresources...), emptyResult)

	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1alpha1.IntegrationSource), err
}