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

package contract

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

const (
	// ConfigMapName is the name of the contract ConfigMap
	ConfigMapName = "nats-broker-ingress-contract"

	// ConfigMapDataKey is the key in the ConfigMap data
	ConfigMapDataKey = "contract"

	// ContractGenerationAnnotation is used for change detection
	ContractGenerationAnnotation = "nats.eventing.knative.dev/contract-generation"
)

// BrokerContract represents a single broker's configuration in the contract
type BrokerContract struct {
	// UID is the broker's unique identifier
	UID string `json:"uid"`

	// Namespace of the broker
	Namespace string `json:"namespace"`

	// Name of the broker
	Name string `json:"name"`

	// StreamName is the JetStream stream for this broker
	StreamName string `json:"streamName"`

	// PublishSubject is the subject pattern for publishing events
	PublishSubject string `json:"publishSubject"`

	// Path is the broker's addressable URL path (/{namespace}/{name})
	Path string `json:"path"`

	// Generation tracks changes for reconciliation
	Generation int64 `json:"generation"`
}

// Contract is the full contract containing all broker configurations
type Contract struct {
	// Brokers maps broker key (namespace/name) to configuration
	Brokers map[string]BrokerContract `json:"brokers"`

	// Generation is incremented on every change for change detection
	Generation int64 `json:"generation"`
}

// BrokerKey returns the key for a broker in the contract map
func BrokerKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

// GetBroker returns a broker from the contract by namespace and name
func (c *Contract) GetBroker(namespace, name string) (BrokerContract, bool) {
	broker, ok := c.Brokers[BrokerKey(namespace, name)]
	return broker, ok
}

// GetBrokerByPath returns a broker from the contract by its URL path
func (c *Contract) GetBrokerByPath(path string) (BrokerContract, bool) {
	for _, broker := range c.Brokers {
		if broker.Path == path {
			return broker, true
		}
	}
	return BrokerContract{}, false
}

// SetBroker adds or updates a broker in the contract
func (c *Contract) SetBroker(broker BrokerContract) {
	if c.Brokers == nil {
		c.Brokers = make(map[string]BrokerContract)
	}
	c.Brokers[BrokerKey(broker.Namespace, broker.Name)] = broker
	c.Generation++
}

// DeleteBroker removes a broker from the contract
func (c *Contract) DeleteBroker(namespace, name string) {
	delete(c.Brokers, BrokerKey(namespace, name))
	c.Generation++
}

// ParseContract parses a Contract from a ConfigMap
func ParseContract(cm *corev1.ConfigMap) (*Contract, error) {
	if cm == nil || cm.Data == nil {
		return &Contract{Brokers: make(map[string]BrokerContract)}, nil
	}

	data, ok := cm.Data[ConfigMapDataKey]
	if !ok || data == "" {
		return &Contract{Brokers: make(map[string]BrokerContract)}, nil
	}

	var contract Contract
	if err := json.Unmarshal([]byte(data), &contract); err != nil {
		return nil, fmt.Errorf("failed to unmarshal contract: %w", err)
	}

	if contract.Brokers == nil {
		contract.Brokers = make(map[string]BrokerContract)
	}

	return &contract, nil
}

// SerializeContract serializes a Contract to JSON string
func SerializeContract(contract *Contract) (string, error) {
	data, err := json.Marshal(contract)
	if err != nil {
		return "", fmt.Errorf("failed to marshal contract: %w", err)
	}
	return string(data), nil
}
