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

package resources

import "maps"

const (
	// BrokerLabelKey is the label key for broker resources
	BrokerLabelKey = "eventing.knative.dev/broker"

	// RoleLabelKey is the label key for role identification
	RoleLabelKey = "eventing.knative.dev/role"

	// FilterRoleLabelValue is the label value for filter components
	FilterRoleLabelValue = "filter"

	// BrokerClassLabelValue is the label value for NatsJetStream broker
	BrokerClassLabelValue = "nats-jetstream-broker"

	// FilterContainerName is the name of the filter container
	FilterContainerName = "filter"

	// IngressPortName is the name of the ingress HTTP port
	IngressPortName = "http"

	// MetricsPortName is the name of the metrics port
	MetricsPortName = "metrics"

	// MetricsPortNumber is the port number for metrics
	MetricsPortNumber = 9090
)

// FilterName returns the name of the filter deployment/service for a broker
func FilterName(brokerName string) string {
	return brokerName + "-broker-filter"
}

// BrokerLabels returns labels for broker-related resources
func BrokerLabels(brokerName string) map[string]string {
	return map[string]string{
		BrokerLabelKey: brokerName,
	}
}

// FilterLabels returns labels for filter resources
func FilterLabels(brokerName string) map[string]string {
	return map[string]string{
		BrokerLabelKey: brokerName,
		RoleLabelKey:   FilterRoleLabelValue,
	}
}

// mergeMaps merges two maps, with values from the second map taking precedence.
// The base map's required labels are preserved.
func mergeMaps(base, override map[string]string) map[string]string {
	result := make(map[string]string, len(base)+len(override))
	maps.Copy(result, base)
	maps.Copy(result, override)
	return result
}
