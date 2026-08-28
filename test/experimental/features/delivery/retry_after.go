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

package delivery

import (
	"net/http"
	"time"

	"k8s.io/utils/ptr"
	"knative.dev/reconciler-test/pkg/feature"

	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
)

// RetryAfterMaxBrokerToSink verifies that RetryAfterMax caps Broker retries.
func RetryAfterMaxBrokerToSink() *feature.Feature {
	return retryAfterMax("Broker delivery Retry-After maximum", brokerRoute)
}

// RetryAfterMaxChannelToSink verifies that a Channel default RetryAfterMax caps Subscription retries.
func RetryAfterMaxChannelToSink() *feature.Feature {
	return retryAfterMax("Channel delivery Retry-After maximum", channelRoute)
}

func retryAfterMax(name string, route deliveryRoute) *feature.Feature {
	backoffPolicy := eventingduckv1.BackoffPolicyExponential
	return newDeliveryFeature(name, "retry-after-max", route, retryBehavior{
		retries:         2,
		responseCode:    http.StatusTooManyRequests,
		responseHeaders: map[string]string{"Retry-After": "6"},
		delivery: &eventingduckv1.DeliverySpec{
			Retry:         ptr.To(int32(2)),
			BackoffPolicy: &backoffPolicy,
			BackoffDelay:  ptr.To("PT1S"),
			BackoffMax:    ptr.To("PT2S"),
			RetryAfterMax: ptr.To("PT2S"),
		},
		expectedIntervals: []time.Duration{2 * time.Second, 2 * time.Second},
		rejectedStep:      "receiver rejects the first two deliveries",
		receivedStep:      "receiver accepts the third delivery",
		timingStep:        "Retry-After delay is capped at two seconds",
	})
}
