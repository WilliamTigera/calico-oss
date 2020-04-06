// Copyright (c) 2019-2020 Tigera, Inc. All rights reserved.

package managedcluster

import (
	"fmt"
	"sync"

	"github.com/projectcalico/kube-controllers/pkg/config"

	relasticsearch "github.com/projectcalico/kube-controllers/pkg/resource/elasticsearch"

	"github.com/projectcalico/kube-controllers/pkg/controllers/elasticsearchconfiguration"
	log "github.com/sirupsen/logrus"
	"github.com/tigera/api/pkg/apis/projectcalico/v3"
	tigeraapi "github.com/tigera/api/pkg/client/clientset_generated/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

type managedClusterESControllerReconciler struct {
	sync.Mutex
	createManagedK8sCLI func(string) (kubernetes.Interface, error)
	esServiceURL        string
	managementK8sCLI    kubernetes.Interface
	calicoCLI           tigeraapi.Interface
	esK8sCLI            relasticsearch.RESTClient
	// the only information we need about an elasticsearch cluster controller for a ManagedCluster is the channel to stop
	// it. The exists of this channel can tell us if we have a controller for a ManagedCluster and the only action we would
	// want to take on one is to stop it
	managedClustersStopChans map[string]chan struct{}
	cfg                      config.ElasticsearchCfgControllerCfg
}

// Reconcile finds the ManagedCluster resource specified by the name and either adds, removes, or recreates the elasticsearch
// configuration controller for that managed cluster. If the ManagedCluster that's being reconciled exists is connected
// then the elasticsearch configuration controller for that managed cluster is added or recreated. If the ManagedCluster
// doesn't exist or is no longer connected then the Elasticsearch configuration controller is stopped for that ManagedCluster,
// if there is one running.
func (c *managedClusterESControllerReconciler) Reconcile(name types.NamespacedName) error {
	reqLogger := log.WithField("request", name)
	reqLogger.Info("Reconciling ManagedClusters")

	mc, err := c.calicoCLI.ProjectcalicoV3().ManagedClusters().Get(name.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Info("ManagedCluster not found")
			// In case the ManagedCluster resource was delete remove the controller for that ManagedCluster
			c.removeManagedClusterWatch(name.Name)
			return nil
		}
		return err
	}
	log.WithField("mc", mc).Info("cluster")
	if !clusterConnected(mc) {
		reqLogger.Info("Attempting to stop watch on disconnected cluster")
		c.removeManagedClusterWatch(mc.Name)
		return nil
	}

	reqLogger.Info("Attempting to start watch on connected cluster")
	if err := c.startManagedClusterWatch(mc.Name); err != nil {
		return err
	}

	return nil
}

func clusterConnected(managedCluster *v3.ManagedCluster) bool {
	for _, condition := range managedCluster.Status.Conditions {
		if condition.Type == v3.ManagedClusterStatusTypeConnected && condition.Status == v3.ManagedClusterStatusValueTrue {
			return true
		}
	}
	return false
}

func (c *managedClusterESControllerReconciler) startManagedClusterWatch(name string) error {
	managedK8sCLI, err := c.createManagedK8sCLI(name)
	if err != nil {
		return err
	}

	c.removeManagedClusterWatch(name)
	c.addManagedClusterWatch(name, managedK8sCLI)

	return nil
}

func (c *managedClusterESControllerReconciler) removeManagedClusterWatch(name string) {
	c.Lock()
	defer c.Unlock()

	log.Infof("Removing cluster watch for %s", name)
	if st, exists := c.managedClustersStopChans[name]; exists {
		close(st)
		delete(c.managedClustersStopChans, name)
	}
}

func (c *managedClusterESControllerReconciler) addManagedClusterWatch(name string, managedK8sCLI kubernetes.Interface) {
	c.Lock()
	defer c.Unlock()

	log.Infof("Adding cluster watch for %s", name)
	// If this happens it's a programming error, setManagerClusterWatch should never be called if the managed cluster
	// already has an entry
	if _, exists := c.managedClustersStopChans[name]; exists {
		panic(fmt.Sprintf("a watch for managed cluster %s already exists", name))
	}

	esCredsController := elasticsearchconfiguration.New(name, c.esServiceURL, managedK8sCLI, c.managementK8sCLI, c.esK8sCLI, false, c.cfg)

	stop := make(chan struct{})
	go esCredsController.Run(stop)
	c.managedClustersStopChans[name] = stop
}

func (c *managedClusterESControllerReconciler) listenForRebootNotify() chan bool {
	listener := make(chan bool)
	go func() {
		for range listener {
			log.Info("Notified of management cluster changes, recreated managed cluster elasticsearch controllers")
			managedClusterList, err := c.calicoCLI.ProjectcalicoV3().ManagedClusters().List(metav1.ListOptions{})
			if err != nil {
				log.WithError(err).Error("failed to list the managed clusters, skipping requeue of ManagedCluster watches")
				continue
			}

			for _, mc := range managedClusterList.Items {
				if err := c.startManagedClusterWatch(mc.Name); err != nil {
					log.WithError(err).Error("couldn't reboot managed cluster watch")
				}
			}
		}
	}()

	return listener
}
