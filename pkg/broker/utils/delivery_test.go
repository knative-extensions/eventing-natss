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

func TestDeliveryIsSet(t *testing.T) {
	r := int32(1)
	backoffMax := "PT10M"
	tests := []struct {
		name string
		spec *eventingduckv1.DeliverySpec
		want bool
	}{
		{name: "nil", spec: nil, want: false},
		{name: "empty", spec: &eventingduckv1.DeliverySpec{}, want: false},
		{name: "retry set", spec: &eventingduckv1.DeliverySpec{Retry: &r}, want: true},
		{name: "backoff max set", spec: &eventingduckv1.DeliverySpec{BackoffMax: &backoffMax}, want: true},
		{name: "dls set", spec: &eventingduckv1.DeliverySpec{DeadLetterSink: &duckv1.Destination{}}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeliveryIsSet(tc.spec); got != tc.want {
				t.Errorf("DeliveryIsSet(%+v) = %v, want %v", tc.spec, got, tc.want)
			}
		})
	}
}

// TestEffectiveDelivery verifies whole-spec precedence: if the trigger sets any
// delivery field its spec is used in its entirety (nothing from the broker);
// the broker's spec is used only when the trigger sets no delivery.
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
	backoffMax := "PT4S"

	tests := []struct {
		name      string
		trigger   *eventingduckv1.DeliverySpec
		broker    *eventingduckv1.DeliverySpec
		wantSpec  *eventingduckv1.DeliverySpec // expected identity of the returned spec
		wantRetry *int32
		wantDLS   *duckv1.Destination
		wantMax   *string
	}{
		{name: "both nil"},
		{
			name:      "broker only",
			broker:    &eventingduckv1.DeliverySpec{Retry: &r5, DeadLetterSink: bDLS},
			wantRetry: &r5, wantDLS: bDLS,
		},
		{
			name:      "trigger only",
			trigger:   &eventingduckv1.DeliverySpec{Retry: &r2, DeadLetterSink: tDLS},
			wantRetry: &r2, wantDLS: tDLS,
		},
		{
			// Trigger sets retry but no DLS: its spec wins wholesale, so the
			// broker's DLS is NOT inherited.
			name:      "trigger set wins wholesale",
			trigger:   &eventingduckv1.DeliverySpec{Retry: &r2},
			broker:    &eventingduckv1.DeliverySpec{Retry: &r5, DeadLetterSink: bDLS},
			wantRetry: &r2, wantDLS: nil,
		},
		{
			// Empty (non-nil) trigger delivery counts as "not set" → broker wins.
			name:      "empty trigger falls back to broker",
			trigger:   &eventingduckv1.DeliverySpec{},
			broker:    &eventingduckv1.DeliverySpec{Retry: &r5, DeadLetterSink: bDLS},
			wantRetry: &r5, wantDLS: bDLS,
		},
		{
			name:      "trigger backoff max alone wins wholesale",
			trigger:   &eventingduckv1.DeliverySpec{BackoffMax: &backoffMax},
			broker:    &eventingduckv1.DeliverySpec{Retry: &r5, DeadLetterSink: bDLS},
			wantMax:   &backoffMax,
			wantRetry: nil,
			wantDLS:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EffectiveDelivery(trig(tc.trigger), brk(tc.broker))
			if tc.wantRetry == nil && tc.wantDLS == nil && tc.trigger == nil && tc.broker == nil {
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
			if got.BackoffMax != tc.wantMax {
				t.Errorf("BackoffMax = %v, want %v", got.BackoffMax, tc.wantMax)
			}
		})
	}
}
