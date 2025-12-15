/*
Copyright 2024 The Knative Authors

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

const (
	// BrokerLabelKey is the label key for broker resources
	BrokerLabelKey = "eventing.knative.dev/broker"

	// RoleLabelKey is the label key for role identification
	RoleLabelKey = "eventing.knative.dev/role"

	// IngressRoleLabelValue is the label value for ingress components
	IngressRoleLabelValue = "ingress"

	// FilterRoleLabelValue is the label value for filter components
	FilterRoleLabelValue = "filter"

	// BrokerClassLabelValue is the label value for NatsJetStream broker
	BrokerClassLabelValue = "nats-jetstream-broker"

	// IngressContainerName is the name of the ingress container
	IngressContainerName = "ingress"

	// FilterContainerName is the name of the filter container
	FilterContainerName = "filter"

	// IngressPortName is the name of the ingress HTTP port
	IngressPortName = "http"

	// IngressPortNumber is the port number for ingress HTTP traffic
	IngressPortNumber = 8080

	// MetricsPortName is the name of the metrics port
	MetricsPortName = "metrics"

	// MetricsPortNumber is the port number for metrics
	MetricsPortNumber = 9090
)

// IngressName returns the name of the ingress deployment/service for a broker
func IngressName(brokerName string) string {
	return brokerName + "-broker-ingress"
}

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

// IngressLabels returns labels for ingress resources
func IngressLabels(brokerName string) map[string]string {
	return map[string]string{
		BrokerLabelKey: brokerName,
		RoleLabelKey:   IngressRoleLabelValue,
	}
}

// FilterLabels returns labels for filter resources
func FilterLabels(brokerName string) map[string]string {
	return map[string]string{
		BrokerLabelKey: brokerName,
		RoleLabelKey:   FilterRoleLabelValue,
	}
}
