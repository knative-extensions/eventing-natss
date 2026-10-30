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
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	messagingv1alpha1 "knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
)

// The manifests below are what users write, so they must decode into the Go
// types without dropping or rejecting any field.
func TestNatsJetStreamBrokerConfigDecode(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		want     NatsJetStreamBrokerConfigSpec
		wantErr  bool
	}{
		{
			name: "stream and filter env",
			manifest: `
spec:
  filter:
    replicas: 2
    env:
      - name: CONSUMER_FETCH_BATCH_SIZE
        value: "50"
      - name: CONSUMER_FETCH_TIMEOUT
        value: "1s"
  stream:
    replicas: 3
`,
			want: NatsJetStreamBrokerConfigSpec{
				Stream: &messagingv1alpha1.StreamConfig{Replicas: 3},
				Filter: &DeploymentTemplate{
					Replicas: ptr.To[int32](2),
					Env: []corev1.EnvVar{
						{Name: "CONSUMER_FETCH_BATCH_SIZE", Value: "50"},
						{Name: "CONSUMER_FETCH_TIMEOUT", Value: "1s"},
					},
				},
			},
		},
		{
			name: "stream",
			manifest: `
spec:
  stream:
    replicas: 5
    storage: Memory
`,
			want: NatsJetStreamBrokerConfigSpec{
				Stream: &messagingv1alpha1.StreamConfig{Replicas: 5, Storage: messagingv1alpha1.MemoryStorage},
			},
		},
		{
			name: "filter topology spread constraints",
			manifest: `
spec:
  filter:
    topologySpreadConstraints:
      - maxSkew: 1
        minDomains: 2
        topologyKey: kubernetes.io/hostname
        whenUnsatisfiable: DoNotSchedule
        labelSelector:
          matchLabels:
            eventing.knative.dev/broker: example-broker
            eventing.knative.dev/role: filter
        matchLabelKeys:
          - pod-template-hash
`,
			want: NatsJetStreamBrokerConfigSpec{
				Filter: &DeploymentTemplate{
					TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{
						MaxSkew:           1,
						MinDomains:        ptr.To[int32](2),
						TopologyKey:       corev1.LabelHostname,
						WhenUnsatisfiable: corev1.DoNotSchedule,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"eventing.knative.dev/broker": "example-broker",
								"eventing.knative.dev/role":   "filter",
							},
						},
						MatchLabelKeys: []string{"pod-template-hash"},
					}},
				},
			},
		},
		{
			name: "unknown field",
			manifest: `
spec:
  stream:
    replica: 3
`,
			wantErr: true,
		},
		{
			name: "type mismatch",
			manifest: `
spec:
  stream: not-an-object
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := &NatsJetStreamBrokerConfig{}
			err := yaml.UnmarshalStrict([]byte(tt.manifest), got)
			if (err != nil) != tt.wantErr {
				t.Fatalf("UnmarshalStrict() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got.Spec); diff != "" {
				t.Errorf("Spec mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
