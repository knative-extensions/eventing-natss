/*
Copyright 2020 The Knative Authors

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

package main

import (
	natswebhook "knative.dev/eventing-natss/pkg/webhook"

	"knative.dev/pkg/injection/sharedmain"
	"knative.dev/pkg/signals"
	"knative.dev/pkg/webhook"
	"knative.dev/pkg/webhook/certificates"
)

const (
	// Component is the name of this component and is used in logging and leader-election
	Component = "nats-webhook"

	// SecretName must match the name of the Secret created in the configuration.
	SecretName = "nats-webhook-certs"
)

func main() {
	// Set up a signal context with our webhook options
	ctx := webhook.WithOptions(signals.NewContext(), webhook.Options{
		ServiceName: webhook.NameFromEnv(),
		Port:        webhook.PortFromEnv(8443),
		SecretName:  SecretName,
	})

	sharedmain.MainWithContext(ctx, Component,
		certificates.NewController,
		natswebhook.NewValidationAdmissionController,
		natswebhook.NewDefaultingAdmissionController,
	)
}
