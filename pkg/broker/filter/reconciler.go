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
	"fmt"

	"go.uber.org/zap"
	apierrs "k8s.io/apimachinery/pkg/api/errors"

	"knative.dev/pkg/logging"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	eventinglisters "knative.dev/eventing/pkg/client/listers/eventing/v1"
	"knative.dev/eventing/pkg/kncloudevents"
)

const (
	// BrokerClass is the annotation value that identifies brokers managed by this controller
	BrokerClass = "NatsJetStreamBroker"
)

// FilterReconciler reconciles triggers and manages consumer subscriptions
type FilterReconciler struct {
	logger *zap.SugaredLogger

	triggerLister eventinglisters.TriggerLister
	brokerLister  eventinglisters.BrokerLister

	consumerManager *ConsumerManager
}

// NewFilterReconciler creates a new filter reconciler
func NewFilterReconciler(
	ctx context.Context,
	triggerLister eventinglisters.TriggerLister,
	brokerLister eventinglisters.BrokerLister,
	consumerManager *ConsumerManager,
) *FilterReconciler {
	return &FilterReconciler{
		logger:          logging.FromContext(ctx),
		triggerLister:   triggerLister,
		brokerLister:    brokerLister,
		consumerManager: consumerManager,
	}
}

// ReconcileTrigger reconciles a trigger to ensure the filter has a subscription
func (r *FilterReconciler) ReconcileTrigger(ctx context.Context, trigger *eventingv1.Trigger) error {
	logger := r.logger.With(
		zap.String("trigger", trigger.Name),
		zap.String("namespace", trigger.Namespace),
	)

	// Get the broker
	broker, err := r.brokerLister.Brokers(trigger.Namespace).Get(trigger.Spec.Broker)
	if err != nil {
		if apierrs.IsNotFound(err) {
			logger.Debugw("broker not found, skipping trigger")
			return nil
		}
		return fmt.Errorf("failed to get broker: %w", err)
	}

	// Check broker class
	if broker.GetAnnotations()[eventingv1.BrokerClassAnnotationKey] != BrokerClass {
		logger.Debugw("broker is not NatsJetStreamBroker, skipping")
		return nil
	}

	// Check if broker is ready
	if !broker.IsReady() {
		logger.Debugw("broker is not ready, skipping trigger")
		return nil
	}

	// Check if trigger is ready
	if trigger.Status.SubscriberURI == nil {
		logger.Debugw("trigger subscriber URI not resolved yet, skipping")
		return nil
	}

	// Get subscriber URI from trigger status
	subscriberURI := trigger.Status.SubscriberURI.String()

	// Get broker ingress URL for reply events
	var brokerIngressURL string
	if broker.Status.Address != nil && broker.Status.Address.URL != nil {
		brokerIngressURL = broker.Status.Address.URL.String()
	}

	// Get dead letter sink URI if configured
	var deadLetterSinkURI string
	if trigger.Status.DeadLetterSinkURI != nil {
		deadLetterSinkURI = trigger.Status.DeadLetterSinkURI.String()
	}

	// Build retry config from trigger delivery spec
	var retryConfig *kncloudevents.RetryConfig
	if trigger.Spec.Delivery != nil {
		config, err := kncloudevents.RetryConfigFromDeliverySpec(*trigger.Spec.Delivery)
		if err != nil {
			logger.Warnw("failed to build retry config from delivery spec", zap.Error(err))
		} else {
			retryConfig = &config
		}
	}

	// Subscribe to the trigger's consumer
	err = r.consumerManager.SubscribeTrigger(
		trigger,
		broker,
		subscriberURI,
		brokerIngressURL,
		deadLetterSinkURI,
		retryConfig,
	)
	if err != nil {
		return fmt.Errorf("failed to subscribe to trigger: %w", err)
	}

	return nil
}

// DeleteTrigger removes the subscription for a deleted trigger
func (r *FilterReconciler) DeleteTrigger(triggerUID string) error {
	return r.consumerManager.UnsubscribeTrigger(triggerUID)
}
