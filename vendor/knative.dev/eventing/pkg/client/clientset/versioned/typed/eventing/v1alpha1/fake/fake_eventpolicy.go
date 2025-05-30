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
	gentype "k8s.io/client-go/gentype"
	v1alpha1 "knative.dev/eventing/pkg/apis/eventing/v1alpha1"
	eventingv1alpha1 "knative.dev/eventing/pkg/client/clientset/versioned/typed/eventing/v1alpha1"
)

// fakeEventPolicies implements EventPolicyInterface
type fakeEventPolicies struct {
	*gentype.FakeClientWithList[*v1alpha1.EventPolicy, *v1alpha1.EventPolicyList]
	Fake *FakeEventingV1alpha1
}

func newFakeEventPolicies(fake *FakeEventingV1alpha1, namespace string) eventingv1alpha1.EventPolicyInterface {
	return &fakeEventPolicies{
		gentype.NewFakeClientWithList[*v1alpha1.EventPolicy, *v1alpha1.EventPolicyList](
			fake.Fake,
			namespace,
			v1alpha1.SchemeGroupVersion.WithResource("eventpolicies"),
			v1alpha1.SchemeGroupVersion.WithKind("EventPolicy"),
			func() *v1alpha1.EventPolicy { return &v1alpha1.EventPolicy{} },
			func() *v1alpha1.EventPolicyList { return &v1alpha1.EventPolicyList{} },
			func(dst, src *v1alpha1.EventPolicyList) { dst.ListMeta = src.ListMeta },
			func(list *v1alpha1.EventPolicyList) []*v1alpha1.EventPolicy {
				return gentype.ToPointerSlice(list.Items)
			},
			func(list *v1alpha1.EventPolicyList, items []*v1alpha1.EventPolicy) {
				list.Items = gentype.FromPointerSlice(items)
			},
		),
		fake,
	}
}
