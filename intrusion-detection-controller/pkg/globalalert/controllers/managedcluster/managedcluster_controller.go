// Copyright 2021 Tigera Inc. All rights reserved.

package managedcluster

import (
	"context"

	"github.com/projectcalico/calico/linseed/pkg/client"

	log "github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	calicoclient "github.com/tigera/api/pkg/client/clientset_generated/clientset"

	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/controller"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/globalalert/worker"
	lmak8s "github.com/projectcalico/calico/lma/pkg/k8s"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// managedClusterController is responsible for watching ManagedCluster resource.
type managedClusterController struct {
	lsClient         client.Client
	tenantID         string
	calicoCLI        calicoclient.Interface
	clientSetFactory lmak8s.ClientSetFactory
	cancel           context.CancelFunc
	worker           worker.Worker
}

// NewManagedClusterController returns a managedClusterController and returns health.Pinger for resources it watches and also
// returns another health.Pinger that monitors health of GlobalAlertController in each of the managed cluster.
func NewManagedClusterController(clientSetFactory lmak8s.ClientSetFactory, calicoCLI calicoclient.Interface, lsClient client.Client, k8sClient kubernetes.Interface, client ctrlclient.WithWatch, namespace string, tenantID, tenantNamespace string) controller.Controller {
	m := &managedClusterController{
		lsClient:         lsClient,
		calicoCLI:        calicoCLI,
		clientSetFactory: clientSetFactory,
		tenantID:         tenantID,
	}

	// Create worker to watch ManagedCluster resource
	m.worker = worker.New(&managedClusterReconciler{
		namespace:                       namespace,
		lsClient:                        lsClient,
		managementCalicoCLI:             m.calicoCLI,
		clientSetFactory:                clientSetFactory,
		client:                          client,
		k8sClient:                       k8sClient,
		alertNameToAlertControllerState: map[string]alertControllerState{},
		tenantID:                        tenantID,
		tenantNamespace:                 tenantNamespace,
	})

	m.worker.AddWatch(
		cache.NewListWatchFromClient(m.calicoCLI.ProjectcalicoV3().RESTClient(), "managedclusters", tenantNamespace, fields.Everything()),
		&v3.ManagedCluster{})

	log.Info("creating a new managed cluster controller")

	return m
}

// Run starts a ManagedCluster monitoring routine.
func (m *managedClusterController) Run(parentCtx context.Context) {
	var ctx context.Context
	ctx, m.cancel = context.WithCancel(parentCtx)
	log.Info("Starting managed cluster controllers")
	go m.worker.Run(ctx.Done())
}

// Close cancels the ManagedCluster worker context and removes health check for all the objects that worker watches.
func (m *managedClusterController) Close() {
	log.Infof("closing a managed cluster controller %+v", m)
	m.worker.Close()
	m.cancel()
}

func (m *managedClusterController) Ping(ctx context.Context) error {
	return nil
}
