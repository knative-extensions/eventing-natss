/*
Copyright 2022 The Knative Authors

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

package testing

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakekubeclientset "k8s.io/client-go/kubernetes/fake"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	rbacv1listers "k8s.io/client-go/listers/rbac/v1"
	"k8s.io/client-go/tools/cache"
	fakenatsslientset "knative.dev/eventing-natss/pkg/client/clientset/versioned/fake"

	fakeeventsclientset "knative.dev/eventing/pkg/client/clientset/versioned/fake"
	"knative.dev/pkg/reconciler/testing"

	natssv1alpha1 "knative.dev/eventing-natss/pkg/apis/messaging/v1alpha1"
	jetstreamlisters "knative.dev/eventing-natss/pkg/client/listers/messaging/v1alpha1"
)

var clientSetSchemes = []func(*runtime.Scheme) error{
	fakekubeclientset.AddToScheme,
	fakeeventsclientset.AddToScheme,
	fakenatsslientset.AddToScheme,
}

type Listers struct {
	sorter testing.ObjectSorter
}

func NewListers(objs []runtime.Object) Listers {
	scheme := runtime.NewScheme()

	for _, addTo := range clientSetSchemes {
		addTo(scheme)
	}

	ls := Listers{
		sorter: testing.NewObjectSorter(scheme),
	}

	ls.sorter.AddObjects(objs...)

	return ls
}

func (l *Listers) indexerFor(obj runtime.Object) cache.Indexer {
	return l.sorter.IndexerForObjectType(obj)
}

func (l *Listers) GetKubeObjects() []runtime.Object {
	return l.sorter.ObjectsForSchemeFunc(fakekubeclientset.AddToScheme)
}

func (l *Listers) GetNatssObjects() []runtime.Object {
	return l.sorter.ObjectsForSchemeFunc(fakenatsslientset.AddToScheme)
}

func (l *Listers) GetEventingObjects() []runtime.Object {
	return l.sorter.ObjectsForSchemeFunc(fakeeventsclientset.AddToScheme)
}

func (l *Listers) GetEventsObjects() []runtime.Object {
	return l.sorter.ObjectsForSchemeFunc(fakeeventsclientset.AddToScheme)
}

func (l *Listers) GetAllObjects() []runtime.Object {
	all := l.GetEventsObjects()
	all = append(all, l.GetKubeObjects()...)
	all = append(all, l.GetNatssObjects()...)
	return all
}

func (l *Listers) GetServiceLister() corev1listers.ServiceLister {
	return corev1listers.NewServiceLister(l.indexerFor(&corev1.Service{}))
}

func (l *Listers) GetServiceAccountLister() corev1listers.ServiceAccountLister {
	return corev1listers.NewServiceAccountLister(l.indexerFor(&corev1.ServiceAccount{}))
}

func (l *Listers) GetEndpointsLister() corev1listers.EndpointsLister {
	return corev1listers.NewEndpointsLister(l.indexerFor(&discoveryv1.EndpointSlice{}))
}

func (l *Listers) GetRoleBindingLister() rbacv1listers.RoleBindingLister {
	return rbacv1listers.NewRoleBindingLister(l.indexerFor(&rbacv1.RoleBinding{}))
}

func (l *Listers) GetDeploymentLister() appsv1listers.DeploymentLister {
	return appsv1listers.NewDeploymentLister(l.indexerFor(&appsv1.Deployment{}))
}

func (l *Listers) GetNatsJetstreamChannelLister() jetstreamlisters.NatsJetStreamChannelLister {
	return jetstreamlisters.NewNatsJetStreamChannelLister(l.indexerFor(&natssv1alpha1.NatsJetStreamChannel{}))
}
