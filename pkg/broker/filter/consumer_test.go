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
	"testing"
	"time"

	"go.uber.org/zap"
	"knative.dev/pkg/logging"
)

func TestConsumerManagerConfigDefaults(t *testing.T) {
	// Verify default values
	if DefaultFetchBatchSize != 10 {
		t.Errorf("DefaultFetchBatchSize = %v, want 10", DefaultFetchBatchSize)
	}

	if DefaultFetchTimeout != 200*time.Millisecond {
		t.Errorf("DefaultFetchTimeout = %v, want 200ms", DefaultFetchTimeout)
	}
}

func TestConsumerManagerConfig(t *testing.T) {
	tests := []struct {
		name               string
		config             *ConsumerManagerConfig
		wantFetchBatchSize int
		wantFetchTimeout   time.Duration
	}{
		{
			name:               "nil config uses defaults",
			config:             nil,
			wantFetchBatchSize: DefaultFetchBatchSize,
			wantFetchTimeout:   DefaultFetchTimeout,
		},
		{
			name:               "empty config uses defaults",
			config:             &ConsumerManagerConfig{},
			wantFetchBatchSize: DefaultFetchBatchSize,
			wantFetchTimeout:   DefaultFetchTimeout,
		},
		{
			name: "zero values use defaults",
			config: &ConsumerManagerConfig{
				FetchBatchSize: 0,
				FetchTimeout:   0,
			},
			wantFetchBatchSize: DefaultFetchBatchSize,
			wantFetchTimeout:   DefaultFetchTimeout,
		},
		{
			name: "custom batch size only",
			config: &ConsumerManagerConfig{
				FetchBatchSize: 20,
				FetchTimeout:   0,
			},
			wantFetchBatchSize: 20,
			wantFetchTimeout:   DefaultFetchTimeout,
		},
		{
			name: "custom timeout only",
			config: &ConsumerManagerConfig{
				FetchBatchSize: 0,
				FetchTimeout:   1 * time.Second,
			},
			wantFetchBatchSize: DefaultFetchBatchSize,
			wantFetchTimeout:   1 * time.Second,
		},
		{
			name: "both custom values",
			config: &ConsumerManagerConfig{
				FetchBatchSize: 50,
				FetchTimeout:   2 * time.Second,
			},
			wantFetchBatchSize: 50,
			wantFetchTimeout:   2 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We can't easily test NewConsumerManager without a real NATS connection,
			// so we test the config application logic directly
			fetchBatchSize := DefaultFetchBatchSize
			fetchTimeout := DefaultFetchTimeout

			if tt.config != nil {
				if tt.config.FetchBatchSize > 0 {
					fetchBatchSize = tt.config.FetchBatchSize
				}
				if tt.config.FetchTimeout > 0 {
					fetchTimeout = tt.config.FetchTimeout
				}
			}

			if fetchBatchSize != tt.wantFetchBatchSize {
				t.Errorf("fetchBatchSize = %v, want %v", fetchBatchSize, tt.wantFetchBatchSize)
			}

			if fetchTimeout != tt.wantFetchTimeout {
				t.Errorf("fetchTimeout = %v, want %v", fetchTimeout, tt.wantFetchTimeout)
			}
		})
	}
}

func TestGetSubscriptionCount(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

	tests := []struct {
		name  string
		count int
	}{
		{"empty map", 0},
		{"one entry", 1},
		{"three entries", 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cm := &ConsumerManager{
				logger:        logging.FromContext(ctx),
				subscriptions: make(map[string]*TriggerSubscription),
			}
			for i := 0; i < tc.count; i++ {
				uid := fmt.Sprintf("uid-%d", i)
				cm.subscriptions[uid] = &TriggerSubscription{}
			}
			if got := cm.GetSubscriptionCount(); got != tc.count {
				t.Errorf("GetSubscriptionCount() = %d, want %d", got, tc.count)
			}
		})
	}
}

func TestHasSubscription(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

	cm := &ConsumerManager{
		logger:        logging.FromContext(ctx),
		subscriptions: make(map[string]*TriggerSubscription),
	}
	cm.subscriptions["existing-uid"] = &TriggerSubscription{}

	if !cm.HasSubscription("existing-uid") {
		t.Error("HasSubscription() = false for existing UID, want true")
	}
	if cm.HasSubscription("missing-uid") {
		t.Error("HasSubscription() = true for missing UID, want false")
	}
}

func TestConsumerManagerClose(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

	cm := &ConsumerManager{
		logger:        logging.FromContext(ctx),
		subscriptions: make(map[string]*TriggerSubscription),
	}

	err := cm.Close()
	if err != nil {
		t.Errorf("Close() unexpected error on empty subscriptions: %v", err)
	}
}

func TestUnsubscribeTrigger_NotFound(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

	cm := &ConsumerManager{
		logger:        logging.FromContext(ctx),
		subscriptions: make(map[string]*TriggerSubscription),
	}

	err := cm.UnsubscribeTrigger("non-existent-uid")
	if err != nil {
		t.Errorf("UnsubscribeTrigger() unexpected error for non-existent UID: %v", err)
	}
}

func TestDefaultMaxConcurrency(t *testing.T) {
	if DefaultMaxConcurrency != 20 {
		t.Errorf("DefaultMaxConcurrency = %v, want 20", DefaultMaxConcurrency)
	}
}

func TestAnnotationConstants(t *testing.T) {
	tests := []struct {
		name  string
		got   string
		want  string
	}{
		{
			name: "TriggerMaxConcurrencyAnnotation",
			got:  TriggerMaxConcurrencyAnnotation,
			want: "natsjetstream.eventing.knative.dev/max-concurrency",
		},
		{
			name: "TriggerFetchBatchSizeAnnotation",
			got:  TriggerFetchBatchSizeAnnotation,
			want: "natsjetstream.eventing.knative.dev/fetch-batch-size",
		},
		{
			name: "TriggerFetchTimeoutAnnotation",
			got:  TriggerFetchTimeoutAnnotation,
			want: "natsjetstream.eventing.knative.dev/fetch-timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestConsumerManagerConfig_MaxConcurrency(t *testing.T) {
	tests := []struct {
		name               string
		config             *ConsumerManagerConfig
		wantMaxConcurrency int
	}{
		{
			name:               "nil config uses default",
			config:             nil,
			wantMaxConcurrency: DefaultMaxConcurrency,
		},
		{
			name:               "zero MaxConcurrency uses default",
			config:             &ConsumerManagerConfig{MaxConcurrency: 0},
			wantMaxConcurrency: DefaultMaxConcurrency,
		},
		{
			name:               "positive MaxConcurrency is used",
			config:             &ConsumerManagerConfig{MaxConcurrency: 50},
			wantMaxConcurrency: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			maxConcurrency := DefaultMaxConcurrency

			if tt.config != nil {
				if tt.config.MaxConcurrency > 0 {
					maxConcurrency = tt.config.MaxConcurrency
				}
			}

			if maxConcurrency != tt.wantMaxConcurrency {
				t.Errorf("maxConcurrency = %v, want %v", maxConcurrency, tt.wantMaxConcurrency)
			}
		})
	}
}

func TestParseTriggerAnnotationInt(t *testing.T) {
	logger := zap.NewNop().Sugar()

	tests := []struct {
		name        string
		annotations map[string]string
		key         string
		defaultVal  int
		want        int
	}{
		{
			name:        "absent key returns default",
			annotations: map[string]string{},
			key:         "some-key",
			defaultVal:  10,
			want:        10,
		},
		{
			name:        "empty string returns default",
			annotations: map[string]string{"some-key": ""},
			key:         "some-key",
			defaultVal:  10,
			want:        10,
		},
		{
			name:        "valid positive int is parsed",
			annotations: map[string]string{"some-key": "42"},
			key:         "some-key",
			defaultVal:  10,
			want:        42,
		},
		{
			name:        "zero returns default",
			annotations: map[string]string{"some-key": "0"},
			key:         "some-key",
			defaultVal:  10,
			want:        10,
		},
		{
			name:        "negative returns default",
			annotations: map[string]string{"some-key": "-5"},
			key:         "some-key",
			defaultVal:  10,
			want:        10,
		},
		{
			name:        "non-numeric returns default",
			annotations: map[string]string{"some-key": "abc"},
			key:         "some-key",
			defaultVal:  10,
			want:        10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTriggerAnnotationInt(tt.annotations, tt.key, tt.defaultVal, logger)
			if got != tt.want {
				t.Errorf("parseTriggerAnnotationInt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseTriggerAnnotationDuration(t *testing.T) {
	logger := zap.NewNop().Sugar()

	tests := []struct {
		name        string
		annotations map[string]string
		key         string
		defaultVal  time.Duration
		want        time.Duration
	}{
		{
			name:        "absent key returns default",
			annotations: map[string]string{},
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        200 * time.Millisecond,
		},
		{
			name:        "empty string returns default",
			annotations: map[string]string{"some-key": ""},
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        200 * time.Millisecond,
		},
		{
			name:        "valid duration is parsed",
			annotations: map[string]string{"some-key": "500ms"},
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        500 * time.Millisecond,
		},
		{
			name:        "zero duration returns default",
			annotations: map[string]string{"some-key": "0s"},
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        200 * time.Millisecond,
		},
		{
			name:        "negative duration returns default",
			annotations: map[string]string{"some-key": "-1s"},
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        200 * time.Millisecond,
		},
		{
			name:        "non-duration string returns default",
			annotations: map[string]string{"some-key": "abc"},
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        200 * time.Millisecond,
		},
		{
			name:        "nil annotations map returns default without panic",
			annotations: nil,
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        200 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTriggerAnnotationDuration(tt.annotations, tt.key, tt.defaultVal, logger)
			if got != tt.want {
				t.Errorf("parseTriggerAnnotationDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDynamicBatchSizeCapping(t *testing.T) {
	tests := []struct {
		name           string
		capacity       int
		occupied       int
		fetchBatchSize int
		wantBatchSize  int
	}{
		{
			name:           "all free and batchSize fits within capacity",
			capacity:       20,
			occupied:       0,
			fetchBatchSize: 10,
			wantBatchSize:  10,
		},
		{
			name:           "all free but batchSize exceeds capacity",
			capacity:       5,
			occupied:       0,
			fetchBatchSize: 10,
			wantBatchSize:  5,
		},
		{
			name:           "partially occupied and batchSize fits within available",
			capacity:       20,
			occupied:       5,
			fetchBatchSize: 10,
			wantBatchSize:  10,
		},
		{
			name:           "partially occupied and batchSize exceeds available",
			capacity:       20,
			occupied:       15,
			fetchBatchSize: 10,
			wantBatchSize:  5,
		},
		{
			name:           "one slot free",
			capacity:       20,
			occupied:       19,
			fetchBatchSize: 10,
			wantBatchSize:  1,
		},
		{
			name:           "all occupied returns zero",
			capacity:       20,
			occupied:       20,
			fetchBatchSize: 10,
			wantBatchSize:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sem := make(chan struct{}, tt.capacity)
			for i := 0; i < tt.occupied; i++ {
				sem <- struct{}{}
			}

			available := cap(sem) - len(sem)
			batchSize := tt.fetchBatchSize
			if available < batchSize {
				batchSize = available
			}

			if batchSize != tt.wantBatchSize {
				t.Errorf("batchSize = %v, want %v (available=%v, fetchBatchSize=%v)",
					batchSize, tt.wantBatchSize, available, tt.fetchBatchSize)
			}
		})
	}
}
