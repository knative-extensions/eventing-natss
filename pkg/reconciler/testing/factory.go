/*
Copyright 2019 The Knative Authors.

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

	clientgotesting "k8s.io/client-go/testing"
	fakeclientset "knative.dev/eventing-natss/pkg/client/injection/client/fake"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/client-go/tools/record"

	fakeeventingclient "knative.dev/eventing/pkg/client/injection/client/fake"

	fakekubeclient "knative.dev/pkg/client/injection/kube/client/fake"
	"knative.dev/pkg/controller"
	fakedynamicclient "knative.dev/pkg/injection/clients/dynamicclient/fake"
	"knative.dev/pkg/logging"
	logtesting "knative.dev/pkg/logging/testing"
	"knative.dev/pkg/reconciler"
	reconcilertesting "knative.dev/pkg/reconciler/testing"
)

const (
	// maxEventBufferSize is the estimated max number of event notifications that
	// can be buffered during reconciliation.
	maxEventBufferSize = 10
)

// Ctor functions create a k8s controller with given params.
type Ctor func(context.Context, *Listers) controller.Reconciler

// MakeFactory creates a reconciler factory with fake clients and controller created by `ctor`.
func MakeFactory(ctor Ctor) reconcilertesting.Factory {
	return func(t *testing.T, r *reconcilertesting.TableRow) (controller.Reconciler, reconcilertesting.ActionRecorderList, reconcilertesting.EventList) {
		ls := NewListers(r.Objects)

		ctx := logging.WithLogger(context.Background(), logtesting.TestLogger(t))
		ctx, kubeClient := fakekubeclient.With(ctx, ls.GetKubeObjects()...)
		ctx, eventingClient := fakeeventingclient.With(ctx, ls.GetEventingObjects()...)
		ctx, client := fakeclientset.With(ctx, ls.GetNatssObjects()...)

		dynamicScheme := runtime.NewScheme()
		for _, addTo := range clientSetSchemes {
			addTo(dynamicScheme)
		}

		ctx, dynamicClient := fakedynamicclient.With(ctx, dynamicScheme, ls.GetAllObjects()...)
		eventRecorder := record.NewFakeRecorder(maxEventBufferSize)
		ctx = controller.WithEventRecorder(ctx, eventRecorder)

		// Set up our Controller from the fakes.
		for _, reactor := range r.WithReactors {
			kubeClient.PrependReactor("*", "*", reactor)
			eventingClient.PrependReactor("*", "*", reactor)
			client.PrependReactor("*", "*", reactor)
			dynamicClient.PrependReactor("*", "*", reactor)
		}

		// Validate all Create operations through the eventing client.
		client.PrependReactor("create", "*", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
			return reconcilertesting.ValidateCreates(context.Background(), action)
		})
		client.PrependReactor("update", "*", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
			return reconcilertesting.ValidateUpdates(context.Background(), action)
		})

		actionRecorderList := reconcilertesting.ActionRecorderList{dynamicClient, client, kubeClient}
		eventList := reconcilertesting.EventList{Recorder: eventRecorder}

		if r.Ctx == nil {
			r.Ctx = ctx
		}

		c := ctor(ctx, &ls)

		// The Reconciler won't do any work until it becomes the leader.
		if la, ok := c.(reconciler.LeaderAware); ok {
			la.Promote(reconciler.UniversalBucket(), func(reconciler.Bucket, types.NamespacedName) {})
		}

		return c, actionRecorderList, eventList
	}
}
