/*
Copyright 2021 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package constants

const (
	// SettingsConfigMapName is the name of the configmap used to hold eventing-nats settings
	SettingsConfigMapName = "config-nats-broker"

	// SettingsConfigMapMountPath is the mount path of the configmap used to hold eventing-nats settings
	SettingsConfigMapMountPath = "/etc/" + SettingsConfigMapName

	// EventingNatsSettingsConfigKey is an entry of the SettingsConfigMapName configmap.
	EventingNatsSettingsConfigKey = "eventing-nats"

	// ConfigMapHashAnnotationKey is an annotation is used by the controller to track updates
	// to config-nats and apply them in the dispatcher deployment
	ConfigMapHashAnnotationKey = "jetstream.eventing.knative.dev/configmap-hash"

	// KnativeConfigMapEventingRole is the role granting workloads access to read configmaps and leases within the
	// knative-eventing namespace. This must be bound to namespace-scoped dispatchers in order for it to function.
	KnativeConfigMapEventingRole = "jetstream-ch-dispatcher-eventing"

	// DefaultNatsURL is derived from the installation documentation which defaults to this service name within the
	// nats-io namespace.
	DefaultNatsURL = "nats://nats.nats-io.svc.cluster.local"

	// DefaultCredentialFileSecretKey is the key within the data section of the "Credentials File" Secret.
	DefaultCredentialFileSecretKey = "nats.creds"
)
