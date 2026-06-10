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

	return &ConsumerManager{
		logger:                logging.FromContext(ctx),
		ctx:                   ctx,
		js:                    js,
		conn:                  conn,
		fetchBatchSize:        fetchBatchSize,
		fetchTimeout:          fetchTimeout,
		defaultMaxConcurrency: maxConcurrency,
		dispatcher:            dispatcher,
		subscriptions:         make(map[string]*TriggerSubscription),
	}
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
