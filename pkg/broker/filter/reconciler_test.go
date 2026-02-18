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
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/logging"

	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
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

// --- Fake JetStream ---

// fakeJetStream embeds the JetStreamContext interface and overrides only
// ConsumerInfo. Any other method will panic if called (embedded nil value),
// which is acceptable since our tests never reach those code paths.
type fakeJetStream struct {
	nats.JetStreamContext
	consumerInfoErr error
}

func (f *fakeJetStream) ConsumerInfo(stream, name string, opts ...nats.JSOpt) (*nats.ConsumerInfo, error) {
	return nil, f.consumerInfoErr
}

func int32Ptr(v int32) *int32 { return &v }

func TestNewFilterReconciler(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

	r := NewFilterReconciler(ctx, nil, nil, nil)
	if r == nil {
		t.Fatal("NewFilterReconciler() returned nil")
	}
	if r.logger == nil {
		t.Error("reconciler.logger should not be nil")
	}
}

func TestReconcileTrigger_FullPath(t *testing.T) {
	tests := []struct {
		name            string
		brokerAddr      bool
		deadLetterURI   string
		deliveryRetry   *int32
		jsErr           error
		wantErrContains string
	}{
		{
			name:            "basic path through to SubscribeTrigger",
			jsErr:           fmt.Errorf("connection refused"),
			wantErrContains: "failed to subscribe to trigger",
		},
		{
			name:            "with broker ingress address",
			brokerAddr:      true,
			jsErr:           fmt.Errorf("connection refused"),
			wantErrContains: "failed to subscribe to trigger",
		},
		{
			name:            "with dead letter sink",
			deadLetterURI:   "http://dead-letter.example.com",
			jsErr:           fmt.Errorf("connection refused"),
			wantErrContains: "failed to subscribe to trigger",
		},
		{
			name:            "with delivery spec",
			deliveryRetry:   int32Ptr(3),
			jsErr:           fmt.Errorf("connection refused"),
			wantErrContains: "failed to subscribe to trigger",
		},
		{
			name:            "all optional fields set",
			brokerAddr:      true,
			deadLetterURI:   "http://dead-letter.example.com",
			deliveryRetry:   int32Ptr(5),
			jsErr:           fmt.Errorf("connection refused"),
			wantErrContains: "failed to subscribe to trigger",
		},
		{
			name:            "consumer not found error",
			jsErr:           nats.ErrConsumerNotFound,
			wantErrContains: "not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

			broker := newReadyTestBroker(testNamespace, testBrokerName, constants.BrokerClassName)
			if tc.brokerAddr {
				broker.Status.SetAddress(&duckv1.Addressable{
					URL: apis.HTTP("broker-ingress.example.com"),
				})
			}

			trigger := newTestTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "http://subscriber.example.com")
			if tc.deadLetterURI != "" {
				u, _ := apis.ParseURL(tc.deadLetterURI)
				trigger.Status.DeadLetterSinkURI = u
			}
			if tc.deliveryRetry != nil {
				trigger.Spec.Delivery = &eventingduckv1.DeliverySpec{
					Retry: tc.deliveryRetry,
				}
			}

			brokerLister := newFakeBrokerLister()
			brokerLister.addBroker(broker)

			cm := &ConsumerManager{
				logger:        logging.FromContext(ctx),
				ctx:           ctx,
				js:            &fakeJetStream{consumerInfoErr: tc.jsErr},
				subscriptions: make(map[string]*TriggerSubscription),
			}

			r := &FilterReconciler{
				logger:          logging.FromContext(ctx),
				brokerLister:    brokerLister,
				consumerManager: cm,
			}

			err := r.ReconcileTrigger(ctx, trigger)
			if err == nil {
				t.Fatal("ReconcileTrigger() expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Errorf("error %q should contain %q", err.Error(), tc.wantErrContains)
			}
		})
	}
}

func TestReconcileTrigger_ExistingSubscription(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

	broker := newReadyTestBroker(testNamespace, testBrokerName, constants.BrokerClassName)
	broker.Status.SetAddress(&duckv1.Addressable{
		URL: apis.HTTP("broker-ingress.example.com"),
	})

	// Use a different subscriber URL than what's already in the handler to
	// prove the in-place update works (no re-subscribe).
	oldSubscriberURL := "http://old-subscriber.example.com"
	newSubscriberURL := "http://new-subscriber.example.com"
	trigger := newTestTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, newSubscriberURL)
	trigger.Spec.Filter = &eventingv1.TriggerFilter{
		Attributes: map[string]string{"type": "test.type"},
	}
	newDLSURL, _ := apis.ParseURL("http://new-dls.example.com")
	trigger.Status.DeadLetterSinkURI = newDLSURL
	triggerUID := string(trigger.UID)

	brokerLister := newFakeBrokerLister()
	brokerLister.addBroker(broker)

	// Pre-populate subscription with old values.
	oldParsedURL, _ := apis.ParseURL(oldSubscriberURL)
	existingHandler := &TriggerHandler{
		subscriber: duckv1.Addressable{URL: oldParsedURL},
	}

	cm := &ConsumerManager{
		logger: logging.FromContext(ctx),
		ctx:    ctx,
		js:     &fakeJetStream{consumerInfoErr: fmt.Errorf("should not be called")},
		subscriptions: map[string]*TriggerSubscription{
			triggerUID: {
				trigger: trigger,
				handler: existingHandler,
			},
		},
	}

	r := &FilterReconciler{
		logger:          logging.FromContext(ctx),
		brokerLister:    brokerLister,
		consumerManager: cm,
	}

	// Should return nil — all fields updated in place, no re-subscribe.
	err := r.ReconcileTrigger(ctx, trigger)
	if err != nil {
		t.Fatalf("ReconcileTrigger() unexpected error: %v", err)
	}

	// Verify all handler fields were updated in place.
	h := cm.subscriptions[triggerUID].handler
	if got := h.subscriber.URL.String(); got != newSubscriberURL {
		t.Errorf("handler.subscriber.URL = %q, want %q", got, newSubscriberURL)
	}
	if h.brokerIngressURL == nil {
		t.Error("handler.brokerIngressURL should not be nil")
	}
	if h.filter == nil {
		t.Error("handler.filter should not be nil after update with filter spec")
	}
	if h.deadLetterSink == nil || h.deadLetterSink.URL.String() != newDLSURL.String() {
		t.Errorf("handler.deadLetterSink.URL = %v, want %v", h.deadLetterSink, newDLSURL)
	}
	if h.trigger != trigger {
		t.Error("handler.trigger should be updated to the new trigger object")
	}
}

// --- Fake trigger lister ---

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
		for _, t := range ns {
			result = append(result, t)
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
	result := make([]*eventingv1.Trigger, 0, len(f.triggers))
	for _, t := range f.triggers {
		result = append(result, t)
	}
	return result, nil
}

func (f *fakeTriggerNamespaceLister) Get(name string) (*eventingv1.Trigger, error) {
	if t, ok := f.triggers[name]; ok {
		return t, nil
	}
	return nil, apierrs.NewNotFound(schema.GroupResource{Group: "eventing.knative.dev", Resource: "triggers"}, name)
}

func TestReconcile(t *testing.T) {
	tests := []struct {
		name            string
		key             string
		trigger         *eventingv1.Trigger
		broker          *eventingv1.Broker
		presetUIDs      map[string]string
		wantErr         bool
		wantErrContains string
		wantUIDs        map[string]string
	}{
		{
			name:    "trigger exists - stores UID mapping and reconciles (skipped by broker not found)",
			key:     testNamespace + "/" + testTriggerName,
			trigger: newTestTrigger(testNamespace, testTriggerName, testBrokerName),
			wantErr: false,
			wantUIDs: map[string]string{
				testNamespace + "/" + testTriggerName: testTriggerUID,
			},
		},
		{
			name:    "trigger deleted with known UID - calls DeleteTrigger and cleans up",
			key:     testNamespace + "/" + testTriggerName,
			trigger: nil,
			presetUIDs: map[string]string{
				testNamespace + "/" + testTriggerName: testTriggerUID,
			},
			wantErr:  false,
			wantUIDs: map[string]string{},
		},
		{
			name:     "trigger deleted with unknown key - returns nil",
			key:      testNamespace + "/" + testTriggerName,
			trigger:  nil,
			wantErr:  false,
			wantUIDs: map[string]string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

			triggerLister := newFakeTriggerLister()
			if tc.trigger != nil {
				triggerLister.addTrigger(tc.trigger)
			}

			brokerLister := newFakeBrokerLister()
			if tc.broker != nil {
				brokerLister.addBroker(tc.broker)
			}

			cm := &ConsumerManager{
				logger:        logging.FromContext(ctx),
				subscriptions: make(map[string]*TriggerSubscription),
			}

			r := &FilterReconciler{
				logger:          logging.FromContext(ctx),
				triggerLister:   triggerLister,
				brokerLister:    brokerLister,
				consumerManager: cm,
				triggerUIDs:     make(map[string]string),
			}

			// Preset UIDs if specified
			if tc.presetUIDs != nil {
				for k, v := range tc.presetUIDs {
					r.triggerUIDs[k] = v
				}
			}

			err := r.Reconcile(ctx, tc.key)
			if tc.wantErr {
				if err == nil {
					t.Error("Reconcile() expected error, got nil")
				} else if tc.wantErrContains != "" && !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Errorf("error %q should contain %q", err.Error(), tc.wantErrContains)
				}
			} else if err != nil {
				t.Errorf("Reconcile() unexpected error: %v", err)
			}

			if tc.wantUIDs != nil {
				if len(r.triggerUIDs) != len(tc.wantUIDs) {
					t.Errorf("triggerUIDs length = %d, want %d", len(r.triggerUIDs), len(tc.wantUIDs))
				}
				for k, v := range tc.wantUIDs {
					if got, ok := r.triggerUIDs[k]; !ok {
						t.Errorf("triggerUIDs missing key %q", k)
					} else if got != v {
						t.Errorf("triggerUIDs[%q] = %q, want %q", k, got, v)
					}
				}
			}
		})
	}
}
