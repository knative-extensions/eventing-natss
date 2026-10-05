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
	"testing"
)

func TestEnvConfig_MaxConcurrency(t *testing.T) {
	tests := []struct {
		name               string
		configMaxConcur    int
		wantMaxConcurrency int
	}{
		{
			name:               "zero uses default",
			configMaxConcur:    0,
			wantMaxConcurrency: DefaultMaxConcurrency,
		},
		{
			name:               "positive value is used",
			configMaxConcur:    40,
			wantMaxConcurrency: 40,
		},
		{
			name:               "negative uses default",
			configMaxConcur:    -1,
			wantMaxConcurrency: DefaultMaxConcurrency,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			maxConcurrency := DefaultMaxConcurrency

			if tt.configMaxConcur > 0 {
				maxConcurrency = tt.configMaxConcur
			}

			if maxConcurrency != tt.wantMaxConcurrency {
				t.Errorf("maxConcurrency = %v, want %v", maxConcurrency, tt.wantMaxConcurrency)
			}
		})
	}
}
