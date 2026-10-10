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

// BackoffMaxBrokerToSink verifies that BackoffMax caps Broker retries.
func BackoffMaxBrokerToSink() *feature.Feature {
	return backoffMax("Broker delivery backoff maximum", brokerRoute)
}

// BackoffMaxChannelToSink verifies that a Channel default BackoffMax caps Subscription retries.
func BackoffMaxChannelToSink() *feature.Feature {
	return backoffMax("Channel delivery backoff maximum", channelRoute)
}

func backoffMax(name string, route deliveryRoute) *feature.Feature {
	backoffPolicy := eventingduckv1.BackoffPolicyExponential
	return newDeliveryFeature(name, "backoff-max", route, retryBehavior{
		retries:      4,
		responseCode: http.StatusServiceUnavailable,
		delivery: &eventingduckv1.DeliverySpec{
			Retry:         ptr.To(int32(4)),
			BackoffPolicy: &backoffPolicy,
			BackoffDelay:  ptr.To("PT1S"),
			BackoffMax:    ptr.To("PT2S"),
		},
		expectedIntervals: []time.Duration{time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second},
		rejectedStep:      "receiver rejects the first four deliveries",
		receivedStep:      "receiver accepts the fifth delivery",
		timingStep:        "retry delay stops growing at two seconds",
	})
}
