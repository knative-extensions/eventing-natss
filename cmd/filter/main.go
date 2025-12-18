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
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kelseyhightower/envconfig"
	"go.uber.org/zap"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/injection"
	"knative.dev/pkg/injection/sharedmain"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/signals"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	brokerinformer "knative.dev/eventing/pkg/client/injection/informers/eventing/v1/broker"
	triggerinformer "knative.dev/eventing/pkg/client/injection/informers/eventing/v1/trigger"
	eventinglisters "knative.dev/eventing/pkg/client/listers/eventing/v1"

	"knative.dev/eventing-natss/pkg/broker/filter"
	"knative.dev/eventing-natss/pkg/common/configloader/fsloader"
	"knative.dev/eventing-natss/pkg/common/constants"
	commonnats "knative.dev/eventing-natss/pkg/common/nats"
)

const (
	component = "natsjs-broker-filter"
)

type envConfig struct {
	Port int `envconfig:"PORT" default:"8080"`
}

func main() {
	ctx := signals.NewContext()

	// Setup logging
	cfg, err := sharedmain.GetLoggingConfig(ctx)
	if err != nil {
		panic(err)
	}
	logger, _ := logging.NewLoggerFromConfig(cfg, component)
	defer logger.Sync()
	ctx = logging.WithLogger(ctx, logger)

	// Load environment configuration
	var env envConfig
	if err := envconfig.Process("", &env); err != nil {
		logger.Fatalw("Failed to process environment variables", zap.Error(err))
	}

	// Get Kubernetes config
	cfg2, err := rest.InClusterConfig()
	if err != nil {
		logger.Fatalw("Failed to get in-cluster config", zap.Error(err))
	}

	// Setup injection
	ctx, informers := injection.Default.SetupInformers(ctx, cfg2)

	// Get the config loader
	ctx = fsloader.WithLoader(ctx, configmap.Load)
	fsLoader, err := fsloader.Get(ctx)
	if err != nil {
		logger.Fatalw("Failed to get ConfigmapLoader from context", zap.Error(err))
	}

	// Load NATS configuration from mounted ConfigMap
	configMap, err := fsLoader(constants.SettingsConfigMapMountPath)
	if err != nil {
		logger.Fatalw("Failed to load NATS configmap", zap.Error(err))
	}

	// Parse NATS configuration
	natsConfig, err := commonnats.LoadEventingNatsConfig(configMap)
	if err != nil {
		logger.Fatalw("Failed to parse NATS configuration", zap.Error(err))
	}

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

	// Get informers
	triggerInformer := triggerinformer.Get(ctx)
	brokerInformer := brokerinformer.Get(ctx)

	// Create consumer manager
	consumerManager := filter.NewConsumerManager(ctx, natsConn, js)
	defer consumerManager.Close()

	// Create filter reconciler
	reconciler := filter.NewFilterReconciler(
		ctx,
		triggerInformer.Lister(),
		brokerInformer.Lister(),
		consumerManager,
	)

	// Set up event handlers for Trigger resources
	triggerInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: filterTriggersByBrokerClass(brokerInformer.Lister()),
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				trigger := obj.(*eventingv1.Trigger)
				if err := reconciler.ReconcileTrigger(ctx, trigger); err != nil {
					logger.Errorw("Failed to reconcile trigger on add", zap.Error(err),
						zap.String("trigger", trigger.Name),
						zap.String("namespace", trigger.Namespace))
				}
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				trigger := newObj.(*eventingv1.Trigger)
				if err := reconciler.ReconcileTrigger(ctx, trigger); err != nil {
					logger.Errorw("Failed to reconcile trigger on update", zap.Error(err),
						zap.String("trigger", trigger.Name),
						zap.String("namespace", trigger.Namespace))
				}
			},
			DeleteFunc: func(obj interface{}) {
				trigger := obj.(*eventingv1.Trigger)
				if err := reconciler.DeleteTrigger(string(trigger.UID)); err != nil {
					logger.Errorw("Failed to delete trigger subscription", zap.Error(err),
						zap.String("trigger", trigger.Name),
						zap.String("namespace", trigger.Namespace))
				}
			},
		},
	})

	// Start informers
	logger.Info("Starting informers...")
	if err := controller.StartInformers(ctx.Done(), informers...); err != nil {
		logger.Fatalw("Failed to start informers", zap.Error(err))
	}

	// Start health server
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if natsConn.IsConnected() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("nats disconnected"))
		}
	})
	healthMux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if natsConn.IsConnected() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("nats disconnected"))
		}
	})

	healthServer := &http.Server{
		Addr:    ":8080",
		Handler: healthMux,
	}

	go func() {
		logger.Infof("Starting health server on port %d", env.Port)
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Errorw("Health server error", zap.Error(err))
		}
	}()

	logger.Info("Filter started successfully")

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("Shutting down filter...")

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		logger.Errorw("Error shutting down health server", zap.Error(err))
	}

	logger.Info("Filter shutdown complete")
}

// filterTriggersByBrokerClass returns a filter function that only passes triggers
// referencing brokers of class NatsJetStreamBroker
func filterTriggersByBrokerClass(brokerLister eventinglisters.BrokerLister) func(obj interface{}) bool {
	return func(obj interface{}) bool {
		trigger, ok := obj.(*eventingv1.Trigger)
		if !ok {
			return false
		}

		// Get the broker referenced by this trigger
		broker, err := brokerLister.Brokers(trigger.Namespace).Get(trigger.Spec.Broker)
		if err != nil {
			// If we can't get the broker, include the trigger anyway
			// and let the reconciler handle the error
			return true
		}

		// Check if the broker is of class NatsJetStreamBroker
		return broker.GetAnnotations()[eventingv1.BrokerClassAnnotationKey] == filter.BrokerClass
	}
}
