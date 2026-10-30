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
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"knative.dev/pkg/controller"
	"knative.dev/pkg/injection"
	"knative.dev/pkg/injection/clients/dynamicclient"
	"knative.dev/pkg/logging"

	eventingv1alpha1 "knative.dev/eventing-natss/pkg/apis/eventing/v1alpha1"
)

type brokerConfigInformerKey struct{}

func init() {
	injection.Default.RegisterInformer(withBrokerConfigInformer)
}

// withBrokerConfigInformer watches raw configurations so a CR that cannot be
// decoded into the Go API type does not block cache sync for the controller.
func withBrokerConfigInformer(ctx context.Context) (context.Context, controller.Informer) {
	client := dynamicclient.Get(ctx).Resource(brokerConfigGVR).Namespace(injection.GetNamespaceScope(ctx))
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

// getBrokerConfigInformer returns the informer that withBrokerConfigInformer registered.
func getBrokerConfigInformer(ctx context.Context) cache.SharedIndexInformer {
	untyped := ctx.Value(brokerConfigInformerKey{})
	if untyped == nil {
		logging.FromContext(ctx).Panic("Unable to fetch the broker config informer from context.")
	}
	return untyped.(cache.SharedIndexInformer)
}

// decodeBrokerConfigSpec decodes the spec of a configuration held by the
// informer. Each broker decodes only its own, so a malformed CR fails just the
// brokers that use it. JSON keeps int64 values exact and rejects out-of-range ones.
func decodeBrokerConfigSpec(obj runtime.Object) (*eventingv1alpha1.NatsJetStreamBrokerConfigSpec, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("unexpected object type %T", obj)
	}
	data, err := json.Marshal(u.Object["spec"])
	if err != nil {
		return nil, err
	}
	var cfg eventingv1alpha1.NatsJetStreamBrokerConfigSpec
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
