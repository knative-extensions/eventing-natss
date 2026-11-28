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

package utils

import (
	"net/http"
	"testing"
)

func TestIsRetriableStatusCode(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{code: http.StatusNotFound, want: true},
		{code: http.StatusRequestTimeout, want: true},
		{code: http.StatusConflict, want: true},
		{code: http.StatusTooManyRequests, want: true},
		{code: http.StatusInternalServerError, want: true},
		{code: http.StatusServiceUnavailable, want: true},
		{code: 599, want: true},
		{code: http.StatusMultipleChoices, want: false},
		{code: http.StatusBadRequest, want: false},
		{code: http.StatusUnauthorized, want: false},
		{code: http.StatusForbidden, want: false},
		{code: http.StatusRequestEntityTooLarge, want: false},
		{code: http.StatusUnprocessableEntity, want: false},
		{code: 0, want: false},
		{code: -1, want: false},
	}
	for _, tt := range tests {
		if got := IsRetriableStatusCode(tt.code); got != tt.want {
			t.Errorf("IsRetriableStatusCode(%d) = %v, want %v", tt.code, got, tt.want)
		}
	}
}
