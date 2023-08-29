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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/kmeta"
)

// RetentionPolicy defines how messages in a stream should be retained
type RetentionPolicy string

const (
	// LimitsRetentionPolicy keeps messages in a stream until that stream reaches its limits
	LimitsRetentionPolicy RetentionPolicy = "Limits"

	// InterestRetentionPolicy keeps messages in a stream whilst there are consumers bound to the stream
	InterestRetentionPolicy RetentionPolicy = "Interest"

	// WorkRetentionPolicy keeps messages in a stream until they are consumed by a single observer after which point
	// they are removed
	WorkRetentionPolicy RetentionPolicy = "Work"
)

// DiscardPolicy sets how messages are discarded when the limits configured for a stream are reached.
type DiscardPolicy string

const (
	// OldDiscardPolicy will remove old messages from a stream when limits are hit, making room for new messages.
	OldDiscardPolicy DiscardPolicy = "Old"

	// NewDiscardPolicy will reject new messages until the stream no longer hits its limits.
	NewDiscardPolicy DiscardPolicy = "New"
)

// Storage sets how messages should be stored in a stream
type Storage string

const (
	// FileStorage will store messages in a stream on disk.
	FileStorage Storage = "File"

	// MemoryStorage will store messages in a stream within memory. Messages will be lost on a server restart.
	MemoryStorage Storage = "Memory"
)

// DeliverPolicy defines where in the stream a consumer should start delivering messages
type DeliverPolicy string

const (
	// AllDeliverPolicy will deliver all messages available messages.
	AllDeliverPolicy DeliverPolicy = "All"

	// LastDeliverPolicy will deliver the last message added to the stream and subsequent messages thereafter.
	LastDeliverPolicy DeliverPolicy = "Last"

	// NewDeliverPolicy will deliver all future messages sent to the stream after the consumer is considered ready.
	NewDeliverPolicy DeliverPolicy = "New"

	// ByStartSequenceDeliverPolicy will deliver messages starting at the sequence specified
	// in ConsumerConfigTemplate.OptStartSeq.
	ByStartSequenceDeliverPolicy DeliverPolicy = "ByStartSequence"

	// ByStartTimeDeliverPolicy will deliver messages starting after the timestamp specified
	// in ConsumerConfigTemplate.OptStartTime
	ByStartTimeDeliverPolicy DeliverPolicy = "ByStartTime"
)

// ReplayPolicy defines how a consumer should deliver message in relation to time. It is only applicable when
// the DeliverPolicy is set to AllDeliverPolicy, ByStartSequenceDeliverPolicy or ByStartTimeDeliverPolicy.
type ReplayPolicy string

const (
	// InstantReplayPolicy will deliver all messages as quickly as possible whilst adhering to the Ack Policy,
	// Max Ack Pending and the client's ability to consume those messages.
	InstantReplayPolicy ReplayPolicy = "Instant"

	// OriginalReplayPolicy will deliver messages at the same rate at which they were received into the stream.
	OriginalReplayPolicy ReplayPolicy = "Original"
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
	// +optional
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

	// +optional
	Stream Stream `json:"stream,omitempty"`

	// +optional
	ConsumerConfigTemplate *ConsumerConfigTemplate `json:"consumerConfigTemplate,omitempty"`

	// +optional
	DeploymentSpecTemplate *JetStreamDispatcherDeploymentTemplate `json:"deploymentSpecTemplate,omitempty"`
}

// Stream provides customization options to how the eventing-jetstream dispatcher creates streams.
type Stream struct {
	// OverrideName allows defining a custom stream name, by default a generated name based on the namespace and name
	// of the NatsJetStreamChannel is used.
	// +optional
	OverrideName string `json:"overrideName,omitempty"`

	// Config defines the StreamConfig for the stream.
	// +optional
	Config *StreamConfig `json:"config,omitempty"`
}

type StreamConfig struct {
	// AdditionalSubjects allows adding additional subjects which this stream will subscribe to. The stream will always
	// subscribe to a generated subject which the eventing-jetstream controller uses internally.
	// +optional
	AdditionalSubjects []string `json:"additionalSubjects,omitempty"`

	// Retention defines the RetentionPolicy for this stream.
	// +optional
	Retention RetentionPolicy `json:"retention,omitempty"`

	// MaxConsumers defines how many consumers this stream can be bound to it.
	// +optional
	MaxConsumers int `json:"maxConsumers,omitempty"`

	// MaxMsgs defines how many messages this stream can store.
	// +optional
	MaxMsgs int64 `json:"maxMsgs,omitempty"`

	// MaxBytes defines how many bytes this stream can store spanning all messages in the stream.
	// +optional
	MaxBytes int64 `json:"maxBytes,omitempty"`

	// Discard defines the DiscardPolicy for this stream.
	// +optional
	Discard DiscardPolicy `json:"discard,omitempty"`

	// MaxAge defines the maximum age of a message which is allowed in the stream.
	// +optional
	MaxAge metav1.Duration `json:"maxAge,omitempty"`

	// MaxMsgSize defines the maximum size in bytes of an individual message. JetStream includes a hard-limit of 1MB so
	// if defined should be less than 2^20=1048576.
	// +optional
	MaxMsgSize int32 `json:"maxMsgSize,omitempty"`

	// Storage defines the Storage mechanism for this stream.
	// +optional
	Storage Storage `json:"storage,omitempty"`

	// Replicas defines how many replicas of each message should be stored. This is only applicable for clustered
	// JetStream instances.
	// +optional
	Replicas int `json:"replicas,omitempty"`

	// NoAck disables acknowledgement of messages when true.
	// +optional
	NoAck bool `json:"noAck,omitempty"`

	// DuplicateWindow defines the duration of which messages should be tracked for detecting duplicates.
	// +optional
	DuplicateWindow metav1.Duration `json:"duplicateWindow,omitempty"`

	// Placement allows configuring which JetStream server the stream should be placed on.
	// +optional
	Placement *StreamPlacement `json:"placement,omitempty"`

	// Mirror configures the stream to mirror another stream.
	// +optional
	Mirror *StreamSource `json:"mirror,omitempty"`

	// Sources allows aggregating messages from other streams into a new stream.
	// +optional
	Sources []StreamSource `json:"sources,omitempty"`
}

// StreamPlacement is used to guide placement of streams in clustered JetStream.
type StreamPlacement struct {
	// Cluster denotes the cluster name which this stream should be placed on.
	Cluster string `json:"cluster,omitempty"`

	// Tags will restrict this stream to only be stored on servers matching these tags.
	Tags []string `json:"tags,omitempty"`
}

// StreamSource dictates how streams can source from other streams.
type StreamSource struct {
	// Name is the stream name which this source is referencing
	Name string `json:"name,omitempty"`

	// OptStartSeq denotes the message sequence number which this source should start from. This takes precedence
	// over OptStartTime if defined.
	// +optional
	OptStartSeq uint64 `json:"optStartSeq,omitempty"`

	// OptStartTime configures the source to deliver messages from the stream starting at the first message after this
	// timestamp.
	// +optional
	OptStartTime *metav1.Time `json:"optStartTime,omitempty"`

	// FilterSubject configures the source to only include messages matching this subject.
	// +optional
	FilterSubject string `json:"filterSubject,omitempty"`
}

// ConsumerConfigTemplate defines the template for how consumers should be created for each Subscription the channel
// has. Some options aren't available compared to what's configurable in native JetStream since some features must be
// fixed for eventing-jetstream to function in a Knative way.
type ConsumerConfigTemplate struct {
	// DeliverPolicy defines the DeliverPolicy for the consumer.
	// +optional
	DeliverPolicy DeliverPolicy `json:"deliverPolicy,omitempty"`

	// OptStartSeq denotes the message sequence number which this consumer should start from. This is only applicable
	// when DeliverPolicy is set to ByStartSequenceDeliverPolicy.
	// +optional
	OptStartSeq uint64 `json:"optStartSeq,omitempty"`

	// OptStartTime configures the consumer to deliver messages from the stream starting at the first message after this
	// timestamp. This is only applicable when DeliverPolicy is set to ByStartTimeDeliverPolicy.
	// +optional
	OptStartTime *metav1.Time `json:"optStartTime,omitempty"`

	// AckWait denotes the duration for which delivered messages should wait for an acknowledgement before attempting
	// redelivery.
	// +optional
	AckWait metav1.Duration `json:"ackWait,omitempty"`

	// MaxDeliver denotes the maximum number of times a message will be redelivered before being dropped (or delivered
	// to the dead-letter queue if configured).
	// +optional
	MaxDeliver int `json:"maxDeliver,omitempty"`

	// FilterSubject configures the source to only include messages matching this subject.
	// +optional
	FilterSubject string `json:"filterSubject,omitempty"`

	// ReplayPolicy defines the ReplayPolicy for the consumer.
	// +optional
	ReplayPolicy ReplayPolicy `json:"replayPolicy"`

	// RateLimitBPS will throttle delivery to the client in bits-per-second.
	// +optional
	RateLimitBPS uint64 `json:"rateLimitBPS,omitempty"`

	// SampleFrequency sets the percentage of acknowledgements that should be sampled for observability. Valid values
	// are in the range 0-100 and, for example, allows both formats of "30" and "30%".
	// +optional
	SampleFrequency string `json:"sampleFrequency,omitempty"`

	// MaxAckPending is the maximum number of messages without an acknowledgement that can be outstanding, once this
	// limit is reached message delivery will be suspended.
	// +optional
	MaxAckPending int `json:"maxAckPending,omitempty"`
}

type JetStreamDispatcherDeploymentTemplate struct {
	Annotations  map[string]string           `json:"annotations,omitempty"`
	Labels       map[string]string           `json:"labels,omitempty"`
	NodeSelector map[string]string           `json:"nodeSelector,omitempty"`
	Resources    corev1.ResourceRequirements `json:"resources,omitempty"`
	Affinity     *corev1.Affinity            `json:"affinity,omitempty"`
}

// NatsJetStreamChannelStatus represents the current state of a NatssChannel.
type NatsJetStreamChannelStatus struct {
	// inherits duck/v1 ChannelableStatus, which currently provides:
	// * ObservedGeneration - the 'Generation' of the Service that was last processed by the controller.
	// * Conditions - the latest available observations of a resource's current state.
	// * AddressStatus is the part where the Channelable fulfills the Addressable contract.
	// * Subscribers is populated with the statuses of each of the Channelable's subscribers.
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
	return SchemeGroupVersion.WithKind("NatsJetStreamChannel")
}

// GetStatus retrieves the duck status for this resource. Implements the KRShaped interface.
func (c *NatsJetStreamChannel) GetStatus() *duckv1.Status {
	return &c.Status.Status
}
