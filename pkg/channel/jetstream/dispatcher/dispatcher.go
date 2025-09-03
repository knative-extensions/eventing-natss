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
	"bytes"
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"sync"

	"github.com/nats-io/nats.go"

	cejs "github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2"
	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/google/uuid"
	"go.opencensus.io/trace"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	commonce "knative.dev/eventing-natss/pkg/common/cloudevents"
	commonerr "knative.dev/eventing-natss/pkg/common/error"
	"knative.dev/eventing-natss/pkg/tracing"

	"knative.dev/eventing/pkg/auth"
	eventingchannels "knative.dev/eventing/pkg/channel"
	"knative.dev/eventing/pkg/eventingtls"
	"knative.dev/eventing/pkg/kncloudevents"
	"knative.dev/pkg/kmeta"
	"knative.dev/pkg/logging"
)

// Dispatcher is responsible for managing both directions of events over the NatsJetStreamChannel. It manages the
// lifecycle of the following components:
// - Stream per NatsJetStreamChannel
// - HTTP receiver which publishes to the desired Stream
// - Consumer per .spec.subscribers[] of a channel, forwarding events to the specified subscriber address.
type Dispatcher struct {
	receiver   *eventingchannels.EventReceiver
	dispatcher *kncloudevents.Dispatcher
	reporter   eventingchannels.StatsReporter

	js nats.JetStreamContext

	publishSubjectFunc  StreamSubjectFunc
	consumerNameFunc    ConsumerNameFunc
	consumerSubjectFunc ConsumerSubjectFunc

	// hostToChannelMap represents a map[string]eventingchannels.ChannelReference
	hostToChannelMap sync.Map

	consumerUpdateLock sync.Mutex
	channelSubscribers map[types.NamespacedName]sets.Set[string]
	consumers          map[types.UID]Consumer
}

func NewDispatcher(ctx context.Context, args NatsDispatcherArgs) (*Dispatcher, error) {
	logger := logging.FromContext(ctx)

	reporter := eventingchannels.NewStatsReporter(args.ContainerName, kmeta.ChildName(args.PodName, uuid.New().String()))

	oidcTokenProvider := auth.NewOIDCTokenProvider(ctx)
	d := &Dispatcher{
		dispatcher: kncloudevents.NewDispatcher(eventingtls.ClientConfig{}, oidcTokenProvider),
		reporter:   reporter,

		js: args.JetStream,

		publishSubjectFunc:  args.SubjectFunc,
		consumerNameFunc:    args.ConsumerNameFunc,
		consumerSubjectFunc: args.ConsumerSubjectFunc,

		channelSubscribers: make(map[types.NamespacedName]sets.Set[string]),
		consumers:          make(map[types.UID]Consumer),
	}

	receiverFunc, err := eventingchannels.NewEventReceiver(
		d.messageReceiver,
		logger.Desugar(),
		reporter,
		eventingchannels.ResolveChannelFromHostHeader(d.getChannelReferenceFromHost),
	)
	if err != nil {
		logger.Error("failed to create message receiver")
		return nil, err
	}

	d.receiver = receiverFunc

	return d, nil
}

func (d *Dispatcher) Start(ctx context.Context) error {
	if d.receiver == nil {
		return fmt.Errorf("message receiver not set")
	}

	return d.receiver.Start(ctx)
}

// RegisterChannelHost registers the NatsDispatcher to accept HTTP events matching the specified HostName
func (d *Dispatcher) RegisterChannelHost(config ChannelConfig) error {
	if old, ok := d.hostToChannelMap.LoadOrStore(config.HostName, config.ChannelReference); ok {
		// map already contained a channel reference for this hostname, check they both reference the same channel,
		// we only care about the Name/Namespace pair matching, since stream name is immutable.
		if old != config.ChannelReference {
			// if something is already there, but it's not the same channel, then fail
			return fmt.Errorf(
				"duplicate hostName found. Each channel must have a unique host header. HostName:%s, channel:%s.%s, channel:%s.%s",
				config.HostName,
				old.(eventingchannels.ChannelReference).Namespace,
				old.(eventingchannels.ChannelReference).Name,
				config.Namespace,
				config.Name,
			)
		}
	}

	return nil
}

func (d *Dispatcher) ReconcileConsumers(ctx context.Context, config ChannelConfig, isLeader bool) error {
	logger := logging.FromContext(ctx).With(zap.Bool("is_leader", isLeader))
	channelNamespacedName := types.NamespacedName{
		Namespace: config.Namespace,
		Name:      config.Name,
	}

	d.consumerUpdateLock.Lock()
	defer d.consumerUpdateLock.Unlock()

	currentSubs, ok := d.channelSubscribers[channelNamespacedName]
	if !ok {
		currentSubs = sets.New[string]()
	}
	expectedSubs := sets.New[string](config.SubscriptionsUIDs()...)
	logger.Infow("expected", zap.Any("expected_subs", expectedSubs))
	logger.Infow("current", zap.Any("current_subs", currentSubs))

	toAddSubs := expectedSubs.Difference(currentSubs)
	toRemoveSubs := currentSubs.Difference(expectedSubs)
	toUpdateSubs := currentSubs.Intersection(expectedSubs)

	nextSubs := sets.New[string]()
	var subErrs commonerr.SubscriberErrors
	for _, sub := range config.Subscriptions {

		uid := string(sub.UID)
		subLogger := logger.With(zap.String("sub_uid", uid))

		var (
			status SubscriberStatusType
			err    error
		)

		if toUpdateSubs.Has(uid) {
			subLogger.Debugw("updating existing subscription")
			_ = d.updateSubscription(logging.WithLogger(ctx, subLogger), config, sub, isLeader)
		}

		if toAddSubs.Has(uid) {
			subLogger.Debugw("subscription not configured for dispatcher, subscribing")
			status, err = d.subscribe(logging.WithLogger(ctx, subLogger), config, sub, isLeader)
		} else {
			subLogger.Debugw("subscription already up to date")
			status = SubscriberStatusTypeUpToDate
		}

		logger.Debugw("Subscription status after add/update", zap.Any("SubStatus", status))

		switch status {
		case SubscriberStatusTypeCreated, SubscriberStatusTypeUpToDate:
			nextSubs.Insert(uid)
		case SubscriberStatusTypeDeleted, SubscriberStatusTypeSkipped, SubscriberStatusTypeError:
			nextSubs.Delete(uid)
		}

		if err != nil {
			subErrs.AddError(uid, err)
		}
	}

	for _, toRemove := range toRemoveSubs.UnsortedList() {
		subLogger := logger.With(zap.String("sub_uid", toRemove))
		subLogger.Debugw("extraneous subscription configured for dispatcher, unsubscribing")
		err := d.unsubscribe(ctx, config, types.UID(toRemove), isLeader)
		if err != nil {
			subErrs.AddError(toRemove, err)
		}

		nextSubs.Delete(toRemove)
	}

	d.channelSubscribers[channelNamespacedName] = nextSubs

	// subErrs used to be a *SubscriberErrors but the return type is always non-nil since the interface type on the
	// return isn't the same as the type of subErrs: https://yourbasic.org/golang/gotcha-why-nil-error-not-equal-nil/
	if len(subErrs) > 0 {
		return subErrs
	}

	return nil
}

func (d *Dispatcher) updateSubscription(ctx context.Context, config ChannelConfig, sub Subscription, isLeader bool) error {
	logger := logging.FromContext(ctx)
	d.consumers[sub.UID].UpdateSubscription(&config, sub)
	consumerName := d.consumerNameFunc(string(sub.UID))

	if isLeader {
		deliverSubject := d.consumerSubjectFunc(config.Namespace, config.Name, string(sub.UID))

		consInfo, err := d.js.ConsumerInfo(config.StreamName, consumerName)
		if err != nil {
			logger.Warnw("failed to get consumer to update", zap.Error(err),
				zap.String("consumer", consumerName), zap.String("stream", config.StreamName))
		}

		var consumerConfig *nats.ConsumerConfig
		// we do not allow update existing consumers from push consumer to pull
		if isPushConsumer(consInfo) {
			consumerConfig = buildPushConsumerConfig(consumerName, deliverSubject, config.ConsumerConfigTemplate, sub.RetryConfig)
		} else {
			consumerConfig = buildPullConsumerConfig(consumerName, config.ConsumerConfigTemplate, sub.RetryConfig)
		}

		_, err = d.js.UpdateConsumer(config.StreamName, consumerConfig)
		if err != nil {
			logger.Errorw("failed to update queue subscription for consumer", zap.Error(err))
			return err
		}
	}

	return nil
}

func isPushConsumer(info *nats.ConsumerInfo) bool {
	return len(info.Config.DeliverSubject) > 0
}

func (d *Dispatcher) subscribe(ctx context.Context, config ChannelConfig, sub Subscription, isLeader bool) (SubscriberStatusType, error) {
	logger := logging.FromContext(ctx)

	info, err := d.getOrEnsureConsumer(ctx, config, sub, isLeader)
	logger.Debugw("ConsumerInfo created", zap.Any("ConsumerInfo", info))
	if err != nil {
		if errors.Is(err, nats.ErrConsumerNotFound) {
			//	this error can only occur if the dispatcher is not the leader
			logger.Infow("dispatcher not leader and consumer does not exist yet")
			return SubscriberStatusTypeSkipped, nil
		}

		logger.Errorw("failed to getOrEnsureConsumer during subscribe")
		return SubscriberStatusTypeError, err
	}

	var consumer Consumer
	if isPushConsumer(info) {
		pushConsumer := &PushConsumer{
			sub:              sub,
			dispatcher:       d.dispatcher,
			reporter:         d.reporter,
			channelNamespace: config.Namespace,
			logger:           logger,
			ctx:              ctx,
			natsConsumerInfo: info,
		}
		jsSub, err := d.js.QueueSubscribe(info.Config.DeliverSubject, info.Config.DeliverGroup, pushConsumer.MsgHandler,
			nats.Bind(info.Stream, info.Name), nats.ManualAck())
		if err != nil {
			logger.Errorw("failed to create queue subscription for consumer")
			return SubscriberStatusTypeError, err
		}
		pushConsumer.jsSub = jsSub
		consumer = pushConsumer
	} else {
		natsSub, err := d.js.PullSubscribe(".>", info.Config.Durable, nats.Bind(info.Stream, info.Name), nats.ManualAck())
		if err != nil {
			logger.Errorw("failed to pull subscribe to jetstream", zap.Error(err))
			return SubscriberStatusTypeError, err
		}
		consumer, err = NewPullConsumer(ctx, natsSub, sub, d.dispatcher, d.reporter, &config)
		if err != nil {
			logger.Errorw("failed to create pull consumer", zap.Error(err))
			return SubscriberStatusTypeError, err
		}
		err = consumer.(*PullConsumer).Start()
		if err != nil {
			logger.Errorw("failed to start pull consumer", zap.Error(err))
			return SubscriberStatusTypeError, err
		}
	}

	d.consumers[sub.UID] = consumer

	return 0, nil
}

func (d *Dispatcher) unsubscribe(ctx context.Context, config ChannelConfig, uid types.UID, isLeader bool) (err error) {
	consumer, ok := d.consumers[uid]
	if !ok {
		return fmt.Errorf(
			"unable to unsubscribe, Consumer is not present in consumers map for UID: %s",
			string(uid),
		)
	}

	delete(d.consumers, uid)

	defer func() {
		if isLeader {
			if err2 := d.deleteConsumer(ctx, config, string(uid)); err2 != nil {
				if err == nil {
					err = err2
				} else {
					err = fmt.Errorf("failed to deleteConsumer after failed consumer.Close(): %s: %w", err2.Error(), err)
				}
			}
		}
	}()

	return consumer.Close()
}

// getOrEnsureConsumer obtains the current ConsumerInfo for this Subscription, updating/creating one if the dispatcher
// is a leader.
func (d *Dispatcher) getOrEnsureConsumer(ctx context.Context, config ChannelConfig, sub Subscription, isLeader bool) (*nats.ConsumerInfo, error) {
	logger := logging.FromContext(ctx)

	consumerName := d.consumerNameFunc(string(sub.UID))

	if isLeader {
		deliverSubject := d.consumerSubjectFunc(config.Namespace, config.Name, string(sub.UID))
		consumerConfig := buildConsumerConfig(ctx, &config, consumerName, deliverSubject, sub.RetryConfig)

		// AddConsumer is idempotent so this will either create the consumer, update to match expected config, or no-op
		info, err := d.js.AddConsumer(config.StreamName, consumerConfig)
		if err != nil {
			logger.Errorw("failed to add consumer")
			return nil, err
		}

		return info, nil
	}

	// dispatcher isn't leader, try and retrieve an existing consumer
	return d.js.ConsumerInfo(config.StreamName, consumerName)
}

func (d *Dispatcher) deleteConsumer(ctx context.Context, config ChannelConfig, uid string) error {
	logger := logging.FromContext(ctx)
	consumerName := d.consumerNameFunc(uid)

	if err := d.js.DeleteConsumer(config.StreamName, consumerName); err != nil {
		logger.Errorw("failed to delete JetStream Consumer", zap.Error(err))
		return err
	}

	return nil
}

func (d *Dispatcher) messageReceiver(ctx context.Context, ch eventingchannels.ChannelReference, event event.Event, _ nethttp.Header) error {
	message := binding.ToMessage(&event)

	logger := logging.FromContext(ctx)
	logger.Debugw("received message from HTTP receiver", zap.Any("event", event))

	eventID := commonce.IDExtractorTransformer("")

	transformers := append([]binding.Transformer{&eventID},
		tracing.SerializeTraceTransformers(trace.FromContext(ctx).SpanContext())...,
	)

	ctx = ce.WithEncodingStructured(ctx)
	writer := new(bytes.Buffer)
	if _, err := cejs.WriteMsg(ctx, message, writer, transformers...); err != nil {
		logger.Error("failed to write binding.Message to bytes.Buffer")
		return err
	}

	subject := d.publishSubjectFunc(ch.Namespace, ch.Name)
	logger = logger.With(zap.String("msg_id", string(eventID)))
	logger.Debugw("parsed message into JetStream encoding, publishing to subject", zap.String("subj", subject))

	// publish the message, passing the cloudevents ID as the MsgId so that we can benefit from detecting duplicate
	// messages
	_, err := d.js.Publish(subject, writer.Bytes(), nats.MsgId(string(eventID)))
	if err != nil {
		logger.Errorw("failed to publish message", zap.Error(err))
		return err
	}

	logger.Debugw("successfully published message to JetStream")
	return nil
}

func (d *Dispatcher) getChannelReferenceFromHost(host string) (eventingchannels.ChannelReference, error) {
	cr, ok := d.hostToChannelMap.Load(host)
	if !ok {
		return eventingchannels.ChannelReference{}, eventingchannels.UnknownHostError(host)
	}
	return cr.(eventingchannels.ChannelReference), nil
}
