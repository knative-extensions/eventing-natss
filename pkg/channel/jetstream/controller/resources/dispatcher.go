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

package resources

import (
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/system"

	"knative.dev/eventing-natss/pkg/channel/jetstream"

	commonconfig "knative.dev/eventing-natss/pkg/common/config"
	"knative.dev/eventing-natss/pkg/common/constants"
)

const (
	DispatcherContainerName = "dispatcher"
)

var (
	DispatcherLabels = map[string]string{
		ChannelLabelKey: ChannelLabelValue,
		RoleLabelKey:    DispatcherRoleLabelValue,
	}
)

type DispatcherDeploymentBuilder struct {
	deployment *v1.Deployment
	args       *DispatcherDeploymentArgs
}

type DispatcherDeploymentArgs struct {
	DispatcherScope        string
	DispatcherNamespace    string
	Image                  string
	Replicas               int32
	ServiceAccount         string
	ConfigMapName          string
	ConfigMapHash          string
	OwnerRef               metav1.OwnerReference
	DeploymentAnnotations  map[string]string
	DeploymentLabels       map[string]string
	DeploymentResources    corev1.ResourceRequirements
	DeploymentNodeSelector map[string]string
	DeploymentAffinity     *corev1.Affinity
	PodAnnotations         map[string]string
	PodLabels              map[string]string
}

// NewDispatcherDeploymentBuilder returns a builder which builds from scratch a dispatcher deployment.
// Intended to be used when creating the dispatcher deployment for the first time.
func NewDispatcherDeploymentBuilder() *DispatcherDeploymentBuilder {
	b := &DispatcherDeploymentBuilder{}
	b.deployment = dispatcherTemplate()
	return b
}

// NewDispatcherDeploymentBuilderFromDeployment returns a builder which builds a dispatcher deployment from the given deployment.
// Intended to be used when updating an existing dispatcher deployment.
func NewDispatcherDeploymentBuilderFromDeployment(d *v1.Deployment) *DispatcherDeploymentBuilder {
	b := &DispatcherDeploymentBuilder{}
	b.deployment = d
	return b
}

func (b *DispatcherDeploymentBuilder) WithArgs(args *DispatcherDeploymentArgs) *DispatcherDeploymentBuilder {
	b.args = args
	return b
}

func (b *DispatcherDeploymentBuilder) Build() *v1.Deployment {
	replicas := b.args.Replicas

	b.deployment.Namespace = b.args.DispatcherNamespace
	b.deployment.OwnerReferences = []metav1.OwnerReference{b.args.OwnerRef}
	b.deployment.Annotations = commonconfig.JoinStringMaps(b.deployment.Annotations, b.args.DeploymentAnnotations)
	b.deployment.Labels = commonconfig.JoinStringMaps(b.deployment.Labels, b.args.DeploymentLabels)

	b.deployment.Spec.Replicas = &replicas
	defaultAnnotations := map[string]string{constants.ConfigMapHashAnnotationKey: b.args.ConfigMapHash}
	b.deployment.Spec.Template.Annotations = commonconfig.JoinStringMaps(defaultAnnotations, b.args.PodAnnotations)
	b.deployment.Spec.Template.Labels = commonconfig.JoinStringMaps(b.deployment.Spec.Template.Labels, b.args.PodLabels)
	b.deployment.Spec.Template.Spec.ServiceAccountName = b.args.ServiceAccount

	b.deployment.Spec.Template.Spec.NodeSelector = b.args.DeploymentNodeSelector
	b.deployment.Spec.Template.Spec.Affinity = b.args.DeploymentAffinity

	for i, c := range b.deployment.Spec.Template.Spec.Containers {
		if c.Name == DispatcherContainerName {
			container := &b.deployment.Spec.Template.Spec.Containers[i]
			container.Resources = b.args.DeploymentResources
			container.Image = b.args.Image
			if container.Env == nil {
				container.Env = makeEnv(b.args)
			}

			break
		}
	}

	// the config map name may be overridden by the config-ch-jetstream-defaults ConfigMap
	if b.args.ConfigMapName != "" {
		for i, v := range b.deployment.Spec.Template.Spec.Volumes {
			if v.Name == constants.SettingsConfigMapName {
				b.deployment.Spec.Template.Spec.Volumes[i].ConfigMap.Name = b.args.ConfigMapName

				break
			}
		}
	}

	return b.deployment
}

func dispatcherTemplate() *v1.Deployment {

	return &v1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployments",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: jetstream.DispatcherName,
		},
		Spec: v1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: DispatcherLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: DispatcherLabels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: DispatcherContainerName,
							Ports: []corev1.ContainerPort{{
								Name:          "metrics",
								ContainerPort: 9090,
							}},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      constants.SettingsConfigMapName,
									MountPath: constants.SettingsConfigMapMountPath,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: constants.SettingsConfigMapName,
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: constants.SettingsConfigMapName,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func makeEnv(args *DispatcherDeploymentArgs) []corev1.EnvVar {
	vars := []corev1.EnvVar{{
		Name:  system.NamespaceEnvKey,
		Value: system.Namespace(),
	}, {
		Name:  "METRICS_DOMAIN",
		Value: "knative.dev/eventing",
	}, {
		Name:  "CONFIG_LOGGING_NAME",
		Value: "config-logging",
	}, {
		Name:  "CONFIG_LEADERELECTION_NAME",
		Value: "config-leader-election",
	}}

	if args.DispatcherScope == "namespace" {
		vars = append(vars, corev1.EnvVar{
			Name: "NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		})
	}

	vars = append(vars, corev1.EnvVar{
		Name: "POD_NAME",
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				APIVersion: "",
				FieldPath:  "metadata.name",
			},
		},
	}, corev1.EnvVar{
		Name:  "CONTAINER_NAME",
		Value: "dispatcher",
	})
	return vars
}
