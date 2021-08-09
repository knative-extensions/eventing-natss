/*
Copyright 2020 The Knative Authors

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
	"errors"

	"knative.dev/eventing-natss/pkg/dispatcher"
	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	messagingv1 "knative.dev/eventing/pkg/apis/messaging/v1"
)

// DispatcherDoNothing is a mock which doesn't do anything
type DispatcherDoNothing struct{}

var _ dispatcher.NatsDispatcher = (*DispatcherDoNothing)(nil)

func NewDispatcherDoNothing() dispatcher.NatsDispatcher {
	return &DispatcherDoNothing{}
}

func (s *DispatcherDoNothing) Start(_ context.Context) error {
	return nil
}

func (s *DispatcherDoNothing) UpdateSubscriptions(ctx context.Context, name, ns string, subscriptions []eventingduckv1.SubscriberSpec, isFinalizer bool) (map[eventingduckv1.SubscriberSpec]error, error) {
	return nil, nil
}

func (s *DispatcherDoNothing) ProcessChannels(_ context.Context, _ []messagingv1.Channel) error {
	return nil
}

// DispatcherFailNatssSubscription simulates that natss has a failed subscription
type DispatcherFailNatssSubscription struct {
}

var _ dispatcher.NatsDispatcher = (*DispatcherFailNatssSubscription)(nil)

func NewDispatcherFailNatssSubscription() *DispatcherFailNatssSubscription {
	return &DispatcherFailNatssSubscription{}
}

func (s *DispatcherFailNatssSubscription) Start(_ context.Context) error {
	return nil
}

// UpdateSubscriptions returns a failed natss subscription
func (s *DispatcherFailNatssSubscription) UpdateSubscriptions(ctx context.Context, name, ns string, subscriptions []eventingduckv1.SubscriberSpec, isFinalizer bool) (map[eventingduckv1.SubscriberSpec]error, error) {
	failedSubscriptions := make(map[eventingduckv1.SubscriberSpec]error, len(subscriptions))
	for _, sub := range subscriptions {
		ss := eventingduckv1.SubscriberSpec{
			UID:           sub.UID,
			Generation:    sub.Generation,
			SubscriberURI: sub.SubscriberURI,
			ReplyURI:      sub.ReplyURI,
			Delivery:      sub.Delivery,
		}
		failedSubscriptions[ss] = errors.New("ups")
	}
	return failedSubscriptions, nil
}

func (s *DispatcherFailNatssSubscription) ProcessChannels(_ context.Context, _ []messagingv1.Channel) error {
	return nil
}
