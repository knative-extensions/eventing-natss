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

	"knative.dev/eventing-natss/pkg/apis/messaging/v1beta1"
)

// NatssChannelOption enables further configuration of a NatssChannel.
type NatssChannelOption func(*v1beta1.NatssChannel)

// NewNatssChannel creates an NatssChannel with NatssChannelOptions.
func NewNatssChannel(name, namespace string, ncopt ...NatssChannelOption) *v1beta1.NatssChannel {
	nc := &v1beta1.NatssChannel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1beta1.NatssChannelSpec{},
	}
	for _, opt := range ncopt {
		opt(nc)
	}
	nc.SetDefaults(context.Background())
	return nc
}

// WithReady marks a NatssChannel as being ready
// The dispatcher reconciler does not set the ready status, instead the controller reconciler does
// For testing, we need to be able to set the status to ready
func WithReady(nc *v1beta1.NatssChannel) {
	cs := apis.NewLivingConditionSet()
	cs.Manage(&nc.Status).MarkTrue(v1beta1.NatssChannelConditionReady)
}

func WithNotReady(reason, messageFormat string) NatssChannelOption {
	return func(nc *v1beta1.NatssChannel) {
		cs := apis.NewLivingConditionSet()
		cs.Manage(&nc.Status).MarkFalse(v1beta1.NatssChannelConditionReady, reason, messageFormat)
	}
}

func WithNatssInitChannelConditions(nc *v1beta1.NatssChannel) {
	nc.Status.InitializeConditions()
}

func WithNatssChannelFinalizer(nc *v1beta1.NatssChannel) {
	nc.Finalizers = []string{"natss-ch-dispatcher"}
}

func WithNatssChannelDeleted(nc *v1beta1.NatssChannel) {
	deleteTime := metav1.NewTime(time.Unix(1e9, 0))
	nc.ObjectMeta.SetDeletionTimestamp(&deleteTime)
}

func WithNatssChannelDeploymentNotReady(reason, message string) NatssChannelOption {
	return func(nc *v1beta1.NatssChannel) {
		nc.Status.MarkDispatcherFailed(reason, message)
	}
}

func WithNatssChannelDeploymentReady() NatssChannelOption {
	return func(nc *v1beta1.NatssChannel) {
		nc.Status.PropagateDispatcherStatus(&appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue}}})
	}
}

func WithNatssChannelServiceNotReady(reason, message string) NatssChannelOption {
	return func(nc *v1beta1.NatssChannel) {
		nc.Status.MarkServiceFailed(reason, message)
	}
}

func WithNatssChannelServiceReady() NatssChannelOption {
	return func(nc *v1beta1.NatssChannel) {
		nc.Status.MarkServiceTrue()
	}
}

func WithNatssChannelChannelServicetNotReady(reason, message string) NatssChannelOption {
	return func(nc *v1beta1.NatssChannel) {
		nc.Status.MarkChannelServiceFailed(reason, message)
	}
}

func WithNatssChannelChannelServiceReady() NatssChannelOption {
	return func(nc *v1beta1.NatssChannel) {
		nc.Status.MarkChannelServiceTrue()
	}
}

func WithNatssChannelEndpointsNotReady(reason, message string) NatssChannelOption {
	return func(nc *v1beta1.NatssChannel) {
		nc.Status.MarkEndpointsFailed(reason, message)
	}
}

func WithNatssChannelEndpointsReady() NatssChannelOption {
	return func(nc *v1beta1.NatssChannel) {
		nc.Status.MarkEndpointsTrue()
	}
}

func WithNatssChannelSubscriber(t *testing.T, subscriberURI string) NatssChannelOption {
	s, err := apis.ParseURL(subscriberURI)
	if err != nil {
		t.Errorf("cannot parse url: %v", err)
	}
	return func(nc *v1beta1.NatssChannel) {
		nc.Spec.Subscribers = []duckv1.SubscriberSpec{{
			UID:           "",
			Generation:    0,
			SubscriberURI: s,
		}}
	}
}

func WithNatssChannelSubscribers(subscribers []duckv1.SubscriberSpec) NatssChannelOption {
	return func(nc *v1beta1.NatssChannel) {
		nc.Spec.Subscribers = subscribers
	}
}

func WithNatssChannelSubscribableStatus(ready corev1.ConditionStatus, message string) NatssChannelOption {
	return func(nc *v1beta1.NatssChannel) {
		nc.Status.Subscribers = []duckv1.SubscriberStatus{{
			Ready:   ready,
			Message: message,
		}}
	}
}
func WithNatssChannelReadySubscriber(uid string) NatssChannelOption {
	return WithNatssChannelReadySubscriberAndGeneration(uid, 0)
}

func WithNatssChannelReadySubscriberAndGeneration(uid string, observedGeneration int64) NatssChannelOption {
	return func(nc *v1beta1.NatssChannel) {
		nc.Status.Subscribers = append(nc.Status.Subscribers, duckv1.SubscriberStatus{
			UID:                types.UID(uid),
			ObservedGeneration: observedGeneration,
			Ready:              corev1.ConditionTrue,
		})
	}
}

func WithNatssChannelAddress(a string) NatssChannelOption {
	return func(nc *v1beta1.NatssChannel) {
		nc.Status.SetAddress(&apis.URL{
			Scheme: "http",
			Host:   a,
		})
	}
}

func Addressable() NatssChannelOption {
	return func(channel *v1beta1.NatssChannel) {
		channel.GetConditionSet().Manage(&channel.Status).MarkTrue(v1beta1.NatssChannelConditionAddressable)
	}
}
