// Copyright (c) 2019-2020 Tigera, Inc. All rights reserved.

package authorization

import (
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

type clusterRoleReconciler struct {
	k8sCLI          kubernetes.Interface
	resourceUpdates chan resourceUpdate
}

type clusterRoleBindingReconciler struct {
	k8sCLI          kubernetes.Interface
	resourceUpdates chan resourceUpdate
}

func (r *clusterRoleReconciler) Reconcile(namespacedName types.NamespacedName) error {
	typ := resourceUpdated
	clusterRoleName := namespacedName.Name

	clusterRole, err := r.k8sCLI.RbacV1().ClusterRoles().Get(clusterRoleName, metav1.GetOptions{})
	if err != nil {
		if !kerrors.IsNotFound(err) {
			return err
		}

		typ = resourceDeleted
	}

	r.resourceUpdates <- resourceUpdate{
		typ:      typ,
		name:     clusterRoleName,
		resource: clusterRole,
	}

	return nil
}

func (r *clusterRoleBindingReconciler) Reconcile(namespacedName types.NamespacedName) error {
	typ := resourceUpdated
	clusterRoleBindingName := namespacedName.Name

	clusterRoleBinding, err := r.k8sCLI.RbacV1().ClusterRoleBindings().Get(clusterRoleBindingName, metav1.GetOptions{})
	if err != nil {
		if !kerrors.IsNotFound(err) {
			return err
		}

		typ = resourceDeleted
	}

	r.resourceUpdates <- resourceUpdate{
		typ:      typ,
		name:     clusterRoleBindingName,
		resource: clusterRoleBinding,
	}

	return nil
}
