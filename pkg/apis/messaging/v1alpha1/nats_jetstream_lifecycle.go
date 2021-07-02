/*
Copyright 2019 The Knative Authors

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

package v1aplha1

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/apis"
	v1 "knative.dev/pkg/apis/duck/v1"
)

var conditionSet = apis.NewLivingConditionSet(
	NatssChannelConditionDispatcherReady,
	NatssChannelConditionServiceReady,
	NatssChannelConditionEndpointsReady,
	NatssChannelConditionAddressable,
	NatssChannelConditionChannelServiceReady)

const (
	// NatssChannelConditionReady has status True when all subconditions below have been set to True.
	NatssChannelConditionReady = apis.ConditionReady

	// NatssChannelConditionDispatcherReady has status True when a Dispatcher deployment is ready
	// Keyed off appsv1.DeploymentAvailable, which means minimum available replicas required are up
	// and running for at least minReadySeconds.
	NatssChannelConditionDispatcherReady apis.ConditionType = "DispatcherReady"

	// NatssChannelConditionServiceReady has status True when a k8s Service is ready. This
	// basically just means it exists because there's no meaningful status in Service. See Endpoints
	// below.
	NatssChannelConditionServiceReady apis.ConditionType = "ServiceReady"

	// NatssChannelConditionEndpointsReady has status True when a k8s Service Endpoints are backed
	// by at least one endpoint.
	NatssChannelConditionEndpointsReady apis.ConditionType = "EndpointsReady"

	// NatssChannelConditionAddressable has status true when this NatssChannel meets
	// the Addressable contract and has a non-empty hostname.
	NatssChannelConditionAddressable apis.ConditionType = "Addressable"

	// NatssChannelConditionChannelServiceReady has status True when a k8s Service representing the channel is ready.
	// Because this uses ExternalName, there are no endpoints to check.
	NatssChannelConditionChannelServiceReady apis.ConditionType = "ChannelServiceReady"
)

// GetConditionSet retrieves the condition set for this resource. Implements the KRShaped interface.
func (*NatsJetStreamChannel) GetConditionSet() apis.ConditionSet {
	return conditionSet
}

// GetUntypedSpec returns the spec of the InMemoryChannel.
func (c *NatsJetStreamChannel) GetUntypedSpec() interface{} {
	return c.Spec
}

// GetCondition returns the condition currently associated with the given type, or nil.
func (cs *NatsJetStreamChannelStatus) GetCondition(t apis.ConditionType) *apis.Condition {
	return conditionSet.Manage(cs).GetCondition(t)
}

// IsReady returns true if the resource is ready overall.
func (cs *NatsJetStreamChannelStatus) IsReady() bool {
	return conditionSet.Manage(cs).IsHappy()
}

// InitializeConditions sets relevant unset conditions to Unknown state.
func (cs *NatsJetStreamChannelStatus) InitializeConditions() {
	conditionSet.Manage(cs).InitializeConditions()
}

// SetAddress sets the address (as part of Addressable contract) and marks the correct condition.
func (cs *NatsJetStreamChannelStatus) SetAddress(url *apis.URL) {
	cs.Address = &v1.Addressable{URL: url}
	if url != nil {
		conditionSet.Manage(cs).MarkTrue(NatssChannelConditionAddressable)
	} else {
		conditionSet.Manage(cs).MarkFalse(NatssChannelConditionAddressable, "emptyHostname", "hostname is the empty string")
	}
}

func (cs *NatsJetStreamChannelStatus) MarkDispatcherFailed(reason, messageFormat string, messageA ...interface{}) {
	conditionSet.Manage(cs).MarkFalse(NatssChannelConditionDispatcherReady, reason, messageFormat, messageA...)
}

// TODO: Unify this with the ones from Eventing. Say: Broker, Trigger.
func (cs *NatsJetStreamChannelStatus) PropagateDispatcherStatus(ds *appsv1.DeploymentStatus) {
	for _, cond := range ds.Conditions {
		if cond.Type == appsv1.DeploymentAvailable {
			if cond.Status != corev1.ConditionTrue {
				cs.MarkDispatcherFailed("DispatcherNotReady", "Dispatcher Deployment is not ready: %s : %s", cond.Reason, cond.Message)
			} else {
				conditionSet.Manage(cs).MarkTrue(NatssChannelConditionDispatcherReady)
			}
		}
	}
}

func (cs *NatsJetStreamChannelStatus) MarkServiceFailed(reason, messageFormat string, messageA ...interface{}) {
	conditionSet.Manage(cs).MarkFalse(NatssChannelConditionServiceReady, reason, messageFormat, messageA...)
}

func (cs *NatsJetStreamChannelStatus) MarkServiceTrue() {
	conditionSet.Manage(cs).MarkTrue(NatssChannelConditionServiceReady)
}

func (cs *NatsJetStreamChannelStatus) MarkChannelServiceFailed(reason, messageFormat string, messageA ...interface{}) {
	conditionSet.Manage(cs).MarkFalse(NatssChannelConditionChannelServiceReady, reason, messageFormat, messageA...)
}

func (cs *NatsJetStreamChannelStatus) MarkChannelServiceTrue() {
	conditionSet.Manage(cs).MarkTrue(NatssChannelConditionChannelServiceReady)
}

func (cs *NatsJetStreamChannelStatus) MarkEndpointsFailed(reason, messageFormat string, messageA ...interface{}) {
	conditionSet.Manage(cs).MarkFalse(NatssChannelConditionEndpointsReady, reason, messageFormat, messageA...)
}

func (cs *NatsJetStreamChannelStatus) MarkEndpointsTrue() {
	conditionSet.Manage(cs).MarkTrue(NatssChannelConditionEndpointsReady)
}
