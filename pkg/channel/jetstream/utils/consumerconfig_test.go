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
	"net/http"
	"testing"
	"time"

	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	"knative.dev/eventing/pkg/kncloudevents"
)

func TestCalculateNakDelayForRetryNumber(t *testing.T) {
	delay := "PT1S"
	backoffMax := "PT2S"
	retryAfterMax := "PT3S"
	retryAfterDisabled := "PT0S"
	exponential := eventingduckv1.BackoffPolicyExponential
	linear := eventingduckv1.BackoffPolicyLinear

	newConfig := func(policy eventingduckv1.BackoffPolicyType, max, retryAfterMax *string) *kncloudevents.RetryConfig {
		t.Helper()
		config, err := kncloudevents.RetryConfigFromDeliverySpec(eventingduckv1.DeliverySpec{
			BackoffPolicy: &policy,
			BackoffDelay:  &delay,
			BackoffMax:    max,
			RetryAfterMax: retryAfterMax,
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
		response     *http.Response
		want         time.Duration
	}{
		{name: "nil config", numDelivered: 1, want: 0},
		{name: "config without backoff", numDelivered: 1, config: &kncloudevents.RetryConfig{}, want: 0},
		{name: "first exponential retry", numDelivered: 1, config: newConfig(exponential, &backoffMax, nil), want: time.Second},
		{name: "second exponential retry", numDelivered: 2, config: newConfig(exponential, &backoffMax, nil), want: 2 * time.Second},
		{name: "capped exponential retry", numDelivered: 4, config: newConfig(exponential, &backoffMax, nil), want: 2 * time.Second},
		{name: "first linear retry", numDelivered: 1, config: newConfig(linear, &backoffMax, nil), want: 0},
		{name: "second linear retry", numDelivered: 2, config: newConfig(linear, &backoffMax, nil), want: time.Second},
		{name: "capped linear retry", numDelivered: 4, config: newConfig(linear, &backoffMax, nil), want: 2 * time.Second},
		{name: "zero delivery number is clamped", numDelivered: 0, config: newConfig(exponential, &backoffMax, nil), want: time.Second},
		{name: "negative delivery number is clamped", numDelivered: -1, config: newConfig(exponential, &backoffMax, nil), want: time.Second},
		{name: "huge retry stays capped", numDelivered: int(^uint(0) >> 1), config: newConfig(exponential, &backoffMax, nil), want: 2 * time.Second},
		{
			name:         "429 retry after is capped independently",
			numDelivered: 1,
			config:       newConfig(exponential, &backoffMax, &retryAfterMax),
			response:     retryAfterResponse(http.StatusTooManyRequests, "5"),
			want:         3 * time.Second,
		},
		{
			name:         "503 retry after is honored",
			numDelivered: 1,
			config:       newConfig(exponential, &backoffMax, &retryAfterMax),
			response:     retryAfterResponse(http.StatusServiceUnavailable, "2"),
			want:         2 * time.Second,
		},
		{
			name:         "backoff max does not cap retry after",
			numDelivered: 1,
			config:       newConfig(exponential, &backoffMax, &retryAfterMax),
			response:     retryAfterResponse(http.StatusTooManyRequests, "3"),
			want:         3 * time.Second,
		},
		{
			name:         "larger normal backoff wins",
			numDelivered: 4,
			config:       newConfig(exponential, nil, &retryAfterMax),
			response:     retryAfterResponse(http.StatusTooManyRequests, "2"),
			want:         8 * time.Second,
		},
		{
			name:         "retry after is ignored without opt in",
			numDelivered: 1,
			config:       newConfig(exponential, &backoffMax, nil),
			response:     retryAfterResponse(http.StatusTooManyRequests, "5"),
			want:         time.Second,
		},
		{
			name:         "zero retry after max opts out",
			numDelivered: 1,
			config:       newConfig(exponential, &backoffMax, &retryAfterDisabled),
			response:     retryAfterResponse(http.StatusTooManyRequests, "5"),
			want:         time.Second,
		},
		{
			name:         "retry after is ignored for other response codes",
			numDelivered: 1,
			config:       newConfig(exponential, &backoffMax, &retryAfterMax),
			response:     retryAfterResponse(http.StatusInternalServerError, "5"),
			want:         time.Second,
		},
		{
			name:         "invalid retry after falls back to normal backoff",
			numDelivered: 1,
			config:       newConfig(exponential, &backoffMax, &retryAfterMax),
			response:     retryAfterResponse(http.StatusTooManyRequests, "invalid"),
			want:         time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CalculateNakDelayForRetryNumber(tt.numDelivered, tt.config, tt.response); got != tt.want {
				t.Errorf("CalculateNakDelayForRetryNumber() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCalculateNakDelayForRetryNumberWithHTTPDate(t *testing.T) {
	delay := "PT1S"
	retryAfterMax := "PT10S"
	exponential := eventingduckv1.BackoffPolicyExponential
	config, err := kncloudevents.RetryConfigFromDeliverySpec(eventingduckv1.DeliverySpec{
		BackoffPolicy: &exponential,
		BackoffDelay:  &delay,
		RetryAfterMax: &retryAfterMax,
	})
	if err != nil {
		t.Fatal(err)
	}

	response := retryAfterResponse(http.StatusServiceUnavailable, time.Now().Add(5*time.Second).UTC().Format(http.TimeFormat))
	got := CalculateNakDelayForRetryNumber(1, &config, response)
	if got < 3*time.Second || got > 5*time.Second {
		t.Fatalf("CalculateNakDelayForRetryNumber() = %v, want between 3s and 5s", got)
	}
}

func retryAfterResponse(status int, retryAfter string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Retry-After": []string{retryAfter}},
	}
}
