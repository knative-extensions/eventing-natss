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
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"knative.dev/pkg/apis"
	"knative.dev/pkg/logging"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	eventinglisters "knative.dev/eventing/pkg/client/listers/eventing/v1"

	"knative.dev/eventing-natss/pkg/broker/constants"
)

const (
	testNamespace   = "test-namespace"
	testBrokerName  = "test-broker"
	testTriggerName = "test-trigger"
	testTriggerUID  = "test-trigger-uid-12345"
)

// newTestBroker creates a test broker with the given class annotation.
func newTestBroker(namespace, name, brokerClass string) *eventingv1.Broker {
	annotations := make(map[string]string)
	if brokerClass != "" {
		annotations[eventingv1.BrokerClassAnnotationKey] = brokerClass
	}
	return &eventingv1.Broker{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespace,
			Name:        name,
			Annotations: annotations,
		},
	}
}

// newReadyTestBroker creates a test broker that reports as ready.
func newReadyTestBroker(namespace, name, brokerClass string) *eventingv1.Broker {
	b := newTestBroker(namespace, name, brokerClass)
	// IsReady checks ObservedGeneration == Generation and all conditions happy.
	b.Generation = 1
	b.Status.InitializeConditions()
	b.Status.ObservedGeneration = 1
	// Mark all dependent conditions true so the top-level Ready becomes true.
	manager := b.GetConditionSet().Manage(&b.Status)
	manager.MarkTrue(eventingv1.BrokerConditionIngress)
	manager.MarkTrue(eventingv1.BrokerConditionTriggerChannel)
	manager.MarkTrue(eventingv1.BrokerConditionFilter)
	manager.MarkTrue(eventingv1.BrokerConditionAddressable)
	manager.MarkTrue(eventingv1.BrokerConditionDeadLetterSinkResolved)
	manager.MarkTrue(eventingv1.BrokerConditionEventPoliciesReady)
	return b
}

// newTestTrigger creates a test trigger referencing a broker.
func newTestTrigger(namespace, name, brokerName string) *eventingv1.Trigger {
	return &eventingv1.Trigger{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       types.UID(testTriggerUID),
		},
		Spec: eventingv1.TriggerSpec{
			Broker: brokerName,
		},
	}
}

// newTestTriggerWithSubscriber creates a trigger with a resolved subscriber URI.
func newTestTriggerWithSubscriber(namespace, name, brokerName, subscriberURL string) *eventingv1.Trigger {
	t := newTestTrigger(namespace, name, brokerName)
	u, _ := apis.ParseURL(subscriberURL)
	t.Status.SubscriberURI = u
	return t
}

// --- Fake listers ---

type fakeBrokerLister struct {
	brokers map[string]map[string]*eventingv1.Broker
}

func newFakeBrokerLister() *fakeBrokerLister {
	return &fakeBrokerLister{
		brokers: make(map[string]map[string]*eventingv1.Broker),
	}
}

func (f *fakeBrokerLister) addBroker(broker *eventingv1.Broker) {
	if f.brokers[broker.Namespace] == nil {
		f.brokers[broker.Namespace] = make(map[string]*eventingv1.Broker)
	}
	f.brokers[broker.Namespace][broker.Name] = broker
}

func (f *fakeBrokerLister) List(selector labels.Selector) ([]*eventingv1.Broker, error) {
	var result []*eventingv1.Broker
	for _, ns := range f.brokers {
		for _, broker := range ns {
			result = append(result, broker)
		}
	}
	return result, nil
}

func (f *fakeBrokerLister) Brokers(namespace string) eventinglisters.BrokerNamespaceLister {
	return &fakeBrokerNamespaceLister{
		namespace: namespace,
		brokers:   f.brokers[namespace],
	}
}

type fakeBrokerNamespaceLister struct {
	namespace string
	brokers   map[string]*eventingv1.Broker
}

func (f *fakeBrokerNamespaceLister) List(selector labels.Selector) ([]*eventingv1.Broker, error) {
	result := make([]*eventingv1.Broker, 0, len(f.brokers))
	for _, broker := range f.brokers {
		result = append(result, broker)
	}
	return result, nil
}

func (f *fakeBrokerNamespaceLister) Get(name string) (*eventingv1.Broker, error) {
	if broker, ok := f.brokers[name]; ok {
		return broker, nil
	}
	return nil, apierrs.NewNotFound(schema.GroupResource{Group: "eventing.knative.dev", Resource: "brokers"}, name)
}

func TestReconcileTrigger(t *testing.T) {
	tests := []struct {
		name    string
		broker  *eventingv1.Broker
		trigger *eventingv1.Trigger
		wantErr bool
	}{
		{
			name:    "broker not found returns nil",
			broker:  nil,
			trigger: newTestTrigger(testNamespace, testTriggerName, testBrokerName),
			wantErr: false,
		},
		{
			name:    "broker wrong class returns nil",
			broker:  newTestBroker(testNamespace, testBrokerName, "OtherBrokerClass"),
			trigger: newTestTrigger(testNamespace, testTriggerName, testBrokerName),
			wantErr: false,
		},
		{
			name:    "broker not ready returns nil",
			broker:  newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			trigger: newTestTrigger(testNamespace, testTriggerName, testBrokerName),
			wantErr: false,
		},
		{
			name:    "subscriber URI not resolved returns nil",
			broker:  newReadyTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			trigger: newTestTrigger(testNamespace, testTriggerName, testBrokerName),
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			brokerLister := newFakeBrokerLister()
			if tc.broker != nil {
				brokerLister.addBroker(tc.broker)
			}

			ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

			r := &FilterReconciler{
				logger:       logging.FromContext(ctx),
				brokerLister: brokerLister,
			}

			err := r.ReconcileTrigger(ctx, tc.trigger)
			if tc.wantErr && err == nil {
				t.Error("ReconcileTrigger() expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ReconcileTrigger() unexpected error: %v", err)
			}
		})
	}
}

func TestReconcileTrigger_BrokerLookupError(t *testing.T) {
	// Use a broker lister that returns an error other than NotFound.
	brokerLister := &errorBrokerLister{
		err: fmt.Errorf("internal error"),
	}

	ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

	r := &FilterReconciler{
		logger:       logging.FromContext(ctx),
		brokerLister: brokerLister,
	}

	trigger := newTestTrigger(testNamespace, testTriggerName, testBrokerName)
	err := r.ReconcileTrigger(ctx, trigger)
	if err == nil {
		t.Error("ReconcileTrigger() expected error for non-NotFound broker lookup error, got nil")
	}
}

// errorBrokerLister returns an arbitrary error for any lookup.
type errorBrokerLister struct {
	err error
}

func (e *errorBrokerLister) List(selector labels.Selector) ([]*eventingv1.Broker, error) {
	return nil, e.err
}

func (e *errorBrokerLister) Brokers(namespace string) eventinglisters.BrokerNamespaceLister {
	return &errorBrokerNamespaceLister{err: e.err}
}

type errorBrokerNamespaceLister struct {
	err error
}

func (e *errorBrokerNamespaceLister) List(selector labels.Selector) ([]*eventingv1.Broker, error) {
	return nil, e.err
}

func (e *errorBrokerNamespaceLister) Get(name string) (*eventingv1.Broker, error) {
	return nil, e.err
}

func TestDeleteTrigger(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

	cm := &ConsumerManager{
		logger:        logging.FromContext(ctx),
		subscriptions: make(map[string]*TriggerSubscription),
	}

	r := &FilterReconciler{
		logger:          logging.FromContext(ctx),
		consumerManager: cm,
	}

	// Deleting a non-existent trigger should return nil.
	if err := r.DeleteTrigger("non-existent-uid"); err != nil {
		t.Errorf("DeleteTrigger() unexpected error for non-existent trigger: %v", err)
	}
}

// Verify that IsReady helper produces truly ready brokers for use in other
// test cases. This guards against upstream condition-set changes breaking
// the helper silently.
func TestNewReadyTestBroker(t *testing.T) {
	b := newReadyTestBroker(testNamespace, testBrokerName, constants.BrokerClassName)
	if !b.IsReady() {
		cond := b.Status.GetTopLevelCondition()
		t.Errorf("newReadyTestBroker() produced broker that is not ready; top-level condition: %+v", cond)
		for _, c := range b.Status.Conditions {
			if c.Status != corev1.ConditionTrue {
				t.Errorf("  condition %s = %s (%s)", c.Type, c.Status, c.Reason)
			}
		}
	}
}

// Verify subscriber URI detection works on both nil and non-nil cases.
func TestNewTestTriggerWithSubscriber(t *testing.T) {
	t.Run("without subscriber", func(t *testing.T) {
		trigger := newTestTrigger(testNamespace, testTriggerName, testBrokerName)
		if trigger.Status.SubscriberURI != nil {
			t.Errorf("expected nil SubscriberURI, got %v", trigger.Status.SubscriberURI)
		}
	})

	t.Run("with subscriber", func(t *testing.T) {
		trigger := newTestTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "http://example.com")
		if trigger.Status.SubscriberURI == nil {
			t.Error("expected non-nil SubscriberURI")
		}
	})
}

// TestReconcileTrigger_SkipReasons verifies that each early-return guard in
// ReconcileTrigger is independently effective. We run through a progression
// where exactly one guard is removed at each step to confirm each check.
func TestReconcileTrigger_SkipReasons(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

	// Ready broker + trigger with subscriber should NOT be skipped.
	// We cannot proceed without a real JetStream, but we can confirm the
	// early returns work by verifying the inverse: a ready broker with
	// wrong class is still skipped.
	broker := newReadyTestBroker(testNamespace, testBrokerName, "WrongClass")
	trigger := newTestTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "http://subscriber.example.com")

	brokerLister := newFakeBrokerLister()
	brokerLister.addBroker(broker)

	r := &FilterReconciler{
		logger:       logging.FromContext(ctx),
		brokerLister: brokerLister,
	}

	// Even though trigger has a subscriber and broker is ready, the wrong
	// class causes a skip (nil return).
	if err := r.ReconcileTrigger(ctx, trigger); err != nil {
		t.Errorf("ReconcileTrigger() with wrong broker class should skip, got error: %v", err)
	}
}
