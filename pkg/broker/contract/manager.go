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
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"knative.dev/pkg/system"
)

// Manager handles contract ConfigMap operations
type Manager struct {
	client    kubernetes.Interface
	lister    corev1listers.ConfigMapLister
	namespace string

	mu sync.Mutex
}

// NewManager creates a new contract Manager
func NewManager(client kubernetes.Interface, lister corev1listers.ConfigMapLister) *Manager {
	return &Manager{
		client:    client,
		lister:    lister,
		namespace: system.Namespace(),
	}
}

// GetContract loads the contract from the ConfigMap
func (m *Manager) GetContract(ctx context.Context) (*Contract, error) {
	cm, err := m.lister.ConfigMaps(m.namespace).Get(ConfigMapName)
	if err != nil {
		if apierrs.IsNotFound(err) {
			return &Contract{Brokers: make(map[string]BrokerContract)}, nil
		}
		return nil, err
	}
	return ParseContract(cm)
}

// UpdateBroker adds or updates a broker in the contract ConfigMap
func (m *Manager) UpdateBroker(ctx context.Context, broker BrokerContract) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get or create ConfigMap
	cm, err := m.client.CoreV1().ConfigMaps(m.namespace).Get(ctx, ConfigMapName, metav1.GetOptions{})
	if err != nil {
		if apierrs.IsNotFound(err) {
			// Create new ConfigMap
			contract := &Contract{Brokers: make(map[string]BrokerContract)}
			contract.SetBroker(broker)

			data, err := SerializeContract(contract)
			if err != nil {
				return err
			}

			cm = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ConfigMapName,
					Namespace: m.namespace,
					Labels: map[string]string{
						"nats.eventing.knative.dev/release": "devel",
					},
					Annotations: map[string]string{
						ContractGenerationAnnotation: fmt.Sprintf("%d", contract.Generation),
					},
				},
				Data: map[string]string{
					ConfigMapDataKey: data,
				},
			}

			_, err = m.client.CoreV1().ConfigMaps(m.namespace).Create(ctx, cm, metav1.CreateOptions{})
			return err
		}
		return err
	}

	// Update existing ConfigMap
	contract, err := ParseContract(cm)
	if err != nil {
		return err
	}

	contract.SetBroker(broker)

	data, err := SerializeContract(contract)
	if err != nil {
		return err
	}

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[ConfigMapDataKey] = data

	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}
	cm.Annotations[ContractGenerationAnnotation] = fmt.Sprintf("%d", contract.Generation)

	_, err = m.client.CoreV1().ConfigMaps(m.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

// DeleteBroker removes a broker from the contract ConfigMap
func (m *Manager) DeleteBroker(ctx context.Context, namespace, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cm, err := m.client.CoreV1().ConfigMaps(m.namespace).Get(ctx, ConfigMapName, metav1.GetOptions{})
	if err != nil {
		if apierrs.IsNotFound(err) {
			return nil // Nothing to delete
		}
		return err
	}

	contract, err := ParseContract(cm)
	if err != nil {
		return err
	}

	// Check if broker exists in contract
	if _, ok := contract.GetBroker(namespace, name); !ok {
		return nil // Broker not in contract, nothing to do
	}

	contract.DeleteBroker(namespace, name)

	data, err := SerializeContract(contract)
	if err != nil {
		return err
	}

	cm.Data[ConfigMapDataKey] = data
	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}
	cm.Annotations[ContractGenerationAnnotation] = fmt.Sprintf("%d", contract.Generation)

	_, err = m.client.CoreV1().ConfigMaps(m.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}
