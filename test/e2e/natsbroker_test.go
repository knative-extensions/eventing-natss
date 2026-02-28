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
	"fmt"
	"testing"
	"time"

	// For our e2e testing, we want this linked first so that our
	// system namespace environment variable is defaulted prior to
	// logstream initialization.
	_ "knative.dev/eventing-natss/test/defaultsystem"
	"knative.dev/pkg/system"
	_ "knative.dev/pkg/system/testing"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"knative.dev/reconciler-test/pkg/eventshub"
	"knative.dev/reconciler-test/pkg/feature"
	"knative.dev/reconciler-test/pkg/k8s"
	"knative.dev/reconciler-test/pkg/knative"

	"knative.dev/eventing-natss/test/e2e/config/deadletter"
	"knative.dev/eventing-natss/test/e2e/config/filtering"
	"knative.dev/eventing-natss/test/e2e/config/natsbroker"
)

// hasEventType returns an EventInfoMatcher that matches CloudEvents of the given type.
func hasEventType(eventType string) eventshub.EventInfoMatcher {
	return func(info eventshub.EventInfo) error {
		if info.Event == nil {
			return fmt.Errorf("event is nil")
		}
		if info.Event.Type() != eventType {
			return fmt.Errorf("event type %q, want %q", info.Event.Type(), eventType)
		}
		return nil
	}
}

// namedRecorderFeature creates a feature that installs an eventshub receiver with the given name.
func namedRecorderFeature(name string) *feature.Feature {
	svc := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
	f := new(feature.Feature)
	f.Setup("install "+name, eventshub.Install(name, eventshub.StartReceiver))
	f.Requirement(name+" is addressable", k8s.IsAddressable(svc, name, time.Second, 30*time.Second))
	return f
}

// TestNatsBrokerDirect tests that a NatsJetStream broker delivers events to a consumer.
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

	// Install broker and trigger first, then wait for them to be ready before
	// installing the producer. This ensures the NATS consumer exists before
	// events are published, so no events are missed with DeliverNew policy.
	f.Setup("install broker and trigger", natsbroker.InstallBrokerAndTrigger())
	f.Setup("wait for broker and trigger ready", AllGoReady)
	f.Setup("install producer", natsbroker.InstallProducer(5))

	f.Alpha("NatsJetStream broker goes ready").Must("goes ready", AllGoReady)
	f.Alpha("NatsJetStream broker delivers events").
		Must("the recorder received all sent events within the time",
			func(ctx context.Context, t feature.T) {
				eventshub.StoreFromContext(ctx, "recorder").AssertAtLeast(ctx, t, 5,
					hasEventType("knative.natsbroker.e2etest"))
			})
	return f
}

// TestNatsBrokerDeadLetter tests that a NatsJetStream broker sends failed events to a dead letter sink.
func TestNatsBrokerDeadLetter(t *testing.T) {
	t.Parallel()
	ctx, env := global.Environment(
		knative.WithKnativeNamespace(system.Namespace()),
		knative.WithLoggingConfig,
		knative.WithObservabilityConfig,
		k8s.WithEventListener,
	)
	env.Test(ctx, t, namedRecorderFeature("dls-recorder"))
	env.Test(ctx, t, NatsBrokerDeadLetterFeature())
	env.Finish()
}

// NatsBrokerDeadLetterFeature tests that failed events reach the dead letter sink.
//
//	producer ---> broker --[trigger]--> failing-subscriber (500) --[DLS]--> dls-recorder
func NatsBrokerDeadLetterFeature() *feature.Feature {
	f := new(feature.Feature)

	f.Setup("install broker, trigger and failing-subscriber", deadletter.InstallBrokerAndSubscriber())
	f.Setup("wait for broker and trigger ready", AllGoReady)
	f.Setup("install producer", deadletter.InstallProducer(2))

	f.Alpha("NatsJetStream broker with DLS goes ready").Must("goes ready", AllGoReady)
	f.Alpha("NatsJetStream broker sends failed events to dead letter sink").
		Must("the dls-recorder received all sent events",
			func(ctx context.Context, t feature.T) {
				eventshub.StoreFromContext(ctx, "dls-recorder").AssertAtLeast(ctx, t, 2,
					hasEventType("dls.test.event"))
			})
	return f
}

// TestNatsBrokerFiltering tests that a NatsJetStream broker routes events by type to the correct subscriber.
func TestNatsBrokerFiltering(t *testing.T) {
	t.Parallel()
	ctx, env := global.Environment(
		knative.WithKnativeNamespace(system.Namespace()),
		knative.WithLoggingConfig,
		knative.WithObservabilityConfig,
		k8s.WithEventListener,
	)
	env.Test(ctx, t, namedRecorderFeature("recorder-type-a"))
	env.Test(ctx, t, namedRecorderFeature("recorder-type-b"))
	env.Test(ctx, t, NatsBrokerFilteringFeature())
	env.Finish()
}

// NatsBrokerFilteringFeature tests type-based event routing through NatsJetStream broker.
//
//	producer-type-a ---> broker --[trigger type-a filter]--> recorder-type-a
//	producer-type-b ---> broker --[trigger type-b filter]--> recorder-type-b
func NatsBrokerFilteringFeature() *feature.Feature {
	f := new(feature.Feature)

	f.Setup("install broker and triggers", filtering.InstallBrokerAndTriggers())
	f.Setup("wait for broker and triggers ready", AllGoReady)
	f.Setup("install producers", filtering.InstallProducers(3, 2))

	f.Alpha("NatsJetStream broker with filtering goes ready").Must("goes ready", AllGoReady)
	f.Alpha("NatsJetStream broker routes type-a events to correct subscriber").
		Must("recorder-type-a received only type-a events",
			func(ctx context.Context, t feature.T) {
				eventshub.StoreFromContext(ctx, "recorder-type-a").AssertAtLeast(ctx, t, 3,
					hasEventType("type-a"))
			})
	f.Alpha("NatsJetStream broker routes type-b events to correct subscriber").
		Must("recorder-type-b received only type-b events",
			func(ctx context.Context, t feature.T) {
				eventshub.StoreFromContext(ctx, "recorder-type-b").AssertAtLeast(ctx, t, 2,
					hasEventType("type-b"))
			})
	return f
}
