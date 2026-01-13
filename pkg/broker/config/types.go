/*
Copyright 2024 The Knative Authors

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

package config

import (
	corev1 "k8s.io/api/core/v1"

	"knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
)

// NatsJetStreamBrokerConfig contains configuration for NatsJetStreamBroker.
// This can be specified at cluster level, namespace level, or per-broker level.
type NatsJetStreamBrokerConfig struct {
	// Stream defines the StreamConfig for the broker's JetStream stream.
	// +optional
	Stream *v1alpha1.StreamConfig `json:"stream,omitempty"`

	// Consumer defines the default ConsumerConfigTemplate for triggers.
	// Individual triggers can override these settings.
	// +optional
	Consumer *v1alpha1.ConsumerConfigTemplate `json:"consumer,omitempty"`

	// Ingress defines the deployment template for the broker ingress.
	// +optional
	Ingress *DeploymentTemplate `json:"ingress,omitempty"`

	// Filter defines the deployment template for the broker filter.
	// +optional
	Filter *DeploymentTemplate `json:"filter,omitempty"`
}

// DeploymentTemplate defines customization options for broker deployments.
type DeploymentTemplate struct {
	// Replicas is the number of replicas for the deployment.
	// +optional
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

	// Env defines additional environment variables for the container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// NatsJetStreamBrokerDefaults contains the default configuration for NatsJetStreamBroker
// at different scopes (cluster, namespace).
type NatsJetStreamBrokerDefaults struct {
	// ClusterDefault is the default configuration for all namespaces
	// that don't have a specific configuration.
	// +optional
	ClusterDefault *NatsJetStreamBrokerConfig `json:"clusterDefault,omitempty"`

	// NamespaceDefaults contains namespace-specific configurations.
	// The key is the namespace name.
	// +optional
	NamespaceDefaults map[string]*NatsJetStreamBrokerConfig `json:"namespaceDefaults,omitempty"`
}

// DeepCopyInto is a deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NatsJetStreamBrokerConfig) DeepCopyInto(out *NatsJetStreamBrokerConfig) {
	*out = *in
	if in.Stream != nil {
		in, out := &in.Stream, &out.Stream
		*out = new(v1alpha1.StreamConfig)
		(*in).DeepCopyInto(*out)
	}
	if in.Consumer != nil {
		in, out := &in.Consumer, &out.Consumer
		*out = new(v1alpha1.ConsumerConfigTemplate)
		(*in).DeepCopyInto(*out)
	}
	if in.Ingress != nil {
		in, out := &in.Ingress, &out.Ingress
		*out = new(DeploymentTemplate)
		(*in).DeepCopyInto(*out)
	}
	if in.Filter != nil {
		in, out := &in.Filter, &out.Filter
		*out = new(DeploymentTemplate)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is a deepcopy function, copying the receiver, creating a new NatsJetStreamBrokerConfig.
func (in *NatsJetStreamBrokerConfig) DeepCopy() *NatsJetStreamBrokerConfig {
	if in == nil {
		return nil
	}
	out := new(NatsJetStreamBrokerConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is a deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DeploymentTemplate) DeepCopyInto(out *DeploymentTemplate) {
	*out = *in
	if in.Replicas != nil {
		in, out := &in.Replicas, &out.Replicas
		*out = new(int32)
		**out = **in
	}
	if in.Annotations != nil {
		in, out := &in.Annotations, &out.Annotations
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Labels != nil {
		in, out := &in.Labels, &out.Labels
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.PodAnnotations != nil {
		in, out := &in.PodAnnotations, &out.PodAnnotations
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.PodLabels != nil {
		in, out := &in.PodLabels, &out.PodLabels
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.NodeSelector != nil {
		in, out := &in.NodeSelector, &out.NodeSelector
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	in.Resources.DeepCopyInto(&out.Resources)
	if in.Affinity != nil {
		in, out := &in.Affinity, &out.Affinity
		*out = new(corev1.Affinity)
		(*in).DeepCopyInto(*out)
	}
	if in.Env != nil {
		in, out := &in.Env, &out.Env
		*out = make([]corev1.EnvVar, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is a deepcopy function, copying the receiver, creating a new DeploymentTemplate.
func (in *DeploymentTemplate) DeepCopy() *DeploymentTemplate {
	if in == nil {
		return nil
	}
	out := new(DeploymentTemplate)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is a deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NatsJetStreamBrokerDefaults) DeepCopyInto(out *NatsJetStreamBrokerDefaults) {
	*out = *in
	if in.ClusterDefault != nil {
		in, out := &in.ClusterDefault, &out.ClusterDefault
		*out = new(NatsJetStreamBrokerConfig)
		(*in).DeepCopyInto(*out)
	}
	if in.NamespaceDefaults != nil {
		in, out := &in.NamespaceDefaults, &out.NamespaceDefaults
		*out = make(map[string]*NatsJetStreamBrokerConfig, len(*in))
		for key, val := range *in {
			var outVal *NatsJetStreamBrokerConfig
			if val == nil {
				(*out)[key] = nil
			} else {
				inVal := (*in)[key]
				in, out := &inVal, &outVal
				*out = new(NatsJetStreamBrokerConfig)
				(*in).DeepCopyInto(*out)
			}
			(*out)[key] = outVal
		}
	}
}

// DeepCopy is a deepcopy function, copying the receiver, creating a new NatsJetStreamBrokerDefaults.
func (in *NatsJetStreamBrokerDefaults) DeepCopy() *NatsJetStreamBrokerDefaults {
	if in == nil {
		return nil
	}
	out := new(NatsJetStreamBrokerDefaults)
	in.DeepCopyInto(out)
	return out
}
