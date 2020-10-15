/*
Copyright 2019 The Knative Authors

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

package controller

import (
	"context"
	"fmt"
	messagingv1 "knative.dev/eventing/pkg/apis/messaging/v1"
	"strings"

	duckv1 "knative.dev/pkg/apis/duck/v1"
	pkgreconciler "knative.dev/pkg/reconciler"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	"knative.dev/eventing/pkg/kncloudevents"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"

	"knative.dev/eventing-natss/pkg/apis/messaging/v1beta1"
	clientset "knative.dev/eventing-natss/pkg/client/clientset/versioned"
	"knative.dev/eventing-natss/pkg/client/injection/client"
	"knative.dev/eventing-natss/pkg/client/injection/informers/messaging/v1beta1/natsschannel"
	natsschannelreconciler "knative.dev/eventing-natss/pkg/client/injection/reconciler/messaging/v1beta1/natsschannel"
	listers "knative.dev/eventing-natss/pkg/client/listers/messaging/v1beta1"
	"knative.dev/eventing-natss/pkg/dispatcher"
	"knative.dev/eventing-natss/pkg/util"
)

const (
	// controllerAgentName is the string used by this controller to identify
	// itself when creating events.
	controllerAgentName = "natss-ch-dispatcher"

	finalizerName = controllerAgentName
)

// Reconciler reconciles NATSS Channels.
type Reconciler struct {
	natssDispatcher dispatcher.NatssDispatcher

	natssClientSet clientset.Interface

	natsschannelLister listers.NatssChannelLister
	impl               *controller.Impl
}

// Check that our Reconciler implements controller.Reconciler.
var _ natsschannelreconciler.Interface = (*Reconciler)(nil)
var _ natsschannelreconciler.Finalizer = (*Reconciler)(nil)

// NewController initializes the controller and is called by the generated code.
// Registers event handlers to enqueue events.
func NewController(ctx context.Context, _ configmap.Watcher) *controller.Impl {

	logger := logging.FromContext(ctx)

	natssConfig := util.GetNatssConfig()
	dispatcherArgs := dispatcher.Args{
		NatssURL:  util.GetDefaultNatssURL(),
		ClusterID: util.GetDefaultClusterID(),
		ClientID:  natssConfig.ClientID,
		Cargs: kncloudevents.ConnectionArgs{
			MaxIdleConns:        natssConfig.MaxIdleConns,
			MaxIdleConnsPerHost: natssConfig.MaxIdleConnsPerHost,
		},
		Logger: logger.Desugar(),
	}
	natssDispatcher, err := dispatcher.NewDispatcher(dispatcherArgs)
	if err != nil {
		logger.Fatal("Unable to create natss dispatcher", zap.Error(err))
	}

	logger = logger.With(zap.String("controller/impl", "pkg"))
	logger.Info("Starting the NATSS dispatcher")

	channelInformer := natsschannel.Get(ctx)

	r := &Reconciler{
		natssDispatcher:    natssDispatcher,
		natsschannelLister: channelInformer.Lister(),
		natssClientSet:     client.Get(ctx),
	}
	r.impl = natsschannelreconciler.NewImpl(ctx, r)

	logger.Info("Setting up event handlers")

	channelInformer.Informer().AddEventHandler(controller.HandleAll(r.impl.Enqueue))

	logger.Info("Starting dispatcher.")
	go func() {
		if err := natssDispatcher.Start(ctx); err != nil {
			logger.Errorw("Cannot start dispatcher", zap.Error(err))
		}
	}()
	return r.impl
}

// reconcile performs the following steps
// - update natss subscriptions
// - set NatssChannel SubscribableStatus
// - update host2channel map
func (r *Reconciler) ReconcileKind(ctx context.Context, natssChannel *v1beta1.NatssChannel) pkgreconciler.Event {
	// TODO update dispatcher API and use Channelable or NatssChannel.
	c := toChannel(natssChannel)

	// Try to subscribe.
	failedSubscriptions, err := r.natssDispatcher.UpdateSubscriptions(ctx, c, false)
	if err != nil {
		logging.FromContext(ctx).Errorw("Error updating subscriptions", zap.Any("channel", c), zap.Error(err))
		return err
	}

	natssChannel.Status.SubscribableStatus = r.createSubscribableStatus(natssChannel.Spec.Subscribers, failedSubscriptions)
	if len(failedSubscriptions) > 0 {
		var b strings.Builder
		for _, subError := range failedSubscriptions {
			b.WriteString("\n")
			b.WriteString(subError.Error())
		}
		errMsg := b.String()
		logging.FromContext(ctx).Error(errMsg)
		return fmt.Errorf(errMsg)
	}

	natssChannels, err := r.natsschannelLister.List(labels.Everything())
	if err != nil {
		logging.FromContext(ctx).Error("Error listing natss channels")
		return err
	}

	channels := make([]messagingv1.Channel, 0)
	for _, nc := range natssChannels {
		if nc.Status.IsReady() {
			channels = append(channels, *toChannel(nc))
		}
	}

	if err := r.natssDispatcher.ProcessChannels(ctx, channels); err != nil {
		logging.FromContext(ctx).Errorw("Error updating host to channel map", zap.Error(err))
		return err
	}

	return nil
}

func (r *Reconciler) FinalizeKind(ctx context.Context, c *v1beta1.NatssChannel) pkgreconciler.Event {

	if _, err := r.natssDispatcher.UpdateSubscriptions(ctx, toChannel(c), true); err != nil {
		logging.FromContext(ctx).Errorw("Error updating subscriptions", zap.Any("channel", c), zap.Error(err))
		return err
	}
	return nil
}

// createSubscribableStatus creates the SubscribableStatus based on the failedSubscriptions
// checks for each subscriber on the natss channel if there is a failed subscription on natss side
// if there is no failed subscription => set ready status
func (r *Reconciler) createSubscribableStatus(subscribers []eventingduckv1.SubscriberSpec, failedSubscriptions map[eventingduckv1.SubscriberSpec]error) eventingduckv1.SubscribableStatus {
	subscriberStatus := make([]eventingduckv1.SubscriberStatus, 0)
	for _, sub := range subscribers {
		status := eventingduckv1.SubscriberStatus{
			UID:                sub.UID,
			ObservedGeneration: sub.Generation,
			Ready:              corev1.ConditionTrue,
		}

		if err, ok := failedSubscriptions[sub]; ok {
			status.Ready = corev1.ConditionFalse
			status.Message = err.Error()
		}
		subscriberStatus = append(subscriberStatus, status)
	}
	return eventingduckv1.SubscribableStatus{
		Subscribers: subscriberStatus,
	}
}

func toChannel(natssChannel *v1beta1.NatssChannel) *messagingv1.Channel {
	channel := &messagingv1.Channel{
		ObjectMeta: v1.ObjectMeta{
			Name:      natssChannel.Name,
			Namespace: natssChannel.Namespace,
		},
		Spec: messagingv1.ChannelSpec{
			ChannelTemplate: nil,
			ChannelableSpec: eventingduckv1.ChannelableSpec{
				SubscribableSpec: eventingduckv1.SubscribableSpec{},
			},
		},
	}

	if natssChannel.Status.Address != nil {
		channel.Status = messagingv1.ChannelStatus{
			ChannelableStatus: eventingduckv1.ChannelableStatus{
				AddressStatus: duckv1.AddressStatus{
					Address: &duckv1.Addressable{
						URL: natssChannel.Status.Address.URL,
					}},
				SubscribableStatus: eventingduckv1.SubscribableStatus{},
				DeadLetterChannel:  nil,
			},
			Channel: nil,
		}
	}

	for _, s := range natssChannel.Spec.Subscribers {
		sbeta1 := eventingduckv1.SubscriberSpec{
			UID:           s.UID,
			Generation:    s.Generation,
			SubscriberURI: s.SubscriberURI,
			ReplyURI:      s.ReplyURI,
			Delivery:      s.Delivery,
		}
		channel.Spec.Subscribers = append(channel.Spec.Subscribers, sbeta1)
	}

	return channel
}
