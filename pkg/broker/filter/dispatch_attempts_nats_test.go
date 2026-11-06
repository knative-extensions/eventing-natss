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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"

	"knative.dev/eventing/pkg/kncloudevents"

	brokerutils "knative.dev/eventing-natss/pkg/broker/utils"
	natsTesting "knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher/testing"
)

// TestDispatchAttemptsCounter verifies that subscriber and dead letter sink
// requests are counted by trigger, response status code, and delivery result.
func TestDispatchAttemptsCounter(t *testing.T) {
	// The last subscriber response is the last try, so the event goes to the
	// dead letter sink after it.
	tests := []struct {
		name                string
		subscriberResponses []int
		deadLetterResponse  int
		want                map[string]int64
	}{{
		name:                "retry, then non-retriable response sent to the dead letter sink",
		subscriberResponses: []int{http.StatusServiceUnavailable, http.StatusBadRequest},
		deadLetterResponse:  http.StatusAccepted,
		want: map[string]int64{
			"subscriber_retry/503":     1,
			"subscriber_permanent/400": 1,
			"dead_letter_success/202":  1,
		},
	}, {
		name:                "dead letter sink failure",
		subscriberResponses: []int{http.StatusBadRequest},
		deadLetterResponse:  http.StatusBadRequest,
		want: map[string]int64{
			"subscriber_permanent/400": 1,
			"dead_letter_failure/400":  1,
		},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := natsTesting.RunBasicJetstreamServer()
			defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
			conn, js := natsTesting.JsClient(t, s)
			defer conn.Close()

			const namespace, brokerName, triggerName, triggerUID = "default", "attempts-broker", "attempts-trigger", "attempts-trigger-uid"
			setupStreamAndConsumer(t, js, namespace, brokerName, triggerUID)

			var subscriberCalls atomic.Int64
			subscriberServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.subscriberResponses[subscriberCalls.Add(1)-1])
			}))
			defer subscriberServer.Close()
			deadLetterServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.deadLetterResponse)
			}))
			defer deadLetterServer.Close()

			reader := sdkmetric.NewManualReader()
			meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter(otelScope)
			dispatchAttempts, err := meter.Int64Counter(dispatchAttemptsMetric)
			if err != nil {
				t.Fatalf("create dispatch attempts counter: %v", err)
			}

			cm := newConsumerManagerForTest(t, conn, js, &ConsumerManagerConfig{
				FetchBatchSize: 1,
				FetchTimeout:   20 * time.Millisecond,
				MaxConcurrency: 1,
			})
			defer cm.Close() //nolint:errcheck
			// NewConsumerManager reads the global meter provider; swap in a
			// counter backed by the local reader.
			cm.dispatchAttempts = dispatchAttempts

			broker := makeTestBrokerForNats(namespace, brokerName)
			trigger := makeTriggerWithUID(namespace, triggerName, brokerName, triggerUID)
			subscriberURL, _ := apis.ParseURL(subscriberServer.URL)
			deadLetterURL, _ := apis.ParseURL(deadLetterServer.URL)
			noRetryConfig := kncloudevents.NoRetries()
			if err := cm.SubscribeTrigger(
				trigger,
				broker,
				duckv1.Addressable{URL: subscriberURL},
				nil,
				&duckv1.Addressable{URL: deadLetterURL},
				&kncloudevents.RetryConfig{RetryMax: len(tc.subscriberResponses) - 1},
				&noRetryConfig,
			); err != nil {
				t.Fatalf("SubscribeTrigger: %v", err)
			}

			publishStructuredCE(t, js, brokerutils.BrokerPublishSubjectName(namespace, brokerName), "attempts-event")

			require.EventuallyWithT(t, func(c *assert.CollectT) {
				assert.Equal(c, tc.want, collectDispatchAttempts(c, reader, namespace, triggerName))
			}, 10*time.Second, 50*time.Millisecond)
		})
	}
}

// collectDispatchAttempts returns the dispatch attempts counter keyed by
// "<delivery.result>/<http.response.status_code>" for one trigger.
func collectDispatchAttempts(c *assert.CollectT, reader sdkmetric.Reader, namespace, triggerName string) map[string]int64 {
	var rm metricdata.ResourceMetrics
	require.NoError(c, reader.Collect(context.Background(), &rm))
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != dispatchAttemptsMetric {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.Truef(c, ok, "dispatch attempts data = %T, want metricdata.Sum[int64]", m.Data)
			for _, dp := range sum.DataPoints {
				name, _ := dp.Attributes.Value("kn.trigger.name")
				ns, _ := dp.Attributes.Value("kn.trigger.namespace")
				if name.AsString() != triggerName || ns.AsString() != namespace {
					continue
				}
				result, _ := dp.Attributes.Value("delivery.result")
				status, _ := dp.Attributes.Value("http.response.status_code")
				got[fmt.Sprintf("%s/%d", result.AsString(), status.AsInt64())] += dp.Value
			}
		}
	}
	return got
}
