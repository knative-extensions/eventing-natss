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

package ingress

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	"go.uber.org/zap"

	"k8s.io/client-go/rest"
	"knative.dev/pkg/injection"

	"knative.dev/eventing-natss/pkg/broker/contract"
	"knative.dev/eventing-natss/pkg/common/configloader/fsloader"
)

// fakeGetter is a test double for configMapGetter.
type fakeGetter struct {
	cm  *corev1.ConfigMap
	err error
}

func (f *fakeGetter) Get(_ string) (*corev1.ConfigMap, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.cm == nil {
		return nil, apierrs.NewNotFound(schema.GroupResource{Resource: "configmaps"}, contract.ConfigMapName)
	}
	return f.cm, nil
}

// contractCM builds a ConfigMap whose data key holds the serialized contract.
func contractCM(c *contract.Contract, namespace string) *corev1.ConfigMap {
	raw, _ := json.Marshal(c)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      contract.ConfigMapName,
			Namespace: namespace,
		},
		Data: map[string]string{
			contract.ConfigMapDataKey: string(raw),
		},
	}
}

// nopHandler creates a Handler with a nil JetStream — safe for tests that
// only exercise UpdateContract / GetBrokerCount (neither touches h.js).
func nopHandler() *Handler {
	return NewHandler(HandlerConfig{
		Logger:    zap.NewNop().Sugar(),
		JetStream: nil,
	})
}

// --- filterContractConfigMap ---

func TestFilterContractConfigMap_Nil(t *testing.T) {
	if filterContractConfigMap(nil) {
		t.Error("expected false for nil object")
	}
}

func TestFilterContractConfigMap_WrongType(t *testing.T) {
	if filterContractConfigMap("not a configmap") {
		t.Error("expected false for non-ConfigMap type")
	}
}

func TestFilterContractConfigMap_WrongName(t *testing.T) {
	t.Setenv("SYSTEM_NAMESPACE", "knative-eventing")
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "other-configmap", Namespace: "knative-eventing"},
	}
	if filterContractConfigMap(cm) {
		t.Error("expected false for wrong ConfigMap name")
	}
}

func TestFilterContractConfigMap_WrongNamespace(t *testing.T) {
	t.Setenv("SYSTEM_NAMESPACE", "knative-eventing")
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: contract.ConfigMapName, Namespace: "other-namespace"},
	}
	if filterContractConfigMap(cm) {
		t.Error("expected false for wrong namespace")
	}
}

func TestFilterContractConfigMap_Match(t *testing.T) {
	t.Setenv("SYSTEM_NAMESPACE", "knative-eventing")
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: contract.ConfigMapName, Namespace: "knative-eventing"},
	}
	if !filterContractConfigMap(cm) {
		t.Error("expected true for matching ConfigMap")
	}
}

func TestFilterContractConfigMap_TombstoneMatch(t *testing.T) {
	t.Setenv("SYSTEM_NAMESPACE", "knative-eventing")
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: contract.ConfigMapName, Namespace: "knative-eventing"},
	}
	tombstone := cache.DeletedFinalStateUnknown{Key: "knative-eventing/" + contract.ConfigMapName, Obj: cm}
	if !filterContractConfigMap(tombstone) {
		t.Error("expected true for tombstone wrapping matching ConfigMap")
	}
}

func TestFilterContractConfigMap_TombstoneWrongName(t *testing.T) {
	t.Setenv("SYSTEM_NAMESPACE", "knative-eventing")
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "wrong-name", Namespace: "knative-eventing"},
	}
	tombstone := cache.DeletedFinalStateUnknown{Key: "knative-eventing/wrong-name", Obj: cm}
	if filterContractConfigMap(tombstone) {
		t.Error("expected false for tombstone wrapping wrong-name ConfigMap")
	}
}

// --- loadContractFromInformer ---

func TestLoadContractFromInformer_NotFound(t *testing.T) {
	h := nopHandler()
	getter := &fakeGetter{} // returns NotFound
	loadContractFromInformer(getter, h, zap.NewNop().Sugar())
	if got := h.GetBrokerCount(); got != 0 {
		t.Errorf("broker count = %d, want 0 (NotFound should be a no-op)", got)
	}
}

func TestLoadContractFromInformer_GetterError(t *testing.T) {
	h := nopHandler()
	getter := &fakeGetter{err: fmt.Errorf("internal cache error")}
	loadContractFromInformer(getter, h, zap.NewNop().Sugar())
	if got := h.GetBrokerCount(); got != 0 {
		t.Errorf("broker count = %d, want 0 (error should be a no-op)", got)
	}
}

func TestLoadContractFromInformer_InvalidJSON(t *testing.T) {
	t.Setenv("SYSTEM_NAMESPACE", "knative-eventing")
	h := nopHandler()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: contract.ConfigMapName, Namespace: "knative-eventing"},
		Data:       map[string]string{contract.ConfigMapDataKey: "{invalid json}"},
	}
	getter := &fakeGetter{cm: cm}
	loadContractFromInformer(getter, h, zap.NewNop().Sugar())
	if got := h.GetBrokerCount(); got != 0 {
		t.Errorf("broker count = %d, want 0 (bad JSON should be a no-op)", got)
	}
}

func TestLoadContractFromInformer_ValidContract(t *testing.T) {
	t.Setenv("SYSTEM_NAMESPACE", "knative-eventing")
	h := nopHandler()

	c := &contract.Contract{
		Brokers: map[string]contract.BrokerContract{
			"ns/br-a": {Namespace: "ns", Name: "br-a", Path: "/ns/br-a"},
			"ns/br-b": {Namespace: "ns", Name: "br-b", Path: "/ns/br-b"},
		},
		Generation: 3,
	}
	getter := &fakeGetter{cm: contractCM(c, "knative-eventing")}
	loadContractFromInformer(getter, h, zap.NewNop().Sugar())

	if got := h.GetBrokerCount(); got != 2 {
		t.Errorf("broker count = %d, want 2", got)
	}
}

func TestLoadContractFromInformer_EmptyContract(t *testing.T) {
	t.Setenv("SYSTEM_NAMESPACE", "knative-eventing")
	h := nopHandler()

	// Seed handler with existing brokers first.
	h.UpdateContract(&contract.Contract{
		Brokers: map[string]contract.BrokerContract{
			"ns/old": {Namespace: "ns", Name: "old", Path: "/ns/old"},
		},
	})

	// Load an empty contract — should clear the existing entries.
	getter := &fakeGetter{cm: contractCM(&contract.Contract{Brokers: map[string]contract.BrokerContract{}}, "knative-eventing")}
	loadContractFromInformer(getter, h, zap.NewNop().Sugar())

	if got := h.GetBrokerCount(); got != 0 {
		t.Errorf("broker count = %d, want 0 after loading empty contract", got)
	}
}

// --- loadEnvConfig ---

func TestLoadEnvConfig_Defaults(t *testing.T) {
	env, err := loadEnvConfig()
	if err != nil {
		t.Fatalf("loadEnvConfig() unexpected error: %v", err)
	}
	if env.Port != 8080 {
		t.Errorf("Port = %d, want 8080 (default)", env.Port)
	}
}

func TestLoadEnvConfig_CustomPort(t *testing.T) {
	t.Setenv("PORT", "9090")
	env, err := loadEnvConfig()
	if err != nil {
		t.Fatalf("loadEnvConfig() unexpected error: %v", err)
	}
	if env.Port != 9090 {
		t.Errorf("Port = %d, want 9090", env.Port)
	}
}

func TestLoadEnvConfig_InvalidPort(t *testing.T) {
	t.Setenv("PORT", "not-a-number")
	_, err := loadEnvConfig()
	if err == nil {
		t.Error("loadEnvConfig() expected error for non-integer PORT, got nil")
	}
}

// --- buildNatsConn ---

func TestBuildNatsConn_NoLoaderInContext(t *testing.T) {
	_, err := buildNatsConn(context.Background())
	if err == nil {
		t.Error("buildNatsConn() expected error when no loader in context, got nil")
	}
}

func TestBuildNatsConn_LoaderReturnsError(t *testing.T) {
	ctx := fsloader.WithLoader(context.Background(), func(_ string) (map[string]string, error) {
		return nil, fmt.Errorf("disk read error")
	})
	_, err := buildNatsConn(ctx)
	if err == nil {
		t.Error("buildNatsConn() expected error when loader fails, got nil")
	}
}

func TestBuildNatsConn_MissingNatsConfigKey(t *testing.T) {
	// Loader returns a map without the required "eventing-nats" key.
	ctx := fsloader.WithLoader(context.Background(), func(_ string) (map[string]string, error) {
		return map[string]string{}, nil
	})
	_, err := buildNatsConn(ctx)
	if err == nil {
		t.Error("buildNatsConn() expected error for missing eventing-nats key, got nil")
	}
}

func TestBuildNatsConn_InvalidNatsConfigYAML(t *testing.T) {
	ctx := fsloader.WithLoader(context.Background(), func(_ string) (map[string]string, error) {
		return map[string]string{"eventing-nats": "{"}, nil // invalid YAML
	})
	_, err := buildNatsConn(ctx)
	if err == nil {
		t.Error("buildNatsConn() expected error for invalid YAML, got nil")
	}
}

func TestBuildNatsConn_ValidConfig(t *testing.T) {
	t.Setenv("SYSTEM_NAMESPACE", "knative-eventing")
	s := runBasicJetStreamServer(t)
	defer s.Shutdown()

	// NewNatsConn always creates a k8s secrets client (for credential/TLS resolution).
	// Inject a stub rest.Config so it doesn't fall back to InClusterConfig.
	ctx := injection.WithConfig(context.Background(), &rest.Config{Host: "http://localhost:6443"})
	ctx = fsloader.WithLoader(ctx, func(_ string) (map[string]string, error) {
		return map[string]string{
			"eventing-nats": fmt.Sprintf("url: %s", s.ClientURL()),
		}, nil
	})

	conn, err := buildNatsConn(ctx)
	if err != nil {
		t.Fatalf("buildNatsConn() unexpected error: %v", err)
	}
	defer conn.Close()

	if !conn.IsConnected() {
		t.Error("expected an active NATS connection")
	}
}
