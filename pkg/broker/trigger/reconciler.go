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
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rickb777/date/period"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"

	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	pkgreconciler "knative.dev/pkg/reconciler"
	"knative.dev/pkg/resolver"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	triggerreconciler "knative.dev/eventing/pkg/client/injection/reconciler/eventing/v1/trigger"
	eventinglisters "knative.dev/eventing/pkg/client/listers/eventing/v1"

	"knative.dev/eventing-natss/pkg/broker/constants"
	brokerutils "knative.dev/eventing-natss/pkg/broker/utils"
)

const (
	// Event reasons
	ReasonConsumerCreated = "JetStreamConsumerCreated"
	ReasonConsumerUpdated = "JetStreamConsumerUpdated"
	ReasonConsumerFailed  = "JetStreamConsumerFailed"
	ReasonConsumerDeleted = "JetStreamConsumerDeleted"
)

// Reconciler implements triggerreconciler.Interface for Trigger resources.
type Reconciler struct {
	// Listers for Kubernetes resources
	brokerLister eventinglisters.BrokerLister

	// NATS JetStream connection
	js nats.JetStreamContext

	// URI resolver for resolving subscriber and dead letter sink addresses
	uriResolver *resolver.URIResolver

	// Configuration
	filterServiceName string
}

// Check that our Reconciler implements Interface
var _ triggerreconciler.Interface = (*Reconciler)(nil)
var _ triggerreconciler.Finalizer = (*Reconciler)(nil)

// ReconcileKind implements triggerreconciler.Interface
func (r *Reconciler) ReconcileKind(ctx context.Context, trigger *eventingv1.Trigger) pkgreconciler.Event {
	logger := logging.FromContext(ctx)
	logger.Infow("Reconciling trigger", zap.String("trigger", trigger.Name), zap.String("namespace", trigger.Namespace))

	// Step 1: Get the broker and check it's ready
	broker, err := r.brokerLister.Brokers(trigger.Namespace).Get(trigger.Spec.Broker)
	if err != nil {
		if apierrs.IsNotFound(err) {
			trigger.Status.MarkBrokerFailed("BrokerNotFound", "Broker %q does not exist", trigger.Spec.Broker)
			return nil
		}
		trigger.Status.MarkBrokerFailed("BrokerGetFailed", "Failed to get broker: %v", err)
		return fmt.Errorf("failed to get broker: %w", err)
	}

	// Check broker class
	if broker.GetAnnotations()[eventingv1.BrokerClassAnnotationKey] != constants.BrokerClassName {
		trigger.Status.MarkBrokerFailed("BrokerClassMismatch", "Broker %q is not of class %s", trigger.Spec.Broker, constants.BrokerClassName)
		return nil
	}

	// Check if broker is ready
	if !broker.IsReady() {
		trigger.Status.MarkBrokerFailed("BrokerNotReady", "Broker %q is not ready", trigger.Spec.Broker)
		return nil
	}

	// Broker is ready
	trigger.Status.PropagateBrokerCondition(broker.Status.GetTopLevelCondition())

	// Step 2: Resolve the subscriber URI
	subscriberURI, err := r.resolveSubscriberURI(ctx, trigger)
	if err != nil {
		trigger.Status.MarkSubscriberResolvedFailed("SubscriberResolveFailed", "Failed to resolve subscriber: %v", err)
		return fmt.Errorf("failed to resolve subscriber: %w", err)
	}
	trigger.Status.SubscriberURI = subscriberURI
	trigger.Status.MarkSubscriberResolvedSucceeded()

	// Step 3: Handle dead letter sink if configured
	if trigger.Spec.Delivery != nil && trigger.Spec.Delivery.DeadLetterSink != nil {
		deadLetterURI, err := r.resolveDeadLetterURI(ctx, trigger)
		if err != nil {
			trigger.Status.MarkDeadLetterSinkResolvedFailed("DeadLetterSinkResolveFailed", "Failed to resolve dead letter sink: %v", err)
			return fmt.Errorf("failed to resolve dead letter sink: %w", err)
		}
		trigger.Status.DeadLetterSinkURI = deadLetterURI
		trigger.Status.MarkDeadLetterSinkResolvedSucceeded()
	} else {
		trigger.Status.MarkDeadLetterSinkNotConfigured()
	}

	// Step 4: Reconcile the JetStream consumer
	if err := r.reconcileConsumer(ctx, trigger, broker); err != nil {
		trigger.Status.MarkNotSubscribed("ConsumerFailed", "Failed to create JetStream consumer: %v", err)
		return err
	}

	// Mark subscription as ready
	trigger.Status.PropagateSubscriptionCondition(&apis.Condition{
		Type:   apis.ConditionReady,
		Status: corev1.ConditionTrue,
	})

	// Mark dependency as succeeded (we don't have external dependencies)
	trigger.Status.MarkDependencySucceeded()

	// Mark OIDC identity as not needed (OIDC authentication is not enabled)
	trigger.Status.MarkOIDCIdentityCreatedSucceededWithReason("OIDCIdentitySkipped", "OIDC authentication is not enabled")

	logger.Infow("Trigger reconciliation completed successfully", zap.String("trigger", trigger.Name))
	return nil
}

// FinalizeKind cleans up resources when the trigger is deleted
func (r *Reconciler) FinalizeKind(ctx context.Context, trigger *eventingv1.Trigger) pkgreconciler.Event {
	logger := logging.FromContext(ctx)
	logger.Infow("Finalizing trigger", zap.String("trigger", trigger.Name))

	// Get the broker to find the stream name
	broker, err := r.brokerLister.Brokers(trigger.Namespace).Get(trigger.Spec.Broker)
	if err != nil {
		if apierrs.IsNotFound(err) {
			// Broker is gone, nothing to clean up
			logger.Debugw("Broker not found during finalization, skipping consumer deletion")
			return nil
		}
		return fmt.Errorf("failed to get broker: %w", err)
	}

	streamName := brokerutils.BrokerStreamName(broker)
	consumerName := brokerutils.TriggerConsumerName(string(trigger.UID))

	// Delete the consumer
	err = r.js.DeleteConsumer(streamName, consumerName)
	if err != nil && !errors.Is(err, nats.ErrConsumerNotFound) {
		logger.Errorw("Failed to delete JetStream consumer", zap.Error(err), zap.String("consumer", consumerName))
		return fmt.Errorf("failed to delete consumer: %w", err)
	}

	logger.Infow("Trigger finalization completed", zap.String("trigger", trigger.Name))
	controller.GetEventRecorder(ctx).Event(trigger, corev1.EventTypeNormal, ReasonConsumerDeleted, "JetStream consumer deleted")
	return nil
}

// resolveSubscriberURI resolves the subscriber URI from the trigger spec
func (r *Reconciler) resolveSubscriberURI(ctx context.Context, trigger *eventingv1.Trigger) (*apis.URL, error) {
	dest := trigger.Spec.Subscriber

	// Convert to duckv1.Destination for the resolver
	destination := duckv1.Destination{
		URI: dest.URI,
	}
	if dest.Ref != nil {
		namespace := dest.Ref.Namespace
		if namespace == "" {
			namespace = trigger.Namespace
		}
		destination.Ref = &duckv1.KReference{
			Kind:       dest.Ref.Kind,
			Namespace:  namespace,
			Name:       dest.Ref.Name,
			APIVersion: dest.Ref.APIVersion,
		}
	}

	return r.uriResolver.URIFromDestinationV1(ctx, destination, trigger)
}

// resolveDeadLetterURI resolves the dead letter sink URI from the trigger spec
func (r *Reconciler) resolveDeadLetterURI(ctx context.Context, trigger *eventingv1.Trigger) (*apis.URL, error) {
	if trigger.Spec.Delivery == nil || trigger.Spec.Delivery.DeadLetterSink == nil {
		return nil, nil
	}

	dest := trigger.Spec.Delivery.DeadLetterSink

	// Convert to duckv1.Destination for the resolver
	destination := duckv1.Destination{
		URI: dest.URI,
	}
	if dest.Ref != nil {
		namespace := dest.Ref.Namespace
		if namespace == "" {
			namespace = trigger.Namespace
		}
		destination.Ref = &duckv1.KReference{
			Kind:       dest.Ref.Kind,
			Namespace:  namespace,
			Name:       dest.Ref.Name,
			APIVersion: dest.Ref.APIVersion,
		}
	}

	return r.uriResolver.URIFromDestinationV1(ctx, destination, trigger)
}

// reconcileConsumer creates or updates the JetStream consumer for the trigger
func (r *Reconciler) reconcileConsumer(ctx context.Context, trigger *eventingv1.Trigger, broker *eventingv1.Broker) error {
	logger := logging.FromContext(ctx)

	streamName := brokerutils.BrokerStreamName(broker)
	consumerName := brokerutils.TriggerConsumerName(string(trigger.UID))

	// Build consumer configuration
	consumerConfig := r.buildConsumerConfig(trigger, broker, consumerName)

	// Check if consumer exists
	_, err := r.js.ConsumerInfo(streamName, consumerName)
	if err != nil {
		if !errors.Is(err, nats.ErrConsumerNotFound) {
			logger.Errorw("Failed to get consumer info", zap.Error(err), zap.String("consumer", consumerName))
			controller.GetEventRecorder(ctx).Event(trigger, corev1.EventTypeWarning, ReasonConsumerFailed, err.Error())
			return fmt.Errorf("failed to get consumer info: %w", err)
		}

		// Consumer doesn't exist, create it
		_, err = r.js.AddConsumer(streamName, consumerConfig)
		if err != nil {
			logger.Errorw("Failed to create JetStream consumer", zap.Error(err), zap.String("consumer", consumerName))
			controller.GetEventRecorder(ctx).Event(trigger, corev1.EventTypeWarning, ReasonConsumerFailed, err.Error())
			return fmt.Errorf("failed to create consumer: %w", err)
		}

		logger.Infow("JetStream consumer created", zap.String("consumer", consumerName))
		controller.GetEventRecorder(ctx).Event(trigger, corev1.EventTypeNormal, ReasonConsumerCreated, "JetStream consumer created")
		return nil
	}

	// Consumer exists, update it
	_, err = r.js.UpdateConsumer(streamName, consumerConfig)
	if err != nil {
		// Some fields cannot be updated, so log and continue if it's just a minor mismatch
		logger.Warnw("Failed to update JetStream consumer", zap.Error(err), zap.String("consumer", consumerName))
	} else {
		logger.Debugw("JetStream consumer updated", zap.String("consumer", consumerName))
		controller.GetEventRecorder(ctx).Event(trigger, corev1.EventTypeNormal, ReasonConsumerUpdated, "JetStream consumer updated")
	}

	return nil
}

// buildConsumerConfig creates a NATS JetStream pull consumer configuration for the trigger
func (r *Reconciler) buildConsumerConfig(trigger *eventingv1.Trigger, broker *eventingv1.Broker, consumerName string) *nats.ConsumerConfig {
	// The filter subject matches all events published to the broker
	filterSubject := brokerutils.BrokerPublishSubjectName(broker.Namespace, broker.Name) + ".>"

	// Default delivery configuration
	ackWait := 30 * time.Second
	maxDeliver := 3

	// Apply delivery configuration from trigger spec
	if trigger.Spec.Delivery != nil {
		if trigger.Spec.Delivery.Retry != nil {
			maxDeliver = int(*trigger.Spec.Delivery.Retry) + 1
		}
		if trigger.Spec.Delivery.Timeout != nil {
			timeout, err := period.Parse(*trigger.Spec.Delivery.Timeout)
			if err == nil {
				ackWait, _ = timeout.Duration()
			}
		}
	}

	// Pull consumer configuration (no DeliverSubject or DeliverGroup)
	return &nats.ConsumerConfig{
		Durable:       consumerName,
		Name:          consumerName,
		FilterSubject: filterSubject,
		AckPolicy:     nats.AckExplicitPolicy,
		AckWait:       ackWait,
		MaxDeliver:    maxDeliver,
		DeliverPolicy: nats.DeliverNewPolicy,
		ReplayPolicy:  nats.ReplayInstantPolicy,
	}
}
