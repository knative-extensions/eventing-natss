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

	cejs "github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/nats-io/nats.go"
	"go.opencensus.io/trace"
	"go.uber.org/zap"
	"knative.dev/eventing-natss/pkg/tracing"
	eventingchannels "knative.dev/eventing/pkg/channel"
	"knative.dev/eventing/pkg/channel/fanout"
	"knative.dev/eventing/pkg/kncloudevents"
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
	if err := c.doHandle(msg); err != nil {
		c.logger.Errorw("failed to handle message", zap.Error(err))
		return
	}

	if err := msg.Ack(); err != nil {
		c.logger.Errorw("failed to Ack message after successful delivery to subscriber", zap.Error(err))
		return
	}
}

func (c *Consumer) doHandle(msg *nats.Msg) error {
	logger := c.logger.With(zap.String("msg_id", msg.Header.Get(nats.MsgIdHdr)))
	logger.Debugw("received message from JetStream consumer")
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
		c.ctx,
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

	logger.Debugw("message forward to downstream subscriber")
	return err
}
