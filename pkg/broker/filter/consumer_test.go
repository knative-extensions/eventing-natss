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
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/logging"
)

type lifecycleRecorder struct {
	mu      sync.Mutex
	actions []string
}

func (r *lifecycleRecorder) record(action string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions = append(r.actions, action)
}

func (r *lifecycleRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.actions)
}

func (r *lifecycleRecorder) assertActions(t *testing.T, want ...string) {
	t.Helper()
	if got := r.snapshot(); !slices.Equal(got, want) {
		t.Errorf("lifecycle actions = %v, want %v", got, want)
	}
}

// withLifecycleState initializes the lifecycle bookkeeping NewConsumerManager
// creates, for tests that build a ConsumerManager literal.
func withLifecycleState(m *ConsumerManager) *ConsumerManager {
	if m.cancel == nil {
		parent := m.ctx
		if parent == nil {
			parent = context.Background()
		}
		m.ctx, m.cancel = context.WithCancel(parent)
	}
	if m.subscriptions == nil {
		m.subscriptions = make(map[string]*TriggerSubscription)
	}
	m.operations = make(map[string]chan struct{})
	m.shutdownStarted = make(chan struct{})
	m.shutdownDone = make(chan struct{})
	return m
}

type blockingPullSubscription struct {
	*shutdownPullSubscription
	fetchStarted  chan struct{}
	fetchCanceled chan error
	releaseFetch  chan struct{}
}

func (s *blockingPullSubscription) Fetch(ctx context.Context, _ int) ([]*nats.Msg, error) {
	s.recorder.record("fetch-start")
	close(s.fetchStarted)
	<-ctx.Done()
	s.recorder.record("fetch-context-canceled")
	s.fetchCanceled <- ctx.Err()
	<-s.releaseFetch
	s.recorder.record("fetch-return")
	return nil, ctx.Err()
}

type shutdownPullSubscription struct {
	recorder     *lifecycleRecorder
	unsubscribed chan struct{}
	once         sync.Once
}

func (*shutdownPullSubscription) Fetch(context.Context, int) ([]*nats.Msg, error) {
	return nil, nats.ErrTimeout
}

func (s *shutdownPullSubscription) Unsubscribe() error {
	s.recorder.record("unsubscribe")
	s.once.Do(func() { close(s.unsubscribed) })
	return nil
}

// newStoppedSubscription returns a subscription whose fetch loop has already
// stopped. Its pull subscription and filter record teardown into recorder.
func newStoppedSubscription(recorder *lifecycleRecorder) (*TriggerSubscription, *shutdownPullSubscription, *cleanupTrackingFilter) {
	done := make(chan struct{})
	close(done)
	pullSub := &shutdownPullSubscription{recorder: recorder, unsubscribed: make(chan struct{})}
	filter := &cleanupTrackingFilter{recorder: recorder, cleaned: make(chan struct{})}
	return &TriggerSubscription{
		subscription: pullSub,
		handler:      &TriggerHandler{config: &handlerConfig{filter: filter}},
		stop:         func() {},
		done:         done,
	}, pullSub, filter
}

// partialBatchPullSubscription models nats.go returning the messages a Fetch
// received before its context ended, with a nil error. Only the first Fetch
// returns messages; it waits for its context to end or for release.
type partialBatchPullSubscription struct {
	msgs         []*nats.Msg
	fetchStarted chan struct{}
	fetchEnded   chan error
	release      chan struct{}
	calls        atomic.Int32
	// fetchCtx is the first Fetch's context, set before fetchStarted closes.
	fetchCtx context.Context
}

func (s *partialBatchPullSubscription) Fetch(ctx context.Context, _ int) ([]*nats.Msg, error) {
	if s.calls.Add(1) > 1 {
		return nil, nats.ErrTimeout
	}
	s.fetchCtx = ctx
	close(s.fetchStarted)
	select {
	case <-ctx.Done():
		s.fetchEnded <- ctx.Err()
	case <-s.release:
	}
	return s.msgs, nil
}

func (*partialBatchPullSubscription) Unsubscribe() error {
	return nil
}

func TestConsumerManagerConfigDefaults(t *testing.T) {
	// Verify default values
	if DefaultFetchBatchSize != 10 {
		t.Errorf("DefaultFetchBatchSize = %v, want 10", DefaultFetchBatchSize)
	}

	if DefaultFetchTimeout != 200*time.Millisecond {
		t.Errorf("DefaultFetchTimeout = %v, want 200ms", DefaultFetchTimeout)
	}
}

func TestConsumerManagerConfig(t *testing.T) {
	tests := []struct {
		name               string
		config             *ConsumerManagerConfig
		wantFetchBatchSize int
		wantFetchTimeout   time.Duration
	}{
		{
			name:               "nil config uses defaults",
			config:             nil,
			wantFetchBatchSize: DefaultFetchBatchSize,
			wantFetchTimeout:   DefaultFetchTimeout,
		},
		{
			name:               "empty config uses defaults",
			config:             &ConsumerManagerConfig{},
			wantFetchBatchSize: DefaultFetchBatchSize,
			wantFetchTimeout:   DefaultFetchTimeout,
		},
		{
			name: "zero values use defaults",
			config: &ConsumerManagerConfig{
				FetchBatchSize: 0,
				FetchTimeout:   0,
			},
			wantFetchBatchSize: DefaultFetchBatchSize,
			wantFetchTimeout:   DefaultFetchTimeout,
		},
		{
			name: "custom batch size only",
			config: &ConsumerManagerConfig{
				FetchBatchSize: 20,
				FetchTimeout:   0,
			},
			wantFetchBatchSize: 20,
			wantFetchTimeout:   DefaultFetchTimeout,
		},
		{
			name: "custom timeout only",
			config: &ConsumerManagerConfig{
				FetchBatchSize: 0,
				FetchTimeout:   1 * time.Second,
			},
			wantFetchBatchSize: DefaultFetchBatchSize,
			wantFetchTimeout:   1 * time.Second,
		},
		{
			name: "both custom values",
			config: &ConsumerManagerConfig{
				FetchBatchSize: 50,
				FetchTimeout:   2 * time.Second,
			},
			wantFetchBatchSize: 50,
			wantFetchTimeout:   2 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We can't easily test NewConsumerManager without a real NATS connection,
			// so we test the config application logic directly
			fetchBatchSize := DefaultFetchBatchSize
			fetchTimeout := DefaultFetchTimeout

			if tt.config != nil {
				if tt.config.FetchBatchSize > 0 {
					fetchBatchSize = tt.config.FetchBatchSize
				}
				if tt.config.FetchTimeout > 0 {
					fetchTimeout = tt.config.FetchTimeout
				}
			}

			if fetchBatchSize != tt.wantFetchBatchSize {
				t.Errorf("fetchBatchSize = %v, want %v", fetchBatchSize, tt.wantFetchBatchSize)
			}

			if fetchTimeout != tt.wantFetchTimeout {
				t.Errorf("fetchTimeout = %v, want %v", fetchTimeout, tt.wantFetchTimeout)
			}
		})
	}
}

func TestGetSubscriptionCount(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

	tests := []struct {
		name  string
		count int
	}{
		{"empty map", 0},
		{"one entry", 1},
		{"three entries", 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cm := &ConsumerManager{
				logger:        logging.FromContext(ctx),
				subscriptions: make(map[string]*TriggerSubscription),
			}
			for i := 0; i < tc.count; i++ {
				uid := fmt.Sprintf("uid-%d", i)
				cm.subscriptions[uid] = &TriggerSubscription{}
			}
			if got := cm.GetSubscriptionCount(); got != tc.count {
				t.Errorf("GetSubscriptionCount() = %d, want %d", got, tc.count)
			}
		})
	}
}

func TestHasSubscription(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

	cm := &ConsumerManager{
		logger:        logging.FromContext(ctx),
		subscriptions: make(map[string]*TriggerSubscription),
	}
	cm.subscriptions["existing-uid"] = &TriggerSubscription{}

	if !cm.HasSubscription("existing-uid") {
		t.Error("HasSubscription() = false for existing UID, want true")
	}
	if cm.HasSubscription("missing-uid") {
		t.Error("HasSubscription() = true for missing UID, want false")
	}
}

func TestConsumerManagerClose(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

	cm := withLifecycleState(&ConsumerManager{logger: logging.FromContext(ctx)})

	err := cm.Close()
	if err != nil {
		t.Errorf("Close() unexpected error on empty subscriptions: %v", err)
	}
}

func TestUnsubscribeTrigger_NotFound(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), logging.FromContext(context.TODO()))

	cm := withLifecycleState(&ConsumerManager{logger: logging.FromContext(ctx)})

	err := cm.UnsubscribeTrigger("non-existent-uid")
	if err != nil {
		t.Errorf("UnsubscribeTrigger() unexpected error for non-existent UID: %v", err)
	}
}

// TestUnsubscribeTriggerWaitsForFetchLoopBeforeTeardown covers the lifecycle
// race where unsubscribe begins while Fetch is blocked and inflight is still
// zero. The fetch loop must be fully stopped before dispatch cancellation and
// Wait, otherwise a late inflight.Add can race with teardown.
func TestUnsubscribeTriggerWaitsForFetchLoopBeforeTeardown(t *testing.T) {
	ctx := logCtx()
	recorder := &lifecycleRecorder{}
	releaseFetch := make(chan struct{})
	releaseInflight := make(chan struct{})
	closeFetch := sync.OnceFunc(func() { close(releaseFetch) })
	closeInflight := sync.OnceFunc(func() { close(releaseInflight) })
	defer closeFetch()
	defer closeInflight()

	pullSub := &blockingPullSubscription{
		shutdownPullSubscription: &shutdownPullSubscription{recorder: recorder, unsubscribed: make(chan struct{})},
		fetchStarted:             make(chan struct{}),
		fetchCanceled:            make(chan error, 1),
		releaseFetch:             releaseFetch,
	}
	filter := &cleanupTrackingFilter{
		recorder: recorder,
		cleaned:  make(chan struct{}),
	}
	handler := &TriggerHandler{config: &handlerConfig{filter: filter}}

	dispatchCtx, cancelDispatch := context.WithCancel(ctx)
	defer cancelDispatch()
	dispatchCanceled := make(chan struct{})
	triggerUID := "lifecycle-trigger-uid"
	sub := &TriggerSubscription{
		trigger:        makeTriggerWithUID("default", "lifecycle-trigger", "", triggerUID),
		subscription:   pullSub,
		handler:        handler,
		fetchBatchSize: 1,
		fetchTimeout:   time.Hour,
		maxConcurrency: 1,
		dispatchCtx:    dispatchCtx,
		dispatchCancel: func() {
			recorder.record("dispatch-cancel")
			cancelDispatch()
			close(dispatchCanceled)
		},
	}
	manager := withLifecycleState(&ConsumerManager{
		logger: logging.FromContext(ctx),
		ctx:    ctx,
		subscriptions: map[string]*TriggerSubscription{
			triggerUID: sub,
		},
	})

	startFetchLoop(manager, sub)
	done := sub.done
	receiveWithin(t, pullSub.fetchStarted, "fetch loop did not enter Fetch")

	unsubscribeResult := make(chan error, 1)
	go func() {
		unsubscribeResult <- manager.UnsubscribeTrigger(triggerUID)
	}()

	if err := receiveWithin(t, pullSub.fetchCanceled, "Fetch context was not canceled by unsubscribe"); err != context.Canceled {
		t.Fatalf("Fetch context error = %v, want context.Canceled", err)
	}

	// Fetch has observed cancellation but deliberately has not returned, so the
	// fetch-loop done gate is still open. No teardown action may cross it.
	select {
	case <-done:
		t.Fatal("fetch loop reported done before blocked Fetch returned")
	default:
	}
	select {
	case <-dispatchCanceled:
		t.Fatal("dispatch context was canceled before fetch loop stopped")
	case <-pullSub.unsubscribed:
		t.Fatal("pull subscription was unsubscribed before fetch loop stopped")
	case <-filter.cleaned:
		t.Fatal("handler was cleaned before fetch loop stopped")
	case err := <-unsubscribeResult:
		t.Fatalf("UnsubscribeTrigger returned before fetch loop stopped: %v", err)
	default:
	}

	// Model a dispatch admitted by the fetch generation that is only now
	// finishing. The counter was zero when unsubscribe began; Add is safe here
	// because unsubscribe cannot start Wait until the fetch-loop done gate closes.
	sub.inflight.Add(1)
	recorder.record("inflight-add")
	dispatchObservedCancel := make(chan struct{})
	go func() {
		<-dispatchCtx.Done()
		recorder.record("dispatch-observed-cancel")
		close(dispatchObservedCancel)
		<-releaseInflight
		recorder.record("inflight-done")
		sub.inflight.Done()
	}()

	closeFetch()
	receiveWithin(t, done, "fetch loop did not stop after Fetch returned")
	receiveWithin(t, dispatchObservedCancel, "dispatch context was not canceled after fetch loop stopped")

	// Once fetch is done, unsubscribe cancels dispatches and waits for inflight
	// work. The pull subscription and handler must remain live during that wait.
	select {
	case <-pullSub.unsubscribed:
		t.Fatal("pull subscription was unsubscribed before inflight completed")
	case <-filter.cleaned:
		t.Fatal("handler was cleaned before inflight completed")
	case err := <-unsubscribeResult:
		t.Fatalf("UnsubscribeTrigger returned before inflight completed: %v", err)
	default:
	}

	closeInflight()
	if err := receiveWithin(t, unsubscribeResult, "UnsubscribeTrigger did not return after inflight completed"); err != nil {
		t.Fatalf("UnsubscribeTrigger() error = %v", err)
	}

	recorder.assertActions(t,
		"fetch-start",
		"fetch-context-canceled",
		"inflight-add",
		"fetch-return",
		"dispatch-cancel",
		"dispatch-observed-cancel",
		"inflight-done",
		"unsubscribe",
		"cleanup",
	)
}

func TestFetchLoopHandsOffMessagesFetchedBeforeStopping(t *testing.T) {
	for _, test := range []struct {
		name string
		// end runs while the loop's only Fetch is in progress.
		end func(stop, interrupt, cancelDispatches context.CancelFunc)
		// interruptsFetch reports whether end cancels the in-progress Fetch.
		interruptsFetch bool
		wantDispatched  bool
	}{{
		name:           "stop lets the Fetch finish",
		end:            func(stop, _, _ context.CancelFunc) { stop() },
		wantDispatched: true,
	}, {
		name:            "interrupt",
		end:             func(_, interrupt, _ context.CancelFunc) { interrupt() },
		interruptsFetch: true,
		wantDispatched:  true,
	}, {
		name:            "dispatch cancellation",
		end:             func(_, _, cancelDispatches context.CancelFunc) { cancelDispatches() },
		interruptsFetch: true,
	}} {
		t.Run(test.name, func(t *testing.T) {
			ctx := logCtx()
			subscriber := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			}))
			defer subscriber.Close()
			handler := newTestHandler(t, ctx, subscriber.URL, "")
			ids := []string{"event-1", "event-2", "event-3"}
			filter := &cleanupTrackingFilter{filtered: make(chan string, len(ids)), cleaned: make(chan struct{})}
			setTestFilter(handler, filter)
			pullSub := &partialBatchPullSubscription{
				fetchStarted: make(chan struct{}),
				fetchEnded:   make(chan error, 1),
				release:      make(chan struct{}),
			}
			for _, id := range ids {
				pullSub.msgs = append(pullSub.msgs, makeStructuredCEMsg("test.type", "test/source", id))
			}

			dispatchCtx, cancelDispatches := context.WithCancel(ctx)
			defer cancelDispatches()
			ts := &TriggerSubscription{
				subscription:   pullSub,
				handler:        handler,
				fetchBatchSize: len(ids),
				fetchTimeout:   time.Hour,
				maxConcurrency: len(ids),
				dispatchCtx:    dispatchCtx,
				dispatchCancel: cancelDispatches,
			}
			startFetchLoop(&ConsumerManager{logger: logging.FromContext(ctx)}, ts)
			receiveWithin(t, pullSub.fetchStarted, "fetch loop did not enter Fetch")

			test.end(ts.stop, ts.cancel, ts.dispatchCancel)
			if test.interruptsFetch {
				receiveWithin(t, pullSub.fetchEnded, "Fetch was not interrupted")
			} else {
				// Cancellation reaches every descendant context before the
				// CancelFunc returns, so this check needs no wait.
				if err := pullSub.fetchCtx.Err(); err != nil {
					t.Fatalf("stop interrupted the in-progress Fetch: %v", err)
				}
				close(pullSub.release)
			}
			receiveWithin(t, ts.done, "fetch loop did not stop after Fetch returned")
			ts.inflight.Wait()
			close(filter.filtered)

			var dispatched []string
			for id := range filter.filtered {
				dispatched = append(dispatched, id)
			}
			slices.Sort(dispatched)
			var want []string
			if test.wantDispatched {
				want = ids
			}
			if !slices.Equal(dispatched, want) {
				t.Errorf("dispatched events = %v, want %v", dispatched, want)
			}
			if calls := pullSub.calls.Load(); calls != 1 {
				t.Errorf("Fetch calls = %d, want 1", calls)
			}
		})
	}
}

func TestConsumerManagerShutdownNaturallyDrainsAndIsIdempotent(t *testing.T) {
	ctx := logCtx()
	recorder := &lifecycleRecorder{}
	producerStopped := make(chan struct{})
	releaseInflight := make(chan struct{})
	sub, _, _ := newStoppedSubscription(recorder)
	sub.stop = func() {
		recorder.record("fetch-stop")
		close(producerStopped)
	}
	sub.inflight.Add(1)
	go func() {
		<-releaseInflight
		recorder.record("inflight-done")
		sub.inflight.Done()
	}()
	manager := withLifecycleState(&ConsumerManager{
		logger: logging.FromContext(ctx),
		// The manager's run context parents every dispatch context.
		cancel:        func() { recorder.record("run-cancel") },
		subscriptions: map[string]*TriggerSubscription{"uid": sub},
	})

	const callers = 20
	results := make(chan error, callers)
	go func() { results <- manager.Shutdown(context.Background()) }()
	receiveWithin(t, producerStopped, "Shutdown did not stop the fetch producer")
	for range callers - 1 {
		go func() { results <- manager.Shutdown(context.Background()) }()
	}

	// Natural drain waits without canceling dispatches or tearing resources down.
	recorder.assertActions(t, "fetch-stop")

	close(releaseInflight)
	for range callers {
		if err := receiveWithin(t, results, "concurrent Shutdown caller did not return"); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	}
	if got := manager.GetSubscriptionCount(); got != 0 {
		t.Errorf("subscription count = %d, want 0", got)
	}
	wantActions := []string{"fetch-stop", "inflight-done", "unsubscribe", "cleanup", "run-cancel"}
	recorder.assertActions(t, wantActions...)
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Errorf("repeated Shutdown() error = %v", err)
	}
	recorder.assertActions(t, wantActions...)
}

func TestConsumerManagerShutdownTimeoutIsStableAndIdempotent(t *testing.T) {
	ctx := logCtx()
	recorder := &lifecycleRecorder{}
	sub, pullSub, filter := newStoppedSubscription(recorder)
	sub.inflight.Add(1) // Deliberately ignores dispatch cancellation until after timeout.
	manager := withLifecycleState(&ConsumerManager{
		logger: logging.FromContext(ctx),
		// The manager's run context parents every dispatch context.
		cancel:        sync.OnceFunc(func() { recorder.record("dispatch-cancel") }),
		subscriptions: map[string]*TriggerSubscription{"uid": sub},
	})

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := manager.Shutdown(shutdownCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want context deadline exceeded", err)
	}
	recorder.assertActions(t, "dispatch-cancel")
	if err := manager.Shutdown(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("repeated Shutdown() error = %v, want stable deadline error", err)
	}
	recorder.assertActions(t, "dispatch-cancel")

	// The caller returned promptly, but ownership is retained until the
	// dispatch eventually exits; teardown must then complete exactly once.
	sub.inflight.Done()
	receiveWithin(t, pullSub.unsubscribed, "eventual cleanup did not unsubscribe after inflight completed")
	receiveWithin(t, filter.cleaned, "eventual cleanup did not clean handler after inflight completed")
	recorder.assertActions(t, "dispatch-cancel", "unsubscribe", "cleanup")
	if got := manager.GetSubscriptionCount(); got != 0 {
		t.Errorf("subscription count after eventual cleanup = %d, want 0", got)
	}
}

func TestConsumerManagerForcedStopCancelsThenDrains(t *testing.T) {
	for _, test := range []struct {
		name string
		stop func(*ConsumerManager) error
	}{{
		name: "Shutdown deadline",
		stop: func(m *ConsumerManager) error {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			return m.Shutdown(ctx)
		},
	}, {
		name: "Close",
		stop: (*ConsumerManager).Close,
	}} {
		t.Run(test.name, func(t *testing.T) {
			ctx := logCtx()
			recorder := &lifecycleRecorder{}
			dispatchCtx, cancelDispatch := context.WithCancel(context.Background())
			sub, _, _ := newStoppedSubscription(recorder)
			sub.inflight.Add(1)
			go func() {
				<-dispatchCtx.Done()
				recorder.record("dispatch-observed-cancel")
				sub.inflight.Done()
			}()
			manager := withLifecycleState(&ConsumerManager{
				logger: logging.FromContext(ctx),
				// The manager's run context parents every dispatch context.
				cancel: sync.OnceFunc(func() {
					recorder.record("dispatch-cancel")
					cancelDispatch()
				}),
				subscriptions: map[string]*TriggerSubscription{"uid": sub},
			})

			if err := test.stop(manager); err != nil {
				t.Fatalf("%s error = %v", test.name, err)
			}
			recorder.assertActions(t, "dispatch-cancel", "dispatch-observed-cancel", "unsubscribe", "cleanup")
		})
	}
}

func TestConsumerManagerShutdownStopsAllProducersBeforeWaiting(t *testing.T) {
	ctx := logCtx()
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	firstStopped := make(chan struct{})
	secondStopped := make(chan struct{})
	newSubscription := func(done chan struct{}, stopped chan struct{}) *TriggerSubscription {
		sub, _, _ := newStoppedSubscription(&lifecycleRecorder{})
		sub.done = done
		sub.stop = func() { close(stopped) }
		return sub
	}
	subscriptions := []*TriggerSubscription{
		newSubscription(firstDone, firstStopped),
		newSubscription(secondDone, secondStopped),
	}
	manager := withLifecycleState(&ConsumerManager{logger: logging.FromContext(ctx)})
	result := make(chan error, 1)
	go func() { result <- manager.shutdown(context.Background(), subscriptions, nil) }()

	receiveWithin(t, firstStopped, "first fetch producer was not stopped")
	receiveWithin(t, secondStopped, "second fetch producer was not stopped before waiting for first done")
	close(firstDone)
	close(secondDone)
	if err := receiveWithin(t, result, "shutdown did not complete after producers stopped"); err != nil {
		t.Fatalf("shutdown() error = %v", err)
	}
}

func TestConsumerManagerShutdownRejectsNewLifecycleOperations(t *testing.T) {
	ctx := logCtx()
	manager := withLifecycleState(&ConsumerManager{logger: logging.FromContext(ctx)})
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := manager.UnsubscribeTrigger("missing"); err != ErrConsumerManagerClosed {
		t.Errorf("UnsubscribeTrigger after shutdown = %v, want %v", err, ErrConsumerManagerClosed)
	}
	trigger := makeTriggerWithUID("default", "trigger", "broker", "uid")
	if err := manager.SubscribeTrigger(trigger, nil, duckv1.Addressable{}, nil, nil, nil, nil); err != ErrConsumerManagerClosed {
		t.Errorf("SubscribeTrigger after shutdown = %v, want %v", err, ErrConsumerManagerClosed)
	}
}

func TestRuntimeShutdownDuringTriggerDeletion(t *testing.T) {
	for _, blocked := range []string{"fetch", "dispatch"} {
		t.Run(blocked, func(t *testing.T) {
			ctx := logCtx()
			recorder := &lifecycleRecorder{}
			sub, _, _ := newStoppedSubscription(recorder)
			fetchCtx, cancelFetch := context.WithCancel(ctx)
			dispatchCtx, cancelDispatch := context.WithCancel(ctx)
			defer cancelFetch()
			defer cancelDispatch()
			producerDone := make(chan struct{})
			var release func()
			if blocked == "fetch" {
				release = func() { close(producerDone) }
			} else {
				close(producerDone)
				sub.inflight.Add(1)
				release = sub.inflight.Done
			}
			release = sync.OnceFunc(release)
			defer release()
			sub.trigger = makeTriggerWithUID("default", "deleted-trigger", "", "deleted")
			sub.cancel = cancelFetch
			sub.done = producerDone
			sub.dispatchCancel = cancelDispatch

			otherStopCtx, stopOther := context.WithCancel(ctx)
			defer stopOther()
			other, _, _ := newStoppedSubscription(&lifecycleRecorder{})
			other.stop = stopOther
			manager := withLifecycleState(&ConsumerManager{
				logger:        logging.FromContext(ctx),
				subscriptions: map[string]*TriggerSubscription{"deleted": sub, "other": other},
			})
			unsubscribeResult := make(chan error, 1)
			go func() { unsubscribeResult <- manager.UnsubscribeTrigger("deleted") }()
			receiveWithin(t, fetchCtx.Done(), "timed out waiting for deleted trigger's fetch cancellation")
			if blocked == "dispatch" {
				receiveWithin(t, dispatchCtx.Done(), "timed out waiting for deleted trigger's dispatch cancellation")
			}
			queuedResult := make(chan error, 1)
			go func() { queuedResult <- manager.UnsubscribeTrigger("deleted") }()
			select {
			case err := <-queuedResult:
				t.Fatalf("second deletion returned while the first was blocked: %v", err)
			case <-time.After(20 * time.Millisecond):
			}

			conn := &runtimeNATSConnection{status: nats.CONNECTED, closeOnDrain: true}
			shutdownRuntimePastDeadline(t, manager, conn, 100*time.Millisecond)
			receiveWithin(t, otherStopCtx.Done(), "timed out waiting for other trigger's fetch stop")
			if got := conn.Status(); got != nats.CLOSED {
				t.Fatalf("NATS status = %s, want CLOSED", got)
			}
			// The deleted subscription is not finalized while its work is still running.
			recorder.assertActions(t)

			// A caller queued behind deletion must reject shutdown immediately,
			// without waiting for the deleted trigger's blocked work.
			if err := receiveWithin(t, queuedResult, "queued deletion waited for the blocked deletion after shutdown"); !errors.Is(err, ErrConsumerManagerClosed) {
				t.Fatalf("queued deletion error = %v, want manager closed", err)
			}

			release()
			if err := <-unsubscribeResult; err != nil {
				t.Fatalf("UnsubscribeTrigger() error = %v", err)
			}
			waitForConsumerCleanup(t, manager)
			recorder.assertActions(t, "unsubscribe", "cleanup")
		})
	}
}

func TestRuntimeShutdownDuringTriggerUpdate(t *testing.T) {
	for _, blocked := range []string{"filter", "fetch restart"} {
		t.Run(blocked, func(t *testing.T) {
			ctx := logCtx()
			handler := newTestHandler(t, ctx, "http://localhost:9999", "")
			recorder := &lifecycleRecorder{}
			pullSub := &shutdownPullSubscription{recorder: recorder, unsubscribed: make(chan struct{})}
			producerDone := make(chan struct{})
			fetchCtx, cancelFetch := context.WithCancel(ctx)
			dispatchCtx, cancelDispatch := context.WithCancel(ctx)
			defer cancelFetch()
			defer cancelDispatch()
			sub := &TriggerSubscription{
				subscription:   pullSub,
				handler:        handler,
				fetchBatchSize: 1,
				fetchTimeout:   time.Hour,
				maxConcurrency: 1,
				stop:           func() {},
				cancel:         cancelFetch,
				done:           producerDone,
				dispatchCtx:    dispatchCtx,
				dispatchCancel: cancelDispatch,
			}
			trigger := makeTriggerWithUID("default", "trigger", "broker", "uid")
			var release func()
			if blocked == "filter" {
				close(producerDone)
				releaseFilter := make(chan struct{})
				filter := &cleanupTrackingFilter{filtered: make(chan string, 1), release: releaseFilter, cleaned: make(chan struct{})}
				setTestFilter(handler, filter)
				sub.inflight.Add(1)
				go func() {
					defer sub.inflight.Done()
					handler.HandleMessage(dispatchCtx, makeStructuredCEMsg("test.type", "test/source", "blocked-event"))
				}()
				receiveWithin(t, filter.filtered, "dispatch did not reach the filter")
				release = func() { close(releaseFilter) }
			} else {
				trigger.Annotations = map[string]string{TriggerFetchBatchSizeAnnotation: "2"}
				release = func() { close(producerDone) }
			}
			release = sync.OnceFunc(release)
			defer release()
			manager := withLifecycleState(&ConsumerManager{
				logger: logging.FromContext(ctx),
				ctx:    ctx,
				// The manager's run context parents every dispatch context.
				cancel:                cancelDispatch,
				fetchBatchSize:        1,
				fetchTimeout:          time.Hour,
				defaultMaxConcurrency: 1,
				subscriptions:         map[string]*TriggerSubscription{"uid": sub},
			})
			updateResult := make(chan error, 1)
			go func() {
				updateResult <- manager.SubscribeTrigger(trigger, nil, handler.config.subscriber, nil, nil, nil, nil)
			}()
			if blocked == "fetch restart" {
				receiveWithin(t, fetchCtx.Done(), "timed out waiting for fetch restart cancellation")
			} else {
				require.Eventually(t, func() bool {
					manager.mu.RLock()
					defer manager.mu.RUnlock()
					return manager.operations["uid"] != nil
				}, 5*time.Second, time.Millisecond, "trigger update did not start")
			}

			conn := &runtimeNATSConnection{status: nats.CONNECTED, closeOnDrain: true}
			shutdownRuntimePastDeadline(t, manager, conn, 100*time.Millisecond)
			if got := conn.Status(); got != nats.CLOSED {
				t.Fatalf("NATS status = %s, want CLOSED", got)
			}
			// The subscription is not finalized before its update and work stop.
			recorder.assertActions(t)

			release()
			if err := <-updateResult; blocked == "fetch restart" && !errors.Is(err, ErrConsumerManagerClosed) {
				t.Fatalf("fetch restart error = %v, want manager closed", err)
			}
			waitForConsumerCleanup(t, manager)
			if sub.done != producerDone {
				t.Fatal("trigger update restarted fetching after shutdown began")
			}
			handler.configMu.RLock()
			config := handler.config
			handler.configMu.RUnlock()
			if config != nil {
				t.Fatal("trigger update restored handler configuration after cleanup")
			}
			recorder.assertActions(t, "unsubscribe")
		})
	}
}

// startFetchLoop starts ts's fetch loop the way SubscribeTrigger does.
func startFetchLoop(m *ConsumerManager, ts *TriggerSubscription) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startFetchLoopLocked(ts, m.logger)
}

// receiveWithin returns the next value from ch, failing the test with failure
// if none arrives within five seconds.
func receiveWithin[T any](t *testing.T, ch <-chan T, failure string) T {
	t.Helper()
	var v T
	select {
	case v = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal(failure)
	}
	return v
}

func waitForConsumerCleanup(t *testing.T, manager *ConsumerManager) {
	t.Helper()
	require.Eventually(t, func() bool {
		manager.mu.RLock()
		defer manager.mu.RUnlock()
		return len(manager.subscriptions)+len(manager.operations) == 0
	}, 5*time.Second, time.Millisecond, "consumer resources remained after blocked lifecycle work finished")
}

func TestDefaultMaxConcurrency(t *testing.T) {
	if DefaultMaxConcurrency != 20 {
		t.Errorf("DefaultMaxConcurrency = %v, want 20", DefaultMaxConcurrency)
	}
}

func TestAnnotationConstants(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "TriggerMaxConcurrencyAnnotation",
			got:  TriggerMaxConcurrencyAnnotation,
			want: "natsjetstream.eventing.knative.dev/max-concurrency",
		},
		{
			name: "TriggerFetchBatchSizeAnnotation",
			got:  TriggerFetchBatchSizeAnnotation,
			want: "natsjetstream.eventing.knative.dev/fetch-batch-size",
		},
		{
			name: "TriggerFetchTimeoutAnnotation",
			got:  TriggerFetchTimeoutAnnotation,
			want: "natsjetstream.eventing.knative.dev/fetch-timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestConsumerManagerConfig_MaxConcurrency(t *testing.T) {
	tests := []struct {
		name               string
		config             *ConsumerManagerConfig
		wantMaxConcurrency int
	}{
		{
			name:               "nil config uses default",
			config:             nil,
			wantMaxConcurrency: DefaultMaxConcurrency,
		},
		{
			name:               "zero MaxConcurrency uses default",
			config:             &ConsumerManagerConfig{MaxConcurrency: 0},
			wantMaxConcurrency: DefaultMaxConcurrency,
		},
		{
			name:               "positive MaxConcurrency is used",
			config:             &ConsumerManagerConfig{MaxConcurrency: 50},
			wantMaxConcurrency: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			maxConcurrency := DefaultMaxConcurrency

			if tt.config != nil {
				if tt.config.MaxConcurrency > 0 {
					maxConcurrency = tt.config.MaxConcurrency
				}
			}

			if maxConcurrency != tt.wantMaxConcurrency {
				t.Errorf("maxConcurrency = %v, want %v", maxConcurrency, tt.wantMaxConcurrency)
			}
		})
	}
}

func TestParseTriggerAnnotationInt(t *testing.T) {
	logger := zap.NewNop().Sugar()

	tests := []struct {
		name        string
		annotations map[string]string
		key         string
		defaultVal  int
		want        int
	}{
		{
			name:        "absent key returns default",
			annotations: map[string]string{},
			key:         "some-key",
			defaultVal:  10,
			want:        10,
		},
		{
			name:        "empty string returns default",
			annotations: map[string]string{"some-key": ""},
			key:         "some-key",
			defaultVal:  10,
			want:        10,
		},
		{
			name:        "valid positive int is parsed",
			annotations: map[string]string{"some-key": "42"},
			key:         "some-key",
			defaultVal:  10,
			want:        42,
		},
		{
			name:        "zero returns default",
			annotations: map[string]string{"some-key": "0"},
			key:         "some-key",
			defaultVal:  10,
			want:        10,
		},
		{
			name:        "negative returns default",
			annotations: map[string]string{"some-key": "-5"},
			key:         "some-key",
			defaultVal:  10,
			want:        10,
		},
		{
			name:        "non-numeric returns default",
			annotations: map[string]string{"some-key": "abc"},
			key:         "some-key",
			defaultVal:  10,
			want:        10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTriggerAnnotationInt(tt.annotations, tt.key, tt.defaultVal, logger)
			if got != tt.want {
				t.Errorf("parseTriggerAnnotationInt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseTriggerAnnotationDuration(t *testing.T) {
	logger := zap.NewNop().Sugar()

	tests := []struct {
		name        string
		annotations map[string]string
		key         string
		defaultVal  time.Duration
		want        time.Duration
	}{
		{
			name:        "absent key returns default",
			annotations: map[string]string{},
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        200 * time.Millisecond,
		},
		{
			name:        "empty string returns default",
			annotations: map[string]string{"some-key": ""},
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        200 * time.Millisecond,
		},
		{
			name:        "valid duration is parsed",
			annotations: map[string]string{"some-key": "500ms"},
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        500 * time.Millisecond,
		},
		{
			name:        "zero duration returns default",
			annotations: map[string]string{"some-key": "0s"},
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        200 * time.Millisecond,
		},
		{
			name:        "negative duration returns default",
			annotations: map[string]string{"some-key": "-1s"},
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        200 * time.Millisecond,
		},
		{
			name:        "non-duration string returns default",
			annotations: map[string]string{"some-key": "abc"},
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        200 * time.Millisecond,
		},
		{
			name:        "nil annotations map returns default without panic",
			annotations: nil,
			key:         "some-key",
			defaultVal:  200 * time.Millisecond,
			want:        200 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTriggerAnnotationDuration(tt.annotations, tt.key, tt.defaultVal, logger)
			if got != tt.want {
				t.Errorf("parseTriggerAnnotationDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDynamicBatchSizeCapping(t *testing.T) {
	tests := []struct {
		name           string
		capacity       int
		occupied       int
		fetchBatchSize int
		wantBatchSize  int
	}{
		{
			name:           "all free and batchSize fits within capacity",
			capacity:       20,
			occupied:       0,
			fetchBatchSize: 10,
			wantBatchSize:  10,
		},
		{
			name:           "all free but batchSize exceeds capacity",
			capacity:       5,
			occupied:       0,
			fetchBatchSize: 10,
			wantBatchSize:  5,
		},
		{
			name:           "partially occupied and batchSize fits within available",
			capacity:       20,
			occupied:       5,
			fetchBatchSize: 10,
			wantBatchSize:  10,
		},
		{
			name:           "partially occupied and batchSize exceeds available",
			capacity:       20,
			occupied:       15,
			fetchBatchSize: 10,
			wantBatchSize:  5,
		},
		{
			name:           "one slot free",
			capacity:       20,
			occupied:       19,
			fetchBatchSize: 10,
			wantBatchSize:  1,
		},
		{
			name:           "all occupied returns zero",
			capacity:       20,
			occupied:       20,
			fetchBatchSize: 10,
			wantBatchSize:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sem := make(chan struct{}, tt.capacity)
			for i := 0; i < tt.occupied; i++ {
				sem <- struct{}{}
			}

			available := cap(sem) - len(sem)
			batchSize := tt.fetchBatchSize
			if available < batchSize {
				batchSize = available
			}

			if batchSize != tt.wantBatchSize {
				t.Errorf("batchSize = %v, want %v (available=%v, fetchBatchSize=%v)",
					batchSize, tt.wantBatchSize, available, tt.fetchBatchSize)
			}
		})
	}
}
