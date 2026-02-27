//go:build e2e
// +build e2e

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

package e2e

import (
	"context"
	"testing"

	// For our e2e testing, we want this linked first so that our
	// system namespace environment variable is defaulted prior to
	// logstream initialization.
	_ "knative.dev/eventing-natss/test/defaultsystem"
	"knative.dev/pkg/system"
	_ "knative.dev/pkg/system/testing"
	"knative.dev/reconciler-test/pkg/eventshub"
	"knative.dev/reconciler-test/pkg/feature"
	"knative.dev/reconciler-test/pkg/k8s"
	"knative.dev/reconciler-test/pkg/knative"

	"knative.dev/eventing-natss/test/e2e/config/natsbroker"
)

// TestNatsBrokerDirect tests that a NatsJetStream broker can deliver events to a consumer.
func TestNatsBrokerDirect(t *testing.T) {
	t.Parallel()
	ctx, env := global.Environment(
		knative.WithKnativeNamespace(system.Namespace()),
		knative.WithLoggingConfig,
		knative.WithObservabilityConfig,
		k8s.WithEventListener,
	)
	env.Test(ctx, t, RecorderFeature())
	env.Test(ctx, t, NatsBrokerDirectFeature())
	env.Finish()
}

// NatsBrokerDirectFeature tests direct event delivery through NatsJetStream broker.
//
//	producer ---> broker --[trigger]--> recorder
func NatsBrokerDirectFeature() *feature.Feature {
	f := new(feature.Feature)

	f.Setup("install NatsJetStream broker resources", natsbroker.Install())
	f.Alpha("NatsJetStream broker goes ready").Must("goes ready", AllGoReady)
	f.Alpha("NatsJetStream broker delivers events").
		Must("the recorder received all sent events within the time",
			func(ctx context.Context, t feature.T) {
				eventshub.StoreFromContext(ctx, "recorder").AssertAtLeast(ctx, t, 5)
			})
	return f
}
