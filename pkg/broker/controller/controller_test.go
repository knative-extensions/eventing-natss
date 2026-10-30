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
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	"knative.dev/pkg/injection"
	"knative.dev/pkg/injection/clients/dynamicclient"
	"knative.dev/pkg/tracker"

	"knative.dev/eventing-natss/pkg/broker/constants"
)

func TestBrokerConfigInformerSyncsInvalidSpec(t *testing.T) {
	for _, namespace := range []string{"", testNamespace} {
		t.Run("scope="+namespace, func(t *testing.T) {
			path := "/apis/eventing.knative.dev/v1alpha1/"
			if namespace != "" {
				path += "namespaces/" + namespace + "/"
			}
			path += "natsjetstreambrokerconfigs"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != path {
					t.Errorf("request path = %q, want %q", req.URL.Path, path)
					http.NotFound(w, req)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if req.URL.Query().Get("watch") == "true" {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					<-req.Context().Done()
					return
				}
				fmt.Fprint(w, `{"apiVersion":"eventing.knative.dev/v1alpha1","kind":"NatsJetStreamBrokerConfigList","metadata":{"resourceVersion":"1"},"items":[
{"apiVersion":"eventing.knative.dev/v1alpha1","kind":"NatsJetStreamBrokerConfig","metadata":{"name":"good","namespace":"test-ns"},"spec":{"stream":{"replicas":3}}},
{"apiVersion":"eventing.knative.dev/v1alpha1","kind":"NatsJetStreamBrokerConfig","metadata":{"name":"large-integer","namespace":"test-ns"},"spec":{"stream":{"maxMsgs":9007199254740993,"sources":[{"name":"source","optStartSeq":9007199254740993}]}}},
{"apiVersion":"eventing.knative.dev/v1alpha1","kind":"NatsJetStreamBrokerConfig","metadata":{"name":"max-integer","namespace":"test-ns"},"spec":{"stream":{"maxMsgs":9223372036854775807,"sources":[{"name":"source","optStartSeq":9223372036854775807}]}}},
{"apiVersion":"eventing.knative.dev/v1alpha1","kind":"NatsJetStreamBrokerConfig","metadata":{"name":"bad-env","namespace":"test-ns"},"spec":{"filter":{"env":[{"name":"BATCH","value":50}]}}},
{"apiVersion":"eventing.knative.dev/v1alpha1","kind":"NatsJetStreamBrokerConfig","metadata":{"name":"bad-quantity","namespace":"test-ns"},"spec":{"filter":{"resources":{"limits":{"cpu":"1e1.1"}}}}},
{"apiVersion":"eventing.knative.dev/v1alpha1","kind":"NatsJetStreamBrokerConfig","metadata":{"name":"bad-range","namespace":"test-ns"},"spec":{"filter":{"replicas":2147483648}}},
{"apiVersion":"eventing.knative.dev/v1alpha1","kind":"NatsJetStreamBrokerConfig","metadata":{"name":"bad-time","namespace":"test-ns"},"spec":{"stream":{"mirror":{"name":"source","optStartTime":"2026-10-29"}}}},
{"apiVersion":"eventing.knative.dev/v1alpha1","kind":"NatsJetStreamBrokerConfig","metadata":{"name":"bad-integer","namespace":"test-ns"},"spec":{"stream":{"maxConsumers":9223372036854775808}}},
{"apiVersion":"eventing.knative.dev/v1alpha1","kind":"NatsJetStreamBrokerConfig","metadata":{"name":"bad-max-msgs","namespace":"test-ns"},"spec":{"stream":{"maxMsgs":9223372036854775808}}},
{"apiVersion":"eventing.knative.dev/v1alpha1","kind":"NatsJetStreamBrokerConfig","metadata":{"name":"bad-negative-seq","namespace":"test-ns"},"spec":{"stream":{"sources":[{"name":"source","optStartSeq":-1}]}}},
{"apiVersion":"eventing.knative.dev/v1alpha1","kind":"NatsJetStreamBrokerConfig","metadata":{"name":"bad-spec","namespace":"test-ns"},"spec":"not-an-object"},
{"apiVersion":"eventing.knative.dev/v1alpha1","kind":"NatsJetStreamBrokerConfig","metadata":{"name":"bad-duration","namespace":"test-ns"},"spec":{"stream":{"maxAge":"tomorrow"}}}
]}`)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(testContext(), 5*time.Second)
			defer cancel()
			client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			ctx = context.WithValue(ctx, dynamicclient.Key{}, client)
			if namespace != "" {
				ctx = injection.WithNamespaceScope(ctx, namespace)
			}
			ctx, informer := withBrokerConfigInformer(ctx)
			go informer.Run(ctx.Done())
			if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
				t.Fatal("config informer failed to sync with invalid configurations")
			}
			shared := getBrokerConfigInformer(ctx)
			r := newBrokerConfigReconciler()
			r.brokerConfigLister = cache.NewGenericLister(shared.GetIndexer(), brokerConfigGVR.GroupResource())
			for _, name := range []string{"bad-env", "bad-quantity", "bad-range", "bad-time", "bad-integer", "bad-max-msgs", "bad-negative-seq", "bad-spec", "bad-duration"} {
				b := testBroker(testNamespace, name)
				b.Spec.Config = testBrokerConfigRef(testNamespace, name)
				if err := r.ReconcileKind(ctx, b); err == nil {
					t.Errorf("ReconcileKind(%s) succeeded, want ConfigLoadFailed", name)
				}
				if cond := b.Status.GetCondition(eventingv1.BrokerConditionIngress); cond == nil || cond.Reason != "ConfigLoadFailed" {
					t.Errorf("ReconcileKind(%s) ingress condition = %+v, want ConfigLoadFailed", name, cond)
				}
			}
			for _, integer := range []struct {
				name string
				want int64
			}{
				{name: "large-integer", want: 9007199254740993},
				{name: "max-integer", want: math.MaxInt64},
			} {
				b := testBroker(testNamespace, integer.name)
				b.Spec.Config = testBrokerConfigRef(testNamespace, integer.name)
				cfg, err := r.getBrokerConfig(ctx, b)
				if err != nil {
					t.Fatalf("getBrokerConfig(%s): %v", integer.name, err)
				}
				if cfg.Stream == nil || cfg.Stream.MaxMsgs != integer.want {
					t.Fatalf("getBrokerConfig(%s) stream = %+v, want maxMsgs %d", integer.name, cfg.Stream, integer.want)
				}
				if len(cfg.Stream.Sources) != 1 || cfg.Stream.Sources[0].OptStartSeq != uint64(integer.want) {
					t.Errorf("getBrokerConfig(%s) sources = %+v, want optStartSeq %d", integer.name, cfg.Stream.Sources, integer.want)
				}
			}
			b := testBroker(testNamespace, "good")
			b.Spec.Config = testBrokerConfigRef(testNamespace, "good")
			cfg, err := r.getBrokerConfig(ctx, b)
			if err != nil {
				t.Fatalf("valid config failed after reconciling invalid configs: %v", err)
			}
			if cfg.Stream == nil || cfg.Stream.Replicas != 3 {
				t.Fatalf("valid Stream = %+v, want replicas 3", cfg.Stream)
			}
			cfg.Stream.Replicas = 5
			cfg, err = r.getBrokerConfig(ctx, b)
			if err != nil || cfg.Stream.Replicas != 3 {
				t.Fatalf("cached config was modified: cfg=%+v, err=%v", cfg, err)
			}
			fixed, _, err := shared.GetIndexer().GetByKey(testNamespace + "/bad-env")
			if err != nil {
				t.Fatal(err)
			}
			updated := fixed.(*unstructured.Unstructured).DeepCopy()
			updated.Object["spec"] = map[string]interface{}{"filter": map[string]interface{}{"env": []interface{}{map[string]interface{}{"name": "BATCH", "value": "50"}}}}
			if err := shared.GetIndexer().Update(updated); err != nil {
				t.Fatal(err)
			}
			b.Spec.Config = testBrokerConfigRef(testNamespace, "bad-env")
			cfg, err = r.getBrokerConfig(ctx, b)
			if err != nil || cfg.Filter == nil || cfg.Filter.Env[0].Value != "50" {
				t.Fatalf("corrected config did not recover: cfg=%+v, err=%v", cfg, err)
			}
		})
	}
}

func TestBrokerConfigEventsEnqueueTrackingBrokers(t *testing.T) {
	var enqueued []types.NamespacedName
	tr := tracker.New(func(key types.NamespacedName) { enqueued = append(enqueued, key) }, time.Hour)
	for _, ref := range []struct{ namespace, broker, config string }{
		{testNamespace, "broker-a", "broker-config"},
		{testNamespace, "broker-b", "broker-config"},
		{testNamespace, "broker-c", "other-config"},
		{"other-ns", "broker-d", "broker-config"},
	} {
		err := tr.TrackReference(tracker.Reference{
			APIVersion: brokerConfigGVK.GroupVersion().String(),
			Kind:       brokerConfigGVK.Kind,
			Namespace:  ref.namespace,
			Name:       ref.config,
		}, testBroker(ref.namespace, ref.broker))
		if err != nil {
			t.Fatal(err)
		}
	}

	handler := enqueueBrokersOfBrokerConfig(tr)
	shared := []types.NamespacedName{
		{Namespace: testNamespace, Name: "broker-a"},
		{Namespace: testNamespace, Name: "broker-b"},
	}
	raw := &unstructured.Unstructured{}
	raw.SetNamespace(testNamespace)
	raw.SetName("broker-config")
	for _, tt := range []struct {
		name string
		obj  interface{}
		want []types.NamespacedName
	}{
		{name: "referenced by two brokers", obj: testBrokerConfig(testNamespace, "broker-config", 1), want: shared},
		{name: "unstructured event", obj: raw, want: shared},
		{name: "unstructured delete tombstone", obj: cache.DeletedFinalStateUnknown{Obj: raw}, want: shared},
		{
			name: "delete tombstone",
			obj:  cache.DeletedFinalStateUnknown{Obj: testBrokerConfig(testNamespace, "broker-config", 1)},
			want: shared,
		},
		{
			name: "same name in another namespace",
			obj:  testBrokerConfig("other-ns", "broker-config", 1),
			want: []types.NamespacedName{{Namespace: "other-ns", Name: "broker-d"}},
		},
		{
			name: "different config",
			obj:  testBrokerConfig(testNamespace, "other-config", 1),
			want: []types.NamespacedName{{Namespace: testNamespace, Name: "broker-c"}},
		},
		{name: "unreferenced config", obj: testBrokerConfig(testNamespace, "unreferenced", 1)},
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

// A broker whose config doesn't exist yet stays tracked, so that creating the
// NatsJetStreamBrokerConfig reconciles it again.
func TestBrokerWithMissingConfigIsEnqueuedOnConfigCreation(t *testing.T) {
	var enqueued []types.NamespacedName
	tr := tracker.New(func(key types.NamespacedName) { enqueued = append(enqueued, key) }, time.Hour)
	r := newBrokerConfigReconciler()
	r.tracker = tr
	b := testBroker(testNamespace, testBrokerName)
	b.Spec.Config = testBrokerConfigRef(testNamespace, "broker-config")
	if _, err := r.getBrokerConfig(testContext(), b); err == nil {
		t.Fatal("getBrokerConfig() succeeded, want error")
	}

	enqueued = nil // Discard the tracker's initial catch-up notifications.
	enqueueBrokersOfBrokerConfig(tr)(testBrokerConfig(testNamespace, "broker-config", 1))
	want := []types.NamespacedName{{Namespace: b.Namespace, Name: b.Name}}
	if diff := cmp.Diff(want, enqueued); diff != "" {
		t.Errorf("enqueued brokers mismatch (-want +got):\n%s", diff)
	}
}

func TestDefaultBrokerConfigLifecycle(t *testing.T) {
	for _, namespace := range []string{testNamespace, "orders"} {
		t.Run(namespace, func(t *testing.T) {
			ctx := testBrokerDefaultsContext(t)
			b := testBroker(namespace, testBrokerName)
			b.Annotations = map[string]string{eventingv1.BrokerClassAnnotationKey: constants.BrokerClassName}
			b.SetDefaults(ctx)

			var enqueued []types.NamespacedName
			tr := tracker.New(func(key types.NamespacedName) { enqueued = append(enqueued, key) }, time.Hour)
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			r := newBrokerConfigReconciler()
			r.tracker = tr
			r.brokerConfigLister = cache.NewGenericLister(indexer, brokerConfigGVR.GroupResource())
			assertMissing := func(t *testing.T) {
				t.Helper()
				if err := r.ReconcileKind(ctx, b); err == nil {
					t.Fatal("ReconcileKind() succeeded with a missing default CR")
				}
				if cond := b.Status.GetCondition(eventingv1.BrokerConditionIngress); cond == nil || cond.Reason != "ConfigLoadFailed" {
					t.Fatalf("ingress condition = %+v, want ConfigLoadFailed", cond)
				}
			}
			assertMissing(t)
			handler := enqueueBrokersOfBrokerConfig(tr)
			for _, step := range []struct {
				name     string
				replicas int
				store    func(cache.Indexer, interface{}) error
				deleted  bool
			}{
				{name: "create", replicas: 3, store: cache.Indexer.Add},
				{name: "update", replicas: 5, store: cache.Indexer.Update},
				{name: "delete", store: cache.Indexer.Delete, deleted: true},
				{name: "recreate", replicas: 1, store: cache.Indexer.Add},
			} {
				t.Run(step.name, func(t *testing.T) {
					enqueued = nil
					raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(
						testBrokerConfig(b.Spec.Config.Namespace, b.Spec.Config.Name, step.replicas))
					if err != nil {
						t.Fatal(err)
					}
					obj := &unstructured.Unstructured{Object: raw}
					if err := step.store(indexer, obj); err != nil {
						t.Fatal(err)
					}
					if step.deleted {
						handler(cache.DeletedFinalStateUnknown{Obj: obj})
					} else {
						handler(obj)
					}
					want := []types.NamespacedName{{Namespace: b.Namespace, Name: b.Name}}
					if diff := cmp.Diff(want, enqueued); diff != "" {
						t.Errorf("enqueued brokers mismatch (-want +got):\n%s", diff)
					}
					if step.deleted {
						assertMissing(t)
						return
					}
					got, err := r.getBrokerConfig(ctx, b)
					if err != nil {
						t.Fatal(err)
					}
					if got.Stream == nil || got.Stream.Replicas != step.replicas {
						t.Errorf("stream = %+v, want replicas %d", got.Stream, step.replicas)
					}
				})
			}
		})
	}
}
