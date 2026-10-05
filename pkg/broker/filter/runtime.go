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
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"

	"knative.dev/eventing-natss/pkg/broker/constants"
)

const (
	// ShutdownTimeout leaves five seconds of
	// constants.FilterTerminationGracePeriod for kubelet and process-level
	// cleanup.
	ShutdownTimeout = constants.FilterTerminationGracePeriod - 5*time.Second

	// natsDrainReserve is the end of the shutdown deadline kept for draining
	// the NATS connection after the consumer manager shuts down.
	natsDrainReserve = 5 * time.Second
)

type consumerShutdowner interface {
	Shutdown(context.Context) error
}

type natsConnection interface {
	Status() nats.Status
	StatusChanged(statuses ...nats.Status) chan nats.Status
	Drain() error
	Close()
}

// Runtime owns the filter's ConsumerManager and NATS connection so process
// shutdown and readiness reflect the data plane rather than only the controller
// work queue.
type Runtime struct {
	signalCtx context.Context
	// stopping is set by the first Shutdown call, which starts runShutdown.
	stopping atomic.Bool

	// consumer and conn are written once before attached is closed and only
	// read after it.
	consumer consumerShutdowner
	conn     natsConnection
	attached chan struct{}

	// done is closed after consumer and NATS shutdown have completed or timed out.
	done chan struct{}
	// err is written before done is closed and only read after it.
	err error
}

func NewRuntime(signalCtx context.Context) *Runtime {
	return &Runtime{
		signalCtx: signalCtx,
		attached:  make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// Attach transfers ownership of the consumer manager and NATS connection to
// the Runtime. A shutdown that arrived during controller construction waits for
// this handoff rather than leaking resources created after the signal. It must
// be called exactly once.
func (r *Runtime) Attach(consumer consumerShutdowner, conn natsConnection) {
	r.consumer = consumer
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
		if r.stopping.Load() || conn == nil {
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

// Shutdown is idempotent. The first caller starts shutdown; all callers wait
// for that same result or their own context deadline.
func (r *Runtime) Shutdown(ctx context.Context) error {
	if r.stopping.CompareAndSwap(false, true) {
		go r.runShutdown(ctx)
	}

	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) runShutdown(ctx context.Context) {
	defer close(r.done)

	// Wait even if the initiating caller's context expires. If shutdown races
	// controller construction, a later Attach must still close the resources;
	// it will receive the already-canceled context and take the forced path.
	<-r.attached

	consumerCtx, cancelConsumer := reserveDeadline(ctx, natsDrainReserve)
	consumerErr := r.consumer.Shutdown(consumerCtx)
	cancelConsumer()

	r.err = errors.Join(consumerErr, drainNATS(ctx, r.conn))
}

// reserveDeadline returns a context that expires reserve before ctx's deadline
// (immediately if that point has passed), or follows ctx when it has none.
func reserveDeadline(ctx context.Context, reserve time.Duration) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, deadline.Add(-reserve))
}

func drainNATS(ctx context.Context, conn natsConnection) error {
	// Register before draining so the CLOSED transition that ends the drain
	// cannot be missed.
	closed := conn.StatusChanged(nats.CLOSED)
	if err := conn.Drain(); err != nil {
		if errors.Is(err, nats.ErrConnectionClosed) {
			return nil
		}
		conn.Close()
		return err
	}

	select {
	case <-closed:
		return nil
	case <-ctx.Done():
		if conn.Status() == nats.CLOSED {
			return nil
		}
		conn.Close()
		return ctx.Err()
	}
}
