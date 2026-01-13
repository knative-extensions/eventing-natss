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
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/nats-io/nats.go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
)

func TestDefaultBrokerConfig(t *testing.T) {
	config := DefaultBrokerConfig()

	if config == nil {
		t.Fatal("DefaultBrokerConfig() returned nil")
	}

	if config.Stream == nil {
		t.Fatal("Stream config should not be nil")
	}

	if config.Stream.Retention != v1alpha1.LimitsRetentionPolicy {
		t.Errorf("Stream.Retention = %v, want %v", config.Stream.Retention, v1alpha1.LimitsRetentionPolicy)
	}

	if config.Stream.Storage != v1alpha1.FileStorage {
		t.Errorf("Stream.Storage = %v, want %v", config.Stream.Storage, v1alpha1.FileStorage)
	}

	if config.Stream.Replicas != 1 {
		t.Errorf("Stream.Replicas = %v, want %v", config.Stream.Replicas, 1)
	}

	if config.Stream.Discard != v1alpha1.OldDiscardPolicy {
		t.Errorf("Stream.Discard = %v, want %v", config.Stream.Discard, v1alpha1.OldDiscardPolicy)
	}

	if config.Consumer == nil {
		t.Fatal("Consumer config should not be nil")
	}

	if config.Consumer.DeliverPolicy != v1alpha1.NewDeliverPolicy {
		t.Errorf("Consumer.DeliverPolicy = %v, want %v", config.Consumer.DeliverPolicy, v1alpha1.NewDeliverPolicy)
	}

	if config.Consumer.ReplayPolicy != v1alpha1.InstantReplayPolicy {
		t.Errorf("Consumer.ReplayPolicy = %v, want %v", config.Consumer.ReplayPolicy, v1alpha1.InstantReplayPolicy)
	}

	if config.Consumer.ConsumerType != v1alpha1.PullConsumerType {
		t.Errorf("Consumer.ConsumerType = %v, want %v", config.Consumer.ConsumerType, v1alpha1.PullConsumerType)
	}
}

func TestLoadDefaultsFromConfigMap(t *testing.T) {
	tests := []struct {
		name    string
		cm      *corev1.ConfigMap
		want    *NatsJetStreamBrokerDefaults
		wantErr bool
	}{
		{
			name: "nil configmap",
			cm:   nil,
			want: nil,
		},
		{
			name: "configmap without config key",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ConfigMapName,
					Namespace: "knative-eventing",
				},
				Data: map[string]string{
					"other-key": "value",
				},
			},
			want: nil,
		},
		{
			name: "configmap with empty config",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ConfigMapName,
					Namespace: "knative-eventing",
				},
				Data: map[string]string{
					ConfigKey: "",
				},
			},
			want: nil,
		},
		{
			name: "configmap with valid YAML config",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ConfigMapName,
					Namespace: "knative-eventing",
				},
				Data: map[string]string{
					ConfigKey: `
clusterDefault:
  stream:
    replicas: 3
`,
				},
			},
			want: &NatsJetStreamBrokerDefaults{
				ClusterDefault: &NatsJetStreamBrokerConfig{
					Stream: &v1alpha1.StreamConfig{
						Replicas: 3,
					},
				},
			},
		},
		{
			name: "configmap with namespace defaults",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ConfigMapName,
					Namespace: "knative-eventing",
				},
				Data: map[string]string{
					ConfigKey: `
namespaceDefaults:
  production:
    stream:
      replicas: 5
`,
				},
			},
			want: &NatsJetStreamBrokerDefaults{
				NamespaceDefaults: map[string]*NatsJetStreamBrokerConfig{
					"production": {
						Stream: &v1alpha1.StreamConfig{
							Replicas: 5,
						},
					},
				},
			},
		},
		{
			name: "configmap with invalid YAML",
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ConfigMapName,
					Namespace: "knative-eventing",
				},
				Data: map[string]string{
					ConfigKey: `invalid: [yaml`,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadDefaultsFromConfigMap(tt.cm)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadDefaultsFromConfigMap() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("LoadDefaultsFromConfigMap() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetConfigFromAnnotation(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		want        *NatsJetStreamBrokerConfig
		wantErr     bool
	}{
		{
			name:        "nil annotations",
			annotations: nil,
			want:        nil,
		},
		{
			name:        "empty annotations",
			annotations: map[string]string{},
			want:        nil,
		},
		{
			name: "no broker config annotation",
			annotations: map[string]string{
				"some-other-annotation": "value",
			},
			want: nil,
		},
		{
			name: "empty broker config annotation",
			annotations: map[string]string{
				BrokerConfigAnnotation: "",
			},
			want: nil,
		},
		{
			name: "valid broker config annotation",
			annotations: map[string]string{
				BrokerConfigAnnotation: `{"stream":{"replicas":3}}`,
			},
			want: &NatsJetStreamBrokerConfig{
				Stream: &v1alpha1.StreamConfig{
					Replicas: 3,
				},
			},
		},
		{
			name: "invalid JSON in annotation",
			annotations: map[string]string{
				BrokerConfigAnnotation: `{invalid json}`,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetConfigFromAnnotation(tt.annotations)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetConfigFromAnnotation() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("GetConfigFromAnnotation() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetConfigFromConfigMap(t *testing.T) {
	tests := []struct {
		name      string
		cm        *corev1.ConfigMap
		namespace string
		want      *NatsJetStreamBrokerConfig
		wantErr   bool
	}{
		{
			name:      "nil configmap returns defaults",
			cm:        nil,
			namespace: "default",
			want:      DefaultBrokerConfig(),
		},
		{
			name: "namespace specific config",
			cm: &corev1.ConfigMap{
				Data: map[string]string{
					ConfigKey: `
clusterDefault:
  stream:
    replicas: 1
namespaceDefaults:
  production:
    stream:
      replicas: 5
`,
				},
			},
			namespace: "production",
			want: &NatsJetStreamBrokerConfig{
				Stream: &v1alpha1.StreamConfig{
					Replicas: 5,
				},
			},
		},
		{
			name: "cluster default when no namespace config",
			cm: &corev1.ConfigMap{
				Data: map[string]string{
					ConfigKey: `
clusterDefault:
  stream:
    replicas: 3
namespaceDefaults:
  production:
    stream:
      replicas: 5
`,
				},
			},
			namespace: "staging",
			want: &NatsJetStreamBrokerConfig{
				Stream: &v1alpha1.StreamConfig{
					Replicas: 3,
				},
			},
		},
		{
			name: "hardcoded defaults when no config specified",
			cm: &corev1.ConfigMap{
				Data: map[string]string{
					ConfigKey: `{}`,
				},
			},
			namespace: "default",
			want:      DefaultBrokerConfig(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetConfigFromConfigMap(tt.cm, tt.namespace)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetConfigFromConfigMap() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("GetConfigFromConfigMap() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildNatsStreamConfig(t *testing.T) {
	tests := []struct {
		name           string
		streamName     string
		publishSubject string
		config         *NatsJetStreamBrokerConfig
		wantName       string
		wantSubjects   []string
		wantRetention  nats.RetentionPolicy
		wantStorage    nats.StorageType
		wantReplicas   int
	}{
		{
			name:           "nil config uses defaults",
			streamName:     "TEST_STREAM",
			publishSubject: "test.subject",
			config:         nil,
			wantName:       "TEST_STREAM",
			wantSubjects:   []string{"test.subject.>"},
			wantRetention:  nats.InterestPolicy,
			wantStorage:    nats.FileStorage,
			wantReplicas:   1,
		},
		{
			name:           "nil stream config uses defaults",
			streamName:     "TEST_STREAM",
			publishSubject: "test.subject",
			config:         &NatsJetStreamBrokerConfig{},
			wantName:       "TEST_STREAM",
			wantSubjects:   []string{"test.subject.>"},
			wantRetention:  nats.InterestPolicy,
			wantStorage:    nats.FileStorage,
			wantReplicas:   1,
		},
		{
			name:           "custom stream config",
			streamName:     "CUSTOM_STREAM",
			publishSubject: "custom.subject",
			config: &NatsJetStreamBrokerConfig{
				Stream: &v1alpha1.StreamConfig{
					Replicas:  3,
					Storage:   v1alpha1.MemoryStorage,
					Retention: v1alpha1.WorkRetentionPolicy,
				},
			},
			wantName:      "CUSTOM_STREAM",
			wantSubjects:  []string{"custom.subject.>"},
			wantRetention: nats.WorkQueuePolicy,
			wantStorage:   nats.MemoryStorage,
			wantReplicas:  3,
		},
		{
			name:           "additional subjects",
			streamName:     "TEST_STREAM",
			publishSubject: "test.subject",
			config: &NatsJetStreamBrokerConfig{
				Stream: &v1alpha1.StreamConfig{
					AdditionalSubjects: []string{"extra.subject.>"},
				},
			},
			wantName:      "TEST_STREAM",
			wantSubjects:  []string{"test.subject.>", "extra.subject.>"},
			wantRetention: nats.InterestPolicy,
			wantStorage:   nats.FileStorage,
			wantReplicas:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildNatsStreamConfig(tt.streamName, tt.publishSubject, tt.config)

			if got.Name != tt.wantName {
				t.Errorf("Name = %v, want %v", got.Name, tt.wantName)
			}

			if diff := cmp.Diff(tt.wantSubjects, got.Subjects); diff != "" {
				t.Errorf("Subjects mismatch (-want +got):\n%s", diff)
			}

			if got.Retention != tt.wantRetention {
				t.Errorf("Retention = %v, want %v", got.Retention, tt.wantRetention)
			}

			if got.Storage != tt.wantStorage {
				t.Errorf("Storage = %v, want %v", got.Storage, tt.wantStorage)
			}

			if got.Replicas != tt.wantReplicas {
				t.Errorf("Replicas = %v, want %v", got.Replicas, tt.wantReplicas)
			}
		})
	}
}

func TestBrokerConfigAnnotationRoundTrip(t *testing.T) {
	// Test that we can serialize and deserialize broker config through annotation
	original := &NatsJetStreamBrokerConfig{
		Stream: &v1alpha1.StreamConfig{
			Replicas: 3,
			Storage:  v1alpha1.MemoryStorage,
		},
		Consumer: &v1alpha1.ConsumerConfigTemplate{
			DeliverPolicy: v1alpha1.AllDeliverPolicy,
		},
	}

	// Serialize to JSON
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	// Create annotation map
	annotations := map[string]string{
		BrokerConfigAnnotation: string(data),
	}

	// Deserialize through GetConfigFromAnnotation
	got, err := GetConfigFromAnnotation(annotations)
	if err != nil {
		t.Fatalf("GetConfigFromAnnotation() error = %v", err)
	}

	if diff := cmp.Diff(original, got); diff != "" {
		t.Errorf("Round-trip mismatch (-want +got):\n%s", diff)
	}
}
