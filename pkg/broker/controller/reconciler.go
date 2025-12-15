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
	endpointsLister  corev1listers.EndpointsLister

	// NATS JetStream connection
	js nats.JetStreamContext

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

	// Step 1: Reconcile JetStream stream
	if err := r.reconcileStream(ctx, b, streamName); err != nil {
		return err
	}

	// Step 2: Reconcile ingress deployment
	if err := r.reconcileIngressDeployment(ctx, b, streamName); err != nil {
		return err
	}

	// Step 3: Reconcile ingress service
	ingressService, err := r.reconcileIngressService(ctx, b)
	if err != nil {
		return err
	}

	// Step 4: Check ingress endpoints readiness
	if err := r.propagateIngressAvailability(ctx, b, ingressService); err != nil {
		return err
	}

	// Step 5: Reconcile filter deployment
	if err := r.reconcileFilterDeployment(ctx, b, streamName); err != nil {
		return err
	}

	// Step 6: Reconcile filter service
	filterService, err := r.reconcileFilterService(ctx, b)
	if err != nil {
		return err
	}

	// Step 7: Check filter endpoints readiness
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

	logger.Infow("Broker reconciliation completed successfully", zap.String("broker", b.Name))
	return nil
}

// reconcileStream ensures the JetStream stream exists for the broker
func (r *Reconciler) reconcileStream(ctx context.Context, b *eventingv1.Broker, streamName string) pkgreconciler.Event {
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
		streamConfig := &nats.StreamConfig{
			Name:      streamName,
			Subjects:  []string{publishSubject + ".>"},
			Retention: nats.InterestPolicy,
			Storage:   nats.FileStorage,
			Replicas:  1,
			Discard:   nats.DiscardOld,
			MaxAge:    0, // No max age by default
		}

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

// reconcileIngressDeployment ensures the ingress deployment exists
func (r *Reconciler) reconcileIngressDeployment(ctx context.Context, b *eventingv1.Broker, streamName string) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	expected := resources.MakeIngressDeployment(&resources.IngressArgs{
		Broker:             b,
		Image:              r.ingressImage,
		ServiceAccountName: r.ingressServiceAccount,
		StreamName:         streamName,
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

// propagateIngressAvailability checks if the ingress endpoints are available
func (r *Reconciler) propagateIngressAvailability(ctx context.Context, b *eventingv1.Broker, svc *corev1.Service) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	endpoints, err := r.endpointsLister.Endpoints(svc.Namespace).Get(svc.Name)
	if err != nil {
		if apierrs.IsNotFound(err) {
			b.Status.MarkIngressFailed("EndpointsNotFound", "Ingress endpoints do not exist")
			return nil // Don't return error, let controller requeue
		}
		logger.Errorw("Failed to get ingress endpoints", zap.Error(err))
		b.Status.MarkIngressFailed("EndpointsGetFailed", "Failed to get ingress endpoints: %v", err)
		return fmt.Errorf("failed to get ingress endpoints: %w", err)
	}

	if len(endpoints.Subsets) == 0 {
		b.Status.MarkIngressFailed("EndpointsNotReady", "Ingress endpoints are not ready")
		return nil // Don't return error, let controller requeue
	}

	// Check if we have at least one ready address
	hasReadyAddress := false
	for _, subset := range endpoints.Subsets {
		if len(subset.Addresses) > 0 {
			hasReadyAddress = true
			break
		}
	}

	if !hasReadyAddress {
		b.Status.MarkIngressFailed("EndpointsNotReady", "Ingress endpoints have no ready addresses")
		return nil
	}

	b.Status.PropagateIngressAvailability(endpoints)
	return nil
}

// propagateFilterAvailability checks if the filter endpoints are available
func (r *Reconciler) propagateFilterAvailability(ctx context.Context, b *eventingv1.Broker, svc *corev1.Service) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	endpoints, err := r.endpointsLister.Endpoints(svc.Namespace).Get(svc.Name)
	if err != nil {
		if apierrs.IsNotFound(err) {
			b.Status.MarkFilterFailed("EndpointsNotFound", "Filter endpoints do not exist")
			return nil // Don't return error, let controller requeue
		}
		logger.Errorw("Failed to get filter endpoints", zap.Error(err))
		b.Status.MarkFilterFailed("EndpointsGetFailed", "Failed to get filter endpoints: %v", err)
		return fmt.Errorf("failed to get filter endpoints: %w", err)
	}

	if len(endpoints.Subsets) == 0 {
		b.Status.MarkFilterFailed("EndpointsNotReady", "Filter endpoints are not ready")
		return nil // Don't return error, let controller requeue
	}

	// Check if we have at least one ready address
	hasReadyAddress := false
	for _, subset := range endpoints.Subsets {
		if len(subset.Addresses) > 0 {
			hasReadyAddress = true
			break
		}
	}

	if !hasReadyAddress {
		b.Status.MarkFilterFailed("EndpointsNotReady", "Filter endpoints have no ready addresses")
		return nil
	}

	b.Status.PropagateFilterAvailability(endpoints)
	return nil
}

// reconcileFilterDeployment ensures the filter deployment exists
func (r *Reconciler) reconcileFilterDeployment(ctx context.Context, b *eventingv1.Broker, streamName string) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	expected := resources.MakeFilterDeployment(&resources.FilterArgs{
		Broker:             b,
		Image:              r.filterImage,
		ServiceAccountName: r.filterServiceAccount,
		StreamName:         streamName,
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
