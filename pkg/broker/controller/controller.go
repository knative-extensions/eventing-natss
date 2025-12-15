package controller

import (
	"context"

	"k8s.io/client-go/tools/cache"
	"knative.dev/eventing/pkg/apis/eventing"
	"knative.dev/eventing/pkg/client/injection/reconciler/eventing/v1/broker"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"

	"knative.dev/pkg/reconciler"

	brokerinformer "knative.dev/eventing/pkg/client/injection/informers/eventing/v1/broker"
	deploymentinformer "knative.dev/pkg/client/injection/kube/informers/apps/v1/deployment"
	endpointsinformer "knative.dev/pkg/client/injection/kube/informers/core/v1/endpoints"
	secretinformer "knative.dev/pkg/client/injection/kube/informers/core/v1/secret"
	serviceinformer "knative.dev/pkg/client/injection/kube/informers/core/v1/service"
)

const (
	ComponentName = "natsjs-broker-controller"
)

const BrokerClass = "NatsJetStreamBroker"

// const (
// 	BrokerConditionExchange       apis.ConditionType = "ExchangeReady"
// 	BrokerConditionDLX            apis.ConditionType = "DLXReady"
// 	BrokerConditionDeadLetterSink apis.ConditionType = "DeadLetterSinkReady"
// 	BrokerConditionSecret         apis.ConditionType = "SecretReady"
// 	BrokerConditionIngress        apis.ConditionType = "IngressReady"
// 	BrokerConditionAddressable    apis.ConditionType = "Addressable"
// )

// var natsJSBrokerCondSet = apis.NewLivingConditionSet(
// 	BrokerConditionExchange,
// 	BrokerConditionDLX,
// 	BrokerConditionDeadLetterSink,
// 	BrokerConditionSecret,
// 	BrokerConditionIngress,
// 	BrokerConditionAddressable,
// )

func NewController(
	ctx context.Context,
	cmw configmap.Watcher,
) *controller.Impl {

	// eventingv1.RegisterAlternateBrokerConditionSet(natsJSBrokerCondSet)

	// var env envConfig
	// if err := envconfig.Process("", &env); err != nil {
	// 	log.Fatal("Failed to process env var", zap.Error(err))
	// }

	secretInformer := secretinformer.Get(ctx)
	deploymentInformer := deploymentinformer.Get(ctx)
	brokerInformer := brokerinformer.Get(ctx)
	serviceInformer := serviceinformer.Get(ctx)
	endpointsInformer := endpointsinformer.Get(ctx)
	// exchangeInformer := exchangeinformer.Get(ctx)
	// policyInformer := policyinformer.Get(ctx)
	// queueInformer := queueinformer.Get(ctx)
	// bindingInformer := bindinginformer.Get(ctx)
	// configmapInformer := configmapinformer.Get(ctx)

	r := &Reconciler{}

	ctrl := broker.NewImpl(ctx, r, BrokerClass)

	brokerInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: reconciler.AnnotationFilterFunc(broker.ClassAnnotationKey, BrokerClass, false /*allowUnset*/),
		Handler:    controller.HandleAll(ctrl.Enqueue),
	})

	serviceInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controller.FilterControllerGK(eventingv1.Kind("Broker")),
		Handler:    controller.HandleAll(ctrl.EnqueueControllerOf),
	})

	secretInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controller.FilterControllerGK(eventingv1.Kind("Broker")),
		Handler:    controller.HandleAll(ctrl.EnqueueControllerOf),
	})

	endpointsInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: reconciler.LabelExistsFilterFunc(eventing.BrokerLabelKey),
		Handler:    controller.HandleAll(ctrl.EnqueueLabelOfNamespaceScopedResource("" /*any namespace*/, eventing.BrokerLabelKey)),
	})

	deploymentInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controller.FilterControllerGK(eventingv1.Kind("Broker")),
		Handler:    controller.HandleAll(ctrl.EnqueueControllerOf),
	})

	// exchangeInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
	// 	FilterFunc: controller.FilterControllerGK(eventingv1.Kind("Broker")),
	// 	Handler:    controller.HandleAll(impl.EnqueueControllerOf),
	// })
	// policyInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
	// 	FilterFunc: controller.FilterControllerGK(eventingv1.Kind("Broker")),
	// 	Handler:    controller.HandleAll(impl.EnqueueControllerOf),
	// })
	// queueInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
	// 	FilterFunc: controller.FilterControllerGK(eventingv1.Kind("Broker")),
	// 	Handler:    controller.HandleAll(impl.EnqueueControllerOf),
	// })
	// bindingInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
	// 	FilterFunc: controller.FilterControllerGK(eventingv1.Kind("Broker")),
	// 	Handler:    controller.HandleAll(impl.EnqueueControllerOf),
	// })
	// configmapInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
	// 	FilterFunc: utils.SystemConfigMapsFilterFunc(),
	// 	Handler:    controller.HandleAll(func(interface{}) { ctrl.GlobalResync(brokerInformer.Informer()) }),
	// })

	return ctrl
}
