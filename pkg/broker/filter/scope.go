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
	"fmt"

	"github.com/kelseyhightower/envconfig"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
)

// BrokerScope identifies the Broker whose Trigger consumers a filter owns.
type BrokerScope struct {
	Namespace string `envconfig:"BROKER_NAMESPACE" required:"true"`
	Name      string `envconfig:"BROKER_NAME" required:"true"`
}

// BrokerScopeFromEnv rejects incomplete scope instead of watching every Broker.
func BrokerScopeFromEnv() (BrokerScope, error) {
	var scope BrokerScope
	if err := envconfig.Process("", &scope); err != nil {
		return BrokerScope{}, fmt.Errorf("invalid Broker scope: %w", err)
	}
	if scope.Namespace == "" || scope.Name == "" {
		return BrokerScope{}, fmt.Errorf("BROKER_NAMESPACE and BROKER_NAME must be non-empty")
	}
	return scope, nil
}

func (s BrokerScope) owns(trigger *eventingv1.Trigger) bool {
	return trigger != nil && trigger.Namespace == s.Namespace && trigger.Spec.Broker == s.Name
}
