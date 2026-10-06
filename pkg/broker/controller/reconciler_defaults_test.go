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
	"encoding/json"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	"knative.dev/pkg/controller"

	eventingv1alpha1 "knative.dev/eventing-natss/pkg/apis/eventing/v1alpha1"
	brokerconfig "knative.dev/eventing-natss/pkg/broker/config"
	"knative.dev/eventing-natss/pkg/broker/controller/resources"
)

// The fake clientset stores objects as written, but an API server adds defaults.
// These tests reconcile against objects with those defaults applied, as the
// informer cache would hold them.

// serverDefaultedDeployment returns d with the defaults an API server adds.
func serverDefaultedDeployment(d *appsv1.Deployment) *appsv1.Deployment {
	d = d.DeepCopy()
	if d.Annotations == nil {
		d.Annotations = map[string]string{}
	}
	d.Annotations["deployment.kubernetes.io/revision"] = "1"

	spec := &d.Spec
	spec.Strategy = appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{
			MaxUnavailable: ptr.To(intstr.FromString("25%")),
			MaxSurge:       ptr.To(intstr.FromString("25%")),
		},
	}
	spec.RevisionHistoryLimit = ptr.To[int32](10)
	spec.ProgressDeadlineSeconds = ptr.To[int32](600)

	pod := &spec.Template.Spec
	pod.DNSPolicy = corev1.DNSClusterFirst
	pod.RestartPolicy = corev1.RestartPolicyAlways
	if pod.TerminationGracePeriodSeconds == nil {
		pod.TerminationGracePeriodSeconds = ptr.To[int64](30)
	}
	pod.SchedulerName = corev1.DefaultSchedulerName
	pod.SecurityContext = &corev1.PodSecurityContext{}
	for i := range pod.Containers {
		c := &pod.Containers[i]
		c.TerminationMessagePath = corev1.TerminationMessagePathDefault
		c.TerminationMessagePolicy = corev1.TerminationMessageReadFile
		c.ImagePullPolicy = corev1.PullIfNotPresent
		for _, p := range []*corev1.Probe{c.LivenessProbe, c.ReadinessProbe} {
			if p == nil {
				continue
			}
			if p.HTTPGet != nil {
				p.HTTPGet.Scheme = corev1.URISchemeHTTP
			}
			if p.TimeoutSeconds == 0 {
				p.TimeoutSeconds = 1
			}
			if p.SuccessThreshold == 0 {
				p.SuccessThreshold = 1
			}
			if p.FailureThreshold == 0 {
				p.FailureThreshold = 3
			}
		}
		for j := range c.Env {
			if f := c.Env[j].ValueFrom; f != nil && f.FieldRef != nil && f.FieldRef.APIVersion == "" {
				f.FieldRef.APIVersion = "v1"
			}
		}
	}
	return d
}

// serverDefaultedService returns svc with the defaults an API server adds.
func serverDefaultedService(svc *corev1.Service) *corev1.Service {
	svc = svc.DeepCopy()
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	svc.Spec.ClusterIP = "10.96.0.10"
	svc.Spec.ClusterIPs = []string{"10.96.0.10"}
	svc.Spec.SessionAffinity = corev1.ServiceAffinityNone
	svc.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv4Protocol}
	svc.Spec.IPFamilyPolicy = ptr.To(corev1.IPFamilyPolicySingleStack)
	svc.Spec.InternalTrafficPolicy = ptr.To(corev1.ServiceInternalTrafficPolicyCluster)
	return svc
}

// drainEvents returns the events recorded so far.
func drainEvents(rec *record.FakeRecorder) []string {
	var events []string
	for {
		select {
		case e := <-rec.Events:
			events = append(events, e)
		default:
			return events
		}
	}
}

func TestReconcileFilterDeploymentAgainstServerDefaults(t *testing.T) {
	kube := kubefake.NewSimpleClientset()
	rec := record.NewFakeRecorder(100)
	ctx := controller.WithEventRecorder(testContext(), rec)
	r := &Reconciler{
		kubeClientSet:        kube,
		filterImage:          "filter:latest",
		filterServiceAccount: "dp-sa",
		natsURL:              "nats://localhost:4222",
	}
	b := testBroker(testNamespace, testBrokerName)
	cfg := brokerconfig.DefaultBrokerConfig()
	name := resources.FilterName(testBrokerName)
	deployments := kube.AppsV1().Deployments(testNamespace)

	get := func() *appsv1.Deployment {
		t.Helper()
		d, err := deployments.Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	// reconcile runs one reconcile and returns the Updates and events it caused.
	reconcile := func() (updates int, events []string) {
		t.Helper()
		r.deploymentLister = newDeploymentLister()
		if stored, err := deployments.Get(context.Background(), name, metav1.GetOptions{}); err == nil {
			r.deploymentLister = newDeploymentLister(serverDefaultedDeployment(stored))
		}
		kube.ClearActions()
		if err := r.reconcileFilterDeployment(ctx, b, "TEST_STREAM", cfg); err != nil {
			t.Fatalf("reconcileFilterDeployment() error: %v", err)
		}
		for _, a := range kube.Actions() {
			if a.Matches("update", "deployments") {
				updates++
			}
		}
		return updates, drainEvents(rec)
	}
	// settled checks that a reconcile does nothing.
	settled := func() {
		t.Helper()
		if updates, events := reconcile(); updates != 0 || len(events) != 0 {
			t.Errorf("settled reconcile made %d updates and recorded %q, want neither", updates, events)
		}
	}
	// updated checks that a reconcile makes exactly one Update.
	updated := func(what string) {
		t.Helper()
		updates, events := reconcile()
		if updates != 1 {
			t.Errorf("%s: %d updates, want 1", what, updates)
		}
		if len(events) != 1 || !strings.Contains(events[0], ReasonFilterDeploymentUpdated) {
			t.Errorf("%s: events = %q, want one %s", what, events, ReasonFilterDeploymentUpdated)
		}
	}

	if updates, events := reconcile(); updates != 0 || len(events) != 1 || !strings.Contains(events[0], ReasonFilterDeploymentCreated) {
		t.Fatalf("first reconcile made %d updates and recorded %q, want a creation", updates, events)
	}
	settled()

	cfg.Filter = &eventingv1alpha1.DeploymentTemplate{
		NodeSelector: map[string]string{"disk": "ssd"},
		PodLabels:    map[string]string{"pod-team": "a"},
		Env:          []corev1.EnvVar{{Name: "CONSUMER_FETCH_BATCH_SIZE", Value: "7"}},
	}
	updated("template added")
	got := get().Spec.Template
	if got.Spec.NodeSelector["disk"] != "ssd" || got.Labels["pod-team"] != "a" || !hasEnv(got.Spec.Containers[0], "CONSUMER_FETCH_BATCH_SIZE") {
		t.Errorf("template was not applied: %+v", got)
	}
	settled()

	// Removing a field from the configuration must remove it from the Deployment.
	cfg.Filter = nil
	updated("template removed")
	got = get().Spec.Template
	if len(got.Spec.NodeSelector) != 0 || got.Labels["pod-team"] != "" || hasEnv(got.Spec.Containers[0], "CONSUMER_FETCH_BATCH_SIZE") {
		t.Errorf("removed template fields are still set: %+v", got)
	}
	settled()

	drifted := get()
	drifted.Spec.Template.Spec.Containers[0].Image = "someone-elses:image"
	if _, err := deployments.Update(context.Background(), drifted, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	updated("image drift")
	if image := get().Spec.Template.Spec.Containers[0].Image; image != "filter:latest" {
		t.Errorf("image = %q, want it restored to filter:latest", image)
	}
	settled()

	// A Deployment tracked before the spec hash existed is updated once.
	legacy := get()
	tracked, err := trackedFilterMetadata(legacy)
	if err != nil {
		t.Fatal(err)
	}
	tracked.SpecHash = ""
	data, err := json.Marshal(tracked)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Annotations[filterMetadataAnnotation] = string(data)
	if _, err := deployments.Update(context.Background(), legacy, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	updated("tracking annotation without a spec hash")
	settled()
}

func hasEnv(c corev1.Container, name string) bool {
	for _, e := range c.Env {
		if e.Name == name {
			return true
		}
	}
	return false
}

func TestReconcileFilterServiceAgainstServerDefaults(t *testing.T) {
	kube := kubefake.NewSimpleClientset()
	ctx := testContext()
	r := &Reconciler{kubeClientSet: kube}
	b := testBroker(testNamespace, testBrokerName)
	name := resources.FilterName(testBrokerName)
	services := kube.CoreV1().Services(testNamespace)

	reconcile := func() (updates int) {
		t.Helper()
		r.serviceLister = newServiceLister()
		if stored, err := services.Get(context.Background(), name, metav1.GetOptions{}); err == nil {
			r.serviceLister = newServiceLister(serverDefaultedService(stored))
		}
		kube.ClearActions()
		if _, err := r.reconcileFilterService(ctx, b); err != nil {
			t.Fatalf("reconcileFilterService() error: %v", err)
		}
		for _, a := range kube.Actions() {
			if a.Matches("update", "services") {
				updates++
			}
		}
		return updates
	}

	if updates := reconcile(); updates != 0 {
		t.Fatalf("creating the service made %d updates, want 0", updates)
	}
	if updates := reconcile(); updates != 0 {
		t.Errorf("settled reconcile made %d updates, want 0", updates)
	}

	drifted, err := services.Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	drifted.Spec.Selector = map[string]string{"app": "someone-elses"}
	if _, err := services.Update(context.Background(), drifted, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if updates := reconcile(); updates != 1 {
		t.Errorf("selector drift made %d updates, want 1", updates)
	}
	if updates := reconcile(); updates != 0 {
		t.Errorf("reconcile after restoring the selector made %d updates, want 0", updates)
	}
}
