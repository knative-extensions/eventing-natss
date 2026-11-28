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
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"

	"knative.dev/eventing/pkg/kncloudevents"

	brokerutils "knative.dev/eventing-natss/pkg/broker/utils"
	natsTesting "knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher/testing"
)

// TestNonRetriableResponseSendsToDeadLetterSinkImmediately verifies that a
// non-retriable subscriber response is sent to the dead letter sink on the
// first delivery, while retries remain, and that the message is then settled.
func TestNonRetriableResponseSendsToDeadLetterSinkImmediately(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	const namespace, brokerName, triggerUID = "default", "non-retriable-broker", "non-retriable-trigger-uid"
	streamName, consumerName := setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID)

	var subscriberCalls atomic.Int64
	subscriberServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		subscriberCalls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer subscriberServer.Close()

	var deadLetterCalls atomic.Int64
	deadLetterServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		deadLetterCalls.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer deadLetterServer.Close()

	cm := newConsumerManagerForTest(t, conn, js, &ConsumerManagerConfig{
		FetchBatchSize: 1,
		FetchTimeout:   20 * time.Millisecond,
		MaxConcurrency: 1,
	})
	defer cm.Close() //nolint:errcheck

	subscriberURL, _ := apis.ParseURL(subscriberServer.URL)
	deadLetterURL, _ := apis.ParseURL(deadLetterServer.URL)
	if err := cm.SubscribeTrigger(
		makeTriggerWithUID(namespace, "non-retriable-trigger", brokerName, triggerUID),
		makeTestBrokerForNats(namespace, brokerName),
		duckv1.Addressable{URL: subscriberURL},
		nil,
		&duckv1.Addressable{URL: deadLetterURL},
		// Retries remain after the first delivery, so it is not the last try.
		&kncloudevents.RetryConfig{RetryMax: 3},
		nil,
	); err != nil {
		t.Fatalf("SubscribeTrigger: %v", err)
	}

	publishStructuredCE(t, js, brokerutils.BrokerPublishSubjectName(namespace, brokerName), "non-retriable-event")

	// The DLS send completes before the message is settled. Once nothing is
	// pending or awaiting ack, the message cannot be redelivered, so the call
	// counts below are final.
	require.Eventually(t, func() bool {
		info, err := js.ConsumerInfo(streamName, consumerName)
		return err == nil && info.NumAckPending == 0 && info.NumPending == 0
	}, 5*time.Second, 10*time.Millisecond, "message was not settled")

	if got := subscriberCalls.Load(); got != 1 {
		t.Errorf("subscriber calls = %d, want 1", got)
	}
	if got := deadLetterCalls.Load(); got != 1 {
		t.Errorf("dead letter sink calls = %d, want 1", got)
	}
}
