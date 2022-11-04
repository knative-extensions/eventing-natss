/*
Copyright 2020 The Knative Authors

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

// Code generated by informer-gen. DO NOT EDIT.

package v1alpha1

import (
	"context"
	time "time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
	messagingv1alpha1 "knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	versioned "knative.dev/eventing-natss/pkg/client/clientset/versioned"
	internalinterfaces "knative.dev/eventing-natss/pkg/client/informers/externalversions/internalinterfaces"
	v1alpha1 "knative.dev/eventing-natss/pkg/client/listers/messaging/v1alpha1"
)

// NatsJetStreamChannelInformer provides access to a shared informer and lister for
// NatsJetStreamChannels.
type NatsJetStreamChannelInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1alpha1.NatsJetStreamChannelLister
}

type natsJetStreamChannelInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
	namespace        string
}

// NewNatsJetStreamChannelInformer constructs a new informer for NatsJetStreamChannel type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewNatsJetStreamChannelInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredNatsJetStreamChannelInformer(client, namespace, resyncPeriod, indexers, nil)
}

// NewFilteredNatsJetStreamChannelInformer constructs a new informer for NatsJetStreamChannel type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredNatsJetStreamChannelInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.MessagingV1alpha1().NatsJetStreamChannels(namespace).List(context.TODO(), options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.MessagingV1alpha1().NatsJetStreamChannels(namespace).Watch(context.TODO(), options)
			},
		},
		&messagingv1alpha1.NatsJetStreamChannel{},
		resyncPeriod,
		indexers,
	)
}

func (f *natsJetStreamChannelInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredNatsJetStreamChannelInformer(client, f.namespace, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *natsJetStreamChannelInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&messagingv1alpha1.NatsJetStreamChannel{}, f.defaultInformer)
}

func (f *natsJetStreamChannelInformer) Lister() v1alpha1.NatsJetStreamChannelLister {
	return v1alpha1.NewNatsJetStreamChannelLister(f.Informer().GetIndexer())
}
