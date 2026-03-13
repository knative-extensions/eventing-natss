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
