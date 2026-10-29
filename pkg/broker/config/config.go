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
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	eventingv1alpha1 "knative.dev/eventing-natss/pkg/apis/eventing/v1alpha1"
	"knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	"knative.dev/eventing-natss/pkg/channel/jetstream/utils"
)

const (
	// ConfigMapName is the name of the ConfigMap containing broker defaults
	ConfigMapName = "natsjetstream-broker-config"

	// ConfigKey is the key in the ConfigMap containing the YAML configuration
	ConfigKey = "config"
)

// LegacyConfigAnnotation carries deprecated per-broker configuration as inline JSON.
// Existing brokers can continue using it; new brokers should use spec.config.
const LegacyConfigAnnotation = "natsjetstream.eventing.knative.dev/config"

// DefaultBrokerConfig returns the default configuration for a broker
func DefaultBrokerConfig() *eventingv1alpha1.NatsJetStreamBrokerConfigSpec {
	return &eventingv1alpha1.NatsJetStreamBrokerConfigSpec{
		Stream: &v1alpha1.StreamConfig{
			Retention: v1alpha1.LimitsRetentionPolicy,
			Storage:   v1alpha1.FileStorage,
			Replicas:  1,
			Discard:   v1alpha1.OldDiscardPolicy,
		},
	}
}

// LoadDefaultsFromConfigMap loads NatsJetStreamBrokerDefaults from a ConfigMap
func LoadDefaultsFromConfigMap(cm *corev1.ConfigMap) (*NatsJetStreamBrokerDefaults, error) {
	if cm == nil {
		return nil, nil
	}

	data, ok := cm.Data[ConfigKey]
	if !ok || data == "" {
		return nil, nil
	}

	defaults := &NatsJetStreamBrokerDefaults{}
	jsonData, err := yaml.YAMLToJSON([]byte(data))
	if err != nil {
		return nil, fmt.Errorf("failed to convert YAML to JSON: %w", err)
	}

	if err := json.Unmarshal(jsonData, defaults); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return defaults, nil
}

// GetConfigFromAnnotation reads the deprecated inline JSON configuration.
// It returns nil if the annotation is absent or empty.
func GetConfigFromAnnotation(annotations map[string]string) (*eventingv1alpha1.NatsJetStreamBrokerConfigSpec, error) {
	configJSON := annotations[LegacyConfigAnnotation]
	if configJSON == "" {
		return nil, nil
	}
	cfg := &eventingv1alpha1.NatsJetStreamBrokerConfigSpec{}
	if err := json.Unmarshal([]byte(configJSON), cfg); err != nil {
		return nil, fmt.Errorf("failed to parse broker config annotation %q: %w", LegacyConfigAnnotation, err)
	}
	return cfg, nil
}

// GetConfigFromConfigMap returns the configuration for a namespace from a ConfigMap.
// It checks namespace-specific config first, then cluster default, then hardcoded defaults.
func GetConfigFromConfigMap(cm *corev1.ConfigMap, namespace string) (*eventingv1alpha1.NatsJetStreamBrokerConfigSpec, error) {
	defaults, err := LoadDefaultsFromConfigMap(cm)
	if err != nil {
		return nil, err
	}

	if defaults == nil {
		return DefaultBrokerConfig(), nil
	}

	// Check for namespace-specific config
	if nsConfig, ok := defaults.NamespaceDefaults[namespace]; ok && nsConfig != nil {
		return nsConfig, nil
	}

	// Check for cluster default config
	if defaults.ClusterDefault != nil {
		return defaults.ClusterDefault, nil
	}

	// Return hardcoded defaults
	return DefaultBrokerConfig(), nil
}

// BuildNatsStreamConfig converts a NatsJetStreamBrokerConfigSpec to a nats.StreamConfig
func BuildNatsStreamConfig(streamName, publishSubject string, config *eventingv1alpha1.NatsJetStreamBrokerConfigSpec) *nats.StreamConfig {
	streamConfig := &nats.StreamConfig{
		Name:     streamName,
		Subjects: []string{publishSubject + ".>"},
	}

	if config == nil || config.Stream == nil {
		// Use defaults
		streamConfig.Retention = nats.LimitsPolicy
		streamConfig.Storage = nats.FileStorage
		streamConfig.Replicas = 1
		streamConfig.Discard = nats.DiscardOld
		return streamConfig
	}

	sc := config.Stream

	// Add additional subjects if specified
	if len(sc.AdditionalSubjects) > 0 {
		streamConfig.Subjects = append(streamConfig.Subjects, sc.AdditionalSubjects...)
	}

	// Default to Limits (not Interest) so a stream without a consumer still
	// retains messages — e.g. a broker whose triggers/filter are not yet created.
	streamConfig.Retention = utils.ConvertRetentionPolicy(sc.Retention, nats.LimitsPolicy)
	streamConfig.MaxConsumers = sc.MaxConsumers
	streamConfig.MaxMsgs = sc.MaxMsgs
	streamConfig.MaxBytes = sc.MaxBytes
	streamConfig.Discard = utils.ConvertDiscardPolicy(sc.Discard, nats.DiscardOld)
	streamConfig.MaxAge = sc.MaxAge.Duration
	streamConfig.MaxMsgSize = sc.MaxMsgSize
	streamConfig.Storage = utils.ConvertStorage(sc.Storage, nats.FileStorage)
	streamConfig.Replicas = sc.Replicas
	streamConfig.NoAck = sc.NoAck
	streamConfig.Duplicates = sc.DuplicateWindow.Duration
	streamConfig.Placement = utils.ConvertPlacement(sc.Placement)
	streamConfig.Mirror = utils.ConvertStreamSource(sc.Mirror)
	streamConfig.Sources = utils.ConvertStreamSources(sc.Sources)

	return streamConfig
}
