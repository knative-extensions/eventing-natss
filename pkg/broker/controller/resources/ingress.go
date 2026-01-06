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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"knative.dev/pkg/kmeta"
	"knative.dev/pkg/system"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"

	messagingv1alpha1 "knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
)

// IngressArgs contains arguments for creating ingress resources
type IngressArgs struct {
	Broker             *eventingv1.Broker
	Image              string
	ServiceAccountName string
	StreamName         string
	NatsURL            string
	Template           *messagingv1alpha1.DeploymentTemplate
}

// MakeIngressDeployment creates a Deployment for the broker ingress
func MakeIngressDeployment(args *IngressArgs) *appsv1.Deployment {
	broker := args.Broker
	name := IngressName(broker.Name)
	labels := IngressLabels(broker.Name)

	// Default replicas
	replicas := int32(1)

	// Deployment labels and annotations
	deploymentLabels := labels
	var deploymentAnnotations map[string]string

	// Pod labels and annotations
	podLabels := labels
	var podAnnotations map[string]string

	// Pod spec customization
	var nodeSelector map[string]string
	var affinity *corev1.Affinity
	var resources corev1.ResourceRequirements

	// Apply template if provided
	if args.Template != nil {
		if args.Template.Replicas != nil {
			replicas = *args.Template.Replicas
		}
		if args.Template.Annotations != nil {
			deploymentAnnotations = args.Template.Annotations
		}
		if args.Template.Labels != nil {
			deploymentLabels = mergeMaps(labels, args.Template.Labels)
		}
		if args.Template.PodAnnotations != nil {
			podAnnotations = args.Template.PodAnnotations
		}
		if args.Template.PodLabels != nil {
			podLabels = mergeMaps(labels, args.Template.PodLabels)
		}
		if args.Template.NodeSelector != nil {
			nodeSelector = args.Template.NodeSelector
		}
		if args.Template.Affinity != nil {
			affinity = args.Template.Affinity
		}
		resources = args.Template.Resources
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       broker.Namespace,
			Labels:          deploymentLabels,
			Annotations:     deploymentAnnotations,
			OwnerReferences: []metav1.OwnerReference{*kmeta.NewControllerRef(broker)},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: podAnnotations,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: args.ServiceAccountName,
					NodeSelector:       nodeSelector,
					Affinity:           affinity,
					Containers: []corev1.Container{
						{
							Name:      IngressContainerName,
							Image:     args.Image,
							Env:       makeIngressEnv(args),
							Resources: resources,
							Ports: []corev1.ContainerPort{
								{
									Name:          IngressPortName,
									ContainerPort: IngressPortNumber,
									Protocol:      corev1.ProtocolTCP,
								},
								{
									Name:          MetricsPortName,
									ContainerPort: MetricsPortNumber,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(IngressPortNumber),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/readyz",
										Port: intstr.FromInt(IngressPortNumber),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
						},
					},
				},
			},
		},
	}
}

// MakeIngressService creates a Service for the broker ingress
func MakeIngressService(broker *eventingv1.Broker) *corev1.Service {
	name := IngressName(broker.Name)
	labels := IngressLabels(broker.Name)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       broker.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{*kmeta.NewControllerRef(broker)},
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:       IngressPortName,
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromInt(IngressPortNumber),
				},
			},
		},
	}
}

func makeIngressEnv(args *IngressArgs) []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name:  system.NamespaceEnvKey,
			Value: system.Namespace(),
		},
		{
			Name:  "BROKER_NAME",
			Value: args.Broker.Name,
		},
		{
			Name:  "BROKER_NAMESPACE",
			Value: args.Broker.Namespace,
		},
		{
			Name:  "STREAM_NAME",
			Value: args.StreamName,
		},
		{
			Name:  "NATS_URL",
			Value: args.NatsURL,
		},
		{
			Name:  "METRICS_DOMAIN",
			Value: "knative.dev/eventing",
		},
		{
			Name:  "CONFIG_LOGGING_NAME",
			Value: "config-logging",
		},
		{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		{
			Name:  "CONTAINER_NAME",
			Value: IngressContainerName,
		},
	}
}
