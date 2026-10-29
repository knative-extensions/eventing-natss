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
	"cmp"
	"context"
	"fmt"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/tracker"

	eventingv1 "knative.dev/eventing/pkg/apis/eventing/v1"

	eventingv1alpha1 "knative.dev/eventing-natss/pkg/apis/eventing/v1alpha1"
	brokerconfig "knative.dev/eventing-natss/pkg/broker/config"
)

var (
	// brokerConfigGVK is the kind a Broker's spec.config must reference.
	brokerConfigGVK = eventingv1alpha1.SchemeGroupVersion.WithKind("NatsJetStreamBrokerConfig")
	brokerConfigGVR = eventingv1alpha1.SchemeGroupVersion.WithResource("natsjetstreambrokerconfigs")
)

// getBrokerConfig loads the broker configuration with the following precedence:
//  1. Config from the NatsJetStreamBrokerConfig referenced by spec.config,
//     either explicitly or through Knative's namespace/cluster defaulting
//     (if present, use it entirely)
//  2. Deprecated broker-specific config from the inline JSON annotation
//  3. Namespace-specific config from the deprecated ConfigMap (use it entirely)
//  4. Cluster default config from the deprecated ConfigMap (use it entirely)
//  5. Hardcoded defaults
func (r *Reconciler) getBrokerConfig(ctx context.Context, b *eventingv1.Broker) (*eventingv1alpha1.NatsJetStreamBrokerConfigSpec, error) {
	// Warn whichever source is used below.
	warnLegacyAnnotation(ctx, b)

	ref := b.Spec.Config
	// Knative's defaulting webhook can supply a ConfigMap reference for any
	// broker class, so a ConfigMap reference is not a NATS config source.
	if ref != nil && !isConfigMapRef(ref) {
		return r.getReferencedBrokerConfig(ctx, b)
	}

	if cfg, err := brokerconfig.GetConfigFromAnnotation(b.Annotations); err != nil {
		return nil, err
	} else if cfg != nil {
		logging.FromContext(ctx).Debugw("Using broker-specific config from deprecated annotation")
		return cfg, nil
	}

	if ref != nil {
		warnIgnoredConfigMapRef(ctx, b)
	}
	return r.getLegacyDefaultsConfig(ctx, b)
}

func isConfigMapRef(ref *duckv1.KReference) bool {
	return ref.APIVersion == "v1" && ref.Kind == "ConfigMap"
}

func warnLegacyAnnotation(ctx context.Context, b *eventingv1.Broker) {
	if b.Annotations[brokerconfig.LegacyConfigAnnotation] == "" {
		return
	}
	controller.GetEventRecorder(ctx).Eventf(b, corev1.EventTypeWarning, ReasonLegacyConfigDeprecated,
		"Annotation %q is deprecated; use a %s referenced by spec.config",
		brokerconfig.LegacyConfigAnnotation, brokerConfigGVK.Kind)
}

// warnIgnoredConfigMapRef warns that the spec.config ConfigMap reference is not used.
//
//nolint:staticcheck // Legacy defaults stay supported until they are removed.
func warnIgnoredConfigMapRef(ctx context.Context, b *eventingv1.Broker) {
	ref := b.Spec.Config
	controller.GetEventRecorder(ctx).Eventf(b, corev1.EventTypeWarning, ReasonConfigMapReferenceIgnored,
		"Ignoring spec.config ConfigMap %s/%s; using broker defaults from ConfigMap %s/%s or built-in defaults",
		cmp.Or(ref.Namespace, b.Namespace), ref.Name, b.Namespace, brokerconfig.ConfigMapName)
}

// getLegacyDefaultsConfig loads the deprecated natsjetstream-broker-config
// ConfigMap from the broker's namespace and falls back to the built-in
// defaults when it does not exist.
//
//nolint:staticcheck // Legacy defaults stay supported until they are removed.
func (r *Reconciler) getLegacyDefaultsConfig(ctx context.Context, b *eventingv1.Broker) (*eventingv1alpha1.NatsJetStreamBrokerConfigSpec, error) {
	cm, err := r.kubeClientSet.CoreV1().ConfigMaps(b.Namespace).Get(ctx, brokerconfig.ConfigMapName, metav1.GetOptions{})
	switch {
	case apierrs.IsNotFound(err):
		logging.FromContext(ctx).Debugw("Broker config ConfigMap not found, using hardcoded defaults",
			zap.String("configmap", brokerconfig.ConfigMapName),
			zap.String("namespace", b.Namespace))
		cm = nil
	case err != nil:
		return nil, fmt.Errorf("failed to get config map: %w", err)
	}

	defaults, err := brokerconfig.LoadDefaultsFromConfigMap(cm)
	if err != nil {
		return nil, err
	}
	if defaults != nil {
		controller.GetEventRecorder(ctx).Eventf(b, corev1.EventTypeWarning, ReasonLegacyDefaultsConfigDeprecated,
			"Broker defaults in ConfigMap %s/%s are deprecated; use a %s referenced by config-br-defaults or spec.config",
			cm.Namespace, cm.Name, brokerConfigGVK.Kind)
	}
	return defaults.ConfigFor(b.Namespace), nil
}

// getReferencedBrokerConfig returns a copy of the spec of the
// NatsJetStreamBrokerConfig that the broker's spec.config points at, and tracks
// it so that the broker is reconciled again when it changes.
func (r *Reconciler) getReferencedBrokerConfig(ctx context.Context, b *eventingv1.Broker) (*eventingv1alpha1.NatsJetStreamBrokerConfigSpec, error) {
	ref := b.Spec.Config
	if ref.APIVersion != brokerConfigGVK.GroupVersion().String() || ref.Kind != brokerConfigGVK.Kind {
		return nil, fmt.Errorf("unsupported spec.config %s %q: only %s %s is supported",
			ref.Kind, ref.APIVersion, brokerConfigGVK.Kind, brokerConfigGVK.GroupVersion())
	}
	namespace := cmp.Or(ref.Namespace, b.Namespace)
	if err := r.tracker.TrackReference(tracker.Reference{
		APIVersion: ref.APIVersion,
		Kind:       ref.Kind,
		Namespace:  namespace,
		Name:       ref.Name,
	}, b); err != nil {
		return nil, fmt.Errorf("failed to track %s %s/%s: %w", ref.Kind, namespace, ref.Name, err)
	}
	obj, err := r.brokerConfigLister.ByNamespace(namespace).Get(ref.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s %s/%s: %w", ref.Kind, namespace, ref.Name, err)
	}
	cfg, err := decodeBrokerConfigSpec(obj)
	if err != nil {
		return nil, fmt.Errorf("invalid %s %s/%s: %w", ref.Kind, namespace, ref.Name, err)
	}
	logging.FromContext(ctx).Debugw("Using broker-specific config from "+ref.Kind,
		zap.String("namespace", namespace), zap.String("name", ref.Name))
	return cfg, nil
}
