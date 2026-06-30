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
	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
)

// EffectiveDelivery merges a trigger's delivery spec over its broker's,
// returning the trigger's value for each field when set and falling back to the
// broker's otherwise. This implements Knative semantics where Broker.Spec.Delivery
// is the default for every trigger and Trigger.Spec.Delivery overrides it
// field-by-field. Returns nil when neither configures delivery.
func EffectiveDelivery(trigger *eventingv1.Trigger, broker *eventingv1.Broker) *eventingduckv1.DeliverySpec {
	var t, b *eventingduckv1.DeliverySpec
	if trigger != nil {
		t = trigger.Spec.Delivery
	}
	if broker != nil {
		b = broker.Spec.Delivery
	}
	switch {
	case t == nil:
		return b
	case b == nil:
		return t
	}

	out := *t
	if out.Retry == nil {
		out.Retry = b.Retry
	}
	if out.Timeout == nil {
		out.Timeout = b.Timeout
	}
	if out.BackoffPolicy == nil {
		out.BackoffPolicy = b.BackoffPolicy
	}
	if out.BackoffDelay == nil {
		out.BackoffDelay = b.BackoffDelay
	}
	if out.DeadLetterSink == nil {
		out.DeadLetterSink = b.DeadLetterSink
	}
	return &out
}
