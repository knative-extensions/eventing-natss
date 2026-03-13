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
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"

	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/reconciler"
	"knative.dev/pkg/system"

	configmapinformer "knative.dev/pkg/client/injection/kube/informers/core/v1/configmap"

	"knative.dev/eventing-natss/pkg/broker/contract"
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
	NatsURL string `envconfig:"NATS_URL" required:"true"`
	Port    int    `envconfig:"PORT" default:"8080"`
}

// NewController creates a new shared ingress controller
func NewController(ctx context.Context, _ configmap.Watcher) *controller.Impl {
	logger := logging.FromContext(ctx)

	// Load environment configuration
	env := &envConfig{}
	if err := envconfig.Process("", env); err != nil {
		logger.Fatalw("Failed to process environment variables", zap.Error(err))
	}

	logger.Infow("Starting shared broker ingress",
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

	logger.Info("Connected to JetStream")

	// Create the shared ingress handler
	handler := NewHandler(HandlerConfig{
		Logger:    logger,
		JetStream: js,
	})

	// Get ConfigMap informer for watching contract changes
	configMapInformer := configmapinformer.Get(ctx)

	// Load initial contract from ConfigMap
	loadContractFromInformer(configMapInformer.Lister().ConfigMaps(system.Namespace()), handler, logger)

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

	// Watch contract ConfigMap for changes
	configMapInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: filterContractConfigMap,
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				loadContractFromInformer(configMapInformer.Lister().ConfigMaps(system.Namespace()), handler, logger)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				loadContractFromInformer(configMapInformer.Lister().ConfigMaps(system.Namespace()), handler, logger)
			},
			DeleteFunc: func(obj interface{}) {
				// Clear all brokers when contract is deleted
				handler.UpdateContract(&contract.Contract{})
				logger.Info("Contract ConfigMap deleted, cleared all broker mappings")
			},
		},
	})

	logger.Info("Shared ingress controller initialized")
	return impl
}

// filterContractConfigMap returns true if the object is the contract ConfigMap
func filterContractConfigMap(obj interface{}) bool {
	if obj == nil {
		return false
	}

	// Handle tombstone objects
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}

	// Check if it's a ConfigMap
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return false
	}

	return cm.Name == contract.ConfigMapName && cm.Namespace == system.Namespace()
}

// configMapGetter is an interface for getting ConfigMaps by name
type configMapGetter interface {
	Get(name string) (*corev1.ConfigMap, error)
}

// loadContractFromInformer loads the contract from the ConfigMap and updates the handler
func loadContractFromInformer(cmGetter configMapGetter, handler *Handler, logger *zap.SugaredLogger) {
	cm, err := cmGetter.Get(contract.ConfigMapName)
	if err != nil {
		if apierrs.IsNotFound(err) {
			logger.Debug("Contract ConfigMap not found, no brokers registered yet")
			return
		}
		logger.Warnw("Failed to get contract ConfigMap", zap.Error(err))
		return
	}

	// Parse the contract
	c, err := contract.ParseContract(cm)
	if err != nil {
		logger.Errorw("Failed to parse contract", zap.Error(err))
		return
	}

	handler.UpdateContract(c)
	logger.Infow("Contract loaded", zap.Int("broker_count", len(c.Brokers)))
}

// noopReconciler is a no-op reconciler since ingress doesn't reconcile any resources
type noopReconciler struct {
	reconciler.LeaderAwareFuncs
}

func (r *noopReconciler) Reconcile(ctx context.Context, key string) error {
	return nil
}
