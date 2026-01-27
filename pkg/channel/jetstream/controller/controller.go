/*
Copyright 2021 The Knative Authors

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
	"time"

	"k8s.io/utils/ptr"

	"knative.dev/pkg/configmap"
	"knative.dev/pkg/reconciler"

	"knative.dev/eventing-natss/pkg/channel/jetstream"
	"knative.dev/eventing-natss/pkg/channel/jetstream/controller/resources"

	"github.com/kelseyhightower/envconfig"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	kubeclient "knative.dev/pkg/client/injection/kube/client"
	deploymentinformer "knative.dev/pkg/client/injection/kube/informers/apps/v1/deployment"
	podinformer "knative.dev/pkg/client/injection/kube/informers/core/v1/pod"
	serviceinformer "knative.dev/pkg/client/injection/kube/informers/core/v1/service"
	serviceaccountinformer "knative.dev/pkg/client/injection/kube/informers/core/v1/serviceaccount"
	rolebindinginformer "knative.dev/pkg/client/injection/kube/informers/rbac/v1/rolebinding"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/system"

	"knative.dev/eventing-natss/pkg/client/injection/informers/messaging/v1alpha1/natsjetstreamchannel"
	jsmreconciler "knative.dev/eventing-natss/pkg/client/injection/reconciler/messaging/v1alpha1/natsjetstreamchannel"
)

const (
	interval = 100 * time.Millisecond
	timeout  = 5 * time.Minute
)

// NewController initializes the controller and is called by the generated code.
// Registers event handlers to enqueue events.
func NewController(ctx context.Context, _ configmap.Watcher) *controller.Impl {
	logger := logging.FromContext(ctx)

	env := &envConfig{}
	if err := envconfig.Process("", env); err != nil {
		logger.Panicf("unable to process required environment variables: %v", err)
	}

	// get the ref of the controller deployment
	ownerRef, err := getControllerOwnerRef(ctx)
	if err != nil {
		logger.Panic("Could not determine the proper owner reference for the dispatcher deployment.", zap.Error(err))
	}

	jsmInformer := natsjetstreamchannel.Get(ctx)
	deploymentInformer := deploymentinformer.Get(ctx)
	serviceInformer := serviceinformer.Get(ctx)
	serviceAccountInformer := serviceaccountinformer.Get(ctx)
	roleBindingInformer := rolebindinginformer.Get(ctx)
	podInformer := podinformer.Get(ctx)
	jsmChannelInformer := natsjetstreamchannel.Get(ctx)

	kubeClient := kubeclient.Get(ctx)

	r := &Reconciler{
		kubeClientSet:            kubeClient,
		systemNamespace:          system.Namespace(),
		dispatcherImage:          env.Image,
		dispatcherServiceAccount: env.DispatcherServiceAccount,
		deploymentLister:         deploymentInformer.Lister(),
		serviceLister:            serviceInformer.Lister(),
		serviceAccountLister:     serviceAccountInformer.Lister(),
		roleBindingLister:        roleBindingInformer.Lister(),
		jsmChannelLister:         jsmChannelInformer.Lister(),
		controllerRef:            *ownerRef,
	}

	impl := jsmreconciler.NewImpl(ctx, r)

	logger.Info("Setting up event handlers")
	jsmInformer.Informer().AddEventHandler(controller.HandleAll(impl.Enqueue))

	grCh := func(v interface{}) {
		logger.Debug("Changes detected, doing global resync", zap.String("trigger", fmt.Sprintf("%v", v)))
		impl.GlobalResync(jsmInformer.Informer())
	}
	filterFunc := controller.FilterWithName(jetstream.DispatcherName)

	// Set up watches for dispatcher resources we care about, since any changes to these
	// resources will affect our Channels. So, set up a watch here, that will cause
	// a global Resync for all the channels to take stock of their health when these change.
	deploymentInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: filterFunc,
		Handler:    controller.HandleAll(grCh),
	})
	serviceInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: filterFunc,
		Handler:    controller.HandleAll(grCh),
	})
	serviceAccountInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: filterFunc,
		Handler:    controller.HandleAll(grCh),
	})
	roleBindingInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: filterFunc,
		Handler:    controller.HandleAll(grCh),
	})
	podInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: reconciler.ChainFilterFuncs(
			reconciler.LabelFilterFunc(resources.ChannelLabelKey, resources.ChannelLabelValue, false),
			reconciler.LabelFilterFunc(resources.RoleLabelKey, resources.DispatcherRoleLabelValue, false),
		),
		Handler: controller.HandleAll(grCh),
	})

	return impl
}

// getControllerOwnerRef builds a *metav1.OwnerReference of the deployment for this controller instance. This is then
// used by the Reconciler for adding owner references to the dispatcher deployments.
func getControllerOwnerRef(ctx context.Context) (*metav1.OwnerReference, error) {
	ownerRef := metav1.OwnerReference{
		APIVersion: "rbac.authorization.k8s.io/v1",
		Kind:       "ClusterRole",
		Controller: ptr.To(true),
	}
	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (done bool, err error) {
		k8sClient := kubeclient.Get(ctx)
		clusterRole, err := k8sClient.RbacV1().ClusterRoles().Get(ctx, jetstream.ControllerName, metav1.GetOptions{})
		if err != nil {
			return true, fmt.Errorf("error retrieving ClusterRole for NatsJetStreamChannel: %w", err)
		}
		ownerRef.Name = clusterRole.Name
		ownerRef.UID = clusterRole.UID
		return true, nil
	})

	if err != nil {
		return nil, err
	}
	return &ownerRef, nil
}
