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
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"

	"go.uber.org/zap"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	reconcilertesting "knative.dev/pkg/reconciler/testing"

	eventingconfig "knative.dev/eventing/pkg/apis/config"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	eventinglisters "knative.dev/eventing/pkg/client/listers/eventing/v1"

	eventingv1alpha1 "knative.dev/eventing-natss/pkg/apis/eventing/v1alpha1"
	messagingv1alpha1 "knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	brokerconfig "knative.dev/eventing-natss/pkg/broker/config"
	"knative.dev/eventing-natss/pkg/broker/contract"
	"knative.dev/eventing-natss/pkg/broker/controller/resources"
	brokerutils "knative.dev/eventing-natss/pkg/broker/utils"
	natsTesting "knative.dev/eventing-natss/pkg/channel/jetstream/dispatcher/testing"
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

func testContext() context.Context {
	ctx := logging.WithLogger(context.Background(), zap.NewNop().Sugar())
	return controller.WithEventRecorder(ctx, record.NewFakeRecorder(100))
}

func newDeploymentLister(objs ...*appsv1.Deployment) appsv1listers.DeploymentLister {
	idx := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	for _, o := range objs {
		_ = idx.Add(o)
	}
	return appsv1listers.NewDeploymentLister(idx)
}

func newServiceLister(objs ...*corev1.Service) corev1listers.ServiceLister {
	idx := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	for _, o := range objs {
		_ = idx.Add(o)
	}
	return corev1listers.NewServiceLister(idx)
}

func deploymentWithReady(ns, name string, ready int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: ready},
	}
}

func TestReconcileDataplaneRBAC(t *testing.T) {
	kube := kubefake.NewSimpleClientset()
	r := &Reconciler{kubeClientSet: kube, filterServiceAccount: "dp-sa"}
	ctx := testContext()
	b := testBroker(testNamespace, testBrokerName)

	if err := r.reconcileDataplaneRBAC(ctx, b); err != nil {
		t.Fatalf("reconcileDataplaneRBAC() error: %v", err)
	}
	if _, err := kube.CoreV1().ServiceAccounts(testNamespace).Get(ctx, "dp-sa", metav1.GetOptions{}); err != nil {
		t.Errorf("service account not created: %v", err)
	}
	crbName := DataplaneClusterRoleName + "-" + testNamespace
	if _, err := kube.RbacV1().ClusterRoleBindings().Get(ctx, crbName, metav1.GetOptions{}); err != nil {
		t.Errorf("cluster role binding not created: %v", err)
	}
	// Running again is a no-op (resources already exist).
	if err := r.reconcileDataplaneRBAC(ctx, b); err != nil {
		t.Errorf("second reconcileDataplaneRBAC() error: %v", err)
	}
}

func testBrokerConfig(ns, name string, streamReplicas int) *eventingv1alpha1.NatsJetStreamBrokerConfig {
	return &eventingv1alpha1.NatsJetStreamBrokerConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: eventingv1alpha1.NatsJetStreamBrokerConfigSpec{
			Stream: &messagingv1alpha1.StreamConfig{Replicas: streamReplicas},
		},
	}
}

func testBrokerConfigRef(ns, name string) *duckv1.KReference {
	return &duckv1.KReference{
		APIVersion: brokerConfigGVK.GroupVersion().String(),
		Kind:       brokerConfigGVK.Kind,
		Namespace:  ns,
		Name:       name,
	}
}

func newBrokerConfigLister(objs ...*eventingv1alpha1.NatsJetStreamBrokerConfig) cache.GenericLister {
	idx := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	for _, o := range objs {
		obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(o)
		if err != nil {
			panic(err)
		}
		_ = idx.Add(&unstructured.Unstructured{Object: obj})
	}
	return cache.NewGenericLister(idx, brokerConfigGVR.GroupResource())
}

// newBrokerConfigReconciler returns a Reconciler that resolves spec.config
// references against the given NatsJetStreamBrokerConfigs.
func newBrokerConfigReconciler(cfgs ...*eventingv1alpha1.NatsJetStreamBrokerConfig) *Reconciler {
	return &Reconciler{
		kubeClientSet:      kubefake.NewSimpleClientset(),
		brokerConfigLister: newBrokerConfigLister(cfgs...),
		tracker:            &reconcilertesting.FakeTracker{},
	}
}

func TestGetBrokerConfig(t *testing.T) {
	ctx := testContext()

	defaultsCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: brokerconfig.ConfigMapName, Namespace: testNamespace},
		Data:       map[string]string{brokerconfig.ConfigKey: `{"clusterDefault":{"stream":{"replicas":5}}}`},
	}

	for _, tt := range []struct {
		name         string
		refNamespace string
		defaults     *corev1.ConfigMap
	}{
		{name: "from NatsJetStreamBrokerConfig referenced by spec.config", refNamespace: testNamespace},
		{name: "spec.config namespace defaults to the broker's namespace", refNamespace: ""},
		{name: "spec.config takes precedence over the namespace default ConfigMap", refNamespace: testNamespace, defaults: defaultsCM},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newBrokerConfigReconciler(testBrokerConfig(testNamespace, "my-broker-cfg", 3))
			if tt.defaults != nil {
				r.kubeClientSet = kubefake.NewSimpleClientset(tt.defaults)
			}
			b := testBroker(testNamespace, testBrokerName)
			b.Spec.Config = testBrokerConfigRef(tt.refNamespace, "my-broker-cfg")
			cfg, err := r.getBrokerConfig(ctx, b)
			if err != nil {
				t.Fatalf("getBrokerConfig() error: %v", err)
			}
			if cfg.Stream == nil || cfg.Stream.Replicas != 3 {
				t.Errorf("Stream.Replicas = %+v, want 3", cfg.Stream)
			}
		})
	}

	for _, tt := range []struct {
		name      string
		configure func(b *eventingv1.Broker)
		wantErr   []string
	}{
		{
			name: "spec.config names a missing NatsJetStreamBrokerConfig",
			configure: func(b *eventingv1.Broker) {
				b.Spec.Config = testBrokerConfigRef(testNamespace, "does-not-exist")
			},
			wantErr: []string{"does-not-exist"},
		},
		{
			name: "unsupported spec.config kind",
			configure: func(b *eventingv1.Broker) {
				b.Spec.Config = &duckv1.KReference{APIVersion: "v1", Kind: "Secret", Namespace: testNamespace, Name: "my-broker-cfg"}
			},
			wantErr: []string{"NatsJetStreamBrokerConfig"},
		},
		{
			name: "unsupported spec.config API version",
			configure: func(b *eventingv1.Broker) {
				b.Spec.Config = testBrokerConfigRef(testNamespace, "my-broker-cfg")
				b.Spec.Config.APIVersion = "eventing.knative.dev/v1"
			},
			wantErr: []string{"NatsJetStreamBrokerConfig"},
		},
		{
			name: "invalid legacy annotation",
			configure: func(b *eventingv1.Broker) {
				b.Annotations = map[string]string{brokerconfig.LegacyConfigAnnotation: `{"stream":`}
			},
			wantErr: []string{brokerconfig.LegacyConfigAnnotation, "failed to parse"},
		},
	} {
		t.Run(tt.name+" is an error", func(t *testing.T) {
			r := newBrokerConfigReconciler()
			r.kubeClientSet = kubefake.NewSimpleClientset(defaultsCM)
			b := testBroker(testNamespace, testBrokerName)
			tt.configure(b)
			_, err := r.getBrokerConfig(ctx, b)
			if err == nil {
				t.Fatal("getBrokerConfig() expected an error, got nil")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("getBrokerConfig() error = %q, want it to mention %q", err, want)
				}
			}
		})
	}

	t.Run("hardcoded defaults when no configmap", func(t *testing.T) {
		r := &Reconciler{kubeClientSet: kubefake.NewSimpleClientset()}
		b := testBroker(testNamespace, testBrokerName)
		cfg, err := r.getBrokerConfig(ctx, b)
		if err != nil {
			t.Fatalf("getBrokerConfig() error: %v", err)
		}
		if cfg.Stream.Replicas != 1 {
			t.Errorf("Stream.Replicas = %d, want 1 (default)", cfg.Stream.Replicas)
		}
	})
}

func TestGetBrokerConfigLegacyAnnotation(t *testing.T) {
	for _, tt := range []struct {
		name         string
		annotation   string
		ref          *duckv1.KReference
		wantReplicas int
		wantErr      string
	}{
		{name: "empty annotation uses defaults", wantReplicas: 5},
		{name: "annotation takes precedence over defaults", annotation: `{"stream":{"replicas":3}}`, wantReplicas: 3},
		{
			name: "annotation is honored with webhook ConfigMap reference", annotation: `{"stream":{"replicas":3}}`,
			ref: &duckv1.KReference{APIVersion: "v1", Kind: "ConfigMap", Name: "config-br-default-channel"}, wantReplicas: 3,
		},
		{
			name: "CR takes precedence over annotation", annotation: `{"stream":{"replicas":3}}`,
			ref: testBrokerConfigRef(testNamespace, "my-broker-cfg"), wantReplicas: 7,
		},
		{
			name: "CR takes precedence over invalid annotation", annotation: `{"stream":`,
			ref: testBrokerConfigRef(testNamespace, "my-broker-cfg"), wantReplicas: 7,
		},
		{
			name: "missing CR does not fall back to annotation", annotation: `{"stream":{"replicas":3}}`,
			ref: testBrokerConfigRef(testNamespace, "missing"), wantErr: "missing",
		},
		{name: "invalid annotation does not fall back to defaults", annotation: `{"stream":`, wantErr: "failed to parse"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newBrokerConfigReconciler(testBrokerConfig(testNamespace, "my-broker-cfg", 7))
			r.kubeClientSet = kubefake.NewSimpleClientset(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: brokerconfig.ConfigMapName, Namespace: testNamespace},
				Data:       map[string]string{brokerconfig.ConfigKey: `{"clusterDefault":{"stream":{"replicas":5}}}`},
			})
			b := testBroker(testNamespace, testBrokerName)
			b.Annotations = map[string]string{brokerconfig.LegacyConfigAnnotation: tt.annotation}
			b.Spec.Config = tt.ref
			recorder := record.NewFakeRecorder(10)
			ctx := controller.WithEventRecorder(testContext(), recorder)
			cfg, err := r.getBrokerConfig(ctx, b)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("getBrokerConfig() error = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("getBrokerConfig() error: %v", err)
			} else if cfg.Stream == nil || cfg.Stream.Replicas != tt.wantReplicas {
				t.Fatalf("stream = %+v, want replicas %d", cfg.Stream, tt.wantReplicas)
			}
			if tt.annotation != "" {
				select {
				case event := <-recorder.Events:
					if !strings.Contains(event, "Warning "+ReasonLegacyConfigDeprecated) || !strings.Contains(event, "spec.config") {
						t.Errorf("event = %q, want annotation deprecation warning", event)
					}
				default:
					t.Error("missing annotation deprecation event")
				}
			}
			select {
			case event := <-recorder.Events:
				t.Errorf("unexpected event: %s", event)
			default:
			}
		})
	}
}

func TestGetBrokerConfigAfterWebhookDefaulting(t *testing.T) {
	for _, tt := range []struct {
		name         string
		defaultName  string
		explicitRef  bool
		defaults     string
		wantReplicas int
	}{
		{name: "hardcoded defaults", defaultName: "config-br-default-channel", wantReplicas: 1},
		{
			name:         "cluster defaults",
			defaultName:  "config-br-default-channel",
			defaults:     `{"clusterDefault":{"stream":{"replicas":5}}}`,
			wantReplicas: 5,
		},
		{
			name:         "namespace defaults take precedence",
			defaultName:  "config-br-default-channel",
			defaults:     `{"clusterDefault":{"stream":{"replicas":5}},"namespaceDefaults":{"test-ns":{"stream":{"replicas":3}}}}`,
			wantReplicas: 3,
		},
		{
			name:         "custom webhook default ConfigMap is also ignored",
			defaultName:  "operator-channel-config",
			defaults:     `{"clusterDefault":{"stream":{"replicas":5}}}`,
			wantReplicas: 5,
		},
		{
			name:         "explicit ConfigMap reference is ignored with a warning",
			defaultName:  "user-config",
			explicitRef:  true,
			defaults:     `{"clusterDefault":{"stream":{"replicas":5}}}`,
			wantReplicas: 5,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := eventingconfig.ToContext(testContext(), &eventingconfig.Config{
				Defaults: &eventingconfig.Defaults{
					ClusterDefaultConfig: &eventingconfig.DefaultConfig{
						DefaultBrokerClass: "MTChannelBasedBroker",
						BrokerConfig: &eventingconfig.BrokerConfig{
							KReference: &duckv1.KReference{
								APIVersion: "v1", Kind: "ConfigMap", Namespace: "knative-eventing", Name: tt.defaultName,
							},
						},
					},
				},
			})
			b := testBroker(testNamespace, testBrokerName)
			b.Annotations = map[string]string{eventingv1.BrokerClassAnnotationKey: "NatsJetStreamBroker"}
			if tt.explicitRef {
				b.Spec.Config = &duckv1.KReference{APIVersion: "v1", Kind: "ConfigMap", Name: tt.defaultName}
			}
			b.SetDefaults(ctx)
			if b.Spec.Config == nil || b.Spec.Config.Kind != "ConfigMap" || b.Spec.Config.Name != tt.defaultName {
				t.Fatalf("webhook did not default spec.config: %+v", b.Spec.Config)
			}
			r := newBrokerConfigReconciler()
			if tt.defaults != "" {
				r.kubeClientSet = kubefake.NewSimpleClientset(&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: brokerconfig.ConfigMapName, Namespace: testNamespace},
					Data:       map[string]string{brokerconfig.ConfigKey: tt.defaults},
				})
			}
			recorder := record.NewFakeRecorder(10)
			ctx = controller.WithEventRecorder(ctx, recorder)
			cfg, err := r.getBrokerConfig(ctx, b)
			if err != nil {
				t.Fatalf("getBrokerConfig() error: %v", err)
			}
			if cfg.Stream == nil || cfg.Stream.Replicas != tt.wantReplicas {
				t.Errorf("Stream = %+v, want replicas %d", cfg.Stream, tt.wantReplicas)
			}
			refNamespace := b.Spec.Config.Namespace
			if refNamespace == "" {
				refNamespace = b.Namespace
			}
			select {
			case event := <-recorder.Events:
				for _, want := range []string{"Warning " + ReasonConfigMapReferenceIgnored, refNamespace + "/" + tt.defaultName, testNamespace + "/" + brokerconfig.ConfigMapName} {
					if !strings.Contains(event, want) {
						t.Errorf("event = %q, want it to mention %q", event, want)
					}
				}
			default:
				t.Error("missing ignored ConfigMap reference event")
			}
		})
	}
}

func TestReconcileStream(t *testing.T) {
	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	r := &Reconciler{js: js}
	ctx := testContext()
	b := testBroker(testNamespace, testBrokerName)
	streamName := brokerutils.BrokerStreamName(b)
	publish := brokerutils.BrokerPublishSubjectName(b.Namespace, b.Name)

	if err := r.reconcileStream(ctx, b, streamName, publish, brokerconfig.DefaultBrokerConfig()); err != nil {
		t.Fatalf("reconcileStream() error: %v", err)
	}
	if _, err := js.StreamInfo(streamName); err != nil {
		t.Fatalf("stream not created: %v", err)
	}
	// Idempotent when the stream already exists.
	if err := r.reconcileStream(ctx, b, streamName, publish, brokerconfig.DefaultBrokerConfig()); err != nil {
		t.Errorf("second reconcileStream() error: %v", err)
	}
}

func TestPropagateIngressAvailability(t *testing.T) {
	const ingressNS, ingressName = "knative-eventing", "nats-broker-ingress"
	tests := []struct {
		name      string
		dep       *appsv1.Deployment
		wantReady bool
	}{
		{name: "ready", dep: deploymentWithReady(ingressNS, ingressName, 1), wantReady: true},
		{name: "no ready replicas", dep: deploymentWithReady(ingressNS, ingressName, 0), wantReady: false},
		{name: "missing", dep: nil, wantReady: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var deps []*appsv1.Deployment
			if tc.dep != nil {
				deps = append(deps, tc.dep)
			}
			r := &Reconciler{deploymentLister: newDeploymentLister(deps...), ingressServiceName: ingressName, ingressNamespace: ingressNS}
			b := testBroker(testNamespace, testBrokerName)
			if err := r.propagateIngressAvailability(testContext(), b); err != nil {
				t.Fatalf("propagateIngressAvailability() error: %v", err)
			}
			cond := b.Status.GetCondition(eventingv1.BrokerConditionIngress)
			if tc.wantReady != (cond != nil && cond.IsTrue()) {
				t.Errorf("ingress condition = %v, wantReady %v", cond, tc.wantReady)
			}
		})
	}
}

func TestPropagateFilterAvailability(t *testing.T) {
	filterName := resources.FilterName(testBrokerName)
	tests := []struct {
		name      string
		dep       *appsv1.Deployment
		wantReady bool
	}{
		{name: "ready", dep: deploymentWithReady(testNamespace, filterName, 1), wantReady: true},
		{name: "no ready replicas", dep: deploymentWithReady(testNamespace, filterName, 0), wantReady: false},
		{name: "missing", dep: nil, wantReady: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var deps []*appsv1.Deployment
			if tc.dep != nil {
				deps = append(deps, tc.dep)
			}
			r := &Reconciler{deploymentLister: newDeploymentLister(deps...)}
			b := testBroker(testNamespace, testBrokerName)
			if err := r.propagateFilterAvailability(testContext(), b, nil); err != nil {
				t.Fatalf("propagateFilterAvailability() error: %v", err)
			}
			cond := b.Status.GetCondition(eventingv1.BrokerConditionFilter)
			if tc.wantReady != (cond != nil && cond.IsTrue()) {
				t.Errorf("filter condition = %v, wantReady %v", cond, tc.wantReady)
			}
		})
	}
}

func TestReconcileFilterServiceCreate(t *testing.T) {
	kube := kubefake.NewSimpleClientset()
	r := &Reconciler{kubeClientSet: kube, serviceLister: newServiceLister()}
	b := testBroker(testNamespace, testBrokerName)

	svc, err := r.reconcileFilterService(testContext(), b)
	if err != nil {
		t.Fatalf("reconcileFilterService() error: %v", err)
	}
	if svc == nil {
		t.Fatal("reconcileFilterService() returned nil service")
	}
	if _, gerr := kube.CoreV1().Services(testNamespace).Get(context.Background(), resources.FilterName(testBrokerName), metav1.GetOptions{}); gerr != nil {
		t.Errorf("filter service not created: %v", gerr)
	}
}

func TestReconcileFilterDeploymentCreate(t *testing.T) {
	kube := kubefake.NewSimpleClientset()
	r := &Reconciler{
		kubeClientSet:        kube,
		deploymentLister:     newDeploymentLister(),
		filterImage:          "filter:latest",
		filterServiceAccount: "dp-sa",
		natsURL:              "nats://localhost:4222",
	}
	b := testBroker(testNamespace, testBrokerName)

	if err := r.reconcileFilterDeployment(testContext(), b, "TEST_STREAM", brokerconfig.DefaultBrokerConfig()); err != nil {
		t.Fatalf("reconcileFilterDeployment() error: %v", err)
	}
	if _, gerr := kube.AppsV1().Deployments(testNamespace).Get(context.Background(), resources.FilterName(testBrokerName), metav1.GetOptions{}); gerr != nil {
		t.Errorf("filter deployment not created: %v", gerr)
	}
}

func TestReconcileFilterServiceUpdate(t *testing.T) {
	name := resources.FilterName(testBrokerName)
	// Existing service with an empty spec differs from the expected spec, so the
	// update branch runs.
	existing := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: name}}
	kube := kubefake.NewSimpleClientset(existing)
	r := &Reconciler{kubeClientSet: kube, serviceLister: newServiceLister(existing)}

	if _, err := r.reconcileFilterService(testContext(), testBroker(testNamespace, testBrokerName)); err != nil {
		t.Fatalf("reconcileFilterService() error: %v", err)
	}
	got, err := kube.CoreV1().Services(testNamespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get service: %v", err)
	}
	if len(got.Spec.Ports) == 0 {
		t.Error("service spec was not updated to the expected spec")
	}
}

func TestReconcileFilterDeploymentUpdate(t *testing.T) {
	name := resources.FilterName(testBrokerName)
	existing := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: name}}
	kube := kubefake.NewSimpleClientset(existing)
	r := &Reconciler{
		kubeClientSet:        kube,
		deploymentLister:     newDeploymentLister(existing),
		filterImage:          "filter:latest",
		filterServiceAccount: "dp-sa",
		natsURL:              "nats://localhost:4222",
	}

	if err := r.reconcileFilterDeployment(testContext(), testBroker(testNamespace, testBrokerName), "TEST_STREAM", brokerconfig.DefaultBrokerConfig()); err != nil {
		t.Fatalf("reconcileFilterDeployment() error: %v", err)
	}
	got, err := kube.AppsV1().Deployments(testNamespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if len(got.Spec.Template.Spec.Containers) == 0 {
		t.Error("deployment spec was not updated to the expected spec")
	}
}

func TestEnqueueBrokerOfTrigger(t *testing.T) {
	var got []types.NamespacedName
	h := enqueueBrokerOfTrigger(func(k types.NamespacedName) { got = append(got, k) })

	h(testTrigger(testNamespace, "t1", "broker-a"))                                                // enqueues broker-a
	h(&corev1.Pod{})                                                                               // not a trigger → ignored
	h(cache.DeletedFinalStateUnknown{Key: "k", Obj: testTrigger(testNamespace, "t2", "broker-b")}) // tombstone → broker-b
	h(testTrigger(testNamespace, "t3", ""))                                                        // empty broker ref → ignored

	want := []types.NamespacedName{
		{Namespace: testNamespace, Name: "broker-a"},
		{Namespace: testNamespace, Name: "broker-b"},
	}
	if len(got) != len(want) {
		t.Fatalf("enqueued %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("enqueued[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestFinalizeKind(t *testing.T) {
	t.Setenv("SYSTEM_NAMESPACE", "knative-eventing")

	s := natsTesting.RunBasicJetstreamServer()
	defer natsTesting.ShutdownJSServerAndRemoveStorage(t, s)
	conn, js := natsTesting.JsClient(t, s)
	defer conn.Close()

	kube := kubefake.NewSimpleClientset()
	cmLister := corev1listers.NewConfigMapLister(cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}))
	r := &Reconciler{js: js, contractManager: contract.NewManager(kube, cmLister)}
	ctx := testContext()
	b := testBroker(testNamespace, testBrokerName)

	streamName := brokerutils.BrokerStreamName(b)
	if _, err := js.AddStream(&nats.StreamConfig{
		Name:     streamName,
		Subjects: []string{brokerutils.BrokerPublishSubjectName(b.Namespace, b.Name) + ".>"},
	}); err != nil {
		t.Fatalf("AddStream() error: %v", err)
	}

	if err := r.FinalizeKind(ctx, b); err != nil {
		t.Fatalf("FinalizeKind() error: %v", err)
	}
	if _, err := js.StreamInfo(streamName); !errors.Is(err, nats.ErrStreamNotFound) {
		t.Errorf("stream not deleted: got err %v", err)
	}
}

func TestReconcileKind(t *testing.T) {
	const ingressNS, ingressName = "knative-eventing", "nats-broker-ingress"

	setup := func(t *testing.T, triggers ...*eventingv1.Trigger) (*Reconciler, *kubefake.Clientset) {
		t.Setenv("SYSTEM_NAMESPACE", ingressNS)
		s := natsTesting.RunBasicJetstreamServer()
		t.Cleanup(func() { natsTesting.ShutdownJSServerAndRemoveStorage(t, s) })
		conn, js := natsTesting.JsClient(t, s)
		t.Cleanup(func() { conn.Close() })

		kube := kubefake.NewSimpleClientset()
		cmLister := corev1listers.NewConfigMapLister(cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}))
		triggerLister := newFakeTriggerLister()
		for _, tr := range triggers {
			triggerLister.add(tr)
		}
		r := &Reconciler{
			kubeClientSet:        kube,
			js:                   js,
			triggerLister:        triggerLister,
			deploymentLister:     newDeploymentLister(deploymentWithReady(ingressNS, ingressName, 1)),
			serviceLister:        newServiceLister(),
			contractManager:      contract.NewManager(kube, cmLister),
			filterImage:          "filter:latest",
			filterServiceAccount: "dp-sa",
			natsURL:              "nats://localhost:4222",
			ingressServiceName:   ingressName,
			ingressNamespace:     ingressNS,
		}
		return r, kube
	}

	t.Run("no triggers: no filter created, filter condition NoTriggers", func(t *testing.T) {
		r, kube := setup(t)
		b := testBroker(testNamespace, testBrokerName)

		if err := r.ReconcileKind(testContext(), b); err != nil {
			t.Fatalf("ReconcileKind() error: %v", err)
		}
		if _, err := kube.AppsV1().Deployments(testNamespace).Get(context.Background(), resources.FilterName(testBrokerName), metav1.GetOptions{}); !apierrs.IsNotFound(err) {
			t.Errorf("filter deployment should not exist, got err %v", err)
		}
		cond := b.Status.GetCondition(eventingv1.BrokerConditionFilter)
		if cond == nil || cond.Reason != "NoTriggers" {
			t.Errorf("filter condition = %v, want reason NoTriggers", cond)
		}
	})

	t.Run("with a trigger: filter deployment is created", func(t *testing.T) {
		r, kube := setup(t, testTrigger(testNamespace, "t1", testBrokerName))
		b := testBroker(testNamespace, testBrokerName)

		if err := r.ReconcileKind(testContext(), b); err != nil {
			t.Fatalf("ReconcileKind() error: %v", err)
		}
		if _, err := kube.AppsV1().Deployments(testNamespace).Get(context.Background(), resources.FilterName(testBrokerName), metav1.GetOptions{}); err != nil {
			t.Errorf("filter deployment should be created: %v", err)
		}
	})

	t.Run("legacy annotation continues reconciling the stream and filter", func(t *testing.T) {
		r, kube := setup(t, testTrigger(testNamespace, "t1", testBrokerName))
		b := testBroker(testNamespace, testBrokerName)
		b.Spec.Config = &duckv1.KReference{APIVersion: "v1", Kind: "ConfigMap", Namespace: ingressNS, Name: "config-br-default-channel"}
		b.Annotations = map[string]string{brokerconfig.LegacyConfigAnnotation: `{"stream":{"maxMsgs":123},"filter":{"replicas":2}}`}
		if err := r.ReconcileKind(testContext(), b); err != nil {
			t.Fatalf("ReconcileKind() error: %v", err)
		}
		if cond := b.Status.GetCondition(eventingv1.BrokerConditionIngress); cond == nil || !cond.IsTrue() {
			t.Errorf("ingress condition = %+v, want ready", cond)
		}
		deployment, err := kube.AppsV1().Deployments(testNamespace).Get(context.Background(), resources.FilterName(testBrokerName), metav1.GetOptions{})
		if err != nil {
			t.Fatalf("filter deployment should be created: %v", err)
		}
		if *deployment.Spec.Replicas != 2 {
			t.Errorf("filter replicas = %d, want 2", *deployment.Spec.Replicas)
		}
		stream, err := r.js.StreamInfo(brokerutils.BrokerStreamName(b))
		if err != nil {
			t.Fatalf("stream should be created: %v", err)
		}
		if stream.Config.MaxMsgs != 123 {
			t.Errorf("stream maxMsgs = %d, want 123", stream.Config.MaxMsgs)
		}
	})

	t.Run("invalid legacy annotation reports ConfigLoadFailed", func(t *testing.T) {
		r := newBrokerConfigReconciler()
		b := testBroker(testNamespace, testBrokerName)
		b.Annotations = map[string]string{brokerconfig.LegacyConfigAnnotation: `{"stream":`}
		if err := r.ReconcileKind(testContext(), b); err == nil {
			t.Fatal("ReconcileKind() succeeded, want ConfigLoadFailed")
		}
		if cond := b.Status.GetCondition(eventingv1.BrokerConditionIngress); cond == nil || cond.Reason != "ConfigLoadFailed" {
			t.Errorf("ingress condition = %+v, want ConfigLoadFailed", cond)
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
