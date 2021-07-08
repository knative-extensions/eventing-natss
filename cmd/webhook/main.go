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
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/injection/sharedmain"
	"knative.dev/pkg/signals"
	"knative.dev/pkg/webhook"
	"knative.dev/pkg/webhook/certificates"
	"knative.dev/pkg/webhook/resourcesemantics"
	"knative.dev/pkg/webhook/resourcesemantics/defaulting"
	"knative.dev/pkg/webhook/resourcesemantics/validation"

	"knative.dev/eventing-natss/pkg/apis/messaging/v1beta1"
)

var ourTypes = map[schema.GroupVersionKind]resourcesemantics.GenericCRD{
	// For group messaging.knative.dev.
	// v1beta1
	v1beta1.SchemeGroupVersion.WithKind("NatssChannel"): &v1beta1.NatssChannel{},
}

var callbacks = map[schema.GroupVersionKind]validation.Callback{}

func NewDefaultingAdmissionController(ctx context.Context, cmw configmap.Watcher) *controller.Impl {
	// A function that infuses the context passed to ConvertTo/ConvertFrom/SetDefaults with custom metadata.
	ctxFunc := func(ctx context.Context) context.Context {
		return ctx
	}

	return defaulting.NewAdmissionController(ctx,

		// Name of the resource webhook.
		"webhook.natss.messaging.knative.dev",

		// The path on which to serve the webhook.
		"/defaulting",

		// The resources to default.
		ourTypes,

		// A function that infuses the context passed to Validate/SetDefaults with custom metadata.
		ctxFunc,

		// Whether to disallow unknown fields.
		true,
	)
}

func NewValidationAdmissionController(ctx context.Context, cmw configmap.Watcher) *controller.Impl {
	// A function that infuses the context passed to ConvertTo/ConvertFrom/SetDefaults with custom metadata.
	ctxFunc := func(ctx context.Context) context.Context {
		return ctx
	}
	return validation.NewAdmissionController(ctx,

		// Name of the resource webhook.
		"validation.webhook.natss.messaging.knative.dev",

		// The path on which to serve the webhook.
		"/resource-validation",

		// The resources to validate.
		ourTypes,

		// A function that infuses the context passed to Validate/SetDefaults with custom metadata.
		ctxFunc,

		// Whether to disallow unknown fields.
		true,

		// Extra validating callbacks to be applied to resources.
		callbacks,
	)
}

func main() {
	// Set up a signal context with our webhook options
	ctx := webhook.WithOptions(signals.NewContext(), webhook.Options{
		ServiceName: "nats-webhook",
		Port:        webhook.PortFromEnv(8443),
		// SecretName must match the name of the Secret created in the configuration.
		SecretName: "nats-webhook-certs",
	})

	sharedmain.WebhookMainWithContext(ctx, "natss-webhook",
		certificates.NewController,
		NewValidationAdmissionController,
		NewDefaultingAdmissionController,
	)
}
