// Copyright 2021 Tigera Inc. All rights reserved.

package alert

import (
	"context"

	"github.com/projectcalico/calico/linseed/pkg/client"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	calicoclient "github.com/tigera/api/pkg/client/clientset_generated/clientset"

	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/globalalert/controllers/controller"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/globalalert/worker"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/health"
)

const (
	GlobalAlertResourceName = "globalalerts"
)

// globalAlertController is responsible for watching GlobalAlert resource in a cluster.
type globalAlertController struct {
	linseedClient client.Client
	calicoCLI     calicoclient.Interface
	k8sClient     kubernetes.Interface
	clusterName   string
	tenantID      string
	namespace     string
	cancel        context.CancelFunc
	worker        worker.Worker
}

// NewGlobalAlertController returns a globalAlertController and for each object it watches,
// a health.Pinger object is created returned for health check.
func NewGlobalAlertController(calicoCLI calicoclient.Interface, linseedClient client.Client, k8sClient kubernetes.Interface, enableAnomalyDetection bool, adDetectionController controller.AnomalyDetectionController, adTrainingController controller.AnomalyDetectionController, clusterName string, tenantID string, namespace string, fipsModeEnabled bool) (controller.Controller, []health.Pinger) {
	c := &globalAlertController{
		linseedClient: linseedClient,
		calicoCLI:     calicoCLI,
		k8sClient:     k8sClient,
		clusterName:   clusterName,
		tenantID:      tenantID,
		namespace:     namespace,
	}

	// Create worker to watch GlobalAlert resource in the cluster
	c.worker = worker.New(
		&globalAlertReconciler{
			linseedClient:          c.linseedClient,
			calicoCLI:              c.calicoCLI,
			k8sClient:              k8sClient,
			adDetectionController:  adDetectionController,
			adTrainingController:   adTrainingController,
			alertNameToAlertState:  map[string]alertState{},
			clusterName:            c.clusterName,
			tenantID:               c.tenantID,
			namespace:              namespace,
			enableAnomalyDetection: enableAnomalyDetection,
			fipsModeEnabled:        fipsModeEnabled,
		})

	pinger := c.worker.AddWatch(
		cache.NewListWatchFromClient(c.calicoCLI.ProjectcalicoV3().RESTClient(), GlobalAlertResourceName, "", fields.Everything()),
		&v3.GlobalAlert{})

	return c, []health.Pinger{pinger}
}

// Run starts the GlobalAlert monitoring routine.
func (c *globalAlertController) Run(parentCtx context.Context) {
	var ctx context.Context
	ctx, c.cancel = context.WithCancel(parentCtx)
	log.Infof("Starting alert controller for cluster %s", c.clusterName)
	go c.worker.Run(ctx.Done())
}

// Close cancels the GlobalAlert worker context and removes health check for all the objects that worker watches.
func (c *globalAlertController) Close() {
	c.worker.Close()
	c.cancel()
}
