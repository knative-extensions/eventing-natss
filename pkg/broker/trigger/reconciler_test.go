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
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	logtesting "knative.dev/pkg/logging/testing"

	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"

	"knative.dev/eventing-natss/pkg/broker/constants"
)

const (
	testFilterServiceName = "natsjs-broker-filter"
)

// mockJetStreamContext implements nats.JetStreamContext for testing
type mockJetStreamContext struct {
	consumers       map[string]map[string]*nats.ConsumerInfo
	addConsumerErr  error
	updateConsumer  error
	deleteConsumer  error
	consumerInfoErr error
}

func newMockJetStreamContext() *mockJetStreamContext {
	return &mockJetStreamContext{
		consumers: make(map[string]map[string]*nats.ConsumerInfo),
	}
}

func (m *mockJetStreamContext) setAddConsumerError(err error) {
	m.addConsumerErr = err
}

func (m *mockJetStreamContext) setDeleteConsumerError(err error) {
	m.deleteConsumer = err
}

func (m *mockJetStreamContext) setConsumerInfoError(err error) {
	m.consumerInfoErr = err
}

func (m *mockJetStreamContext) addConsumerInfo(streamName, consumerName string) {
	if m.consumers[streamName] == nil {
		m.consumers[streamName] = make(map[string]*nats.ConsumerInfo)
	}
	m.consumers[streamName][consumerName] = &nats.ConsumerInfo{
		Name:   consumerName,
		Stream: streamName,
	}
}

// JetStreamContext interface implementation
func (m *mockJetStreamContext) ConsumerInfo(stream, consumer string, opts ...nats.JSOpt) (*nats.ConsumerInfo, error) {
	if m.consumerInfoErr != nil {
		return nil, m.consumerInfoErr
	}
	if m.consumers[stream] == nil {
		return nil, nats.ErrConsumerNotFound
	}
	if info, ok := m.consumers[stream][consumer]; ok {
		return info, nil
	}
	return nil, nats.ErrConsumerNotFound
}

func (m *mockJetStreamContext) AddConsumer(stream string, cfg *nats.ConsumerConfig, opts ...nats.JSOpt) (*nats.ConsumerInfo, error) {
	if m.addConsumerErr != nil {
		return nil, m.addConsumerErr
	}
	if m.consumers[stream] == nil {
		m.consumers[stream] = make(map[string]*nats.ConsumerInfo)
	}
	info := &nats.ConsumerInfo{
		Name:   cfg.Name,
		Stream: stream,
		Config: *cfg,
	}
	m.consumers[stream][cfg.Name] = info
	return info, nil
}

func (m *mockJetStreamContext) UpdateConsumer(stream string, cfg *nats.ConsumerConfig, opts ...nats.JSOpt) (*nats.ConsumerInfo, error) {
	if m.updateConsumer != nil {
		return nil, m.updateConsumer
	}
	if m.consumers[stream] == nil || m.consumers[stream][cfg.Name] == nil {
		return nil, nats.ErrConsumerNotFound
	}
	info := &nats.ConsumerInfo{
		Name:   cfg.Name,
		Stream: stream,
		Config: *cfg,
	}
	m.consumers[stream][cfg.Name] = info
	return info, nil
}

func (m *mockJetStreamContext) DeleteConsumer(stream, consumer string, opts ...nats.JSOpt) error {
	if m.deleteConsumer != nil {
		return m.deleteConsumer
	}
	if m.consumers[stream] == nil || m.consumers[stream][consumer] == nil {
		return nats.ErrConsumerNotFound
	}
	delete(m.consumers[stream], consumer)
	return nil
}

// JetStream interface methods
func (m *mockJetStreamContext) Publish(subj string, data []byte, opts ...nats.PubOpt) (*nats.PubAck, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) PublishMsg(msg *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) PublishAsync(subj string, data []byte, opts ...nats.PubOpt) (nats.PubAckFuture, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) PublishMsgAsync(msg *nats.Msg, opts ...nats.PubOpt) (nats.PubAckFuture, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) PublishAsyncPending() int { return 0 }
func (m *mockJetStreamContext) PublishAsyncComplete() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
func (m *mockJetStreamContext) CleanupPublisher() {}
func (m *mockJetStreamContext) Subscribe(subj string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) SubscribeSync(subj string, opts ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) ChanSubscribe(subj string, ch chan *nats.Msg, opts ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) ChanQueueSubscribe(subj, queue string, ch chan *nats.Msg, opts ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) QueueSubscribe(subj, queue string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) QueueSubscribeSync(subj, queue string, opts ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) PullSubscribe(subj, durable string, opts ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, errors.New("not implemented")
}

// JetStreamManager interface methods
func (m *mockJetStreamContext) AddStream(cfg *nats.StreamConfig, opts ...nats.JSOpt) (*nats.StreamInfo, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) UpdateStream(cfg *nats.StreamConfig, opts ...nats.JSOpt) (*nats.StreamInfo, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) DeleteStream(name string, opts ...nats.JSOpt) error {
	return errors.New("not implemented")
}
func (m *mockJetStreamContext) StreamInfo(stream string, opts ...nats.JSOpt) (*nats.StreamInfo, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) PurgeStream(name string, opts ...nats.JSOpt) error {
	return errors.New("not implemented")
}
func (m *mockJetStreamContext) StreamsInfo(opts ...nats.JSOpt) <-chan *nats.StreamInfo {
	return nil
}
func (m *mockJetStreamContext) Streams(opts ...nats.JSOpt) <-chan *nats.StreamInfo { return nil }
func (m *mockJetStreamContext) StreamNames(opts ...nats.JSOpt) <-chan string       { return nil }
func (m *mockJetStreamContext) ConsumersInfo(stream string, opts ...nats.JSOpt) <-chan *nats.ConsumerInfo {
	return nil
}
func (m *mockJetStreamContext) Consumers(stream string, opts ...nats.JSOpt) <-chan *nats.ConsumerInfo {
	return nil
}
func (m *mockJetStreamContext) ConsumerNames(stream string, opts ...nats.JSOpt) <-chan string {
	return nil
}
func (m *mockJetStreamContext) AccountInfo(opts ...nats.JSOpt) (*nats.AccountInfo, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) GetMsg(name string, seq uint64, opts ...nats.JSOpt) (*nats.RawStreamMsg, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) GetLastMsg(name, subject string, opts ...nats.JSOpt) (*nats.RawStreamMsg, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) DeleteMsg(name string, seq uint64, opts ...nats.JSOpt) error {
	return errors.New("not implemented")
}
func (m *mockJetStreamContext) SecureDeleteMsg(name string, seq uint64, opts ...nats.JSOpt) error {
	return errors.New("not implemented")
}
func (m *mockJetStreamContext) StreamNameBySubject(subject string, opts ...nats.JSOpt) (string, error) {
	return "", errors.New("not implemented")
}

// KeyValueManager interface methods
func (m *mockJetStreamContext) KeyValue(bucket string) (nats.KeyValue, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) CreateKeyValue(cfg *nats.KeyValueConfig) (nats.KeyValue, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) DeleteKeyValue(bucket string) error {
	return errors.New("not implemented")
}
func (m *mockJetStreamContext) KeyValueStoreNames() <-chan string { return nil }
func (m *mockJetStreamContext) KeyValueStores() <-chan nats.KeyValueStatus {
	return nil
}

// ObjectStoreManager interface methods
func (m *mockJetStreamContext) ObjectStore(bucket string) (nats.ObjectStore, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) CreateObjectStore(cfg *nats.ObjectStoreConfig) (nats.ObjectStore, error) {
	return nil, errors.New("not implemented")
}
func (m *mockJetStreamContext) DeleteObjectStore(bucket string) error {
	return errors.New("not implemented")
}
func (m *mockJetStreamContext) ObjectStoreNames(opts ...nats.ObjectOpt) <-chan string { return nil }
func (m *mockJetStreamContext) ObjectStores(opts ...nats.ObjectOpt) <-chan nats.ObjectStoreStatus {
	return nil
}

// Ensure mockJetStreamContext implements nats.JetStreamContext
var _ nats.JetStreamContext = (*mockJetStreamContext)(nil)

func newReconcilerForTest(brokerLister *fakeBrokerLister, js nats.JetStreamContext) *Reconciler {
	return &Reconciler{
		brokerLister:      brokerLister,
		js:                js,
		filterServiceName: testFilterServiceName,
	}
}

func newReadyBroker(namespace, name string) *eventingv1.Broker {
	broker := newTestBroker(namespace, name, constants.BrokerClassName)
	broker.Status.InitializeConditions()
	// Set address to make broker ready
	broker.Status.SetAddress(&duckv1.Addressable{
		URL: &apis.URL{
			Scheme: "http",
			Host:   "broker-ingress.knative-eventing.svc.cluster.local",
		},
	})
	return broker
}

func newTriggerWithSubscriber(namespace, name, brokerName string, subscriberURI string) *eventingv1.Trigger {
	trigger := newTestTrigger(namespace, name, brokerName)
	trigger.UID = types.UID(testTriggerUID)
	trigger.Spec.Subscriber = duckv1.Destination{
		URI: apis.HTTP(subscriberURI),
	}
	trigger.Status.InitializeConditions()
	return trigger
}

func testContextWithRecorder(t *testing.T) context.Context {
	ctx := logging.WithLogger(context.Background(), logtesting.TestLogger(t))
	recorder := record.NewFakeRecorder(10)
	return controller.WithEventRecorder(ctx, recorder)
}

func TestReconcileKind_BrokerNotFound(t *testing.T) {
	ctx := testContextWithRecorder(t)
	brokerLister := newFakeBrokerLister()
	js := newMockJetStreamContext()

	r := newReconcilerForTest(brokerLister, js)

	trigger := newTriggerWithSubscriber(testNamespace, testTriggerName, "non-existent-broker", "subscriber.example.com")

	err := r.ReconcileKind(ctx, trigger)

	if err != nil {
		t.Errorf("ReconcileKind() returned error = %v, want nil (soft failure)", err)
	}

	// Check that the trigger status reflects the broker not found
	cond := trigger.Status.GetCondition(eventingv1.TriggerConditionBroker)
	if cond == nil || cond.Status != corev1.ConditionFalse {
		t.Errorf("Expected TriggerConditionBroker to be False, got %+v", cond)
	}
}

func TestReconcileKind_BrokerClassMismatch(t *testing.T) {
	ctx := testContextWithRecorder(t)
	brokerLister := newFakeBrokerLister()
	js := newMockJetStreamContext()

	// Add broker with different class
	broker := newTestBroker(testNamespace, testBrokerName, "DifferentBrokerClass")
	brokerLister.addBroker(broker)

	r := newReconcilerForTest(brokerLister, js)

	trigger := newTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "subscriber.example.com")

	err := r.ReconcileKind(ctx, trigger)

	if err != nil {
		t.Errorf("ReconcileKind() returned error = %v, want nil (soft failure)", err)
	}

	// Check that the trigger status reflects the class mismatch
	cond := trigger.Status.GetCondition(eventingv1.TriggerConditionBroker)
	if cond == nil || cond.Status != corev1.ConditionFalse {
		t.Errorf("Expected TriggerConditionBroker to be False, got %+v", cond)
	}
}

func TestReconcileKind_BrokerNotReady(t *testing.T) {
	ctx := testContextWithRecorder(t)
	brokerLister := newFakeBrokerLister()
	js := newMockJetStreamContext()

	// Add broker that is not ready
	broker := newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName)
	broker.Status.InitializeConditions()
	// Not setting address - broker not ready
	brokerLister.addBroker(broker)

	r := newReconcilerForTest(brokerLister, js)

	trigger := newTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "subscriber.example.com")

	err := r.ReconcileKind(ctx, trigger)

	if err != nil {
		t.Errorf("ReconcileKind() returned error = %v, want nil (soft failure)", err)
	}

	// Check that the trigger status reflects the broker not ready
	cond := trigger.Status.GetCondition(eventingv1.TriggerConditionBroker)
	if cond == nil || cond.Status != corev1.ConditionFalse {
		t.Errorf("Expected TriggerConditionBroker to be False, got %+v", cond)
	}
}

func TestFinalizeKind_BrokerNotFound(t *testing.T) {
	ctx := testContextWithRecorder(t)
	brokerLister := newFakeBrokerLister()
	js := newMockJetStreamContext()

	r := newReconcilerForTest(brokerLister, js)

	trigger := newTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "subscriber.example.com")

	err := r.FinalizeKind(ctx, trigger)

	if err != nil {
		t.Errorf("FinalizeKind() returned error = %v, want nil when broker not found", err)
	}
}

func TestFinalizeKind_ConsumerDeleteError(t *testing.T) {
	ctx := testContextWithRecorder(t)
	brokerLister := newFakeBrokerLister()
	js := newMockJetStreamContext()

	broker := newReadyBroker(testNamespace, testBrokerName)
	brokerLister.addBroker(broker)

	// Set delete consumer to fail
	js.setDeleteConsumerError(errors.New("delete failed"))

	r := newReconcilerForTest(brokerLister, js)

	trigger := newTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "subscriber.example.com")

	err := r.FinalizeKind(ctx, trigger)

	if err == nil {
		t.Errorf("FinalizeKind() returned nil, want error when delete fails")
	}
}

func TestFinalizeKind_ConsumerNotFoundOK(t *testing.T) {
	ctx := testContextWithRecorder(t)
	brokerLister := newFakeBrokerLister()
	js := newMockJetStreamContext()

	broker := newReadyBroker(testNamespace, testBrokerName)
	brokerLister.addBroker(broker)

	// Consumer doesn't exist - should be OK
	r := newReconcilerForTest(brokerLister, js)

	trigger := newTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "subscriber.example.com")

	err := r.FinalizeKind(ctx, trigger)

	if err != nil {
		t.Errorf("FinalizeKind() returned error = %v, want nil when consumer not found", err)
	}
}

func TestBuildConsumerConfig(t *testing.T) {
	tests := []struct {
		name           string
		trigger        *eventingv1.Trigger
		broker         *eventingv1.Broker
		consumerName   string
		wantAckWait    time.Duration
		wantMaxDeliver int
		wantAckPolicy  nats.AckPolicy
		wantFilterSubj string
	}{
		{
			name:           "default delivery configuration",
			trigger:        newTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "subscriber.example.com"),
			broker:         newReadyBroker(testNamespace, testBrokerName),
			consumerName:   "test-consumer",
			wantAckWait:    30 * time.Second,
			wantMaxDeliver: 3,
			wantAckPolicy:  nats.AckExplicitPolicy,
			wantFilterSubj: "test-namespace.test-broker._knative_broker.>",
		},
		{
			name: "custom retry count",
			trigger: func() *eventingv1.Trigger {
				retry := int32(5)
				trigger := newTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "subscriber.example.com")
				trigger.Spec.Delivery = &eventingduckv1.DeliverySpec{
					Retry: &retry,
				}
				return trigger
			}(),
			broker:         newReadyBroker(testNamespace, testBrokerName),
			consumerName:   "test-consumer",
			wantAckWait:    30 * time.Second,
			wantMaxDeliver: 6, // retry + 1
			wantAckPolicy:  nats.AckExplicitPolicy,
			wantFilterSubj: "test-namespace.test-broker._knative_broker.>",
		},
		{
			name: "custom timeout",
			trigger: func() *eventingv1.Trigger {
				timeout := "PT1M" // 1 minute in ISO 8601 duration format
				trigger := newTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "subscriber.example.com")
				trigger.Spec.Delivery = &eventingduckv1.DeliverySpec{
					Timeout: &timeout,
				}
				return trigger
			}(),
			broker:         newReadyBroker(testNamespace, testBrokerName),
			consumerName:   "test-consumer",
			wantAckWait:    1 * time.Minute,
			wantMaxDeliver: 3,
			wantAckPolicy:  nats.AckExplicitPolicy,
			wantFilterSubj: "test-namespace.test-broker._knative_broker.>",
		},
		{
			name: "invalid timeout falls back to default",
			trigger: func() *eventingv1.Trigger {
				timeout := "invalid-duration"
				trigger := newTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "subscriber.example.com")
				trigger.Spec.Delivery = &eventingduckv1.DeliverySpec{
					Timeout: &timeout,
				}
				return trigger
			}(),
			broker:         newReadyBroker(testNamespace, testBrokerName),
			consumerName:   "test-consumer",
			wantAckWait:    30 * time.Second, // default
			wantMaxDeliver: 3,
			wantAckPolicy:  nats.AckExplicitPolicy,
			wantFilterSubj: "test-namespace.test-broker._knative_broker.>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Reconciler{}
			config := r.buildConsumerConfig(tc.trigger, tc.broker, tc.consumerName)

			if config.AckWait != tc.wantAckWait {
				t.Errorf("AckWait = %v, want %v", config.AckWait, tc.wantAckWait)
			}
			if config.MaxDeliver != tc.wantMaxDeliver {
				t.Errorf("MaxDeliver = %d, want %d", config.MaxDeliver, tc.wantMaxDeliver)
			}
			if config.AckPolicy != tc.wantAckPolicy {
				t.Errorf("AckPolicy = %v, want %v", config.AckPolicy, tc.wantAckPolicy)
			}
			if config.FilterSubject != tc.wantFilterSubj {
				t.Errorf("FilterSubject = %q, want %q", config.FilterSubject, tc.wantFilterSubj)
			}
			if config.Name != tc.consumerName {
				t.Errorf("Name = %q, want %q", config.Name, tc.consumerName)
			}
			if config.Durable != tc.consumerName {
				t.Errorf("Durable = %q, want %q", config.Durable, tc.consumerName)
			}
			if config.DeliverPolicy != nats.DeliverNewPolicy {
				t.Errorf("DeliverPolicy = %v, want %v", config.DeliverPolicy, nats.DeliverNewPolicy)
			}
			if config.ReplayPolicy != nats.ReplayInstantPolicy {
				t.Errorf("ReplayPolicy = %v, want %v", config.ReplayPolicy, nats.ReplayInstantPolicy)
			}
		})
	}
}

func TestReconcileConsumer_CreateNew(t *testing.T) {
	ctx := testContextWithRecorder(t)
	brokerLister := newFakeBrokerLister()
	js := newMockJetStreamContext()

	broker := newReadyBroker(testNamespace, testBrokerName)
	brokerLister.addBroker(broker)

	r := newReconcilerForTest(brokerLister, js)

	trigger := newTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "subscriber.example.com")

	err := r.reconcileConsumer(ctx, trigger, broker)

	if err != nil {
		t.Errorf("reconcileConsumer() returned error = %v, want nil", err)
	}

	// Verify consumer was created
	streamName := "KN_BROKER_TEST_NAMESPACE__TEST_BROKER"
	consumerName := "KN_TRIGGER_TESTTRIGGERUID12345"
	if js.consumers[streamName] == nil || js.consumers[streamName][consumerName] == nil {
		t.Error("reconcileConsumer() did not create consumer")
	}
}

func TestReconcileConsumer_CreateFailed(t *testing.T) {
	ctx := testContextWithRecorder(t)
	brokerLister := newFakeBrokerLister()
	js := newMockJetStreamContext()
	js.setAddConsumerError(errors.New("failed to add consumer"))

	broker := newReadyBroker(testNamespace, testBrokerName)
	brokerLister.addBroker(broker)

	r := newReconcilerForTest(brokerLister, js)

	trigger := newTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "subscriber.example.com")

	err := r.reconcileConsumer(ctx, trigger, broker)

	if err == nil {
		t.Error("reconcileConsumer() returned nil, want error when create fails")
	}
}

func TestReconcileConsumer_UpdateExisting(t *testing.T) {
	ctx := testContextWithRecorder(t)
	brokerLister := newFakeBrokerLister()
	js := newMockJetStreamContext()

	broker := newReadyBroker(testNamespace, testBrokerName)
	brokerLister.addBroker(broker)

	// Pre-create the consumer
	streamName := "KN_BROKER_TEST_NAMESPACE__TEST_BROKER"
	consumerName := "KN_TRIGGER_TESTTRIGGERUID12345"
	js.addConsumerInfo(streamName, consumerName)

	r := newReconcilerForTest(brokerLister, js)

	trigger := newTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "subscriber.example.com")

	err := r.reconcileConsumer(ctx, trigger, broker)

	if err != nil {
		t.Errorf("reconcileConsumer() returned error = %v, want nil", err)
	}
}

func TestReconcileConsumer_ConsumerInfoError(t *testing.T) {
	ctx := testContextWithRecorder(t)
	brokerLister := newFakeBrokerLister()
	js := newMockJetStreamContext()
	js.setConsumerInfoError(errors.New("connection error"))

	broker := newReadyBroker(testNamespace, testBrokerName)
	brokerLister.addBroker(broker)

	r := newReconcilerForTest(brokerLister, js)

	trigger := newTriggerWithSubscriber(testNamespace, testTriggerName, testBrokerName, "subscriber.example.com")

	err := r.reconcileConsumer(ctx, trigger, broker)

	if err == nil {
		t.Error("reconcileConsumer() returned nil, want error when consumer info fails")
	}
}

func TestEventReasons(t *testing.T) {
	tests := []struct {
		constant string
		want     string
	}{
		{ReasonConsumerCreated, "JetStreamConsumerCreated"},
		{ReasonConsumerUpdated, "JetStreamConsumerUpdated"},
		{ReasonConsumerFailed, "JetStreamConsumerFailed"},
		{ReasonConsumerDeleted, "JetStreamConsumerDeleted"},
	}

	for _, tc := range tests {
		if tc.constant != tc.want {
			t.Errorf("Constant = %q, want %q", tc.constant, tc.want)
		}
	}
}
