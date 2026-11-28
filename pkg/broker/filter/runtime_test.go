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
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

type consumerShutdownFunc func(context.Context) error

func (f consumerShutdownFunc) Shutdown(ctx context.Context) error {
	return f(ctx)
}

type runtimeNATSConnection struct {
	mu           sync.RWMutex
	status       nats.Status
	closed       []chan nats.Status
	recorder     *lifecycleRecorder
	drainErr     error
	closeOnDrain bool
}

func (c *runtimeNATSConnection) Status() nats.Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// StatusChanged only supports the CLOSED notification used by drainNATS.
func (c *runtimeNATSConnection) StatusChanged(...nats.Status) chan nats.Status {
	ch := make(chan nats.Status, 1)
	c.mu.Lock()
	c.closed = append(c.closed, ch)
	c.mu.Unlock()
	return ch
}

func (c *runtimeNATSConnection) setStatus(status nats.Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status = status
	if status == nats.CLOSED {
		for _, ch := range c.closed {
			select {
			case ch <- status:
			default:
			}
		}
	}
}

func (c *runtimeNATSConnection) Drain() error {
	if c.recorder != nil {
		c.recorder.record("nats-drain")
	}
	// Like (*nats.Conn).Drain, a closed connection rejects draining.
	if c.Status() == nats.CLOSED {
		return nats.ErrConnectionClosed
	}
	if c.drainErr == nil {
		c.setStatus(nats.DRAINING_SUBS)
		if c.closeOnDrain {
			c.setStatus(nats.CLOSED)
		}
	}
	return c.drainErr
}

func (c *runtimeNATSConnection) Close() {
	if c.recorder != nil {
		c.recorder.record("nats-close")
	}
	c.setStatus(nats.CLOSED)
}

// shutdownRuntimePastDeadline shuts down a Runtime that owns consumer and conn
// with a timeout the blocked work outlasts, and waits until the runtime has
// forced its shutdown.
func shutdownRuntimePastDeadline(t *testing.T, consumer consumerShutdowner, conn natsConnection, timeout time.Duration) *Runtime {
	t.Helper()
	runtime := NewRuntime(context.Background())
	runtime.Attach(consumer, conn)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := runtime.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want deadline exceeded", err)
	}
	receiveWithin(t, runtime.done, "runtime shutdown did not finish after its deadline")
	return runtime
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
	runtime.Attach(consumerShutdownFunc(func(context.Context) error { return nil }), conn)
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
	runtime.Attach(consumerShutdownFunc(func(context.Context) error { return nil }), conn)

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

func TestRuntimeShutdownOrderAndReadiness(t *testing.T) {
	recorder := &lifecycleRecorder{}
	consumerStarted := make(chan struct{})
	releaseConsumer := make(chan struct{})
	consumer := consumerShutdownFunc(func(context.Context) error {
		recorder.record("consumer-shutdown")
		close(consumerStarted)
		<-releaseConsumer
		recorder.record("consumer-done")
		return nil
	})
	conn := &runtimeNATSConnection{
		status:       nats.CONNECTED,
		recorder:     recorder,
		closeOnDrain: true,
	}
	runtime := NewRuntime(context.Background())
	runtime.Attach(consumer, conn)

	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- runtime.Shutdown(context.Background()) }()
	receiveWithin(t, consumerStarted, "consumer shutdown did not start")
	if got := runtimeProbeStatus(runtime.ReadinessHandler()); got != http.StatusServiceUnavailable {
		t.Errorf("readiness during shutdown = %d, want %d", got, http.StatusServiceUnavailable)
	}
	recorder.assertActions(t, "consumer-shutdown")

	close(releaseConsumer)
	if err := receiveWithin(t, shutdownResult, "Shutdown did not complete"); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case <-runtime.done:
	default:
		t.Fatal("Done was not closed when Shutdown returned")
	}
	recorder.assertActions(t, "consumer-shutdown", "consumer-done", "nats-drain")
}

func TestRuntimeShutdownIsConcurrentAndIdempotent(t *testing.T) {
	recorder := &lifecycleRecorder{}
	consumer := consumerShutdownFunc(func(context.Context) error {
		recorder.record("consumer-shutdown")
		return nil
	})
	conn := &runtimeNATSConnection{status: nats.CONNECTED, recorder: recorder, closeOnDrain: true}
	runtime := NewRuntime(context.Background())
	runtime.Attach(consumer, conn)

	const callers = 20
	results := make(chan error, callers)
	for range callers {
		go func() { results <- runtime.Shutdown(context.Background()) }()
	}
	for range callers {
		if err := receiveWithin(t, results, "concurrent Shutdown caller did not return"); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	}
	recorder.assertActions(t, "consumer-shutdown", "nats-drain")
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Errorf("repeated Shutdown() error = %v", err)
	}
	recorder.assertActions(t, "consumer-shutdown", "nats-drain")
}

func TestRuntimeShutdownJoinsErrorsAndClosesAfterDrainFailure(t *testing.T) {
	consumerErr := errors.New("consumer shutdown failed")
	drainErr := errors.New("NATS drain failed")
	recorder := &lifecycleRecorder{}
	consumer := consumerShutdownFunc(func(context.Context) error {
		recorder.record("consumer-shutdown")
		return consumerErr
	})
	conn := &runtimeNATSConnection{
		status:   nats.CONNECTED,
		recorder: recorder,
		drainErr: drainErr,
	}
	runtime := NewRuntime(context.Background())
	runtime.Attach(consumer, conn)

	err := runtime.Shutdown(context.Background())
	if !errors.Is(err, consumerErr) || !errors.Is(err, drainErr) {
		t.Fatalf("Shutdown() error = %v, want joined consumer and drain errors", err)
	}
	recorder.assertActions(t, "consumer-shutdown", "nats-drain", "nats-close")
}

func TestRuntimeShutdownDeadlineForcesNATSClose(t *testing.T) {
	consumer := consumerShutdownFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	recorder := &lifecycleRecorder{}
	conn := &runtimeNATSConnection{status: nats.CONNECTED, recorder: recorder}
	runtime := shutdownRuntimePastDeadline(t, consumer, conn, 20*time.Millisecond)
	err := runtime.Shutdown(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("completed Shutdown() error = %v, want context deadline exceeded", err)
	}
	recorder.assertActions(t, "nats-drain", "nats-close")
}

func TestRuntimeShutdownBeforeAttachStillCleansLateResources(t *testing.T) {
	runtime := NewRuntime(context.Background())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := runtime.Shutdown(shutdownCtx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() before Attach error = %v, want context deadline exceeded", err)
	}

	consumerCalled := make(chan struct{})
	consumer := consumerShutdownFunc(func(context.Context) error {
		close(consumerCalled)
		return nil
	})
	recorder := &lifecycleRecorder{}
	conn := &runtimeNATSConnection{status: nats.CONNECTED, recorder: recorder, closeOnDrain: true}
	runtime.Attach(consumer, conn)

	receiveWithin(t, consumerCalled, "late-attached consumer was not shut down after shutdown had already started")
	receiveWithin(t, runtime.done, "late-attached resources did not finish shutdown")
	recorder.assertActions(t, "nats-drain")
}
