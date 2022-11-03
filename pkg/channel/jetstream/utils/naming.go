/*
Copyright 2021 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"fmt"
	"knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	"strings"
)

var (
	streamNameReplacer = strings.NewReplacer("-", "_")
	uidReplacer        = strings.NewReplacer("-", "")
)

// StreamName builds a valid NATS Jetstream stream name from a *v1alpha1.NatsJetStreamChannel.
// If overrideName is not set, the format is KN_NAMESPACE__NAME, where hyphens in the namespace or name are replaced
// with _.
//
// For example:
// - "default/channel" => "KN_DEFAULT__CHANNEL"
// - "knative-eventing/my-channel" => "KN_KNATIVE_EVENTING__MY_CHANNEL"
func StreamName(nc *v1alpha1.NatsJetStreamChannel) string {
	if nc.Spec.Stream.OverrideName != "" {
		return nc.Spec.Stream.OverrideName
	}

	return strings.ToUpper(streamNameReplacer.Replace(fmt.Sprintf("KN_%s__%s", nc.Namespace, nc.Name)))
}

func PublishSubjectName(namespace, name string) string {
	return fmt.Sprintf("%s.%s._knative", namespace, name)
}

func ConsumerName(subUID string) string {
	return fmt.Sprintf("KN_SUB_%s", strings.ToUpper(uidReplacer.Replace(subUID)))
}

// ConsumerSubjectName generates a shareable subject name to bind to NATS Consumers for each Knative subscriber. This
// can be used by multiple dispatcher replicas to share delivery of
func ConsumerSubjectName(namespace, name, uid string) string {
	return fmt.Sprintf("%s.%s._knative_consumer.%s", namespace, name, strings.ToLower(uidReplacer.Replace(uid)))
}
