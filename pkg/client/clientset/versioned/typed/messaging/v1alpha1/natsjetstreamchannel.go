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

// Code generated by client-gen. DO NOT EDIT.

package v1alpha1

import (
	context "context"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	gentype "k8s.io/client-go/gentype"
	messagingv1alpha1 "knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	scheme "knative.dev/eventing-natss/pkg/client/clientset/versioned/scheme"
)

// NatsJetStreamChannelsGetter has a method to return a NatsJetStreamChannelInterface.
// A group's client should implement this interface.
type NatsJetStreamChannelsGetter interface {
	NatsJetStreamChannels(namespace string) NatsJetStreamChannelInterface
}

// NatsJetStreamChannelInterface has methods to work with NatsJetStreamChannel resources.
type NatsJetStreamChannelInterface interface {
	Create(ctx context.Context, natsJetStreamChannel *messagingv1alpha1.NatsJetStreamChannel, opts v1.CreateOptions) (*messagingv1alpha1.NatsJetStreamChannel, error)
	Update(ctx context.Context, natsJetStreamChannel *messagingv1alpha1.NatsJetStreamChannel, opts v1.UpdateOptions) (*messagingv1alpha1.NatsJetStreamChannel, error)
	// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
	UpdateStatus(ctx context.Context, natsJetStreamChannel *messagingv1alpha1.NatsJetStreamChannel, opts v1.UpdateOptions) (*messagingv1alpha1.NatsJetStreamChannel, error)
	Delete(ctx context.Context, name string, opts v1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error
	Get(ctx context.Context, name string, opts v1.GetOptions) (*messagingv1alpha1.NatsJetStreamChannel, error)
	List(ctx context.Context, opts v1.ListOptions) (*messagingv1alpha1.NatsJetStreamChannelList, error)
	Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *messagingv1alpha1.NatsJetStreamChannel, err error)
	NatsJetStreamChannelExpansion
}

// natsJetStreamChannels implements NatsJetStreamChannelInterface
type natsJetStreamChannels struct {
	*gentype.ClientWithList[*messagingv1alpha1.NatsJetStreamChannel, *messagingv1alpha1.NatsJetStreamChannelList]
}

// newNatsJetStreamChannels returns a NatsJetStreamChannels
func newNatsJetStreamChannels(c *MessagingV1alpha1Client, namespace string) *natsJetStreamChannels {
	return &natsJetStreamChannels{
		gentype.NewClientWithList[*messagingv1alpha1.NatsJetStreamChannel, *messagingv1alpha1.NatsJetStreamChannelList](
			"natsjetstreamchannels",
			c.RESTClient(),
			scheme.ParameterCodec,
			namespace,
			func() *messagingv1alpha1.NatsJetStreamChannel { return &messagingv1alpha1.NatsJetStreamChannel{} },
			func() *messagingv1alpha1.NatsJetStreamChannelList {
				return &messagingv1alpha1.NatsJetStreamChannelList{}
			},
		),
	}
}
