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

	"github.com/google/go-cmp/cmp"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/pointer"
	"knative.dev/eventing/pkg/apis/eventing"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/network"

	"knative.dev/eventing-natss/pkg/channel/jetstream"
	"knative.dev/eventing-natss/pkg/channel/jetstream/controller/resources"
	jetstreamv1alpha1listers "knative.dev/eventing-natss/pkg/client/listers/messaging/v1alpha1"
	"knative.dev/eventing-natss/pkg/common/constants"

	"knative.dev/pkg/logging"
	"knative.dev/pkg/reconciler"

	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	rbacv1listers "k8s.io/client-go/listers/rbac/v1"

	"knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	natssChannelReconciler "knative.dev/eventing-natss/pkg/client/injection/reconciler/messaging/v1alpha1/natsjetstreamchannel"
)

const (
	// Name of the corev1.Events emitted from the reconciliation process.

	dispatcherDeploymentCreated     = "DispatcherDeploymentCreated"
	dispatcherDeploymentFailed      = "DispatcherDeploymentFailed"
	dispatcherDeploymentUpdated     = "DispatcherDeploymentUpdated"
	dispatcherEndpointsNotFound     = "DispatcherEndpointsDoesNotExist"
	dispatcherRoleBindingCreated    = "DispatcherRoleBindingCreated"
	dispatcherRoleBindingFailed     = "DispatcherRoleBindingFailed"
	dispatcherServiceAccountCreated = "DispatcherServiceAccountCreated"
	dispatcherServiceAccountFailed  = "DispatcherServiceAccountFailed"
	dispatcherServiceCreated        = "DispatcherServiceCreated"
	dispatcherServiceFailed         = "DispatcherServiceFailed"
	dispatcherServiceUpdated        = "DispatcherServiceUpdated"
	natsJetStreamChannelReconciled  = "NatsJetStreamChannelReconciled"
)

func newReconciledNormal(namespace, name string) reconciler.Event {
	return reconciler.NewEvent(corev1.EventTypeNormal, natsJetStreamChannelReconciled, "NatsJetStreamChannel reconciled: \"%s/%s\"", namespace, name)
}

func newDeploymentWarn(err error) reconciler.Event {
	return reconciler.NewEvent(corev1.EventTypeWarning, dispatcherDeploymentFailed, "Reconciling dispatcher Deployment failed with: %s", err)
}

func newServiceWarn(err error) reconciler.Event {
	return reconciler.NewEvent(corev1.EventTypeWarning, dispatcherServiceFailed, "Reconciling dispatcher Service failed with: %s", err)
}

func newDispatcherServiceWarn(err error) reconciler.Event {
	return reconciler.NewEvent(corev1.EventTypeWarning, dispatcherServiceFailed, "Reconciling dispatcher Service failed with: %s", err)
}

func newServiceAccountWarn(err error) reconciler.Event {
	return reconciler.NewEvent(corev1.EventTypeWarning, dispatcherServiceAccountFailed, "Reconciling dispatcher ServiceAccount failed: %s", err)
}

func newRoleBindingWarn(err error) reconciler.Event {
	return reconciler.NewEvent(corev1.EventTypeWarning, dispatcherRoleBindingFailed, "Reconciling dispatcher RoleBinding failed: %s", err)
}

// Reconciler is the controller reconciler which manages dispatcher deployments and other related manifests in response
// to NATSJetStreamChannel resource changes.
type Reconciler struct {
	kubeClientSet kubernetes.Interface

	systemNamespace          string
	dispatcherImage          string
	dispatcherServiceAccount string

	deploymentLister     appsv1listers.DeploymentLister
	serviceLister        corev1listers.ServiceLister
	endpointsLister      corev1listers.EndpointsLister
	serviceAccountLister corev1listers.ServiceAccountLister
	roleBindingLister    rbacv1listers.RoleBindingLister
	jsmChannelLister     jetstreamv1alpha1listers.NatsJetStreamChannelLister

	controllerRef metav1.OwnerReference
}

var _ natssChannelReconciler.Interface = (*Reconciler)(nil)

func (r *Reconciler) ReconcileKind(ctx context.Context, nc *v1alpha1.NatsJetStreamChannel) reconciler.Event {
	logger := logging.FromContext(ctx)

	// If the channel is not scoped then the dispatcher deployment and other resources are created in the system
	// namespace, otherwise the namespace where the channel is located is used.
	scope, ok := nc.Annotations[eventing.ScopeAnnotationKey]
	if !ok {
		scope = eventing.ScopeCluster
	}

	dispatcherNamespace := r.systemNamespace
	if scope == eventing.ScopeNamespace {
		dispatcherNamespace = nc.Namespace
	}

	logger.Debugw("ReconcileKind", zap.String("dispatcher_namespace", dispatcherNamespace))

	// Make sure the dispatcher deployment exists and propagate the status to the Channel
	if err := r.reconcileDispatcher(ctx, scope, dispatcherNamespace, nc); err != nil {
		return err
	}

	// Make sure the dispatcher service exists and propagate the status to the Channel in case it does not exist.
	// We don't do anything with the service because it's status contains nothing useful, so just do
	// an existence check. Then below we check the endpoints targeting it.

	if err := r.reconcileDispatcherService(ctx, dispatcherNamespace, nc); err != nil {
		return err
	}

	// Get the Dispatcher Service Endpoints and propagate the status to the Channel
	// endpoints has the same name as the service, so not a bug.
	e, err := r.endpointsLister.Endpoints(dispatcherNamespace).Get(jetstream.DispatcherName)
	if err != nil {
		if apierrs.IsNotFound(err) {
			nc.Status.MarkEndpointsFailed("DispatcherEndpointsDoesNotExist", "Dispatcher Endpoints does not exist")
			return nil
		}

		logger.Errorw("Unable to get the dispatcher endpoints", zap.Error(err))
		nc.Status.MarkEndpointsFailed("DispatcherEndpointsGetFailed", "Failed to get dispatcher endpoints")
		return err
	}

	if len(e.Subsets) == 0 {
		logger.Infow("No endpoints found for Dispatcher service")
		nc.Status.MarkEndpointsFailed("DispatcherEndpointsNotReady", "There are no endpoints ready for Dispatcher service")
		return nil
	}

	nc.Status.MarkEndpointsTrue()

	// Reconcile the k8s service representing the actual Channel. It points to the Dispatcher service via ExternalName
	svc, err := r.reconcileChannelService(ctx, dispatcherNamespace, nc)
	if err != nil {
		return err
	}
	nc.Status.MarkChannelServiceTrue()
	nc.Status.SetAddress(&apis.URL{
		Scheme: "http",
		Host:   network.GetServiceHostname(svc.Name, svc.Namespace),
	})

	return nil
}

// FinalizeKind checks whether there are still resources requiring a dispatcher to exist. This handles both
// namespace-scoped and cluster-scoped resources
func (r *Reconciler) FinalizeKind(ctx context.Context, nc *v1alpha1.NatsJetStreamChannel) (err reconciler.Event) {
	logger := logging.FromContext(ctx)
	scope, ok := nc.Annotations[eventing.ScopeAnnotationKey]
	if !ok {
		scope = eventing.ScopeCluster
	}

	// we don't support running multiple dispatchers in a namespace, so if a namespace-scoped channel exists in the
	// system namespace it will be shared with other cluster-scoped channels.
	if scope == eventing.ScopeCluster || nc.Namespace == r.systemNamespace {
		return r.finalizeClusterScopedKind(ctx, nc)
	}

	if scope == eventing.ScopeNamespace {
		return r.finalizeNamespaceScopedKind(ctx, nc)
	}

	logger.Errorw("unknown scope for NatsJetStreamChannel, skipping finalization")

	return nil
}

func (r *Reconciler) reconcileDispatcher(ctx context.Context, scope, dispatcherNamespace string, nc *v1alpha1.NatsJetStreamChannel) error {
	logger := logging.FromContext(ctx)
	if scope == eventing.ScopeNamespace {
		// Configure RBAC in namespace to access the configmaps
		sa, err := r.reconcileServiceAccount(ctx, dispatcherNamespace, nc)
		if err != nil {
			return err
		}

		err = r.reconcileRoleBinding(ctx, jetstream.DispatcherName, dispatcherNamespace, nc, jetstream.DispatcherName, sa, true)
		if err != nil {
			return err
		}

		// Reconcile the RoleBinding allowing read access to the shared configmaps.
		// Note this RoleBinding is created in the system namespace and points to a
		// subject in the dispatcher's namespace.
		roleBindingName := fmt.Sprintf("%s-eventing-scoped-%s", jetstream.DispatcherName, dispatcherNamespace)
		err = r.reconcileRoleBinding(ctx, roleBindingName, r.systemNamespace, nc, constants.KnativeConfigMapEventingRole, sa, false)
		if err != nil {
			return err
		}
	}

	args := resources.DispatcherDeploymentArgs{
		DispatcherScope:     scope,
		DispatcherNamespace: dispatcherNamespace,
		Image:               r.dispatcherImage,
		Replicas:            1,
		ServiceAccount:      r.dispatcherServiceAccount,
		OwnerRef:            r.controllerRef,
	}

	want := resources.NewDispatcherDeploymentBuilder().WithArgs(&args).Build()
	d, err := r.deploymentLister.Deployments(dispatcherNamespace).Get(jetstream.DispatcherName)
	if err != nil {
		logger.Debugw("failed to get dispatcher deployment", zap.Error(err))
		if apierrs.IsNotFound(err) {
			logger.Debugw("dispatcher deployment does not exist, creating a new one")
			d, err = r.kubeClientSet.AppsV1().Deployments(dispatcherNamespace).Create(ctx, want, metav1.CreateOptions{})
			if err != nil {
				logger.Errorw("error while creating dispatcher deployment", zap.Error(err), zap.String("namespace", dispatcherNamespace), zap.Any("deployment", want))
				nc.Status.MarkDispatcherFailed(dispatcherDeploymentFailed, "Failed to create the dispatcher deployment: %v", err)
				return newDeploymentWarn(err)
			}

			logger.Debugw("dispatcher deployment created", zap.Any("deployment", d))

			controller.GetEventRecorder(ctx).Event(nc, corev1.EventTypeNormal, dispatcherDeploymentCreated, "Dispatcher deployment created")
			nc.Status.PropagateDispatcherStatus(&d.Status)
			return nil
		}

		logger.Errorw("can't get dispatcher deployment", zap.Error(err), zap.String("namespace", dispatcherNamespace),
			zap.String("dispatcher-name", jetstream.DispatcherName))
		nc.Status.MarkDispatcherUnknown("DispatcherDeploymentFailed", "Failed to get dispatcher deployment: %v", err)
		return err
	}

	// scale up the dispatcher to 1, otherwise keep the existing number in case the user has scaled it up.
	if *d.Spec.Replicas == 0 {
		logger.Infof("Dispatcher deployment has 0 replica. Scaling up deployment to 1 replica")
		args.Replicas = 1
	} else {
		args.Replicas = *d.Spec.Replicas
	}
	want = resources.NewDispatcherDeploymentBuilderFromDeployment(d.DeepCopy()).WithArgs(&args).Build()

	if !equality.Semantic.DeepEqual(want.ObjectMeta, d.ObjectMeta) || !equality.Semantic.DeepEqual(want.Spec, d.Spec) {
		logger.Infof("Dispatcher deployment changed; reconciling: ObjectMeta=\n%s, Spec=\n%s", cmp.Diff(want.ObjectMeta, d.ObjectMeta), cmp.Diff(want.Spec, d.Spec))
		if d, err = r.kubeClientSet.AppsV1().Deployments(dispatcherNamespace).Update(ctx, want, metav1.UpdateOptions{}); err != nil {
			logger.Errorw("error while updating dispatcher deployment", zap.Error(err), zap.String("namespace", dispatcherNamespace), zap.Any("deployment", want))
			nc.Status.MarkServiceFailed("DispatcherDeploymentUpdateFailed", "Failed to update the dispatcher deployment: %v", err)
			return newDeploymentWarn(err)
		} else {
			controller.GetEventRecorder(ctx).Event(nc, corev1.EventTypeNormal, dispatcherDeploymentUpdated, "Dispatcher deployment updated")
		}
	}
	nc.Status.PropagateDispatcherStatus(&d.Status)
	return nil
}

func (r *Reconciler) reconcileServiceAccount(ctx context.Context, dispatcherNamespace string, nc *v1alpha1.NatsJetStreamChannel) (*corev1.ServiceAccount, error) {
	sa, err := r.serviceAccountLister.ServiceAccounts(dispatcherNamespace).Get(r.dispatcherServiceAccount)
	if err != nil {
		if apierrs.IsNotFound(err) {
			expected := resources.MakeServiceAccount(dispatcherNamespace, r.dispatcherServiceAccount)
			sa, err := r.kubeClientSet.CoreV1().ServiceAccounts(dispatcherNamespace).Create(ctx, expected, metav1.CreateOptions{})
			if err == nil {
				controller.GetEventRecorder(ctx).Event(nc, corev1.EventTypeNormal, dispatcherServiceAccountCreated, "Dispatcher service account created")
				return sa, nil
			} else {
				nc.Status.MarkDispatcherFailed("DispatcherServiceAccountFailed", "Failed to create the dispatcher service account: %v", err)
				return sa, newServiceAccountWarn(err)
			}
		}

		nc.Status.MarkDispatcherUnknown("DispatcherServiceAccountFailed", "Failed to get dispatcher service account: %v", err)
		return nil, newServiceAccountWarn(err)
	}
	return sa, err
}

func (r *Reconciler) reconcileRoleBinding(ctx context.Context, name string, ns string, nc *v1alpha1.NatsJetStreamChannel, roleName string, sa *corev1.ServiceAccount, isClusterRole bool) error {
	_, err := r.roleBindingLister.RoleBindings(ns).Get(name)
	if err != nil {
		if apierrs.IsNotFound(err) {
			expected := resources.MakeRoleBinding(ns, name, sa, roleName, isClusterRole)
			_, err := r.kubeClientSet.RbacV1().RoleBindings(ns).Create(ctx, expected, metav1.CreateOptions{})
			if err == nil {
				controller.GetEventRecorder(ctx).Event(nc, corev1.EventTypeNormal, dispatcherRoleBindingCreated, "Dispatcher role binding created")
				return nil
			} else {
				nc.Status.MarkDispatcherFailed("DispatcherRoleBindingFailed", "Failed to create the dispatcher role binding: %v", err)
				return newRoleBindingWarn(err)
			}
		}
		nc.Status.MarkDispatcherUnknown("DispatcherRoleBindingFailed", "Failed to get dispatcher role binding: %v", err)
		return newRoleBindingWarn(err)
	}
	return err
}

func (r *Reconciler) reconcileDispatcherService(ctx context.Context, dispatcherNamespace string, nc *v1alpha1.NatsJetStreamChannel) error {
	logger := logging.FromContext(ctx)

	args := resources.DispatcherServiceArgs{
		DispatcherNamespace: dispatcherNamespace,
	}

	want := resources.NewDispatcherServiceBuilder().WithArgs(&args).Build()
	svc, err := r.serviceLister.Services(dispatcherNamespace).Get(jetstream.DispatcherName)
	if err != nil {
		if apierrs.IsNotFound(err) {
			_, err := r.kubeClientSet.CoreV1().Services(dispatcherNamespace).Create(ctx, want, metav1.CreateOptions{})
			if err == nil {
				controller.GetEventRecorder(ctx).Event(nc, corev1.EventTypeNormal, dispatcherServiceCreated, "Dispatcher service created")
				nc.Status.MarkServiceTrue()
			} else {
				logger.Errorw("Unable to create the dispatcher service", zap.Error(err))
				controller.GetEventRecorder(ctx).Eventf(nc, corev1.EventTypeWarning, dispatcherServiceFailed, "Failed to create the dispatcher service: %v", err)
				nc.Status.MarkServiceFailed("DispatcherServiceFailed", "Failed to create the dispatcher service: %v", err)
				return err
			}
			return err
		}
		logger.Errorw("can't get dispatcher service", zap.Error(err), zap.String("namespace", dispatcherNamespace), zap.String("dispatcher-name", jetstream.DispatcherName))
		nc.Status.MarkServiceUnknown("DispatcherServiceFailed", "Failed to get dispatcher service: %v", err)
		return newDispatcherServiceWarn(err)
	} else {
		want = resources.NewDispatcherServiceBuilderFromService(svc.DeepCopy()).WithArgs(&args).Build()
		if !equality.Semantic.DeepEqual(want.ObjectMeta, svc.ObjectMeta) || !equality.Semantic.DeepEqual(want.Spec, svc.Spec) {
			logger.Infof("Dispatcher service changed; reconciling: ObjectMeta=\n%s, Spec=\n%s", cmp.Diff(want.ObjectMeta, svc.ObjectMeta), cmp.Diff(want.Spec, svc.Spec))
			if _, err = r.kubeClientSet.CoreV1().Services(dispatcherNamespace).Update(ctx, want, metav1.UpdateOptions{}); err != nil {
				logger.Errorw("error while updating dispatcher service", zap.Error(err), zap.String("namespace", dispatcherNamespace), zap.Any("service", want))
				nc.Status.MarkServiceFailed("DispatcherServiceUpdateFailed", "Failed to update the dispatcher service: %v", err)
				return newServiceWarn(err)
			} else {
				controller.GetEventRecorder(ctx).Event(nc, corev1.EventTypeNormal, dispatcherServiceUpdated, "Dispatcher service updated")
			}
		}
		nc.Status.MarkServiceTrue()
		return nil
	}
}

func (r *Reconciler) reconcileChannelService(ctx context.Context, dispatcherNamespace string, nc *v1alpha1.NatsJetStreamChannel) (*corev1.Service, error) {
	logger := logging.FromContext(ctx)
	// Get the Service and propagate the status to the Channel in case it does not exist.
	// We don't do anything with the service because it's status contains nothing useful, so just do
	// an existence check. Then below we check the endpoints targeting it.
	// We may change this name later, so we have to ensure we use proper addressable when resolving these.
	svc, err := r.serviceLister.Services(nc.Namespace).Get(resources.MakeJSMChannelServiceName(nc.Name))
	if err != nil {
		if apierrs.IsNotFound(err) {
			svc, err = resources.MakeK8sService(nc, resources.ExternalService(dispatcherNamespace, jetstream.DispatcherName))
			if err != nil {
				logger.Error("Failed to create the channel service object", zap.Error(err))
				return nil, err
			}
			svc, err = r.kubeClientSet.CoreV1().Services(nc.Namespace).Create(ctx, svc, metav1.CreateOptions{})
			if err != nil {
				logger.Error("Failed to create the channel service", zap.Error(err))
				return nil, err
			}
			return svc, nil
		}
		logger.Error("Unable to get the channel service", zap.Error(err))
		return nil, err
	}
	// Check to make sure that the NatsJetStreamChannel owns this service and if not, complain.
	if !metav1.IsControlledBy(svc, nc) {
		return nil, fmt.Errorf("jetstreamchannel: %s/%s does not own Service: %q", nc.Namespace, nc.Name, svc.Name)
	}
	return svc, nil
}

// finalizeClusterScopedKind finds all other cluster-scoped NatsJetStreamChannels and if none are cluster-scoped nor
// ones which exist in the system namespace (these can have any scope) then the dispatcher in the system namespace is
// deleted.
func (r *Reconciler) finalizeClusterScopedKind(ctx context.Context, nc *v1alpha1.NatsJetStreamChannel) reconciler.Event {
	logger := logging.FromContext(ctx)

	allChannels, err := r.jsmChannelLister.List(labels.Everything())
	if err != nil {
		logger.Errorw("failed to list NatsJetStreamChannels", zap.Error(err))
		// TODO: is this ok? should we be returning an actual event within FinalizeKind?
		return err
	}

	var stillRequireDispatcher bool
	for _, channel := range allChannels {
		// ignore the channel being finalized.
		if channel.UID == nc.UID {
			continue
		}

		if channel.Namespace == r.systemNamespace {
			stillRequireDispatcher = true
			break
		}

		if channel.Annotations[eventing.ScopeAnnotationKey] != eventing.ScopeNamespace {
			stillRequireDispatcher = true
			break
		}
	}

	if stillRequireDispatcher {
		logger.Debugw("cluster-scoped dispatcher still required, nothing more to do")
		return nil
	}

	logger.Info("cluster-scoped dispatcher no longer required, cleaning up deployment resources")

	return r.cleanDispatcherResources(ctx, r.systemNamespace)
}

func (r *Reconciler) finalizeNamespaceScopedKind(ctx context.Context, nc *v1alpha1.NatsJetStreamChannel) reconciler.Event {
	logger := logging.FromContext(ctx)

	allChannels, err := r.jsmChannelLister.NatsJetStreamChannels(nc.Namespace).List(labels.Everything())
	if err != nil {
		logger.Errorw("failed to list NatsJetStreamChannels", zap.Error(err))
		// TODO: is this ok? should we be returning an actual event within FinalizeKind?
		return err
	}

	var stillRequireDispatcher bool
	for _, channel := range allChannels {
		// ignore the channel being finalized.
		if channel.UID == nc.UID {
			continue
		}

		if channel.Annotations[eventing.ScopeAnnotationKey] == eventing.ScopeNamespace {
			stillRequireDispatcher = true
			break
		}
	}

	if stillRequireDispatcher {
		logger.Debugw("namespace-scoped dispatcher still required, nothing more to do")
		return nil
	}

	logger.Info("namespace-scoped dispatcher no longer required, cleaning up deployment resources")

	return r.cleanDispatcherResources(ctx, nc.Namespace)
}

// cleanDispatcherResources cleans up all the dispatcher-related resources after they're no longer required by any
// NatsJetStreamChannels. For now this just sets the Deployment to have 0 replicas, if we determine this is an ideal
// approach then this could be optimized by implementing a patch rather than a read-update-delete loop.
func (r *Reconciler) cleanDispatcherResources(ctx context.Context, namespace string) error {
	logger := logging.FromContext(ctx)

	deployment, err := r.deploymentLister.Deployments(namespace).Get(jetstream.DispatcherName)
	if err != nil {
		if apierrs.IsNotFound(err) {
			logger.Warnw("dispatcher not wanted but does not exist when it is expected")
			return nil
		}

		logger.Errorw("failed to Get dispatcher Deployment", zap.Error(err))
		return err
	}

	toUpdate := deployment.DeepCopy()
	toUpdate.Spec.Replicas = pointer.Int32(0)

	_, err = r.kubeClientSet.AppsV1().Deployments(namespace).Update(ctx, toUpdate, metav1.UpdateOptions{})
	if err != nil {
		logger.Errorw("failed to update dispatcher Deployment to zero-replicas", zap.Error(err))
		return err
	}

	return nil
}
