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

package dispatcher

import (
	"context"

	"github.com/kelseyhightower/envconfig"
	"go.uber.org/zap"
	"k8s.io/client-go/tools/cache"
	"knative.dev/eventing-natss/pkg/channel/jetstream/utils"
	clientinject "knative.dev/eventing-natss/pkg/client/injection/client"
	jsminformer "knative.dev/eventing-natss/pkg/client/injection/informers/messaging/v1alpha1/natsjetstreamchannel"
	"knative.dev/eventing-natss/pkg/common/configloader/fsloader"
	"knative.dev/eventing/pkg/apis/eventing"
	messagingv1client "knative.dev/eventing/pkg/client/injection/client"
	"knative.dev/pkg/injection"
	pkgreconciler "knative.dev/pkg/reconciler"

	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/tracing"

	"knative.dev/eventing-natss/pkg/client/injection/reconciler/messaging/v1alpha1/natsjetstreamchannel"
	"knative.dev/eventing-natss/pkg/common/constants"
	commonnats "knative.dev/eventing-natss/pkg/common/nats"
)

const (
	// controllerAgentName is the string used by this controller to identify
	// itself when creating events.
	controllerAgentName = "jetstream-ch-dispatcher"

	finalizerName = controllerAgentName
)

func NewController(ctx context.Context, cmw configmap.Watcher) *controller.Impl {
	logger := logging.FromContext(ctx)

	env := &envConfig{}
	if err := envconfig.Process("", env); err != nil {
		logger.Panicf("unable to process required environment variables: %v", err)
	}

	_, err := tracing.SetupPublishingWithDynamicConfig(logger, cmw, "jetstream-ch-dispatcher", "config-tracing")
	if err != nil {
		logger.Fatalw("failed to setup tracing", zap.Error(err))
	}

	fsLoader, err := fsloader.Get(ctx)
	if err != nil {
		logger.Fatalw("failed to get ConfigmapLoader from context... terminating", zap.Error(err))
	}

	configMap, err := fsLoader(constants.SettingsConfigMapMountPath)
	if err != nil {
		logger.Fatalw("failed to load configmap", zap.Error(err))
	}

	natsConfig, err := commonnats.LoadEventingNatsConfig(configMap)
	if err != nil {
		logger.Fatalw("failed to load configmap into struct", zap.Error(err))
	}

	conn, err := commonnats.NewNatsConn(ctx, natsConfig)

	if err != nil {
		logger.Fatalw("failed to establish nats connection", zap.Error(err))
	}

	js, err := conn.JetStream()
	if err != nil {
		// this shouldn't happen whilst no options are passed to conn.JetStream()
		logger.Fatalw("failed to switch to JetStream context", zap.Error(err))
	}

	dispatcher, err := NewDispatcher(ctx, NatsDispatcherArgs{
		JetStream:           js,
		SubjectFunc:         utils.PublishSubjectName,
		ConsumerNameFunc:    utils.ConsumerName,
		ConsumerSubjectFunc: utils.ConsumerSubjectName,
		PodName:             env.PodName,
		ContainerName:       env.ContainerName,
	})
	if err != nil {
		logger.Fatalw("failed to create dispatcher", zap.Error(err))
	}

	r := &Reconciler{
		msgingClient:     messagingv1client.Get(ctx),
		clientSet:        clientinject.Get(ctx),
		js:               js,
		dispatcher:       dispatcher,
		streamNameFunc:   utils.StreamName,
		consumerNameFunc: dispatcher.consumerNameFunc,
	}

	impl := natsjetstreamchannel.NewImpl(ctx, r)

	jsmInformer := jsminformer.Get(ctx)

	logger.Info("setting up event handlers")
	jsmInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: filterWithAnnotation(injection.HasNamespaceScope(ctx)),
		Handler:    controller.HandleAll(impl.Enqueue),
	})

	logger.Info("starting dispatcher")
	go func() {
		if err := dispatcher.Start(ctx); err != nil {
			logger.Errorw("dispatcher execution failed", zap.Error(err))
		}
	}()

	return impl
}

func filterWithAnnotation(namespaced bool) func(obj interface{}) bool {
	if namespaced {
		return pkgreconciler.AnnotationFilterFunc(eventing.ScopeAnnotationKey, "namespace", false)
	}
	return pkgreconciler.AnnotationFilterFunc(eventing.ScopeAnnotationKey, "cluster", true)
}
