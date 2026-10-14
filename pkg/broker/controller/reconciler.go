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
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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
	"knative.dev/pkg/resolver"

	eventingduckv1 "knative.dev/eventing/pkg/apis/duck/v1"
	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"
	eventinglisters "knative.dev/eventing/pkg/client/listers/eventing/v1"

	brokerconfig "knative.dev/eventing-natss/pkg/broker/config"
	"knative.dev/eventing-natss/pkg/broker/contract"
	"knative.dev/eventing-natss/pkg/broker/controller/resources"
	brokerutils "knative.dev/eventing-natss/pkg/broker/utils"
)

const (
	// Event reasons
	ReasonContractUpdated         = "ContractUpdated"
	ReasonContractFailed          = "ContractFailed"
	ReasonFilterDeploymentCreated = "FilterDeploymentCreated"
	ReasonFilterDeploymentUpdated = "FilterDeploymentUpdated"
	ReasonFilterDeploymentFailed  = "FilterDeploymentFailed"
	ReasonFilterServiceCreated    = "FilterServiceCreated"
	ReasonFilterServiceFailed     = "FilterServiceFailed"
	ReasonStreamCreated           = "JetStreamStreamCreated"
	ReasonStreamFailed            = "JetStreamStreamFailed"

	// DataplaneClusterRoleName is the name of the ClusterRole for dataplane components
	DataplaneClusterRoleName = "natsjetstream-broker-dataplane"
)

// Reconciler implements controller.Reconciler for Broker resources.
type Reconciler struct {
	kubeClientSet kubernetes.Interface

	// Listers for Kubernetes resources
	deploymentLister appsv1listers.DeploymentLister
	serviceLister    corev1listers.ServiceLister

	// Lister for Triggers, used to decide whether a broker needs a filter
	triggerLister eventinglisters.TriggerLister

	// Contract manager for updating shared ingress configuration
	contractManager *contract.Manager

	// NATS JetStream connection
	js nats.JetStreamContext

	// URI resolver for resolving the dead letter sink address
	uriResolver *resolver.URIResolver

	// NATS URL for data plane components
	natsURL string

	// Image configuration
	filterImage          string
	filterServiceAccount string

	// Shared ingress service configuration
	ingressServiceName string
	ingressNamespace   string
}

// ReconcileKind implements Interface.ReconcileKind
func (r *Reconciler) ReconcileKind(ctx context.Context, b *eventingv1.Broker) pkgreconciler.Event {
	logger := logging.FromContext(ctx)
	logger.Infow("Reconciling broker", zap.String("broker", b.Name), zap.String("namespace", b.Namespace))

	// Get stream name for this broker
	streamName := brokerutils.BrokerStreamName(b)
	publishSubject := brokerutils.BrokerPublishSubjectName(b.Namespace, b.Name)

	// Load broker configuration (once for the entire reconciliation)
	brokerCfg, err := r.getBrokerConfig(ctx, b)
	if err != nil {
		logger.Errorw("Failed to get broker config", zap.Error(err))
		b.Status.MarkIngressFailed("ConfigLoadFailed", "Failed to load broker configuration: %v", err)
		return fmt.Errorf("failed to get broker config: %w", err)
	}

	// Step 1: Reconcile dataplane RBAC (service account and role binding for filter)
	if err := r.reconcileDataplaneRBAC(ctx, b); err != nil {
		return err
	}

	// Step 2: Reconcile JetStream stream
	if err := r.reconcileStream(ctx, b, streamName, publishSubject, brokerCfg); err != nil {
		return err
	}

	// Step 3: Update contract ConfigMap for shared ingress
	brokerContract := contract.BrokerContract{
		UID:            string(b.UID),
		Namespace:      b.Namespace,
		Name:           b.Name,
		StreamName:     streamName,
		PublishSubject: publishSubject,
		Path:           fmt.Sprintf("/%s/%s", b.Namespace, b.Name),
		Generation:     b.Generation,
	}

	// Step 4: Reconcile ingress service
	contractUpdated, err := r.contractManager.UpdateBrokerIfChanged(ctx, brokerContract)
	if err != nil {
		logger.Errorw("Failed to update contract", zap.Error(err))
		controller.GetEventRecorder(ctx).Event(b, corev1.EventTypeWarning, ReasonContractFailed, err.Error())
		b.Status.MarkIngressFailed("ContractUpdateFailed", "Failed to update contract ConfigMap: %v", err)
		return fmt.Errorf("failed to update contract: %w", err)
	}

	if contractUpdated {
		controller.GetEventRecorder(ctx).Event(b, corev1.EventTypeNormal, ReasonContractUpdated, "Contract updated")
	}

	// Step 4: Check shared ingress deployment readiness
	if err := r.propagateIngressAvailability(ctx, b); err != nil {
		return err
	}

	// Steps 5-7: The per-broker filter only dispatches events to trigger
	// subscribers, so it is needed only when triggers exist. Ingress and the
	// JetStream stream (steps 2-4) are always reconciled, so events are still
	// received and stored regardless of triggers — a filter created later
	// consumes the buffered messages.
	hasTriggers, err := r.hasTriggers(b)
	if err != nil {
		b.Status.MarkFilterFailed("TriggerListFailed", "Failed to list triggers: %v", err)
		return fmt.Errorf("failed to list triggers: %w", err)
	}
	if hasTriggers {
		if err := r.reconcileFilterDeployment(ctx, b, streamName, brokerCfg); err != nil {
			return err
		}
		filterService, err := r.reconcileFilterService(ctx, b)
		if err != nil {
			return err
		}
		if err := r.propagateFilterAvailability(ctx, b, filterService); err != nil {
			return err
		}
	} else {
		// No triggers reference this broker: tear down the filter if present and
		// mark the condition ready so the broker still becomes Ready (required
		// before any trigger can be created against it).
		if err := r.deleteFilter(ctx, b); err != nil {
			return err
		}
		b.Status.GetConditionSet().Manage(&b.Status).MarkTrueWithReason(eventingv1.BrokerConditionFilter, "NoTriggers", "No triggers reference this broker")
	}

	// Step 8: Set broker address to shared ingress with path
	b.Status.SetAddress(&duckv1.Addressable{
		Name: ptr.To("http"),
		URL: &apis.URL{
			Scheme: "http",
			Host:   network.GetServiceHostname(r.ingressServiceName, r.ingressNamespace),
			Path:   fmt.Sprintf("/%s/%s", b.Namespace, b.Name),
		},
	})

	// Step 9: Mark TriggerChannel as ready (we use JetStream instead of a channel)
	b.Status.GetConditionSet().Manage(&b.Status).MarkTrue(eventingv1.BrokerConditionTriggerChannel)

	// Step 10: Resolve the dead letter sink. Triggers without their own
	// delivery inherit this resolved address (see trigger reconciler).
	if b.Spec.Delivery == nil || b.Spec.Delivery.DeadLetterSink == nil {
		b.Status.MarkDeadLetterSinkNotConfigured()
	} else {
		dlsAddr, err := r.resolveDeadLetterSink(ctx, b)
		if err != nil {
			b.Status.MarkDeadLetterSinkResolvedFailed("DeadLetterSinkResolveFailed", "Failed to resolve dead letter sink: %v", err)
			return fmt.Errorf("failed to resolve dead letter sink: %w", err)
		}
		b.Status.MarkDeadLetterSinkResolvedSucceeded(eventingduckv1.NewDeliveryStatusFromAddressable(dlsAddr))
	}

	// Step 11: Mark EventPolicies as ready (not using OIDC authentication)
	b.Status.MarkEventPoliciesTrueWithReason("EventPoliciesSkipped", "Feature %q is disabled", "OIDC")

	logger.Infow("Broker reconciliation completed successfully", zap.String("broker", b.Name))
	return nil
}

// reconcileStream ensures the JetStream stream exists for the broker
func (r *Reconciler) reconcileStream(ctx context.Context, b *eventingv1.Broker, streamName, publishSubject string, brokerCfg *brokerconfig.NatsJetStreamBrokerConfig) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

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

// resolveDeadLetterSink resolves the broker's dead letter sink to an Addressable.
// A Ref without a namespace defaults to the broker's namespace.
func (r *Reconciler) resolveDeadLetterSink(ctx context.Context, b *eventingv1.Broker) (*duckv1.Addressable, error) {
	dest := b.Spec.Delivery.DeadLetterSink
	destination := duckv1.Destination{URI: dest.URI}
	if dest.Ref != nil {
		namespace := dest.Ref.Namespace
		if namespace == "" {
			namespace = b.Namespace
		}
		destination.Ref = &duckv1.KReference{
			Kind:       dest.Ref.Kind,
			Namespace:  namespace,
			Name:       dest.Ref.Name,
			APIVersion: dest.Ref.APIVersion,
		}
	}
	return r.uriResolver.AddressableFromDestinationV1(ctx, destination, b)
}

// getBrokerConfig loads the broker configuration with the following precedence:
// 1. Broker-specific config from annotation (if present, use it entirely)
// 2. Namespace-specific config from ConfigMap (if present, use it entirely)
// 3. Cluster default config from ConfigMap (if present, use it entirely)
// 4. Hardcoded defaults
func (r *Reconciler) getBrokerConfig(ctx context.Context, b *eventingv1.Broker) (*brokerconfig.NatsJetStreamBrokerConfig, error) {
	logger := logging.FromContext(ctx)

	// Broker-specific config (highest priority): the annotation names a ConfigMap
	// in the broker's namespace holding this broker's config.
	if cmName := b.Annotations[brokerconfig.BrokerConfigAnnotation]; cmName != "" {
		cm, err := r.kubeClientSet.CoreV1().ConfigMaps(b.Namespace).Get(ctx, cmName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get broker config ConfigMap %q in namespace %q: %w", cmName, b.Namespace, err)
		}
		cfg, err := brokerconfig.ParseBrokerConfig(cm)
		if err != nil {
			return nil, fmt.Errorf("failed to parse broker config from ConfigMap %q: %w", cmName, err)
		}
		logger.Debugw("Using broker-specific config from ConfigMap", zap.String("configmap", cmName))
		return cfg, nil
	}

	// No annotation config, try to load namespace/cluster defaults from ConfigMap
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

// propagateIngressAvailability checks if the shared ingress deployment is available
func (r *Reconciler) propagateIngressAvailability(ctx context.Context, b *eventingv1.Broker) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	deployment, err := r.deploymentLister.Deployments(r.ingressNamespace).Get(r.ingressServiceName)
	if err != nil {
		if apierrs.IsNotFound(err) {
			b.Status.MarkIngressFailed("DeploymentNotFound", "Shared ingress deployment %q not found in namespace %q", r.ingressServiceName, r.ingressNamespace)
			return nil
		}
		logger.Errorw("Failed to get shared ingress deployment", zap.Error(err))
		b.Status.MarkIngressFailed("DeploymentGetFailed", "Failed to get shared ingress deployment: %v", err)
		return fmt.Errorf("failed to get ingress deployment: %w", err)
	}

	if deployment.Status.ReadyReplicas == 0 {
		b.Status.MarkIngressFailed("DeploymentNotReady", "Shared ingress deployment has no ready replicas")
		return nil
	}

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
func (r *Reconciler) reconcileFilterDeployment(ctx context.Context, b *eventingv1.Broker, streamName string, brokerCfg *brokerconfig.NatsJetStreamBrokerConfig) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	// Get filter deployment template if configured
	var filterTemplate *brokerconfig.DeploymentTemplate
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

// hasTriggers reports whether any Trigger in the broker's namespace references it.
func (r *Reconciler) hasTriggers(b *eventingv1.Broker) (bool, error) {
	triggers, err := r.triggerLister.Triggers(b.Namespace).List(labels.Everything())
	if err != nil {
		return false, err
	}
	for _, t := range triggers {
		if t.Spec.Broker == b.Name {
			return true, nil
		}
	}
	return false, nil
}

// deleteFilter removes the filter deployment and service for a broker with no
// triggers. Missing resources are treated as already deleted.
func (r *Reconciler) deleteFilter(ctx context.Context, b *eventingv1.Broker) pkgreconciler.Event {
	logger := logging.FromContext(ctx)
	name := resources.FilterName(b.Name)

	if err := r.kubeClientSet.AppsV1().Deployments(b.Namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrs.IsNotFound(err) {
		logger.Errorw("Failed to delete filter deployment", zap.Error(err))
		b.Status.MarkFilterFailed("FilterDeploymentDeleteFailed", "Failed to delete filter deployment: %v", err)
		return fmt.Errorf("failed to delete filter deployment: %w", err)
	}
	if err := r.kubeClientSet.CoreV1().Services(b.Namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrs.IsNotFound(err) {
		logger.Errorw("Failed to delete filter service", zap.Error(err))
		b.Status.MarkFilterFailed("FilterServiceDeleteFailed", "Failed to delete filter service: %v", err)
		return fmt.Errorf("failed to delete filter service: %w", err)
	}
	return nil
}

// FinalizeKind cleans up resources when the broker is deleted
func (r *Reconciler) FinalizeKind(ctx context.Context, b *eventingv1.Broker) pkgreconciler.Event {
	logger := logging.FromContext(ctx)
	logger.Infow("Finalizing broker", zap.String("broker", b.Name))

	// Delete from contract ConfigMap
	if err := r.contractManager.DeleteBroker(ctx, b.Namespace, b.Name); err != nil {
		logger.Errorw("Failed to delete broker from contract", zap.Error(err))
		return fmt.Errorf("failed to delete broker from contract: %w", err)
	}

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

// reconcileDataplaneRBAC ensures the service account and cluster role binding exist
// for the dataplane components (filter) in the broker's namespace.
func (r *Reconciler) reconcileDataplaneRBAC(ctx context.Context, b *eventingv1.Broker) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	// Create the service account if it doesn't exist
	saName := r.filterServiceAccount
	_, err := r.kubeClientSet.CoreV1().ServiceAccounts(b.Namespace).Get(ctx, saName, metav1.GetOptions{})
	if err != nil {
		if apierrs.IsNotFound(err) {
			sa := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: b.Namespace,
					Labels: map[string]string{
						"nats.eventing.knative.dev/release": "devel",
					},
				},
			}
			_, err = r.kubeClientSet.CoreV1().ServiceAccounts(b.Namespace).Create(ctx, sa, metav1.CreateOptions{})
			if err != nil && !apierrs.IsAlreadyExists(err) {
				logger.Errorw("Failed to create dataplane service account", zap.Error(err))
				return fmt.Errorf("failed to create dataplane service account: %w", err)
			}
			logger.Infow("Created dataplane service account", zap.String("name", saName), zap.String("namespace", b.Namespace))
		} else {
			logger.Errorw("Failed to get dataplane service account", zap.Error(err))
			return fmt.Errorf("failed to get dataplane service account: %w", err)
		}
	}

	// Create the cluster role binding if it doesn't exist
	// Each namespace gets its own ClusterRoleBinding
	crbName := fmt.Sprintf("%s-%s", DataplaneClusterRoleName, b.Namespace)
	_, err = r.kubeClientSet.RbacV1().ClusterRoleBindings().Get(ctx, crbName, metav1.GetOptions{})
	if err != nil {
		if apierrs.IsNotFound(err) {
			crb := &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: crbName,
					Labels: map[string]string{
						"nats.eventing.knative.dev/release": "devel",
					},
				},
				Subjects: []rbacv1.Subject{
					{
						Kind:      "ServiceAccount",
						Name:      saName,
						Namespace: b.Namespace,
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     DataplaneClusterRoleName,
				},
			}
			_, err = r.kubeClientSet.RbacV1().ClusterRoleBindings().Create(ctx, crb, metav1.CreateOptions{})
			if err != nil && !apierrs.IsAlreadyExists(err) {
				logger.Errorw("Failed to create dataplane cluster role binding", zap.Error(err))
				return fmt.Errorf("failed to create dataplane cluster role binding: %w", err)
			}
			logger.Infow("Created dataplane cluster role binding", zap.String("name", crbName))
		} else {
			logger.Errorw("Failed to get dataplane cluster role binding", zap.Error(err))
			return fmt.Errorf("failed to get dataplane cluster role binding: %w", err)
		}
	}

	return nil
}
