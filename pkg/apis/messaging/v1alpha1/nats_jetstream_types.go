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

package v1aplha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/kmeta"
)

// +genclient
// +genreconciler
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NatsJetStreamChannel is a resource representing a NATS JetStream Channel.
type NatsJetStreamChannel struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of the Channel.
	Spec NatsJetStreamChannelSpec `json:"spec,omitempty"`

	// Status represents the current state of the NatssChannel. This data may be out of
	// date.
	// +optional
	Status NatsJetStreamChannelStatus `json:"status,omitempty"`
}

// Check that Channel can be validated, can be defaulted, and has immutable fields.
var (
	_ apis.Validatable = (*NatsJetStreamChannelSpec)(nil)
	_ apis.Defaultable = (*NatsJetStreamChannelSpec)(nil)
	// Check that InMemoryChannel can return its spec untyped.
	_ apis.HasSpec = (*NatsJetStreamChannel)(nil)
	// Check that we can create OwnerReferences to an InMemoryChannel.
	_ kmeta.OwnerRefable = (*NatsJetStreamChannel)(nil)
	_ runtime.Object     = (*NatsJetStreamChannel)(nil)
	_ duckv1.KRShaped    = (*NatsJetStreamChannel)(nil)
)

// NatsJetStreamChannelSpec defines the specification for a NatssChannel.
type NatsJetStreamChannelSpec struct {
	// inherits duck/v1 ChannelableSpec, which currently provides:
	// * SubscribableSpec - List of subscribers
	// * DeliverySpec - contains options controlling the event delivery
	eventingduckv1.ChannelableSpec `json:",inline"`
}

// NatsJetStreamChannelStatus represents the current state of a NatssChannel.
type NatsJetStreamChannelStatus struct {
	// inherits duck/v1 ChannelableStatus, which currently provides:
	// * ObservedGeneration - the 'Generation' of the Service that was last processed by the controller.
	// * Conditions - the latest available observations of a resource's current state.
	// * AddressStatus is the part where the Channelable fulfills the Addressable contract.
	// * Subscribers is populated with the statuses of each of the Channelable's subscribers.
	// * DeadLetterChannel is a KReference and is set by the channel when it supports native error handling via a channel
	//   Failed messages are delivered here.
	eventingduckv1.ChannelableStatus `json:",inline"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NatsJetStreamChannelList is a collection of NatssChannels.
type NatsJetStreamChannelList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NatsJetStreamChannel `json:"items"`
}

// GetGroupVersionKind returns GroupVersionKind for NatssChannels
func (*NatsJetStreamChannel) GetGroupVersionKind() schema.GroupVersionKind {
	return SchemeGroupVersion.WithKind("NatssChannel")
}

// GetStatus retrieves the duck status for this resource. Implements the KRShaped interface.
func (n *NatsJetStreamChannel) GetStatus() *duckv1.Status {
	return &n.Status.Status
}
