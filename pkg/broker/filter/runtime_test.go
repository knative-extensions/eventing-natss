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
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/nats-io/nats.go"
)

type runtimeNATSConnection struct {
	mu     sync.RWMutex
	status nats.Status
}

func (c *runtimeNATSConnection) Status() nats.Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

func (c *runtimeNATSConnection) setStatus(status nats.Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status = status
}

func runtimeProbeStatus(handler http.HandlerFunc) int {
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	return recorder.Code
}

func TestRuntimeReadinessReflectsNATSState(t *testing.T) {
	signalCtx, cancelSignal := context.WithCancel(context.Background())
	defer cancelSignal()
	runtime := NewRuntime(signalCtx)
	if got := runtimeProbeStatus(runtime.ReadinessHandler()); got != http.StatusServiceUnavailable {
		t.Fatalf("starting runtime readiness = %d, want %d", got, http.StatusServiceUnavailable)
	}

	conn := &runtimeNATSConnection{status: nats.CONNECTED}
	runtime.Attach(conn)
	for _, test := range []struct {
		status nats.Status
		want   int
	}{
		{status: nats.CONNECTED, want: http.StatusOK},
		{status: nats.CONNECTING, want: http.StatusServiceUnavailable},
		{status: nats.DISCONNECTED, want: http.StatusServiceUnavailable},
		{status: nats.RECONNECTING, want: http.StatusServiceUnavailable},
		{status: nats.DRAINING_SUBS, want: http.StatusServiceUnavailable},
		{status: nats.DRAINING_PUBS, want: http.StatusServiceUnavailable},
		{status: nats.CLOSED, want: http.StatusServiceUnavailable},
	} {
		t.Run(test.status.String(), func(t *testing.T) {
			conn.setStatus(test.status)
			if got := runtimeProbeStatus(runtime.ReadinessHandler()); got != test.want {
				t.Errorf("readiness for NATS %s = %d, want %d", test.status, got, test.want)
			}
		})
	}

	conn.setStatus(nats.CONNECTED)
	cancelSignal()
	if got := runtimeProbeStatus(runtime.ReadinessHandler()); got != http.StatusServiceUnavailable {
		t.Errorf("shutting-down runtime readiness = %d, want %d", got, http.StatusServiceUnavailable)
	}
}

func TestRuntimeLivenessAllowsReconnectButRejectsClosedAndShutdown(t *testing.T) {
	signalCtx, cancelSignal := context.WithCancel(context.Background())
	runtime := NewRuntime(signalCtx)
	conn := &runtimeNATSConnection{status: nats.RECONNECTING}
	runtime.Attach(conn)

	if got := runtimeProbeStatus(runtime.LivenessHandler()); got != http.StatusOK {
		t.Errorf("reconnecting liveness = %d, want %d", got, http.StatusOK)
	}
	conn.setStatus(nats.CLOSED)
	if got := runtimeProbeStatus(runtime.LivenessHandler()); got != http.StatusInternalServerError {
		t.Errorf("closed liveness = %d, want %d", got, http.StatusInternalServerError)
	}
	conn.setStatus(nats.CONNECTED)
	cancelSignal()
	if got := runtimeProbeStatus(runtime.LivenessHandler()); got != http.StatusInternalServerError {
		t.Errorf("shutdown liveness = %d, want %d", got, http.StatusInternalServerError)
	}
}
