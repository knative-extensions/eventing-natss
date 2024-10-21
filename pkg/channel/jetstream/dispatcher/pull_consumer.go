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
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	cejs "github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2"
	"github.com/cloudevents/sdk-go/v2/binding"

	"go.opencensus.io/trace"
	"knative.dev/eventing-natss/pkg/tracing"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
	eventingchannels "knative.dev/eventing/pkg/channel"
	"knative.dev/eventing/pkg/channel/fanout"
	"knative.dev/eventing/pkg/kncloudevents"
	"knative.dev/pkg/logging"
)

const (
	fetchBatchSizeEnv = "FETCH_BATCH_SIZE"
)

var (
	// FetchBatchSize is the number of messages that will be fetched from JetStream in a single
	// request. This can be configured via the FETCH_BATCH_SIZE environment variable.
	//
	// If you expect to process a high-volume of messages, you may want to increase this number to
	// reduce the number of requests made to JetStream.
	FetchBatchSize = 32

	FetchMaxWaitDefault = 200 * time.Millisecond
)

func init() {
	fetchSize, err := strconv.Atoi(os.Getenv(fetchBatchSizeEnv))
	if err == nil {
		FetchBatchSize = fetchSize
	}
}

type PullConsumer struct {
	dispatcher       *kncloudevents.Dispatcher
	reporter         eventingchannels.StatsReporter
	channelNamespace string

	natsConsumer     *nats.Subscription
	natsConsumerInfo *nats.ConsumerInfo

	logger *zap.SugaredLogger

	sub   Subscription
	subMu sync.RWMutex

	lifecycleMu sync.Mutex
	started     bool
	closing     chan struct{}
	closed      chan struct{}
}

func NewPullConsumer(ctx context.Context, consumer *nats.Subscription, subscription Subscription, dispatcher *kncloudevents.Dispatcher, reporter eventingchannels.StatsReporter, channelConfig *ChannelConfig) (*PullConsumer, error) {
	consumerInfo, err := consumer.ConsumerInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get consumer info: %w", err)
	}

	logger := logging.FromContext(ctx)
	updatePullSubscriptionConfig(channelConfig, &subscription)

	return &PullConsumer{
		dispatcher:       dispatcher,
		reporter:         reporter,
		channelNamespace: channelConfig.Namespace,

		natsConsumer:     consumer,
		natsConsumerInfo: consumerInfo,

		logger: logger,

		sub: subscription,

		closing: make(chan struct{}),
		closed:  make(chan struct{}),
	}, nil
}

func (c *PullConsumer) ConsumerType() ConsumerType {
	return PullConsumerType
}

// Start begins the consumer and handles messages until Close is called. This method is blocking and
// will return an error if the consumer fails prematurely. A nil error will be returned upon being
// stopped by the Close method.
func (c *PullConsumer) Start() error {
	if err := c.checkStart(); err != nil {
		return err
	}
	defer close(c.closed)

	ctx := logging.WithLogger(context.Background(), c.logger)

	var wg sync.WaitGroup

	// wait for handlers to finish
	defer wg.Wait()

	// all messages are attached with this ctx, upon closing the consumer, we will cancel this ctx
	// so that all pending messages are cancelled. This should cause any pending requests be
	// cancelled (which also results in a nack) and the batch to be drained.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for {
		batch, err := c.natsConsumer.FetchBatch(FetchBatchSize, nats.MaxWait(c.sub.PullSubscription.FetchMaxWait))
		if err != nil {
			c.logger.Errorw("Failed to fetch messages", zap.Error(err), zap.String("consumer", c.sub.Name))
		}

		if err := c.consumeMessages(ctx, batch, &wg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return err
		}
	}
}

// consumeMessages consumes messages from the batch and forwards them to the queue. The enqueued
// message includes a context which will cancel after the AckWait duration of the consumer.
//
// This method returns once the MessageBatch has been consumed, or upon a call to Consumer.Close.
// Returning as a result of Consumer.Close results in an io.EOF error.
func (c *PullConsumer) consumeMessages(ctx context.Context, batch nats.MessageBatch, wg *sync.WaitGroup) error {
	for {
		select {
		case natsMsg, ok := <-batch.Messages():
			if !ok {
				return nil
			}

			wg.Add(1)

			go func() {
				defer wg.Done()
				ctx := logging.WithLogger(ctx, c.logger.With(zap.String("msg_id", natsMsg.Header.Get(nats.MsgIdHdr))))
				//msg := internal.NewMessage(ctx, natsMsg, c.natsConsumerInfo.Config.AckWait)

				if err := c.handleMessage(ctx, natsMsg); err != nil {
					// handleMessage only errors if the message cannot be finished, any other error
					// is consumed by msg.Finish(err)
					logging.FromContext(ctx).Errorw("failed to finish message", zap.Error(err))
				}
			}()
		case <-c.closing:
			return io.EOF
		}
	}
}

func (c *PullConsumer) UpdateSubscription(config *ChannelConfig, sub Subscription) {
	// wait for any pending messages to be processed with the old subscription
	c.subMu.Lock()
	defer c.subMu.Unlock()

	c.sub = sub
	updatePullSubscriptionConfig(config, &sub)
}

func updatePullSubscriptionConfig(config *ChannelConfig, sub *Subscription) {
	if sub.PullSubscription == nil {
		sub.PullSubscription = &PullSubscription{}
	}

	sub.PullSubscription.FetchBatchSize = config.ConsumerConfigTemplate.FetchBatchSize
	if config.ConsumerConfigTemplate.FetchBatchSize == 0 {
		sub.PullSubscription.FetchBatchSize = FetchBatchSize
	}

	sub.PullSubscription.FetchMaxWait = config.ConsumerConfigTemplate.FetchMaxWait.Duration
	if config.ConsumerConfigTemplate.FetchMaxWait.Duration == 0 {
		sub.PullSubscription.FetchMaxWait = FetchMaxWaitDefault
	}
}

func (c *PullConsumer) handleMessage(ctx context.Context, msg *nats.Msg) (err error) {
	// ensure that c.sub is not modified while we are handling a message
	c.subMu.RLock()
	defer c.subMu.RUnlock()

	ctx, finish := context.WithTimeout(ctx, c.natsConsumerInfo.Config.AckWait)
	defer finish()

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

	sc, ok := tracing.ParseSpanContext(event)
	var span *trace.Span
	if !ok {
		c.logger.Warn("Cannot parse the spancontext, creating a new span")
		ctx, span = trace.StartSpan(ctx, jsmChannel+"-"+string(c.sub.UID))
	} else {
		ctx, span = trace.StartSpanWithRemoteParent(ctx, jsmChannel+"-"+string(c.sub.UID), sc)
	}

	defer span.End()

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

	_ = fanout.ParseDispatchResultAndReportMetrics(fanout.NewDispatchResult(err, dispatchExecutionInfo), c.reporter, eventingchannels.ReportArgs{
		Ns:        c.channelNamespace,
		EventType: string(te),
	})

	if err != nil {
		logger.Errorw("failed to forward message to downstream subscriber",
			zap.Error(err),
			zap.Any("dispatch_resp_code", dispatchExecutionInfo.ResponseCode))

		// let knative decide what to do with the message, if it wraps an Ack/Nack then that is what will happen,
		// otherwise we will Terminate the message
		return err
	}

	logger.Debugw("dispatched message to subscriber",
		zap.Int("response_code", dispatchExecutionInfo.ResponseCode))

	return nil
}

func (c *PullConsumer) Close() error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	close(c.closing)

	<-c.closed

	// allow reusing the consumer - not sure if this is required but adds negligible overhead.
	c.started = false
	c.closing = make(chan struct{})
	c.closed = make(chan struct{})

	return nil
}

// checkStart ensures that the consumer is not already running and marks it as started.
func (c *PullConsumer) checkStart() error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	if c.started {
		return errors.New("consumer already started")
	}

	c.started = true

	return nil
}
