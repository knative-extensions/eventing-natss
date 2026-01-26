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

package trigger

import (
	"context"

	"github.com/kelseyhightower/envconfig"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"

	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/resolver"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	brokerinformer "knative.dev/eventing/pkg/client/injection/informers/eventing/v1/broker"
	triggerinformer "knative.dev/eventing/pkg/client/injection/informers/eventing/v1/trigger"
	triggerreconciler "knative.dev/eventing/pkg/client/injection/reconciler/eventing/v1/trigger"
	eventinglisters "knative.dev/eventing/pkg/client/listers/eventing/v1"

	"knative.dev/eventing-natss/pkg/broker/constants"
	"knative.dev/eventing-natss/pkg/common/configloader/fsloader"
	commonnats "knative.dev/eventing-natss/pkg/common/nats"
)

const (
	// ComponentName is the name of this controller component
	ComponentName = "natsjs-trigger-controller"
)

// envConfig holds configuration from environment variables
type envConfig struct {
	FilterServiceName string `envconfig:"FILTER_SERVICE_NAME" default:"natsjs-broker-filter"`
}

// NewController creates a new controller for the NATS JetStream Trigger
func NewController(
	ctx context.Context,
	cmw configmap.Watcher,
) *controller.Impl {
	logger := logging.FromContext(ctx)

	// Load environment configuration
	var env envConfig
	if err := envconfig.Process("", &env); err != nil {
		logger.Fatalw("Failed to process environment variables", zap.Error(err))
	}

	logger.Infow("Trigger controller configuration",
		zap.String("filter_service_name", env.FilterServiceName),
	)

	// Get the config loader from context
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

	// Create JetStream context
	js, err := natsConn.JetStream()
	if err != nil {
		logger.Fatalw("Failed to create JetStream context", zap.Error(err))
	}

	// Get informers
	triggerInformer := triggerinformer.Get(ctx)
	brokerInformer := brokerinformer.Get(ctx)

	// Create reconciler
	r := &Reconciler{
		brokerLister: brokerInformer.Lister(),

		js: js,

		filterServiceName: env.FilterServiceName,
	}

	// Create controller implementation using the generated NewImpl
	impl := triggerreconciler.NewImpl(ctx, r, func(impl *controller.Impl) controller.Options {
		return controller.Options{
			AgentName:         ComponentName,
			PromoteFilterFunc: filterTriggersByBrokerClass(r),
		}
	})

	// Create URI resolver for resolving subscriber and dead letter sink addresses
	r.uriResolver = resolver.NewURIResolverFromTracker(ctx, impl.Tracker)

	// Set up event handlers for Trigger resources
	triggerInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: filterTriggersByBrokerClass(r),
		Handler:    controller.HandleAll(impl.Enqueue),
	})

	// Watch Broker resources to re-reconcile triggers when broker changes
	brokerInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: filterBrokersByClass,
		Handler:    controller.HandleAll(enqueueTriggerOfBroker(triggerInformer.Lister(), impl)),
	})

	logger.Info("NATS JetStream Trigger controller initialized")

	return impl
}

// filterTriggersByBrokerClass returns a filter function that only passes triggers
// referencing brokers of class NatsJetStreamBroker
func filterTriggersByBrokerClass(r *Reconciler) func(obj interface{}) bool {
	return func(obj interface{}) bool {
		trigger, ok := obj.(*eventingv1.Trigger)
		if !ok {
			return false
		}

		// Get the broker referenced by this trigger
		broker, err := r.brokerLister.Brokers(trigger.Namespace).Get(trigger.Spec.Broker)
		if err != nil {
			// If we can't get the broker, let the reconciler handle the error
			return true
		}

		// Check if the broker is of class NatsJetStreamBroker
		return broker.GetAnnotations()[eventingv1.BrokerClassAnnotationKey] == constants.BrokerClassName
	}
}

// filterBrokersByClass filters brokers by their class annotation
func filterBrokersByClass(obj interface{}) bool {
	broker, ok := obj.(*eventingv1.Broker)
	if !ok {
		return false
	}
	return broker.GetAnnotations()[eventingv1.BrokerClassAnnotationKey] == constants.BrokerClassName
}

// enqueueTriggerOfBroker returns a handler that enqueues all triggers associated with a broker
func enqueueTriggerOfBroker(lister eventinglisters.TriggerLister, impl *controller.Impl) func(obj interface{}) {
	return func(obj interface{}) {
		broker, ok := obj.(*eventingv1.Broker)
		if !ok {
			return
		}

		// Find all triggers in the same namespace that reference this broker
		triggers, err := lister.Triggers(broker.Namespace).List(labels.Everything())
		if err != nil {
			return
		}

		for _, trigger := range triggers {
			if trigger.Spec.Broker == broker.Name {
				impl.Enqueue(trigger)
			}
		}
	}
}
