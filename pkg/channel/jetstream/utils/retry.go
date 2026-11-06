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

import "net/http"

// IsRetriableStatusCode reports whether a delivery that failed with the given
// HTTP status code should be retried. It follows the Knative Eventing data
// plane specification, which retries 404, 408, 409, 429, and 5xx responses:
// https://github.com/knative/specs/blob/main/specs/eventing/data-plane.md#event-acknowledgement-and-delivery-retry
func IsRetriableStatusCode(code int) bool {
	return code/100 == 5 ||
		code == http.StatusNotFound ||
		code == http.StatusRequestTimeout ||
		code == http.StatusConflict ||
		code == http.StatusTooManyRequests
}
