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
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"knative.dev/eventing/pkg/kncloudevents"

	dispatchertesting "knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher/testing"
)

// TestProcessDispatchResultRetriesPerSpec verifies that a failed delivery is
// redelivered for the status codes that the Knative data plane specification
// retries, and is not redelivered for the others.
func TestProcessDispatchResultRetriesPerSpec(t *testing.T) {
	s := dispatchertesting.RunBasicJetstreamServer()
	defer dispatchertesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := dispatchertesting.JsClient(t, s)
	defer conn.Close()

	tests := []struct {
		code        int
		wantRetried bool
	}{
		{code: http.StatusNotFound, wantRetried: true},
		{code: http.StatusConflict, wantRetried: true},
		{code: http.StatusServiceUnavailable, wantRetried: true},
		{code: http.StatusBadRequest, wantRetried: false},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.code), func(t *testing.T) {
			stream := fmt.Sprintf("PROCESS_RESULT_%d", tt.code)
			subject := fmt.Sprintf("process.result.%d", tt.code)
			if _, err := js.AddStream(&nats.StreamConfig{
				Name:     stream,
				Subjects: []string{subject},
				Storage:  nats.MemoryStorage,
			}); err != nil {
				t.Fatalf("AddStream(%q): %v", stream, err)
			}
			sub, err := js.PullSubscribe(subject, "consumer", nats.BindStream(stream), nats.AckExplicit())
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

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// A nil retry config makes the NAK delay zero, so a retried
			// message is redelivered at once.
			processDispatchResult(ctx, msgs[0], nil, 1,
				&kncloudevents.DispatchInfo{ResponseCode: tt.code}, errors.New("delivery failed"))

			redelivered, err := sub.Fetch(1, nats.MaxWait(500*time.Millisecond))
			if !tt.wantRetried {
				if !errors.Is(err, nats.ErrTimeout) {
					t.Fatalf("Fetch after a non-retriable response: got %d messages, err %v; want a timeout", len(redelivered), err)
				}
				return
			}
			if err != nil || len(redelivered) != 1 {
				t.Fatalf("Fetch after a retriable response: got %d messages, err %v; want 1 redelivered message", len(redelivered), err)
			}
			meta, err := redelivered[0].Metadata()
			if err != nil {
				t.Fatalf("Metadata: %v", err)
			}
			if meta.NumDelivered != 2 {
				t.Errorf("NumDelivered = %d, want 2", meta.NumDelivered)
			}
			if err := redelivered[0].Term(); err != nil {
				t.Errorf("Term: %v", err)
			}
		})
	}
}
