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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"

	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/controller"
	reconcilertesting "knative.dev/pkg/reconciler/testing"

	eventingconfig "knative.dev/eventing/pkg/apis/config"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"

	eventingv1alpha1 "knative.dev/eventing-natss/pkg/apis/eventing/v1alpha1"
	messagingv1alpha1 "knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	brokerconfig "knative.dev/eventing-natss/pkg/broker/config"
	"knative.dev/eventing-natss/pkg/broker/constants"
)

// legacyDefaultsConfigMapName is the deprecated defaults ConfigMap, which stays
// supported until it is removed.
const legacyDefaultsConfigMapName = brokerconfig.ConfigMapName //nolint:staticcheck

// legacyDefaultsConfigMap returns the legacy defaults ConfigMap for ns holding config.
func legacyDefaultsConfigMap(ns, config string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: legacyDefaultsConfigMapName, Namespace: ns},
		Data:       map[string]string{brokerconfig.ConfigKey: config},
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

	defaultsCM := legacyDefaultsConfigMap(testNamespace, `{"clusterDefault":{"stream":{"replicas":5}}}`)

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
			r.kubeClientSet = kubefake.NewSimpleClientset(legacyDefaultsConfigMap(testNamespace, `{"clusterDefault":{"stream":{"replicas":5}}}`))
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
			if tt.annotation == "" {
				select {
				case event := <-recorder.Events:
					if !strings.Contains(event, "Warning "+ReasonLegacyDefaultsConfigDeprecated) {
						t.Errorf("event = %q, want defaults deprecation warning", event)
					}
				default:
					t.Error("missing defaults deprecation event")
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
			b.Annotations = map[string]string{eventingv1.BrokerClassAnnotationKey: constants.BrokerClassName}
			if tt.explicitRef {
				b.Spec.Config = &duckv1.KReference{APIVersion: "v1", Kind: "ConfigMap", Name: tt.defaultName}
			}
			b.SetDefaults(ctx)
			if b.Spec.Config == nil || b.Spec.Config.Kind != "ConfigMap" || b.Spec.Config.Name != tt.defaultName {
				t.Fatalf("webhook did not default spec.config: %+v", b.Spec.Config)
			}
			r := newBrokerConfigReconciler()
			if tt.defaults != "" {
				r.kubeClientSet = kubefake.NewSimpleClientset(legacyDefaultsConfigMap(testNamespace, tt.defaults))
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
				for _, want := range []string{"Warning " + ReasonConfigMapReferenceIgnored, refNamespace + "/" + tt.defaultName, testNamespace + "/" + legacyDefaultsConfigMapName} {
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

func testBrokerDefaultsContext(t *testing.T) context.Context {
	t.Helper()
	defaults, err := eventingconfig.NewDefaultsConfigFromConfigMap(&corev1.ConfigMap{
		Data: map[string]string{eventingconfig.BrokerDefaultsKey: `
clusterDefault:
  brokerClass: MTChannelBasedBroker
  apiVersion: v1
  kind: ConfigMap
  name: config-br-default-channel
  namespace: knative-eventing
  brokerClasses:
    NatsJetStreamBroker:
      apiVersion: eventing.knative.dev/v1alpha1
      kind: NatsJetStreamBrokerConfig
      name: natsjetstream-default
      namespace: knative-eventing
namespaceDefaults:
  orders:
    brokerClasses:
      NatsJetStreamBroker:
        apiVersion: eventing.knative.dev/v1alpha1
        kind: NatsJetStreamBrokerConfig
        name: natsjetstream-default
        namespace: orders
`},
	})
	if err != nil {
		t.Fatal(err)
	}
	return eventingconfig.ToContext(testContext(), &eventingconfig.Config{Defaults: defaults})
}

func TestGetBrokerConfigFromDefaultCR(t *testing.T) {
	for _, tt := range []struct {
		name         string
		namespace    string
		brokerClass  string
		explicitRef  *duckv1.KReference
		wantRef      *duckv1.KReference
		wantReplicas int
	}{
		{
			name: "cluster defaults", namespace: testNamespace, brokerClass: constants.BrokerClassName,
			wantRef: testBrokerConfigRef("knative-eventing", "natsjetstream-default"), wantReplicas: 3,
		},
		{
			name: "namespace defaults override cluster defaults", namespace: "orders", brokerClass: constants.BrokerClassName,
			wantRef: testBrokerConfigRef("orders", "natsjetstream-default"), wantReplicas: 5,
		},
		{
			name: "explicit reference overrides defaults", namespace: "orders", brokerClass: constants.BrokerClassName,
			explicitRef: testBrokerConfigRef("", "custom-config"),
			wantRef:     testBrokerConfigRef("orders", "custom-config"), wantReplicas: 7,
		},
		{
			name: "other broker class keeps its defaults", namespace: "orders", brokerClass: "MTChannelBasedBroker",
			wantRef: &duckv1.KReference{APIVersion: "v1", Kind: "ConfigMap", Name: "config-br-default-channel", Namespace: "knative-eventing"},
		},
		{
			name: "default broker class is preserved", namespace: "orders",
			wantRef: &duckv1.KReference{APIVersion: "v1", Kind: "ConfigMap", Name: "config-br-default-channel", Namespace: "knative-eventing"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testBrokerDefaultsContext(t)
			b := testBroker(tt.namespace, testBrokerName)
			if tt.brokerClass != "" {
				b.Annotations = map[string]string{eventingv1.BrokerClassAnnotationKey: tt.brokerClass}
			}
			b.Spec.Config = tt.explicitRef
			b.SetDefaults(ctx)
			if diff := cmp.Diff(tt.wantRef, b.Spec.Config); diff != "" {
				t.Fatalf("spec.config mismatch (-want +got):\n%s", diff)
			}
			wantClass := tt.brokerClass
			if wantClass == "" {
				wantClass = "MTChannelBasedBroker"
			}
			if got := b.Annotations[eventingv1.BrokerClassAnnotationKey]; got != wantClass {
				t.Fatalf("broker class = %q, want %q", got, wantClass)
			}
			if wantClass != constants.BrokerClassName {
				return
			}
			r := newBrokerConfigReconciler(
				testBrokerConfig("knative-eventing", "natsjetstream-default", 3),
				testBrokerConfig("orders", "natsjetstream-default", 5),
				testBrokerConfig("orders", "custom-config", 7),
			)
			r.kubeClientSet = kubefake.NewSimpleClientset(legacyDefaultsConfigMap(tt.namespace, `{"clusterDefault":{"stream":{"replicas":9},"filter":{"replicas":11}}}`))
			recorder := record.NewFakeRecorder(10)
			cfg, err := r.getBrokerConfig(controller.WithEventRecorder(ctx, recorder), b)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Stream == nil || cfg.Stream.Replicas != tt.wantReplicas || cfg.Filter != nil {
				t.Errorf("config = %+v, want CR with stream replicas %d and no filter defaults", cfg, tt.wantReplicas)
			}
			select {
			case event := <-recorder.Events:
				t.Errorf("unexpected event: %s", event)
			default:
			}

			// Updating defaults must not replace an existing Broker's reference.
			updated := eventingconfig.FromContext(ctx).Defaults.DeepCopy()
			updated.ClusterDefaultConfig.BrokerClasses[constants.BrokerClassName].Name = "replacement"
			updated.NamespaceDefaultsConfig["orders"].BrokerClasses[constants.BrokerClassName].Name = "replacement"
			b.SetDefaults(eventingconfig.ToContext(ctx, &eventingconfig.Config{Defaults: updated}))
			if diff := cmp.Diff(tt.wantRef, b.Spec.Config); diff != "" {
				t.Errorf("existing spec.config changed (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetBrokerConfigLegacyDefaultsWarning(t *testing.T) {
	for _, tt := range []struct {
		name         string
		data         map[string]string
		wantReplicas int
		wantWarning  bool
		wantErr      bool
	}{
		{name: "no ConfigMap", wantReplicas: 1},
		{name: "missing config key", data: map[string]string{}, wantReplicas: 1},
		{name: "empty config", data: map[string]string{brokerconfig.ConfigKey: ""}, wantReplicas: 1},
		{
			name: "cluster defaults", wantReplicas: 5, wantWarning: true,
			data: map[string]string{brokerconfig.ConfigKey: `{"clusterDefault":{"stream":{"replicas":5}}}`},
		},
		{
			name: "namespace defaults", wantReplicas: 3, wantWarning: true,
			data: map[string]string{brokerconfig.ConfigKey: `{"clusterDefault":{"stream":{"replicas":5}},"namespaceDefaults":{"test-ns":{"stream":{"replicas":3}}}}`},
		},
		{name: "invalid config", data: map[string]string{brokerconfig.ConfigKey: "invalid: [yaml"}, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newBrokerConfigReconciler()
			if tt.data != nil {
				r.kubeClientSet = kubefake.NewSimpleClientset(&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: legacyDefaultsConfigMapName, Namespace: testNamespace},
					Data:       tt.data,
				})
			}
			recorder := record.NewFakeRecorder(10)
			ctx := controller.WithEventRecorder(testContext(), recorder)
			cfg, err := r.getBrokerConfig(ctx, testBroker(testNamespace, testBrokerName))
			if (err != nil) != tt.wantErr {
				t.Fatalf("getBrokerConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && (cfg.Stream == nil || cfg.Stream.Replicas != tt.wantReplicas) {
				t.Errorf("stream = %+v, want replicas %d", cfg.Stream, tt.wantReplicas)
			}
			if tt.wantWarning {
				select {
				case event := <-recorder.Events:
					for _, want := range []string{"Warning " + ReasonLegacyDefaultsConfigDeprecated, testNamespace + "/" + legacyDefaultsConfigMapName, "NatsJetStreamBrokerConfig", "config-br-defaults"} {
						if !strings.Contains(event, want) {
							t.Errorf("event = %q, want it to mention %q", event, want)
						}
					}
				default:
					t.Error("missing defaults deprecation event")
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
