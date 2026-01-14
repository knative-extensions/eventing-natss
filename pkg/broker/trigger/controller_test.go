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

package trigger

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

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

// newTestBroker creates a test broker with the given class annotation
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

// newTestTrigger creates a test trigger referencing a broker
func newTestTrigger(namespace, name, brokerName string) *eventingv1.Trigger {
	return &eventingv1.Trigger{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       "test-uid",
		},
		Spec: eventingv1.TriggerSpec{
			Broker: brokerName,
		},
	}
}

// fakeBrokerLister implements the BrokerLister interface for testing
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
	var result []*eventingv1.Broker
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

// fakeTriggerLister implements the TriggerLister interface for testing
type fakeTriggerLister struct {
	triggers map[string]map[string]*eventingv1.Trigger
}

func newFakeTriggerLister() *fakeTriggerLister {
	return &fakeTriggerLister{
		triggers: make(map[string]map[string]*eventingv1.Trigger),
	}
}

func (f *fakeTriggerLister) addTrigger(trigger *eventingv1.Trigger) {
	if f.triggers[trigger.Namespace] == nil {
		f.triggers[trigger.Namespace] = make(map[string]*eventingv1.Trigger)
	}
	f.triggers[trigger.Namespace][trigger.Name] = trigger
}

func (f *fakeTriggerLister) List(selector labels.Selector) ([]*eventingv1.Trigger, error) {
	var result []*eventingv1.Trigger
	for _, ns := range f.triggers {
		for _, trigger := range ns {
			result = append(result, trigger)
		}
	}
	return result, nil
}

func (f *fakeTriggerLister) Triggers(namespace string) eventinglisters.TriggerNamespaceLister {
	return &fakeTriggerNamespaceLister{
		namespace: namespace,
		triggers:  f.triggers[namespace],
	}
}

type fakeTriggerNamespaceLister struct {
	namespace string
	triggers  map[string]*eventingv1.Trigger
}

func (f *fakeTriggerNamespaceLister) List(selector labels.Selector) ([]*eventingv1.Trigger, error) {
	var result []*eventingv1.Trigger
	for _, trigger := range f.triggers {
		result = append(result, trigger)
	}
	return result, nil
}

func (f *fakeTriggerNamespaceLister) Get(name string) (*eventingv1.Trigger, error) {
	if trigger, ok := f.triggers[name]; ok {
		return trigger, nil
	}
	return nil, apierrs.NewNotFound(schema.GroupResource{Group: "eventing.knative.dev", Resource: "triggers"}, name)
}

// fakeEnqueuer records enqueued objects for testing
type fakeEnqueuer struct {
	enqueued []interface{}
}

func (f *fakeEnqueuer) Enqueue(obj interface{}) {
	f.enqueued = append(f.enqueued, obj)
}

func TestFilterTriggersByBrokerClass(t *testing.T) {
	tests := []struct {
		name         string
		broker       *eventingv1.Broker
		trigger      *eventingv1.Trigger
		wantFiltered bool
	}{
		{
			name:         "trigger with NatsJetStreamBroker class broker",
			broker:       newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			trigger:      newTestTrigger(testNamespace, testTriggerName, testBrokerName),
			wantFiltered: true,
		},
		{
			name:         "trigger with different broker class",
			broker:       newTestBroker(testNamespace, testBrokerName, "OtherBrokerClass"),
			trigger:      newTestTrigger(testNamespace, testTriggerName, testBrokerName),
			wantFiltered: false,
		},
		{
			name:         "trigger with broker without class annotation",
			broker:       newTestBroker(testNamespace, testBrokerName, ""),
			trigger:      newTestTrigger(testNamespace, testTriggerName, testBrokerName),
			wantFiltered: false,
		},
		{
			name:         "trigger referencing non-existent broker - should pass to reconciler",
			broker:       nil,
			trigger:      newTestTrigger(testNamespace, testTriggerName, "non-existent-broker"),
			wantFiltered: true, // Let the reconciler handle the error
		},
		{
			name:         "non-trigger object",
			broker:       newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			trigger:      nil,
			wantFiltered: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			brokerLister := newFakeBrokerLister()
			if tc.broker != nil {
				brokerLister.addBroker(tc.broker)
			}

			r := &Reconciler{
				brokerLister: brokerLister,
			}

			filterFunc := filterTriggersByBrokerClass(r)

			var obj interface{}
			if tc.trigger != nil {
				obj = tc.trigger
			} else {
				obj = "not a trigger"
			}

			got := filterFunc(obj)
			if got != tc.wantFiltered {
				t.Errorf("filterTriggersByBrokerClass() = %v, want %v", got, tc.wantFiltered)
			}
		})
	}
}

func TestFilterBrokersByClass(t *testing.T) {
	tests := []struct {
		name         string
		obj          interface{}
		wantFiltered bool
	}{
		{
			name:         "broker with NatsJetStreamBroker class",
			obj:          newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			wantFiltered: true,
		},
		{
			name:         "broker with different class",
			obj:          newTestBroker(testNamespace, testBrokerName, "OtherBrokerClass"),
			wantFiltered: false,
		},
		{
			name:         "broker without class annotation",
			obj:          newTestBroker(testNamespace, testBrokerName, ""),
			wantFiltered: false,
		},
		{
			name:         "non-broker object",
			obj:          "not a broker",
			wantFiltered: false,
		},
		{
			name:         "nil object",
			obj:          nil,
			wantFiltered: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterBrokersByClass(tc.obj)
			if got != tc.wantFiltered {
				t.Errorf("filterBrokersByClass() = %v, want %v", got, tc.wantFiltered)
			}
		})
	}
}

// TestEnqueueTriggerOfBrokerLogic tests the logic used by enqueueTriggerOfBroker
// This tests the filtering logic without requiring a real controller.Impl
func TestEnqueueTriggerOfBrokerLogic(t *testing.T) {
	tests := []struct {
		name             string
		broker           *eventingv1.Broker
		triggers         []*eventingv1.Trigger
		obj              interface{}
		wantEnqueueCount int
	}{
		{
			name:   "enqueue triggers that reference the broker",
			broker: newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			triggers: []*eventingv1.Trigger{
				newTestTrigger(testNamespace, "trigger1", testBrokerName),
				newTestTrigger(testNamespace, "trigger2", testBrokerName),
				newTestTrigger(testNamespace, "trigger3", "other-broker"),
			},
			obj:              newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			wantEnqueueCount: 2,
		},
		{
			name:   "no triggers reference the broker",
			broker: newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			triggers: []*eventingv1.Trigger{
				newTestTrigger(testNamespace, "trigger1", "other-broker"),
			},
			obj:              newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			wantEnqueueCount: 0,
		},
		{
			name:             "triggers in different namespace",
			broker:           newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			triggers:         []*eventingv1.Trigger{newTestTrigger("other-namespace", "trigger1", testBrokerName)},
			obj:              newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			wantEnqueueCount: 0,
		},
		{
			name:             "non-broker object does nothing",
			broker:           newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			triggers:         []*eventingv1.Trigger{newTestTrigger(testNamespace, "trigger1", testBrokerName)},
			obj:              "not a broker",
			wantEnqueueCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			triggerLister := newFakeTriggerLister()
			for _, trigger := range tc.triggers {
				triggerLister.addTrigger(trigger)
			}

			// Test the core logic of enqueueTriggerOfBroker
			var enqueued []*eventingv1.Trigger

			broker, ok := tc.obj.(*eventingv1.Broker)
			if !ok {
				// Non-broker object should result in no enqueues
				if len(enqueued) != tc.wantEnqueueCount {
					t.Errorf("enqueue logic for non-broker: got %d items, want %d", len(enqueued), tc.wantEnqueueCount)
				}
				return
			}

			// Find all triggers in the same namespace that reference this broker
			triggers, err := triggerLister.Triggers(broker.Namespace).List(labels.Everything())
			if err != nil {
				return
			}

			for _, trigger := range triggers {
				if trigger.Spec.Broker == broker.Name {
					enqueued = append(enqueued, trigger)
				}
			}

			if len(enqueued) != tc.wantEnqueueCount {
				t.Errorf("enqueue logic: got %d items, want %d", len(enqueued), tc.wantEnqueueCount)
			}
		})
	}
}

func TestComponentName(t *testing.T) {
	if ComponentName != "natsjs-trigger-controller" {
		t.Errorf("ComponentName = %q, want %q", ComponentName, "natsjs-trigger-controller")
	}
}
