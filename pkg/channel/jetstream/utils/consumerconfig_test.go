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
	"time"

	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	"knative.dev/eventing/pkg/kncloudevents"
)

func TestCalculateNakDelayForRetryNumber(t *testing.T) {
	delay := "PT1S"
	backoffMax := "PT2S"
	exponential := eventingduckv1.BackoffPolicyExponential
	linear := eventingduckv1.BackoffPolicyLinear

	newConfig := func(policy eventingduckv1.BackoffPolicyType, max *string) *kncloudevents.RetryConfig {
		t.Helper()
		config, err := kncloudevents.RetryConfigFromDeliverySpec(eventingduckv1.DeliverySpec{
			BackoffPolicy: &policy,
			BackoffDelay:  &delay,
			BackoffMax:    max,
		})
		if err != nil {
			t.Fatal(err)
		}
		return &config
	}

	tests := []struct {
		name         string
		numDelivered int
		config       *kncloudevents.RetryConfig
		want         time.Duration
	}{
		{name: "nil config", numDelivered: 1, want: 0},
		{name: "config without backoff", numDelivered: 1, config: &kncloudevents.RetryConfig{}, want: 0},
		{name: "first exponential retry", numDelivered: 1, config: newConfig(exponential, &backoffMax), want: time.Second},
		{name: "second exponential retry", numDelivered: 2, config: newConfig(exponential, &backoffMax), want: 2 * time.Second},
		{name: "capped exponential retry", numDelivered: 4, config: newConfig(exponential, &backoffMax), want: 2 * time.Second},
		{name: "first linear retry", numDelivered: 1, config: newConfig(linear, &backoffMax), want: 0},
		{name: "second linear retry", numDelivered: 2, config: newConfig(linear, &backoffMax), want: time.Second},
		{name: "capped linear retry", numDelivered: 4, config: newConfig(linear, &backoffMax), want: 2 * time.Second},
		{name: "zero delivery number is clamped", numDelivered: 0, config: newConfig(exponential, &backoffMax), want: time.Second},
		{name: "negative delivery number is clamped", numDelivered: -1, config: newConfig(exponential, &backoffMax), want: time.Second},
		{name: "huge retry stays capped", numDelivered: int(^uint(0) >> 1), config: newConfig(exponential, &backoffMax), want: 2 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CalculateNakDelayForRetryNumber(tt.numDelivered, tt.config); got != tt.want {
				t.Errorf("CalculateNakDelayForRetryNumber() = %v, want %v", got, tt.want)
			}
		})
	}
}
