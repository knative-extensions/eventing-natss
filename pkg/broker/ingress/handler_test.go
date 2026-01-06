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

	"github.com/nats-io/nats.go"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"go.uber.org/zap"
)

func runBasicJetStreamServer(t *testing.T) *natsserver.Server {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
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
		Logger:     logger,
		JetStream:  js,
		BrokerName: "test-broker",
		Namespace:  "test-namespace",
		StreamName: "TEST_STREAM",
	})

	if handler == nil {
		t.Fatal("NewHandler returned nil")
	}

	if handler.brokerName != "test-broker" {
		t.Errorf("brokerName = %v, want test-broker", handler.brokerName)
	}

	if handler.namespace != "test-namespace" {
		t.Errorf("namespace = %v, want test-namespace", handler.namespace)
	}

	if handler.streamName != "TEST_STREAM" {
		t.Errorf("streamName = %v, want TEST_STREAM", handler.streamName)
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
		Logger:     logger,
		JetStream:  js,
		BrokerName: "test-broker",
		Namespace:  "test-namespace",
		StreamName: "TEST_STREAM",
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
		Logger:     logger,
		JetStream:  js,
		BrokerName: "test-broker",
		Namespace:  "test-namespace",
		StreamName: "TEST_STREAM",
	})

	// Request without CloudEvents headers
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("not a cloud event"))
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
		Logger:     logger,
		JetStream:  js,
		BrokerName: "test-broker",
		Namespace:  "test-namespace",
		StreamName: "TEST_STREAM",
	})

	// Create a valid CloudEvent in structured format
	event := map[string]interface{}{
		"specversion": "1.0",
		"type":        "test.type",
		"source":      "test/source",
		"id":          "test-id-123",
		"data":        map[string]string{"key": "value"},
	}

	eventJSON, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(eventJSON))
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
		Logger:     logger,
		JetStream:  js,
		BrokerName: "test-broker",
		Namespace:  "default",
		StreamName: "BINARY_STREAM",
	})

	// Create a valid CloudEvent in binary format
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"key": "value"}`))
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

	t.Run("stream available", func(t *testing.T) {
		// Create stream first
		_, err = js.AddStream(&nats.StreamConfig{
			Name:     "READY_STREAM",
			Subjects: []string{"ready.>"},
		})
		if err != nil {
			t.Fatalf("Failed to create stream: %v", err)
		}

		handler := NewHandler(HandlerConfig{
			Logger:     logger,
			JetStream:  js,
			BrokerName: "test-broker",
			Namespace:  "test-namespace",
			StreamName: "READY_STREAM",
		})

		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		w := httptest.NewRecorder()

		handler.ReadinessChecker()(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Status code = %v, want %v", w.Code, http.StatusOK)
		}
	})

	t.Run("stream not available", func(t *testing.T) {
		handler := NewHandler(HandlerConfig{
			Logger:     logger,
			JetStream:  js,
			BrokerName: "test-broker",
			Namespace:  "test-namespace",
			StreamName: "NONEXISTENT_STREAM",
		})

		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		w := httptest.NewRecorder()

		handler.ReadinessChecker()(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("Status code = %v, want %v", w.Code, http.StatusServiceUnavailable)
		}
	})
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
		Logger:     logger,
		JetStream:  js,
		BrokerName: "test-broker",
		Namespace:  "test-namespace",
		StreamName: "TEST_STREAM",
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	handler.LivenessChecker()(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %v, want %v", w.Code, http.StatusOK)
	}
}
