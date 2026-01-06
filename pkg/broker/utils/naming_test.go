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

package utils

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
)

func TestBrokerStreamName(t *testing.T) {
	tests := []struct {
		name      string
		broker    *eventingv1.Broker
		wantName  string
	}{
		{
			name: "simple names",
			broker: &eventingv1.Broker{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "mybroker",
				},
			},
			wantName: "KN_BROKER_DEFAULT__MYBROKER",
		},
		{
			name: "names with hyphens",
			broker: &eventingv1.Broker{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "knative-eventing",
					Name:      "test-broker",
				},
			},
			wantName: "KN_BROKER_KNATIVE_EVENTING__TEST_BROKER",
		},
		{
			name: "lowercase conversion",
			broker: &eventingv1.Broker{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "MyNamespace",
					Name:      "MyBroker",
				},
			},
			wantName: "KN_BROKER_MYNAMESPACE__MYBROKER",
		},
		{
			name: "multiple hyphens",
			broker: &eventingv1.Broker{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "my-test-namespace",
					Name:      "my-test-broker",
				},
			},
			wantName: "KN_BROKER_MY_TEST_NAMESPACE__MY_TEST_BROKER",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BrokerStreamName(tt.broker)
			if got != tt.wantName {
				t.Errorf("BrokerStreamName() = %v, want %v", got, tt.wantName)
			}
		})
	}
}

func TestBrokerPublishSubjectName(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		brokerName string
		want      string
	}{
		{
			name:       "simple names",
			namespace:  "default",
			brokerName: "mybroker",
			want:       "default.mybroker._knative_broker",
		},
		{
			name:       "names with hyphens",
			namespace:  "knative-eventing",
			brokerName: "test-broker",
			want:       "knative-eventing.test-broker._knative_broker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BrokerPublishSubjectName(tt.namespace, tt.brokerName)
			if got != tt.want {
				t.Errorf("BrokerPublishSubjectName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTriggerConsumerName(t *testing.T) {
	tests := []struct {
		name       string
		triggerUID string
		want       string
	}{
		{
			name:       "standard UUID",
			triggerUID: "12345678-1234-1234-1234-123456789abc",
			want:       "KN_TRIGGER_12345678123412341234123456789ABC",
		},
		{
			name:       "lowercase UUID",
			triggerUID: "abcdef12-3456-7890-abcd-ef1234567890",
			want:       "KN_TRIGGER_ABCDEF1234567890ABCDEF1234567890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TriggerConsumerName(tt.triggerUID)
			if got != tt.want {
				t.Errorf("TriggerConsumerName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTriggerConsumerSubjectName(t *testing.T) {
	tests := []struct {
		name       string
		namespace  string
		brokerName string
		triggerUID string
		want       string
	}{
		{
			name:       "standard names",
			namespace:  "default",
			brokerName: "mybroker",
			triggerUID: "12345678-1234-1234-1234-123456789abc",
			want:       "default.mybroker._knative_trigger.12345678123412341234123456789abc",
		},
		{
			name:       "names with hyphens",
			namespace:  "test-ns",
			brokerName: "test-broker",
			triggerUID: "abcdef12-3456-7890-abcd-ef1234567890",
			want:       "test-ns.test-broker._knative_trigger.abcdef1234567890abcdef1234567890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TriggerConsumerSubjectName(tt.namespace, tt.brokerName, tt.triggerUID)
			if got != tt.want {
				t.Errorf("TriggerConsumerSubjectName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeadLetterStreamName(t *testing.T) {
	tests := []struct {
		name     string
		broker   *eventingv1.Broker
		wantName string
	}{
		{
			name: "simple names",
			broker: &eventingv1.Broker{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "mybroker",
				},
			},
			wantName: "KN_BROKER_DLQ_DEFAULT__MYBROKER",
		},
		{
			name: "names with hyphens",
			broker: &eventingv1.Broker{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "knative-eventing",
					Name:      "test-broker",
				},
			},
			wantName: "KN_BROKER_DLQ_KNATIVE_EVENTING__TEST_BROKER",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeadLetterStreamName(tt.broker)
			if got != tt.wantName {
				t.Errorf("DeadLetterStreamName() = %v, want %v", got, tt.wantName)
			}
		})
	}
}

func TestDeadLetterSubjectName(t *testing.T) {
	tests := []struct {
		name       string
		namespace  string
		brokerName string
		want       string
	}{
		{
			name:       "simple names",
			namespace:  "default",
			brokerName: "mybroker",
			want:       "default.mybroker._knative_broker_dlq",
		},
		{
			name:       "names with hyphens",
			namespace:  "knative-eventing",
			brokerName: "test-broker",
			want:       "knative-eventing.test-broker._knative_broker_dlq",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeadLetterSubjectName(tt.namespace, tt.brokerName)
			if got != tt.want {
				t.Errorf("DeadLetterSubjectName() = %v, want %v", got, tt.want)
			}
		})
	}
}
