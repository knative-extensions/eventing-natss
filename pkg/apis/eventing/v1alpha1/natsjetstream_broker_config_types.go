/*
Copyright 2026 The Knative Authors

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

	messagingv1alpha1 "knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=natsjetstreambrokerconfigs,shortName=njsbc,categories=all;knative;eventing

// NatsJetStreamBrokerConfig configures a NatsJetStreamBroker. A Broker selects
// it through spec.config.
type NatsJetStreamBrokerConfig struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired configuration of the broker.
	Spec NatsJetStreamBrokerConfigSpec `json:"spec"`
}

// +kubebuilder:validation:XValidation:rule="!has(self.stream) || !has(self.stream.mirror)",message="stream.mirror is not supported: a mirror stream cannot have the subjects the broker publishes to"
// +kubebuilder:validation:XValidation:rule="!has(self.stream) || !has(self.stream.noAck) || !self.stream.noAck",message="stream.noAck is not supported: the broker ingress waits for publish acknowledgements"
// +kubebuilder:validation:XValidation:rule="!has(self.stream) || !has(self.stream.retention) || self.stream.retention != 'Work'",message="stream.retention Work is not supported: every trigger consumes the same subject, which a work-queue stream allows for only one consumer"

// NatsJetStreamBrokerConfigSpec is the configuration of a NatsJetStreamBroker.
// The same shape is used by the cluster/namespace defaults ConfigMap.
type NatsJetStreamBrokerConfigSpec struct {
	// Stream defines the StreamConfig for the broker's JetStream stream.
	// +optional
	Stream *messagingv1alpha1.StreamConfig `json:"stream,omitempty"`

	// Filter defines the deployment template for the broker filter.
	// +optional
	Filter *DeploymentTemplate `json:"filter,omitempty"`
}

// DeploymentTemplate defines customization options for broker deployments.
type DeploymentTemplate struct {
	// Replicas is the number of replicas for the deployment.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2147483647
	Replicas *int32 `json:"replicas,omitempty"`

	// Annotations to add to the deployment.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Labels to add to the deployment.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// PodAnnotations to add to the pod template.
	// +optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// PodLabels to add to the pod template.
	// +optional
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// NodeSelector for pod scheduling.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Resources for the container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Affinity for pod scheduling.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// TopologySpreadConstraints controls how pods are spread across topology domains.
	// +optional
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`

	// Env defines additional environment variables for the container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NatsJetStreamBrokerConfigList is a list of NatsJetStreamBrokerConfig resources.
type NatsJetStreamBrokerConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []NatsJetStreamBrokerConfig `json:"items"`
}
