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
	"github.com/nats-io/nats.go"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"

	"knative.dev/eventing/pkg/channel"
	"knative.dev/eventing/pkg/channel/fanout"
)

type (
	StreamNameFunc      func(nc *v1alpha1.NatsJetStreamChannel) string
	StreamSubjectFunc   func(namespace, name string) string
	ConsumerSubjectFunc func(namespace, name, uid string) string
	ConsumerNameFunc    func(subID string) string
)

// EnqueueFunc is passed to the Reconciler for when a follower instance attempts to sync on a Consumer which does not
// yet exist
type EnqueueFunc func(ref types.NamespacedName)

type NatsDispatcherArgs struct {
	JetStream nats.JetStreamContext

	SubjectFunc         StreamSubjectFunc
	ConsumerNameFunc    ConsumerNameFunc
	ConsumerSubjectFunc ConsumerSubjectFunc

	PodName       string
	ContainerName string
}

type ChannelConfig struct {
	channel.ChannelReference
	HostName               string
	StreamName             string
	ConsumerConfigTemplate *v1alpha1.ConsumerConfigTemplate
	Subscriptions          []Subscription
}

func (cc ChannelConfig) SubscriptionsUIDs() []string {
	res := make([]string, 0, len(cc.Subscriptions))
	for _, s := range cc.Subscriptions {
		res = append(res, string(s.UID))
	}
	return res
}

type Subscription struct {
	UID types.UID
	fanout.Subscription
}

type envConfig struct {
	PodName       string `envconfig:"POD_NAME" required:"true"`
	ContainerName string `envconfig:"CONTAINER_NAME" required:"true"`
}

type SubscriberStatusType int

const (
	SubscriberStatusTypeCreated SubscriberStatusType = iota
	SubscriberStatusTypeSkipped
	SubscriberStatusTypeUpToDate
	SubscriberStatusTypeError
	SubscriberStatusTypeDeleted
)

type SubscriberStatus struct {
	UID   types.UID
	Type  SubscriberStatusType
	Error error
}

func NewSubscriberStatusCreated(uid types.UID) SubscriberStatus {
	return SubscriberStatus{UID: uid, Type: SubscriberStatusTypeCreated}
}

func NewSubscriberStatusUpToDate(uid types.UID) SubscriberStatus {
	return SubscriberStatus{UID: uid, Type: SubscriberStatusTypeUpToDate}
}

func NewSubscriberStatusSkipped(uid types.UID) SubscriberStatus {
	return SubscriberStatus{UID: uid, Type: SubscriberStatusTypeSkipped}
}

func NewSubscriberStatusError(uid types.UID, err error) SubscriberStatus {
	return SubscriberStatus{UID: uid, Type: SubscriberStatusTypeError, Error: err}
}
