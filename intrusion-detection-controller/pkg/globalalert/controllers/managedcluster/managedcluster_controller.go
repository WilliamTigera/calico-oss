// Copyright 2021 Tigera Inc. All rights reserved.

package managedcluster

import (
	"context"

	log "github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/globalalert/controllers/controller"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/globalalert/worker"
	es "github.com/projectcalico/calico/intrusion-detection-controller/pkg/storage"
	lma "github.com/projectcalico/calico/lma/pkg/elastic"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	calicoclient "github.com/tigera/api/pkg/client/clientset_generated/clientset"
)

// managedClusterController is responsible for watching ManagedCluster resource.
type managedClusterController struct {
	lmaESClient            lma.Client
	indexSettings          es.IndexSettings
	calicoCLI              calicoclient.Interface
	createManagedCalicoCLI func(string) (calicoclient.Interface, error)
	cancel                 context.CancelFunc
	worker                 worker.Worker
}

// NewManagedClusterController returns a managedClusterController and returns health.Pinger for resources it watches and also
// returns another health.Pinger that monitors health of GlobalAlertController in each of the managed cluster.
func NewManagedClusterController(calicoCLI calicoclient.Interface, lmaESClient lma.Client, k8sClient kubernetes.Interface,
	enableAnomalyDetection bool, anomalyTrainingController controller.AnomalyDetectionController,
	anomalyDetectionController controller.AnomalyDetectionController, indexSettings es.IndexSettings, namespace string,
	createManagedCalicoCLI func(string) (calicoclient.Interface, error), fipsModeEnabled bool,
) controller.Controller {
	m := &managedClusterController{
		lmaESClient:            lmaESClient,
		indexSettings:          indexSettings,
		calicoCLI:              calicoCLI,
		createManagedCalicoCLI: createManagedCalicoCLI,
	}

	// Create worker to watch ManagedCluster resource
	m.worker = worker.New(&managedClusterReconciler{
		createManagedCalicoCLI:          m.createManagedCalicoCLI,
		namespace:                       namespace,
		indexSettings:                   m.indexSettings,
		lmaESClient:                     m.lmaESClient,
		managementCalicoCLI:             m.calicoCLI,
		k8sClient:                       k8sClient,
		adTrainingController:            anomalyTrainingController,
		adDetectionController:           anomalyDetectionController,
		alertNameToAlertControllerState: map[string]alertControllerState{},
		enableAnomalyDetection:          enableAnomalyDetection,
		fipsModeEnabled:                 fipsModeEnabled,
	})

	m.worker.AddWatch(
		cache.NewListWatchFromClient(m.calicoCLI.ProjectcalicoV3().RESTClient(), "managedclusters", "", fields.Everything()),
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
