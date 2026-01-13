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

package controller

import (
	"context"

	"github.com/kelseyhightower/envconfig"
	"go.uber.org/zap"
	"k8s.io/client-go/tools/cache"

	kubeclient "knative.dev/pkg/client/injection/kube/client"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	pkgreconciler "knative.dev/pkg/reconciler"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	"knative.dev/eventing/pkg/client/injection/reconciler/eventing/v1/broker"

	brokerinformer "knative.dev/eventing/pkg/client/injection/informers/eventing/v1/broker"
	deploymentinformer "knative.dev/pkg/client/injection/kube/informers/apps/v1/deployment"
	serviceinformer "knative.dev/pkg/client/injection/kube/informers/core/v1/service"

	"knative.dev/eventing-natss/pkg/broker/constants"
	"knative.dev/eventing-natss/pkg/common/configloader/fsloader"
	commonnats "knative.dev/eventing-natss/pkg/common/nats"
)

const (
	// ComponentName is the name of this controller component
	ComponentName = "natsjs-broker-controller"
)

// envConfig holds configuration from environment variables
type envConfig struct {
	IngressImage          string `envconfig:"BROKER_INGRESS_IMAGE" required:"true"`
	FilterImage           string `envconfig:"BROKER_FILTER_IMAGE" required:"true"`
	IngressServiceAccount string `envconfig:"BROKER_INGRESS_SERVICE_ACCOUNT" default:"natsjs-broker-ingress"`
	FilterServiceAccount  string `envconfig:"BROKER_FILTER_SERVICE_ACCOUNT" default:"natsjs-broker-filter"`
}

// NewController creates a new controller for the NATS JetStream Broker
func NewController(
	ctx context.Context,
	cmw configmap.Watcher,
) *controller.Impl {
	logger := logging.FromContext(ctx)

	// Load environment configuration
	var env envConfig
	if err := envconfig.Process("", &env); err != nil {
		logger.Fatalw("Failed to process environment variables", zap.Error(err))
	}

	logger.Infow("Broker controller configuration",
		zap.String("ingress_image", env.IngressImage),
		zap.String("filter_image", env.FilterImage),
		zap.String("ingress_service_account", env.IngressServiceAccount),
		zap.String("filter_service_account", env.FilterServiceAccount),
	)

	// Get the config loader from context
	fsLoader, err := fsloader.Get(ctx)
	if err != nil {
		logger.Fatalw("Failed to get ConfigmapLoader from context", zap.Error(err))
	}

	// Load NATS configuration from mounted ConfigMap
	configMap, err := fsLoader(constants.SettingsConfigMapMountPath)
	if err != nil {
		logger.Fatalw("Failed to load NATS configmap", zap.Error(err))
	}

	// Parse NATS configuration
	natsConfig, err := commonnats.LoadEventingNatsConfig(configMap)
	if err != nil {
		logger.Fatalw("Failed to parse NATS configuration", zap.Error(err))
	}

	// Create NATS connection
	natsConn, err := commonnats.NewNatsConn(ctx, natsConfig)
	if err != nil {
		logger.Fatalw("Failed to create NATS connection", zap.Error(err))
	}

	// Create JetStream context
	js, err := natsConn.JetStream()
	if err != nil {
		logger.Fatalw("Failed to create JetStream context", zap.Error(err))
	}

	// Get informers
	brokerInformer := brokerinformer.Get(ctx)
	deploymentInformer := deploymentinformer.Get(ctx)
	serviceInformer := serviceinformer.Get(ctx)

	// Create reconciler
	r := &Reconciler{
		kubeClientSet: kubeclient.Get(ctx),

		deploymentLister: deploymentInformer.Lister(),
		serviceLister:    serviceInformer.Lister(),

		js: js,

		natsURL: natsConfig.URL,

		ingressImage:          env.IngressImage,
		filterImage:           env.FilterImage,
		ingressServiceAccount: env.IngressServiceAccount,
		filterServiceAccount:  env.FilterServiceAccount,
	}

	// Create controller implementation
	impl := broker.NewImpl(ctx, r, constants.BrokerClassName)

	// Set up event handlers for Broker resources
	brokerInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: pkgreconciler.AnnotationFilterFunc(broker.ClassAnnotationKey, constants.BrokerClassName, false /*allowUnset*/),
		Handler:    controller.HandleAll(impl.Enqueue),
	})

	// Watch deployments owned by brokers
	deploymentInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controller.FilterControllerGK(eventingv1.Kind("Broker")),
		Handler:    controller.HandleAll(impl.EnqueueControllerOf),
	})

	// Watch services owned by brokers
	serviceInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controller.FilterControllerGK(eventingv1.Kind("Broker")),
		Handler:    controller.HandleAll(impl.EnqueueControllerOf),
	})

	logger.Info("NATS JetStream Broker controller initialized")

	return impl
}
