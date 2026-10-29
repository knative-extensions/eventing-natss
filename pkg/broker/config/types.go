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

package config

import (
	eventingv1alpha1 "knative.dev/eventing-natss/pkg/apis/eventing/v1alpha1"
)

// NatsJetStreamBrokerDefaults contains the default configuration for NatsJetStreamBroker
// at different scopes (cluster, namespace).
type NatsJetStreamBrokerDefaults struct {
	// ClusterDefault is the default configuration for all namespaces
	// that don't have a specific configuration.
	// +optional
	ClusterDefault *eventingv1alpha1.NatsJetStreamBrokerConfigSpec `json:"clusterDefault,omitempty"`

	// NamespaceDefaults contains namespace-specific configurations.
	// The key is the namespace name.
	// +optional
	NamespaceDefaults map[string]*eventingv1alpha1.NatsJetStreamBrokerConfigSpec `json:"namespaceDefaults,omitempty"`
}
