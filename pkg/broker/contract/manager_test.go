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

package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

const testSystemNamespace = "test-system"

// --- fake ConfigMap lister ---

type fakeConfigMapLister struct {
	cms map[string]*corev1.ConfigMap
	err error
}

func newFakeConfigMapLister(cms ...*corev1.ConfigMap) *fakeConfigMapLister {
	f := &fakeConfigMapLister{cms: make(map[string]*corev1.ConfigMap)}
	for _, cm := range cms {
		f.cms[cm.Name] = cm
	}
	return f
}

func (f *fakeConfigMapLister) List(selector labels.Selector) ([]*corev1.ConfigMap, error) {
	result := make([]*corev1.ConfigMap, 0, len(f.cms))
	for _, cm := range f.cms {
		result = append(result, cm)
	}
	return result, nil
}

func (f *fakeConfigMapLister) ConfigMaps(namespace string) corev1listers.ConfigMapNamespaceLister {
	return &fakeConfigMapNamespaceLister{cms: f.cms, err: f.err}
}

type fakeConfigMapNamespaceLister struct {
	cms map[string]*corev1.ConfigMap
	err error
}

func (f *fakeConfigMapNamespaceLister) List(selector labels.Selector) ([]*corev1.ConfigMap, error) {
	result := make([]*corev1.ConfigMap, 0, len(f.cms))
	for _, cm := range f.cms {
		result = append(result, cm)
	}
	return result, nil
}

func (f *fakeConfigMapNamespaceLister) Get(name string) (*corev1.ConfigMap, error) {
	if f.err != nil {
		return nil, f.err
	}
	if cm, ok := f.cms[name]; ok {
		return cm, nil
	}
	return nil, apierrs.NewNotFound(schema.GroupResource{Resource: "configmaps"}, name)
}

// newTestManager creates a Manager with the given lister and fake k8s client,
// bypassing system.Namespace() by setting namespace directly.
func newTestManager(lister corev1listers.ConfigMapLister) (*Manager, *fake.Clientset) {
	client := fake.NewClientset()
	return &Manager{
		client:    client,
		lister:    lister,
		namespace: testSystemNamespace,
	}, client
}

// contractConfigMap builds a ConfigMap containing the serialized contract.
func contractConfigMap(contract *Contract) *corev1.ConfigMap {
	data, _ := json.Marshal(contract)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: testSystemNamespace,
		},
		Data: map[string]string{
			ConfigMapDataKey: string(data),
		},
	}
}

// --- GetContract tests ---

func TestManager_GetContract_NotFound(t *testing.T) {
	lister := newFakeConfigMapLister() // empty — Get returns NotFound
	m := &Manager{lister: lister, namespace: testSystemNamespace}

	got, err := m.GetContract(context.Background())
	if err != nil {
		t.Fatalf("GetContract() unexpected error: %v", err)
	}
	if got.Brokers == nil {
		t.Error("Brokers should not be nil")
	}
	if len(got.Brokers) != 0 {
		t.Errorf("expected empty contract, got %v", got.Brokers)
	}
}

func TestManager_GetContract_ListerError(t *testing.T) {
	lister := newFakeConfigMapLister()
	lister.err = fmt.Errorf("internal cache error")
	m := &Manager{lister: lister, namespace: testSystemNamespace}

	_, err := m.GetContract(context.Background())
	if err == nil {
		t.Error("GetContract() expected error, got nil")
	}
}

func TestManager_GetContract_ValidData(t *testing.T) {
	contract := &Contract{
		Brokers: map[string]BrokerContract{
			"ns/br": {Namespace: "ns", Name: "br", StreamName: "stream-x"},
		},
		Generation: 5,
	}
	lister := newFakeConfigMapLister(contractConfigMap(contract))
	m := &Manager{lister: lister, namespace: testSystemNamespace}

	got, err := m.GetContract(context.Background())
	if err != nil {
		t.Fatalf("GetContract() unexpected error: %v", err)
	}
	if got.Generation != 5 {
		t.Errorf("Generation = %d, want 5", got.Generation)
	}
	b, ok := got.Brokers["ns/br"]
	if !ok {
		t.Fatal("broker not in returned contract")
	}
	if b.StreamName != "stream-x" {
		t.Errorf("StreamName = %q, want %q", b.StreamName, "stream-x")
	}
}

// --- UpdateBroker tests ---

func TestManager_UpdateBroker_CreatesConfigMap(t *testing.T) {
	lister := newFakeConfigMapLister() // cache is empty
	m, client := newTestManager(lister)

	broker := BrokerContract{
		UID: "uid-1", Namespace: "ns", Name: "br",
		StreamName: "stream-1", Path: "/ns/br",
	}

	if err := m.UpdateBroker(context.Background(), broker); err != nil {
		t.Fatalf("UpdateBroker() unexpected error: %v", err)
	}

	// Verify ConfigMap was created via the fake client.
	cm, err := client.CoreV1().ConfigMaps(testSystemNamespace).Get(
		context.Background(), ConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("ConfigMap not created: %v", err)
	}

	contract, err := ParseContract(cm)
	if err != nil {
		t.Fatalf("ParseContract() error: %v", err)
	}
	if contract.Generation != 1 {
		t.Errorf("Generation = %d, want 1", contract.Generation)
	}
	b, ok := contract.Brokers["ns/br"]
	if !ok {
		t.Fatal("broker not in created contract")
	}
	if b.StreamName != "stream-1" {
		t.Errorf("StreamName = %q, want %q", b.StreamName, "stream-1")
	}

	// Verify annotation.
	if ann := cm.Annotations[ContractGenerationAnnotation]; ann != "1" {
		t.Errorf("annotation %q = %q, want %q", ContractGenerationAnnotation, ann, "1")
	}
}

func TestManager_UpdateBroker_UpdatesExistingConfigMap(t *testing.T) {
	// Pre-populate fake client with an existing CM containing one broker.
	existing := &Contract{
		Brokers: map[string]BrokerContract{
			"ns/old": {Namespace: "ns", Name: "old", StreamName: "stream-old"},
		},
		Generation: 3,
	}
	existingCM := contractConfigMap(existing)

	client := fake.NewClientset(existingCM)
	lister := newFakeConfigMapLister() // lister unused by UpdateBroker
	m := &Manager{client: client, lister: lister, namespace: testSystemNamespace}

	newBroker := BrokerContract{
		Namespace: "ns", Name: "new", StreamName: "stream-new",
	}
	if err := m.UpdateBroker(context.Background(), newBroker); err != nil {
		t.Fatalf("UpdateBroker() unexpected error: %v", err)
	}

	cm, err := client.CoreV1().ConfigMaps(testSystemNamespace).Get(
		context.Background(), ConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}

	contract, err := ParseContract(cm)
	if err != nil {
		t.Fatalf("ParseContract() error: %v", err)
	}

	// Both old and new brokers should be present.
	if _, ok := contract.Brokers["ns/old"]; !ok {
		t.Error("old broker should still be present")
	}
	if _, ok := contract.Brokers["ns/new"]; !ok {
		t.Error("new broker should be added")
	}
	// Generation should be 3 (initial) + 1 (SetBroker) = 4.
	if contract.Generation != 4 {
		t.Errorf("Generation = %d, want 4", contract.Generation)
	}
}

func TestManager_UpdateBroker_AnnotationUpdated(t *testing.T) {
	existing := &Contract{
		Brokers:    make(map[string]BrokerContract),
		Generation: 10,
	}
	existingCM := contractConfigMap(existing)

	client := fake.NewClientset(existingCM)
	m := &Manager{client: client, lister: newFakeConfigMapLister(), namespace: testSystemNamespace}

	broker := BrokerContract{Namespace: "ns", Name: "br"}
	if err := m.UpdateBroker(context.Background(), broker); err != nil {
		t.Fatalf("UpdateBroker() unexpected error: %v", err)
	}

	cm, _ := client.CoreV1().ConfigMaps(testSystemNamespace).Get(
		context.Background(), ConfigMapName, metav1.GetOptions{})

	if ann := cm.Annotations[ContractGenerationAnnotation]; ann != "11" {
		t.Errorf("annotation = %q, want %q", ann, "11")
	}
}

// --- DeleteBroker tests ---

func TestManager_DeleteBroker_ConfigMapNotFound(t *testing.T) {
	client := fake.NewClientset() // no ConfigMap
	m := &Manager{client: client, lister: newFakeConfigMapLister(), namespace: testSystemNamespace}

	// Should return nil — nothing to delete.
	if err := m.DeleteBroker(context.Background(), "ns", "br"); err != nil {
		t.Errorf("DeleteBroker() unexpected error: %v", err)
	}
}

func TestManager_DeleteBroker_BrokerNotInContract(t *testing.T) {
	existing := &Contract{
		Brokers:    make(map[string]BrokerContract),
		Generation: 1,
	}
	client := fake.NewClientset(contractConfigMap(existing))
	m := &Manager{client: client, lister: newFakeConfigMapLister(), namespace: testSystemNamespace}

	// Broker does not exist in contract — should be a no-op.
	if err := m.DeleteBroker(context.Background(), "ns", "missing"); err != nil {
		t.Errorf("DeleteBroker() unexpected error: %v", err)
	}

	// ConfigMap should be unchanged (no Update call).
	cm, _ := client.CoreV1().ConfigMaps(testSystemNamespace).Get(
		context.Background(), ConfigMapName, metav1.GetOptions{})
	contract, _ := ParseContract(cm)
	if contract.Generation != 1 {
		t.Errorf("Generation = %d, want 1 (unchanged)", contract.Generation)
	}
}

func TestManager_DeleteBroker_RemovesBroker(t *testing.T) {
	existing := &Contract{
		Brokers: map[string]BrokerContract{
			"ns/br":    {Namespace: "ns", Name: "br"},
			"ns/other": {Namespace: "ns", Name: "other"},
		},
		Generation: 2,
	}
	client := fake.NewClientset(contractConfigMap(existing))
	m := &Manager{client: client, lister: newFakeConfigMapLister(), namespace: testSystemNamespace}

	if err := m.DeleteBroker(context.Background(), "ns", "br"); err != nil {
		t.Fatalf("DeleteBroker() unexpected error: %v", err)
	}

	cm, err := client.CoreV1().ConfigMaps(testSystemNamespace).Get(
		context.Background(), ConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}

	contract, err := ParseContract(cm)
	if err != nil {
		t.Fatalf("ParseContract() error: %v", err)
	}

	if _, ok := contract.Brokers["ns/br"]; ok {
		t.Error("deleted broker should not be present")
	}
	if _, ok := contract.Brokers["ns/other"]; !ok {
		t.Error("other broker should still be present")
	}
	// Generation should be 2 (initial) + 1 (DeleteBroker) = 3.
	if contract.Generation != 3 {
		t.Errorf("Generation = %d, want 3", contract.Generation)
	}

	// Annotation should reflect new generation.
	if ann := cm.Annotations[ContractGenerationAnnotation]; ann != "3" {
		t.Errorf("annotation = %q, want %q", ann, "3")
	}
}
