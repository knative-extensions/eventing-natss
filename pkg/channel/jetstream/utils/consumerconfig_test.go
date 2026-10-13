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
	"context"
	"fmt"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/go-retryablehttp"
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
		{name: "capped linear retry", numDelivered: 4, config: newConfig(linear, &backoffMax, nil), want: 2 * time.Second},
		{name: "zero delivery number is clamped", numDelivered: 0, config: newConfig(exponential, &backoffMax, nil), want: time.Second},
		{name: "negative delivery number is clamped", numDelivered: -1, config: newConfig(exponential, &backoffMax, nil), want: time.Second},
		{name: "huge retry stays capped", numDelivered: int(^uint(0) >> 1), config: newConfig(exponential, &backoffMax, nil), want: 2 * time.Second},
		{name: "huge linear retry stays capped", numDelivered: int(^uint(0) >> 1), config: newConfig(linear, &backoffMax, nil), want: 2 * time.Second},
		{
			name:         "linear retry after is capped independently",
			numDelivered: 1,
			config:       newConfig(linear, &backoffMax, &retryAfterMax),
			response:     retryAfterResponse(http.StatusTooManyRequests, "5"),
			want:         3 * time.Second,
		},
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

func TestCalculateNakDelayMatchesHTTPRetryWaits(t *testing.T) {
	const retries = 4

	limits := []struct {
		name          string
		backoffMax    *string
		retryAfterMax *string
	}{
		{name: "no limits"},
		{name: "backoff max", backoffMax: ptrTo("PT2S")},
		{name: "backoff max below delay", backoffMax: ptrTo("PT0.5S")},
		{name: "retry after max", backoffMax: ptrTo("PT2S"), retryAfterMax: ptrTo("PT3S")},
	}
	failures := []struct {
		name       string
		status     int
		retryAfter string
	}{
		{name: "500", status: http.StatusInternalServerError},
		{name: "503 retry after 2s", status: http.StatusServiceUnavailable, retryAfter: "2"},
		{name: "429 retry after 5s", status: http.StatusTooManyRequests, retryAfter: "5"},
	}
	policies := []eventingduckv1.BackoffPolicyType{
		eventingduckv1.BackoffPolicyExponential,
		eventingduckv1.BackoffPolicyLinear,
	}

	for _, policy := range policies {
		for _, limit := range limits {
			for _, failure := range failures {
				t.Run(fmt.Sprintf("%s %s %s", policy, limit.name, failure.name), func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						config, err := kncloudevents.RetryConfigFromDeliverySpec(eventingduckv1.DeliverySpec{
							Retry:         ptrTo(int32(retries)),
							BackoffPolicy: &policy,
							BackoffDelay:  ptrTo("PT1S"),
							BackoffMax:    limit.backoffMax,
							RetryAfterMax: limit.retryAfterMax,
						})
						if err != nil {
							t.Fatal(err)
						}

						start := time.Now()
						var sentAt []time.Duration
						client := retryablehttp.Client{
							HTTPClient: &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
								sentAt = append(sentAt, time.Since(start))
								resp := &http.Response{
									StatusCode: failure.status,
									Header:     make(http.Header),
									Body:       http.NoBody,
									Request:    req,
								}
								if failure.retryAfter != "" {
									resp.Header.Set("Retry-After", failure.retryAfter)
								}
								return resp, nil
							})},
							RetryMax:   retries,
							CheckRetry: func(context.Context, *http.Response, error) (bool, error) { return true, nil },
							Backoff:    generateBackoffFn(&config),
							ErrorHandler: func(resp *http.Response, err error, _ int) (*http.Response, error) {
								return resp, err
							},
						}
						req, err := retryablehttp.NewRequest(http.MethodPost, "http://subscriber.example.com", nil)
						if err != nil {
							t.Fatal(err)
						}
						resp, err := client.Do(req)
						if err != nil {
							t.Fatal(err)
						}
						resp.Body.Close()

						if len(sentAt) != retries+1 {
							t.Fatalf("sent %d requests, want %d", len(sentAt), retries+1)
						}
						response := &http.Response{StatusCode: failure.status, Header: resp.Header}
						for retry := 1; retry <= retries; retry++ {
							wantWait := sentAt[retry] - sentAt[retry-1]
							if got := CalculateNakDelayForRetryNumber(retry, &config, response); got != wantWait {
								t.Errorf("NAK delay before retry %d = %v, but the HTTP retry loop waited %v", retry, got, wantWait)
							}
						}
					})
				})
			}
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func ptrTo[T any](v T) *T {
	return &v
}

func retryAfterResponse(status int, retryAfter string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Retry-After": []string{retryAfter}},
	}
}
