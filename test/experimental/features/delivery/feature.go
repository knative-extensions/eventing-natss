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

package delivery

import (
	"context"
	"fmt"
	"time"

	cetest "github.com/cloudevents/sdk-go/v2/test"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/reconciler-test/pkg/environment"
	"knative.dev/reconciler-test/pkg/eventshub"
	"knative.dev/reconciler-test/pkg/feature"
	"knative.dev/reconciler-test/pkg/k8s"

	natssv1alpha1 "knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	brokerconstants "knative.dev/eventing-natss/pkg/broker/constants"
	natssclient "knative.dev/eventing-natss/pkg/client/injection/client"
	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	messagingv1 "knative.dev/eventing/pkg/apis/messaging/v1"
	eventingclient "knative.dev/eventing/pkg/client/injection/client"
)

var (
	brokerGVR       = eventingv1.SchemeGroupVersion.WithResource("brokers")
	triggerGVR      = eventingv1.SchemeGroupVersion.WithResource("triggers")
	subscriptionGVR = messagingv1.SchemeGroupVersion.WithResource("subscriptions")
	natsChannelGVR  = natssv1alpha1.SchemeGroupVersion.WithResource("natsjetstreamchannels")
	serviceGVR      = corev1.SchemeGroupVersion.WithResource("services")
)

type deliveryRoute int

const (
	brokerRoute deliveryRoute = iota
	channelRoute
)

type retryBehavior struct {
	retries           int32
	responseCode      int
	responseHeaders   map[string]string
	delivery          *eventingduckv1.DeliverySpec
	expectedIntervals []time.Duration
	rejectedStep      string
	receivedStep      string
	timingStep        string
}

func newDeliveryFeature(name, resourcePrefix string, route deliveryRoute, behavior retryBehavior) *feature.Feature {
	receiverName := feature.MakeRandomK8sName(resourcePrefix + "-receiver")
	senderName := feature.MakeRandomK8sName(resourcePrefix + "-sender")
	event := cetest.FullEvent()

	receiverOptions := []eventshub.EventsHubOption{
		eventshub.StartReceiver,
		eventshub.DropFirstN(uint(behavior.retries)),
		eventshub.DropEventsResponseCode(behavior.responseCode),
	}
	if len(behavior.responseHeaders) > 0 {
		receiverOptions = append(receiverOptions, eventshub.DropEventsResponseHeaders(behavior.responseHeaders))
	}

	f := feature.NewFeatureNamed(name)
	f.Setup("install receiver", eventshub.Install(receiverName, receiverOptions...))

	var target schema.GroupVersionResource
	var targetName string
	switch route {
	case brokerRoute:
		target = brokerGVR
		targetName = feature.MakeRandomK8sName(resourcePrefix + "-broker")
		triggerName := feature.MakeRandomK8sName(resourcePrefix + "-trigger")
		f.Setup("install broker", installBroker(targetName))
		f.Setup("install trigger", installTrigger(targetName, triggerName, receiverName, behavior.delivery))
		f.Requirement("receiver is addressable", k8s.IsAddressable(serviceGVR, receiverName, time.Second, time.Minute))
		f.Requirement("broker is ready", k8s.IsReady(brokerGVR, targetName, time.Second, 3*time.Minute))
		f.Requirement("trigger is ready", k8s.IsReady(triggerGVR, triggerName, time.Second, 3*time.Minute))
	case channelRoute:
		target = natsChannelGVR
		targetName = feature.MakeRandomK8sName(resourcePrefix + "-channel")
		subscriptionName := feature.MakeRandomK8sName(resourcePrefix + "-subscription")
		f.Setup("install channel", installChannel(targetName, behavior.delivery))
		f.Setup("install subscription", installSubscription(targetName, subscriptionName, receiverName))
		f.Requirement("receiver is addressable", k8s.IsAddressable(serviceGVR, receiverName, time.Second, time.Minute))
		f.Requirement("channel is ready", k8s.IsReady(natsChannelGVR, targetName, time.Second, 3*time.Minute))
		f.Requirement("subscription is ready", k8s.IsReady(subscriptionGVR, subscriptionName, time.Second, 3*time.Minute))
	default:
		panic(fmt.Sprintf("unsupported delivery route %d", route))
	}

	f.Assert("send event", eventshub.Install(
		senderName,
		eventshub.StartSenderToResource(target, targetName),
		eventshub.InputEvent(event),
	))
	f.Assert(behavior.rejectedStep, assertExact(receiverName, int(behavior.retries), eventWithKind(event.ID(), eventshub.EventRejected)))
	f.Assert(behavior.receivedStep, assertExact(receiverName, 1, eventWithKind(event.ID(), eventshub.EventReceived)))
	f.Assert(behavior.timingStep, assertExact(receiverName, int(behavior.retries)+1, deliveriesFollowIntervals(event.ID(), behavior.expectedIntervals)))

	return f
}

func assertExact(receiverName string, count int, matcher eventshub.EventInfoMatcher) feature.StepFn {
	return func(ctx context.Context, t feature.T) {
		eventshub.StoreFromContext(ctx, receiverName).AssertExact(ctx, t, count, matcher)
	}
}

func installBroker(name string) feature.StepFn {
	return func(ctx context.Context, t feature.T) {
		namespace := environment.FromContext(ctx).Namespace()
		_, err := eventingclient.Get(ctx).EventingV1().Brokers(namespace).Create(ctx, &eventingv1.Broker{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Annotations: map[string]string{
					eventingv1.BrokerClassAnnotationKey: brokerconstants.BrokerClassName,
				},
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err)
	}
}

func installTrigger(brokerName, triggerName, receiverName string, delivery *eventingduckv1.DeliverySpec) feature.StepFn {
	return func(ctx context.Context, t feature.T) {
		namespace := environment.FromContext(ctx).Namespace()
		_, err := eventingclient.Get(ctx).EventingV1().Triggers(namespace).Create(ctx, &eventingv1.Trigger{
			ObjectMeta: metav1.ObjectMeta{Name: triggerName, Namespace: namespace},
			Spec: eventingv1.TriggerSpec{
				Broker: brokerName,
				Subscriber: duckv1.Destination{Ref: &duckv1.KReference{
					APIVersion: corev1.SchemeGroupVersion.String(),
					Kind:       "Service",
					Name:       receiverName,
				}},
				Delivery: delivery,
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err)
	}
}

func installChannel(name string, delivery *eventingduckv1.DeliverySpec) feature.StepFn {
	return func(ctx context.Context, t feature.T) {
		namespace := environment.FromContext(ctx).Namespace()
		_, err := natssclient.Get(ctx).MessagingV1alpha1().NatsJetStreamChannels(namespace).Create(ctx, &natssv1alpha1.NatsJetStreamChannel{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: natssv1alpha1.NatsJetStreamChannelSpec{
				ChannelableSpec: eventingduckv1.ChannelableSpec{Delivery: delivery},
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err)
	}
}

func installSubscription(channelName, subscriptionName, receiverName string) feature.StepFn {
	return func(ctx context.Context, t feature.T) {
		namespace := environment.FromContext(ctx).Namespace()
		_, err := eventingclient.Get(ctx).MessagingV1().Subscriptions(namespace).Create(ctx, &messagingv1.Subscription{
			ObjectMeta: metav1.ObjectMeta{Name: subscriptionName, Namespace: namespace},
			Spec: messagingv1.SubscriptionSpec{
				Channel: duckv1.KReference{
					APIVersion: natssv1alpha1.SchemeGroupVersion.String(),
					Kind:       "NatsJetStreamChannel",
					Name:       channelName,
				},
				Subscriber: &duckv1.Destination{Ref: &duckv1.KReference{
					APIVersion: corev1.SchemeGroupVersion.String(),
					Kind:       "Service",
					Name:       receiverName,
				}},
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err)
	}
}
