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

package natss

import (
	"context"
	"fmt"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	clientgotesting "k8s.io/client-go/testing"
	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	"knative.dev/pkg/apis"
	fakekubeclient "knative.dev/pkg/client/injection/kube/client/fake"
	_ "knative.dev/pkg/client/injection/kube/informers/core/v1/service/fake"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/injection"
	fakedynamicclient "knative.dev/pkg/injection/clients/dynamicclient/fake"
	"knative.dev/pkg/logging"
	. "knative.dev/pkg/reconciler/testing"

	fakeeventingclient "knative.dev/eventing/pkg/client/injection/client/fake"

	"knative.dev/eventing-natss/pkg/client/injection/client"
	fakeclientset "knative.dev/eventing-natss/pkg/client/injection/client/fake"
	_ "knative.dev/eventing-natss/pkg/client/injection/informers/messaging/v1beta1/natsschannel/fake"
	natsschannelreconciler "knative.dev/eventing-natss/pkg/client/injection/reconciler/messaging/v1beta1/natsschannel"
	"knative.dev/eventing-natss/pkg/dispatcher"
	dispatchertesting "knative.dev/eventing-natss/pkg/dispatcher/testing"
	reconciletesting "knative.dev/eventing-natss/pkg/reconciler/testing"

	"go.uber.org/zap"
)

const (
	testNS = "test-namespace"
	ncName = "test-nc"

	twoSubscriberPatch       = `[{"op":"add","path":"/status/subscribers","value":[{"observedGeneration":1,"ready":"True","uid":"2f9b5e8e-deb6-11e8-9f32-f2801f1b9fd1"},{"observedGeneration":2,"ready":"True","uid":"34c5aec8-deb6-11e8-9f32-f2801f1b9fd1"}]}]`
	twoSubscriberPatchFailed = `[{"op":"add","path":"/status/subscribers","value":[{"message":"ups","observedGeneration":1,"ready":"False","uid":"2f9b5e8e-deb6-11e8-9f32-f2801f1b9fd1"},{"message":"ups","observedGeneration":2,"ready":"False","uid":"34c5aec8-deb6-11e8-9f32-f2801f1b9fd1"}]}]`
)

var (
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

var (
	finalizerUpdatedEvent = Eventf(
		corev1.EventTypeNormal,
		"FinalizerUpdate",
		fmt.Sprintf(`Updated %q finalizers`, ncName),
	)
)

func TestAllCases(t *testing.T) {
	ncKey := testNS + "/" + ncName

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
			Name: "reconcile ok: channel ready",
			Key:  ncKey,
			Objects: []runtime.Object{
				reconciletesting.NewNatssChannel(ncName, testNS,
					reconciletesting.WithNatssChannelChannelServiceReady(),
					reconciletesting.WithNatssChannelServiceReady(),
					reconciletesting.WithNatssChannelEndpointsReady(),
					reconciletesting.WithNatssChannelDeploymentReady(),
					reconciletesting.Addressable(),
					reconciletesting.WithReady,
				),
			},
			WantPatches: []clientgotesting.PatchActionImpl{
				makeFinalizerPatch(testNS, ncName),
			},
			WantEvents: []string{
				finalizerUpdatedEvent,
			},
		},
		{
			Name: "with subscribers, works",
			Key:  ncKey,
			Objects: []runtime.Object{
				reconciletesting.NewNatssChannel(ncName, testNS,
					reconciletesting.WithNatssChannelChannelServiceReady(),
					reconciletesting.WithNatssChannelServiceReady(),
					reconciletesting.WithNatssChannelEndpointsReady(),
					reconciletesting.WithNatssChannelDeploymentReady(),
					reconciletesting.Addressable(),
					reconciletesting.WithReady,
					reconciletesting.WithNatssChannelSubscribers(subscribers),
				),
			},
			WantPatches: []clientgotesting.PatchActionImpl{
				makeFinalizerPatch(testNS, ncName),
				makePatch(testNS, ncName, twoSubscriberPatch),
			},
			WantEvents: []string{
				finalizerUpdatedEvent,
			},
		},
		{
			Name: "with subscribers, patch fails",
			Key:  ncKey,
			Objects: []runtime.Object{
				reconciletesting.NewNatssChannel(ncName, testNS,
					reconciletesting.WithNatssChannelChannelServiceReady(),
					reconciletesting.WithNatssChannelServiceReady(),
					reconciletesting.WithNatssChannelEndpointsReady(),
					reconciletesting.WithNatssChannelDeploymentReady(),
					reconciletesting.Addressable(),
					reconciletesting.WithReady,
					reconciletesting.WithNatssChannelSubscribers(subscribers),
				),
			},
			WithReactors: []clientgotesting.ReactionFunc{
				InduceFailure("patch", "natsschannels/status"),
			},
			WantPatches: []clientgotesting.PatchActionImpl{
				makeFinalizerPatch(testNS, ncName),
				makePatch(testNS, ncName, twoSubscriberPatch),
			},
			WantEvents: []string{
				finalizerUpdatedEvent,
				Eventf(corev1.EventTypeWarning, "InternalError", "Failed patching: inducing failure for patch natsschannels"),
			},
			WantErr: true,
		},
	}

	table.Test(t, reconciletesting.MakeFactory(func(ctx context.Context, listers *reconciletesting.Listers) controller.Reconciler {
		return createReconciler(ctx, listers, func() dispatcher.NatssDispatcher {
			return dispatchertesting.NewDispatcherDoNothing()
		})
	}))
}

type failOnFatalAndErrorLogger struct {
	*zap.Logger
	t *testing.T
}

func (l *failOnFatalAndErrorLogger) Error(msg string, fields ...zap.Field) {
	l.t.Fatalf("Error() called - msg: %s - fields: %v", msg, fields)
}

func (l *failOnFatalAndErrorLogger) Fatal(msg string, fields ...zap.Field) {
	l.t.Fatalf("Fatal() called - msg: %s - fields: %v", msg, fields)
}

func TestNewController(t *testing.T) {
	os.Setenv("POD_NAME", "testpod")
	os.Setenv("CONTAINER_NAME", "testcontainer")

	logger := failOnFatalAndErrorLogger{
		Logger: zap.NewNop(),
		t:      t,
	}
	ctx := logging.WithLogger(context.Background(), logger.Sugar())
	ctx, _ = fakekubeclient.With(ctx)
	ctx, _ = fakeeventingclient.With(ctx)
	ctx, _ = fakedynamicclient.With(ctx, runtime.NewScheme())
	ctx, _ = fakeclientset.With(ctx)
	cfg := &rest.Config{}
	ctx = injection.WithConfig(ctx, cfg)
	ctx, _ = injection.Fake.SetupInformers(ctx, cfg)

	NewController(ctx, configmap.NewStaticWatcher())
}

func TestFailedNatssSubscription(t *testing.T) {
	os.Setenv("POD_NAME", "testpod")
	os.Setenv("CONTAINER_NAME", "testcontainer")

	ncKey := testNS + "/" + ncName

	table := TableTest{
		{
			Name: "a failed natss subscription is reflected in Status.SubscribableStatus",
			Objects: []runtime.Object{
				reconciletesting.NewNatssChannel(ncName, testNS,
					reconciletesting.WithNatssChannelChannelServiceReady(),
					reconciletesting.WithNatssChannelServiceReady(),
					reconciletesting.WithNatssChannelEndpointsReady(),
					reconciletesting.WithNatssChannelDeploymentReady(),
					reconciletesting.Addressable(),
					reconciletesting.WithReady,
					// add subscriber for channel
					reconciletesting.WithNatssChannelSubscribers(subscribers),
				),
			},
			Key: ncKey,
			WantEvents: []string{
				finalizerUpdatedEvent,
				Eventf(corev1.EventTypeWarning, "InternalError", "\nups\nups"),
			},
			WantPatches: []clientgotesting.PatchActionImpl{
				makeFinalizerPatch(testNS, ncName),
				makePatch(testNS, ncName, twoSubscriberPatchFailed),
			},
			WantErr: true,
		},
	}

	table.Test(t, reconciletesting.MakeFactory(func(ctx context.Context, listers *reconciletesting.Listers) controller.Reconciler {
		return createReconciler(ctx, listers, func() dispatcher.NatssDispatcher {
			return dispatchertesting.NewDispatcherFailNatssSubscription()
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

func createReconciler(
	ctx context.Context,
	listers *reconciletesting.Listers,
	dispatcherFactory func() dispatcher.NatssDispatcher,
) controller.Reconciler {

	return natsschannelreconciler.NewReconciler(
		ctx,
		logging.FromContext(ctx),
		client.Get(ctx),
		listers.GetNatssChannelLister(),
		controller.GetEventRecorder(ctx),
		&Reconciler{
			natssDispatcher:    dispatcherFactory(),
			natsschannelLister: listers.GetNatssChannelLister(),
			natssClientSet:     client.Get(ctx),
		},
		controller.Options{
			FinalizerName: finalizerName,
		},
	)
}
