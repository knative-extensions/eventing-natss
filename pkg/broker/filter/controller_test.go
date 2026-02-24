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
	"testing"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"

	"knative.dev/eventing-natss/pkg/broker/constants"
)

func TestFilterTriggersByBrokerClass(t *testing.T) {
	tests := []struct {
		name         string
		broker       *eventingv1.Broker
		trigger      *eventingv1.Trigger
		wantFiltered bool
	}{
		{
			name:         "trigger referencing NatsJetStreamBroker class broker",
			broker:       newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			trigger:      newTestTrigger(testNamespace, testTriggerName, testBrokerName),
			wantFiltered: true,
		},
		{
			name:         "trigger referencing different broker class",
			broker:       newTestBroker(testNamespace, testBrokerName, "OtherBrokerClass"),
			trigger:      newTestTrigger(testNamespace, testTriggerName, testBrokerName),
			wantFiltered: false,
		},
		{
			name:         "trigger referencing broker without class annotation",
			broker:       newTestBroker(testNamespace, testBrokerName, ""),
			trigger:      newTestTrigger(testNamespace, testTriggerName, testBrokerName),
			wantFiltered: false,
		},
		{
			name:         "trigger referencing non-existent broker passes to reconciler",
			broker:       nil,
			trigger:      newTestTrigger(testNamespace, testTriggerName, "non-existent-broker"),
			wantFiltered: true,
		},
		{
			name:         "non-trigger object",
			broker:       newTestBroker(testNamespace, testBrokerName, constants.BrokerClassName),
			trigger:      nil,
			wantFiltered: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			brokerLister := newFakeBrokerLister()
			if tc.broker != nil {
				brokerLister.addBroker(tc.broker)
			}

			filterFunc := filterTriggersByBrokerClass(brokerLister)

			var obj interface{}
			if tc.trigger != nil {
				obj = tc.trigger
			} else {
				obj = "not a trigger"
			}

			got := filterFunc(obj)
			if got != tc.wantFiltered {
				t.Errorf("filterTriggersByBrokerClass() = %v, want %v", got, tc.wantFiltered)
			}
		})
	}
}
