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

package v1alpha1

import (
	"testing"

	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

var condReady = apis.Condition{
	Type:   NatssChannelConditionReady,
	Status: corev1.ConditionTrue,
}

var condDispatcherNotReady = apis.Condition{
	Type:   NatssChannelConditionDispatcherReady,
	Status: corev1.ConditionFalse,
}

var deploymentConditionReady = appsv1.DeploymentCondition{
	Type:   appsv1.DeploymentAvailable,
	Status: corev1.ConditionTrue,
}

var deploymentConditionNotReady = appsv1.DeploymentCondition{
	Type:   appsv1.DeploymentAvailable,
	Status: corev1.ConditionFalse,
}

var deploymentStatusReady = &appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{deploymentConditionReady}}
var deploymentStatusNotReady = &appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{deploymentConditionNotReady}}

var ignoreAllButTypeAndStatus = cmpopts.IgnoreFields(
	apis.Condition{},
	"LastTransitionTime", "Message", "Reason", "Severity")

func TestNatssChannelGetConditionSet(t *testing.T) {
	r := &NatsJetStreamChannel{}

	if got, want := r.GetConditionSet().GetTopLevelConditionType(), apis.ConditionReady; got != want {
		t.Errorf("GetTopLevelCondition=%v, want=%v", got, want)
	}
}

func TestChannelGetCondition(t *testing.T) {
	tests := []struct {
		name      string
		cs        *NatsJetStreamChannelStatus
		condQuery apis.ConditionType
		want      *apis.Condition
	}{{
		name: "single condition",
		cs: &NatsJetStreamChannelStatus{
			ChannelableStatus: eventingduckv1.ChannelableStatus{
				Status: duckv1.Status{
					Conditions: []apis.Condition{
						condReady,
						condDispatcherNotReady,
					},
				},
			},
		},
		condQuery: apis.ConditionReady,
		want:      &condReady,
	}, {
		name: "unknown condition",
		cs: &NatsJetStreamChannelStatus{
			ChannelableStatus: eventingduckv1.ChannelableStatus{
				Status: duckv1.Status{
					Conditions: []apis.Condition{
						condReady,
						condDispatcherNotReady,
					},
				},
			},
		},
		condQuery: apis.ConditionType("foo"),
		want:      nil,
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.cs.GetCondition(test.condQuery)
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("unexpected condition (-want, +got) = %v", diff)
			}
		})
	}
}

func TestChannelInitializeConditions(t *testing.T) {
	tests := []struct {
		name string
		cs   *NatsJetStreamChannelStatus
		want *NatsJetStreamChannelStatus
	}{{
		name: "empty",
		cs:   &NatsJetStreamChannelStatus{},
		want: &NatsJetStreamChannelStatus{
			ChannelableStatus: eventingduckv1.ChannelableStatus{
				Status: duckv1.Status{
					Conditions: []apis.Condition{{
						Type:   NatssChannelConditionAddressable,
						Status: corev1.ConditionUnknown,
					}, {
						Type:   NatssChannelConditionChannelServiceReady,
						Status: corev1.ConditionUnknown,
					}, {
						Type:   NatssChannelConditionDispatcherReady,
						Status: corev1.ConditionUnknown,
					}, {
						Type:   NatssChannelConditionEndpointsReady,
						Status: corev1.ConditionUnknown,
					}, {
						Type:   NatssChannelConditionReady,
						Status: corev1.ConditionUnknown,
					}, {
						Type:   NatssChannelConditionServiceReady,
						Status: corev1.ConditionUnknown,
					}},
				},
			},
		},
	}, {
		name: "one false",
		cs: &NatsJetStreamChannelStatus{
			ChannelableStatus: eventingduckv1.ChannelableStatus{
				Status: duckv1.Status{
					Conditions: []apis.Condition{{
						Type:   NatssChannelConditionDispatcherReady,
						Status: corev1.ConditionFalse,
					}},
				},
			},
		},
		want: &NatsJetStreamChannelStatus{
			ChannelableStatus: eventingduckv1.ChannelableStatus{
				Status: duckv1.Status{
					Conditions: []apis.Condition{{
						Type:   NatssChannelConditionAddressable,
						Status: corev1.ConditionUnknown,
					}, {
						Type:   NatssChannelConditionChannelServiceReady,
						Status: corev1.ConditionUnknown,
					}, {
						Type:   NatssChannelConditionDispatcherReady,
						Status: corev1.ConditionFalse,
					}, {
						Type:   NatssChannelConditionEndpointsReady,
						Status: corev1.ConditionUnknown,
					}, {
						Type:   NatssChannelConditionReady,
						Status: corev1.ConditionUnknown,
					}, {
						Type:   NatssChannelConditionServiceReady,
						Status: corev1.ConditionUnknown,
					}},
				},
			},
		},
	}, {
		name: "one true",
		cs: &NatsJetStreamChannelStatus{
			ChannelableStatus: eventingduckv1.ChannelableStatus{
				Status: duckv1.Status{
					Conditions: []apis.Condition{{
						Type:   NatssChannelConditionDispatcherReady,
						Status: corev1.ConditionTrue,
					}},
				},
			},
		},
		want: &NatsJetStreamChannelStatus{
			ChannelableStatus: eventingduckv1.ChannelableStatus{
				Status: duckv1.Status{
					Conditions: []apis.Condition{{
						Type:   NatssChannelConditionAddressable,
						Status: corev1.ConditionUnknown,
					}, {
						Type:   NatssChannelConditionChannelServiceReady,
						Status: corev1.ConditionUnknown,
					}, {
						Type:   NatssChannelConditionDispatcherReady,
						Status: corev1.ConditionTrue,
					}, {
						Type:   NatssChannelConditionEndpointsReady,
						Status: corev1.ConditionUnknown,
					}, {
						Type:   NatssChannelConditionReady,
						Status: corev1.ConditionUnknown,
					}, {
						Type:   NatssChannelConditionServiceReady,
						Status: corev1.ConditionUnknown,
					}},
				},
			},
		},
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.cs.InitializeConditions()
			if diff := cmp.Diff(test.want, test.cs, ignoreAllButTypeAndStatus); diff != "" {
				t.Errorf("unexpected conditions (-want, +got) = %v", diff)
			}
		})
	}
}

func TestChannelIsReady(t *testing.T) {
	tests := []struct {
		name                    string
		markServiceReady        bool
		markChannelServiceReady bool
		setAddress              bool
		markEndpointsReady      bool
		wantReady               bool
		dispatcherStatus        *appsv1.DeploymentStatus
	}{{
		name:                    "all happy",
		markServiceReady:        true,
		markChannelServiceReady: true,
		markEndpointsReady:      true,
		dispatcherStatus:        deploymentStatusReady,
		setAddress:              true,
		wantReady:               true,
	}, {
		name:                    "service not ready",
		markServiceReady:        false,
		markChannelServiceReady: false,
		markEndpointsReady:      true,
		dispatcherStatus:        deploymentStatusReady,
		setAddress:              true,
		wantReady:               false,
	}, {
		name:                    "endpoints not ready",
		markServiceReady:        true,
		markChannelServiceReady: false,
		markEndpointsReady:      false,
		dispatcherStatus:        deploymentStatusReady,
		setAddress:              true,
		wantReady:               false,
	}, {
		name:                    "deployment not ready",
		markServiceReady:        true,
		markEndpointsReady:      true,
		markChannelServiceReady: false,
		dispatcherStatus:        deploymentStatusNotReady,
		setAddress:              true,
		wantReady:               false,
	}, {
		name:                    "address not set",
		markServiceReady:        true,
		markChannelServiceReady: false,
		markEndpointsReady:      true,
		dispatcherStatus:        deploymentStatusReady,
		setAddress:              false,
		wantReady:               false,
	}, {
		name:                    "channel service not ready",
		markServiceReady:        true,
		markChannelServiceReady: false,
		markEndpointsReady:      true,
		dispatcherStatus:        deploymentStatusReady,
		setAddress:              true,
		wantReady:               false,
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cs := &NatsJetStreamChannelStatus{}
			cs.InitializeConditions()
			if test.markServiceReady {
				cs.MarkServiceTrue()
			} else {
				cs.MarkServiceFailed("NotReadyService", "testing")
			}
			if test.markChannelServiceReady {
				cs.MarkChannelServiceTrue()
			} else {
				cs.MarkChannelServiceFailed("NotReadyChannelService", "testing")
			}
			if test.setAddress {
				cs.SetAddress(&apis.URL{Scheme: "http", Host: "foo.bar"})
			}
			if test.markEndpointsReady {
				cs.MarkEndpointsTrue()
			} else {
				cs.MarkEndpointsFailed("NotReadyEndpoints", "testing")
			}
			if test.dispatcherStatus != nil {
				cs.PropagateDispatcherStatus(test.dispatcherStatus)
			} else {
				cs.MarkDispatcherFailed("NotReadyDispatcher", "testing")
			}
			got := cs.IsReady()
			if test.wantReady != got {
				t.Errorf("unexpected readiness: want %v, got %v", test.wantReady, got)
			}
		})
	}
}

func TestNatssChannelStatus_SetAddressable(t *testing.T) {
	testCases := map[string]struct {
		url  *apis.URL
		want *NatsJetStreamChannelStatus
	}{
		"empty string": {
			want: &NatsJetStreamChannelStatus{
				ChannelableStatus: eventingduckv1.ChannelableStatus{
					Status: duckv1.Status{
						Conditions: []apis.Condition{
							{
								Type:   NatssChannelConditionAddressable,
								Status: corev1.ConditionFalse,
							},
							// Note that Ready is here because when the condition is marked False, duck
							// automatically sets Ready to false.
							{
								Type:   NatssChannelConditionReady,
								Status: corev1.ConditionFalse,
							},
						},
					},
					AddressStatus: duckv1.AddressStatus{Address: &duckv1.Addressable{}},
				},
			},
		},
		"has domain": {
			url: &apis.URL{Scheme: "http", Host: "test-domain"},
			want: &NatsJetStreamChannelStatus{
				ChannelableStatus: eventingduckv1.ChannelableStatus{
					AddressStatus: duckv1.AddressStatus{
						Address: &duckv1.Addressable{
							URL: &apis.URL{
								Scheme: "http",
								Host:   "test-domain",
							},
						},
					},
					Status: duckv1.Status{
						Conditions: []apis.Condition{{
							Type:   NatssChannelConditionAddressable,
							Status: corev1.ConditionTrue,
						}, {
							// Ready unknown comes from other dependent conditions via MarkTrue.
							Type:   NatssChannelConditionReady,
							Status: corev1.ConditionUnknown,
						}},
					},
				},
			},
		},
	}
	for n, tc := range testCases {
		t.Run(n, func(t *testing.T) {
			cs := &NatsJetStreamChannelStatus{}
			cs.SetAddress(tc.url)
			if diff := cmp.Diff(tc.want, cs, ignoreAllButTypeAndStatus); diff != "" {
				t.Errorf("unexpected conditions (-want, +got) = %v", diff)
			}
		})
	}
}
