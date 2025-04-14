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

package v1

import (
	context "context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	gentype "k8s.io/client-go/gentype"
	flowsv1 "knative.dev/eventing/pkg/apis/flows/v1"
	scheme "knative.dev/eventing/pkg/client/clientset/versioned/scheme"
)

// SequencesGetter has a method to return a SequenceInterface.
// A group's client should implement this interface.
type SequencesGetter interface {
	Sequences(namespace string) SequenceInterface
}

// SequenceInterface has methods to work with Sequence resources.
type SequenceInterface interface {
	Create(ctx context.Context, sequence *flowsv1.Sequence, opts metav1.CreateOptions) (*flowsv1.Sequence, error)
	Update(ctx context.Context, sequence *flowsv1.Sequence, opts metav1.UpdateOptions) (*flowsv1.Sequence, error)
	// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
	UpdateStatus(ctx context.Context, sequence *flowsv1.Sequence, opts metav1.UpdateOptions) (*flowsv1.Sequence, error)
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*flowsv1.Sequence, error)
	List(ctx context.Context, opts metav1.ListOptions) (*flowsv1.SequenceList, error)
	Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (result *flowsv1.Sequence, err error)
	SequenceExpansion
}

// sequences implements SequenceInterface
type sequences struct {
	*gentype.ClientWithList[*flowsv1.Sequence, *flowsv1.SequenceList]
}

// newSequences returns a Sequences
func newSequences(c *FlowsV1Client, namespace string) *sequences {
	return &sequences{
		gentype.NewClientWithList[*flowsv1.Sequence, *flowsv1.SequenceList](
			"sequences",
			c.RESTClient(),
			scheme.ParameterCodec,
			namespace,
			func() *flowsv1.Sequence { return &flowsv1.Sequence{} },
			func() *flowsv1.SequenceList { return &flowsv1.SequenceList{} },
		),
	}
}
