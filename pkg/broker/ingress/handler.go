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
	"net/http"

	cejs "github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2"
	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	cehttp "github.com/cloudevents/sdk-go/v2/protocol/http"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	brokerutils "knative.dev/eventing-natss/pkg/broker/utils"
	commonce "knative.dev/eventing-natss/pkg/common/cloudevents"
	"knative.dev/eventing-natss/pkg/tracing"
)

// Handler handles incoming CloudEvents and publishes them to JetStream
type Handler struct {
	logger     *zap.SugaredLogger
	js         nats.JetStreamContext
	brokerName string
	namespace  string
	streamName string
}

// HandlerConfig contains configuration for creating a Handler
type HandlerConfig struct {
	Logger     *zap.SugaredLogger
	JetStream  nats.JetStreamContext
	BrokerName string
	Namespace  string
	StreamName string
}

// NewHandler creates a new ingress handler
func NewHandler(config HandlerConfig) *Handler {
	return &Handler{
		logger:     config.Logger,
		js:         config.JetStream,
		brokerName: config.BrokerName,
		namespace:  config.Namespace,
		streamName: config.StreamName,
	}
}

// ServeHTTP implements http.Handler
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := h.logger

	// Only accept POST requests
	if r.Method != http.MethodPost {
		logger.Warnw("Received non-POST request", zap.String("method", r.Method))
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

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
	if err := h.publishToJetStream(ctx, event); err != nil {
		logger.Errorw("Failed to publish event to JetStream", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	logger.Debugw("Successfully published event to JetStream", zap.String("id", event.ID()))
	w.WriteHeader(http.StatusAccepted)
}

// publishToJetStream publishes a CloudEvent to the broker's JetStream stream
func (h *Handler) publishToJetStream(ctx context.Context, event *ce.Event) error {
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
	subject := brokerutils.BrokerPublishSubjectName(h.namespace, h.brokerName) + ".events"

	// Publish to JetStream with message ID for deduplication
	_, err := h.js.Publish(subject, writer.Bytes(), nats.MsgId(string(eventID)))
	if err != nil {
		logger.Errorw("Failed to publish to JetStream",
			zap.Error(err),
			zap.String("subject", subject),
			zap.String("event_id", string(eventID)),
		)
		return err
	}

	logger.Debugw("Published event to JetStream",
		zap.String("subject", subject),
		zap.String("event_id", string(eventID)),
	)

	return nil
}

// ReadinessChecker returns an http.HandlerFunc for readiness checks
func (h *Handler) ReadinessChecker() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check JetStream connection by getting stream info
		_, err := h.js.StreamInfo(h.streamName)
		if err != nil {
			h.logger.Warnw("Readiness check failed: stream not available", zap.Error(err))
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// LivenessChecker returns an http.HandlerFunc for liveness checks
func (h *Handler) LivenessChecker() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
}
