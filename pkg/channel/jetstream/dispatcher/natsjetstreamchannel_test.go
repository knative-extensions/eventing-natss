/*
Copyright 2022 The Knative Authors

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

package dispatcher

import (
	"context"
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	"knative.dev/pkg/apis"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgotesting "k8s.io/client-go/testing"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	. "knative.dev/pkg/reconciler/testing"

	dispatchertesting "knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher/testing"
	"knative.dev/eventing-natss/pkg/channel/jetstream/utils"
	"knative.dev/eventing-natss/pkg/client/injection/client"
	natsjschannelreconciler "knative.dev/eventing-natss/pkg/client/injection/reconciler/messaging/v1alpha1/natsjetstreamchannel"
	reconciletesting "knative.dev/eventing-natss/pkg/reconciler/testing"
	messagingv1client "knative.dev/eventing/pkg/client/injection/client"
)

const (
	testNS = "test-namespace"
	ncName = "test-nc"

	twoSubscriberPatch    = `[{"op":"add","path":"/status/subscribers","value":[{"observedGeneration":1,"ready":"True","uid":"2f9b5e8e-deb6-11e8-9f32-f2801f1b9fd1"},{"observedGeneration":2,"ready":"True","uid":"34c5aec8-deb6-11e8-9f32-f2801f1b9fd1"}]}]`
	channelServiceAddress = "test-nc-kn-jsm-channel.test-namespace.svc.cluster.local"
)

var (
	finalizerUpdatedEvent = Eventf(
		v1.EventTypeNormal,
		"FinalizerUpdate",
		fmt.Sprintf(`Updated %q finalizers`, ncName),
	)

	subscriber1UID        = types.UID("2f9b5e8e-deb6-11e8-9f32-f2801f1b9fd1")
	subscriber2UID        = types.UID("34c5aec8-deb6-11e8-9f32-f2801f1b9fd1")
	subscriber1Generation = int64(1)
	subscriber2Generation = int64(2)

	subscriber1 = eventingduckv1.SubscriberSpec{
		UID:           subscriber1UID,
		Generation:    subscriber1Generation,
		SubscriberURI: apis.HTTP("call1"),
		ReplyURI:      apis.HTTP("sink2"),
	}

	subscriber2 = eventingduckv1.SubscriberSpec{
		UID:           subscriber2UID,
		Generation:    subscriber2Generation,
		SubscriberURI: apis.HTTP("call2"),
		ReplyURI:      apis.HTTP("sink2"),
	}
	subscribers = []eventingduckv1.SubscriberSpec{subscriber1, subscriber2}
)

func TestAllCases(t *testing.T) {
	ncKey := testNS + "/" + ncName
	s := dispatchertesting.RunBasicJetstreamServer()
	defer dispatchertesting.ShutdownJSServerAndRemoveStorage(t, s)

	table := TableTest{
		{
			Name: "make sure reconcile handles bad keys",
			Key:  "too/many/parts",
		},
		{
			Name: "make sure reconcile handles good keys that don't exist",
			Key:  "foo/not-found",
		},
		{
			Name: "reconcile ok: stream ready",
			Key:  ncKey,
			Objects: []runtime.Object{
				reconciletesting.NewNatsJetStreamChannel(ncName, testNS),
			},
			WantStatusUpdates: []clientgotesting.UpdateActionImpl{{
				Object: reconciletesting.NewNatsJetStreamChannel(ncName, testNS,
					reconciletesting.WithNatsJetStreamInitChannelConditions,
					reconciletesting.WithNatsJetStreamChannelStreamReady(),
				),
			}},
			WantEvents: []string{
				finalizerUpdatedEvent,
				Eventf(v1.EventTypeNormal, ReasonJetstreamStreamCreated, "JetStream stream created"),
			},
			WantPatches: []clientgotesting.PatchActionImpl{
				makeFinalizerPatch(testNS, ncName),
			},
		},
		{
			Name: "reconcile ok: update consumer",
			Key:  ncKey,
			Objects: []runtime.Object{
				reconciletesting.NewNatsJetStreamChannel(ncName, testNS,
					reconciletesting.WithNatsJetStreamInitChannelConditions,
					reconciletesting.WithNatsJetStreamChannelAddress(channelServiceAddress),
					reconciletesting.JetStreamAddressable(),
					reconciletesting.WithNatsJetStreamChannelChannelServiceReady(),
					reconciletesting.WithNatsJetStreamChannelServiceReady(),
					reconciletesting.WithNatsJetStreamChannelEndpointsReady(),
					reconciletesting.WithNatsJetStreamChannelDeploymentReady(),
					reconciletesting.WithNatsJetStreamChannelStreamReady(),
					reconciletesting.WithNatsJetStreamChannelSubscribers(subscribers)),
			},
			WantEvents: []string{
				finalizerUpdatedEvent,
			},
			WantPatches: []clientgotesting.PatchActionImpl{
				makeFinalizerPatch(testNS, ncName),
				makePatch(testNS, ncName, twoSubscriberPatch),
			},
		},
	}

	table.Test(t, reconciletesting.MakeFactory(func(ctx context.Context, l *reconciletesting.Listers) controller.Reconciler {
		_, js := dispatchertesting.JsClient(t, s)
		return createReconciler(ctx, l, js, func() *Dispatcher {
			d, err := NewDispatcher(ctx, NatsDispatcherArgs{
				JetStream:           js,
				SubjectFunc:         utils.PublishSubjectName,
				ConsumerNameFunc:    utils.ConsumerName,
				ConsumerSubjectFunc: utils.ConsumerSubjectName,
				PodName:             "test",
				ContainerName:       "test",
			})
			require.NoError(t, err)
			return d
		})
	}))
}

func makeFinalizerPatch(namespace, name string) clientgotesting.PatchActionImpl {
	return makePatch(namespace, name, `{"metadata":{"finalizers":["`+finalizerName+`"],"resourceVersion":""}}`)
}

func makePatch(namespace, name, patch string) clientgotesting.PatchActionImpl {
	return clientgotesting.PatchActionImpl{
		ActionImpl: clientgotesting.ActionImpl{
			Namespace: namespace,
		},
		Name:  name,
		Patch: []byte(patch),
	}
}

func createReconciler(ctx context.Context, listers *reconciletesting.Listers, js nats.JetStreamManager, dispatcherFactory func() *Dispatcher) controller.Reconciler {
	return natsjschannelreconciler.NewReconciler(
		ctx,
		logging.FromContext(ctx),
		client.Get(ctx),
		listers.GetNatsJetstreamChannelLister(),
		controller.GetEventRecorder(ctx),
		&Reconciler{
			checkOrphanedSubscriptions: false,
			msgingClient:               messagingv1client.Get(ctx),
			clientSet:                  client.Get(ctx),
			js:                         js,
			dispatcher:                 dispatcherFactory(),
			streamNameFunc:             utils.StreamName,
			consumerNameFunc:           utils.ConsumerName,
		},
		controller.Options{
			FinalizerName: "jetstream-ch-dispatcher",
		},
	)
}
