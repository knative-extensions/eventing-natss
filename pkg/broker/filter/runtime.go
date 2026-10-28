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
	"net/http"

	"github.com/nats-io/nats.go"
)

type natsConnection interface {
	Status() nats.Status
}

// Runtime reports the filter's NATS connection and process health.
type Runtime struct {
	signalCtx context.Context
	conn      natsConnection
	attached  chan struct{}
}

func NewRuntime(signalCtx context.Context) *Runtime {
	return &Runtime{signalCtx: signalCtx, attached: make(chan struct{})}
}

// Attach makes the controller's NATS connection available to the probes.
// It must be called exactly once.
func (r *Runtime) Attach(conn natsConnection) {
	r.conn = conn
	close(r.attached)
}

// attachedConn returns the NATS connection, or nil before Attach.
func (r *Runtime) attachedConn() natsConnection {
	select {
	case <-r.attached:
		return r.conn
	default:
		return nil
	}
}

// ReadinessHandler reports ready only while the runtime is accepting work and
// the NATS connection is fully connected. Draining connections are not ready.
func (r *Runtime) ReadinessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if r.signalCtx.Err() != nil {
			http.Error(w, "filter is shutting down", http.StatusServiceUnavailable)
			return
		}

		conn := r.attachedConn()
		if conn == nil {
			http.Error(w, "filter runtime is not running", http.StatusServiceUnavailable)
			return
		}
		if status := conn.Status(); status != nats.CONNECTED {
			http.Error(w, fmt.Sprintf("NATS connection is %s", status), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// LivenessHandler keeps the process alive during recoverable NATS reconnects,
// but asks kubelet to restart a terminally closed connection. It also preserves
// sharedmain's default behavior of failing once SIGTERM is received.
func (r *Runtime) LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if r.signalCtx.Err() != nil {
			http.Error(w, "filter is shutting down", http.StatusInternalServerError)
			return
		}

		if conn := r.attachedConn(); conn != nil && conn.Status() == nats.CLOSED {
			http.Error(w, "NATS connection is closed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
