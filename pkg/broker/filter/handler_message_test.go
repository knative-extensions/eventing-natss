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
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cejs "github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/logging"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	"knative.dev/eventing/pkg/eventfilter"
	"knative.dev/eventing/pkg/eventingtls"
	"knative.dev/eventing/pkg/kncloudevents"
)

// cleanupTrackingFilter passes every event. Optional fields report filtered
// event IDs, block evaluation until release, and record cleanup.
type cleanupTrackingFilter struct {
	filtered chan string
	release  <-chan struct{}
	cleaned  chan struct{}
}

func (f *cleanupTrackingFilter) Filter(_ context.Context, event cloudevents.Event) eventfilter.FilterResult {
	if f.filtered != nil {
		f.filtered <- event.ID()
	}
	if f.release != nil {
		<-f.release
	}
	return eventfilter.PassFilter
}

func (f *cleanupTrackingFilter) Cleanup() {
	close(f.cleaned)
}

// makeStructuredCEMsg constructs a nats.Msg carrying a structured CloudEvent.
// The message header contains "Content-Type: application/cloudevents+json"
// so cejs.NewMessage returns EncodingStructured and the body is the CE JSON.
func makeStructuredCEMsg(eventType, source, id string) *nats.Msg {
	body := `{"specversion":"1.0","type":"` + eventType + `","source":"` + source + `","id":"` + id + `"}`
	return &nats.Msg{
		Subject: "test.subject",
		Header:  nats.Header{"Content-Type": []string{"application/cloudevents+json"}},
		Data:    []byte(body),
	}
}

// makeTrigger builds a minimal trigger with an optional attribute filter.
// filterType may be empty ("") to disable the attribute filter.
func makeTrigger(namespace, name, filterType string) *eventingv1.Trigger {
	t := &eventingv1.Trigger{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: eventingv1.TriggerSpec{
			Broker: "test-broker",
		},
	}
	if filterType != "" {
		t.Spec.Filter = &eventingv1.TriggerFilter{
			Attributes: map[string]string{"type": filterType},
		}
	}
	return t
}

// newTestDispatcher creates a dispatcher suitable for unit tests.
// Passing nil OIDC token provider is fine for tests — OIDC token injection
// is only triggered when the destination requires it.
func newTestDispatcher(_ context.Context) *kncloudevents.Dispatcher {
	return kncloudevents.NewDispatcher(eventingtls.ClientConfig{}, nil)
}

// newTestHandler creates a TriggerHandler wired to the given subscriber URL.
func newTestHandler(t *testing.T, ctx context.Context, subscriberURL string, filterType string) *TriggerHandler {
	t.Helper()
	u, err := apis.ParseURL(subscriberURL)
	if err != nil {
		t.Fatalf("ParseURL(%q): %v", subscriberURL, err)
	}
	subscriber := duckv1.Addressable{URL: u}
	trigger := makeTrigger("default", "test-trigger", filterType)
	dispatcher := newTestDispatcher(ctx)
	h, err := NewTriggerHandler(ctx, trigger, subscriber, nil, nil, nil, nil, dispatcher, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewTriggerHandler: %v", err)
	}
	return h
}

// setTestFilter swaps filter into h's configuration the way Update does,
// keeping the rest of the configuration.
func setTestFilter(h *TriggerHandler, filter eventfilter.Filter) {
	h.configMu.RLock()
	next := *h.config
	h.configMu.RUnlock()
	next.filter = filter
	h.replaceConfig(&next)
}

// logCtx returns a context carrying a no-op zap logger.
func logCtx() context.Context {
	return logging.WithLogger(context.Background(), zap.NewNop().Sugar())
}

// TestTriggerHandlerUpdatePreservesInflightSnapshot verifies that an update is
// an atomic handoff between immutable handler configurations. Update waits for
// an old filter invocation before cleaning that filter, but does not wait for
// the old HTTP dispatch; that dispatch retains the old subscriber snapshot.
func TestTriggerHandlerUpdatePreservesInflightSnapshot(t *testing.T) {
	ctx := logCtx()

	oldRequests := make(chan string, 1)
	releaseOldSubscriber := make(chan struct{})
	oldSubscriber := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oldRequests <- r.Header.Get("Ce-Id")
		<-releaseOldSubscriber
		w.WriteHeader(http.StatusNoContent)
	}))
	defer oldSubscriber.Close()
	releaseOld := sync.OnceFunc(func() { close(releaseOldSubscriber) })
	defer releaseOld()

	newRequests := make(chan string, 1)
	newSubscriber := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newRequests <- r.Header.Get("Ce-Id")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer newSubscriber.Close()

	handler := newTestHandler(t, ctx, oldSubscriber.URL, "")
	defer handler.Cleanup()
	releaseOldFilter := make(chan struct{})
	oldFilter := &cleanupTrackingFilter{
		filtered: make(chan string, 2),
		release:  releaseOldFilter,
		cleaned:  make(chan struct{}),
	}
	setTestFilter(handler, oldFilter)

	dispatchDone := make(chan struct{})
	go func() {
		handler.HandleMessage(ctx, makeStructuredCEMsg("old.type", "test/source", "old-event"))
		close(dispatchDone)
	}()

	if got := receiveWithin(t, oldFilter.filtered, "old dispatch never reached the old filter"); got != "old-event" {
		t.Fatalf("old filter saw event %q, want %q", got, "old-event")
	}

	// The old filter is still evaluating, so Update must not be able to replace
	// and clean its config yet.
	if handler.configMu.TryLock() {
		handler.configMu.Unlock()
		t.Fatal("handler config was not protected during filter evaluation")
	}

	newURL, err := apis.ParseURL(newSubscriber.URL)
	if err != nil {
		t.Fatalf("ParseURL(%q): %v", newSubscriber.URL, err)
	}
	updateDone := make(chan struct{})
	go func() {
		handler.Update(
			makeTrigger("default", "test-trigger", ""),
			duckv1.Addressable{URL: newURL},
			nil,
			nil,
			nil,
			nil,
		)
		close(updateDone)
	}()

	// TryRLock fails only once a writer is waiting, so Update has reached the
	// write lock. It must remain blocked, and the old filter must remain live,
	// until Filter returns.
	require.Eventually(t, func() bool {
		if handler.configMu.TryRLock() {
			handler.configMu.RUnlock()
			return false
		}
		return true
	}, 5*time.Second, time.Millisecond, "Update did not reach the write lock")
	select {
	case <-updateDone:
		t.Fatal("Update returned while the old filter was still evaluating")
	case <-oldFilter.cleaned:
		t.Fatal("old filter was cleaned up while it was still evaluating")
	default:
	}

	// Once filtering finishes, the old request proceeds using its immutable
	// subscriber snapshot. The HTTP endpoint intentionally remains blocked.
	close(releaseOldFilter)
	if got := receiveWithin(t, oldRequests, "old dispatch never reached the old subscriber"); got != "old-event" {
		t.Fatalf("old subscriber saw event %q, want %q", got, "old-event")
	}
	receiveWithin(t, updateDone, "Update did not finish after old filter evaluation completed")
	receiveWithin(t, oldFilter.cleaned, "old filter was not cleaned up after its evaluation completed")
	select {
	case <-dispatchDone:
		t.Fatal("old dispatch finished before its HTTP subscriber was released")
	default:
	}

	// A subsequent request may use the new configuration while the old HTTP
	// request is still in flight. It must not reuse the old filter or subscriber.
	handler.HandleMessage(ctx, makeStructuredCEMsg("new.type", "test/source", "new-event"))
	if got := receiveWithin(t, newRequests, "next dispatch did not use the new subscriber config"); got != "new-event" {
		t.Fatalf("new subscriber saw event %q, want %q", got, "new-event")
	}
	select {
	case got := <-oldFilter.filtered:
		t.Fatalf("old filter was reused by the next dispatch for event %q", got)
	default:
	}
	select {
	case got := <-oldRequests:
		t.Fatalf("old subscriber unexpectedly received another event %q", got)
	default:
	}

	releaseOld()
	receiveWithin(t, dispatchDone, "old dispatch did not finish after subscriber release")
}

// TestHandleMessage_BadData verifies that a message with structured encoding
// but unparseable data (no valid JSON) is terminated and the subscriber is not called.
func TestHandleMessage_BadData(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := logCtx()
	handler := newTestHandler(t, ctx, srv.URL, "")

	// Structured encoding with non-JSON body — cejs.NewMessage returns EncodingStructured
	// but binding.ToEvent will fail to parse it. Handler calls msg.Term() and returns.
	msg := &nats.Msg{
		Subject: "test.subject",
		Header:  nil, // nil header → EncodingStructured
		Data:    []byte("not json at all"),
	}

	handler.HandleMessage(ctx, msg)

	if called {
		t.Error("subscriber should NOT be called when CE conversion fails")
	}
}

// TestHandleMessage_FilteredOut verifies that a message whose event type does not
// match the trigger filter is acked (not dispatched to the subscriber).
func TestHandleMessage_FilteredOut(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := logCtx()
	// Trigger has filter for "other.type" but message is "test.type" — should be filtered out.
	handler := newTestHandler(t, ctx, srv.URL, "other.type")

	msg := makeStructuredCEMsg("test.type", "test/source", "test-id-1")
	handler.HandleMessage(ctx, msg)

	if called {
		t.Error("subscriber should NOT be called for filtered-out messages")
	}
}

// TestHandleMessage_Dispatch_Success verifies that 2xx responses cause a successful dispatch.
func TestHandleMessage_Dispatch_Success(t *testing.T) {
	codes := []int{http.StatusOK, http.StatusAccepted}
	for _, code := range codes {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			var count int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt64(&count, 1)
				w.WriteHeader(code)
			}))
			defer srv.Close()

			ctx := logCtx()
			handler := newTestHandler(t, ctx, srv.URL, "")

			msg := makeStructuredCEMsg("test.type", "test/source", "test-id-2xx")
			handler.HandleMessage(ctx, msg)

			if got := atomic.LoadInt64(&count); got != 1 {
				t.Errorf("subscriber called %d times, want 1", got)
			}
		})
	}
}

// TestHandleMessage_Dispatch_RetriableError verifies that 5xx/429 responses
// reach the subscriber and nack is attempted (fails gracefully without a real NATS conn).
func TestHandleMessage_Dispatch_RetriableError(t *testing.T) {
	codes := []int{
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
		http.StatusTooManyRequests,
	}
	for _, code := range codes {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			var count int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt64(&count, 1)
				w.WriteHeader(code)
			}))
			defer srv.Close()

			ctx := logCtx()
			handler := newTestHandler(t, ctx, srv.URL, "")

			msg := makeStructuredCEMsg("test.type", "test/source", "test-id-5xx")
			// NakWithDelay will fail with ErrMsgNoReply — handler logs and continues.
			handler.HandleMessage(ctx, msg)

			if got := atomic.LoadInt64(&count); got != 1 {
				t.Errorf("subscriber called %d times, want 1", got)
			}
		})
	}
}

// TestHandleMessage_Dispatch_NonRetriable verifies that 4xx responses
// reach the subscriber and term is attempted (fails gracefully without a real NATS conn).
func TestHandleMessage_Dispatch_NonRetriable(t *testing.T) {
	codes := []int{
		http.StatusBadRequest,
		http.StatusForbidden,
		http.StatusNotFound,
	}
	for _, code := range codes {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			var count int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt64(&count, 1)
				w.WriteHeader(code)
			}))
			defer srv.Close()

			ctx := logCtx()
			handler := newTestHandler(t, ctx, srv.URL, "")

			msg := makeStructuredCEMsg("test.type", "test/source", "test-id-4xx")
			// Term will fail with ErrMsgNoReply — handler logs and continues.
			handler.HandleMessage(ctx, msg)

			if got := atomic.LoadInt64(&count); got != 1 {
				t.Errorf("subscriber called %d times, want 1", got)
			}
		})
	}
}

// TestHandleMessage_CancelledContext verifies that when the context is already
// cancelled before HandleMessage, the dispatch is cancelled and no panic occurs.
func TestHandleMessage_CancelledContext(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// Block until context is done — simulates the cancelled case.
		<-r.Context().Done()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx := logCtx()
	handler := newTestHandler(t, ctx, srv.URL, "")

	// Cancel the context before calling HandleMessage.
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()

	msg := makeStructuredCEMsg("test.type", "test/source", "test-id-cancelled")

	// Should not panic; eventProcessingDeadlineExceeded fires and returns early.
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.HandleMessage(cancelCtx, msg)
	}()

	select {
	case <-done:
		// Good — no hang.
	case <-time.After(5 * time.Second):
		t.Fatal("HandleMessage did not return within 5 seconds with cancelled context")
	}
	_ = called // subscriber may or may not be called depending on timing; we just assert no hang
}

// TestTransform_NoType covers the branch where GetAttribute returns nil for the
// CloudEvent type (e.g., the attribute is not set), so the inner if-block is skipped.
// This covers the previously uncovered "ty == nil → skip" branch.
func TestTransform_NoType(t *testing.T) {
	// Use a cejs.Message wrapping a *nats.Msg with binary encoding but no ce-type header.
	// ce-specversion present → binary encoding; ce-type absent → GetAttribute(spec.Type) returns nil.
	msg := &nats.Msg{
		Subject: "test.subject",
		Header: nats.Header{
			"Ce-Specversion": []string{"1.0"},
			"Ce-Source":      []string{"test/source"},
			"Ce-Id":          []string{"test-id-notype"},
			// "Ce-Type" intentionally absent
		},
		Data: []byte(`{}`),
	}

	import_cejs := cejs.NewMessage(msg)

	te := TypeExtractorTransformer("initial")
	err := te.Transform(import_cejs, nil)
	if err != nil {
		t.Fatalf("Transform() unexpected error: %v", err)
	}
	// No type header → TypeExtractorTransformer should remain unchanged.
	if string(te) != "initial" {
		t.Errorf("TypeExtractorTransformer = %q, want %q (should be unchanged when type is absent)", string(te), "initial")
	}
}
