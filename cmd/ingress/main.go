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

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/kelseyhightower/envconfig"
	"go.uber.org/zap"

	"knative.dev/pkg/configmap"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/signals"

	"knative.dev/eventing-natss/pkg/broker/ingress"
	"knative.dev/eventing-natss/pkg/common/configloader/fsloader"
	"knative.dev/eventing-natss/pkg/common/constants"
	commonnats "knative.dev/eventing-natss/pkg/common/nats"
)

const (
	component = "natsjs-broker-ingress"
)

type envConfig struct {
	BrokerName string `envconfig:"BROKER_NAME" required:"true"`
	Namespace  string `envconfig:"BROKER_NAMESPACE" required:"true"`
	StreamName string `envconfig:"STREAM_NAME" required:"true"`
	Port       int    `envconfig:"PORT" default:"8080"`
}

func main() {
	ctx := signals.NewContext()

	// Set up logging
	loggingConfig, err := logging.NewConfigFromMap(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logging config: %v\n", err)
		os.Exit(1)
	}

	logger, _ := logging.NewLoggerFromConfig(loggingConfig, component)
	defer logger.Sync()

	ctx = logging.WithLogger(ctx, logger)

	// Load environment configuration
	var env envConfig
	if err := envconfig.Process("", &env); err != nil {
		logger.Fatalw("Failed to process environment variables", zap.Error(err))
	}

	logger.Infow("Starting broker ingress",
		zap.String("broker", env.BrokerName),
		zap.String("namespace", env.Namespace),
		zap.String("stream", env.StreamName),
		zap.Int("port", env.Port),
	)

	// Load NATS configuration from mounted ConfigMap
	configMap, err := configmap.Load(constants.SettingsConfigMapMountPath)
	if err != nil {
		logger.Fatalw("Failed to load NATS configmap", zap.Error(err))
	}

	// Parse NATS configuration
	natsConfig, err := commonnats.LoadEventingNatsConfig(configMap)
	if err != nil {
		logger.Fatalw("Failed to parse NATS configuration", zap.Error(err))
	}

	// Initialize fsloader context for NATS connection
	ctx = fsloader.WithLoader(ctx, configmap.Load)

	// Create NATS connection
	natsConn, err := commonnats.NewNatsConn(ctx, natsConfig)
	if err != nil {
		logger.Fatalw("Failed to create NATS connection", zap.Error(err))
	}
	defer natsConn.Close()

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
	handler := ingress.NewHandler(ingress.HandlerConfig{
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
		Addr:    fmt.Sprintf(":%d", env.Port),
		Handler: mux,
	}

	// Start the server
	errCh := make(chan error, 1)
	go func() {
		logger.Infow("Starting HTTP server", zap.Int("port", env.Port))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for shutdown signal or error
	select {
	case <-ctx.Done():
		logger.Info("Shutting down server...")
		if err := server.Shutdown(context.Background()); err != nil {
			logger.Errorw("Error during server shutdown", zap.Error(err))
		}
	case err := <-errCh:
		logger.Fatalw("Server error", zap.Error(err))
	}

	logger.Info("Broker ingress shutdown complete")
}
