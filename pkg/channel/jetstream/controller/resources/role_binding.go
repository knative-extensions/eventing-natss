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

package resources

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	roleKindRole        = "Role"
	roleKindClusterRole = "ClusterRole"
)

// MakeRoleBinding creates a RoleBinding object for the JetStream dispatcher
// service account 'sa' in the Namespace 'ns'.
func MakeRoleBinding(ns, name string, sa *corev1.ServiceAccount, roleName string, isClusterRole bool) *rbacv1.RoleBinding {
	var roleKind string
	if isClusterRole {
		roleKind = roleKindClusterRole
	} else {
		roleKind = roleKindRole
	}

	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     roleKind,
			Name:     roleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Namespace: sa.Namespace,
				Name:      sa.Name,
			},
		},
	}
}
