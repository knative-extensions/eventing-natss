/*
Copyright 2022 The Knative Authors

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
	"context"
	"testing"
	"time"

	"knative.dev/eventing/pkg/channel/fanout"
	"knative.dev/eventing/pkg/kncloudevents"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	dispatchertesting "knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher/testing"
	"knative.dev/eventing-natss/pkg/client/injection/client"
	fakeclientset "knative.dev/eventing-natss/pkg/client/injection/client/fake"
	reconcilertesting "knative.dev/eventing-natss/pkg/reconciler/testing"
	fakeeventingclient "knative.dev/eventing/pkg/client/injection/client/fake"
	fakekubeclient "knative.dev/pkg/client/injection/kube/client/fake"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	logtesting "knative.dev/pkg/logging/testing"

	"knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	"knative.dev/eventing-natss/pkg/channel/jetstream/utils"
	reconciletesting "knative.dev/eventing-natss/pkg/reconciler/testing"
	"knative.dev/eventing/pkg/channel"
)

func TestDispatcher_RegisterChannelHost(t *testing.T) {
	nc := reconciletesting.NewNatsJetStreamChannel(testNS, ncName)
	config := createChannelConfig(nc)

	d := &Dispatcher{}

	err := d.RegisterChannelHost(*config)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDispatcher_ReconcileConsumers(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), logtesting.TestLogger(t))

	s := dispatchertesting.RunBasicJetstreamServer()
	defer dispatchertesting.ShutdownJSServerAndRemoveStorage(t, s)
	_, js := dispatchertesting.JsClient(t, s)

	ls := reconcilertesting.NewListers([]runtime.Object{})

	ctx, _ = fakekubeclient.With(ctx, ls.GetKubeObjects()...)
	ctx, _ = fakeeventingclient.With(ctx, ls.GetEventingObjects()...)
	ctx, _ = fakeclientset.With(ctx, ls.GetNatssObjects()...)

	eventRecorder := record.NewFakeRecorder(10)
	ctx = controller.WithEventRecorder(ctx, eventRecorder)

	nc := reconciletesting.NewNatsJetStreamChannel(testNS, ncName, reconciletesting.WithNatsJetStreamChannelSubscribers(subscribers))
	sub := fanout.Subscription{
		RetryConfig: &kncloudevents.RetryConfig{
			RequestTimeout: time.Second,
			RetryMax:       1,
		},
	}
	config := createChannelConfig(nc, Subscription{
		UID:          subscriber1UID,
		Subscription: sub,
	})

	d, err := NewDispatcher(ctx, NatsDispatcherArgs{
		JetStream:           js,
		SubjectFunc:         utils.PublishSubjectName,
		ConsumerNameFunc:    utils.ConsumerName,
		ConsumerSubjectFunc: utils.ConsumerSubjectName,
		PodName:             "test",
		ContainerName:       "test",
	})
	require.NoError(t, err)

	reconciler := &Reconciler{
		clientSet:        client.Get(ctx),
		js:               js,
		dispatcher:       d,
		streamNameFunc:   utils.StreamName,
		consumerNameFunc: utils.ConsumerName,
	}
	_ = reconciler.reconcileStream(ctx, nc)

	err = d.ReconcileConsumers(ctx, *config, true)
	if err != nil {
		t.Fatal(err)
	}

	configNew := createChannelConfig(nc, Subscription{
		UID: subscriber1UID,
	}, Subscription{
		UID: subscriber2UID,
	})

	err = d.ReconcileConsumers(ctx, *configNew, false)
	if err != nil {
		t.Fatal(err)
	}

	newConfig := createChannelConfig(nc)
	err = d.ReconcileConsumers(ctx, *newConfig, true)
	if err != nil {
		t.Fatal(err)
	}
}

func createChannelConfig(nc *v1alpha1.NatsJetStreamChannel, subs ...Subscription) *ChannelConfig {
	if subs == nil {
		subs = []Subscription{}
	}
	return &ChannelConfig{
		ChannelReference: channel.ChannelReference{
			Namespace: nc.Namespace,
			Name:      nc.Name,
		},
		StreamName:             utils.StreamName(nc),
		HostName:               "a.b.c.d",
		ConsumerConfigTemplate: nc.Spec.ConsumerConfigTemplate,
		Subscriptions:          subs,
	}
}
