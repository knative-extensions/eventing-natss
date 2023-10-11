/*
Copyright 2019 The Knative Authors.

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

package controller

import (
	"context"
	"fmt"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgotesting "k8s.io/client-go/testing"
	kubeclient "knative.dev/pkg/client/injection/kube/client"
	"knative.dev/pkg/kmeta"
	"knative.dev/pkg/network"

	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	. "knative.dev/pkg/reconciler/testing"

	"knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	"knative.dev/eventing-natss/pkg/apis/messaging/v1beta1"
	"knative.dev/eventing-natss/pkg/channel/jetstream/controller/resources"
	"knative.dev/eventing-natss/pkg/client/clientset/versioned/scheme"
	fakeclientset "knative.dev/eventing-natss/pkg/client/injection/client/fake"
	"knative.dev/eventing-natss/pkg/client/injection/reconciler/messaging/v1alpha1/natsjetstreamchannel"
	reconciletesting "knative.dev/eventing-natss/pkg/reconciler/testing"
)

func init() {
	// Add types to scheme
	_ = v1beta1.AddToScheme(scheme.Scheme)
	_ = duckv1.AddToScheme(scheme.Scheme)
}

const (
	testNS                   = "test-namespace"
	ncName                   = "test-nc"
	dispatcherImage          = "test-image"
	dispatcherDeploymentName = "jetstream-ch-dispatcher"
	dispatcherServiceName    = "jetstream-ch-dispatcher"
	dispatcherServiceAccount = "test-service-account"
	channelServiceAddress    = "test-nc-kn-jsm-channel.test-namespace.svc.cluster.local"
)

var (
	finalizerUpdatedEvent = Eventf(
		v1.EventTypeNormal,
		"FinalizerUpdate",
		fmt.Sprintf(`Updated %q finalizers`, ncName),
	)
)

func TestAllCases(t *testing.T) {
	ncKey := testNS + "/" + ncName
	table := TableTest{
		{
			Name: "bad workqueue key",
			// Make sure Reconcile handles bad keys.
			Key: "too/many/parts",
		}, {
			Name: "key not found",
			// Make sure Reconcile handles good keys that don't exist.
			Key: "foo/not-found",
		}, {
			Name: "deleting",
			Key:  ncKey,
			Objects: []runtime.Object{
				reconciletesting.NewNatsJetStreamChannel(ncName, testNS,
					reconciletesting.WithNatsJetStreamInitChannelConditions,
					reconciletesting.WithNatsJetStreamChannelDeleted),
			},
			WantErr: false,
		}, {
			Name: "deployment does not exist",
			Key:  ncKey,
			Objects: []runtime.Object{
				reconciletesting.NewNatsJetStreamChannel(ncName, testNS),
			},
			WantStatusUpdates: []clientgotesting.UpdateActionImpl{{
				Object: reconciletesting.NewNatsJetStreamChannel(ncName, testNS,
					reconciletesting.WithNatsJetStreamInitChannelConditions,
					reconciletesting.WithNatsJetStreamChannelEndpointsNotReady(dispatcherEndpointsNotFound, "Dispatcher Endpoints does not exist"),
					reconciletesting.WithNatsJetStreamChannelServiceReady(),
				),
			}},
			WantCreates: []runtime.Object{
				makeDispatcherDeployment(),
				makeDispatcherService(),
			},
			WantPatches: []clientgotesting.PatchActionImpl{{
				Name:      ncName,
				Patch:     []byte(`{"metadata":{"finalizers":["natsjetstreamchannels.messaging.knative.dev"],"resourceVersion":""}}`),
				PatchType: types.MergePatchType,
			}},
			WantEvents: []string{
				finalizerUpdatedEvent,
				Eventf(v1.EventTypeNormal, dispatcherDeploymentCreated, "Dispatcher deployment created"),
				Eventf(v1.EventTypeNormal, dispatcherServiceCreated, "Dispatcher service created"),
			},
		}, {
			Name: "service does not exist",
			Key:  ncKey,
			Objects: []runtime.Object{
				makeReadyDispatcherDeployment(),
				reconciletesting.NewNatsJetStreamChannel(ncName, testNS),
			},
			WantStatusUpdates: []clientgotesting.UpdateActionImpl{{
				Object: reconciletesting.NewNatsJetStreamChannel(ncName, testNS,
					reconciletesting.WithNatsJetStreamInitChannelConditions,
					reconciletesting.WithNatsJetStreamChannelDispatcherReady(),
					reconciletesting.WithNatsJetStreamChannelEndpointsNotReady(dispatcherEndpointsNotFound, "Dispatcher Endpoints does not exist"),
					reconciletesting.WithNatsJetStreamChannelServiceReady(),
				),
			}},
			WantCreates: []runtime.Object{
				makeDispatcherService(),
			},
			WantPatches: []clientgotesting.PatchActionImpl{{
				Name:      ncName,
				Patch:     []byte(`{"metadata":{"finalizers":["natsjetstreamchannels.messaging.knative.dev"],"resourceVersion":""}}`),
				PatchType: types.MergePatchType,
			}},
			WantEvents: []string{
				finalizerUpdatedEvent,
				Eventf(v1.EventTypeNormal, dispatcherServiceCreated, "Dispatcher service created"),
			},
		}, {
			Name: "Endpoints does not exist",
			Key:  ncKey,
			Objects: []runtime.Object{
				makeReadyDispatcherDeployment(),
				makeDispatcherService(),
				reconciletesting.NewNatsJetStreamChannel(ncName, testNS),
			},
			WantStatusUpdates: []clientgotesting.UpdateActionImpl{{
				Object: reconciletesting.NewNatsJetStreamChannel(ncName, testNS,
					reconciletesting.WithNatsJetStreamInitChannelConditions,
					reconciletesting.WithNatsJetStreamChannelDispatcherReady(),
					reconciletesting.WithNatsJetStreamChannelEndpointsNotReady(dispatcherEndpointsNotFound, "Dispatcher Endpoints does not exist"),
					reconciletesting.WithNatsJetStreamChannelServiceReady(),
				),
			}},
			WantPatches: []clientgotesting.PatchActionImpl{{
				Name:      ncName,
				Patch:     []byte(`{"metadata":{"finalizers":["natsjetstreamchannels.messaging.knative.dev"],"resourceVersion":""}}`),
				PatchType: types.MergePatchType,
			}},
			WantEvents: []string{
				finalizerUpdatedEvent,
			},
		}, {
			Name: "Works, creates a new channel",
			Key:  ncKey,
			Objects: []runtime.Object{
				makeReadyDispatcherDeployment(),
				makeDispatcherService(),
				makeReadyEndpoints(),
				reconciletesting.NewNatsJetStreamChannel(ncName, testNS),
			},
			WantStatusUpdates: []clientgotesting.UpdateActionImpl{{
				Object: reconciletesting.NewNatsJetStreamChannel(ncName, testNS,
					reconciletesting.WithNatsJetStreamInitChannelConditions,
					reconciletesting.WithNatsJetStreamChannelAddress(channelServiceAddress),
					reconciletesting.JetStreamAddressable(),
					reconciletesting.WithNatsJetStreamChannelDispatcherReady(),
					reconciletesting.WithNatsJetStreamChannelEndpointsReady(),
					reconciletesting.WithNatsJetStreamChannelServiceReady(),
					reconciletesting.WithNatsJetStreamChannelChannelServiceReady(),
				),
			}},
			WantCreates: []runtime.Object{
				makeChannelService(reconciletesting.NewNatsJetStreamChannel(ncName, testNS)),
			},
			WantPatches: []clientgotesting.PatchActionImpl{{
				Name:      ncName,
				Patch:     []byte(`{"metadata":{"finalizers":["natsjetstreamchannels.messaging.knative.dev"],"resourceVersion":""}}`),
				PatchType: types.MergePatchType,
			}},
			WantEvents: []string{
				finalizerUpdatedEvent,
			},
		}, {
			Name: "Works, channel exists",
			Key:  ncKey,
			Objects: []runtime.Object{
				makeReadyDispatcherDeployment(),
				makeDispatcherService(),
				makeReadyEndpoints(),
				reconciletesting.NewNatsJetStreamChannel(ncName, testNS),
				makeChannelService(reconciletesting.NewNatsJetStreamChannel(ncName, testNS)),
			},
			WantStatusUpdates: []clientgotesting.UpdateActionImpl{{
				Object: reconciletesting.NewNatsJetStreamChannel(ncName, testNS,
					reconciletesting.WithNatsJetStreamInitChannelConditions,
					reconciletesting.WithNatsJetStreamChannelAddress(channelServiceAddress),
					reconciletesting.JetStreamAddressable(),
					reconciletesting.WithNatsJetStreamChannelDispatcherReady(),
					reconciletesting.WithNatsJetStreamChannelEndpointsReady(),
					reconciletesting.WithNatsJetStreamChannelServiceReady(),
					reconciletesting.WithNatsJetStreamChannelChannelServiceReady(),
				),
			}},
			WantPatches: []clientgotesting.PatchActionImpl{{
				Name:      ncName,
				Patch:     []byte(`{"metadata":{"finalizers":["natsjetstreamchannels.messaging.knative.dev"],"resourceVersion":""}}`),
				PatchType: types.MergePatchType,
			}},
			WantEvents: []string{
				finalizerUpdatedEvent,
			},
		}, {
			Name: "Works, channel with deployment spec",
			Key:  ncKey,
			Objects: []runtime.Object{
				reconciletesting.NewNatsJetStreamChannel(ncName, testNS,
					reconciletesting.WithNatsJetStreamDeploymentSpecTemplate),
			},
			WantStatusUpdates: []clientgotesting.UpdateActionImpl{{
				Object: reconciletesting.NewNatsJetStreamChannel(ncName, testNS,
					reconciletesting.WithNatsJetStreamInitChannelConditions,
					reconciletesting.WithNatsJetStreamChannelEndpointsNotReady(dispatcherEndpointsNotFound, "Dispatcher Endpoints does not exist"),
					reconciletesting.WithNatsJetStreamChannelServiceReady(),
					reconciletesting.WithNatsJetStreamDeploymentSpecTemplate,
				),
			}},
			WantCreates: []runtime.Object{
				makeDispatcherDeploymentWithSpec(),
				makeDispatcherService(),
			},
			WantPatches: []clientgotesting.PatchActionImpl{{
				Name:      ncName,
				Patch:     []byte(`{"metadata":{"finalizers":["natsjetstreamchannels.messaging.knative.dev"],"resourceVersion":""}}`),
				PatchType: types.MergePatchType,
			}},
			WantEvents: []string{
				finalizerUpdatedEvent,
				Eventf(v1.EventTypeNormal, dispatcherDeploymentCreated, "Dispatcher deployment created"),
				Eventf(v1.EventTypeNormal, dispatcherServiceCreated, "Dispatcher service created"),
			},
		},
	}

	table.Test(t, reconciletesting.MakeFactory(func(ctx context.Context, listers *reconciletesting.Listers) controller.Reconciler {
		r := &Reconciler{
			kubeClientSet:            kubeclient.Get(ctx),
			systemNamespace:          testNS,
			dispatcherImage:          dispatcherImage,
			dispatcherServiceAccount: dispatcherServiceAccount,
			deploymentLister:         listers.GetDeploymentLister(),
			serviceLister:            listers.GetServiceLister(),
			endpointsLister:          listers.GetEndpointsLister(),
			serviceAccountLister:     listers.GetServiceAccountLister(),
			roleBindingLister:        listers.GetRoleBindingLister(),
			jsmChannelLister:         listers.GetNatsJetstreamChannelLister(),
			// TODO: Figure out controllerRef
			// controllerRef:            v1.OwnerReference{},
		}
		return natsjetstreamchannel.NewReconciler(ctx, logging.FromContext(ctx),
			fakeclientset.Get(ctx), listers.GetNatsJetstreamChannelLister(),
			controller.GetEventRecorder(ctx),
			r)
	}))
}

func makeDispatcherDeployment() *appsv1.Deployment {
	return resources.NewDispatcherDeploymentBuilder().WithArgs(&resources.DispatcherDeploymentArgs{
		DispatcherScope:     "",
		DispatcherNamespace: testNS,
		Image:               dispatcherImage,
		Replicas:            1,
		ServiceAccount:      dispatcherServiceAccount,
		OwnerRef:            metav1.OwnerReference{}, // TODO: Make this work
	}).Build()
}

func makeDispatcherDeploymentWithSpec() *appsv1.Deployment {
	return resources.NewDispatcherDeploymentBuilder().WithArgs(&resources.DispatcherDeploymentArgs{
		DispatcherScope:     "",
		DispatcherNamespace: testNS,
		Image:               dispatcherImage,
		Replicas:            1,
		ServiceAccount:      dispatcherServiceAccount,
		OwnerRef:            metav1.OwnerReference{}, // TODO: Make this work
		DeploymentLabels: map[string]string{
			"label1": "value",
		},
		DeploymentAnnotations: map[string]string{
			"annotation1": "value",
		},
	}).Build()
}

func makeEmptyEndpoints() *v1.Endpoints {
	return &v1.Endpoints{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Endpoints",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNS,
			Name:      dispatcherServiceName,
		},
	}
}

func makeReadyEndpoints() *v1.Endpoints {
	e := makeEmptyEndpoints()
	e.Subsets = []v1.EndpointSubset{{Addresses: []v1.EndpointAddress{{IP: "1.1.1.1"}}}}
	return e
}

func makeReadyDispatcherDeployment() *appsv1.Deployment {
	d := makeDispatcherDeployment()
	d.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: v1.ConditionTrue}}
	return d
}

func makeDispatcherService() *v1.Service {
	return resources.NewDispatcherServiceBuilder().WithArgs(&resources.DispatcherServiceArgs{
		DispatcherNamespace: testNS,
	}).Build()
}

func makeChannelService(nc *v1alpha1.NatsJetStreamChannel) *v1.Service {
	return &v1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNS,
			Name:      fmt.Sprintf("%s-kn-jsm-channel", ncName),
			Labels: map[string]string{
				resources.MessagingRoleLabel: resources.MessagingRole,
			},
			OwnerReferences: []metav1.OwnerReference{
				*kmeta.NewControllerRef(nc),
			},
		},
		Spec: v1.ServiceSpec{
			Type:         v1.ServiceTypeExternalName,
			ExternalName: network.GetServiceHostname(dispatcherServiceName, testNS),
		},
	}
}
