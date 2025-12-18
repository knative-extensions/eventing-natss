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
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"knative.dev/pkg/logging"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	"knative.dev/eventing/pkg/auth"
	"knative.dev/eventing/pkg/eventingtls"
	"knative.dev/eventing/pkg/kncloudevents"

	brokerutils "knative.dev/eventing-natss/pkg/broker/utils"
)

const (
	// fetchBatchSize is the number of messages to fetch in each batch
	fetchBatchSize = 10
	// fetchTimeout is the timeout for fetching messages
	fetchTimeout = 5 * time.Second
)

// ConsumerManager manages JetStream consumer subscriptions for triggers
type ConsumerManager struct {
	logger *zap.SugaredLogger
	ctx    context.Context

	js   nats.JetStreamContext
	conn *nats.Conn

	// Event dispatcher
	dispatcher *kncloudevents.Dispatcher

	// Map of trigger UID to subscription
	subscriptions map[string]*TriggerSubscription
	mu            sync.RWMutex
}

// TriggerSubscription holds the subscription and handler for a trigger
type TriggerSubscription struct {
	trigger      *eventingv1.Trigger
	subscription *nats.Subscription
	handler      *TriggerHandler
	streamName   string
	consumerName string
	cancel       context.CancelFunc
}

// NewConsumerManager creates a new consumer manager
func NewConsumerManager(ctx context.Context, conn *nats.Conn, js nats.JetStreamContext) *ConsumerManager {
	// Create OIDC token provider and dispatcher
	oidcTokenProvider := auth.NewOIDCTokenProvider(ctx)
	dispatcher := kncloudevents.NewDispatcher(eventingtls.ClientConfig{}, oidcTokenProvider)

	return &ConsumerManager{
		logger:        logging.FromContext(ctx),
		ctx:           ctx,
		js:            js,
		conn:          conn,
		dispatcher:    dispatcher,
		subscriptions: make(map[string]*TriggerSubscription),
	}
}

// SubscribeTrigger creates a pull-based subscription for a trigger's consumer
func (m *ConsumerManager) SubscribeTrigger(
	trigger *eventingv1.Trigger,
	broker *eventingv1.Broker,
	subscriberURI string,
	deadLetterSinkURI string,
	retryConfig *kncloudevents.RetryConfig,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	triggerUID := string(trigger.UID)
	logger := m.logger.With(
		zap.String("trigger", trigger.Name),
		zap.String("namespace", trigger.Namespace),
		zap.String("trigger_uid", triggerUID),
	)

	// Check if we already have a subscription for this trigger
	if existing, ok := m.subscriptions[triggerUID]; ok {
		// Check if configuration has changed
		if existing.handler.subscriberURI == subscriberURI {
			logger.Debugw("trigger subscription already exists and is up to date")
			return nil
		}
		// Configuration changed, unsubscribe and re-subscribe
		logger.Infow("trigger configuration changed, re-subscribing")
		if err := m.unsubscribeLocked(triggerUID); err != nil {
			logger.Warnw("failed to unsubscribe old trigger subscription", zap.Error(err))
		}
	}

	// Create the trigger handler
	handler, err := NewTriggerHandler(
		m.ctx,
		trigger,
		subscriberURI,
		deadLetterSinkURI,
		retryConfig,
		m.dispatcher,
	)
	if err != nil {
		return fmt.Errorf("failed to create trigger handler: %w", err)
	}

	// Derive stream and consumer names
	streamName := brokerutils.BrokerStreamName(broker)
	consumerName := brokerutils.TriggerConsumerName(triggerUID)

	// Verify consumer exists
	_, err = m.js.ConsumerInfo(streamName, consumerName)
	if err != nil {
		handler.Cleanup()
		if errors.Is(err, nats.ErrConsumerNotFound) {
			return fmt.Errorf("consumer %s not found in stream %s: trigger controller may not have reconciled yet", consumerName, streamName)
		}
		return fmt.Errorf("failed to get consumer info: %w", err)
	}

	// Get the filter subject from the consumer's configuration
	filterSubject := brokerutils.BrokerPublishSubjectName(broker.Namespace, broker.Name) + ".>"

	logger.Infow("creating pull subscription for trigger consumer",
		zap.String("stream", streamName),
		zap.String("consumer", consumerName),
		zap.String("filter_subject", filterSubject),
	)

	// Create pull subscription bound to the existing consumer
	sub, err := m.js.PullSubscribe(
		filterSubject,
		consumerName,
		nats.Bind(streamName, consumerName),
	)
	if err != nil {
		handler.Cleanup()
		return fmt.Errorf("failed to create pull subscription: %w", err)
	}

	// Create cancellable context for the fetch loop
	ctx, cancel := context.WithCancel(m.ctx)

	// Store the subscription
	m.subscriptions[triggerUID] = &TriggerSubscription{
		trigger:      trigger,
		subscription: sub,
		handler:      handler,
		streamName:   streamName,
		consumerName: consumerName,
		cancel:       cancel,
	}

	// Start the message fetch loop
	go m.fetchLoop(ctx, triggerUID, sub, handler, logger)

	logger.Infow("successfully started pull subscription for trigger consumer")
	return nil
}

// fetchLoop continuously fetches messages from the pull consumer
func (m *ConsumerManager) fetchLoop(
	ctx context.Context,
	triggerUID string,
	sub *nats.Subscription,
	handler *TriggerHandler,
	logger *zap.SugaredLogger,
) {
	for {
		select {
		case <-ctx.Done():
			logger.Debugw("fetch loop stopped")
			return
		default:
			// Fetch a batch of messages
			msgs, err := sub.Fetch(fetchBatchSize, nats.MaxWait(fetchTimeout))
			if err != nil {
				if errors.Is(err, nats.ErrTimeout) {
					// No messages available, continue polling
					continue
				}
				if errors.Is(err, nats.ErrConnectionClosed) || errors.Is(err, nats.ErrConsumerDeleted) {
					logger.Warnw("subscription closed, stopping fetch loop", zap.Error(err))
					return
				}
				if errors.Is(err, context.Canceled) {
					return
				}
				logger.Errorw("error fetching messages", zap.Error(err))
				// Back off on errors
				time.Sleep(time.Second)
				continue
			}

			// Process fetched messages
			for _, msg := range msgs {
				handler.HandleMessage(msg)
			}
		}
	}
}

// UnsubscribeTrigger removes a subscription for a trigger
func (m *ConsumerManager) UnsubscribeTrigger(triggerUID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.unsubscribeLocked(triggerUID)
}

// unsubscribeLocked removes a subscription (must be called with lock held)
func (m *ConsumerManager) unsubscribeLocked(triggerUID string) error {
	sub, ok := m.subscriptions[triggerUID]
	if !ok {
		return nil
	}

	logger := m.logger.With(
		zap.String("trigger", sub.trigger.Name),
		zap.String("namespace", sub.trigger.Namespace),
	)

	logger.Infow("unsubscribing from trigger consumer")

	// Cancel the fetch loop first
	if sub.cancel != nil {
		sub.cancel()
	}

	// Unsubscribe from the pull consumer
	if err := sub.subscription.Unsubscribe(); err != nil {
		logger.Warnw("failed to unsubscribe", zap.Error(err))
	}

	// Cleanup the handler
	sub.handler.Cleanup()

	// Remove from map
	delete(m.subscriptions, triggerUID)

	return nil
}

// Close closes all subscriptions
func (m *ConsumerManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.logger.Infow("closing consumer manager", zap.Int("subscription_count", len(m.subscriptions)))

	var errs []error
	for uid := range m.subscriptions {
		if err := m.unsubscribeLocked(uid); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing subscriptions: %v", errs)
	}
	return nil
}

// GetSubscriptionCount returns the number of active subscriptions
func (m *ConsumerManager) GetSubscriptionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.subscriptions)
}

// HasSubscription checks if a subscription exists for a trigger
func (m *ConsumerManager) HasSubscription(triggerUID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.subscriptions[triggerUID]
	return ok
}
