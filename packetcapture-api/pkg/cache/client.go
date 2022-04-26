// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package cache

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/client-go/tools/cache"

	log "github.com/sirupsen/logrus"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	lmaauth "github.com/tigera/lma/pkg/auth"

	lmak8s "github.com/tigera/lma/pkg/k8s"

	"k8s.io/client-go/rest"

	informers "github.com/tigera/api/pkg/client/informers_generated/externalversions/projectcalico/v3"
)

// ClientCache caches a client set and k8s config per cluster id
// in order to make use of long lasting client connections
type ClientCache interface {
	GetClientAndConfig(clusterID string) (lmak8s.ClientSet, *rest.Config, error)
	GetAuthorizer(clusterID string) (lmaauth.RBACAuthorizer, error)
	Init() error
	StartBackendSync(stop chan struct{}) error
}

type clientCache struct {
	csFactory lmak8s.ClientSetFactory
	cache     map[string]*clientBundle
	rw        sync.RWMutex
}

type clientBundle struct {
	lmak8s.ClientSet
	*rest.Config
	lmaauth.RBACAuthorizer
}

// NewClientCache return an implementation for ClientCache that stores the
// client set and k8s configuration against a cluster id
func NewClientCache(csFactory lmak8s.ClientSetFactory) ClientCache {
	return &clientCache{csFactory: csFactory, cache: make(map[string]*clientBundle)}
}

func (cc *clientCache) GetClientAndConfig(clusterID string) (lmak8s.ClientSet, *rest.Config, error) {
	var bundle, ok = cc.get(clusterID)

	if !ok {
		return nil, nil, fmt.Errorf("failed to match %s against a client", clusterID)
	}

	return bundle.ClientSet, bundle.Config, nil
}

func (cc *clientCache) GetAuthorizer(clusterID string) (lmaauth.RBACAuthorizer, error) {
	var bundle, ok = cc.get(clusterID)

	if !ok {
		return nil, fmt.Errorf("failed to match %s against an authorizer", clusterID)
	}

	return bundle.RBACAuthorizer, nil
}

func (cc *clientCache) Init() error {
	_, err := cc.load(lmak8s.DefaultCluster)
	if err != nil {
		return err
	}

	return nil
}

func (cc *clientCache) StartBackendSync(stop chan struct{}) error {
	defaultBundle, ok := cc.get(lmak8s.DefaultCluster)
	if !ok {
		return fmt.Errorf("missing client for default cluster")
	}

	var sharedInformers = informers.NewManagedClusterInformer(defaultBundle, time.Second*5, cache.Indexers{})
	var onAdd = func(obj interface{}) {
		cluster, ok := obj.(*v3.ManagedCluster)
		if !ok {
			log.Debugf("Interface conversion failed for %v", obj)
			return
		}
		if isConnected(*cluster) {
			log.Debugf("Cluster %s is connected after add", cluster.ObjectMeta.Name)
			var _, err = cc.load(cluster.ObjectMeta.Name)
			if err != nil {
				log.WithError(err).Errorf("Failed to load cluster after add %s", cluster.ObjectMeta.Name)
			}
		}
	}
	var onDelete = func(obj interface{}) {
		cluster, ok := obj.(*v3.ManagedCluster)
		if !ok {
			log.Debugf("Interface conversion failed for %v", obj)
			return
		}
		log.Debugf("Cluster %s not is connected after delete", cluster.ObjectMeta.Name)
		cc.delete(cluster.ObjectMeta.Name)
	}

	var onUpdate = func(oldObj, newObj interface{}) {
		newCluster, ok := newObj.(*v3.ManagedCluster)
		if !ok {
			log.Debugf("Interface conversion failed for %v", newObj)
			return
		}
		oldCluster, ok := newObj.(*v3.ManagedCluster)
		if !ok {
			log.Debugf("Interface conversion failed for %v", oldCluster)
			return
		}

		if isConnected(*newCluster) {
			log.Debugf("Cluster %s is connected after update", newCluster.ObjectMeta.Name)
			var _, err = cc.load(newCluster.ObjectMeta.Name)
			if err != nil {
				log.WithError(err).Errorf("Failed to load cluster after update %s", newCluster.ObjectMeta.Name)
			}
		} else {
			log.Debugf("Cluster %s not is connected after update", newCluster.ObjectMeta.Name)
			cc.delete(newCluster.ObjectMeta.Name)
		}
	}

	sharedInformers.AddEventHandler(&cache.ResourceEventHandlerFuncs{
		AddFunc:    onAdd,
		DeleteFunc: onDelete,
		UpdateFunc: onUpdate,
	})

	sharedInformers.Run(stop)

	return nil
}

func (cc *clientCache) get(clusterID string) (*clientBundle, bool) {
	cc.rw.RLock()
	defer cc.rw.RUnlock()

	cs, ok := cc.cache[clusterID]
	return cs, ok
}

func (cc *clientCache) load(clusterID string) (*clientBundle, error) {
	cc.rw.Lock()
	defer cc.rw.Unlock()

	var cs, err = cc.csFactory.NewClientSetForApplication(clusterID)
	if err != nil {
		return nil, err
	}
	var tuple = &clientBundle{cs,
		cc.csFactory.NewRestConfigForApplication(clusterID),
		lmaauth.NewRBACAuthorizer(cs),
	}
	cc.cache[clusterID] = tuple

	return tuple, nil
}

func (cc *clientCache) delete(clusterID string) {
	cc.rw.Lock()
	defer cc.rw.Unlock()

	delete(cc.cache, clusterID)
}

func isConnected(managedCluster v3.ManagedCluster) bool {
	for _, condition := range managedCluster.Status.Conditions {
		if condition.Type == "ManagedClusterConnected" && condition.Status == "True" {
			return true
		}
	}
	return false
}
