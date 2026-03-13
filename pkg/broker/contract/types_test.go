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
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBrokerKey(t *testing.T) {
	tests := []struct {
		namespace, name, want string
	}{
		{"ns", "br", "ns/br"},
		{"default", "my-broker", "default/my-broker"},
		{"", "broker", "/broker"},
		{"ns", "", "ns/"},
	}
	for _, tc := range tests {
		if got := BrokerKey(tc.namespace, tc.name); got != tc.want {
			t.Errorf("BrokerKey(%q, %q) = %q, want %q", tc.namespace, tc.name, got, tc.want)
		}
	}
}

func TestContract_GetBroker(t *testing.T) {
	c := &Contract{
		Brokers: map[string]BrokerContract{
			"ns/br": {Namespace: "ns", Name: "br", StreamName: "stream-1"},
		},
	}

	t.Run("found", func(t *testing.T) {
		got, ok := c.GetBroker("ns", "br")
		if !ok {
			t.Fatal("GetBroker() ok = false, want true")
		}
		if got.StreamName != "stream-1" {
			t.Errorf("StreamName = %q, want %q", got.StreamName, "stream-1")
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, ok := c.GetBroker("ns", "missing")
		if ok {
			t.Error("GetBroker() ok = true, want false")
		}
	})
}

func TestContract_GetBrokerByPath(t *testing.T) {
	c := &Contract{
		Brokers: map[string]BrokerContract{
			"ns/br": {Namespace: "ns", Name: "br", Path: "/ns/br"},
		},
	}

	t.Run("found", func(t *testing.T) {
		got, ok := c.GetBrokerByPath("/ns/br")
		if !ok {
			t.Fatal("GetBrokerByPath() ok = false, want true")
		}
		if got.Name != "br" {
			t.Errorf("Name = %q, want %q", got.Name, "br")
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, ok := c.GetBrokerByPath("/other/path")
		if ok {
			t.Error("GetBrokerByPath() ok = true, want false")
		}
	})

	t.Run("empty contract", func(t *testing.T) {
		_, ok := (&Contract{}).GetBrokerByPath("/ns/br")
		if ok {
			t.Error("GetBrokerByPath() on empty contract ok = true, want false")
		}
	})
}

func TestContract_SetBroker(t *testing.T) {
	t.Run("initializes nil map", func(t *testing.T) {
		c := &Contract{}
		c.SetBroker(BrokerContract{Namespace: "ns", Name: "br"})
		if c.Brokers == nil {
			t.Fatal("Brokers map should not be nil after SetBroker")
		}
		if _, ok := c.Brokers["ns/br"]; !ok {
			t.Error("broker not stored")
		}
		if c.Generation != 1 {
			t.Errorf("Generation = %d, want 1", c.Generation)
		}
	})

	t.Run("increments generation on each call", func(t *testing.T) {
		c := &Contract{Brokers: make(map[string]BrokerContract)}
		c.SetBroker(BrokerContract{Namespace: "ns", Name: "a"})
		c.SetBroker(BrokerContract{Namespace: "ns", Name: "b"})
		if c.Generation != 2 {
			t.Errorf("Generation = %d, want 2", c.Generation)
		}
	})

	t.Run("overwrites existing broker", func(t *testing.T) {
		c := &Contract{Brokers: make(map[string]BrokerContract)}
		c.SetBroker(BrokerContract{Namespace: "ns", Name: "br", StreamName: "old"})
		c.SetBroker(BrokerContract{Namespace: "ns", Name: "br", StreamName: "new"})
		if got := c.Brokers["ns/br"].StreamName; got != "new" {
			t.Errorf("StreamName = %q, want %q", got, "new")
		}
	})
}

func TestContract_DeleteBroker(t *testing.T) {
	t.Run("removes existing broker", func(t *testing.T) {
		c := &Contract{
			Brokers: map[string]BrokerContract{
				"ns/br": {Namespace: "ns", Name: "br"},
			},
		}
		c.DeleteBroker("ns", "br")
		if _, ok := c.Brokers["ns/br"]; ok {
			t.Error("broker should have been deleted")
		}
		if c.Generation != 1 {
			t.Errorf("Generation = %d, want 1", c.Generation)
		}
	})

	t.Run("non-existent broker still increments generation", func(t *testing.T) {
		c := &Contract{Brokers: make(map[string]BrokerContract)}
		c.DeleteBroker("ns", "missing")
		if c.Generation != 1 {
			t.Errorf("Generation = %d, want 1", c.Generation)
		}
	})
}

func TestParseContract(t *testing.T) {
	t.Run("nil ConfigMap", func(t *testing.T) {
		got, err := ParseContract(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Brokers == nil {
			t.Error("Brokers map should not be nil")
		}
	})

	t.Run("ConfigMap with nil Data", func(t *testing.T) {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: ConfigMapName}}
		got, err := ParseContract(cm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Brokers) != 0 {
			t.Errorf("expected empty brokers, got %v", got.Brokers)
		}
	})

	t.Run("ConfigMap missing contract key", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			Data: map[string]string{"other-key": "value"},
		}
		got, err := ParseContract(cm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Brokers) != 0 {
			t.Errorf("expected empty brokers, got %v", got.Brokers)
		}
	})

	t.Run("ConfigMap empty contract value", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			Data: map[string]string{ConfigMapDataKey: ""},
		}
		got, err := ParseContract(cm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Brokers) != 0 {
			t.Errorf("expected empty brokers, got %v", got.Brokers)
		}
	})

	t.Run("valid JSON with broker", func(t *testing.T) {
		contract := &Contract{
			Brokers: map[string]BrokerContract{
				"ns/br": {Namespace: "ns", Name: "br", StreamName: "s"},
			},
			Generation: 3,
		}
		data, _ := json.Marshal(contract)
		cm := &corev1.ConfigMap{
			Data: map[string]string{ConfigMapDataKey: string(data)},
		}
		got, err := ParseContract(cm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Generation != 3 {
			t.Errorf("Generation = %d, want 3", got.Generation)
		}
		if b, ok := got.Brokers["ns/br"]; !ok || b.StreamName != "s" {
			t.Errorf("broker not parsed correctly: %+v", got.Brokers)
		}
	})

	t.Run("JSON with null brokers field initializes map", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			Data: map[string]string{ConfigMapDataKey: `{"generation":1}`},
		}
		got, err := ParseContract(cm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Brokers == nil {
			t.Error("Brokers map should not be nil after parsing null field")
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			Data: map[string]string{ConfigMapDataKey: "not-json"},
		}
		_, err := ParseContract(cm)
		if err == nil {
			t.Error("expected error for invalid JSON, got nil")
		}
	})
}

func TestSerializeContract(t *testing.T) {
	t.Run("empty contract", func(t *testing.T) {
		c := &Contract{Brokers: make(map[string]BrokerContract)}
		s, err := SerializeContract(c)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s == "" {
			t.Error("serialized string should not be empty")
		}
	})

	t.Run("contract with broker", func(t *testing.T) {
		c := &Contract{
			Brokers: map[string]BrokerContract{
				"ns/br": {Namespace: "ns", Name: "br", StreamName: "stream"},
			},
			Generation: 2,
		}
		s, err := SerializeContract(c)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var got Contract
		if err := json.Unmarshal([]byte(s), &got); err != nil {
			t.Fatalf("result is not valid JSON: %v", err)
		}
		if got.Generation != 2 {
			t.Errorf("Generation = %d, want 2", got.Generation)
		}
	})
}

func TestParseSerializeRoundTrip(t *testing.T) {
	original := &Contract{
		Brokers: map[string]BrokerContract{
			"ns/br":   {Namespace: "ns", Name: "br", StreamName: "s1", Path: "/ns/br", Generation: 1},
			"ns2/br2": {Namespace: "ns2", Name: "br2", StreamName: "s2", Path: "/ns2/br2", Generation: 5},
		},
		Generation: 7,
	}

	data, err := SerializeContract(original)
	if err != nil {
		t.Fatalf("SerializeContract() error: %v", err)
	}

	cm := &corev1.ConfigMap{
		Data: map[string]string{ConfigMapDataKey: data},
	}

	got, err := ParseContract(cm)
	if err != nil {
		t.Fatalf("ParseContract() error: %v", err)
	}

	if got.Generation != original.Generation {
		t.Errorf("Generation = %d, want %d", got.Generation, original.Generation)
	}
	if len(got.Brokers) != len(original.Brokers) {
		t.Errorf("len(Brokers) = %d, want %d", len(got.Brokers), len(original.Brokers))
	}
	for k, want := range original.Brokers {
		got, ok := got.Brokers[k]
		if !ok {
			t.Errorf("broker %q missing after round-trip", k)
			continue
		}
		if got.StreamName != want.StreamName {
			t.Errorf("broker %q StreamName = %q, want %q", k, got.StreamName, want.StreamName)
		}
		if got.Path != want.Path {
			t.Errorf("broker %q Path = %q, want %q", k, got.Path, want.Path)
		}
	}
}
