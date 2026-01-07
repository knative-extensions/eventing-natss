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
	"github.com/cloudevents/sdk-go/v2/binding/spec"
	"github.com/cloudevents/sdk-go/v2/protocol"
	"github.com/cloudevents/sdk-go/v2/types"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/logging"

	jsutils "knative.dev/eventing-natss/pkg/channel/jetstream/utils"
	"knative.dev/eventing-natss/pkg/tracing"
	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	"knative.dev/eventing/pkg/eventfilter"
	"knative.dev/eventing/pkg/eventfilter/attributes"
	"knative.dev/eventing/pkg/eventfilter/subscriptionsapi"
	"knative.dev/eventing/pkg/kncloudevents"
)

var retryMax int32 = 3
var retryTimeout = "PT1S"
var retryBackoffDelay = "PT0.5S"

// TypeExtractorTransformer extracts the CloudEvent type from a message.
// Copied from channel/jetstream/dispatcher to avoid importing that package
// which registers channel informers.
type TypeExtractorTransformer string

func (a *TypeExtractorTransformer) Transform(reader binding.MessageMetadataReader, _ binding.MessageMetadataWriter) error {
	_, ty := reader.GetAttribute(spec.Type)
	if ty != nil {
		tyParsed, err := types.ToString(ty)
		if err != nil {
			return err
		}
		*a = TypeExtractorTransformer(tyParsed)
	}
	return nil
}

var retryDelivery = eventingduckv1.BackoffPolicyLinear
var defaultRetry, _ = kncloudevents.RetryConfigFromDeliverySpec(eventingduckv1.DeliverySpec{
	Retry:         &retryMax,
	Timeout:       &retryTimeout,
	BackoffPolicy: &retryDelivery,
	BackoffDelay:  &retryBackoffDelay,
})

// TriggerHandler handles message dispatch for a single trigger
type TriggerHandler struct {
	logger *zap.SugaredLogger
	ctx    context.Context

	// Trigger configuration
	trigger    *eventingv1.Trigger
	subscriber duckv1.Addressable
	filter     eventfilter.Filter

	// Broker ingress URL for reply events
	brokerIngressURL *duckv1.Addressable

	// Dispatcher for sending events
	dispatcher *kncloudevents.Dispatcher

	// Retry configuration
	retryConfig   *kncloudevents.RetryConfig
	noRetryConfig *kncloudevents.RetryConfig

	// Dead letter sink
	deadLetterSink *duckv1.Addressable

	subscription *nats.Subscription
	consumer     *nats.ConsumerInfo
}

// NewTriggerHandler creates a new handler for a trigger
func NewTriggerHandler(
	ctx context.Context,
	trigger *eventingv1.Trigger,
	subscriber duckv1.Addressable,
	brokerIngressURL *duckv1.Addressable,
	deadLetterSink *duckv1.Addressable,
	retryConfig *kncloudevents.RetryConfig,
	noRetryConfig *kncloudevents.RetryConfig,
	dispatcher *kncloudevents.Dispatcher,
) (*TriggerHandler, error) {
	logger := logging.FromContext(ctx).With(
		zap.String("trigger", trigger.Name),
		zap.String("namespace", trigger.Namespace),
	)

	return &TriggerHandler{
		logger:           logger,
		ctx:              ctx,
		trigger:          trigger,
		subscriber:       subscriber,
		filter:           buildTriggerFilter(logger, trigger),
		brokerIngressURL: brokerIngressURL,
		dispatcher:       dispatcher,
		retryConfig:      retryConfig,
		noRetryConfig:    noRetryConfig,
		deadLetterSink:   deadLetterSink,
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
		zap.String("subscriber", h.subscriber.URL.String()),
		zap.String("type", event.Type()),
		zap.String("source", event.Source()),
		zap.String("id", event.ID()),
	)

	dispatchInfo, err := h.dispatchEvent(ctx, event, msg)
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

	additionalHeaders := tracing.ConvertEventToHttpHeader(event)
	te := TypeExtractorTransformer("")

	// Get retry number from message metadata
	retryNumber := 1
	if meta, err := msg.Metadata(); err == nil {
		retryNumber = int(meta.NumDelivered)
	}

	// Determine if this is the last try
	maxRetries := 1
	if h.retryConfig != nil {
		maxRetries = h.retryConfig.RetryMax
	}
	lastTry := retryNumber > maxRetries

	// Dispatch the message to trigger's destination
	dispatchInfo, err := h.dispatcher.SendEvent(ctx, *event, h.subscriber,
		kncloudevents.WithHeader(additionalHeaders),
		kncloudevents.WithTransformers(&te),
		kncloudevents.WithRetryConfig(h.noRetryConfig),
	)

	result := determineNatsResult(dispatchInfo.ResponseCode, err)

	// Handle ack/nack/term based on result
	switch {
	case protocol.IsACK(result):
		// process reply url case first
		if h.brokerIngressURL != nil {
			// TODO: should we retry in-memory reply url or go with re-delivery of the message?
			replyDispatchInfo, replyErr := h.dispatcher.SendEvent(ctx, *event, *h.brokerIngressURL,
				kncloudevents.WithRetryConfig(&defaultRetry),
				kncloudevents.WithHeader(additionalHeaders),
				kncloudevents.WithTransformers(&te),
			)
			if replyErr != nil {
				logger.Errorw("failed to send reply to broker ingress",
					zap.Error(replyErr),
					zap.Int("response_code", replyDispatchInfo.ResponseCode),
				)
			}
		}
		if err := msg.Ack(nats.Context(ctx)); err != nil {
			logger.Errorw("failed to ack message", zap.Error(err))
		}
	case protocol.IsNACK(result):
		if lastTry {
			if h.deadLetterSink != nil {
				// Send to dead letter sink
				dlsDispatchInfo, dlsErr := h.dispatcher.SendEvent(ctx, *event, *h.deadLetterSink,
					kncloudevents.WithRetryConfig(&defaultRetry),
					kncloudevents.WithHeader(additionalHeaders),
					kncloudevents.WithTransformers(&te),
				)
				if dlsErr != nil {
					logger.Errorw("failed to send to dead letter sink",
						zap.Error(dlsErr),
						zap.Int("response_code", dlsDispatchInfo.ResponseCode),
					)
				}
			}

			// Ack after DLS attempt
			if err := msg.Ack(nats.Context(ctx)); err != nil {
				logger.Errorw("failed to ack message after last retry", zap.Error(err))
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
		if lastTry && h.deadLetterSink != nil {
			// Send to dead letter sink
			dlsDispatchInfo, dlsErr := h.dispatcher.SendEvent(ctx, *event, *h.deadLetterSink,
				kncloudevents.WithRetryConfig(&defaultRetry),
				kncloudevents.WithHeader(additionalHeaders),
				kncloudevents.WithTransformers(&te),
			)
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

func determineNatsResult(responseCode int, err error) protocol.Result {
	result := protocol.ResultACK
	if err != nil {
		code := responseCode
		if code/100 == 5 || code == http.StatusTooManyRequests || code == http.StatusRequestTimeout {
			// Retriable error, effectively this is nats protocol NACK
			result = protocol.NewReceipt(false, "%w", err)
		} else {
			// Non-retriable error
			result = err
		}
	}
	return result
}

// buildTriggerFilter builds a filter from the trigger spec.
// Priority:
// 1. trigger.Spec.Filters (new subscriptions API filters) - if defined
// 2. trigger.Spec.Filter (legacy attributes filter) - if defined
// 3. nil (pass all events) - if neither is defined
func buildTriggerFilter(logger *zap.SugaredLogger, trigger *eventingv1.Trigger) eventfilter.Filter {
	switch {
	case len(trigger.Spec.Filters) > 0:
		// Use new subscriptions API filters
		logger.Debugw("using subscriptions API filters",
			zap.Any("filters", trigger.Spec.Filters),
		)
		return subscriptionsapi.CreateSubscriptionsAPIFilters(logger.Desugar(), trigger.Spec.Filters)
	case trigger.Spec.Filter != nil && trigger.Spec.Filter.Attributes != nil:
		// Use legacy attributes filter
		logger.Debugw("using legacy attributes filter",
			zap.Any("filter", trigger.Spec.Filter),
		)
		return attributes.NewAttributesFilter(trigger.Spec.Filter.Attributes)
	default:
		// No filter defined, pass all events
		logger.Debugw("no filter defined, passing all events")
		return nil
	}
}
