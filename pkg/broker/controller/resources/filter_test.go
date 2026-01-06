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

package resources

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	"knative.dev/pkg/ptr"

	messagingv1alpha1 "knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
)

func TestMakeFilterDeployment(t *testing.T) {
	broker := &eventingv1.Broker{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-broker",
			Namespace: "test-namespace",
			UID:       "test-uid",
		},
	}

	tests := []struct {
		name          string
		args          *FilterArgs
		wantName      string
		wantNamespace string
		wantReplicas  int32
		wantImage     string
		wantLabels    map[string]string
	}{
		{
			name: "basic deployment",
			args: &FilterArgs{
				Broker:             broker,
				Image:              "gcr.io/test/filter:latest",
				ServiceAccountName: "test-sa",
				StreamName:         "TEST_STREAM",
				NatsURL:            "nats://nats:4222",
			},
			wantName:      "test-broker-broker-filter",
			wantNamespace: "test-namespace",
			wantReplicas:  1,
			wantImage:     "gcr.io/test/filter:latest",
			wantLabels: map[string]string{
				BrokerLabelKey: "test-broker",
				RoleLabelKey:   FilterRoleLabelValue,
			},
		},
		{
			name: "deployment with template",
			args: &FilterArgs{
				Broker:             broker,
				Image:              "gcr.io/test/filter:latest",
				ServiceAccountName: "test-sa",
				StreamName:         "TEST_STREAM",
				NatsURL:            "nats://nats:4222",
				Template: &messagingv1alpha1.DeploymentTemplate{
					Replicas: ptr.Int32(5),
					Labels: map[string]string{
						"custom": "label",
					},
					Annotations: map[string]string{
						"custom": "annotation",
					},
				},
			},
			wantName:      "test-broker-broker-filter",
			wantNamespace: "test-namespace",
			wantReplicas:  5,
			wantImage:     "gcr.io/test/filter:latest",
			wantLabels: map[string]string{
				BrokerLabelKey: "test-broker",
				RoleLabelKey:   FilterRoleLabelValue,
				"custom":       "label",
			},
		},
		{
			name: "deployment with pod labels and annotations",
			args: &FilterArgs{
				Broker:             broker,
				Image:              "gcr.io/test/filter:latest",
				ServiceAccountName: "test-sa",
				StreamName:         "TEST_STREAM",
				NatsURL:            "nats://nats:4222",
				Template: &messagingv1alpha1.DeploymentTemplate{
					PodLabels: map[string]string{
						"pod-label": "value",
					},
					PodAnnotations: map[string]string{
						"pod-annotation": "value",
					},
				},
			},
			wantName:      "test-broker-broker-filter",
			wantNamespace: "test-namespace",
			wantReplicas:  1,
			wantImage:     "gcr.io/test/filter:latest",
			wantLabels: map[string]string{
				BrokerLabelKey: "test-broker",
				RoleLabelKey:   FilterRoleLabelValue,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deployment := MakeFilterDeployment(tt.args)

			if deployment.Name != tt.wantName {
				t.Errorf("Name = %v, want %v", deployment.Name, tt.wantName)
			}

			if deployment.Namespace != tt.wantNamespace {
				t.Errorf("Namespace = %v, want %v", deployment.Namespace, tt.wantNamespace)
			}

			if *deployment.Spec.Replicas != tt.wantReplicas {
				t.Errorf("Replicas = %v, want %v", *deployment.Spec.Replicas, tt.wantReplicas)
			}

			if len(deployment.Spec.Template.Spec.Containers) != 1 {
				t.Fatalf("Expected 1 container, got %d", len(deployment.Spec.Template.Spec.Containers))
			}

			container := deployment.Spec.Template.Spec.Containers[0]
			if container.Image != tt.wantImage {
				t.Errorf("Image = %v, want %v", container.Image, tt.wantImage)
			}

			if container.Name != FilterContainerName {
				t.Errorf("Container name = %v, want %v", container.Name, FilterContainerName)
			}

			// Check labels
			for k, v := range tt.wantLabels {
				if deployment.Labels[k] != v {
					t.Errorf("Label %s = %v, want %v", k, deployment.Labels[k], v)
				}
			}

			// Verify owner reference is set
			if len(deployment.OwnerReferences) != 1 {
				t.Errorf("Expected 1 owner reference, got %d", len(deployment.OwnerReferences))
			}

			// Verify ports
			if len(container.Ports) != 2 {
				t.Errorf("Expected 2 ports, got %d", len(container.Ports))
			}

			// Verify probes are set
			if container.LivenessProbe == nil {
				t.Error("LivenessProbe should not be nil")
			}
			if container.ReadinessProbe == nil {
				t.Error("ReadinessProbe should not be nil")
			}
		})
	}
}

func TestMakeFilterDeploymentWithResources(t *testing.T) {
	broker := &eventingv1.Broker{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-broker",
			Namespace: "test-namespace",
			UID:       "test-uid",
		},
	}

	args := &FilterArgs{
		Broker:             broker,
		Image:              "gcr.io/test/filter:latest",
		ServiceAccountName: "test-sa",
		StreamName:         "TEST_STREAM",
		NatsURL:            "nats://nats:4222",
		Template: &messagingv1alpha1.DeploymentTemplate{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
			},
		},
	}

	deployment := MakeFilterDeployment(args)
	container := deployment.Spec.Template.Spec.Containers[0]

	if container.Resources.Requests.Cpu().String() != "200m" {
		t.Errorf("CPU request = %v, want 200m", container.Resources.Requests.Cpu().String())
	}

	if container.Resources.Requests.Memory().String() != "256Mi" {
		t.Errorf("Memory request = %v, want 256Mi", container.Resources.Requests.Memory().String())
	}

	if container.Resources.Limits.Cpu().String() != "1" {
		t.Errorf("CPU limit = %v, want 1", container.Resources.Limits.Cpu().String())
	}
}

func TestMakeFilterDeploymentWithNodeSelector(t *testing.T) {
	broker := &eventingv1.Broker{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-broker",
			Namespace: "test-namespace",
			UID:       "test-uid",
		},
	}

	args := &FilterArgs{
		Broker:             broker,
		Image:              "gcr.io/test/filter:latest",
		ServiceAccountName: "test-sa",
		StreamName:         "TEST_STREAM",
		NatsURL:            "nats://nats:4222",
		Template: &messagingv1alpha1.DeploymentTemplate{
			NodeSelector: map[string]string{
				"disktype": "ssd",
			},
		},
	}

	deployment := MakeFilterDeployment(args)

	if deployment.Spec.Template.Spec.NodeSelector["disktype"] != "ssd" {
		t.Errorf("NodeSelector disktype = %v, want ssd", deployment.Spec.Template.Spec.NodeSelector["disktype"])
	}
}

func TestMakeFilterService(t *testing.T) {
	broker := &eventingv1.Broker{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-broker",
			Namespace: "test-namespace",
			UID:       "test-uid",
		},
	}

	service := MakeFilterService(broker)

	if service.Name != "test-broker-broker-filter" {
		t.Errorf("Name = %v, want test-broker-broker-filter", service.Name)
	}

	if service.Namespace != "test-namespace" {
		t.Errorf("Namespace = %v, want test-namespace", service.Namespace)
	}

	// Check labels
	if service.Labels[BrokerLabelKey] != "test-broker" {
		t.Errorf("Label %s = %v, want test-broker", BrokerLabelKey, service.Labels[BrokerLabelKey])
	}

	if service.Labels[RoleLabelKey] != FilterRoleLabelValue {
		t.Errorf("Label %s = %v, want %s", RoleLabelKey, service.Labels[RoleLabelKey], FilterRoleLabelValue)
	}

	// Check selector
	if service.Spec.Selector[BrokerLabelKey] != "test-broker" {
		t.Errorf("Selector %s = %v, want test-broker", BrokerLabelKey, service.Spec.Selector[BrokerLabelKey])
	}

	// Check ports
	if len(service.Spec.Ports) != 1 {
		t.Fatalf("Expected 1 port, got %d", len(service.Spec.Ports))
	}

	port := service.Spec.Ports[0]
	if port.Port != 80 {
		t.Errorf("Port = %v, want 80", port.Port)
	}

	if port.TargetPort.IntVal != FilterPortNumber {
		t.Errorf("TargetPort = %v, want %d", port.TargetPort.IntVal, FilterPortNumber)
	}

	// Verify owner reference is set
	if len(service.OwnerReferences) != 1 {
		t.Errorf("Expected 1 owner reference, got %d", len(service.OwnerReferences))
	}
}

func TestMakeFilterEnvVars(t *testing.T) {
	broker := &eventingv1.Broker{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-broker",
			Namespace: "test-namespace",
		},
	}

	args := &FilterArgs{
		Broker:             broker,
		Image:              "gcr.io/test/filter:latest",
		ServiceAccountName: "test-sa",
		StreamName:         "TEST_STREAM",
		NatsURL:            "nats://nats:4222",
	}

	deployment := MakeFilterDeployment(args)
	container := deployment.Spec.Template.Spec.Containers[0]
	envVars := container.Env

	// Build a map for easy lookup
	envMap := make(map[string]string)
	for _, env := range envVars {
		if env.Value != "" {
			envMap[env.Name] = env.Value
		}
	}

	// Check required environment variables
	if envMap["BROKER_NAME"] != "test-broker" {
		t.Errorf("BROKER_NAME = %v, want test-broker", envMap["BROKER_NAME"])
	}

	if envMap["BROKER_NAMESPACE"] != "test-namespace" {
		t.Errorf("BROKER_NAMESPACE = %v, want test-namespace", envMap["BROKER_NAMESPACE"])
	}

	if envMap["STREAM_NAME"] != "TEST_STREAM" {
		t.Errorf("STREAM_NAME = %v, want TEST_STREAM", envMap["STREAM_NAME"])
	}

	if envMap["NATS_URL"] != "nats://nats:4222" {
		t.Errorf("NATS_URL = %v, want nats://nats:4222", envMap["NATS_URL"])
	}

	if envMap["CONTAINER_NAME"] != FilterContainerName {
		t.Errorf("CONTAINER_NAME = %v, want %s", envMap["CONTAINER_NAME"], FilterContainerName)
	}

	// Filter should have CONFIG_LEADERELECTION_NAME
	if envMap["CONFIG_LEADERELECTION_NAME"] != "config-leader-election" {
		t.Errorf("CONFIG_LEADERELECTION_NAME = %v, want config-leader-election", envMap["CONFIG_LEADERELECTION_NAME"])
	}
}
