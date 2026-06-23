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

package filter

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/logging"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	"knative.dev/eventing/pkg/auth"
	"knative.dev/eventing/pkg/eventingtls"
	"knative.dev/eventing/pkg/kncloudevents"

	brokerutils "knative.dev/eventing-natss/pkg/broker/utils"
)

// otelScope is the OTel instrumentation scope used for the broker filter's
// tracer and meter. Channel parity: dispatch.duration histogram is named the
// same as the channel's (kn.eventing.dispatch.duration) so they aggregate.
const otelScope = "knative.dev/eventing-natss/pkg/broker/filter"

const (
	// DefaultFetchBatchSize is the default number of messages to fetch in each batch
	DefaultFetchBatchSize = 10
	// DefaultFetchTimeout is the default timeout for fetching messages
	DefaultFetchTimeout = 200 * time.Millisecond
	// DefaultMaxConcurrency is the default per-trigger maximum number of messages
	// dispatched concurrently. Should be >= FetchBatchSize so a full batch always
	// has available slots and is never left fetched-but-unprocessed with its
	// AckWait ticking. Individual triggers can override this via the
	// TriggerMaxConcurrencyAnnotation annotation.
	DefaultMaxConcurrency = 20

	// TriggerMaxConcurrencyAnnotation is the annotation key on a Trigger that
	// overrides the per-trigger dispatch concurrency limit. Must be a positive
	// integer; absent or invalid values fall back to DefaultMaxConcurrency (or
	// the value set via CONSUMER_MAX_CONCURRENCY on the filter deployment).
	TriggerMaxConcurrencyAnnotation = "natsjetstream.eventing.knative.dev/max-concurrency"

	// TriggerFetchBatchSizeAnnotation is the annotation key on a Trigger that
	// overrides the number of messages fetched from JetStream in each pull
	// request. Must be a positive integer; absent or invalid values fall back
	// to DefaultFetchBatchSize (or CONSUMER_FETCH_BATCH_SIZE on the filter
	// deployment).
	TriggerFetchBatchSizeAnnotation = "natsjetstream.eventing.knative.dev/fetch-batch-size"

	// TriggerFetchTimeoutAnnotation is the annotation key on a Trigger that
	// overrides how long a fetch request waits for messages before returning
	// empty. Must be a valid Go duration string (e.g. "500ms", "1s"); absent
	// or invalid values fall back to DefaultFetchTimeout (or
	// CONSUMER_FETCH_TIMEOUT on the filter deployment).
	TriggerFetchTimeoutAnnotation = "natsjetstream.eventing.knative.dev/fetch-timeout"
)

// ConsumerManagerConfig holds configuration for the ConsumerManager
type ConsumerManagerConfig struct {
	// FetchBatchSize is the number of messages to fetch in each batch.
	// Defaults to DefaultFetchBatchSize if not set.
	FetchBatchSize int

	// FetchTimeout is the timeout for fetching messages.
	// Defaults to DefaultFetchTimeout if not set.
	FetchTimeout time.Duration

	// MaxConcurrency is the default per-trigger maximum number of messages
	// dispatched concurrently. Individual triggers can override this via the
	// TriggerMaxConcurrencyAnnotation annotation.
	// Defaults to DefaultMaxConcurrency if not set.
	MaxConcurrency int
}

// ConsumerManager manages JetStream consumer subscriptions for triggers
type ConsumerManager struct {
	logger *zap.SugaredLogger
	ctx    context.Context

	js   nats.JetStreamContext
	conn *nats.Conn

	// Configuration
	fetchBatchSize        int
	fetchTimeout          time.Duration
	defaultMaxConcurrency int

	// Event dispatcher
	dispatcher *kncloudevents.Dispatcher

	// Observability instruments. Resolved from the global OTel providers in
	// NewConsumerManager; passed to each TriggerHandler. The inflight gauge
	// is registered once and walks m.subscriptions on each collection cycle.
	tracer           trace.Tracer
	dispatchDuration metric.Float64Histogram

	// Map of trigger UID to subscription
	subscriptions map[string]*TriggerSubscription
	mu            sync.RWMutex
}

// TriggerSubscription holds the subscription and handler for a trigger
type TriggerSubscription struct {
	trigger        *eventingv1.Trigger
	subscription   *nats.Subscription
	handler        *TriggerHandler
	streamName     string
	consumerName   string
	ackWait        time.Duration
	fetchBatchSize int
	fetchTimeout   time.Duration
	maxConcurrency int
	// sem is a per-trigger counting semaphore. A slot is acquired before
	// spawning each dispatch goroutine and released when it exits, bounding
	// the number of concurrent in-flight HTTP calls for this trigger and
	// providing backpressure to its fetch loop. Replaced wholesale when
	// max-concurrency changes; old in-flight goroutines keep releasing into
	// the channel they captured.
	sem chan struct{}
	// dispatchCtx parents the per-message context used for each in-flight
	// HTTP call. It lives for the subscription's full lifetime so that
	// restarting the fetch loop on an annotation change does not cancel
	// in-progress dispatches.
	dispatchCtx    context.Context
	dispatchCancel context.CancelFunc
	// cancel stops the current fetch loop only. A new fetch loop with new
	// parameters can be started after done closes.
	cancel context.CancelFunc
	// done is closed by the current fetch loop as soon as it returns.
	// Restart waits on this before starting a new fetch loop on the same
	// pull subscription.
	done chan struct{}
	// inflight tracks every dispatch goroutine spawned by any fetch loop
	// for this subscription. unsubscribeLocked waits on it so the NATS
	// subscription and trigger handler are not torn down while a dispatch
	// goroutine is still using them (msg.Ack, h.filter.Filter, etc.).
	inflight sync.WaitGroup
}

// NewConsumerManager creates a new consumer manager
func NewConsumerManager(ctx context.Context, conn *nats.Conn, js nats.JetStreamContext, config *ConsumerManagerConfig) *ConsumerManager {
	logger := logging.FromContext(ctx)

	// Create OIDC token provider and dispatcher
	oidcTokenProvider := auth.NewOIDCTokenProvider(ctx)
	dispatcher := kncloudevents.NewDispatcher(eventingtls.ClientConfig{}, oidcTokenProvider)

	// Apply defaults
	fetchBatchSize := DefaultFetchBatchSize
	fetchTimeout := DefaultFetchTimeout
	maxConcurrency := DefaultMaxConcurrency

	if config != nil {
		if config.FetchBatchSize > 0 {
			fetchBatchSize = config.FetchBatchSize
		}
		if config.FetchTimeout > 0 {
			fetchTimeout = config.FetchTimeout
		}
		if config.MaxConcurrency > 0 {
			maxConcurrency = config.MaxConcurrency
		}
	}

	// Resolve tracer + meter from the global OTel providers. When no real
	// provider is registered these are no-ops, so this is safe to call
	// unconditionally regardless of how the host wires observability.
	tracer := otel.GetTracerProvider().Tracer(otelScope)
	meter := otel.GetMeterProvider().Meter(otelScope)
	dispatchDuration, err := meter.Float64Histogram(
		"kn.eventing.dispatch.duration",
		metric.WithDescription("The duration to dispatch the event"),
		metric.WithUnit("s"),
	)
	if err != nil {
		logger.Warnw("failed to create dispatch duration histogram; metric will be skipped", zap.Error(err))
		dispatchDuration = nil
	}

	cm := &ConsumerManager{
		logger:                logger,
		ctx:                   ctx,
		js:                    js,
		conn:                  conn,
		fetchBatchSize:        fetchBatchSize,
		fetchTimeout:          fetchTimeout,
		defaultMaxConcurrency: maxConcurrency,
		dispatcher:            dispatcher,
		tracer:                tracer,
		dispatchDuration:      dispatchDuration,
		subscriptions:         make(map[string]*TriggerSubscription),
	}

	// Observable gauge: in-flight dispatches per trigger. len(sem) is the
	// number of currently-held semaphore slots, which equals the number of
	// dispatch goroutines this trigger has in flight. Read under m.mu so the
	// subscription map is stable while the callback iterates.
	if _, err := meter.Int64ObservableGauge(
		"kn.eventing.broker.filter.dispatches.inflight",
		metric.WithDescription("Current number of in-flight dispatch goroutines per trigger"),
		metric.WithInt64Callback(func(_ context.Context, obs metric.Int64Observer) error {
			cm.mu.RLock()
			defer cm.mu.RUnlock()
			for _, sub := range cm.subscriptions {
				obs.Observe(int64(len(sub.sem)), metric.WithAttributes(
					attribute.String("trigger.name", sub.trigger.Name),
					attribute.String("trigger.namespace", sub.trigger.Namespace),
				))
			}
			return nil
		}),
	); err != nil {
		logger.Warnw("failed to register inflight observable gauge; metric will be skipped", zap.Error(err))
	}

	return cm
}

// parseTriggerAnnotationInt reads key from annotations as a positive int,
// returning defaultVal (with a warning log) when absent, non-numeric, or <= 0.
func parseTriggerAnnotationInt(annotations map[string]string, key string, defaultVal int, logger *zap.SugaredLogger) int {
	ann := annotations[key]
	if ann == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(ann)
	if err != nil || n <= 0 {
		logger.Warnw("invalid annotation value, using default",
			zap.String("key", key),
			zap.String("annotation", ann),
			zap.Int("default", defaultVal),
		)
		return defaultVal
	}
	return n
}

// parseTriggerAnnotationDuration reads key from annotations as a positive
// duration, returning defaultVal (with a warning log) when absent, unparseable, or <= 0.
func parseTriggerAnnotationDuration(annotations map[string]string, key string, defaultVal time.Duration, logger *zap.SugaredLogger) time.Duration {
	ann := annotations[key]
	if ann == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(ann)
	if err != nil || d <= 0 {
		logger.Warnw("invalid annotation value, using default",
			zap.String("key", key),
			zap.String("annotation", ann),
			zap.Duration("default", defaultVal),
		)
		return defaultVal
	}
	return d
}

// SubscribeTrigger creates a pull-based subscription for a trigger's consumer
func (m *ConsumerManager) SubscribeTrigger(
	trigger *eventingv1.Trigger,
	broker *eventingv1.Broker,
	subscriber duckv1.Addressable,
	brokerIngressURL *duckv1.Addressable,
	deadLetterSink *duckv1.Addressable,
	retryConfig *kncloudevents.RetryConfig,
	noRetryConfig *kncloudevents.RetryConfig,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	triggerUID := string(trigger.UID)
	logger := m.logger.With(
		zap.String("trigger", trigger.Name),
		zap.String("namespace", trigger.Namespace),
		zap.String("trigger_uid", triggerUID),
	)

	// Check if we already have a subscription for this trigger.
	// All handler fields are safe to update in place — the NATS pull
	// subscription is bound to (stream, consumer) which are derived from
	// the immutable (broker, trigger UID) and never change. The fetch loop,
	// however, captures its parameters at start, so a change to any of the
	// three fetch-related annotations requires restarting it.
	if existing, ok := m.subscriptions[triggerUID]; ok {
		existing.handler.subscriber = subscriber
		existing.handler.brokerIngressURL = brokerIngressURL
		existing.handler.noRetryConfig = noRetryConfig
		existing.handler.retryConfig = retryConfig
		existing.handler.filter = buildTriggerFilter(logger, trigger)
		existing.handler.deadLetterSink = deadLetterSink
		existing.handler.trigger = trigger

		newBatch := parseTriggerAnnotationInt(trigger.Annotations, TriggerFetchBatchSizeAnnotation, m.fetchBatchSize, logger)
		newTimeout := parseTriggerAnnotationDuration(trigger.Annotations, TriggerFetchTimeoutAnnotation, m.fetchTimeout, logger)
		newMaxConc := parseTriggerAnnotationInt(trigger.Annotations, TriggerMaxConcurrencyAnnotation, m.defaultMaxConcurrency, logger)

		if newBatch == existing.fetchBatchSize &&
			newTimeout == existing.fetchTimeout &&
			newMaxConc == existing.maxConcurrency {
			logger.Debugw("trigger subscription updated in place")
			return nil
		}

		logger.Infow("fetch parameters changed, restarting fetch loop",
			zap.Int("old_batch_size", existing.fetchBatchSize),
			zap.Int("new_batch_size", newBatch),
			zap.Duration("old_fetch_timeout", existing.fetchTimeout),
			zap.Duration("new_fetch_timeout", newTimeout),
			zap.Int("old_max_concurrency", existing.maxConcurrency),
			zap.Int("new_max_concurrency", newMaxConc),
		)

		// Stop the current fetch loop and wait until it has stopped calling
		// Fetch. Two goroutines must not overlap on the same pull subscription.
		// Worst-case wait is one fetchTimeout (the in-progress Fetch call
		// returning). In-flight dispatches use existing.dispatchCtx and keep
		// running uninterrupted.
		existing.cancel()
		<-existing.done

		newSem := make(chan struct{}, newMaxConc)
		fetchCtx, fetchCancel := context.WithCancel(m.ctx)
		newDone := make(chan struct{})

		existing.fetchBatchSize = newBatch
		existing.fetchTimeout = newTimeout
		existing.maxConcurrency = newMaxConc
		existing.sem = newSem
		existing.cancel = fetchCancel
		existing.done = newDone

		go m.fetchLoop(fetchCtx, existing.dispatchCtx, newDone, &existing.inflight, existing.subscription, existing.handler, existing.ackWait, newBatch, newTimeout, newSem, logger)
		return nil
	}

	// Create the trigger handler
	handler, err := NewTriggerHandler(
		m.ctx,
		trigger,
		subscriber,
		brokerIngressURL,
		deadLetterSink,
		retryConfig,
		noRetryConfig,
		m.dispatcher,
		m.tracer,
		m.dispatchDuration,
	)
	if err != nil {
		return fmt.Errorf("failed to create trigger handler: %w", err)
	}

	// Derive stream and consumer names
	streamName := brokerutils.BrokerStreamName(broker)
	consumerName := brokerutils.TriggerConsumerName(triggerUID)

	// Get consumer info (also verifies consumer exists)
	consumerInfo, err := m.js.ConsumerInfo(streamName, consumerName)
	if err != nil {
		handler.Cleanup()
		if errors.Is(err, nats.ErrConsumerNotFound) {
			return fmt.Errorf("consumer %s not found in stream %s: trigger controller may not have reconciled yet", consumerName, streamName)
		}
		return fmt.Errorf("failed to get consumer info: %w", err)
	}
	ackWait := consumerInfo.Config.AckWait

	// Resolve per-trigger fetch parameters.
	// Trigger annotations take precedence; absent or invalid values fall back
	// to the manager defaults set via env vars on the filter deployment.
	fetchBatchSize := parseTriggerAnnotationInt(trigger.Annotations, TriggerFetchBatchSizeAnnotation, m.fetchBatchSize, logger)
	fetchTimeout := parseTriggerAnnotationDuration(trigger.Annotations, TriggerFetchTimeoutAnnotation, m.fetchTimeout, logger)
	maxConcurrency := parseTriggerAnnotationInt(trigger.Annotations, TriggerMaxConcurrencyAnnotation, m.defaultMaxConcurrency, logger)

	sem := make(chan struct{}, maxConcurrency)

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

	// Set subscription and consumer info on handler
	handler.subscription = sub

	// Two cancellable contexts: dispatchCtx survives fetch-loop restart and
	// parents per-message msgCtx; fetchCtx controls the current fetch loop only.
	dispatchCtx, dispatchCancel := context.WithCancel(m.ctx)
	fetchCtx, fetchCancel := context.WithCancel(m.ctx)
	done := make(chan struct{})

	// Store the subscription
	ts := &TriggerSubscription{
		trigger:        trigger,
		subscription:   sub,
		handler:        handler,
		streamName:     streamName,
		consumerName:   consumerName,
		ackWait:        ackWait,
		fetchBatchSize: fetchBatchSize,
		fetchTimeout:   fetchTimeout,
		maxConcurrency: maxConcurrency,
		sem:            sem,
		dispatchCtx:    dispatchCtx,
		dispatchCancel: dispatchCancel,
		cancel:         fetchCancel,
		done:           done,
	}
	m.subscriptions[triggerUID] = ts

	logger.Infow("starting fetch loop",
		zap.Int("fetch_batch_size", fetchBatchSize),
		zap.Duration("fetch_timeout", fetchTimeout),
		zap.Int("max_concurrency", maxConcurrency),
	)

	// Start the message fetch loop
	go m.fetchLoop(fetchCtx, dispatchCtx, done, &ts.inflight, sub, handler, ackWait, fetchBatchSize, fetchTimeout, sem, logger)

	logger.Infow("successfully started pull subscription for trigger consumer")
	return nil
}

// fetchLoop continuously fetches messages from the pull consumer and dispatches
// them concurrently. Before each fetch it checks how many semaphore slots are
// free and requests at most that many messages. This guarantees every fetched
// message acquires its slot within microseconds — no message sits fetched-but-
// unprocessed with JetStream's AckWait clock already running. When all slots
// are occupied the loop waits one fetchTimeout before re-checking, leaving
// messages safely in the stream. Each dispatch goroutine carries a context
// deadline equal to the consumer's AckWait so that the outbound HTTP call is
// cancelled before JetStream redelivers the message.
//
// Two contexts govern lifetime:
//   - ctx controls the fetch loop itself. Cancel it to stop fetching (used by
//     unsubscribe and restart-on-annotation-change).
//   - dispatchCtx parents each in-flight msgCtx. It survives a fetch-loop
//     restart so a parameter change does not abort in-progress dispatches.
//
// Spawned dispatches are tracked on the subscription-scoped inflight
// WaitGroup, not a local one. unsubscribeLocked waits on it to drain in-flight
// dispatches (across any fetch-loop generation) before tearing down the NATS
// subscription and trigger handler. The fetch loop itself does not wait on
// dispatches — it closes done and returns as soon as it stops calling Fetch,
// so a restart can start a new fetch loop without delay.
func (m *ConsumerManager) fetchLoop(
	ctx context.Context,
	dispatchCtx context.Context,
	done chan struct{},
	inflight *sync.WaitGroup,
	sub *nats.Subscription,
	handler *TriggerHandler,
	ackWait time.Duration,
	fetchBatchSize int,
	fetchTimeout time.Duration,
	sem chan struct{},
	logger *zap.SugaredLogger,
) {
	defer close(done)

	for {
		select {
		case <-ctx.Done():
			logger.Debugw("fetch loop stopped")
			return
		default:
		}

		// Determine how many messages to request this round.
		// When a semaphore is configured, cap the request to the number of
		// free slots so every returned message can acquire its slot
		// immediately — keeping our AckWait context aligned with JetStream's
		// delivery clock. fetchLoop is the only goroutine that acquires slots,
		// so cap(sem)-len(sem) is a stable lower bound: in-flight goroutines
		// can only release slots between here and the acquire, never consume
		// new ones.
		batchSize := fetchBatchSize
		if sem != nil {
			available := cap(sem) - len(sem)
			if available == 0 {
				// All slots occupied. Wait one fetch interval so messages
				// remain in the stream, then re-check.
				select {
				case <-time.After(fetchTimeout):
				case <-ctx.Done():
					return
				}
				continue
			}
			if available < batchSize {
				batchSize = available
			}
		}

		msgs, err := sub.Fetch(batchSize, nats.MaxWait(fetchTimeout))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				continue
			}
			if errors.Is(err, nats.ErrConnectionClosed) || errors.Is(err, nats.ErrConsumerDeleted) || errors.Is(err, nats.ErrBadSubscription) {
				logger.Warnw("subscription closed, stopping fetch loop", zap.Error(err))
				return
			}
			if errors.Is(err, context.Canceled) {
				return
			}
			logger.Errorw("error fetching messages", zap.Error(err))
			time.Sleep(200 * time.Millisecond)
			continue
		}

		for _, msg := range msgs {
			msg := msg

			// Acquire a semaphore slot. Because batchSize was capped to the
			// number of free slots above, this send is non-blocking in the
			// steady state. The ctx.Done case handles clean shutdown.
			if sem != nil {
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
			}

			inflight.Add(1)
			go func() {
				defer func() {
					if sem != nil {
						<-sem
					}
					inflight.Done()
				}()

				var msgCtx context.Context
				var cancel context.CancelFunc
				if ackWait > 0 {
					msgCtx, cancel = context.WithTimeout(dispatchCtx, ackWait)
				} else {
					msgCtx, cancel = context.WithCancel(dispatchCtx)
				}
				defer cancel()

				handler.HandleMessage(msgCtx, msg)
			}()
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

	// Cancel the fetch loop and any in-flight dispatches. The two contexts
	// are separate so restart-on-annotation-change can stop the fetch loop
	// without aborting in-progress HTTP calls; on unsubscribe we cancel both.
	if sub.cancel != nil {
		sub.cancel()
	}
	if sub.dispatchCancel != nil {
		sub.dispatchCancel()
	}

	// Wait for every dispatch goroutine — across any fetch-loop generation —
	// to exit before tearing down the NATS subscription and trigger handler.
	// Without this wait, in-flight goroutines could race with Unsubscribe
	// (msg.Ack on a closed subscription) and with handler.Cleanup (concurrent
	// h.filter.Filter vs h.filter.Cleanup). Bounded by ackWait via msgCtx;
	// resolves in milliseconds when the HTTP client respects ctx cancellation.
	sub.inflight.Wait()

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
