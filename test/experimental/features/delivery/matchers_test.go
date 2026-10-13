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

package delivery

import (
	"testing"
	"time"

	cetest "github.com/cloudevents/sdk-go/v2/test"
	"github.com/stretchr/testify/require"
	"knative.dev/reconciler-test/pkg/eventshub"
)

func TestEventWithKind(t *testing.T) {
	event := cetest.FullEvent()
	matcher := eventWithKind(event.ID(), eventshub.EventReceived)

	require.NoError(t, matcher(eventshub.EventInfo{Event: &event, Kind: eventshub.EventReceived}))
	require.Error(t, matcher(eventshub.EventInfo{Kind: eventshub.EventReceived}))
	require.Error(t, matcher(eventshub.EventInfo{Event: &event, Kind: eventshub.EventRejected}))

	other := cetest.FullEvent()
	other.SetID("other")
	require.Error(t, matcher(eventshub.EventInfo{Event: &other, Kind: eventshub.EventReceived}))
}

func TestDeliveriesFollowIntervals(t *testing.T) {
	event := cetest.FullEvent()
	tests := []struct {
		name      string
		expected  []time.Duration
		intervals []time.Duration
		wantErr   bool
	}{
		{
			name:      "capped exponential backoff",
			expected:  []time.Duration{time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second},
			intervals: []time.Duration{time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second},
		},
		{
			name:      "uncapped exponential backoff",
			expected:  []time.Duration{time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second},
			intervals: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second},
			wantErr:   true,
		},
		{
			name:      "capped Retry-After",
			expected:  []time.Duration{2 * time.Second, 2 * time.Second},
			intervals: []time.Duration{2 * time.Second, 2 * time.Second},
		},
		{
			name:      "uncapped Retry-After",
			expected:  []time.Duration{2 * time.Second, 2 * time.Second},
			intervals: []time.Duration{6 * time.Second, 6 * time.Second},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := deliveriesFollowIntervals(event.ID(), tt.expected)
			receivedAt := time.Unix(0, 0)
			var gotErr error
			for i := 0; i <= len(tt.intervals); i++ {
				if i > 0 {
					receivedAt = receivedAt.Add(tt.intervals[i-1])
				}

				kind := eventshub.EventRejected
				sequence := uint64(i + 1)
				if i == len(tt.intervals) {
					kind = eventshub.EventReceived
					sequence = 1
				}
				gotErr = matcher(eventshub.EventInfo{
					Event:    &event,
					Kind:     kind,
					Time:     receivedAt,
					Sequence: sequence,
				})
				if i < len(tt.intervals) {
					require.NoError(t, gotErr)
				}
			}

			if tt.wantErr {
				require.Error(t, gotErr)
			} else {
				require.NoError(t, gotErr)
			}
		})
	}
}
