package controller

import (
	"context"

	"go.uber.org/zap"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/pointer"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/network"
	pkgreconciler "knative.dev/pkg/reconciler"

	"knative.dev/eventing-rabbitmq/pkg/reconciler/broker/resources"
	"knative.dev/eventing-rabbitmq/pkg/utils"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

type Reconciler struct{}

func (r *Reconciler) ReconcileKind(ctx context.Context, b *eventingv1.Broker) pkgreconciler.Event {
	log := logging.FromContext(ctx)
	log.Infow("Reconciling", zap.Any("Broker", b))

	if err := r.reconcileIngressDeployment(ctx, b); err != nil {
		logging.FromContext(ctx).Errorw("Problem reconciling ingress Deployment", zap.Error(err))
		bs := &b.Status
		bs.GetConditionSet().Manage(bs).MarkFalse(eventingv1.BrokerConditionIngress, "DeploymentFailure", "Failed to reconcile deployment: %s", err)
		return err
	}

	ingressEndpoints, err := r.reconcileIngressService(ctx, b)
	if err != nil {
		logging.FromContext(ctx).Errorw("Problem reconciling ingress Service", zap.Error(err))
		bs := &b.Status
		bs.GetConditionSet().Manage(bs).MarkFalse(eventingv1.BrokerConditionIngress, "ServiceFailure", "Failed to reconcile service: %s", err)
		return err
	}
	//TODO PropagateIngressAvailability(&b.Status, ingressEndpoints)

	b.Status.SetAddress(&duckv1.Addressable{
		Name: pointer.String("http"),
		URL: &apis.URL{
			Scheme: "http",
			Host:   network.GetServiceHostname(ingressEndpoints.GetName(), ingressEndpoints.GetNamespace()),
		},
	})

	return nil
}

func (r *Reconciler) reconcileIngressService(ctx context.Context, b *eventingv1.Broker) (*discoveryv1.EndpointSlice, error) {
	expected := resources.MakeIngressService(b)
	return r.reconcileService(ctx, expected)
}

func (r *Reconciler) reconcileService(ctx context.Context, svc *corev1.Service) (*corev1.Endpoints, error) {
	current, err := r.serviceLister.Services(svc.Namespace).Get(svc.Name)
	if apierrs.IsNotFound(err) {
		current, err = r.kubeClientSet.CoreV1().Services(svc.Namespace).Create(ctx, svc, metav1.CreateOptions{})
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	// spec.clusterIP is immutable and is set on existing services. If we don't set this to the same value, we will
	// encounter an error while updating.
	svc.Spec.ClusterIP = current.Spec.ClusterIP
	if !equality.Semantic.DeepDerivative(svc.Spec, current.Spec) {
		// Don't modify the informers copy.
		desired := current.DeepCopy()
		desired.Spec = svc.Spec
		if _, err = r.kubeClientSet.CoreV1().Services(current.Namespace).Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
			return nil, err
		}
	}

	return r.endpointsLister.Endpoints(svc.Namespace).Get(svc.Name)
}

func (r *Reconciler) reconcileIngressDeployment(ctx context.Context, b *eventingv1.Broker, rabbitmqVhost string) error {
	clusterRef, err := r.brokerConfig.GetRabbitMQClusterRef(ctx, b)
	if err != nil {
		return err
	}
	secretName, err := r.rabbit.GetRabbitMQCASecret(ctx, clusterRef)
	if err != nil {
		return err
	}
	resourceRequirements, err := utils.GetResourceRequirements(b.ObjectMeta)
	if err != nil {
		return err
	}
	expected := resources.MakeIngressDeployment(&resources.IngressArgs{
		Broker:               b,
		Image:                r.ingressImage,
		RabbitMQSecretName:   rabbit.SecretName(b.Name, "broker"),
		RabbitMQCASecretName: secretName,
		BrokerUrlSecretKey:   rabbit.BrokerURLSecretKey,
		RabbitMQVhost:        rabbitmqVhost,
		Configs:              r.configs,
		ResourceRequirements: resourceRequirements,
	})
	return r.reconcileDeployment(ctx, expected)
}
