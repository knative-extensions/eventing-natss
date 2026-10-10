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
	"net/http"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/nats-io/nats.go"
	"knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	"knative.dev/eventing/pkg/kncloudevents"

	_ "unsafe"
)

//go:linkname generateBackoffFn knative.dev/eventing/pkg/kncloudevents.generateBackoffFn
func generateBackoffFn(config *kncloudevents.RetryConfig) retryablehttp.Backoff

func ConvertJsDeliverPolicy(in v1alpha1.DeliverPolicy, def jetstream.DeliverPolicy) jetstream.DeliverPolicy {
	switch in {
	case v1alpha1.AllDeliverPolicy:
		return jetstream.DeliverAllPolicy
	case v1alpha1.LastDeliverPolicy:
		return jetstream.DeliverLastPolicy
	case v1alpha1.NewDeliverPolicy:
		return jetstream.DeliverNewPolicy
	case v1alpha1.ByStartSequenceDeliverPolicy:
		return jetstream.DeliverByStartSequencePolicy
	case v1alpha1.ByStartTimeDeliverPolicy:
		return jetstream.DeliverByStartTimePolicy
	}

	return def
}

func ConvertJsReplayPolicy(in v1alpha1.ReplayPolicy, def jetstream.ReplayPolicy) jetstream.ReplayPolicy {
	switch in {
	case v1alpha1.InstantReplayPolicy:
		return jetstream.ReplayInstantPolicy
	case v1alpha1.OriginalReplayPolicy:
		return jetstream.ReplayOriginalPolicy
	}

	return def
}

func ConvertDeliverPolicy(in v1alpha1.DeliverPolicy, def nats.DeliverPolicy) nats.DeliverPolicy {
	switch in {
	case v1alpha1.AllDeliverPolicy:
		return nats.DeliverAllPolicy
	case v1alpha1.LastDeliverPolicy:
		return nats.DeliverLastPolicy
	case v1alpha1.NewDeliverPolicy:
		return nats.DeliverNewPolicy
	case v1alpha1.ByStartSequenceDeliverPolicy:
		return nats.DeliverByStartSequencePolicy
	case v1alpha1.ByStartTimeDeliverPolicy:
		return nats.DeliverByStartTimePolicy
	}

	return def
}

func ConvertReplayPolicy(in v1alpha1.ReplayPolicy, def nats.ReplayPolicy) nats.ReplayPolicy {
	switch in {
	case v1alpha1.InstantReplayPolicy:
		return nats.ReplayInstantPolicy
	case v1alpha1.OriginalReplayPolicy:
		return nats.ReplayOriginalPolicy
	}

	return def
}

func CalcRequestTimeout(numDelivered int, ackWait time.Duration) time.Duration {
	const jitter = time.Millisecond * 200

	// if previous deliveries were explicitly nacked earlier than the deadline, then our actual deadline will be earlier
	// than the deadline above
	ackDeadlineFromNow := ackWait - jitter

	//meta, err := msg.Metadata()
	//if err != nil {
	//	return ackDeadlineFromNow
	//}

	// if each delivery has timed out, then multiplying the number of deliveries by the ack wait will give us the
	// duration from publish which this attempt will be ack-waited
	ackDurationFromPublish := time.Duration(numDelivered) * ackWait

	// the deadline is the published timestamp plus our duration calculated above
	deadline := ackDurationFromPublish - jitter

	if deadline > ackDeadlineFromNow {
		deadline = ackDeadlineFromNow
	}
	return deadline
}

// CalculateNakDelayForRetryNumber calculates the NAK delay for a JetStream
// delivery. JetStream's NumDelivered is one-based, while Knative's retry
// backoff callback receives a zero-based retry attempt.
func CalculateNakDelayForRetryNumber(numDelivered int, config *kncloudevents.RetryConfig, response *http.Response) time.Duration {
	if config == nil || config.Backoff == nil {
		return 0
	}
	attemptNum := numDelivered - 1
	if attemptNum < 0 {
		attemptNum = 0
	}
	return generateBackoffFn(config)(0, 0, attemptNum, response)
}
