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

package dispatcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"knative.dev/eventing/pkg/eventingtls"
	"knative.dev/eventing/pkg/kncloudevents"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"

	dispatchertesting "knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher/testing"
)

// TestSendMessageDeadLetterSettlement checks that a delivery remains pending
// while the dead letter sink handles it, and is settled only after acceptance.
func TestSendMessageDeadLetterSettlement(t *testing.T) {
	for _, tc := range []struct {
		name             string
		subscriberStatus int
		deadLetterStatus int
		retries          int
	}{
		{"non-retriable subscriber response", http.StatusBadRequest, http.StatusAccepted, 1},
		{"last subscriber attempt", http.StatusServiceUnavailable, http.StatusAccepted, 0},
		{"failed dead letter delivery", http.StatusBadRequest, http.StatusServiceUnavailable, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testSendMessageDeadLetterSettlement(t, tc.subscriberStatus, tc.deadLetterStatus, tc.retries)
		})
	}
}

func testSendMessageDeadLetterSettlement(t *testing.T, subscriberStatus, deadLetterStatus, retries int) {
	t.Helper()
	s := dispatchertesting.RunBasicJetstreamServer()
	defer dispatchertesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := dispatchertesting.JsClient(t, s)
	defer conn.Close()

	var mu sync.Mutex
	subscriberCalls, deadLetterCalls := 0, 0
	deadLetterErrorCode := ""
	subscriber := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		subscriberCalls++
		mu.Unlock()
		w.WriteHeader(subscriberStatus)
	}))
	defer subscriber.Close()
	var sub *nats.Subscription
	deadLetterSink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, err := sub.ConsumerInfo()
		if err != nil {
			t.Errorf("ConsumerInfo during dead letter delivery: %v", err)
		} else if info.NumAckPending != 1 {
			t.Errorf("NumAckPending during dead letter delivery = %d, want 1", info.NumAckPending)
		}
		mu.Lock()
		deadLetterCalls++
		deadLetterErrorCode = r.Header.Get("Ce-Knativeerrorcode")
		mu.Unlock()
		w.WriteHeader(deadLetterStatus)
	}))
	defer deadLetterSink.Close()

	const stream, subject = "NON_RETRIABLE", "non.retriable"
	if _, err := js.AddStream(&nats.StreamConfig{Name: stream, Subjects: []string{subject}, Storage: nats.MemoryStorage}); err != nil {
		t.Fatalf("AddStream(%q): %v", stream, err)
	}
	var err error
	sub, err = js.PullSubscribe(subject, "consumer", nats.BindStream(stream), nats.AckExplicit(), nats.AckWait(100*time.Millisecond), nats.MaxDeliver(3))
	if err != nil {
		t.Fatalf("PullSubscribe(%q): %v", subject, err)
	}
	defer sub.Unsubscribe() //nolint:errcheck
	if _, err := js.Publish(subject, []byte("event")); err != nil {
		t.Fatalf("Publish(%q): %v", subject, err)
	}
	msgs, err := sub.Fetch(1, nats.MaxWait(time.Second))
	if err != nil || len(msgs) != 1 {
		t.Fatalf("Fetch: got %d messages, err %v; want 1 message", len(msgs), err)
	}

	retryConfig := kncloudevents.NoRetries()
	retryConfig.RetryMax = retries
	retryConfig.CheckRetry = kncloudevents.SelectiveRetry

	event := cloudevents.NewEvent(cloudevents.VersionV1)
	event.SetID(uuid.New().String())
	event.SetType(testCeType)
	event.SetSource(testCeSource)
	subscriberURL, _ := apis.ParseURL(subscriber.URL)
	deadLetterURL, _ := apis.ParseURL(deadLetterSink.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := SendMessage(
		kncloudevents.NewDispatcher(eventingtls.ClientConfig{}, nil),
		ctx,
		binding.ToMessage(&event),
		duckv1.Addressable{URL: subscriberURL},
		ackWait,
		msgs[0],
		WithDeadLetterSink(&duckv1.Addressable{URL: deadLetterURL}),
		WithRetryConfig(&retryConfig),
	); (err != nil) != (deadLetterStatus != http.StatusAccepted) {
		t.Errorf("SendMessage error = %v, dead letter status = %d", err, deadLetterStatus)
	}

	mu.Lock()
	defer mu.Unlock()
	if subscriberCalls != 1 {
		t.Errorf("subscriber calls = %d, want 1", subscriberCalls)
	}
	wantDeadLetterCalls := 1
	if deadLetterStatus != http.StatusAccepted {
		wantDeadLetterCalls += retries
	}
	if deadLetterCalls != wantDeadLetterCalls {
		t.Errorf("dead letter sink calls = %d, want %d", deadLetterCalls, wantDeadLetterCalls)
	}
	if deadLetterErrorCode != strconv.Itoa(subscriberStatus) {
		t.Errorf("dead letter knativeerrorcode = %q, want %d", deadLetterErrorCode, subscriberStatus)
	}

	info, err := sub.ConsumerInfo()
	if err != nil {
		t.Fatalf("ConsumerInfo: %v", err)
	}
	if deadLetterStatus != http.StatusAccepted {
		if info.NumAckPending != 1 {
			t.Fatalf("NumAckPending after failed dead letter delivery = %d, want 1", info.NumAckPending)
		}
		redelivered, err := sub.Fetch(1, nats.MaxWait(time.Second))
		if err != nil || len(redelivered) != 1 {
			t.Fatalf("Fetch after failed dead letter delivery: got %d messages, err %v", len(redelivered), err)
		}
		if err := redelivered[0].Term(); err != nil {
			t.Errorf("Term: %v", err)
		}
		return
	}
	if info.NumAckPending != 0 {
		t.Errorf("NumAckPending after successful dead letter delivery = %d, want 0", info.NumAckPending)
	}
	if redelivered, err := sub.Fetch(1, nats.MaxWait(200*time.Millisecond)); !errors.Is(err, nats.ErrTimeout) {
		t.Errorf("Fetch after the dead letter delivery: got %d messages, err %v; want a timeout", len(redelivered), err)
	}
}
