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

package utils

import (
	"fmt"
	"strings"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
)

var (
	streamNameReplacer = strings.NewReplacer("-", "_")
	uidReplacer        = strings.NewReplacer("-", "")
)

// BrokerStreamNameAnnotation, when set on a Broker, overrides the generated
// JetStream stream name with its value. The value must be a valid JetStream
// stream name (see ValidateStreamName). Because it lives on the Broker object,
// it is resolvable everywhere the stream name is needed, including finalization.
const BrokerStreamNameAnnotation = "natsjetstream.eventing.knative.dev/stream-name"

// invalidStreamNameChars are the characters JetStream disallows in a stream name.
const invalidStreamNameChars = " \t\n\r.*>/\\"

// BrokerStreamName returns the JetStream stream name for a Broker. If the
// BrokerStreamNameAnnotation is set, its value is used verbatim; otherwise the
// name is generated as KN_BROKER_NAMESPACE__NAME, where hyphens in the namespace
// or name are replaced with _.
//
// For example:
// - "default/my-broker" => "KN_BROKER_DEFAULT__MY_BROKER"
// - "knative-eventing/test-broker" => "KN_BROKER_KNATIVE_EVENTING__TEST_BROKER"
func BrokerStreamName(b *eventingv1.Broker) string {
	if name := b.Annotations[BrokerStreamNameAnnotation]; name != "" {
		return name
	}
	return BrokerStreamNameByNsAndName(b.Namespace, b.Name)
}

// ValidateStreamName reports whether name is a usable JetStream stream name.
func ValidateStreamName(name string) error {
	if name == "" {
		return fmt.Errorf("stream name must not be empty")
	}
	if strings.ContainsAny(name, invalidStreamNameChars) {
		return fmt.Errorf("invalid stream name %q: must not contain spaces, '.', '*', '>', '/' or '\\'", name)
	}
	return nil
}

func BrokerStreamNameByNsAndName(ns string, name string) string {
	return strings.ToUpper(streamNameReplacer.Replace(fmt.Sprintf("KN_BROKER_%s__%s", ns, name)))
}

// BrokerPublishSubjectName generates the subject name for publishing events to a broker's stream.
func BrokerPublishSubjectName(namespace, name string) string {
	return fmt.Sprintf("%s.%s._knative_broker", namespace, name)
}

// TriggerConsumerName generates a consumer name for a Trigger.
// Format: KN_TRIGGER_{UID} where hyphens are removed from the UID.
func TriggerConsumerName(triggerUID string) string {
	return fmt.Sprintf("KN_TRIGGER_%s", strings.ToUpper(uidReplacer.Replace(triggerUID)))
}

// TriggerConsumerSubjectName generates a shareable subject name for Trigger consumers.
// This allows multiple filter replicas to share delivery of events.
func TriggerConsumerSubjectName(namespace, brokerName, triggerUID string) string {
	return fmt.Sprintf("%s.%s._knative_trigger.%s", namespace, brokerName, strings.ToLower(uidReplacer.Replace(triggerUID)))
}

// DeadLetterStreamName generates the dead letter stream name for a broker.
func DeadLetterStreamName(b *eventingv1.Broker) string {
	return strings.ToUpper(streamNameReplacer.Replace(fmt.Sprintf("KN_BROKER_DLQ_%s__%s", b.Namespace, b.Name)))
}

// DeadLetterSubjectName generates the dead letter subject name for a broker.
func DeadLetterSubjectName(namespace, name string) string {
	return fmt.Sprintf("%s.%s._knative_broker_dlq", namespace, name)
}
