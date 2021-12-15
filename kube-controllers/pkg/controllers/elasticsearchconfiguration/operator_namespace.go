// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package elasticsearchconfiguration

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/projectcalico/calico/kube-controllers/pkg/controllers/worker"
	"github.com/projectcalico/calico/kube-controllers/pkg/resource"
)

const (
	defaultTigeraOperatorNamespace = "tigera-operator"
)

// fetchOperatorNamespace reads the active operator namespace and returns it.
func fetchOperatorNamespace(c kubernetes.Interface) (string, error) {
	cm, err := c.CoreV1().ConfigMaps("calico-system").Get(context.Background(), "active-operator", metav1.GetOptions{})
	if err != nil {
		// If not found then assume we are looking at a earlier version that did not use
		// the active-operator ConfigMap.
		if kerrors.IsNotFound(err) {
			return defaultTigeraOperatorNamespace, nil
		}
		return "", fmt.Errorf("unable to get the active-operator ConfigMap: %w", err)
	} else {
		if ns, ok := cm.Data["active-namespace"]; ok {
			return ns, nil
		} else {
			return "", fmt.Errorf("active-operator ConfigMap does not have the data field 'active-namespace'")
		}
	}
}

func addWatchForActiveOperator(w worker.Worker, c kubernetes.Interface) {
	w.AddWatch(
		cache.NewListWatchFromClient(c.CoreV1().RESTClient(), "configmaps", resource.CalicoNamespaceName,
			fields.ParseSelectorOrDie(fmt.Sprintf("metadata.name=%s", resource.ActiveOperatorConfigMapName))),
		&corev1.ConfigMap{},
		worker.ResourceWatchUpdate, worker.ResourceWatchDelete, worker.ResourceWatchAdd,
	)
}
