/*
Copyright 2024 The Knative Authors

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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"knative.dev/eventing-natss/pkg/broker/contract"
)

func runBasicJetStreamServer(t *testing.T) *natsserver.Server {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir() // Use temp dir to isolate test state
	return natstest.RunServer(&opts)
}

func TestNewHandler(t *testing.T) {
	s := runBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("Failed to get JetStream context: %v", err)
	}

	logger := zap.NewNop().Sugar()

	handler := NewHandler(HandlerConfig{
		Logger:    logger,
		JetStream: js,
	})

	if handler == nil {
		t.Fatal("NewHandler returned nil")
	}

	if handler.GetBrokerCount() != 0 {
		t.Errorf("Initial broker count = %v, want 0", handler.GetBrokerCount())
	}
}

func TestUpdateContract(t *testing.T) {
	s := runBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("Failed to get JetStream context: %v", err)
	}

	logger := zap.NewNop().Sugar()

	handler := NewHandler(HandlerConfig{
		Logger:    logger,
		JetStream: js,
	})

	// Update with contract containing brokers
	c := &contract.Contract{
		Brokers: map[string]contract.BrokerContract{
			"default/test-broker": {
				Namespace:      "default",
				Name:           "test-broker",
				Path:           "/default/test-broker",
				PublishSubject: "default.test-broker._knative_broker",
				StreamName:     "KN_BROKER_default_test-broker",
			},
			"prod/my-broker": {
				Namespace:      "prod",
				Name:           "my-broker",
				Path:           "/prod/my-broker",
				PublishSubject: "prod.my-broker._knative_broker",
				StreamName:     "KN_BROKER_prod_my-broker",
			},
		},
	}

	handler.UpdateContract(c)

	if handler.GetBrokerCount() != 2 {
		t.Errorf("Broker count = %v, want 2", handler.GetBrokerCount())
	}
}

func TestServeHTTP_MethodNotAllowed(t *testing.T) {
	s := runBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("Failed to get JetStream context: %v", err)
	}

	logger := zap.NewNop().Sugar()

	handler := NewHandler(HandlerConfig{
		Logger:    logger,
		JetStream: js,
	})

	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/", nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("Status code = %v, want %v", w.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestServeHTTP_UnknownBrokerPath(t *testing.T) {
	s := runBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("Failed to get JetStream context: %v", err)
	}

	logger := zap.NewNop().Sugar()

	handler := NewHandler(HandlerConfig{
		Logger:    logger,
		JetStream: js,
	})

	// No brokers registered
	req := httptest.NewRequest(http.MethodPost, "/unknown/broker", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status code = %v, want %v", w.Code, http.StatusNotFound)
	}
}

func TestServeHTTP_InvalidCloudEvent(t *testing.T) {
	s := runBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("Failed to get JetStream context: %v", err)
	}

	logger := zap.NewNop().Sugar()

	handler := NewHandler(HandlerConfig{
		Logger:    logger,
		JetStream: js,
	})

	// Register a broker
	c := &contract.Contract{
		Brokers: map[string]contract.BrokerContract{
			"test-namespace/test-broker": {
				Namespace:      "test-namespace",
				Name:           "test-broker",
				Path:           "/test-namespace/test-broker",
				PublishSubject: "test-namespace.test-broker._knative_broker",
				StreamName:     "TEST_STREAM",
			},
		},
	}
	handler.UpdateContract(c)

	// Request without CloudEvents headers
	req := httptest.NewRequest(http.MethodPost, "/test-namespace/test-broker", bytes.NewBufferString("not a cloud event"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status code = %v, want %v", w.Code, http.StatusBadRequest)
	}
}

func TestServeHTTP_ValidCloudEvent(t *testing.T) {
	s := runBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("Failed to get JetStream context: %v", err)
	}

	// Create a stream to publish to
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "TEST_STREAM",
		Subjects: []string{"test-namespace.test-broker._knative_broker.>"},
	})
	if err != nil {
		t.Fatalf("Failed to create stream: %v", err)
	}

	logger := zap.NewNop().Sugar()

	handler := NewHandler(HandlerConfig{
		Logger:    logger,
		JetStream: js,
	})

	// Register the broker
	c := &contract.Contract{
		Brokers: map[string]contract.BrokerContract{
			"test-namespace/test-broker": {
				Namespace:      "test-namespace",
				Name:           "test-broker",
				Path:           "/test-namespace/test-broker",
				PublishSubject: "test-namespace.test-broker._knative_broker",
				StreamName:     "TEST_STREAM",
			},
		},
	}
	handler.UpdateContract(c)

	// Create a valid CloudEvent in structured format
	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "test.type",
		"source":      "test/source",
		"id":          "test-id-123",
		"data":        map[string]string{"key": "value"},
	}

	eventJSON, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, "/test-namespace/test-broker", bytes.NewBuffer(eventJSON))
	req.Header.Set("Content-Type", "application/cloudevents+json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("Status code = %v, want %v", w.Code, http.StatusAccepted)
	}

	// Verify the event was published to the stream
	streamInfo, err := js.StreamInfo("TEST_STREAM")
	if err != nil {
		t.Fatalf("Failed to get stream info: %v", err)
	}

	if streamInfo.State.Msgs != 1 {
		t.Errorf("Stream message count = %v, want 1", streamInfo.State.Msgs)
	}
}

func TestServeHTTP_BinaryCloudEvent(t *testing.T) {
	s := runBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("Failed to get JetStream context: %v", err)
	}

	// Create a stream to publish to
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "BINARY_STREAM",
		Subjects: []string{"default.test-broker._knative_broker.>"},
	})
	if err != nil {
		t.Fatalf("Failed to create stream: %v", err)
	}

	logger := zap.NewNop().Sugar()

	handler := NewHandler(HandlerConfig{
		Logger:    logger,
		JetStream: js,
	})

	// Register the broker
	c := &contract.Contract{
		Brokers: map[string]contract.BrokerContract{
			"default/test-broker": {
				Namespace:      "default",
				Name:           "test-broker",
				Path:           "/default/test-broker",
				PublishSubject: "default.test-broker._knative_broker",
				StreamName:     "BINARY_STREAM",
			},
		},
	}
	handler.UpdateContract(c)

	// Create a valid CloudEvent in binary format
	req := httptest.NewRequest(http.MethodPost, "/default/test-broker", bytes.NewBufferString(`{"key": "value"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("ce-specversion", "1.0")
	req.Header.Set("ce-type", "test.type")
	req.Header.Set("ce-source", "test/source")
	req.Header.Set("ce-id", "binary-test-id")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("Status code = %v, want %v", w.Code, http.StatusAccepted)
	}
}

func TestServeHTTP_MultiplesBrokers(t *testing.T) {
	s := runBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("Failed to get JetStream context: %v", err)
	}

	// Create streams for both brokers
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "STREAM_A",
		Subjects: []string{"ns-a.broker-a._knative_broker.>"},
	})
	if err != nil {
		t.Fatalf("Failed to create stream A: %v", err)
	}

	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "STREAM_B",
		Subjects: []string{"ns-b.broker-b._knative_broker.>"},
	})
	if err != nil {
		t.Fatalf("Failed to create stream B: %v", err)
	}

	logger := zap.NewNop().Sugar()

	handler := NewHandler(HandlerConfig{
		Logger:    logger,
		JetStream: js,
	})

	// Register multiple brokers
	c := &contract.Contract{
		Brokers: map[string]contract.BrokerContract{
			"ns-a/broker-a": {
				Namespace:      "ns-a",
				Name:           "broker-a",
				Path:           "/ns-a/broker-a",
				PublishSubject: "ns-a.broker-a._knative_broker",
				StreamName:     "STREAM_A",
			},
			"ns-b/broker-b": {
				Namespace:      "ns-b",
				Name:           "broker-b",
				Path:           "/ns-b/broker-b",
				PublishSubject: "ns-b.broker-b._knative_broker",
				StreamName:     "STREAM_B",
			},
		},
	}
	handler.UpdateContract(c)

	// Send event to broker A
	eventA := map[string]interface{}{
		"specversion": "1.0",
		"type":        "test.type.a",
		"source":      "test/source/a",
		"id":          "event-a",
		"data":        map[string]string{"broker": "a"},
	}
	eventAJSON, _ := json.Marshal(eventA)
	reqA := httptest.NewRequest(http.MethodPost, "/ns-a/broker-a", bytes.NewBuffer(eventAJSON))
	reqA.Header.Set("Content-Type", "application/cloudevents+json")
	wA := httptest.NewRecorder()
	handler.ServeHTTP(wA, reqA)

	if wA.Code != http.StatusAccepted {
		t.Errorf("Broker A: Status code = %v, want %v", wA.Code, http.StatusAccepted)
	}

	// Send event to broker B
	eventB := map[string]interface{}{
		"specversion": "1.0",
		"type":        "test.type.b",
		"source":      "test/source/b",
		"id":          "event-b",
		"data":        map[string]string{"broker": "b"},
	}
	eventBJSON, _ := json.Marshal(eventB)
	reqB := httptest.NewRequest(http.MethodPost, "/ns-b/broker-b", bytes.NewBuffer(eventBJSON))
	reqB.Header.Set("Content-Type", "application/cloudevents+json")
	wB := httptest.NewRecorder()
	handler.ServeHTTP(wB, reqB)

	if wB.Code != http.StatusAccepted {
		t.Errorf("Broker B: Status code = %v, want %v", wB.Code, http.StatusAccepted)
	}

	// Verify events were published to correct streams
	streamAInfo, err := js.StreamInfo("STREAM_A")
	if err != nil {
		t.Fatalf("Failed to get stream A info: %v", err)
	}
	if streamAInfo.State.Msgs != 1 {
		t.Errorf("Stream A message count = %v, want 1", streamAInfo.State.Msgs)
	}

	streamBInfo, err := js.StreamInfo("STREAM_B")
	if err != nil {
		t.Fatalf("Failed to get stream B info: %v", err)
	}
	if streamBInfo.State.Msgs != 1 {
		t.Errorf("Stream B message count = %v, want 1", streamBInfo.State.Msgs)
	}
}

func TestReadinessChecker(t *testing.T) {
	s := runBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("Failed to get JetStream context: %v", err)
	}

	logger := zap.NewNop().Sugar()

	handler := NewHandler(HandlerConfig{
		Logger:    logger,
		JetStream: js,
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	handler.ReadinessChecker()(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %v, want %v", w.Code, http.StatusOK)
	}
}

func TestLivenessChecker(t *testing.T) {
	s := runBasicJetStreamServer(t)
	defer s.Shutdown()

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("Failed to get JetStream context: %v", err)
	}

	logger := zap.NewNop().Sugar()

	handler := NewHandler(HandlerConfig{
		Logger:    logger,
		JetStream: js,
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	handler.LivenessChecker()(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %v, want %v", w.Code, http.StatusOK)
	}
}
