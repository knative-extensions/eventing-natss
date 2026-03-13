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

package filter

import (
	"context"
	"errors"
	"net/http"
	"testing"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/protocol"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/logging"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	"knative.dev/eventing/pkg/eventfilter"
	"knative.dev/eventing/pkg/kncloudevents"
)

func TestDetermineNatsResult(t *testing.T) {
	tests := []struct {
		name         string
		responseCode int
		err          error
		wantACK      bool
		wantNACK     bool
	}{
		{
			name:         "success - no error",
			responseCode: http.StatusOK,
			err:          nil,
			wantACK:      true,
			wantNACK:     false,
		},
		{
			name:         "success - 2xx response",
			responseCode: http.StatusAccepted,
			err:          nil,
			wantACK:      true,
			wantNACK:     false,
		},
		{
			name:         "retriable - 500 error",
			responseCode: http.StatusInternalServerError,
			err:          errors.New("server error"),
			wantACK:      false,
			wantNACK:     true,
		},
		{
			name:         "retriable - 502 bad gateway",
			responseCode: http.StatusBadGateway,
			err:          errors.New("bad gateway"),
			wantACK:      false,
			wantNACK:     true,
		},
		{
			name:         "retriable - 503 service unavailable",
			responseCode: http.StatusServiceUnavailable,
			err:          errors.New("service unavailable"),
			wantACK:      false,
			wantNACK:     true,
		},
		{
			name:         "retriable - 504 gateway timeout",
			responseCode: http.StatusGatewayTimeout,
			err:          errors.New("gateway timeout"),
			wantACK:      false,
			wantNACK:     true,
		},
		{
			name:         "retriable - 429 too many requests",
			responseCode: http.StatusTooManyRequests,
			err:          errors.New("too many requests"),
			wantACK:      false,
			wantNACK:     true,
		},
		{
			name:         "retriable - 408 request timeout",
			responseCode: http.StatusRequestTimeout,
			err:          errors.New("request timeout"),
			wantACK:      false,
			wantNACK:     true,
		},
		{
			name:         "non-retriable - 400 bad request",
			responseCode: http.StatusBadRequest,
			err:          errors.New("bad request"),
			wantACK:      false,
			wantNACK:     false,
		},
		{
			name:         "non-retriable - 401 unauthorized",
			responseCode: http.StatusUnauthorized,
			err:          errors.New("unauthorized"),
			wantACK:      false,
			wantNACK:     false,
		},
		{
			name:         "non-retriable - 403 forbidden",
			responseCode: http.StatusForbidden,
			err:          errors.New("forbidden"),
			wantACK:      false,
			wantNACK:     false,
		},
		{
			name:         "non-retriable - 404 not found",
			responseCode: http.StatusNotFound,
			err:          errors.New("not found"),
			wantACK:      false,
			wantNACK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := determineNatsResult(tt.responseCode, tt.err)

			isACK := protocol.IsACK(result)
			isNACK := protocol.IsNACK(result)

			if isACK != tt.wantACK {
				t.Errorf("IsACK() = %v, want %v", isACK, tt.wantACK)
			}

			if isNACK != tt.wantNACK {
				t.Errorf("IsNACK() = %v, want %v", isNACK, tt.wantNACK)
			}
		})
	}
}

func TestTypeExtractorTransformer(t *testing.T) {
	// TypeExtractorTransformer is a string type that implements binding.Transformer
	// It extracts the CloudEvent type from a message

	te := TypeExtractorTransformer("")

	// Initial value should be empty
	if string(te) != "" {
		t.Errorf("Initial value = %v, want empty string", string(te))
	}
}

func TestRetryConfigDefaults(t *testing.T) {
	// Verify the default retry configuration values
	if retryMax != 3 {
		t.Errorf("retryMax = %v, want 3", retryMax)
	}

	if retryTimeout != "PT1S" {
		t.Errorf("retryTimeout = %v, want PT1S", retryTimeout)
	}

	if retryBackoffDelay != "PT0.5S" {
		t.Errorf("retryBackoffDelay = %v, want PT0.5S", retryBackoffDelay)
	}
}

func TestDetermineNatsResult_EdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		responseCode int
		err          error
		wantACK      bool
	}{
		{
			name:         "zero response code with no error",
			responseCode: 0,
			err:          nil,
			wantACK:      true,
		},
		{
			name:         "zero response code with error",
			responseCode: 0,
			err:          errors.New("some error"),
			wantACK:      false,
		},
		{
			name:         "1xx informational (no error)",
			responseCode: 100,
			err:          nil,
			wantACK:      true,
		},
		{
			name:         "3xx redirect with error",
			responseCode: 301,
			err:          errors.New("redirect"),
			wantACK:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := determineNatsResult(tt.responseCode, tt.err)

			isACK := protocol.IsACK(result)
			if isACK != tt.wantACK {
				t.Errorf("IsACK() = %v, want %v", isACK, tt.wantACK)
			}
		})
	}
}

func TestBuildTriggerFilter(t *testing.T) {
	logger := zap.NewNop().Sugar()

	tests := []struct {
		name           string
		trigger        *eventingv1.Trigger
		wantNilFilter  bool
		testEvent      cloudevents.Event
		wantFilterPass bool
	}{
		{
			name: "no filter - passes all events",
			trigger: &eventingv1.Trigger{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trigger",
					Namespace: "default",
				},
				Spec: eventingv1.TriggerSpec{
					Broker: "test-broker",
				},
			},
			wantNilFilter:  true,
			wantFilterPass: true,
		},
		{
			name: "legacy filter only - uses attributes filter",
			trigger: &eventingv1.Trigger{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trigger",
					Namespace: "default",
				},
				Spec: eventingv1.TriggerSpec{
					Broker: "test-broker",
					Filter: &eventingv1.TriggerFilter{
						Attributes: map[string]string{
							"type": "test.event.type",
						},
					},
				},
			},
			wantNilFilter: false,
			testEvent: func() cloudevents.Event {
				e := cloudevents.NewEvent()
				e.SetType("test.event.type")
				e.SetSource("test-source")
				return e
			}(),
			wantFilterPass: true,
		},
		{
			name: "legacy filter - event does not match",
			trigger: &eventingv1.Trigger{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trigger",
					Namespace: "default",
				},
				Spec: eventingv1.TriggerSpec{
					Broker: "test-broker",
					Filter: &eventingv1.TriggerFilter{
						Attributes: map[string]string{
							"type": "test.event.type",
						},
					},
				},
			},
			wantNilFilter: false,
			testEvent: func() cloudevents.Event {
				e := cloudevents.NewEvent()
				e.SetType("different.type")
				e.SetSource("test-source")
				return e
			}(),
			wantFilterPass: false,
		},
		{
			name: "new filters take priority over legacy filter",
			trigger: &eventingv1.Trigger{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trigger",
					Namespace: "default",
				},
				Spec: eventingv1.TriggerSpec{
					Broker: "test-broker",
					// New filters - should take priority
					Filters: []eventingv1.SubscriptionsAPIFilter{
						{
							Exact: map[string]string{
								"type": "new.filter.type",
							},
						},
					},
					// Legacy filter - should be ignored
					Filter: &eventingv1.TriggerFilter{
						Attributes: map[string]string{
							"type": "legacy.filter.type",
						},
					},
				},
			},
			wantNilFilter: false,
			testEvent: func() cloudevents.Event {
				e := cloudevents.NewEvent()
				e.SetType("new.filter.type")
				e.SetSource("test-source")
				return e
			}(),
			wantFilterPass: true,
		},
		{
			name: "new filters - event matches legacy but not new filter",
			trigger: &eventingv1.Trigger{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trigger",
					Namespace: "default",
				},
				Spec: eventingv1.TriggerSpec{
					Broker: "test-broker",
					Filters: []eventingv1.SubscriptionsAPIFilter{
						{
							Exact: map[string]string{
								"type": "new.filter.type",
							},
						},
					},
					Filter: &eventingv1.TriggerFilter{
						Attributes: map[string]string{
							"type": "legacy.filter.type",
						},
					},
				},
			},
			wantNilFilter: false,
			testEvent: func() cloudevents.Event {
				e := cloudevents.NewEvent()
				e.SetType("legacy.filter.type") // matches legacy but not new
				e.SetSource("test-source")
				return e
			}(),
			wantFilterPass: false, // new filters take priority, so should fail
		},
		{
			name: "new filters with prefix",
			trigger: &eventingv1.Trigger{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trigger",
					Namespace: "default",
				},
				Spec: eventingv1.TriggerSpec{
					Broker: "test-broker",
					Filters: []eventingv1.SubscriptionsAPIFilter{
						{
							Prefix: map[string]string{
								"type": "test.event.",
							},
						},
					},
				},
			},
			wantNilFilter: false,
			testEvent: func() cloudevents.Event {
				e := cloudevents.NewEvent()
				e.SetType("test.event.created")
				e.SetSource("test-source")
				return e
			}(),
			wantFilterPass: true,
		},
		{
			name: "new filters with suffix",
			trigger: &eventingv1.Trigger{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-trigger",
					Namespace: "default",
				},
				Spec: eventingv1.TriggerSpec{
					Broker: "test-broker",
					Filters: []eventingv1.SubscriptionsAPIFilter{
						{
							Suffix: map[string]string{
								"type": ".created",
							},
						},
					},
				},
			},
			wantNilFilter: false,
			testEvent: func() cloudevents.Event {
				e := cloudevents.NewEvent()
				e.SetType("order.created")
				e.SetSource("test-source")
				return e
			}(),
			wantFilterPass: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := buildTriggerFilter(logger, tt.trigger)

			if tt.wantNilFilter {
				if filter != nil {
					t.Errorf("expected nil filter, got %v", filter)
				}
				return
			}

			if filter == nil {
				t.Fatal("expected non-nil filter")
			}

			// Test the filter with the test event
			result := filter.Filter(context.Background(), tt.testEvent)
			passed := result != eventfilter.FailFilter

			if passed != tt.wantFilterPass {
				t.Errorf("filter result = %v (passed=%v), want passed=%v", result, passed, tt.wantFilterPass)
			}
		})
	}
}

func TestBuildTriggerFilter_NilAttributes(t *testing.T) {
	logger := zap.NewNop().Sugar()

	// Filter set but Attributes nil should fall through to default (nil filter).
	trigger := &eventingv1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: "t", Namespace: "ns"},
		Spec: eventingv1.TriggerSpec{
			Broker: "b",
			Filter: &eventingv1.TriggerFilter{
				Attributes: nil,
			},
		},
	}
	f := buildTriggerFilter(logger, trigger)
	if f != nil {
		t.Error("expected nil filter when Filter is set but Attributes is nil")
	}
}

func TestBuildTriggerFilter_MultipleAttributes(t *testing.T) {
	logger := zap.NewNop().Sugar()

	trigger := &eventingv1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: "t", Namespace: "ns"},
		Spec: eventingv1.TriggerSpec{
			Broker: "b",
			Filter: &eventingv1.TriggerFilter{
				Attributes: map[string]string{
					"type":   "order.created",
					"source": "shop",
				},
			},
		},
	}
	f := buildTriggerFilter(logger, trigger)
	if f == nil {
		t.Fatal("expected non-nil filter")
	}

	t.Run("all attributes match", func(t *testing.T) {
		e := cloudevents.NewEvent()
		e.SetType("order.created")
		e.SetSource("shop")
		if f.Filter(context.Background(), e) == eventfilter.FailFilter {
			t.Error("expected event to pass filter")
		}
	})

	t.Run("partial match fails", func(t *testing.T) {
		e := cloudevents.NewEvent()
		e.SetType("order.created")
		e.SetSource("other-shop")
		if f.Filter(context.Background(), e) != eventfilter.FailFilter {
			t.Error("expected event to fail filter")
		}
	})
}

func TestTypeExtractorTransformerTransform(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		wantType  string
	}{
		{
			name:      "extracts type from event",
			eventType: "com.example.event.v1",
			wantType:  "com.example.event.v1",
		},
		{
			name:      "extracts empty type",
			eventType: "",
			wantType:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := cloudevents.NewEvent()
			e.SetType(tc.eventType)
			e.SetSource("test-source")

			em := binding.EventMessage(e)

			te := TypeExtractorTransformer("")
			err := te.Transform(&em, nil)
			if err != nil {
				t.Fatalf("Transform() unexpected error: %v", err)
			}
			if string(te) != tc.wantType {
				t.Errorf("TypeExtractorTransformer = %q, want %q", string(te), tc.wantType)
			}
		})
	}
}

func TestNewTriggerHandler(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

	trigger := &eventingv1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: "test-trigger", Namespace: "test-ns"},
		Spec:       eventingv1.TriggerSpec{Broker: "test-broker"},
	}

	subscriberURL, _ := apis.ParseURL("http://subscriber.example.com")
	subscriber := duckv1.Addressable{URL: subscriberURL}

	handler, err := NewTriggerHandler(ctx, trigger, subscriber, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewTriggerHandler() unexpected error: %v", err)
	}
	if handler == nil {
		t.Fatal("NewTriggerHandler() returned nil handler")
	}
	if handler.trigger != trigger {
		t.Error("handler.trigger not set correctly")
	}
	if handler.subscriber.URL.String() != subscriber.URL.String() {
		t.Errorf("handler.subscriber URL = %v, want %v", handler.subscriber.URL, subscriber.URL)
	}
	if handler.filter != nil {
		t.Error("handler.filter should be nil for trigger without filter")
	}
	if handler.brokerIngressURL != nil {
		t.Error("handler.brokerIngressURL should be nil when not provided")
	}
	if handler.deadLetterSink != nil {
		t.Error("handler.deadLetterSink should be nil when not provided")
	}
	if handler.retryConfig != nil {
		t.Error("handler.retryConfig should be nil when not provided")
	}
}

func TestNewTriggerHandler_WithOptionalParams(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

	trigger := &eventingv1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: "test-trigger", Namespace: "test-ns"},
		Spec: eventingv1.TriggerSpec{
			Broker: "test-broker",
			Filter: &eventingv1.TriggerFilter{
				Attributes: map[string]string{"type": "test.type"},
			},
		},
	}

	subscriberURL, _ := apis.ParseURL("http://subscriber.example.com")
	subscriber := duckv1.Addressable{URL: subscriberURL}

	ingressURL, _ := apis.ParseURL("http://broker-ingress.example.com")
	brokerIngress := &duckv1.Addressable{URL: ingressURL}

	dlsURL, _ := apis.ParseURL("http://dead-letter.example.com")
	dls := &duckv1.Addressable{URL: dlsURL}

	retryConfig := &kncloudevents.RetryConfig{RetryMax: 3}
	noRetryConfig := &kncloudevents.RetryConfig{RetryMax: 0}

	handler, err := NewTriggerHandler(ctx, trigger, subscriber, brokerIngress, dls, retryConfig, noRetryConfig, nil)
	if err != nil {
		t.Fatalf("NewTriggerHandler() unexpected error: %v", err)
	}
	if handler.filter == nil {
		t.Error("handler.filter should not be nil for trigger with filter")
	}
	if handler.brokerIngressURL != brokerIngress {
		t.Error("handler.brokerIngressURL not set correctly")
	}
	if handler.deadLetterSink != dls {
		t.Error("handler.deadLetterSink not set correctly")
	}
	if handler.retryConfig != retryConfig {
		t.Error("handler.retryConfig not set correctly")
	}
	if handler.noRetryConfig != noRetryConfig {
		t.Error("handler.noRetryConfig not set correctly")
	}
}

func TestTriggerHandlerCleanup(t *testing.T) {
	t.Run("nil filter", func(t *testing.T) {
		h := &TriggerHandler{}
		h.Cleanup() // should not panic
	})

	t.Run("with filter", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		trigger := &eventingv1.Trigger{
			ObjectMeta: metav1.ObjectMeta{Name: "t", Namespace: "ns"},
			Spec: eventingv1.TriggerSpec{
				Broker: "b",
				Filter: &eventingv1.TriggerFilter{
					Attributes: map[string]string{"type": "test"},
				},
			},
		}
		f := buildTriggerFilter(logger, trigger)
		h := &TriggerHandler{filter: f}
		h.Cleanup() // should call f.Cleanup() without error
	})
}

func TestResponseToEvent(t *testing.T) {
	ctx := context.Background()

	t.Run("empty response body returns nil", func(t *testing.T) {
		di := &kncloudevents.DispatchInfo{
			ResponseCode: http.StatusAccepted,
			ResponseBody: nil,
		}
		event, err := responseToEvent(ctx, di)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if event != nil {
			t.Error("expected nil event for empty response body")
		}
	})

	t.Run("empty byte slice returns nil", func(t *testing.T) {
		di := &kncloudevents.DispatchInfo{
			ResponseCode: http.StatusOK,
			ResponseBody: []byte{},
		}
		event, err := responseToEvent(ctx, di)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if event != nil {
			t.Error("expected nil event for empty byte slice response body")
		}
	})

	t.Run("non-cloudevent response body returns nil", func(t *testing.T) {
		di := &kncloudevents.DispatchInfo{
			ResponseCode:   http.StatusOK,
			ResponseHeader: http.Header{"Content-Type": []string{"text/plain"}},
			ResponseBody:   []byte("just a plain text response"),
		}
		event, err := responseToEvent(ctx, di)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if event != nil {
			t.Error("expected nil event for non-CloudEvent response")
		}
	})

	t.Run("structured CloudEvent response is parsed", func(t *testing.T) {
		ceJSON := `{"specversion":"1.0","type":"reply.type","source":"subscriber","id":"reply-1","data":"hello"}`
		di := &kncloudevents.DispatchInfo{
			ResponseCode:   http.StatusOK,
			ResponseHeader: http.Header{"Content-Type": []string{"application/cloudevents+json"}},
			ResponseBody:   []byte(ceJSON),
		}
		event, err := responseToEvent(ctx, di)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if event == nil {
			t.Fatal("expected non-nil event for structured CloudEvent response")
		}
		if event.Type() != "reply.type" {
			t.Errorf("event.Type() = %q, want %q", event.Type(), "reply.type")
		}
		if event.Source() != "subscriber" {
			t.Errorf("event.Source() = %q, want %q", event.Source(), "subscriber")
		}
		if event.ID() != "reply-1" {
			t.Errorf("event.ID() = %q, want %q", event.ID(), "reply-1")
		}
	})

	t.Run("binary CloudEvent response is parsed", func(t *testing.T) {
		di := &kncloudevents.DispatchInfo{
			ResponseCode: http.StatusOK,
			ResponseHeader: http.Header{
				"Content-Type":   []string{"application/json"},
				"Ce-Specversion": []string{"1.0"},
				"Ce-Type":        []string{"binary.reply"},
				"Ce-Source":      []string{"subscriber"},
				"Ce-Id":          []string{"reply-2"},
			},
			ResponseBody: []byte(`{"key":"value"}`),
		}
		event, err := responseToEvent(ctx, di)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if event == nil {
			t.Fatal("expected non-nil event for binary CloudEvent response")
		}
		if event.Type() != "binary.reply" {
			t.Errorf("event.Type() = %q, want %q", event.Type(), "binary.reply")
		}
	})
}

func TestDefaultRetryConfigInitialized(t *testing.T) {
	if defaultRetry.RetryMax != int(retryMax) {
		t.Errorf("defaultRetry.RetryMax = %v, want %v", defaultRetry.RetryMax, retryMax)
	}
	if defaultRetry.Backoff == nil {
		t.Error("defaultRetry.Backoff should not be nil")
	}
}
