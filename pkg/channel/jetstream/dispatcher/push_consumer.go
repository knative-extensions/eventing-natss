/*
Copyright 2021 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dispatcher

import (
	"context"
	"errors"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/eventing/pkg/kncloudevents"

	cejs "github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
	"knative.dev/eventing-natss/pkg/tracing"
	"knative.dev/eventing/pkg/observability"
	eventingtracing "knative.dev/eventing/pkg/tracing"
	"knative.dev/pkg/logging"
)

const (
	jsmChannel = "jsm-channel"
	scopeName  = "knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher"
)

var (
	ErrConsumerClosed = errors.New("dispatcher consumer closed")
	latencyBounds     = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
)

type PushConsumer struct {
	sub              Subscription
	dispatcher       *kncloudevents.Dispatcher
	channelNamespace string
	channelName      string
	dispatchDuration metric.Float64Histogram
	tracer           trace.Tracer

	jsSub            *nats.Subscription
	natsConsumerInfo *nats.ConsumerInfo

	logger *zap.SugaredLogger
	ctx    context.Context

	mu     sync.Mutex
	closed bool
}

func (c *PushConsumer) ConsumerType() ConsumerType {
	return PushConsumerType
}

func (c *PushConsumer) UpdateSubscription(_ *ChannelConfig, sub Subscription) {
	//// wait for any pending messages to be processed with the old subscription
	//c.subMu.Lock()
	//defer c.subMu.Unlock()
	c.sub = sub
}

func (c *PushConsumer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrConsumerClosed
	}

	c.closed = true

	// TODO: should we wait for messages to finish and c.jsSub.IsValid() to return false?
	return c.jsSub.Drain()
}

func (c *PushConsumer) MsgHandler(msg *nats.Msg) {
	go func() {
		logger := c.logger.With(zap.String("msg_id", msg.Header.Get(nats.MsgIdHdr)))
		ctx := logging.WithLogger(c.ctx, logger)

		c.doHandle(ctx, msg)
	}()
}

// doHandle forwards the received event to the subscriber, the return has three outcomes:
// - Ack (includes `nil`): the event was successfully delivered to the subscriber
// - Nack: the event was not delivered to the subscriber, but it can be retried
// - any other error: the event should be terminated and not retried
func (c *PushConsumer) doHandle(ctx context.Context, msg *nats.Msg) {
	logger := logging.FromContext(ctx)

	if logger.Desugar().Core().Enabled(zap.DebugLevel) {
		var debugKVs []interface{}
		if meta, err := msg.Metadata(); err == nil {
			debugKVs = append(debugKVs, zap.Any("msg_metadata", meta))
		}

		logger.Debugw("received message from JetStream consumer", debugKVs...)
	}

	message := cejs.NewMessage(msg)
	if message.ReadEncoding() == binding.EncodingUnknown {
		logger.Errorw("received a message with unknown encoding")
		return
	}

	event := tracing.ConvertNatsMsgToEvent(c.logger.Desugar(), msg)
	additionalHeaders := tracing.ConvertEventToHttpHeader(event)

	ctx = observability.WithChannelLabels(ctx, types.NamespacedName{Name: c.channelName, Namespace: c.channelNamespace})
	ctx = observability.WithMessagingLabels(
		ctx,
		eventingtracing.SubscriptionMessagingDestination(
			types.NamespacedName{
				Name:      c.sub.Name,
				Namespace: c.sub.Namespace,
			},
		),
		"send",
	)
	ctx = observability.WithMinimalEventLabels(ctx, event)

	ctx = tracing.ParseSpanContext(ctx, event)
	ctx, span := c.tracer.Start(ctx, jsmChannel+"-"+string(c.sub.UID))
	defer func() {
		if span.IsRecording() {
			// add full event labels here so that they only populate the span, and not any metrics
			ctx = observability.WithEventLabels(ctx, event)
			labeler, _ := otelhttp.LabelerFromContext(ctx)
			span.SetAttributes(labeler.Get()...)
		}
		span.End()
	}()

	te := TypeExtractorTransformer("")

	dispatchExecutionInfo, err := SendMessage(
		c.dispatcher,
		ctx,
		message,
		c.sub.Subscriber,
		c.natsConsumerInfo.Config.AckWait,
		msg,
		WithReply(c.sub.Reply),
		WithDeadLetterSink(c.sub.DeadLetter),
		WithRetryConfig(c.sub.RetryConfig),
		WithTransformers(&te),
		WithHeader(additionalHeaders),
	)

	labeler, _ := otelhttp.LabelerFromContext(ctx)

	c.dispatchDuration.Record(
		ctx,
		dispatchExecutionInfo.Duration.Seconds(),
		metric.WithAttributes(labeler.Get()...),
	)

	if err != nil {
		logger.Errorw("failed to forward message to downstream subscriber",
			zap.Error(err),
			zap.Any("dispatch_resp_code", dispatchExecutionInfo.ResponseCode))
	}

	logger.Debug("message forwarded to downstream subscriber")
}
