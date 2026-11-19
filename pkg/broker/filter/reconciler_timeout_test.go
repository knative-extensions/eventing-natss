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

package filter

import (
	"context"
	"fmt"
	"testing"
	"time"

	"knative.dev/pkg/logging"

	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"

	"knative.dev/eventing-natss/pkg/broker/constants"
)

// TestReconcileTrigger_RequestTimeout verifies that each subscriber request
// times out after the delivery timeout, with the same default and parsing the
// Trigger reconciler uses for the consumer's AckWait.
func TestReconcileTrigger_RequestTimeout(t *testing.T) {
	stringPtr := func(s string) *string { return &s }
	tests := []struct {
		name     string
		delivery *eventingduckv1.DeliverySpec
		want     time.Duration
	}{{
		name: "unset delivery uses the default",
		want: 30 * time.Second,
	}, {
		name:     "delivery timeout",
		delivery: &eventingduckv1.DeliverySpec{Timeout: stringPtr("PT5S")},
		want:     5 * time.Second,
	}, {
		name: "delivery timeout with another invalid field",
		delivery: &eventingduckv1.DeliverySpec{
			Timeout:      stringPtr("PT5S"),
			BackoffDelay: stringPtr("invalid"),
		},
		want: 5 * time.Second,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))
			broker := newReadyTestBroker(testNamespace, testBrokerName, constants.BrokerClassName)
			trigger := newTestTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "http://subscriber.example.com")
			trigger.Spec.Delivery = tc.delivery
			triggerUID := string(trigger.UID)

			brokerLister := newFakeBrokerLister()
			brokerLister.addBroker(broker)
			existingHandler := newTestHandler(t, ctx, "http://subscriber.example.com", "")
			cm := &ConsumerManager{
				logger: logging.FromContext(ctx),
				ctx:    ctx,
				js:     &fakeJetStream{consumerInfoErr: fmt.Errorf("should not be called")},
				subscriptions: map[string]*TriggerSubscription{
					triggerUID: {trigger: trigger, handler: existingHandler},
				},
			}
			r := &FilterReconciler{
				logger:          logging.FromContext(ctx),
				brokerLister:    brokerLister,
				consumerManager: cm,
			}

			if err := r.ReconcileTrigger(ctx, trigger); err != nil {
				t.Fatalf("ReconcileTrigger() unexpected error: %v", err)
			}
			config := snapshotHandlerConfig(t, cm.subscriptions[triggerUID].handler)
			if config.noRetryConfig == nil {
				t.Fatal("handler noRetryConfig is nil")
			}
			if got := config.noRetryConfig.RequestTimeout; got != tc.want {
				t.Errorf("RequestTimeout = %v, want %v", got, tc.want)
			}
		})
	}
}
