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

// DeliveryIsSet reports whether the delivery spec configures anything. A nil
// spec, or a non-nil spec with every field unset, is considered not set.
func DeliveryIsSet(d *eventingduckv1.DeliverySpec) bool {
	return d != nil && (d.DeadLetterSink != nil ||
		d.Retry != nil ||
		d.Timeout != nil ||
		d.BackoffPolicy != nil ||
		d.BackoffDelay != nil ||
		d.RetryAfterMax != nil ||
		d.Format != nil)
}

// EffectiveDelivery returns the delivery spec that applies to a trigger. The
// trigger's spec takes precedence as a whole: if the trigger sets any delivery
// field, its spec is used in its entirety and nothing is taken from the broker.
// The broker's spec is used only when the trigger configures no delivery. This
// matches Knative semantics, where Trigger.Spec.Delivery overrides
// Broker.Spec.Delivery wholesale rather than field-by-field. Returns nil when
// neither configures delivery.
func EffectiveDelivery(trigger *eventingv1.Trigger, broker *eventingv1.Broker) *eventingduckv1.DeliverySpec {
	if trigger != nil && DeliveryIsSet(trigger.Spec.Delivery) {
		return trigger.Spec.Delivery
	}
	if broker != nil {
		return broker.Spec.Delivery
	}
	return nil
}
