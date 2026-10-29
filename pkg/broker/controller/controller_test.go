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
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	"knative.dev/pkg/tracker"

	brokerconfig "knative.dev/eventing-natss/pkg/broker/config"
)

func testConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
}

func TestConfigMapEventsEnqueueTrackingBrokers(t *testing.T) {
	var enqueued []types.NamespacedName
	tr := tracker.New(func(key types.NamespacedName) { enqueued = append(enqueued, key) }, time.Hour)
	for _, ref := range []struct{ namespace, broker, config string }{
		{testNamespace, "broker-a", "broker-config"},
		{testNamespace, "broker-b", "broker-config"},
		{testNamespace, "broker-c", "other-config"},
		{"other-ns", "broker-d", "broker-config"},
	} {
		err := tr.TrackReference(tracker.Reference{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Namespace:  ref.namespace,
			Name:       ref.config,
		}, testBroker(ref.namespace, ref.broker))
		if err != nil {
			t.Fatal(err)
		}
	}

	handler := enqueueBrokersOfConfigMap(tr)
	shared := []types.NamespacedName{
		{Namespace: testNamespace, Name: "broker-a"},
		{Namespace: testNamespace, Name: "broker-b"},
	}
	for _, tt := range []struct {
		name string
		obj  interface{}
		want []types.NamespacedName
	}{
		{name: "referenced by two brokers", obj: testConfigMap(testNamespace, "broker-config"), want: shared},
		{
			name: "delete tombstone",
			obj:  cache.DeletedFinalStateUnknown{Obj: testConfigMap(testNamespace, "broker-config")},
			want: shared,
		},
		{
			name: "same name in another namespace",
			obj:  testConfigMap("other-ns", "broker-config"),
			want: []types.NamespacedName{{Namespace: "other-ns", Name: "broker-d"}},
		},
		{
			name: "different config",
			obj:  testConfigMap(testNamespace, "other-config"),
			want: []types.NamespacedName{{Namespace: testNamespace, Name: "broker-c"}},
		},
		{name: "unreferenced config", obj: testConfigMap(testNamespace, "unreferenced")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			enqueued = nil // Discard the tracker's initial catch-up notifications.
			handler(tt.obj)
			sort.Slice(enqueued, func(i, j int) bool { return enqueued[i].String() < enqueued[j].String() })
			if diff := cmp.Diff(tt.want, enqueued); diff != "" {
				t.Errorf("enqueued brokers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A broker whose config can't be read stays tracked, so that creating or fixing
// the ConfigMap reconciles it again.
func TestBrokerWithBrokenConfigIsEnqueuedOnConfigMapEvent(t *testing.T) {
	cm := testConfigMap(testNamespace, "broker-config")
	invalid := cm.DeepCopy()
	invalid.Data = map[string]string{brokerconfig.ConfigKey: `{"filter":"invalid"}`}

	for _, tt := range []struct {
		name string
		seed []runtime.Object
	}{
		{name: "missing ConfigMap"},
		{name: "invalid ConfigMap", seed: []runtime.Object{invalid}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var enqueued []types.NamespacedName
			tr := tracker.New(func(key types.NamespacedName) { enqueued = append(enqueued, key) }, time.Hour)
			r := &Reconciler{kubeClientSet: kubefake.NewSimpleClientset(tt.seed...), tracker: tr}
			b := testBroker(testNamespace, testBrokerName)
			b.Annotations = map[string]string{brokerconfig.BrokerConfigAnnotation: cm.Name}
			if _, err := r.getBrokerConfig(testContext(), b); err == nil {
				t.Fatal("getBrokerConfig() succeeded, want error")
			}

			enqueued = nil // Discard the tracker's initial catch-up notifications.
			enqueueBrokersOfConfigMap(tr)(cm)
			want := []types.NamespacedName{{Namespace: b.Namespace, Name: b.Name}}
			if diff := cmp.Diff(want, enqueued); diff != "" {
				t.Errorf("enqueued brokers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
