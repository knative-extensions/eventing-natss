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

	"github.com/stretchr/testify/require"
	"knative.dev/reconciler-test/pkg/feature"
)

func TestFeaturesRunSenderAfterReadiness(t *testing.T) {
	tests := []struct {
		name         string
		build        func() *feature.Feature
		readiness    []string
		resultChecks []string
	}{
		{
			name:      "Broker BackoffMax",
			build:     BackoffMaxBrokerToSink,
			readiness: []string{"receiver is addressable", "broker is ready", "trigger is ready"},
			resultChecks: []string{
				"receiver rejects the first four deliveries",
				"receiver accepts the fifth delivery",
				"retry delay stops growing at two seconds",
			},
		},
		{
			name:      "Channel BackoffMax",
			build:     BackoffMaxChannelToSink,
			readiness: []string{"receiver is addressable", "channel is ready", "subscription is ready"},
			resultChecks: []string{
				"receiver rejects the first four deliveries",
				"receiver accepts the fifth delivery",
				"retry delay stops growing at two seconds",
			},
		},
		{
			name:      "Broker RetryAfterMax",
			build:     RetryAfterMaxBrokerToSink,
			readiness: []string{"receiver is addressable", "broker is ready", "trigger is ready"},
			resultChecks: []string{
				"receiver rejects the first two deliveries",
				"receiver accepts the third delivery",
				"Retry-After delay is capped at two seconds",
			},
		},
		{
			name:      "Channel RetryAfterMax",
			build:     RetryAfterMaxChannelToSink,
			readiness: []string{"receiver is addressable", "channel is ready", "subscription is ready"},
			resultChecks: []string{
				"receiver rejects the first two deliveries",
				"receiver accepts the third delivery",
				"Retry-After delay is capped at two seconds",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timings := make(map[string]feature.Timing)
			for _, step := range tt.build().Steps {
				timings[step.Name] = step.T
			}

			for _, name := range tt.readiness {
				timing, ok := timings[name]
				require.Truef(t, ok, "step %q not found", name)
				require.Equal(t, feature.Requirement, timing)
			}

			for _, name := range append([]string{"send event"}, tt.resultChecks...) {
				timing, ok := timings[name]
				require.Truef(t, ok, "step %q not found", name)
				require.Equal(t, feature.Assert, timing)
			}
		})
	}
}
