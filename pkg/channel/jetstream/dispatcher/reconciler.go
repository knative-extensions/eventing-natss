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
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	v1 "knative.dev/eventing/pkg/apis/duck/v1"
	"knative.dev/eventing/pkg/channel"
	"knative.dev/eventing/pkg/channel/fanout"
	"knative.dev/eventing/pkg/kncloudevents"
	"knative.dev/pkg/apis/duck"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	pkgreconciler "knative.dev/pkg/reconciler"

	"knative.dev/eventing-natss/pkg/client/clientset/versioned"
	commonerr "knative.dev/eventing-natss/pkg/common/error"

	"knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	jsmreconciler "knative.dev/eventing-natss/pkg/client/injection/reconciler/messaging/v1alpha1/natsjetstreamchannel"
)

const (
	// Name of the corev1.Events emitted from the reconciliation process.

	ReasonJetstreamStreamCreated   = "JetstreamStreamCreated"
	ReasonJetstreamStreamFailed    = "JetstreamStreamFailed"
	ReasonJetstreamConsumerCreated = "JetstreamConsumerCreated"
	ReasonJetstreamConsumerFailed  = "JetstreamConsumerFailed"
)

// Reconciler reconciles incoming NatsJetstreamChannel CRDs by ensuring the following states:
// - Creates a JSM Stream for each channel
// - Creates a HTTP listener which publishes received events to the Stream
// - Creates a consumer for each .spec.subscribers[] and forwards events to the subscriber address
type Reconciler struct {
	clientSet  versioned.Interface
	js         nats.JetStreamManager
	dispatcher *Dispatcher

	streamNameFunc   StreamNameFunc
	consumerNameFunc ConsumerNameFunc
}

var _ jsmreconciler.Interface = (*Reconciler)(nil)
var _ jsmreconciler.ReadOnlyInterface = (*Reconciler)(nil)
var _ jsmreconciler.Finalizer = (*Reconciler)(nil)

func (r *Reconciler) ReconcileKind(ctx context.Context, nc *v1alpha1.NatsJetStreamChannel) pkgreconciler.Event {
	logger := logging.FromContext(ctx)
	logger.Debugw("ReconcileKind for channel")

	if err := r.reconcileStream(ctx, nc); err != nil {
		logger.Errorw("failed to reconcile stream", zap.Error(err))
		return err
	}

	if err := r.syncChannel(ctx, nc, true); err != nil {
		logger.Errorw("failed to syncChannel", zap.Error(err))
		return err
	}

	return r.reconcileSubscriberStatuses(ctx, nc)
}

func (r *Reconciler) reconcileSubscriberStatuses(ctx context.Context, nc *v1alpha1.NatsJetStreamChannel) error {
	logger := logging.FromContext(ctx)
	after := nc.DeepCopy()
	after.Status.Subscribers = make([]v1.SubscriberStatus, len(nc.Spec.Subscribers))
	for i, s := range nc.Spec.Subscribers {
		slog := logger.With(zap.String("sub_uid", string(s.UID)))
		slog.Debugw("reconciling subscriber status")

		after.Status.Subscribers[i] = v1.SubscriberStatus{
			UID:                s.UID,
			ObservedGeneration: s.Generation,
			Ready:              corev1.ConditionUnknown,
		}

		_, err := r.js.ConsumerInfo(r.streamNameFunc(nc), r.consumerNameFunc(string(s.UID)))
		if err != nil {
			after.Status.Subscribers[i].Ready = corev1.ConditionFalse
			if errors.Is(err, nats.ErrConsumerNotFound) {
				after.Status.Subscribers[i].Message = "Consumer does not exist"
			} else {
				slog.Errorw("failed to retrieve ConsumerInfo", zap.Error(err))
				after.Status.Subscribers[i].Message = fmt.Sprintf("Failed to query Consumer Info: %s", err.Error())
			}

			continue
		}

		after.Status.Subscribers[i].Ready = corev1.ConditionTrue
	}

	jsonPatch, err := duck.CreatePatch(nc, after)
	if err != nil {
		return fmt.Errorf("failed to create JSON patch: %w", err)
	}

	// If there is nothing to patch, we are good, just return.
	// Empty patch is [], hence we check for that.
	if len(jsonPatch) == 0 {
		return nil
	}

	patch, err := jsonPatch.MarshalJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal patch to JSON: %w", err)
	}

	patched, err := r.clientSet.MessagingV1alpha1().
		NatsJetStreamChannels(nc.Namespace).
		Patch(ctx, nc.Name, types.JSONPatchType, patch, metav1.PatchOptions{}, "status")
	if err != nil {
		return fmt.Errorf("failed to patch subscriber status: %w", err)
	}

	logger.Debugw("patched resource", zap.Any("patch", patch), zap.Any("patched", patched))

	return nil
}

// ObserveKind is invoked when a NatsJetStreamChannel requires reconciliation but the dispatcher is not the leader.
// This will wait until the channel has been marked "ready" by the leader, then subscribe to the JSM stream to forward
// to Knative subscriptions; this enables us to scale dispatchers horizontally to cope with large message volumes.
// The only requirement to allow this to work is the use of Queue Subscribers.
func (r *Reconciler) ObserveKind(ctx context.Context, nc *v1alpha1.NatsJetStreamChannel) pkgreconciler.Event {
	logger := logging.FromContext(ctx)
	logger.Debugw("ObserveKind for channel", zap.String("channel", nc.Name))

	return r.syncChannel(ctx, nc, false)
}

// FinalizeKind is invoked when the resource is set for deletion. This method is only called when the controller is
// leader, so unsubscribe all consumers and then delete the stream.
func (r *Reconciler) FinalizeKind(ctx context.Context, nc *v1alpha1.NatsJetStreamChannel) (err pkgreconciler.Event) {
	logger := logging.FromContext(ctx)
	logger.Debugw("FinalizeKind for channel", zap.String("channel", nc.Name))

	defer func() {
		if err2 := r.js.DeleteStream(r.streamNameFunc(nc)); err2 != nil {
			if err == nil {
				err = err2
			} else {
				err = fmt.Errorf("failed to DeleteStream after failure to ReconcileConsumers(): %s: %w", err2.Error(), err)
			}
		}
	}()

	config := r.newConfigFromChannel(nc)

	// clear subscriptions then ReconcileConsumers will just clean everything up for us
	config.Subscriptions = nil

	return r.dispatcher.ReconcileConsumers(ctx, config, true)
}

// ObserveDeletion is called on non-leader controllers after the actual resource is deleted. In this case we just
// unsubscribe from all consumers, the leader will clean the actual JetStream resources up for us via FinalizeKind.
func (r *Reconciler) ObserveDeletion(ctx context.Context, key types.NamespacedName) error {
	logger := logging.FromContext(ctx)
	logger.Debugw("ObserveDeletion for channel", zap.String("channel", key.Name))

	// ChannelConfig will be missing a load of fields because the NatsJetStreamChannel has been deleted so there's no
	// way to know the stream and consumer template, however since there are no subscriptions this is going to cause the
	// method to just unsubscribe from everything, which doesn't need any of those fields set.
	return r.dispatcher.ReconcileConsumers(ctx, ChannelConfig{
		ChannelReference: channel.ChannelReference{
			Namespace: key.Namespace,
			Name:      key.Name,
		},
	}, false)
}

// reconcileStream ensures that the stream exists with the correct configuration.
func (r *Reconciler) reconcileStream(ctx context.Context, nc *v1alpha1.NatsJetStreamChannel) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	streamName := r.streamNameFunc(nc)
	primarySubject := r.dispatcher.publishSubjectFunc(nc.Namespace, nc.Name)

	existing, err := r.js.StreamInfo(streamName)
	if err != nil && !errors.Is(err, nats.ErrStreamNotFound) {
		logger.Errorw("failed to check current stream info")
		return err
	}

	streamConfig := buildStreamConfig(streamName, primarySubject, nc.Spec.Stream.Config)
	isCreating := existing == nil

	// AddStream is idempotent if the config is identical to that on the server
	info, err := r.js.AddStream(streamConfig)
	if err != nil {
		logger.Errorw("failed to add stream")
		controller.GetEventRecorder(ctx).Event(nc, corev1.EventTypeWarning, ReasonJetstreamStreamFailed, err.Error())
		nc.Status.MarkStreamFailed("DispatcherCreateStreamFailed", "Failed to create JetStream stream")
		return err
	}

	if isCreating {
		logger.Infow("jetstream stream created", zap.String("stream_name", info.Config.Name))
		controller.GetEventRecorder(ctx).Event(nc, corev1.EventTypeNormal, ReasonJetstreamStreamCreated, "JetStream stream created")
	}

	nc.Status.MarkStreamTrue()

	return nil
}

func (r *Reconciler) syncChannel(ctx context.Context, nc *v1alpha1.NatsJetStreamChannel, isLeader bool) pkgreconciler.Event {
	logger := logging.FromContext(ctx).With(zap.Bool("is_leader", isLeader))

	if !nc.Status.IsReady() {
		logger.Debugw("NatsJetStreamChannel still not ready, short-circuiting the reconciler", zap.String("channel", nc.Name))
		return nil
	}

	config := r.newConfigFromChannel(nc)

	// Update receiver side
	if err := r.dispatcher.RegisterChannelHost(config); err != nil {
		logger.Errorw("failed to update host to channel map in dispatcher")
		return err
	}

	// Update dispatcher side
	err := r.dispatcher.ReconcileConsumers(logging.WithLogger(ctx, logger), config, isLeader)
	if err != nil {
		logger.Errorw("failure occurred during reconcile consumers", zap.Error(err))

		if errs, ok := err.(commonerr.SubscriberErrors); ok {
			for _, subErr := range errs {
				controller.GetEventRecorder(ctx).Event(
					nc,
					corev1.EventTypeWarning,
					ReasonJetstreamConsumerFailed,
					fmt.Sprintf("Failed to create subscriber for UID: %s: %s", subErr.UID, subErr.Err.Error()),
				)
			}

			// don't let the controller report extra information due to subscriber error, just requeue in 10 seconds.
			return controller.NewRequeueAfter(10 * time.Second)
		}

		// if an unexpected err occurred, let the controller decide how
		return err
	}

	return nil
}

// newConfigFromChannel creates a new ChannelConfig from a NatsJetStreamChannel for use in the Dispatcher.
func (r *Reconciler) newConfigFromChannel(nc *v1alpha1.NatsJetStreamChannel) ChannelConfig {
	channelConfig := ChannelConfig{
		ChannelReference: channel.ChannelReference{
			Namespace: nc.Namespace,
			Name:      nc.Name,
		},
		StreamName:             r.streamNameFunc(nc),
		HostName:               nc.Status.Address.URL.Host,
		ConsumerConfigTemplate: nc.Spec.ConsumerConfigTemplate,
	}
	if nc.Spec.SubscribableSpec.Subscribers != nil {
		newSubs := make([]Subscription, len(nc.Spec.SubscribableSpec.Subscribers))
		for i, source := range nc.Spec.SubscribableSpec.Subscribers {
			innerSub, _ := fanout.SubscriberSpecToFanoutConfig(source)

			// This functionality cannot be configured via the Subscription CRD. The default implementation is
			// kncloudevents.RetryIfGreaterThan300 which is not ideal behaviour since we will retry on bad requests.
			// This may be improved in the future with a more specific implementation, but consumers which wish the
			// event be redelivered should respond with a 429 Too Many Requests code.
			//
			// We could leverage JetStream's ability to redeliver messages to a consumer, but this would require
			// not using DispatchMessageWithRetries, and translate the subscription's delivery configuration into
			// the JetStream ConsumerConfig. We would then use dispatcher.Consumer's MsgHandler function to handle
			// whether to ack, nack or term the message.
			if innerSub.RetryConfig != nil {
				innerSub.RetryConfig.CheckRetry = kncloudevents.SelectiveRetry
			}

			newSubs[i] = Subscription{
				Subscription: *innerSub,
				UID:          source.UID,
			}
		}
		channelConfig.Subscriptions = newSubs
	}

	return channelConfig
}
