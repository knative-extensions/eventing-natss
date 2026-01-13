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

package filter

import (
	"context"
	"net/http"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
	"k8s.io/client-go/tools/cache"

	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/reconciler"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	brokerinformer "knative.dev/eventing/pkg/client/injection/informers/eventing/v1/broker"
	triggerinformer "knative.dev/eventing/pkg/client/injection/informers/eventing/v1/trigger"
	eventinglisters "knative.dev/eventing/pkg/client/listers/eventing/v1"

	"knative.dev/eventing-natss/pkg/broker/constants"
	commonnats "knative.dev/eventing-natss/pkg/common/nats"
)

type envConfig struct {
	PodName        string        `envconfig:"POD_NAME" required:"true"`
	ContainerName  string        `envconfig:"CONTAINER_NAME" required:"true"`
	NatsURL        string        `envconfig:"NATS_URL" required:"true"`
	FetchBatchSize int           `envconfig:"CONSUMER_FETCH_BATCH_SIZE" default:"0"`
	FetchTimeout   time.Duration `envconfig:"CONSUMER_FETCH_TIMEOUT" default:"0"`
}

// NewController creates a new filter controller
func NewController(ctx context.Context, _ configmap.Watcher) *controller.Impl {
	logger := logging.FromContext(ctx)

	env := &envConfig{}
	if err := envconfig.Process("", env); err != nil {
		logger.Fatalw("Failed to process environment variables", zap.Error(err))
	}

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

	// Get informers
	triggerInformer := triggerinformer.Get(ctx)
	brokerInformer := brokerinformer.Get(ctx)

	// Create consumer manager with optional configuration from environment
	consumerConfig := &ConsumerManagerConfig{
		FetchBatchSize: env.FetchBatchSize,
		FetchTimeout:   env.FetchTimeout,
	}
	consumerManager := NewConsumerManager(ctx, natsConn, js, consumerConfig)

	// Create filter reconciler
	reconciler := NewFilterReconciler(
		ctx,
		triggerInformer.Lister(),
		brokerInformer.Lister(),
		consumerManager,
	)

	// Create a simple controller impl - we don't use the standard reconciler pattern
	// because we handle events directly via informer handlers
	impl := controller.NewContext(ctx, &noopReconciler{}, controller.ControllerOptions{
		WorkQueueName: "NatsJetStreamBrokerFilter",
		Logger:        logger,
	})

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
			UpdateFunc: func(_, newObj interface{}) {
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

	// Start health server in background
	go startHealthServer(ctx, logger, natsConn)

	logger.Info("Filter controller initialized")
	return impl
}

// noopReconciler is a no-op reconciler since we handle events directly
type noopReconciler struct {
	reconciler.LeaderAwareFuncs
}

func (r *noopReconciler) Reconcile(ctx context.Context, key string) error {
	return nil
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
		return broker.GetAnnotations()[eventingv1.BrokerClassAnnotationKey] == constants.BrokerClassName
	}
}

func startHealthServer(ctx context.Context, logger *zap.SugaredLogger, natsConn *nats.Conn) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if natsConn.IsConnected() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("nats disconnected"))
		}
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if natsConn.IsConnected() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("nats disconnected"))
		}
	})

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	logger.Info("Starting health server on :8080")
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Errorw("Health server error", zap.Error(err))
		}
	}()

	<-ctx.Done()
	logger.Info("Shutting down health server")
	server.Shutdown(context.Background())
}
