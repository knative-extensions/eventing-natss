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
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"go.uber.org/zap"
	"knative.dev/pkg/logging"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	eventinglisters "knative.dev/eventing/pkg/client/listers/eventing/v1"

	"knative.dev/eventing-natss/pkg/broker/controller/resources"
)

const (
	testNamespace  = "test-ns"
	testBrokerName = "test-broker"
)

func testBroker(ns, name string) *eventingv1.Broker {
	return &eventingv1.Broker{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
}

func testTrigger(ns, name, brokerName string) *eventingv1.Trigger {
	return &eventingv1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       eventingv1.TriggerSpec{Broker: brokerName},
	}
}

func TestHasTriggers(t *testing.T) {
	tests := []struct {
		name     string
		triggers []*eventingv1.Trigger
		want     bool
	}{
		{name: "no triggers", want: false},
		{
			name:     "matching trigger",
			triggers: []*eventingv1.Trigger{testTrigger(testNamespace, "t1", testBrokerName)},
			want:     true,
		},
		{
			name:     "only trigger for another broker",
			triggers: []*eventingv1.Trigger{testTrigger(testNamespace, "t1", "other-broker")},
			want:     false,
		},
		{
			name:     "matching trigger in another namespace is ignored",
			triggers: []*eventingv1.Trigger{testTrigger("other-ns", "t1", testBrokerName)},
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lister := newFakeTriggerLister()
			for _, tr := range tc.triggers {
				lister.add(tr)
			}
			r := &Reconciler{triggerLister: lister}

			got, err := r.hasTriggers(testBroker(testNamespace, testBrokerName))
			if err != nil {
				t.Fatalf("hasTriggers() error: %v", err)
			}
			if got != tc.want {
				t.Errorf("hasTriggers() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeleteFilter(t *testing.T) {
	name := resources.FilterName(testBrokerName)

	t.Run("deletes existing filter deployment and service", func(t *testing.T) {
		kube := kubefake.NewSimpleClientset(
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: name}},
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: name}},
		)
		r := &Reconciler{kubeClientSet: kube}
		ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

		if err := r.deleteFilter(ctx, testBroker(testNamespace, testBrokerName)); err != nil {
			t.Fatalf("deleteFilter() error: %v", err)
		}

		if _, err := kube.AppsV1().Deployments(testNamespace).Get(ctx, name, metav1.GetOptions{}); !apierrs.IsNotFound(err) {
			t.Errorf("deployment: expected NotFound, got %v", err)
		}
		if _, err := kube.CoreV1().Services(testNamespace).Get(ctx, name, metav1.GetOptions{}); !apierrs.IsNotFound(err) {
			t.Errorf("service: expected NotFound, got %v", err)
		}
	})

	t.Run("no error when filter is already absent", func(t *testing.T) {
		r := &Reconciler{kubeClientSet: kubefake.NewSimpleClientset()}
		ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

		if err := r.deleteFilter(ctx, testBroker(testNamespace, testBrokerName)); err != nil {
			t.Errorf("deleteFilter() on absent filter returned error: %v", err)
		}
	})

	t.Run("surfaces a non-NotFound delete error and marks filter failed", func(t *testing.T) {
		kube := kubefake.NewSimpleClientset()
		kube.PrependReactor("delete", "deployments", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("boom")
		})
		r := &Reconciler{kubeClientSet: kube}
		ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())

		b := testBroker(testNamespace, testBrokerName)
		if err := r.deleteFilter(ctx, b); err == nil {
			t.Fatal("deleteFilter() expected an error, got nil")
		}
		if cond := b.Status.GetCondition(eventingv1.BrokerConditionFilter); cond == nil || cond.IsTrue() {
			t.Errorf("expected BrokerConditionFilter to be marked failed, got %v", cond)
		}
	})
}

// fakeTriggerLister implements eventinglisters.TriggerLister for testing.
type fakeTriggerLister struct {
	triggers map[string]map[string]*eventingv1.Trigger
}

func newFakeTriggerLister() *fakeTriggerLister {
	return &fakeTriggerLister{triggers: make(map[string]map[string]*eventingv1.Trigger)}
}

func (f *fakeTriggerLister) add(tr *eventingv1.Trigger) {
	if f.triggers[tr.Namespace] == nil {
		f.triggers[tr.Namespace] = make(map[string]*eventingv1.Trigger)
	}
	f.triggers[tr.Namespace][tr.Name] = tr
}

func (f *fakeTriggerLister) List(labels.Selector) ([]*eventingv1.Trigger, error) {
	var result []*eventingv1.Trigger
	for _, ns := range f.triggers {
		for _, tr := range ns {
			result = append(result, tr)
		}
	}
	return result, nil
}

func (f *fakeTriggerLister) Triggers(namespace string) eventinglisters.TriggerNamespaceLister {
	return &fakeTriggerNamespaceLister{triggers: f.triggers[namespace]}
}

type fakeTriggerNamespaceLister struct {
	triggers map[string]*eventingv1.Trigger
}

func (f *fakeTriggerNamespaceLister) List(labels.Selector) ([]*eventingv1.Trigger, error) {
	result := make([]*eventingv1.Trigger, 0, len(f.triggers))
	for _, tr := range f.triggers {
		result = append(result, tr)
	}
	return result, nil
}

func (f *fakeTriggerNamespaceLister) Get(name string) (*eventingv1.Trigger, error) {
	if tr, ok := f.triggers[name]; ok {
		return tr, nil
	}
	return nil, apierrs.NewNotFound(schema.GroupResource{Group: "eventing.knative.dev", Resource: "triggers"}, name)
}
