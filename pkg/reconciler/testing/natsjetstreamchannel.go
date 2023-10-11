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

package testing

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	duckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	"knative.dev/pkg/apis"

	"knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
)

// NatsJetStreamChannelOption enables further configuration of a NatsJetStreamChannel.
type NatsJetStreamChannelOption func(*v1alpha1.NatsJetStreamChannel)

// NewNatsJetStreamChannel creates an NatsJetStreamChannel with NatsJetStreamChannelOptions.
func NewNatsJetStreamChannel(name, namespace string, ncopt ...NatsJetStreamChannelOption) *v1alpha1.NatsJetStreamChannel {
	nc := &v1alpha1.NatsJetStreamChannel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.NatsJetStreamChannelSpec{},
	}
	for _, opt := range ncopt {
		opt(nc)
	}
	nc.SetDefaults(context.Background())
	return nc
}

// WithReady marks a NatsJetStreamChannel as being ready
// The dispatcher reconciler does not set the ready status, instead the controller reconciler does
// For testing, we need to be able to set the status to ready
func WithJetStreamReady(nc *v1alpha1.NatsJetStreamChannel) {
	cs := apis.NewLivingConditionSet()
	cs.Manage(&nc.Status).MarkTrue(v1alpha1.NatsJetStreamChannelConditionReady)
}

func WithJetStreamNotReady(reason, messageFormat string) NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		cs := apis.NewLivingConditionSet()
		cs.Manage(&nc.Status).MarkFalse(v1alpha1.NatsJetStreamChannelConditionReady, reason, messageFormat)
	}
}

func WithNatsJetStreamInitChannelConditions(nc *v1alpha1.NatsJetStreamChannel) {
	nc.Status.InitializeConditions()
}

func WithNatsJetStreamChannelFinalizer(nc *v1alpha1.NatsJetStreamChannel) {
	nc.Finalizers = []string{"natss-ch-dispatcher"}
}

func WithNatsJetStreamChannelDeleted(nc *v1alpha1.NatsJetStreamChannel) {
	deleteTime := metav1.NewTime(time.Unix(1e9, 0))
	nc.ObjectMeta.SetDeletionTimestamp(&deleteTime)
}

func WithNatsJetStreamDeploymentSpecTemplate(nc *v1alpha1.NatsJetStreamChannel) {
	nc.Spec.DeploymentSpecTemplate = &v1alpha1.JetStreamDispatcherDeploymentTemplate{
		Labels: map[string]string{
			"label1": "value",
		},
		Annotations: map[string]string{
			"annotation1": "value",
		},
	}
}

func WithNatsJetStreamChannelDispatcherReady() NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.MarkDispatcherTrue()
	}
}

func WithNatsJetStreamChannelDeploymentNotReady(reason, message string) NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.MarkDispatcherFailed(reason, message)
	}
}

func WithNatsJetStreamChannelDeploymentReady() NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.PropagateDispatcherStatus(&appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue}}})
	}
}

func WithNatsJetStreamChannelServiceNotReady(reason, message string) NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.MarkServiceFailed(reason, message)
	}
}

func WithNatsJetStreamChannelServiceReady() NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.MarkServiceTrue()
	}
}

func WithNatsJetStreamChannelChannelServicetNotReady(reason, message string) NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.MarkChannelServiceFailed(reason, message)
	}
}

func WithNatsJetStreamChannelChannelServiceReady() NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.MarkChannelServiceTrue()
	}
}

func WithNatsJetStreamChannelEndpointsNotReady(reason, message string) NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.MarkEndpointsFailed(reason, message)
	}
}

func WithNatsJetStreamChannelEndpointsReady() NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.MarkEndpointsTrue()
	}
}

func WithNatsJetStreamChannelStreamNotReady(reason, message string) NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.MarkStreamFailed(reason, message)
	}
}

func WithNatsJetStreamChannelStreamReady() NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.MarkStreamTrue()
	}
}

func WithNatsJetStreamChannelSubscriber(t *testing.T, subscriberURI string) NatsJetStreamChannelOption {
	s, err := apis.ParseURL(subscriberURI)
	if err != nil {
		t.Errorf("cannot parse url: %v", err)
	}
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Spec.Subscribers = []duckv1.SubscriberSpec{{
			UID:           "",
			Generation:    0,
			SubscriberURI: s,
		}}
	}
}

func WithNatsJetStreamChannelSubscribers(subscribers []duckv1.SubscriberSpec) NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Spec.Subscribers = subscribers
	}
}

func WithNatsJetStreamChannelSubscribableStatus(ready corev1.ConditionStatus, message string) NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.Subscribers = []duckv1.SubscriberStatus{{
			Ready:   ready,
			Message: message,
		}}
	}
}
func WithNatsJetStreamChannelReadySubscriber(uid string) NatsJetStreamChannelOption {
	return WithNatsJetStreamChannelReadySubscriberAndGeneration(uid, 0)
}

func WithNatsJetStreamChannelReadySubscriberAndGeneration(uid string, observedGeneration int64) NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.Subscribers = append(nc.Status.Subscribers, duckv1.SubscriberStatus{
			UID:                types.UID(uid),
			ObservedGeneration: observedGeneration,
			Ready:              corev1.ConditionTrue,
		})
	}
}

func WithNatsJetStreamChannelAddress(a string) NatsJetStreamChannelOption {
	return func(nc *v1alpha1.NatsJetStreamChannel) {
		nc.Status.SetAddress(&apis.URL{
			Scheme: "http",
			Host:   a,
		})
	}
}

func JetStreamAddressable() NatsJetStreamChannelOption {
	return func(channel *v1alpha1.NatsJetStreamChannel) {
		channel.GetConditionSet().Manage(&channel.Status).MarkTrue(v1alpha1.NatsJetStreamChannelConditionAddressable)
	}
}
