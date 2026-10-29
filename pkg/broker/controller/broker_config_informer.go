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

package controller

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"knative.dev/pkg/controller"
	"knative.dev/pkg/injection"
	"knative.dev/pkg/injection/clients/dynamicclient"
)

type brokerConfigInformerKey struct{}

func init() {
	injection.Default.RegisterInformer(withBrokerConfigInformer)
}

// withBrokerConfigInformer watches raw configurations so a CR that cannot be
// decoded into the Go API type does not block cache sync for the controller.
func withBrokerConfigInformer(ctx context.Context) (context.Context, controller.Informer) {
	namespace := metav1.NamespaceAll
	if injection.HasNamespaceScope(ctx) {
		namespace = injection.GetNamespaceScope(ctx)
	}
	client := dynamicclient.Get(ctx).Resource(brokerConfigGVK.GroupVersion().WithResource("natsjetstreambrokerconfigs")).Namespace(namespace)
	informer := cache.NewSharedIndexInformer(&cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			return client.List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return client.Watch(ctx, options)
		},
	}, &unstructured.Unstructured{}, controller.GetResyncPeriod(ctx), cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	return context.WithValue(ctx, brokerConfigInformerKey{}, informer), informer
}
