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
	"net/http"
	"sync"
	"time"

	cejs "github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/protocol"
	"github.com/nats-io/nats.go"
	"go.opencensus.io/trace"
	"go.uber.org/zap"
	"knative.dev/eventing-natss/pkg/tracing"
	eventingchannels "knative.dev/eventing/pkg/channel"
	"knative.dev/eventing/pkg/channel/fanout"
	"knative.dev/eventing/pkg/kncloudevents"
	"knative.dev/pkg/logging"
)

const (
	jsmChannel = "jsm-channel"
)

var (
	ErrConsumerClosed = errors.New("dispatcher consumer closed")
)

type Consumer struct {
	sub              Subscription
	dispatcher       eventingchannels.MessageDispatcher
	reporter         eventingchannels.StatsReporter
	channelNamespace string

	jsSub *nats.Subscription

	logger *zap.SugaredLogger
	ctx    context.Context

	mu     sync.Mutex
	closed bool
}

func (c *Consumer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrConsumerClosed
	}

	c.closed = true

	// TODO: should we wait for messages to finish and c.jsSub.IsValid() to return false?
	return c.jsSub.Drain()
}

func (c *Consumer) MsgHandler(msg *nats.Msg) {
	logger := c.logger.With(zap.String("msg_id", msg.Header.Get(nats.MsgIdHdr)))
	ctx := logging.WithLogger(c.ctx, logger)
	tickerCtx, tickerCancel := context.WithCancel(c.ctx)

	tickerDone := make(chan struct{})

	go func() {
		defer close(tickerDone)

		// TODO(dan-j): this should be a fraction of the Consumer's AckWait
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-tickerCtx.Done():
				return
			case <-ticker.C:
				if err := msg.InProgress(nats.Context(tickerCtx)); err != nil && !errors.Is(err, context.Canceled) {
					logging.FromContext(ctx).Errorw("failed to mark message as in progress", zap.Error(err))
				}
			}
		}
	}()

	go func() {
		var result protocol.Result

		// wrap the handler in a local function so that the tickerCtx is cancelled even if a panic occurs.
		func() {
			defer tickerCancel()
			result = c.doHandle(ctx, msg)
		}()

		// wait for the ticker to stop to prevent attempts to mark the message as in progress after it has been acked
		// or nacked
		<-tickerDone

		switch {
		case protocol.IsACK(result):
			if err := msg.Ack(nats.Context(ctx)); err != nil {
				logger.Errorw("failed to Ack message after successful delivery to subscriber", zap.Error(err))
			}
		case protocol.IsNACK(result):
			if err := msg.Nak(nats.Context(ctx)); err != nil {
				logger.Errorw("failed to Nack message after failed delivery to subscriber", zap.Error(err))
			}
		default:
			if err := msg.Term(nats.Context(ctx)); err != nil {
				logger.Errorw("failed to Term message after failed delivery to subscriber", zap.Error(err))
			}
		}
	}()
}

// doHandle forwards the received event to the subscriber, the return has three outcomes:
// - Ack (includes `nil`): the event was successfully delivered to the subscriber
// - Nack: the event was not delivered to the subscriber, but it can be retried
// - any other error: the event should be terminated and not retried
func (c *Consumer) doHandle(ctx context.Context, msg *nats.Msg) protocol.Result {
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
		return errors.New("received a message with unknown encoding")
	}

	event := tracing.ConvertNatsMsgToEvent(c.logger.Desugar(), msg)
	additionalHeaders := tracing.ConvertEventToHttpHeader(event)

	sc, ok := tracing.ParseSpanContext(event)
	var span *trace.Span
	if !ok {
		c.logger.Warn("Cannot parse the spancontext, creating a new span")
		c.ctx, span = trace.StartSpan(c.ctx, jsmChannel+"-"+string(c.sub.UID))
	} else {
		c.ctx, span = trace.StartSpanWithRemoteParent(c.ctx, jsmChannel+"-"+string(c.sub.UID), sc)
	}

	defer span.End()

	te := kncloudevents.TypeExtractorTransformer("")

	dispatchExecutionInfo, err := c.dispatcher.DispatchMessageWithRetries(
		ctx,
		message,
		additionalHeaders,
		c.sub.Subscriber,
		c.sub.Reply,
		c.sub.DeadLetter,
		c.sub.RetryConfig,
		&te,
	)

	args := eventingchannels.ReportArgs{
		Ns:        c.channelNamespace,
		EventType: string(te),
	}
	_ = fanout.ParseDispatchResultAndReportMetrics(fanout.NewDispatchResult(err, dispatchExecutionInfo), c.reporter, args)

	if err != nil {
		logger.Errorw("failed to forward message to downstream subscriber",
			zap.Error(err),
			zap.Any("dispatch_resp_code", dispatchExecutionInfo.ResponseCode))

		code := dispatchExecutionInfo.ResponseCode
		if code/100 == 5 || code == http.StatusTooManyRequests || code == http.StatusRequestTimeout {
			// tell JSM to redeliver the message later
			return protocol.NewReceipt(false, "%w", err)
		}

		// let knative decide what to do with the message, if it wraps an Ack/Nack then that is what will happen,
		// otherwise we will Terminate the message
		return err
	}

	logger.Debug("message forwarded to downstream subscriber")

	return protocol.ResultACK
}
