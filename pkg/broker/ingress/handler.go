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

package ingress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	cejs "github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2"
	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	cehttp "github.com/cloudevents/sdk-go/v2/protocol/http"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"knative.dev/eventing-natss/pkg/broker/contract"
	commonce "knative.dev/eventing-natss/pkg/common/cloudevents"
	"knative.dev/eventing-natss/pkg/tracing"
)

// defaultPublishTimeout bounds how long a request waits for a JetStream ack
// before the producer is asked to retry.
const defaultPublishTimeout = 30 * time.Second

// errPublishUnavailable indicates the event could not be durably stored due to
// backpressure or a transient JetStream error. It maps to HTTP 503 so the
// producer retries; deduplication (nats.MsgId + the stream's duplicate window)
// makes those retries safe.
var errPublishUnavailable = errors.New("jetstream publish unavailable")

// This is a shared handler that can route events to multiple brokers based on URL path
type Handler struct {
	logger *zap.SugaredLogger
	js     nats.JetStreamContext

	// publishTimeout bounds how long a request waits for a JetStream ack.
	publishTimeout time.Duration

	// Broker mappings protected by mutex
	mu      sync.RWMutex
	brokers map[string]contract.BrokerContract // path -> broker
}

// HandlerConfig contains configuration for creating a Handler
type HandlerConfig struct {
	Logger    *zap.SugaredLogger
	JetStream nats.JetStreamContext

	// PublishTimeout bounds how long a request waits for a JetStream ack before
	// asking the producer to retry. Defaults to defaultPublishTimeout when unset.
	PublishTimeout time.Duration
}

// NewHandler creates a new shared ingress handler
func NewHandler(config HandlerConfig) *Handler {
	publishTimeout := config.PublishTimeout
	if publishTimeout <= 0 {
		publishTimeout = defaultPublishTimeout
	}
	return &Handler{
		logger:         config.Logger,
		js:             config.JetStream,
		publishTimeout: publishTimeout,
		brokers:        make(map[string]contract.BrokerContract),
	}
}

// UpdateContract refreshes the handler's broker mapping from the contract
func (h *Handler) UpdateContract(c *contract.Contract) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.brokers = make(map[string]contract.BrokerContract)
	for _, broker := range c.Brokers {
		h.brokers[broker.Path] = broker
	}

	h.logger.Infow("Contract updated", zap.Int("broker_count", len(h.brokers)))
}

// GetBrokerCount returns the number of registered brokers
func (h *Handler) GetBrokerCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.brokers)
}

// getBrokerForPath returns the broker for the given path
func (h *Handler) getBrokerForPath(path string) (contract.BrokerContract, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	broker, ok := h.brokers[path]
	return broker, ok
}

// ServeHTTP implements http.Handler with path-based routing
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := h.logger

	// Only accept POST requests
	if r.Method != http.MethodPost {
		logger.Warnw("Received non-POST request", zap.String("method", r.Method))
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Extract broker from path: /{namespace}/{name}
	path := r.URL.Path
	broker, ok := h.getBrokerForPath(path)
	if !ok {
		logger.Warnw("Unknown broker path", zap.String("path", path))
		w.WriteHeader(http.StatusNotFound)
		return
	}

	logger = logger.With(
		zap.String("broker", broker.Name),
		zap.String("namespace", broker.Namespace),
	)

	// Convert HTTP request to CloudEvents message
	message := cehttp.NewMessageFromHttpRequest(r)
	defer message.Finish(nil)

	// Extract the event from the message
	event, err := binding.ToEvent(ctx, message)
	if err != nil {
		logger.Warnw("Failed to extract event from request", zap.Error(err))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Validate the event
	if err := event.Validate(); err != nil {
		logger.Warnw("Invalid CloudEvent", zap.Error(err))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	logger.Debugw("Received CloudEvent",
		zap.String("id", event.ID()),
		zap.String("type", event.Type()),
		zap.String("source", event.Source()),
	)

	// Publish to JetStream
	if err := h.publishToJetStream(ctx, event, broker); err != nil {
		if errors.Is(err, errPublishUnavailable) {
			// Backpressure or transient JetStream error: ask the producer to
			// retry rather than dropping the event as a hard failure.
			logger.Warnw("JetStream publish unavailable, asking producer to retry", zap.Error(err))
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		logger.Errorw("Failed to publish event to JetStream", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	logger.Debugw("Successfully published event to JetStream", zap.String("id", event.ID()))
	w.WriteHeader(http.StatusAccepted)
}

// publishToJetStream publishes a CloudEvent to the broker's JetStream stream
func (h *Handler) publishToJetStream(ctx context.Context, event *ce.Event, broker contract.BrokerContract) error {
	logger := h.logger.With(zap.String("msg_id", event.ID()))

	// Convert event to binding message
	message := binding.ToMessage(event)

	// Extract event ID for deduplication (populated by Transform() during WriteMsg)
	eventID := commonce.IDExtractorTransformer("")
	transformers := append([]binding.Transformer{&eventID},
		tracing.SerializeTraceTransformers(ctx)...,
	)

	// Encode the message for JetStream
	ctx = ce.WithEncodingStructured(ctx)
	writer := new(bytes.Buffer)
	if _, err := cejs.WriteMsg(ctx, message, writer, transformers...); err != nil {
		logger.Errorw("Failed to encode CloudEvent for JetStream", zap.Error(err))
		return err
	}

	// Build the subject name for publishing
	// Add .events suffix to match the stream's subject pattern (publishSubject.>)
	subject := broker.PublishSubject + ".events"

	// Publish asynchronously so many requests can be in flight at once, but wait
	// for the ack before returning: a 202 is only sent once JetStream has
	// durably stored the event. nats.MsgId enables server-side deduplication so
	// a producer retry after a timeout does not double-store the event.
	pubCtx, cancel := context.WithTimeout(ctx, h.publishTimeout)
	defer cancel()

	future, err := h.js.PublishAsync(subject, writer.Bytes(), nats.MsgId(string(eventID)))
	if err != nil {
		// Immediate failure, e.g. the in-flight window is full (backpressure).
		logger.Errorw("Failed to submit publish to JetStream",
			zap.Error(err),
			zap.String("subject", subject),
			zap.String("event_id", string(eventID)),
		)
		return fmt.Errorf("%w: %v", errPublishUnavailable, err)
	}

	select {
	case <-future.Ok():
		logger.Debugw("Published event to JetStream",
			zap.String("subject", subject),
			zap.String("event_id", string(eventID)),
		)
		return nil
	case ackErr := <-future.Err():
		logger.Errorw("JetStream rejected the published event",
			zap.Error(ackErr),
			zap.String("subject", subject),
			zap.String("event_id", string(eventID)),
		)
		return fmt.Errorf("%w: %v", errPublishUnavailable, ackErr)
	case <-pubCtx.Done():
		logger.Errorw("Timed out waiting for JetStream ack",
			zap.Error(pubCtx.Err()),
			zap.String("subject", subject),
			zap.String("event_id", string(eventID)),
		)
		return fmt.Errorf("%w: %v", errPublishUnavailable, pubCtx.Err())
	}
}

// ReadinessChecker returns an http.HandlerFunc for readiness checks
func (h *Handler) ReadinessChecker() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
}

// LivenessChecker returns an http.HandlerFunc for liveness checks
func (h *Handler) LivenessChecker() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
}
