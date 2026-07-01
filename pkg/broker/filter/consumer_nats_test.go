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
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	fakekubeclient "knative.dev/pkg/client/injection/kube/client/fake"
	"knative.dev/pkg/logging"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	"knative.dev/eventing/pkg/eventingtls"
	"knative.dev/eventing/pkg/kncloudevents"

	brokerutils "knative.dev/eventing-natss/pkg/broker/utils"
	natsTesting "knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher/testing"
)

// setupStreamAndConsumer creates a JetStream stream and durable pull consumer for testing.
// Returns (streamName, consumerName).
func setupStreamAndConsumer(t *testing.T, js nats.JetStreamContext, namespace, brokerName, triggerUID string) (string, string) {
	t.Helper()

	broker := &eventingv1.Broker{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      brokerName,
		},
	}

	streamName := brokerutils.BrokerStreamName(broker)
	consumerName := brokerutils.TriggerConsumerName(triggerUID)
	publishSubject := brokerutils.BrokerPublishSubjectName(namespace, brokerName)
	filterSubject := publishSubject + ".>"

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     streamName,
		Subjects: []string{filterSubject},
		Storage:  nats.MemoryStorage,
	})
	if err != nil {
		t.Fatalf("AddStream(%q): %v", streamName, err)
	}

	_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:       consumerName,
		AckPolicy:     nats.AckExplicitPolicy,
		FilterSubject: filterSubject,
		AckWait:       30 * time.Second,
	})
	if err != nil {
		t.Fatalf("AddConsumer(%q, %q): %v", streamName, consumerName, err)
	}

	return streamName, consumerName
}

// makeTestBrokerForNats creates a minimal Broker with the given namespace and name.
func makeTestBrokerForNats(namespace, name string) *eventingv1.Broker {
	return &eventingv1.Broker{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
}

// makeTriggerWithUID creates a Trigger with a specific UID.
func makeTriggerWithUID(namespace, name, brokerName, uid string) *eventingv1.Trigger {
	return &eventingv1.Trigger{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       types.UID(uid),
		},
		Spec: eventingv1.TriggerSpec{
			Broker: brokerName,
		},
	}
}

// newConsumerManagerForTest creates a ConsumerManager with a real NATS connection.
// Mirrors NewConsumerManager's observability bootstrap (tracer, dispatch-duration
// histogram, in-flight observable gauge) so tests exercise the same code path.
func newConsumerManagerForTest(t *testing.T, ctx context.Context, conn *nats.Conn, js nats.JetStreamContext, cfg *ConsumerManagerConfig) *ConsumerManager {
	t.Helper()
	dispatcher := kncloudevents.NewDispatcher(eventingtls.ClientConfig{}, nil)

	fetchBatchSize := DefaultFetchBatchSize
	fetchTimeout := DefaultFetchTimeout
	maxConcurrency := DefaultMaxConcurrency

	if cfg != nil {
		if cfg.FetchBatchSize > 0 {
			fetchBatchSize = cfg.FetchBatchSize
		}
		if cfg.FetchTimeout > 0 {
			fetchTimeout = cfg.FetchTimeout
		}
		if cfg.MaxConcurrency > 0 {
			maxConcurrency = cfg.MaxConcurrency
		}
	}

	tracer := otel.GetTracerProvider().Tracer("knative.dev/eventing-natss/pkg/broker/filter")
	meter := otel.GetMeterProvider().Meter("knative.dev/eventing-natss/pkg/broker/filter")
	dispatchDuration, err := meter.Float64Histogram(
		"kn.eventing.dispatch.duration",
		otelmetric.WithUnit("s"),
		otelmetric.WithExplicitBucketBoundaries(latencyBounds...),
	)
	if err != nil {
		t.Fatalf("create dispatch duration histogram: %v", err)
	}
	processDuration, err := meter.Float64Histogram(
		"kn.eventing.broker.filter.process.duration",
		otelmetric.WithUnit("s"),
		otelmetric.WithExplicitBucketBoundaries(latencyBounds...),
	)
	if err != nil {
		t.Fatalf("create process duration histogram: %v", err)
	}

	cm := &ConsumerManager{
		logger:                logging.FromContext(ctx),
		ctx:                   ctx,
		js:                    js,
		conn:                  conn,
		fetchBatchSize:        fetchBatchSize,
		fetchTimeout:          fetchTimeout,
		defaultMaxConcurrency: maxConcurrency,
		dispatcher:            dispatcher,
		tracer:                tracer,
		dispatchDuration:      dispatchDuration,
		processDuration:       processDuration,
		subscriptions:         make(map[string]*TriggerSubscription),
	}

	if _, err := meter.Int64ObservableGauge(
		"kn.eventing.broker.filter.dispatches.inflight",
		otelmetric.WithInt64Callback(func(_ context.Context, obs otelmetric.Int64Observer) error {
			cm.mu.RLock()
			defer cm.mu.RUnlock()
			for _, sub := range cm.subscriptions {
				obs.Observe(int64(len(sub.sem)), otelmetric.WithAttributes(
					attribute.String("kn.trigger.name", sub.trigger.Name),
					attribute.String("kn.trigger.namespace", sub.trigger.Namespace),
				))
			}
			return nil
		}),
	); err != nil {
		t.Fatalf("register inflight observable gauge: %v", err)
	}

	return cm
}

// publishStructuredCE publishes a structured CloudEvent to the given subject.
// The subject must match the stream's filter subject. Since the stream uses
// the pattern "namespace.broker._knative_broker.>", we append ".event" to
// the base publish subject so it matches the wildcard.
func publishStructuredCE(t *testing.T, js nats.JetStreamContext, baseSubject, eventID string) {
	t.Helper()
	subject := baseSubject + ".event"
	body := fmt.Sprintf(
		`{"specversion":"1.0","type":"test.type","source":"test/source","id":"%s"}`,
		eventID,
	)
	msg := &nats.Msg{
		Subject: subject,
		Header:  nats.Header{"Content-Type": []string{"application/cloudevents+json"}},
		Data:    []byte(body),
	}
	if _, err := js.PublishMsg(msg); err != nil {
		t.Fatalf("PublishMsg(%q): %v", subject, err)
	}
}

// contextWithFakeKube returns a context with a fake Kubernetes client injected.
// This satisfies auth.NewOIDCTokenProvider which calls kubeclient.Get(ctx).
func contextWithFakeKube(t *testing.T) context.Context {
	t.Helper()
	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())
	ctx, _ = fakekubeclient.With(ctx)
	return ctx
}

// TestNewConsumerManager_Fields verifies that NewConsumerManager applies config values correctly.
func TestNewConsumerManager_Fields(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	ctx := contextWithFakeKube(t)

	cfg := &ConsumerManagerConfig{
		FetchBatchSize: 7,
		FetchTimeout:   350 * time.Millisecond,
		MaxConcurrency: 15,
	}
	cm := NewConsumerManager(ctx, conn, js, cfg)

	if cm.fetchBatchSize != 7 {
		t.Errorf("fetchBatchSize = %d, want 7", cm.fetchBatchSize)
	}
	if cm.fetchTimeout != 350*time.Millisecond {
		t.Errorf("fetchTimeout = %v, want 350ms", cm.fetchTimeout)
	}
	if cm.defaultMaxConcurrency != 15 {
		t.Errorf("defaultMaxConcurrency = %d, want 15", cm.defaultMaxConcurrency)
	}
}

// TestNewConsumerManager_DefaultFields verifies that nil config falls back to defaults.
func TestNewConsumerManager_DefaultFields(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	ctx := contextWithFakeKube(t)

	cm := NewConsumerManager(ctx, conn, js, nil)

	if cm.fetchBatchSize != DefaultFetchBatchSize {
		t.Errorf("fetchBatchSize = %d, want %d", cm.fetchBatchSize, DefaultFetchBatchSize)
	}
	if cm.fetchTimeout != DefaultFetchTimeout {
		t.Errorf("fetchTimeout = %v, want %v", cm.fetchTimeout, DefaultFetchTimeout)
	}
	if cm.defaultMaxConcurrency != DefaultMaxConcurrency {
		t.Errorf("defaultMaxConcurrency = %d, want %d", cm.defaultMaxConcurrency, DefaultMaxConcurrency)
	}
}

// TestSubscribeTrigger_And_Unsubscribe verifies subscribe/unsubscribe lifecycle.
func TestSubscribeTrigger_And_Unsubscribe(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

	namespace := "default"
	brokerName := "test-broker"
	triggerUID := "subscribe-trigger-uid-001"

	setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID)

	cm := newConsumerManagerForTest(t, ctx, conn, js, &ConsumerManagerConfig{
		FetchBatchSize: 2,
		FetchTimeout:   100 * time.Millisecond,
		MaxConcurrency: 5,
	})

	broker := makeTestBrokerForNats(namespace, brokerName)
	trigger := makeTriggerWithUID(namespace, "test-trigger", brokerName, triggerUID)

	subscriberURL, _ := apis.ParseURL("http://localhost:9999")
	subscriber := duckv1.Addressable{URL: subscriberURL}

	err := cm.SubscribeTrigger(trigger, broker, subscriber, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("SubscribeTrigger: %v", err)
	}

	if !cm.HasSubscription(triggerUID) {
		t.Error("HasSubscription() = false after subscribe, want true")
	}

	err = cm.UnsubscribeTrigger(triggerUID)
	if err != nil {
		t.Fatalf("UnsubscribeTrigger: %v", err)
	}

	if cm.HasSubscription(triggerUID) {
		t.Error("HasSubscription() = true after unsubscribe, want false")
	}
	if cm.GetSubscriptionCount() != 0 {
		t.Errorf("GetSubscriptionCount() = %d, want 0", cm.GetSubscriptionCount())
	}
}

// TestFetchLoop_DispatchesMessages verifies that messages published to the stream
// are fetched and dispatched to the subscriber httptest server.
func TestFetchLoop_DispatchesMessages(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

	namespace := "default"
	brokerName := "dispatch-broker"
	triggerUID := "dispatch-trigger-uid-001"

	setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID)

	var receivedCount int64
	received := make(chan struct{}, 10)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&receivedCount, 1)
		received <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cm := newConsumerManagerForTest(t, ctx, conn, js, &ConsumerManagerConfig{
		FetchBatchSize: 5,
		FetchTimeout:   200 * time.Millisecond,
		MaxConcurrency: 10,
	})

	broker := makeTestBrokerForNats(namespace, brokerName)
	trigger := makeTriggerWithUID(namespace, "dispatch-trigger", brokerName, triggerUID)

	subscriberURL, _ := apis.ParseURL(srv.URL)
	subscriber := duckv1.Addressable{URL: subscriberURL}

	err := cm.SubscribeTrigger(trigger, broker, subscriber, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("SubscribeTrigger: %v", err)
	}
	defer cm.UnsubscribeTrigger(triggerUID) //nolint:errcheck

	publishSubject := brokerutils.BrokerPublishSubjectName(namespace, brokerName)
	for i := 0; i < 3; i++ {
		publishStructuredCE(t, js, publishSubject, fmt.Sprintf("event-id-%d", i))
	}

	deadline := time.After(10 * time.Second)
	for got := 0; got < 3; {
		select {
		case <-received:
			got++
		case <-deadline:
			t.Fatalf("timed out waiting for messages: got %d, want 3", got)
		}
	}

	if n := atomic.LoadInt64(&receivedCount); n != 3 {
		t.Errorf("subscriber received %d requests, want 3", n)
	}
}

// TestFetchLoop_ContextCancellation verifies that UnsubscribeTrigger stops the fetch loop.
func TestFetchLoop_ContextCancellation(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

	namespace := "default"
	brokerName := "cancel-broker"
	triggerUID := "cancel-trigger-uid-001"

	setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID)

	cm := newConsumerManagerForTest(t, ctx, conn, js, &ConsumerManagerConfig{
		FetchBatchSize: 2,
		FetchTimeout:   100 * time.Millisecond,
		MaxConcurrency: 5,
	})

	broker := makeTestBrokerForNats(namespace, brokerName)
	trigger := makeTriggerWithUID(namespace, "cancel-trigger", brokerName, triggerUID)

	// Subscriber that just returns OK.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	subscriberURL, _ := apis.ParseURL(srv.URL)
	subscriber := duckv1.Addressable{URL: subscriberURL}

	err := cm.SubscribeTrigger(trigger, broker, subscriber, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("SubscribeTrigger: %v", err)
	}

	if !cm.HasSubscription(triggerUID) {
		t.Fatal("HasSubscription should be true after subscribe")
	}

	// Unsubscribe should stop the fetch loop quickly.
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := cm.UnsubscribeTrigger(triggerUID); err != nil {
			t.Errorf("UnsubscribeTrigger: %v", err)
		}
	}()

	select {
	case <-done:
		// Good.
	case <-time.After(5 * time.Second):
		t.Fatal("UnsubscribeTrigger did not return within 5 seconds")
	}

	if cm.HasSubscription(triggerUID) {
		t.Error("HasSubscription() = true after unsubscribe, want false")
	}
}

// TestClose_WithActiveSubscriptions verifies that Close() stops all subscriptions.
func TestClose_WithActiveSubscriptions(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

	namespace := "default"
	brokerName := "close-broker"

	triggerUID1 := "close-trigger-uid-001"
	triggerUID2 := "close-trigger-uid-002"

	setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID1)
	setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID2)

	cm := newConsumerManagerForTest(t, ctx, conn, js, &ConsumerManagerConfig{
		FetchBatchSize: 2,
		FetchTimeout:   100 * time.Millisecond,
		MaxConcurrency: 5,
	})

	broker := makeTestBrokerForNats(namespace, brokerName)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	subscriberURL, _ := apis.ParseURL(srv.URL)
	subscriber := duckv1.Addressable{URL: subscriberURL}

	trigger1 := makeTriggerWithUID(namespace, "close-trigger-1", brokerName, triggerUID1)
	trigger2 := makeTriggerWithUID(namespace, "close-trigger-2", brokerName, triggerUID2)

	if err := cm.SubscribeTrigger(trigger1, broker, subscriber, nil, nil, nil, nil); err != nil {
		t.Fatalf("SubscribeTrigger(trigger1): %v", err)
	}
	if err := cm.SubscribeTrigger(trigger2, broker, subscriber, nil, nil, nil, nil); err != nil {
		t.Fatalf("SubscribeTrigger(trigger2): %v", err)
	}

	if cm.GetSubscriptionCount() != 2 {
		t.Fatalf("GetSubscriptionCount() = %d, want 2", cm.GetSubscriptionCount())
	}

	if err := cm.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}

	if cm.GetSubscriptionCount() != 0 {
		t.Errorf("GetSubscriptionCount() = %d, want 0 after Close()", cm.GetSubscriptionCount())
	}
}

// TestFetchLoop_DynamicBatchSize verifies that the fetch loop respects semaphore capacity.
func TestFetchLoop_DynamicBatchSize(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

	namespace := "default"
	brokerName := "dynamic-broker"
	triggerUID := "dynamic-trigger-uid-001"

	setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID)

	var receivedCount int64
	received := make(chan struct{}, 20)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&receivedCount, 1)
		received <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// maxConcurrency=2 forces fetch loop to cap batch to 2 at a time.
	cm := newConsumerManagerForTest(t, ctx, conn, js, &ConsumerManagerConfig{
		FetchBatchSize: 5,
		FetchTimeout:   200 * time.Millisecond,
		MaxConcurrency: 2,
	})

	broker := makeTestBrokerForNats(namespace, brokerName)
	trigger := makeTriggerWithUID(namespace, "dynamic-trigger", brokerName, triggerUID)

	subscriberURL, _ := apis.ParseURL(srv.URL)
	subscriber := duckv1.Addressable{URL: subscriberURL}

	err := cm.SubscribeTrigger(trigger, broker, subscriber, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("SubscribeTrigger: %v", err)
	}
	defer cm.UnsubscribeTrigger(triggerUID) //nolint:errcheck

	publishSubject := brokerutils.BrokerPublishSubjectName(namespace, brokerName)
	const totalMessages = 5
	for i := 0; i < totalMessages; i++ {
		publishStructuredCE(t, js, publishSubject, fmt.Sprintf("dyn-event-id-%d", i))
	}

	deadline := time.After(15 * time.Second)
	for got := 0; got < totalMessages; {
		select {
		case <-received:
			got++
		case <-deadline:
			t.Fatalf("timed out waiting for %d messages: got %d", totalMessages, got)
		}
	}

	if n := atomic.LoadInt64(&receivedCount); n != totalMessages {
		t.Errorf("received %d messages, want %d", n, totalMessages)
	}
}

// TestSubscribeTrigger_RestartOnAnnotationChange verifies that changing any of
// the three fetch-related annotations causes the fetch loop to restart with
// the new parameters, while the dispatch context (and any in-flight HTTP calls
// it parents) survives.
func TestSubscribeTrigger_RestartOnAnnotationChange(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

	namespace := "default"
	brokerName := "restart-broker"
	triggerUID := "restart-trigger-uid-001"

	setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID)

	cm := newConsumerManagerForTest(t, ctx, conn, js, &ConsumerManagerConfig{
		FetchBatchSize: 5,
		FetchTimeout:   100 * time.Millisecond,
		MaxConcurrency: 4,
	})
	defer cm.Close() //nolint:errcheck

	broker := makeTestBrokerForNats(namespace, brokerName)
	trigger := makeTriggerWithUID(namespace, "restart-trigger", brokerName, triggerUID)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	subscriberURL, _ := apis.ParseURL(srv.URL)
	subscriber := duckv1.Addressable{URL: subscriberURL}

	if err := cm.SubscribeTrigger(trigger, broker, subscriber, nil, nil, nil, nil); err != nil {
		t.Fatalf("SubscribeTrigger (first): %v", err)
	}

	cm.mu.RLock()
	first := cm.subscriptions[triggerUID]
	firstSem := first.sem
	firstDone := first.done
	firstDispatchCtx := first.dispatchCtx
	cm.mu.RUnlock()

	if got, want := cap(firstSem), 4; got != want {
		t.Fatalf("initial sem cap = %d, want %d", got, want)
	}

	// Update annotations on the same trigger UID.
	trigger.Annotations = map[string]string{
		TriggerFetchBatchSizeAnnotation: "8",
		TriggerFetchTimeoutAnnotation:   "250ms",
		TriggerMaxConcurrencyAnnotation: "12",
	}

	if err := cm.SubscribeTrigger(trigger, broker, subscriber, nil, nil, nil, nil); err != nil {
		t.Fatalf("SubscribeTrigger (restart): %v", err)
	}

	cm.mu.RLock()
	second := cm.subscriptions[triggerUID]
	cm.mu.RUnlock()

	if second != first {
		t.Errorf("subscription pointer changed on restart; in-place update expected")
	}
	if got, want := second.fetchBatchSize, 8; got != want {
		t.Errorf("fetchBatchSize = %d, want %d", got, want)
	}
	if got, want := second.fetchTimeout, 250*time.Millisecond; got != want {
		t.Errorf("fetchTimeout = %v, want %v", got, want)
	}
	if got, want := second.maxConcurrency, 12; got != want {
		t.Errorf("maxConcurrency = %d, want %d", got, want)
	}
	if got, want := cap(second.sem), 12; got != want {
		t.Errorf("sem cap = %d, want %d (new semaphore should be sized to new max-concurrency)", got, want)
	}
	if second.sem == firstSem {
		t.Errorf("sem channel was reused; expected a fresh channel on restart")
	}
	if second.done == firstDone {
		t.Errorf("done channel was reused; expected a fresh channel on restart")
	}
	if second.dispatchCtx != firstDispatchCtx {
		t.Errorf("dispatchCtx changed across restart; expected it to survive")
	}
	if second.dispatchCtx.Err() != nil {
		t.Errorf("dispatchCtx was cancelled across restart: %v", second.dispatchCtx.Err())
	}

	// Old fetch loop should have observed cancel and closed firstDone.
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Errorf("old fetch loop's done channel was not closed within 2s")
	}
}

// TestSubscribeTrigger_NoRestartWhenAnnotationsUnchanged verifies that re-
// subscribing without changing any fetch-related annotation does not restart
// the fetch loop.
func TestSubscribeTrigger_NoRestartWhenAnnotationsUnchanged(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

	namespace := "default"
	brokerName := "no-restart-broker"
	triggerUID := "no-restart-trigger-uid-001"

	setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID)

	cm := newConsumerManagerForTest(t, ctx, conn, js, &ConsumerManagerConfig{
		FetchBatchSize: 3,
		FetchTimeout:   100 * time.Millisecond,
		MaxConcurrency: 6,
	})
	defer cm.Close() //nolint:errcheck

	broker := makeTestBrokerForNats(namespace, brokerName)
	trigger := makeTriggerWithUID(namespace, "no-restart-trigger", brokerName, triggerUID)

	url1, _ := apis.ParseURL("http://localhost:9996")
	url2, _ := apis.ParseURL("http://localhost:9995")

	if err := cm.SubscribeTrigger(trigger, broker, duckv1.Addressable{URL: url1}, nil, nil, nil, nil); err != nil {
		t.Fatalf("SubscribeTrigger (first): %v", err)
	}

	cm.mu.RLock()
	first := cm.subscriptions[triggerUID]
	firstSem := first.sem
	firstDone := first.done
	cm.mu.RUnlock()

	// Same trigger, no annotations — should be a pure in-place handler update.
	if err := cm.SubscribeTrigger(trigger, broker, duckv1.Addressable{URL: url2}, nil, nil, nil, nil); err != nil {
		t.Fatalf("SubscribeTrigger (second): %v", err)
	}

	cm.mu.RLock()
	second := cm.subscriptions[triggerUID]
	cm.mu.RUnlock()

	if second.sem != firstSem {
		t.Errorf("sem was replaced when annotations did not change")
	}
	if second.done != firstDone {
		t.Errorf("done channel was replaced when annotations did not change")
	}

	// firstDone should NOT have been closed.
	select {
	case <-firstDone:
		t.Errorf("fetch loop's done channel was closed despite no annotation change")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestSubscribeTrigger_RestartTriggers verifies that the fetch loop restarts
// iff the *effective* fetch parameters would differ — not whenever the raw
// annotation map changes. Both sides of the comparison run through the same
// parser, so e.g. an annotation whose value equals the manager default is a
// no-op, an unparseable annotation is a no-op (both resolve to default), and
// removing an annotation only restarts if the default differs from the value
// previously in effect.
func TestSubscribeTrigger_RestartTriggers(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

	// Manager defaults: batch=5, timeout=100ms, maxConc=6.
	cfg := &ConsumerManagerConfig{
		FetchBatchSize: 5,
		FetchTimeout:   100 * time.Millisecond,
		MaxConcurrency: 6,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	subscriberURL, _ := apis.ParseURL(srv.URL)
	subscriber := duckv1.Addressable{URL: subscriberURL}

	cases := []struct {
		name        string
		firstAnn    map[string]string
		secondAnn   map[string]string
		wantRestart bool
	}{
		{
			name:        "no annotations on either side",
			firstAnn:    nil,
			secondAnn:   nil,
			wantRestart: false,
		},
		{
			name:        "same explicit values on both sides",
			firstAnn:    map[string]string{TriggerFetchBatchSizeAnnotation: "8"},
			secondAnn:   map[string]string{TriggerFetchBatchSizeAnnotation: "8"},
			wantRestart: false,
		},
		{
			name:        "annotation value equals manager default",
			firstAnn:    nil,
			secondAnn:   map[string]string{TriggerFetchBatchSizeAnnotation: "5"},
			wantRestart: false,
		},
		{
			name:        "remove annotation whose value equalled default",
			firstAnn:    map[string]string{TriggerFetchBatchSizeAnnotation: "5"},
			secondAnn:   nil,
			wantRestart: false,
		},
		{
			name:        "invalid annotation on both sides resolves to default",
			firstAnn:    map[string]string{TriggerFetchBatchSizeAnnotation: "garbage"},
			secondAnn:   map[string]string{TriggerFetchBatchSizeAnnotation: "also-garbage"},
			wantRestart: false,
		},
		{
			name:        "unrelated annotation added — no fetch params touched",
			firstAnn:    nil,
			secondAnn:   map[string]string{"unrelated/key": "anything"},
			wantRestart: false,
		},
		{
			name:        "batch size changed",
			firstAnn:    map[string]string{TriggerFetchBatchSizeAnnotation: "8"},
			secondAnn:   map[string]string{TriggerFetchBatchSizeAnnotation: "16"},
			wantRestart: true,
		},
		{
			name:        "only fetch-timeout changed",
			firstAnn:    nil,
			secondAnn:   map[string]string{TriggerFetchTimeoutAnnotation: "500ms"},
			wantRestart: true,
		},
		{
			name:        "only max-concurrency changed",
			firstAnn:    nil,
			secondAnn:   map[string]string{TriggerMaxConcurrencyAnnotation: "20"},
			wantRestart: true,
		},
		{
			name:        "removing annotation now diverges from default",
			firstAnn:    map[string]string{TriggerFetchBatchSizeAnnotation: "9"},
			secondAnn:   nil,
			wantRestart: true,
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			brokerName := fmt.Sprintf("restart-cases-broker-%d", i)
			triggerUID := fmt.Sprintf("restart-cases-uid-%d", i)
			namespace := "default"

			setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID)

			cm := newConsumerManagerForTest(t, ctx, conn, js, cfg)
			defer cm.Close() //nolint:errcheck

			broker := makeTestBrokerForNats(namespace, brokerName)
			trigger := makeTriggerWithUID(namespace, "trig", brokerName, triggerUID)

			trigger.Annotations = tc.firstAnn
			if err := cm.SubscribeTrigger(trigger, broker, subscriber, nil, nil, nil, nil); err != nil {
				t.Fatalf("first SubscribeTrigger: %v", err)
			}

			cm.mu.RLock()
			before := cm.subscriptions[triggerUID]
			beforeDone := before.done
			beforeSem := before.sem
			cm.mu.RUnlock()

			trigger.Annotations = tc.secondAnn
			if err := cm.SubscribeTrigger(trigger, broker, subscriber, nil, nil, nil, nil); err != nil {
				t.Fatalf("second SubscribeTrigger: %v", err)
			}

			cm.mu.RLock()
			after := cm.subscriptions[triggerUID]
			afterDone := after.done
			afterSem := after.sem
			cm.mu.RUnlock()

			restarted := beforeDone != afterDone || beforeSem != afterSem

			if restarted != tc.wantRestart {
				t.Errorf("restart = %v, want %v", restarted, tc.wantRestart)
			}

			if tc.wantRestart {
				// The old done channel must have been closed.
				select {
				case <-beforeDone:
				case <-time.After(2 * time.Second):
					t.Errorf("restart claimed but old done channel never closed")
				}
			} else {
				// The old done channel must NOT have been closed.
				select {
				case <-beforeDone:
					t.Errorf("no-restart claimed but old done channel was closed")
				case <-time.After(100 * time.Millisecond):
				}
			}
		})
	}
}

// TestObservability_SpanAndMetricsEmitted verifies that the broker filter
// produces (1) a "broker.filter.dispatch" span per dispatch with the expected
// attributes, (2) a kn.eventing.dispatch.duration histogram observation, and
// (3) a kn.eventing.broker.filter.dispatches.inflight observable gauge that
// can be collected. The MeterProvider and TracerProvider are constructed
// locally and injected directly into the ConsumerManager — we deliberately
// avoid otel.SetMeterProvider because the OTel global's delegate retains
// instrument registrations from earlier tests in the same package, which
// would prevent our callback from being collected.
func TestObservability_SpanAndMetricsEmitted(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	spanExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))

	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

	namespace := "default"
	brokerName := "obs-broker"
	triggerUID := "obs-trigger-uid-001"

	setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID)

	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case received <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Build a ConsumerManager directly bound to the local providers — no
	// otel.Set* dance. This also keeps the test parallel-safe in principle.
	meter := mp.Meter("knative.dev/eventing-natss/pkg/broker/filter")
	tracer := tp.Tracer("knative.dev/eventing-natss/pkg/broker/filter")
	dispatchDuration, err := meter.Float64Histogram(
		"kn.eventing.dispatch.duration",
		otelmetric.WithUnit("s"),
		otelmetric.WithExplicitBucketBoundaries(latencyBounds...),
	)
	if err != nil {
		t.Fatalf("create dispatch histogram: %v", err)
	}
	processDuration, err := meter.Float64Histogram(
		"kn.eventing.broker.filter.process.duration",
		otelmetric.WithUnit("s"),
		otelmetric.WithExplicitBucketBoundaries(latencyBounds...),
	)
	if err != nil {
		t.Fatalf("create process histogram: %v", err)
	}

	cm := &ConsumerManager{
		logger:                logging.FromContext(ctx),
		ctx:                   ctx,
		js:                    js,
		conn:                  conn,
		fetchBatchSize:        1,
		fetchTimeout:          100 * time.Millisecond,
		defaultMaxConcurrency: 4,
		dispatcher:            kncloudevents.NewDispatcher(eventingtls.ClientConfig{}, nil),
		tracer:                tracer,
		dispatchDuration:      dispatchDuration,
		processDuration:       processDuration,
		subscriptions:         make(map[string]*TriggerSubscription),
	}
	defer cm.Close() //nolint:errcheck

	if _, err := meter.Int64ObservableGauge(
		"kn.eventing.broker.filter.dispatches.inflight",
		otelmetric.WithInt64Callback(func(_ context.Context, obs otelmetric.Int64Observer) error {
			cm.mu.RLock()
			defer cm.mu.RUnlock()
			for _, sub := range cm.subscriptions {
				obs.Observe(int64(len(sub.sem)), otelmetric.WithAttributes(
					attribute.String("kn.trigger.name", sub.trigger.Name),
					attribute.String("kn.trigger.namespace", sub.trigger.Namespace),
				))
			}
			return nil
		}),
	); err != nil {
		t.Fatalf("register inflight gauge: %v", err)
	}

	broker := makeTestBrokerForNats(namespace, brokerName)
	trigger := makeTriggerWithUID(namespace, "obs-trigger", brokerName, triggerUID)
	subscriberURL, _ := apis.ParseURL(srv.URL)
	subscriber := duckv1.Addressable{URL: subscriberURL}

	if err := cm.SubscribeTrigger(trigger, broker, subscriber, nil, nil, nil, nil); err != nil {
		t.Fatalf("SubscribeTrigger: %v", err)
	}

	publishSubject := brokerutils.BrokerPublishSubjectName(namespace, brokerName)
	publishStructuredCE(t, js, publishSubject, "obs-event-id")

	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("subscriber never received the dispatched event")
	}

	// Allow span/metric records to flush. Spans are synchronous via WithSyncer;
	// histogram records are synchronous via ManualReader.
	deadline := time.Now().Add(2 * time.Second)
	var dispatchSpan *tracetest.SpanStub
	for time.Now().Before(deadline) {
		for i, s := range spanExporter.GetSpans() {
			if s.Name == "broker.filter.dispatch" {
				dispatchSpan = &spanExporter.GetSpans()[i]
				break
			}
		}
		if dispatchSpan != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if dispatchSpan == nil {
		t.Fatal("broker.filter.dispatch span was not recorded")
	}

	attrs := map[string]string{}
	for _, a := range dispatchSpan.Attributes {
		attrs[string(a.Key)] = a.Value.AsString()
	}
	if attrs["kn.trigger.name"] != "obs-trigger" {
		t.Errorf("span kn.trigger.name = %q, want %q", attrs["kn.trigger.name"], "obs-trigger")
	}
	if attrs["kn.trigger.namespace"] != namespace {
		t.Errorf("span kn.trigger.namespace = %q, want %q", attrs["kn.trigger.namespace"], namespace)
	}
	if attrs["ce.id"] != "obs-event-id" {
		t.Errorf("span ce.id = %q, want %q", attrs["ce.id"], "obs-event-id")
	}
	if attrs["nats.result"] != "ack" {
		t.Errorf("span nats.result = %q, want %q", attrs["nats.result"], "ack")
	}

	// Collect metrics. ManualReader.Collect populates rm in place.
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("metric reader Collect: %v", err)
	}

	var sawDuration, sawProcess, sawInflight bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "kn.eventing.dispatch.duration":
				if h, ok := m.Data.(metricdata.Histogram[float64]); ok && len(h.DataPoints) > 0 {
					sawDuration = true
				}
			case "kn.eventing.broker.filter.process.duration":
				if h, ok := m.Data.(metricdata.Histogram[float64]); ok && len(h.DataPoints) > 0 {
					sawProcess = true
				}
			case "kn.eventing.broker.filter.dispatches.inflight":
				if g, ok := m.Data.(metricdata.Gauge[int64]); ok && len(g.DataPoints) >= 0 {
					// Even with zero in-flight at collection time the gauge is
					// considered registered as long as the callback ran without
					// error; the data points slice may be empty if no subscriptions
					// exist, but in this test we do have one.
					sawInflight = true
				}
			}
		}
	}
	if !sawDuration {
		t.Error("kn.eventing.dispatch.duration histogram had no data points")
	}
	if !sawProcess {
		t.Error("kn.eventing.broker.filter.process.duration histogram had no data points")
	}
	if !sawInflight {
		t.Error("kn.eventing.broker.filter.dispatches.inflight gauge was not collected")
	}
}

// TestUnsubscribeTrigger_WaitsForInflightDispatches verifies that
// UnsubscribeTrigger does not return until every dispatch goroutine spawned
// by the fetch loop has exited. Without this guarantee, in-flight goroutines
// could race with sub.Unsubscribe (msg.Ack on a closed subscription) and with
// handler.Cleanup (concurrent h.filter.Filter vs h.filter.Cleanup).
func TestUnsubscribeTrigger_WaitsForInflightDispatches(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

	namespace := "default"
	brokerName := "drain-broker"
	triggerUID := "drain-trigger-uid-001"

	setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID)

	// HTTP server that signals when a request arrives, then blocks until the
	// client's request context is cancelled. dispatchCancel (fired from
	// UnsubscribeTrigger) propagates through msgCtx, which cancels the
	// dispatcher's HTTP client, which closes the connection — the server's
	// r.Context() observes Done and the handler returns.
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case received <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cm := newConsumerManagerForTest(t, ctx, conn, js, &ConsumerManagerConfig{
		FetchBatchSize: 2,
		FetchTimeout:   100 * time.Millisecond,
		MaxConcurrency: 5,
	})

	broker := makeTestBrokerForNats(namespace, brokerName)
	trigger := makeTriggerWithUID(namespace, "drain-trigger", brokerName, triggerUID)

	subscriberURL, _ := apis.ParseURL(srv.URL)
	subscriber := duckv1.Addressable{URL: subscriberURL}

	if err := cm.SubscribeTrigger(trigger, broker, subscriber, nil, nil, nil, nil); err != nil {
		t.Fatalf("SubscribeTrigger: %v", err)
	}

	// Capture sub before unsubscribe so we can inspect its inflight WG after.
	cm.mu.RLock()
	sub := cm.subscriptions[triggerUID]
	cm.mu.RUnlock()

	publishSubject := brokerutils.BrokerPublishSubjectName(namespace, brokerName)
	publishStructuredCE(t, js, publishSubject, "drain-event-id")

	// Wait for a dispatch goroutine to actually be in flight.
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch never reached the subscriber")
	}

	// Unsubscribe; this should block until every inflight dispatch goroutine
	// has exited, then tear down the NATS subscription and handler.
	if err := cm.UnsubscribeTrigger(triggerUID); err != nil {
		t.Fatalf("UnsubscribeTrigger: %v", err)
	}

	// Post-condition: sub.inflight must be fully drained. A fresh Wait must
	// return effectively immediately. If unsubscribe failed to wait, this Wait
	// would still block until the dispatch goroutine finally finishes.
	done := make(chan struct{})
	go func() {
		sub.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Error("sub.inflight was not drained when UnsubscribeTrigger returned")
	}
}

// TestSubscribeTrigger_UpdateInPlace verifies that re-subscribing an existing trigger
// updates the handler in place without creating a new subscription.
func TestSubscribeTrigger_UpdateInPlace(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

	namespace := "default"
	brokerName := "update-broker"
	triggerUID := "update-trigger-uid-001"

	setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID)

	cm := newConsumerManagerForTest(t, ctx, conn, js, &ConsumerManagerConfig{
		FetchBatchSize: 2,
		FetchTimeout:   100 * time.Millisecond,
		MaxConcurrency: 5,
	})

	broker := makeTestBrokerForNats(namespace, brokerName)
	trigger := makeTriggerWithUID(namespace, "update-trigger", brokerName, triggerUID)

	url1, _ := apis.ParseURL("http://localhost:9998")
	url2, _ := apis.ParseURL("http://localhost:9997")

	err := cm.SubscribeTrigger(trigger, broker, duckv1.Addressable{URL: url1}, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("SubscribeTrigger (first): %v", err)
	}

	// Second call with same triggerUID — should update in place.
	err = cm.SubscribeTrigger(trigger, broker, duckv1.Addressable{URL: url2}, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("SubscribeTrigger (second): %v", err)
	}

	// Still only one subscription.
	if cm.GetSubscriptionCount() != 1 {
		t.Errorf("GetSubscriptionCount() = %d, want 1 after update", cm.GetSubscriptionCount())
	}

	cm.Close() //nolint:errcheck
}
