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
	"fmt"
	"net/http"
	"time"

	"github.com/kelseyhightower/envconfig"
	"go.uber.org/zap"

	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/reconciler"

	commonnats "knative.dev/eventing-natss/pkg/common/nats"
)

// todo: Make these confgurable when migrate to shared ingress
const (
	// HTTP server timeouts
	httpReadTimeout  = 30 * time.Second
	httpWriteTimeout = 30 * time.Second
	httpIdleTimeout  = 120 * time.Second
)

type envConfig struct {
	BrokerName string `envconfig:"BROKER_NAME" required:"true"`
	Namespace  string `envconfig:"BROKER_NAMESPACE" required:"true"`
	StreamName string `envconfig:"STREAM_NAME" required:"true"`
	NatsURL    string `envconfig:"NATS_URL" required:"true"`
	Port       int    `envconfig:"PORT" default:"8080"`
}

// NewController creates a new ingress controller
func NewController(ctx context.Context, _ configmap.Watcher) *controller.Impl {
	logger := logging.FromContext(ctx)

	// Load environment configuration
	env := &envConfig{}
	if err := envconfig.Process("", env); err != nil {
		logger.Fatalw("Failed to process environment variables", zap.Error(err))
	}

	logger.Infow("Starting broker ingress",
		zap.String("broker", env.BrokerName),
		zap.String("namespace", env.Namespace),
		zap.String("stream", env.StreamName),
		zap.String("nats_url", env.NatsURL),
		zap.Int("port", env.Port),
	)

	// Create NATS connection using URL from environment variable
	natsConn, err := commonnats.NewNatsConnFromURL(ctx, env.NatsURL)
	if err != nil {
		logger.Fatalw("Failed to create NATS connection", zap.Error(err))
	}

	// Create JetStream context
	js, err := natsConn.JetStream()
	if err != nil {
		logger.Fatalw("Failed to create JetStream context", zap.Error(err))
	}

	// Verify stream exists
	_, err = js.StreamInfo(env.StreamName)
	if err != nil {
		logger.Fatalw("Stream does not exist", zap.Error(err), zap.String("stream", env.StreamName))
	}

	logger.Infow("Connected to JetStream", zap.String("stream", env.StreamName))

	// Create the ingress handler
	handler := NewHandler(HandlerConfig{
		Logger:     logger,
		JetStream:  js,
		BrokerName: env.BrokerName,
		Namespace:  env.Namespace,
		StreamName: env.StreamName,
	})

	// Set up HTTP server with routes
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.HandleFunc("/healthz", handler.LivenessChecker())
	mux.HandleFunc("/readyz", handler.ReadinessChecker())

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", env.Port),
		Handler:      mux,
		ReadTimeout:  httpReadTimeout,
		WriteTimeout: httpWriteTimeout,
		IdleTimeout:  httpIdleTimeout,
	}

	// Start HTTP server in background
	go func() {
		logger.Infow("Starting HTTP server", zap.Int("port", env.Port))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalw("HTTP server error", zap.Error(err))
		}
	}()

	// Handle graceful shutdown
	go func() {
		<-ctx.Done()
		logger.Info("Shutting down HTTP server")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(ctx)
		natsConn.Close()
	}()

	// Create a no-op controller impl since ingress doesn't reconcile any resources
	impl := controller.NewContext(ctx, &noopReconciler{}, controller.ControllerOptions{
		WorkQueueName: "NatsJetStreamBrokerIngress",
		Logger:        logger,
	})

	logger.Info("Ingress controller initialized")
	return impl
}

// noopReconciler is a no-op reconciler since ingress doesn't reconcile any resources
type noopReconciler struct {
	reconciler.LeaderAwareFuncs
}

func (r *noopReconciler) Reconcile(ctx context.Context, key string) error {
	return nil
}
