/*
Copyright 2020 The Knative Authors

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
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/nats-io/nats-server/v2/server"
	natsserver "github.com/nats-io/nats-server/v2/test"
	dispatchertesting "knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher/testing"

	"knative.dev/eventing/pkg/eventingtls"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/observability/tracing"

	v1 "knative.dev/eventing/pkg/apis/duck/v1"
	"knative.dev/eventing/pkg/kncloudevents"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/binding/transformer"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/sets"

	"knative.dev/eventing/pkg/utils"
)

var (
	// Headers that are added to the response, but we don't want to check in our assertions.
	unimportantHeaders = sets.NewString(
		"accept-encoding",
		"content-length",
		"content-type",
		"user-agent",
		"tracestate",
		"ce-tracestate",
	)

	// Headers that should be present, but their value should not be asserted.
	ignoreValueHeaders = sets.NewString(
		// These are headers added for tracing, they will have random values, so don't bother
		// checking them.
		"traceparent",
		// CloudEvents headers, they will have random values, so don't bother checking them.
		"ce-id",
		"ce-time",
		"ce-traceparent",
	)
)

const (
	testCeSource = "testsource"
	testCeType   = "testtype"
	ackWait      = 30 * time.Second
)

var (
	retryCount    int32 = 3
	backoffDelay        = "PT1S"
	backoffPolicy       = v1.BackoffPolicyExponential
)

func TestDispatchMessage(t *testing.T) {
	testCases := map[string]struct {
		sendToDestination         bool
		sendToReply               bool
		hasDeadLetterSink         bool
		eventExtensions           map[string]string
		header                    http.Header
		body                      string
		fakeResponse              *http.Response
		fakeReplyResponse         *http.Response
		fakeDeadLetterResponse    *http.Response
		expectedErr               bool
		expectedDestRequest       *requestValidation
		expectedReplyRequest      *requestValidation
		expectedDeadLetterRequest *requestValidation
		lastReceiver              string
		delivery                  *v1.DeliverySpec
	}{
		"destination - only": {
			sendToDestination: true,
			header: map[string][]string{
				// do-not-forward should not get forwarded.
				"do-not-forward": {"header"},
				"x-request-id":   {"id123"},
				"knative-1":      {"knative-1-value"},
				"knative-2":      {"knative-2-value"},
			},
			body: "destination",
			eventExtensions: map[string]string{
				"abc": `"ce-abc-value"`,
			},
			expectedDestRequest: &requestValidation{
				Headers: map[string][]string{
					"x-request-id":   {"id123"},
					"knative-1":      {"knative-1-value"},
					"knative-2":      {"knative-2-value"},
					"prefer":         {"reply"},
					"traceparent":    {"ignored-value-header"},
					"ce-abc":         {`"ce-abc-value"`},
					"ce-id":          {"ignored-value-header"},
					"ce-time":        {"ignored-value-header"},
					"ce-source":      {testCeSource},
					"ce-type":        {testCeType},
					"ce-specversion": {cloudevents.VersionV1},
				},
				Body: `"destination"`,
			},
			lastReceiver: "destination",
		},
		"destination - nil additional headers": {
			sendToDestination: true,
			body:              "destination",
			eventExtensions: map[string]string{
				"abc": `"ce-abc-value"`,
			},
			expectedDestRequest: &requestValidation{
				Headers: map[string][]string{
					"prefer":         {"reply"},
					"traceparent":    {"ignored-value-header"},
					"ce-abc":         {`"ce-abc-value"`},
					"ce-id":          {"ignored-value-header"},
					"ce-time":        {"ignored-value-header"},
					"ce-source":      {testCeSource},
					"ce-type":        {testCeType},
					"ce-specversion": {cloudevents.VersionV1},
				},
				Body: `"destination"`,
			},
			lastReceiver: "destination",
		},
		"destination and reply - dest returns bad status code": {
			sendToDestination: true,
			sendToReply:       true,
			header: map[string][]string{
				// do-not-forward should not get forwarded.
				"do-not-forward": {"header"},
				"x-request-id":   {"id123"},
				"knative-1":      {"knative-1-value"},
				"knative-2":      {"knative-2-value"},
			},
			body: "destination",
			eventExtensions: map[string]string{
				"abc": `"ce-abc-value"`,
			},
			expectedDestRequest: &requestValidation{
				Headers: map[string][]string{
					"x-request-id":   {"id123"},
					"knative-1":      {"knative-1-value"},
					"knative-2":      {"knative-2-value"},
					"prefer":         {"reply"},
					"traceparent":    {"ignored-value-header"},
					"ce-abc":         {`"ce-abc-value"`},
					"ce-id":          {"ignored-value-header"},
					"ce-time":        {"ignored-value-header"},
					"ce-source":      {testCeSource},
					"ce-type":        {testCeType},
					"ce-specversion": {cloudevents.VersionV1},
				},
				Body: `"destination"`,
			},
			fakeResponse: &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(bytes.NewBufferString("destination-response")),
			},
			expectedErr:  true,
			lastReceiver: "reply",
		},
		"destination and reply - dest returns empty body": {
			sendToDestination: true,
			sendToReply:       true,
			header: map[string][]string{
				// do-not-forward should not get forwarded.
				"do-not-forward": {"header"},
				"x-request-id":   {"id123"},
				"knative-1":      {"knative-1-value"},
				"knative-2":      {"knative-2-value"},
			},
			body: "destination",
			eventExtensions: map[string]string{
				"abc": `"ce-abc-value"`,
			},
			expectedDestRequest: &requestValidation{
				Headers: map[string][]string{
					"x-request-id":   {"id123"},
					"knative-1":      {"knative-1-value"},
					"knative-2":      {"knative-2-value"},
					"prefer":         {"reply"},
					"traceparent":    {"ignored-value-header"},
					"ce-abc":         {`"ce-abc-value"`},
					"ce-id":          {"ignored-value-header"},
					"ce-time":        {"ignored-value-header"},
					"ce-source":      {testCeSource},
					"ce-type":        {testCeType},
					"ce-specversion": {cloudevents.VersionV1},
				},
				Body: `"destination"`,
			},
			fakeResponse: &http.Response{
				StatusCode: http.StatusAccepted,
				Header: map[string][]string{
					"do-not-passthrough": {"no"},
					"x-request-id":       {"altered-id"},
					"knative-1":          {"new-knative-1-value"},
					"ce-abc":             {`"new-ce-abc-value"`},
				},
				Body: io.NopCloser(bytes.NewBufferString("")),
			},
			lastReceiver: "reply",
		},
		"destination and reply": {
			sendToDestination: true,
			sendToReply:       true,
			header: map[string][]string{
				// do-not-forward should not get forwarded.
				"do-not-forward": {"header"},
				"x-request-id":   {"id123"},
				"knative-1":      {"knative-1-value"},
				"knative-2":      {"knative-2-value"},
			},
			body: "destination",
			eventExtensions: map[string]string{
				"abc": `"ce-abc-value"`,
			},
			expectedDestRequest: &requestValidation{
				Headers: map[string][]string{
					"x-request-id":   {"id123"},
					"knative-1":      {"knative-1-value"},
					"knative-2":      {"knative-2-value"},
					"prefer":         {"reply"},
					"traceparent":    {"ignored-value-header"},
					"ce-abc":         {`"ce-abc-value"`},
					"ce-id":          {"ignored-value-header"},
					"ce-time":        {"2002-10-02T15:00:00Z"},
					"ce-source":      {testCeSource},
					"ce-type":        {testCeType},
					"ce-specversion": {cloudevents.VersionV1},
				},
				Body: `"destination"`,
			},
			fakeResponse: &http.Response{
				StatusCode: http.StatusAccepted,
				Header: map[string][]string{
					"do-not-passthrough": {"no"},
					"x-request-id":       {"altered-id"},
					"knative-1":          {"new-knative-1-value"},
					"ce-abc":             {`"new-ce-abc-value"`},
					"ce-id":              {"ignored-value-header"},
					"ce-time":            {"2002-10-02T15:00:00Z"},
					"ce-source":          {testCeSource},
					"ce-type":            {testCeType},
					"ce-specversion":     {cloudevents.VersionV1},
				},
				Body: io.NopCloser(bytes.NewBufferString("destination-response")),
			},
			expectedReplyRequest: &requestValidation{
				Headers: map[string][]string{
					"x-request-id":   {"altered-id"},
					"knative-1":      {"new-knative-1-value"},
					"traceparent":    {"ignored-value-header"},
					"ce-abc":         {`"new-ce-abc-value"`},
					"ce-id":          {"ignored-value-header"},
					"ce-time":        {"2002-10-02T15:00:00Z"},
					"ce-source":      {testCeSource},
					"ce-type":        {testCeType},
					"ce-specversion": {cloudevents.VersionV1},
				},
				Body: "destination-response",
			},
			lastReceiver: "reply",
		},
		"no restriction on message response size": {
			sendToDestination: true,
			sendToReply:       true,
			hasDeadLetterSink: false,
			header: map[string][]string{
				// do-not-forward should not get forwarded.
				"do-not-forward": {"header"},
				"x-request-id":   {"id123"},
				"knative-1":      {"knative-1-value"},
				"knative-2":      {"knative-2-value"},
			},
			body: "destination",
			eventExtensions: map[string]string{
				"abc": `"ce-abc-value"`,
			},
			expectedDestRequest: &requestValidation{
				Headers: map[string][]string{
					"x-request-id":   {"id123"},
					"knative-1":      {"knative-1-value"},
					"knative-2":      {"knative-2-value"},
					"prefer":         {"reply"},
					"traceparent":    {"ignored-value-header"},
					"ce-abc":         {`"ce-abc-value"`},
					"ce-id":          {"ignored-value-header"},
					"ce-time":        {"2002-10-02T15:00:00Z"},
					"ce-source":      {testCeSource},
					"ce-type":        {testCeType},
					"ce-specversion": {cloudevents.VersionV1},
				},
				Body: `"destination"`,
			},
			expectedReplyRequest: &requestValidation{
				Headers: map[string][]string{
					"x-request-id":   {"id123"},
					"knative-1":      {"knative-1-value"},
					"traceparent":    {"ignored-value-header"},
					"ce-abc":         {`"ce-abc-value"`},
					"ce-id":          {"ignored-value-header"},
					"ce-time":        {"2002-10-02T15:00:00Z"},
					"ce-source":      {testCeSource},
					"ce-type":        {testCeType},
					"ce-specversion": {cloudevents.VersionV1},
				},
				Body: strings.Repeat("a", 2000),
			},
			fakeResponse: &http.Response{
				StatusCode: http.StatusAccepted,
				Header: map[string][]string{
					"do-not-passthrough": {"no"},
					"x-request-id":       {"id123"},
					"knative-1":          {"knative-1-value"},
					"ce-abc":             {`"ce-abc-value"`},
					"ce-id":              {"ignored-value-header"},
					"ce-time":            {"2002-10-02T15:00:00Z"},
					"ce-source":          {testCeSource},
					"ce-type":            {testCeType},
					"ce-specversion":     {cloudevents.VersionV1},
				},
				Body: io.NopCloser(bytes.NewBufferString(strings.Repeat("a", 2000))),
			},

			lastReceiver: "reply",
		},
		"error response and retries": {
			delivery: &v1.DeliverySpec{
				Retry:         &retryCount,
				BackoffDelay:  &backoffDelay,
				BackoffPolicy: &backoffPolicy,
			},
			sendToDestination: true,
			header: map[string][]string{
				// do-not-forward should not get forwarded.
				"do-not-forward": {"header"},
				"x-request-id":   {"id123"},
				"knative-1":      {"knative-1-value"},
				"knative-2":      {"knative-2-value"},
			},
			body: "destination",
			eventExtensions: map[string]string{
				"abc": `"ce-abc-value"`,
			},
			expectedDestRequest: &requestValidation{
				Headers: map[string][]string{
					"x-request-id":   {"id123"},
					"knative-1":      {"knative-1-value"},
					"knative-2":      {"knative-2-value"},
					"prefer":         {"reply"},
					"traceparent":    {"ignored-value-header"},
					"ce-abc":         {`"ce-abc-value"`},
					"ce-id":          {"ignored-value-header"},
					"ce-time":        {"ignored-value-header"},
					"ce-source":      {testCeSource},
					"ce-type":        {testCeType},
					"ce-specversion": {cloudevents.VersionV1},
				},
				Body: `"destination"`,
			},
			fakeResponse: &http.Response{
				StatusCode: http.StatusGatewayTimeout,
				Body:       io.NopCloser(bytes.NewBufferString("destination-response")),
			},
			expectedErr:  true,
			lastReceiver: "destination",
		},
		"error response term message": {
			delivery: &v1.DeliverySpec{
				Retry:         &retryCount,
				BackoffDelay:  &backoffDelay,
				BackoffPolicy: &backoffPolicy,
			},
			sendToDestination: true,
			sendToReply:       true,
			header: map[string][]string{
				// do-not-forward should not get forwarded.
				"do-not-forward": {"header"},
				"x-request-id":   {"id123"},
				"knative-1":      {"knative-1-value"},
				"knative-2":      {"knative-2-value"},
			},
			body: "destination",
			eventExtensions: map[string]string{
				"abc": `"ce-abc-value"`,
			},
			expectedDestRequest: &requestValidation{
				Headers: map[string][]string{
					"x-request-id":   {"id123"},
					"knative-1":      {"knative-1-value"},
					"knative-2":      {"knative-2-value"},
					"prefer":         {"reply"},
					"traceparent":    {"ignored-value-header"},
					"ce-abc":         {`"ce-abc-value"`},
					"ce-id":          {"ignored-value-header"},
					"ce-time":        {"ignored-value-header"},
					"ce-source":      {testCeSource},
					"ce-type":        {testCeType},
					"ce-specversion": {cloudevents.VersionV1},
				},
				Body: `"destination"`,
			},
			fakeResponse: &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(bytes.NewBufferString("destination-response")),
			},
			expectedErr:  true,
			lastReceiver: "destination",
		},
	}

	s := dispatchertesting.RunBasicJetstreamServer()
	defer dispatchertesting.ShutdownJSServerAndRemoveStorage(t, s)
	_, js := dispatchertesting.JsClient(t, s)

	for n, tc := range testCases {
		t.Run(n, func(t *testing.T) {
			reader := metric.NewManualReader()
			mp := metric.NewMeterProvider(metric.WithReader(reader))
			otel.SetMeterProvider(mp)

			exporter := tracetest.NewInMemoryExporter()
			tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
			otel.SetTracerProvider(tp)
			otel.SetTextMapPropagator(tracing.DefaultTextMapPropagator())
			destHandler := &fakeHandler{
				t:        t,
				response: tc.fakeResponse,
				requests: make([]requestValidation, 0),
			}
			destServer := httptest.NewServer(destHandler)
			defer destServer.Close()

			replyHandler := &fakeHandler{
				t:        t,
				response: tc.fakeResponse,
				requests: make([]requestValidation, 0),
			}
			replyServer := httptest.NewServer(replyHandler)
			defer replyServer.Close()
			if tc.fakeReplyResponse != nil {
				replyHandler.response = tc.fakeReplyResponse
			}

			var deadLetterSinkHandler *fakeHandler
			var deadLetterSinkServer *httptest.Server
			var deadLetterSinkURL *apis.URL

			if tc.hasDeadLetterSink {
				deadLetterSinkHandler = &fakeHandler{
					t:        t,
					response: tc.fakeDeadLetterResponse,
					requests: make([]requestValidation, 0),
				}
				deadLetterSinkServer = httptest.NewServer(deadLetterSinkHandler)
				defer deadLetterSinkServer.Close()

				getOnlyDomainURL(t, true, deadLetterSinkServer.URL)
				deadLetterSinkURL, _ = apis.ParseURL(deadLetterSinkServer.URL)
			}

			deadLetterSinkDestination := duckv1.Addressable{URL: deadLetterSinkURL}

			event := cloudevents.NewEvent(cloudevents.VersionV1)
			event.SetID(uuid.New().String())
			event.SetType("testtype")
			event.SetSource("testsource")
			for n, v := range tc.eventExtensions {
				event.SetExtension(n, v)
			}
			event.SetData(cloudevents.ApplicationJSON, tc.body)

			ctx := context.Background()

			md := kncloudevents.NewDispatcher(eventingtls.ClientConfig{}, nil)

			getOnlyDomainURL(t, tc.sendToDestination, destServer.URL)
			getOnlyDomainURL(t, tc.sendToReply, replyServer.URL)

			destinationURL, _ := apis.ParseURL(destServer.URL)
			destination := duckv1.Addressable{URL: destinationURL}
			replyURL, _ := apis.ParseURL(replyServer.URL)
			replyDestination := duckv1.Addressable{URL: replyURL}

			// We need to do message -> event -> message to emulate the same transformers the event receiver would do
			message := binding.ToMessage(&event)
			var err error
			ev, err := binding.ToEvent(ctx, message, binding.Transformers{transformer.AddTimeNow})
			if err != nil {
				t.Fatal(err)
			}
			message = binding.ToMessage(ev)
			finishInvoked := 0
			message = binding.WithFinish(message, func(err error) {
				finishInvoked++
			})

			var headers http.Header = nil
			if tc.header != nil {
				headers = utils.PassThroughHeaders(tc.header)
			}
			_, err = js.AddStream(&nats.StreamConfig{
				Name:     "test",
				Storage:  nats.MemoryStorage,
				Subjects: []string{"test.>"},
			})
			if err != nil {
				log.Fatalf("Failed to add stream, %s", err)
			}
			_, err = js.Publish("test.data", []byte("Here is a string...."))
			if err != nil {
				log.Fatalf("Failed to publish msg, %s", err)
			}
			natsSub, _ := js.SubscribeSync("test.>")
			msg, err := natsSub.NextMsg(50 * time.Millisecond)
			if err != nil {
				log.Fatalf("Failed to read msg: %s", err)
			}

			var retryConfig kncloudevents.RetryConfig
			if tc.delivery == nil {
				retryConfig = kncloudevents.NoRetries()
				backoffLinear := v1.BackoffPolicyLinear
				retryConfig.BackoffPolicy = &backoffLinear
				backoffDelay := "5s"
				retryConfig.BackoffDelay = &backoffDelay
			} else {
				retryConfig = kncloudevents.RetryConfig{
					RetryMax:      int(*tc.delivery.Retry),
					BackoffPolicy: tc.delivery.BackoffPolicy,
					BackoffDelay:  tc.delivery.BackoffDelay,
				}
			}

			te := TypeExtractorTransformer("")
			info, err := SendMessage(
				md,
				ctx,
				message,
				destination,
				ackWait,
				msg,
				WithReply(&replyDestination),
				WithDeadLetterSink(&deadLetterSinkDestination),
				WithRetryConfig(&retryConfig),
				WithTransformers(&te),
				WithHeader(headers))

			if tc.lastReceiver != "" {
				switch tc.lastReceiver {
				case "destination":
					if tc.fakeResponse != nil {
						if tc.fakeResponse.StatusCode != info.ResponseCode {
							t.Errorf("Unexpected response code in DispatchResultInfo. Expected %v. Actual: %v", tc.fakeResponse.StatusCode, info.ResponseCode)
						}
					}
				case "deadLetter":
					if tc.fakeDeadLetterResponse != nil {
						if tc.fakeDeadLetterResponse.StatusCode != info.ResponseCode {
							t.Errorf("Unexpected response code in DispatchResultInfo. Expected %v. Actual: %v", tc.fakeDeadLetterResponse.StatusCode, info.ResponseCode)
						}
					}
				case "reply":
					if tc.fakeReplyResponse != nil {
						if tc.fakeReplyResponse.StatusCode != info.ResponseCode {
							t.Errorf("Unexpected response code in DispatchResultInfo. Expected %v. Actual: %v", tc.fakeReplyResponse.StatusCode, info.ResponseCode)
						}
					}
				}
			}

			if tc.expectedErr != (err != nil) {
				t.Errorf("Unexpected error from DispatchMessage. Expected %v. Actual: %v", tc.expectedErr, err)
			}
			if finishInvoked != 1 {
				t.Error("Finish should be invoked exactly one time. Actual:", finishInvoked)
			}
			if tc.expectedDestRequest != nil {
				rv := destHandler.popRequest(t)
				assertEquality(t, destServer.URL, *tc.expectedDestRequest, rv)
			}
			if tc.expectedReplyRequest != nil {
				rv := replyHandler.popRequest(t)
				assertEquality(t, replyServer.URL, *tc.expectedReplyRequest, rv)
			}
			if tc.expectedDeadLetterRequest != nil {
				if tc.sendToReply {
					tc.expectedDeadLetterRequest.Headers.Set("ce-knativeerrordest", replyServer.URL+"/")
				} else if tc.sendToDestination {
					tc.expectedDeadLetterRequest.Headers.Set("ce-knativeerrordest", destServer.URL+"/")
				}
				rv := deadLetterSinkHandler.popRequest(t)
				assertEquality(t, deadLetterSinkServer.URL, *tc.expectedDeadLetterRequest, rv)
			}
			if len(destHandler.requests) != 0 {
				t.Errorf("Unexpected destination requests: %+v", destHandler.requests)
			}
			if len(replyHandler.requests) != 0 {
				t.Errorf("Unexpected reply requests: %+v", replyHandler.requests)
			}
			if deadLetterSinkHandler != nil && len(deadLetterSinkHandler.requests) != 0 {
				t.Errorf("Unexpected dead letter sink requests: %+v", deadLetterSinkHandler.requests)
			}
		})
	}
}

func getOnlyDomainURL(t *testing.T, shouldSend bool, serverURL string) *url.URL {
	if shouldSend {
		server, err := url.Parse(serverURL)
		if err != nil {
			t.Errorf("Bad serverURL: %q", serverURL)
		}
		return &url.URL{
			Host: server.Host,
		}
	}
	return nil
}

type requestValidation struct {
	Host    string
	Headers http.Header
	Body    string
}

type fakeHandler struct {
	t        *testing.T
	response *http.Response
	requests []requestValidation
}

func (f *fakeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	// Make a copy of the request.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Error("Failed to read the request body")
	}
	f.requests = append(f.requests, requestValidation{
		Host:    r.Host,
		Headers: r.Header,
		Body:    string(body),
	})

	// Write the response.
	if f.response != nil {
		for h, vs := range f.response.Header {
			for _, v := range vs {
				w.Header().Add(h, v)
			}
		}
		w.WriteHeader(f.response.StatusCode)
		if _, err := io.Copy(w, f.response.Body); err != nil {
			f.t.Error("Error copying Body:", err)
		}
	} else {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(""))
	}
}

func (f *fakeHandler) popRequest(t *testing.T) requestValidation {
	if len(f.requests) == 0 {
		t.Error("Unable to pop request")
		return requestValidation{
			Host: "MADE UP, no such request",
			Body: "MADE UP, no such request",
		}
	}
	rv := f.requests[0]
	f.requests = f.requests[1:]
	return rv
}

func assertEquality(t *testing.T, replacementURL string, expected, actual requestValidation) {
	t.Helper()
	server, err := url.Parse(replacementURL)
	if err != nil {
		t.Errorf("Bad replacement URL: %q", replacementURL)
	}
	expected.Host = server.Host
	canonicalizeHeaders(expected, actual)
	if diff := cmp.Diff(expected, actual); diff != "" {
		t.Error("Unexpected difference (-want, +got):", diff)
	}
}

func canonicalizeHeaders(rvs ...requestValidation) {
	// HTTP header names are case-insensitive, so normalize them to lower case for comparison.
	for _, rv := range rvs {
		headers := rv.Headers
		for n, v := range headers {
			delete(headers, n)
			n = strings.ToLower(n)
			if unimportantHeaders.Has(n) {
				continue
			}
			if ignoreValueHeaders.Has(n) {
				headers[n] = []string{"ignored-value-header"}
			} else {
				headers[n] = v
			}
		}
	}
}

func RunServerOnPort(port int) *server.Server {
	opts := natsserver.DefaultTestOptions
	opts.Port = port
	return RunServerWithOptions(&opts)
}

func RunServerWithOptions(opts *server.Options) *server.Server {
	return natsserver.RunServer(opts)
}
