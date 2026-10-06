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

package filter

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/eventing-natss/pkg/broker/constants"
	brokerutils "knative.dev/eventing-natss/pkg/broker/utils"
	natsTesting "knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher/testing"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	"knative.dev/pkg/logging"
)

func TestBrokerScopeFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name, namespace, broker string
		wantErr                 bool
	}{
		{"complete", "namespace-a", "broker-a", false},
		{"missing namespace", "", "broker-a", true},
		{"missing Broker", "namespace-a", "", true},
		{"missing both", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BROKER_NAMESPACE", tc.namespace)
			t.Setenv("BROKER_NAME", tc.broker)
			scope, err := BrokerScopeFromEnv()
			if (err != nil) != tc.wantErr {
				t.Fatalf("BrokerScopeFromEnv() error = %v", err)
			}
			if err == nil && (scope.Namespace != tc.namespace || scope.Name != tc.broker) {
				t.Fatalf("unexpected scope: %+v", scope)
			}
		})
	}
}

func TestBrokerScopeOwnsTrigger(t *testing.T) {
	scope := BrokerScope{Namespace: "namespace-a", Name: "broker-a"}
	for _, tc := range []struct {
		name, namespace, broker string
		want                    bool
	}{
		{"owned", "namespace-a", "broker-a", true},
		{"another namespace", "namespace-b", "broker-a", false},
		{"another Broker", "namespace-a", "broker-b", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trigger := newTestTrigger(tc.namespace, "trigger", tc.broker)
			if got := scope.owns(trigger); got != tc.want {
				t.Fatalf("owns() = %v, want %v", got, tc.want)
			}
		})
	}
	if scope.owns(nil) {
		t.Fatal("nil Trigger must not be owned")
	}
}

func TestScopedReconcileSubscriptions(t *testing.T) {
	server := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, server)
	conn, js := natsTesting.JsClient(t, server)
	defer conn.Close()
	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())
	triggers := newFakeTriggerLister()
	brokers := newFakeBrokerLister()
	brokers.addBroker(newReadyTestBroker("namespace-a", "broker-a", constants.BrokerClassName))
	owned := newTestTriggerWithSubscriber("namespace-a", "trigger-a", "broker-a", "http://localhost:9999")
	owned.UID = types.UID("owned-uid")
	foreignBroker := newTestTriggerWithSubscriber("namespace-a", "trigger-b", "broker-b", "http://localhost:9999")
	foreignBroker.UID = types.UID("foreign-broker-uid")
	foreignNamespace := newTestTriggerWithSubscriber("namespace-b", "trigger-a", "broker-a", "http://localhost:9999")
	foreignNamespace.UID = types.UID("foreign-namespace-uid")
	for _, trigger := range []*eventingv1.Trigger{owned, foreignBroker, foreignNamespace} {
		triggers.addTrigger(trigger)
		setupStreamAndConsumer(t, js, trigger.Namespace, trigger.Spec.Broker, string(trigger.UID))
	}
	// Each generated filter replica independently subscribes to the same durable consumer.
	for replica := 0; replica < 2; replica++ {
		cm := newConsumerManagerForTest(t, ctx, conn, js, &ConsumerManagerConfig{FetchTimeout: 10 * time.Millisecond})
		defer cm.Close()
		r := NewFilterReconciler(ctx, triggers, brokers, cm)
		r.brokerScope = &BrokerScope{Namespace: "namespace-a", Name: "broker-a"}
		for _, key := range []string{"namespace-a/trigger-a", "namespace-a/trigger-b", "namespace-b/trigger-a"} {
			if err := r.Reconcile(ctx, key); err != nil {
				t.Fatal(err)
			}
		}
		if cm.GetSubscriptionCount() != 1 || !cm.HasSubscription("owned-uid") {
			t.Fatalf("replica %d subscribed outside its Broker: %d", replica, cm.GetSubscriptionCount())
		}
		// spec.broker is immutable, but a same-name replacement with a new UID
		// can target another Broker before the old deletion is reconciled.
		recreatedForOtherBroker := owned.DeepCopy()
		recreatedForOtherBroker.UID = types.UID("foreign-replacement-uid")
		recreatedForOtherBroker.Spec.Broker = "broker-b"
		triggers.addTrigger(recreatedForOtherBroker)
		if err := r.Reconcile(ctx, "namespace-a/trigger-a"); err != nil {
			t.Fatal(err)
		}
		if cm.GetSubscriptionCount() != 0 {
			t.Fatal("Trigger recreated for another Broker retained a subscription")
		}
		triggers.addTrigger(owned)
		if err := r.Reconcile(ctx, "namespace-a/trigger-a"); err != nil {
			t.Fatal(err)
		}
		replacement := owned.DeepCopy()
		replacement.UID = types.UID("replacement-uid")
		// A new UID shares the existing stream but has its own durable consumer.
		_, err := js.AddConsumer(brokerutils.BrokerStreamName(brokers.brokers["namespace-a"]["broker-a"]), &nats.ConsumerConfig{
			Durable: brokerutils.TriggerConsumerName(string(replacement.UID)), AckPolicy: nats.AckExplicitPolicy,
			FilterSubject: brokerutils.BrokerPublishSubjectName("namespace-a", "broker-a") + ".>",
		})
		if err != nil {
			t.Fatal(err)
		}
		triggers.addTrigger(replacement)
		if err := r.Reconcile(ctx, "namespace-a/trigger-a"); err != nil {
			t.Fatal(err)
		}
		if cm.HasSubscription("owned-uid") || !cm.HasSubscription("replacement-uid") {
			t.Fatal("recreated Trigger retained the previous UID subscription")
		}
		// Remove the old key and reconcile its deletion before restoring the fixture.
		delete(triggers.triggers["namespace-a"], "trigger-a")
		if err := r.Reconcile(ctx, "namespace-a/trigger-a"); err != nil {
			t.Fatal(err)
		}
		if cm.GetSubscriptionCount() != 0 {
			t.Fatal("deleted Trigger retained a subscription")
		}
		triggers.addTrigger(owned)
	}
}
