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
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"

	brokerutils "knative.dev/eventing-natss/pkg/broker/utils"
	natsTesting "knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher/testing"
	"knative.dev/eventing/pkg/kncloudevents"
)

// subscribeTimeoutTest uses the production consumer manager and dispatcher.
// Only the server's AckWait is shortened in tests that exercise its extension.
func subscribeTimeoutTest(t *testing.T, subscriberURL, ingressURL, dlsURL string, retry *kncloudevents.RetryConfig, timeout, ackWait time.Duration) (*ConsumerManager, nats.JetStreamContext, string, string) {
	t.Helper()
	s := natsTesting.RunBasicJetstreamServer()
	t.Cleanup(func() { natsTesting.ShutdownJSServerAndRemoveStorage(t, s) })
	conn, js := natsTesting.JsClient(t, s)
	t.Cleanup(conn.Close)
	stream, consumer := setupStreamAndConsumer(t, js, "default", "timeout-broker", "timeout-trigger")
	info, err := js.ConsumerInfo(stream, consumer)
	require.NoError(t, err)
	info.Config.AckWait = ackWait
	info.Config.MaxDeliver = retry.RetryMax + 1
	_, err = js.UpdateConsumer(stream, &info.Config)
	require.NoError(t, err)

	cm := newConsumerManagerForTest(t, conn, js, &ConsumerManagerConfig{
		FetchBatchSize: 1,
		FetchTimeout:   100 * time.Millisecond,
		MaxConcurrency: 2,
	})
	t.Cleanup(func() { require.NoError(t, cm.Close()) })
	address := func(raw string) *duckv1.Addressable {
		if raw == "" {
			return nil
		}
		u, err := apis.ParseURL(raw)
		require.NoError(t, err)
		return &duckv1.Addressable{URL: u}
	}
	require.NoError(t, cm.SubscribeTrigger(
		makeTriggerWithUID("default", "timeout-trigger", "timeout-broker", "timeout-trigger"),
		makeTestBrokerForNats("default", "timeout-broker"),
		*address(subscriberURL), address(ingressURL), address(dlsURL), retry,
		&kncloudevents.RetryConfig{
			RequestTimeout: timeout,
			CheckRetry:     func(context.Context, *http.Response, error) (bool, error) { return false, nil },
		},
	))
	return cm, js, stream, consumer
}

func waitTimeoutMessageSettled(t *testing.T, js nats.JetStreamContext, stream, consumer string) {
	t.Helper()
	require.Eventually(t, func() bool {
		info, err := js.ConsumerInfo(stream, consumer)
		return err == nil && info.NumAckPending == 0 && info.AckFloor.Stream == 1
	}, 5*time.Second, 10*time.Millisecond, "message was not acknowledged")
}

func TestDeliveryTimeout_RetryAndDeadLetter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dls     bool
		backoff time.Duration
	}{
		{name: "dead letter sink", dls: true},
		{name: "backoff before retry", dls: true, backoff: 150 * time.Millisecond},
		{name: "no dead letter sink"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := make(chan time.Time, 10)
			subscriber := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				attempts <- time.Now()
				<-r.Context().Done()
			}))
			t.Cleanup(subscriber.Close)
			deliveries := make(chan string, 10)
			dls := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				deliveries <- r.Header.Get("Ce-Id")
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(dls.Close)
			dlsURL := ""
			if tc.dls {
				dlsURL = dls.URL
			}
			const timeout = 200 * time.Millisecond
			ackWait := brokerutils.ConsumerAckWait(timeout)
			_, js, stream, consumer := subscribeTimeoutTest(t, subscriber.URL, "", dlsURL,
				&kncloudevents.RetryConfig{RetryMax: 2, Backoff: func(int, *http.Response) time.Duration { return tc.backoff }}, timeout, ackWait)
			start := time.Now()
			publishStructuredCE(t, js, brokerutils.BrokerPublishSubjectName("default", "timeout-broker"), "timeout-event")
			waitTimeoutMessageSettled(t, js, stream, consumer)
			require.Less(t, time.Since(start), 5*time.Second, "retries must use NAK, not AckWait expiry")
			require.Len(t, attempts, 3)
			previous := <-attempts
			for range 2 {
				current := <-attempts
				// The client starts its timeout just before the server records the
				// request. Allow that small difference while checking that the
				// backoff follows the failed request, rather than overlapping it.
				require.GreaterOrEqual(t, current.Sub(previous), timeout+tc.backoff-20*time.Millisecond)
				previous = current
			}
			if tc.dls {
				require.Len(t, deliveries, 1)
				require.Equal(t, "timeout-event", <-deliveries)
			} else {
				require.Empty(t, deliveries)
			}
		})
	}
}

func TestDeliveryTimeout_ExtendsAckWaitForReply(t *testing.T) {
	attempts := make(chan struct{}, 10)
	subscriber := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts <- struct{}{}
		time.Sleep(time.Second)
		w.Header().Set("Content-Type", "application/cloudevents+json")
		_, _ = io.WriteString(w, `{"specversion":"1.0","id":"reply-event","source":"test/reply","type":"test.reply"}`)
	}))
	t.Cleanup(subscriber.Close)
	replies := make(chan string, 10)
	ingress := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(800 * time.Millisecond)
		replies <- r.Header.Get("Ce-Id")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ingress.Close)
	_, js, stream, consumer := subscribeTimeoutTest(t, subscriber.URL, ingress.URL, "",
		&kncloudevents.RetryConfig{RetryMax: 2}, 2*time.Second, 1500*time.Millisecond)
	publishStructuredCE(t, js, brokerutils.BrokerPublishSubjectName("default", "timeout-broker"), "reply-original")
	waitTimeoutMessageSettled(t, js, stream, consumer)
	require.Equal(t, "reply-event", receiveWithin(t, replies, "reply did not reach ingress"))
	require.Len(t, attempts, 1)
	<-attempts
	select {
	case <-attempts:
		t.Fatal("subscriber was called again while forwarding the reply")
	case <-time.After(2 * time.Second):
	}
	require.Empty(t, replies, "reply was forwarded more than once")
}

func TestDeliveryTimeout_UnsubscribeFinishesDeadLetter(t *testing.T) {
	subscriber := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(subscriber.Close)
	started := make(chan struct{}, 10)
	delivered := make(chan string, 10)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	dls := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		delivered <- r.Header.Get("Ce-Id")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(dls.Close)
	cm, js, stream, consumer := subscribeTimeoutTest(t, subscriber.URL, "", dls.URL,
		&kncloudevents.RetryConfig{}, 200*time.Millisecond, brokerutils.ConsumerAckWait(200*time.Millisecond))
	t.Cleanup(unblock)
	publishStructuredCE(t, js, brokerutils.BrokerPublishSubjectName("default", "timeout-broker"), "shutdown-dls")
	receiveWithin(t, started, "dead letter request did not start")
	done := make(chan error, 1)
	go func() { done <- cm.UnsubscribeTrigger("timeout-trigger") }()
	select {
	case err := <-done:
		t.Fatalf("unsubscribe returned before dead letter delivery: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	require.NoError(t, receiveWithin(t, done, "unsubscribe did not finish"))
	require.Equal(t, "shutdown-dls", receiveWithin(t, delivered, "dead letter request was canceled"))
	waitTimeoutMessageSettled(t, js, stream, consumer)
	require.Empty(t, delivered)
	require.Empty(t, started, "dead letter request was retried")
}

func TestDeliveryTimeout_CanceledSubscriberStaysPending(t *testing.T) {
	started := make(chan struct{}, 10)
	subscriber := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(subscriber.Close)
	deliveries := make(chan struct{}, 10)
	dls := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deliveries <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(dls.Close)
	cm, js, stream, consumer := subscribeTimeoutTest(t, subscriber.URL, "", dls.URL,
		&kncloudevents.RetryConfig{}, 5*time.Second, brokerutils.ConsumerAckWait(5*time.Second))
	publishStructuredCE(t, js, brokerutils.BrokerPublishSubjectName("default", "timeout-broker"), "canceled-event")
	receiveWithin(t, started, "subscriber request did not start")
	require.NoError(t, cm.UnsubscribeTrigger("timeout-trigger"))
	info, err := js.ConsumerInfo(stream, consumer)
	require.NoError(t, err)
	require.EqualValues(t, 1, info.NumAckPending)
	require.Zero(t, info.AckFloor.Stream)
	require.Empty(t, deliveries, "canceled request must not go to the dead letter sink")
}

func TestDispatchCanceled(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "no error"},
		{name: "shutdown", err: context.Canceled, want: true},
		{name: "wrapped shutdown", err: fmt.Errorf("dispatch: %w", context.Canceled), want: true},
		{name: "deadline", err: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, dispatchCanceled(tc.err))
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client := &http.Client{Timeout: 50 * time.Millisecond}
	_, err := client.Get(server.URL)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, dispatchCanceled(err), "HTTP client timeout is a failed try, not shutdown")
}
