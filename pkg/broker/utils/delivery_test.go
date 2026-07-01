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

package utils

import (
	"testing"

	duckv1 "knative.dev/pkg/apis/duck/v1"

	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
)

func TestEffectiveDelivery(t *testing.T) {
	trig := func(d *eventingduckv1.DeliverySpec) *eventingv1.Trigger {
		return &eventingv1.Trigger{Spec: eventingv1.TriggerSpec{Delivery: d}}
	}
	brk := func(d *eventingduckv1.DeliverySpec) *eventingv1.Broker {
		return &eventingv1.Broker{Spec: eventingv1.BrokerSpec{Delivery: d}}
	}
	tDLS := &duckv1.Destination{}
	bDLS := &duckv1.Destination{}
	r2, r5 := int32(2), int32(5)

	tests := []struct {
		name        string
		trigger     *eventingduckv1.DeliverySpec
		broker      *eventingduckv1.DeliverySpec
		wantRetry   *int32
		wantDLS     *duckv1.Destination
		wantNilSpec bool
	}{
		{name: "both nil", wantNilSpec: true},
		{name: "broker only", broker: &eventingduckv1.DeliverySpec{Retry: &r5, DeadLetterSink: bDLS}, wantRetry: &r5, wantDLS: bDLS},
		{name: "trigger only", trigger: &eventingduckv1.DeliverySpec{Retry: &r2, DeadLetterSink: tDLS}, wantRetry: &r2, wantDLS: tDLS},
		{
			name:      "trigger overrides retry, inherits DLS",
			trigger:   &eventingduckv1.DeliverySpec{Retry: &r2},
			broker:    &eventingduckv1.DeliverySpec{Retry: &r5, DeadLetterSink: bDLS},
			wantRetry: &r2,
			wantDLS:   bDLS,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EffectiveDelivery(trig(tc.trigger), brk(tc.broker))
			if tc.wantNilSpec {
				if got != nil {
					t.Fatalf("got %+v, want nil", got)
				}
				return
			}
			if got.Retry != tc.wantRetry {
				t.Errorf("Retry = %v, want %v", got.Retry, tc.wantRetry)
			}
			if got.DeadLetterSink != tc.wantDLS {
				t.Errorf("DeadLetterSink = %v, want %v", got.DeadLetterSink, tc.wantDLS)
			}
		})
	}
}

// TestEffectiveDelivery_AllFields ensures every DeliverySpec field is merged:
// unset trigger fields inherit the broker's, set trigger fields win.
func TestEffectiveDelivery_AllFields(t *testing.T) {
	retry := int32(7)
	timeout, backoffDelay, retryAfterMax := "PT1M", "PT2S", "PT30S"
	backoffPolicy := eventingduckv1.BackoffPolicyLinear
	format := eventingduckv1.DeliveryFormatBinary
	full := &eventingduckv1.DeliverySpec{
		DeadLetterSink: &duckv1.Destination{},
		Retry:          &retry,
		Timeout:        &timeout,
		BackoffPolicy:  &backoffPolicy,
		BackoffDelay:   &backoffDelay,
		RetryAfterMax:  &retryAfterMax,
		Format:         &format,
	}

	broker := &eventingv1.Broker{Spec: eventingv1.BrokerSpec{Delivery: full}}

	// Trigger with no delivery inherits every broker field.
	got := EffectiveDelivery(&eventingv1.Trigger{}, broker)
	if got.DeadLetterSink != full.DeadLetterSink || got.Retry != full.Retry ||
		got.Timeout != full.Timeout || got.BackoffPolicy != full.BackoffPolicy ||
		got.BackoffDelay != full.BackoffDelay || got.RetryAfterMax != full.RetryAfterMax ||
		got.Format != full.Format {
		t.Errorf("inherited spec = %+v, want all fields from broker %+v", got, full)
	}

	// Trigger that sets a field overrides only that field.
	otherMax := "PT99S"
	trigger := &eventingv1.Trigger{Spec: eventingv1.TriggerSpec{
		Delivery: &eventingduckv1.DeliverySpec{RetryAfterMax: &otherMax},
	}}
	got = EffectiveDelivery(trigger, broker)
	if got.RetryAfterMax != &otherMax {
		t.Errorf("RetryAfterMax = %v, want trigger's %v", got.RetryAfterMax, &otherMax)
	}
	if got.Format != full.Format {
		t.Errorf("Format = %v, want inherited %v", got.Format, full.Format)
	}
}
