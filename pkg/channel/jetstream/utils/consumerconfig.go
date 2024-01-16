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
	"math"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rickb777/date/period"
	"knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	v1 "knative.dev/eventing/pkg/apis/duck/v1"
	"knative.dev/eventing/pkg/kncloudevents"
)

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

func CalcRequestDeadline(msg *nats.Msg, ackWait time.Duration) time.Time {
	const jitter = time.Millisecond * 200

	// if previous deliveries were explicitly nacked earlier than the deadline, then our actual deadline will be earlier
	// than the deadline above
	ackDeadlineFromNow := time.Now().Add(ackWait).Add(-jitter)

	meta, err := msg.Metadata()
	if err != nil {
		return ackDeadlineFromNow
	}

	// if each delivery has timed out, then multiplying the number of deliveries by the ack wait will give us the
	// duration from publish which this attempt will be ack-waited
	ackDurationFromPublish := time.Duration(meta.NumDelivered) * ackWait

	// the deadline is the published timestamp plus our duration calculated above
	deadline := meta.Timestamp.Add(ackDurationFromPublish).Add(-jitter)

	if deadline.After(ackDeadlineFromNow) {
		deadline = ackDeadlineFromNow
	}
	return deadline
}

func CalcRequestTimeout(msg *nats.Msg, ackWait time.Duration) time.Duration {
	const jitter = time.Millisecond * 200

	// if previous deliveries were explicitly nacked earlier than the deadline, then our actual deadline will be earlier
	// than the deadline above
	ackDeadlineFromNow := ackWait - jitter

	meta, err := msg.Metadata()
	if err != nil {
		return ackDeadlineFromNow
	}

	// if each delivery has timed out, then multiplying the number of deliveries by the ack wait will give us the
	// duration from publish which this attempt will be ack-waited
	ackDurationFromPublish := time.Duration(meta.NumDelivered) * ackWait

	// the deadline is the published timestamp plus our duration calculated above
	deadline := ackDurationFromPublish - jitter

	if deadline > ackDeadlineFromNow {
		deadline = ackDeadlineFromNow
	}
	return deadline
}

func CalculateNakDelayForRetryNumber(attemptNum int, config *kncloudevents.RetryConfig) time.Duration {
	backoff, backoffDelay := parseBackoffFuncAndDelay(config)
	return backoff(attemptNum, backoffDelay)
}

type backoffFunc func(attemptNum int, delayDuration time.Duration) time.Duration

func CalculateAckWaitAndBackoffDelays(config *kncloudevents.RetryConfig) (time.Duration, []time.Duration) {
	var delays = make([]time.Duration, config.RetryMax)
	var totalDelays = config.RequestTimeout * time.Duration(config.RetryMax)
	// 1 second jitter
	const jitter = 1 * time.Second

	backoff, backoffDelay := parseBackoffFuncAndDelay(config)

	for i := 0; i < config.RetryMax; i++ {
		var nextDelay time.Duration
		if i == 0 {
			// the first backoff should be just request timeout + jitter
			nextDelay = 0
		} else {
			nextDelay = backoff(i-1, backoffDelay)
		}

		totalDelays += nextDelay
		delays[i] = nextDelay + config.RequestTimeout + jitter
	}
	return totalDelays, delays
}

func LinearBackoff(attemptNum int, delayDuration time.Duration) time.Duration {
	return delayDuration * time.Duration(attemptNum)
}

func ExpBackoff(attemptNum int, delayDuration time.Duration) time.Duration {
	return delayDuration * time.Duration(math.Exp2(float64(attemptNum)))
}

func parseBackoffFuncAndDelay(config *kncloudevents.RetryConfig) (backoffFunc, time.Duration) {
	var backoff backoffFunc
	switch *config.BackoffPolicy {
	case v1.BackoffPolicyExponential:
		backoff = ExpBackoff
	case v1.BackoffPolicyLinear:
		backoff = LinearBackoff
	}
	// it should be validated at this point
	delay, _ := period.Parse(*config.BackoffDelay)
	backoffDelay, _ := delay.Duration()

	return backoff, backoffDelay
}
