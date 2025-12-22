/*
Copyright 2024 The Knative Authors

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

	cejs "github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/protocol"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/logging"

	"knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher"
	jsutils "knative.dev/eventing-natss/pkg/channel/jetstream/utils"
	"knative.dev/eventing-natss/pkg/tracing"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	"knative.dev/eventing/pkg/eventfilter"
	"knative.dev/eventing/pkg/eventfilter/attributes"
	"knative.dev/eventing/pkg/kncloudevents"
)

// TriggerHandler handles message dispatch for a single trigger
type TriggerHandler struct {
	logger *zap.SugaredLogger
	ctx    context.Context

	// Trigger configuration
	trigger       *eventingv1.Trigger
	subscriberURI string
	filter        eventfilter.Filter

	// Broker ingress URL for reply events
	brokerIngressURL string

	// Dispatcher for sending events
	dispatcher *kncloudevents.Dispatcher

	// Retry configuration
	retryConfig *kncloudevents.RetryConfig

	// Dead letter sink
	deadLetterSinkURI string

	natsSub          *nats.Subscription
	natsConsumerInfo *nats.ConsumerInfo
}

// NewTriggerHandler creates a new handler for a trigger
func NewTriggerHandler(
	ctx context.Context,
	trigger *eventingv1.Trigger,
	subscriberURI string,
	brokerIngressURL string,
	deadLetterSinkURI string,
	retryConfig *kncloudevents.RetryConfig,
	dispatcher *kncloudevents.Dispatcher,
) (*TriggerHandler, error) {
	logger := logging.FromContext(ctx).With(
		zap.String("trigger", trigger.Name),
		zap.String("namespace", trigger.Namespace),
	)

	// Build the filter from trigger spec
	var filter eventfilter.Filter
	if trigger.Spec.Filter != nil && trigger.Spec.Filter.Attributes != nil {
		filter = attributes.NewAttributesFilter(trigger.Spec.Filter.Attributes)
	}

	return &TriggerHandler{
		logger:            logger,
		ctx:               ctx,
		trigger:           trigger,
		subscriberURI:     subscriberURI,
		filter:            filter,
		brokerIngressURL:  brokerIngressURL,
		dispatcher:        dispatcher,
		retryConfig:       retryConfig,
		deadLetterSinkURI: deadLetterSinkURI,
	}, nil
}

// HandleMessage processes a NATS message, applies filter, and dispatches to subscriber.
// With pull-based subscriptions, this is called synchronously from the fetch loop.
func (h *TriggerHandler) HandleMessage(msg *nats.Msg) {
	logger := h.logger.With(zap.String("msg_id", msg.Header.Get(nats.MsgIdHdr)))
	ctx := logging.WithLogger(h.ctx, logger)

	h.doHandle(ctx, msg)
}

// doHandle processes the message synchronously
func (h *TriggerHandler) doHandle(ctx context.Context, msg *nats.Msg) {
	logger := logging.FromContext(ctx)

	// Convert NATS message to CloudEvents message
	message := cejs.NewMessage(msg)
	if message.ReadEncoding() == binding.EncodingUnknown {
		logger.Errorw("received a message with unknown encoding")
		if err := msg.Term(); err != nil {
			logger.Errorw("failed to terminate message", zap.Error(err))
		}
		return
	}

	// Convert to CloudEvent for filtering
	event, err := binding.ToEvent(ctx, message)
	if err != nil {
		logger.Errorw("failed to convert message to CloudEvent", zap.Error(err))
		if err := msg.Term(); err != nil {
			logger.Errorw("failed to terminate message", zap.Error(err))
		}
		return
	}

	// event := tracing.ConvertNatsMsgToEvent(c.logger.Desugar(), msg)
	additionalHeaders := tracing.ConvertEventToHttpHeader(event)

	// Apply filter
	if h.filter != nil {
		filterResult := h.filter.Filter(ctx, *event)
		if filterResult == eventfilter.FailFilter {
			logger.Debugw("event filtered out",
				zap.String("type", event.Type()),
				zap.String("source", event.Source()),
			)
			// Ack the message since it was intentionally filtered
			if err := msg.Ack(); err != nil {
				logger.Errorw("failed to ack filtered message", zap.Error(err))
			}
			return
		}
	}

	// Dispatch to subscriber
	logger.Debugw("dispatching event to subscriber",
		zap.String("subscriber", h.subscriberURI),
		zap.String("type", event.Type()),
		zap.String("source", event.Source()),
		zap.String("id", event.ID()),
	)

	// Build destination addressable
	subscriberURL, err := apis.ParseURL(h.subscriberURI)
	if err != nil {
		logger.Errorw("failed to parse subscriber URI", zap.Error(err))
		if err := msg.Term(); err != nil {
			logger.Errorw("failed to terminate message", zap.Error(err))
		}
		return
	}
	destination := duckv1.Addressable{URL: subscriberURL}

	// Build reply addressable for broker ingress
	var reply *duckv1.Addressable
	if h.brokerIngressURL != "" {
		if replyURL, err := apis.ParseURL(h.brokerIngressURL); err == nil {
			reply = &duckv1.Addressable{URL: replyURL}
		}
	}

	// Build dead letter sink addressable
	var deadLetterSink *duckv1.Addressable
	if h.deadLetterSinkURI != "" {
		if dlsURL, err := apis.ParseURL(h.deadLetterSinkURI); err == nil {
			deadLetterSink = &duckv1.Addressable{URL: dlsURL}
		}
	}

	te := dispatcher.TypeExtractorTransformer("")

	dispatchInfo, err := dispatcher.SendMessage(
		h.dispatcher,
		ctx,
		message,
		destination,
		h.natsConsumerInfo.Config.AckWait,
		msg,
		dispatcher.WithReply(reply),
		dispatcher.WithDeadLetterSink(deadLetterSink),
		dispatcher.WithRetryConfig(h.retryConfig),
		dispatcher.WithTransformers(&te),
		dispatcher.WithHeader(additionalHeaders),
	)
	// dispatchInfo, err := h.dispatchEvent(ctx, event, msg)
	if err != nil {
		logger.Errorw("failed to dispatch event",
			zap.Error(err),
			zap.Int("response_code", dispatchInfo.ResponseCode),
		)
		return
	}

	logger.Debugw("event dispatched successfully",
		zap.Int("response_code", dispatchInfo.ResponseCode),
	)
}

// dispatchEvent sends the event to the subscriber and handles ack/nack
func (h *TriggerHandler) dispatchEvent(ctx context.Context, event *cloudevents.Event, msg *nats.Msg) (*kncloudevents.DispatchInfo, error) {
	logger := logging.FromContext(ctx)

	// Build destination
	subscriberURL, err := apis.ParseURL(h.subscriberURI)
	if err != nil {
		return &kncloudevents.DispatchInfo{}, err
	}
	destination := duckv1.Addressable{URL: subscriberURL}

	// Build dead letter sink addressable
	var deadLetterSink *duckv1.Addressable
	if h.deadLetterSinkURI != "" {
		if dlsURL, err := apis.ParseURL(h.deadLetterSinkURI); err == nil {
			deadLetterSink = &duckv1.Addressable{URL: dlsURL}
		}
	}

	// Get retry number from message metadata
	retryNumber := 1
	if meta, err := msg.Metadata(); err == nil {
		retryNumber = int(meta.NumDelivered)
	}

	// Determine if this is the last try
	maxRetries := 3
	if h.retryConfig != nil {
		maxRetries = h.retryConfig.RetryMax
	}
	lastTry := retryNumber > maxRetries

	// Dispatch the message
	dispatchInfo, err := h.dispatcher.SendEvent(ctx, *event, destination)
	if dispatchInfo == nil {
		dispatchInfo = &kncloudevents.DispatchInfo{}
	}

	// Process the result
	result := protocol.ResultACK
	if err != nil {
		code := dispatchInfo.ResponseCode
		if code/100 == 5 || code == http.StatusTooManyRequests || code == http.StatusRequestTimeout {
			// Retriable error
			result = protocol.NewReceipt(false, "%w", err)
		} else {
			// Non-retriable error
			result = err
		}
	}

	// Handle ack/nack/term based on result
	switch {
	case protocol.IsACK(result):
		if err := msg.Ack(nats.Context(ctx)); err != nil {
			logger.Errorw("failed to ack message", zap.Error(err))
		}
	case protocol.IsNACK(result):
		if lastTry && deadLetterSink != nil {
			// Send to dead letter sink
			dlsDispatchInfo, dlsErr := h.dispatcher.SendEvent(ctx, *event, *deadLetterSink)
			if dlsErr != nil {
				logger.Errorw("failed to send to dead letter sink",
					zap.Error(dlsErr),
					zap.Int("response_code", dlsDispatchInfo.ResponseCode),
				)
			}
			// Ack after DLS attempt
			if err := msg.Ack(nats.Context(ctx)); err != nil {
				logger.Errorw("failed to ack message after DLS", zap.Error(err))
			}
		} else {
			// Nack for retry
			nakDelay := jsutils.CalculateNakDelayForRetryNumber(retryNumber, h.retryConfig)
			if err := msg.NakWithDelay(nakDelay, nats.Context(ctx)); err != nil {
				logger.Errorw("failed to nack message", zap.Error(err))
			}
		}
	default:
		// Terminate - non-retriable error
		if lastTry && deadLetterSink != nil {
			// Send to dead letter sink
			dlsDispatchInfo, dlsErr := h.dispatcher.SendEvent(ctx, *event, *deadLetterSink)
			if dlsErr != nil {
				logger.Errorw("failed to send to dead letter sink",
					zap.Error(dlsErr),
					zap.Int("response_code", dlsDispatchInfo.ResponseCode),
				)
			}
		}
		if err := msg.Term(nats.Context(ctx)); err != nil {
			logger.Errorw("failed to term message", zap.Error(err))
		}
	}

	return dispatchInfo, err
}

// Cleanup releases resources
func (h *TriggerHandler) Cleanup() {
	if h.filter != nil {
		h.filter.Cleanup()
	}
}
