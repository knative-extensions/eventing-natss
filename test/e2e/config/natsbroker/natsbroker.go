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

package natsbroker

import (
	"context"
	"embed"

	"knative.dev/reconciler-test/pkg/environment"
	"knative.dev/reconciler-test/pkg/feature"
	"knative.dev/reconciler-test/pkg/manifest"
)

//go:embed *.yaml
var yamls embed.FS

//go:embed broker.yaml trigger.yaml
var brokerYamls embed.FS

//go:embed producer.yaml
var producerYamls embed.FS

// InstallBrokerAndTrigger installs the broker and trigger resources.
// Call this before waiting for readiness, then call InstallProducer after
// the broker and trigger are ready to avoid missing events with DeliverNew policy.
func InstallBrokerAndTrigger() feature.StepFn {
	return func(ctx context.Context, t feature.T) {
		registerImages(ctx, t)
		if _, err := manifest.InstallYamlFS(ctx, brokerYamls, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// InstallProducer installs the producer job. Call this after the broker and
// trigger are ready so that the NATS consumer exists before events are sent.
func InstallProducer(count int) feature.StepFn {
	return func(ctx context.Context, t feature.T) {
		registerImages(ctx, t)
		args := map[string]interface{}{"producerCount": count}
		if _, err := manifest.InstallYamlFS(ctx, producerYamls, args); err != nil {
			t.Fatal(err)
		}
	}
}

func registerImages(ctx context.Context, t feature.T) {
	opt := environment.RegisterPackage(manifest.ImagesFromFS(ctx, yamls)...)
	if _, err := opt(ctx, nil); err != nil {
		t.Fatal(err)
	}
}
