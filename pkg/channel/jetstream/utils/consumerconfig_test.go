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

func TestCalculateNakDelayUsesZeroBasedAttempt(t *testing.T) {
	delay := "PT1S"
	exponential := eventingduckv1.BackoffPolicyExponential
	linear := eventingduckv1.BackoffPolicyLinear

	newConfig := func(policy eventingduckv1.BackoffPolicyType) *kncloudevents.RetryConfig {
		t.Helper()
		return &kncloudevents.RetryConfig{
			BackoffPolicy: &policy,
			BackoffDelay:  &delay,
		}
	}

	tests := []struct {
		name         string
		numDelivered int
		config       *kncloudevents.RetryConfig
		want         time.Duration
	}{
		{name: "first exponential retry", numDelivered: 1, config: newConfig(exponential), want: time.Second},
		{name: "second exponential retry", numDelivered: 2, config: newConfig(exponential), want: 2 * time.Second},
		{name: "fourth exponential retry", numDelivered: 4, config: newConfig(exponential), want: 8 * time.Second},
		{name: "first linear retry", numDelivered: 1, config: newConfig(linear), want: 0},
		{name: "second linear retry", numDelivered: 2, config: newConfig(linear), want: time.Second},
		{name: "fourth linear retry", numDelivered: 4, config: newConfig(linear), want: 3 * time.Second},
		{name: "zero delivery number is clamped", numDelivered: 0, config: newConfig(exponential), want: time.Second},
		{name: "negative delivery number is clamped", numDelivered: -1, config: newConfig(exponential), want: time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CalculateNakDelayForRetryNumber(tt.numDelivered, tt.config); got != tt.want {
				t.Errorf("CalculateNakDelayForRetryNumber() = %v, want %v", got, tt.want)
			}
		})
	}
}
