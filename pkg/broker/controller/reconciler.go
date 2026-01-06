/*
Copyright 2024 The Knative Authors

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
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/utils/ptr"

	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/network"
	pkgreconciler "knative.dev/pkg/reconciler"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"

	messagingv1alpha1 "knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	brokerconfig "knative.dev/eventing-natss/pkg/broker/config"
	"knative.dev/eventing-natss/pkg/broker/controller/resources"
	brokerutils "knative.dev/eventing-natss/pkg/broker/utils"
)

const (
	// Event reasons
	ReasonIngressDeploymentCreated = "IngressDeploymentCreated"
	ReasonIngressDeploymentUpdated = "IngressDeploymentUpdated"
	ReasonIngressDeploymentFailed  = "IngressDeploymentFailed"
	ReasonIngressServiceCreated    = "IngressServiceCreated"
	ReasonIngressServiceFailed     = "IngressServiceFailed"
	ReasonFilterDeploymentCreated  = "FilterDeploymentCreated"
	ReasonFilterDeploymentUpdated  = "FilterDeploymentUpdated"
	ReasonFilterDeploymentFailed   = "FilterDeploymentFailed"
	ReasonFilterServiceCreated     = "FilterServiceCreated"
	ReasonFilterServiceFailed      = "FilterServiceFailed"
	ReasonStreamCreated            = "JetStreamStreamCreated"
	ReasonStreamFailed             = "JetStreamStreamFailed"
)

// Reconciler implements controller.Reconciler for Broker resources.
type Reconciler struct {
	kubeClientSet kubernetes.Interface

	// Listers for Kubernetes resources
	deploymentLister appsv1listers.DeploymentLister
	serviceLister    corev1listers.ServiceLister

	// NATS JetStream connection
	js nats.JetStreamContext

	// NATS URL for data plane components
	natsURL string

	// Image configuration
	ingressImage          string
	filterImage           string
	ingressServiceAccount string
	filterServiceAccount  string
}

// ReconcileKind implements Interface.ReconcileKind
func (r *Reconciler) ReconcileKind(ctx context.Context, b *eventingv1.Broker) pkgreconciler.Event {
	logger := logging.FromContext(ctx)
	logger.Infow("Reconciling broker", zap.String("broker", b.Name), zap.String("namespace", b.Namespace))

	// Get stream name for this broker
	streamName := brokerutils.BrokerStreamName(b)

	// Load broker configuration (once for the entire reconciliation)
	brokerCfg, err := r.getBrokerConfig(ctx, b)
	if err != nil {
		logger.Errorw("Failed to get broker config", zap.Error(err))
		b.Status.MarkIngressFailed("ConfigLoadFailed", "Failed to load broker configuration: %v", err)
		return fmt.Errorf("failed to get broker config: %w", err)
	}

	// Step 1: Reconcile JetStream stream
	if err := r.reconcileStream(ctx, b, streamName, brokerCfg); err != nil {
		return err
	}

	// Step 2: Reconcile ingress deployment
	if err := r.reconcileIngressDeployment(ctx, b, streamName, brokerCfg); err != nil {
		return err
	}

	// Step 3: Reconcile ingress service
	ingressService, err := r.reconcileIngressService(ctx, b)
	if err != nil {
		return err
	}

	// Step 4: Check ingress deployment readiness
	if err := r.propagateIngressAvailability(ctx, b, ingressService); err != nil {
		return err
	}

	// Step 5: Reconcile filter deployment
	if err := r.reconcileFilterDeployment(ctx, b, streamName, brokerCfg); err != nil {
		return err
	}

	// Step 6: Reconcile filter service
	filterService, err := r.reconcileFilterService(ctx, b)
	if err != nil {
		return err
	}

	// Step 7: Check filter deployment readiness
	if err := r.propagateFilterAvailability(ctx, b, filterService); err != nil {
		return err
	}

	// Step 8: Set broker address
	b.Status.SetAddress(&duckv1.Addressable{
		Name: ptr.To("http"),
		URL: &apis.URL{
			Scheme: "http",
			Host:   network.GetServiceHostname(ingressService.Name, ingressService.Namespace),
		},
	})

	// Step 9: Mark TriggerChannel as ready (we use JetStream instead of a channel)
	b.Status.GetConditionSet().Manage(&b.Status).MarkTrue(eventingv1.BrokerConditionTriggerChannel)

	// Step 10: Mark DeadLetterSink condition
	if b.Spec.Delivery == nil || b.Spec.Delivery.DeadLetterSink == nil {
		b.Status.MarkDeadLetterSinkNotConfigured()
	} else {
		// TODO: Resolve dead letter sink URI and mark as succeeded
		b.Status.MarkDeadLetterSinkNotConfigured()
	}

	// Step 11: Mark EventPolicies as ready (not using OIDC authentication)
	b.Status.MarkEventPoliciesTrueWithReason("EventPoliciesSkipped", "Feature %q is disabled", "OIDC")

	logger.Infow("Broker reconciliation completed successfully", zap.String("broker", b.Name))
	return nil
}

// reconcileStream ensures the JetStream stream exists for the broker
func (r *Reconciler) reconcileStream(ctx context.Context, b *eventingv1.Broker, streamName string, brokerCfg *messagingv1alpha1.NatsJetStreamBrokerConfig) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	publishSubject := brokerutils.BrokerPublishSubjectName(b.Namespace, b.Name)

	// Check if stream exists
	_, err := r.js.StreamInfo(streamName)
	if err != nil {
		if !errors.Is(err, nats.ErrStreamNotFound) {
			logger.Errorw("Failed to get stream info", zap.Error(err), zap.String("stream", streamName))
			b.Status.MarkIngressFailed("StreamInfoFailed", "Failed to get JetStream stream info: %v", err)
			return fmt.Errorf("failed to get stream info: %w", err)
		}

		// Stream doesn't exist, create it
		streamConfig := brokerconfig.BuildNatsStreamConfig(streamName, publishSubject, brokerCfg)

		_, err = r.js.AddStream(streamConfig)
		if err != nil {
			logger.Errorw("Failed to create JetStream stream", zap.Error(err), zap.String("stream", streamName))
			controller.GetEventRecorder(ctx).Event(b, corev1.EventTypeWarning, ReasonStreamFailed, err.Error())
			b.Status.MarkIngressFailed("StreamCreationFailed", "Failed to create JetStream stream: %v", err)
			return fmt.Errorf("failed to create stream: %w", err)
		}

		logger.Infow("JetStream stream created", zap.String("stream", streamName))
		controller.GetEventRecorder(ctx).Event(b, corev1.EventTypeNormal, ReasonStreamCreated, "JetStream stream created")
	}

	return nil
}

// getBrokerConfig loads the broker configuration with the following precedence:
// 1. Broker-specific config from annotation (if present, use it entirely)
// 2. Namespace-specific config from ConfigMap (if present, use it entirely)
// 3. Cluster default config from ConfigMap (if present, use it entirely)
// 4. Hardcoded defaults
func (r *Reconciler) getBrokerConfig(ctx context.Context, b *eventingv1.Broker) (*messagingv1alpha1.NatsJetStreamBrokerConfig, error) {
	logger := logging.FromContext(ctx)

	// Check for broker-specific annotation first (highest priority)
	if cfg, err := brokerconfig.GetConfigFromAnnotation(b.Annotations); err != nil {
		return nil, err
	} else if cfg != nil {
		logger.Debugw("Using broker-specific config from annotation")
		return cfg, nil
	}

	// No annotation config, try to load from ConfigMap
	cm, err := r.kubeClientSet.CoreV1().ConfigMaps(b.Namespace).Get(ctx, brokerconfig.ConfigMapName, metav1.GetOptions{})
	if err != nil {
		if !apierrs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get config map: %w", err)
		}
		// ConfigMap not found, use hardcoded defaults
		logger.Debugw("Broker config ConfigMap not found, using hardcoded defaults",
			zap.String("configmap", brokerconfig.ConfigMapName),
			zap.String("namespace", b.Namespace))
		return brokerconfig.DefaultBrokerConfig(), nil
	}

	// Load and return config from ConfigMap
	return brokerconfig.GetConfigFromConfigMap(cm, b.Namespace)
}

// reconcileIngressDeployment ensures the ingress deployment exists
func (r *Reconciler) reconcileIngressDeployment(ctx context.Context, b *eventingv1.Broker, streamName string, brokerCfg *messagingv1alpha1.NatsJetStreamBrokerConfig) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	// Get ingress deployment template if configured
	var ingressTemplate *messagingv1alpha1.DeploymentTemplate
	if brokerCfg != nil {
		ingressTemplate = brokerCfg.Ingress
	}

	expected := resources.MakeIngressDeployment(&resources.IngressArgs{
		Broker:             b,
		Image:              r.ingressImage,
		ServiceAccountName: r.ingressServiceAccount,
		StreamName:         streamName,
		NatsURL:            r.natsURL,
		Template:           ingressTemplate,
	})

	name := resources.IngressName(b.Name)
	existing, err := r.deploymentLister.Deployments(b.Namespace).Get(name)
	if err != nil {
		if apierrs.IsNotFound(err) {
			_, err = r.kubeClientSet.AppsV1().Deployments(b.Namespace).Create(ctx, expected, metav1.CreateOptions{})
			if err != nil {
				logger.Errorw("Failed to create ingress deployment", zap.Error(err))
				b.Status.MarkIngressFailed("IngressDeploymentFailed", "Failed to create ingress deployment: %v", err)
				return fmt.Errorf("failed to create ingress deployment: %w", err)
			}
			controller.GetEventRecorder(ctx).Event(b, corev1.EventTypeNormal, ReasonIngressDeploymentCreated, "Ingress deployment created")
			return nil
		}
		logger.Errorw("Failed to get ingress deployment", zap.Error(err))
		b.Status.MarkIngressFailed("IngressDeploymentFailed", "Failed to get ingress deployment: %v", err)
		return fmt.Errorf("failed to get ingress deployment: %w", err)
	}

	// Update if needed
	if !equality.Semantic.DeepEqual(expected.Spec, existing.Spec) {
		toUpdate := existing.DeepCopy()
		toUpdate.Spec = expected.Spec
		_, err = r.kubeClientSet.AppsV1().Deployments(b.Namespace).Update(ctx, toUpdate, metav1.UpdateOptions{})
		if err != nil {
			logger.Errorw("Failed to update ingress deployment", zap.Error(err))
			b.Status.MarkIngressFailed("IngressDeploymentFailed", "Failed to update ingress deployment: %v", err)
			return fmt.Errorf("failed to update ingress deployment: %w", err)
		}
		controller.GetEventRecorder(ctx).Event(b, corev1.EventTypeNormal, ReasonIngressDeploymentUpdated, "Ingress deployment updated")
	}

	return nil
}

// reconcileIngressService ensures the ingress service exists
func (r *Reconciler) reconcileIngressService(ctx context.Context, b *eventingv1.Broker) (*corev1.Service, pkgreconciler.Event) {
	logger := logging.FromContext(ctx)

	expected := resources.MakeIngressService(b)
	name := resources.IngressName(b.Name)

	existing, err := r.serviceLister.Services(b.Namespace).Get(name)
	if err != nil {
		if apierrs.IsNotFound(err) {
			svc, err := r.kubeClientSet.CoreV1().Services(b.Namespace).Create(ctx, expected, metav1.CreateOptions{})
			if err != nil {
				logger.Errorw("Failed to create ingress service", zap.Error(err))
				b.Status.MarkIngressFailed("IngressServiceFailed", "Failed to create ingress service: %v", err)
				return nil, fmt.Errorf("failed to create ingress service: %w", err)
			}
			controller.GetEventRecorder(ctx).Event(b, corev1.EventTypeNormal, ReasonIngressServiceCreated, "Ingress service created")
			return svc, nil
		}
		logger.Errorw("Failed to get ingress service", zap.Error(err))
		b.Status.MarkIngressFailed("IngressServiceFailed", "Failed to get ingress service: %v", err)
		return nil, fmt.Errorf("failed to get ingress service: %w", err)
	}

	// Update ClusterIP from existing service (immutable field)
	expected.Spec.ClusterIP = existing.Spec.ClusterIP

	if !equality.Semantic.DeepEqual(expected.Spec, existing.Spec) {
		toUpdate := existing.DeepCopy()
		toUpdate.Spec = expected.Spec
		svc, err := r.kubeClientSet.CoreV1().Services(b.Namespace).Update(ctx, toUpdate, metav1.UpdateOptions{})
		if err != nil {
			logger.Errorw("Failed to update ingress service", zap.Error(err))
			b.Status.MarkIngressFailed("IngressServiceFailed", "Failed to update ingress service: %v", err)
			return nil, fmt.Errorf("failed to update ingress service: %w", err)
		}
		return svc, nil
	}

	return existing, nil
}

// propagateIngressAvailability checks if the ingress deployment is available
func (r *Reconciler) propagateIngressAvailability(ctx context.Context, b *eventingv1.Broker, svc *corev1.Service) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	deploymentName := resources.IngressName(b.Name)
	deployment, err := r.deploymentLister.Deployments(b.Namespace).Get(deploymentName)
	if err != nil {
		if apierrs.IsNotFound(err) {
			b.Status.MarkIngressFailed("DeploymentNotFound", "Ingress deployment does not exist")
			return nil // Don't return error, let controller requeue
		}
		logger.Errorw("Failed to get ingress deployment", zap.Error(err))
		b.Status.MarkIngressFailed("DeploymentGetFailed", "Failed to get ingress deployment: %v", err)
		return fmt.Errorf("failed to get ingress deployment: %w", err)
	}

	if deployment.Status.ReadyReplicas == 0 {
		b.Status.MarkIngressFailed("DeploymentNotReady", "Ingress deployment has no ready replicas")
		return nil // Don't return error, let controller requeue
	}

	// Mark ingress as ready using condition set manager
	b.Status.GetConditionSet().Manage(&b.Status).MarkTrue(eventingv1.BrokerConditionIngress)
	return nil
}

// propagateFilterAvailability checks if the filter deployment is available
func (r *Reconciler) propagateFilterAvailability(ctx context.Context, b *eventingv1.Broker, svc *corev1.Service) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	deploymentName := resources.FilterName(b.Name)
	deployment, err := r.deploymentLister.Deployments(b.Namespace).Get(deploymentName)
	if err != nil {
		if apierrs.IsNotFound(err) {
			b.Status.MarkFilterFailed("DeploymentNotFound", "Filter deployment does not exist")
			return nil // Don't return error, let controller requeue
		}
		logger.Errorw("Failed to get filter deployment", zap.Error(err))
		b.Status.MarkFilterFailed("DeploymentGetFailed", "Failed to get filter deployment: %v", err)
		return fmt.Errorf("failed to get filter deployment: %w", err)
	}

	if deployment.Status.ReadyReplicas == 0 {
		b.Status.MarkFilterFailed("DeploymentNotReady", "Filter deployment has no ready replicas")
		return nil // Don't return error, let controller requeue
	}

	// Mark filter as ready using condition set manager
	b.Status.GetConditionSet().Manage(&b.Status).MarkTrue(eventingv1.BrokerConditionFilter)
	return nil
}

// reconcileFilterDeployment ensures the filter deployment exists
func (r *Reconciler) reconcileFilterDeployment(ctx context.Context, b *eventingv1.Broker, streamName string, brokerCfg *messagingv1alpha1.NatsJetStreamBrokerConfig) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	// Get filter deployment template if configured
	var filterTemplate *messagingv1alpha1.DeploymentTemplate
	if brokerCfg != nil {
		filterTemplate = brokerCfg.Filter
	}

	expected := resources.MakeFilterDeployment(&resources.FilterArgs{
		Broker:             b,
		Image:              r.filterImage,
		ServiceAccountName: r.filterServiceAccount,
		StreamName:         streamName,
		NatsURL:            r.natsURL,
		Template:           filterTemplate,
	})

	name := resources.FilterName(b.Name)
	existing, err := r.deploymentLister.Deployments(b.Namespace).Get(name)
	if err != nil {
		if apierrs.IsNotFound(err) {
			_, err = r.kubeClientSet.AppsV1().Deployments(b.Namespace).Create(ctx, expected, metav1.CreateOptions{})
			if err != nil {
				logger.Errorw("Failed to create filter deployment", zap.Error(err))
				b.Status.MarkFilterFailed("FilterDeploymentFailed", "Failed to create filter deployment: %v", err)
				return fmt.Errorf("failed to create filter deployment: %w", err)
			}
			controller.GetEventRecorder(ctx).Event(b, corev1.EventTypeNormal, ReasonFilterDeploymentCreated, "Filter deployment created")
			return nil
		}
		logger.Errorw("Failed to get filter deployment", zap.Error(err))
		b.Status.MarkFilterFailed("FilterDeploymentFailed", "Failed to get filter deployment: %v", err)
		return fmt.Errorf("failed to get filter deployment: %w", err)
	}

	// Update if needed
	if !equality.Semantic.DeepEqual(expected.Spec, existing.Spec) {
		toUpdate := existing.DeepCopy()
		toUpdate.Spec = expected.Spec
		_, err = r.kubeClientSet.AppsV1().Deployments(b.Namespace).Update(ctx, toUpdate, metav1.UpdateOptions{})
		if err != nil {
			logger.Errorw("Failed to update filter deployment", zap.Error(err))
			b.Status.MarkFilterFailed("FilterDeploymentFailed", "Failed to update filter deployment: %v", err)
			return fmt.Errorf("failed to update filter deployment: %w", err)
		}
		controller.GetEventRecorder(ctx).Event(b, corev1.EventTypeNormal, ReasonFilterDeploymentUpdated, "Filter deployment updated")
	}

	return nil
}

// reconcileFilterService ensures the filter service exists
func (r *Reconciler) reconcileFilterService(ctx context.Context, b *eventingv1.Broker) (*corev1.Service, pkgreconciler.Event) {
	logger := logging.FromContext(ctx)

	expected := resources.MakeFilterService(b)
	name := resources.FilterName(b.Name)

	existing, err := r.serviceLister.Services(b.Namespace).Get(name)
	if err != nil {
		if apierrs.IsNotFound(err) {
			svc, err := r.kubeClientSet.CoreV1().Services(b.Namespace).Create(ctx, expected, metav1.CreateOptions{})
			if err != nil {
				logger.Errorw("Failed to create filter service", zap.Error(err))
				b.Status.MarkFilterFailed("FilterServiceFailed", "Failed to create filter service: %v", err)
				return nil, fmt.Errorf("failed to create filter service: %w", err)
			}
			controller.GetEventRecorder(ctx).Event(b, corev1.EventTypeNormal, ReasonFilterServiceCreated, "Filter service created")
			return svc, nil
		}
		logger.Errorw("Failed to get filter service", zap.Error(err))
		b.Status.MarkFilterFailed("FilterServiceFailed", "Failed to get filter service: %v", err)
		return nil, fmt.Errorf("failed to get filter service: %w", err)
	}

	// Update ClusterIP from existing service (immutable field)
	expected.Spec.ClusterIP = existing.Spec.ClusterIP

	if !equality.Semantic.DeepEqual(expected.Spec, existing.Spec) {
		toUpdate := existing.DeepCopy()
		toUpdate.Spec = expected.Spec
		svc, err := r.kubeClientSet.CoreV1().Services(b.Namespace).Update(ctx, toUpdate, metav1.UpdateOptions{})
		if err != nil {
			logger.Errorw("Failed to update filter service", zap.Error(err))
			b.Status.MarkFilterFailed("FilterServiceFailed", "Failed to update filter service: %v", err)
			return nil, fmt.Errorf("failed to update filter service: %w", err)
		}
		return svc, nil
	}

	return existing, nil
}

// FinalizeKind cleans up resources when the broker is deleted
func (r *Reconciler) FinalizeKind(ctx context.Context, b *eventingv1.Broker) pkgreconciler.Event {
	logger := logging.FromContext(ctx)
	logger.Infow("Finalizing broker", zap.String("broker", b.Name))

	streamName := brokerutils.BrokerStreamName(b)

	// Delete the JetStream stream
	err := r.js.DeleteStream(streamName)
	if err != nil && !errors.Is(err, nats.ErrStreamNotFound) {
		logger.Errorw("Failed to delete JetStream stream", zap.Error(err), zap.String("stream", streamName))
		return fmt.Errorf("failed to delete stream: %w", err)
	}

	logger.Infow("Broker finalization completed", zap.String("broker", b.Name))
	return nil
}
