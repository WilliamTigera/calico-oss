// Copyright (c) 2019-2020 Tigera, Inc. All rights reserved.

package worker

// package worker contains code to watch k8s resources and react based on changes to those resources. This was abstracted
// out from common logic the controllers were using, so that all that's needed to create a new controller is declaring what
// resources you want to watch for and what to do when they're updated

import (
	"time"

	log "github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	uruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// ResourceWatch represents a type of update to react to when watching a resource
type ResourceWatch string

const (
	ResourceWatchAdd    ResourceWatch = "ADD"
	ResourceWatchUpdate ResourceWatch = "UPDATE"
	ResourceWatchDelete ResourceWatch = "DELETE"
)

// Reconciler is the interface that is used to react to changes to the resources that the worker is watching. When a change
// to a resource is detected, the Reconcile function of the passed in reconciler is used
type Reconciler interface {
	Reconcile(name types.NamespacedName) error
}

// Worker is the interface used to watch k8s resources and react to changes to those resources
type Worker interface {
	AddWatch(listWatcher cache.ListerWatcher, obj runtime.Object, handlers ...ResourceWatch)
	Run(workerCount int, stop chan struct{})
}

type worker struct {
	reconciler Reconciler
	workqueue.RateLimitingInterface
	watches []watch
}

// watch contains the information needed to create a resource watch
type watch struct {
	listWatcher cache.ListerWatcher
	obj         runtime.Object
	handlers    []ResourceWatch
}

// New creates a new Worker implementation
func New(reconciler Reconciler) Worker {
	return &worker{
		reconciler:            reconciler,
		RateLimitingInterface: workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
	}
}

// AddWatch registers a resource to watch and run the reconciler on changes to that resource
func (w *worker) AddWatch(listWatcher cache.ListerWatcher, obj runtime.Object, handlers ...ResourceWatch) {
	w.watches = append(w.watches, watch{
		listWatcher: listWatcher,
		obj:         obj,
		handlers:    handlers,
	})
}

func (w *worker) resourceEventHandlerFuncs(options ...ResourceWatch) cache.ResourceEventHandlerFuncs {
	r := cache.ResourceEventHandlerFuncs{}

	if len(options) == 0 || hasFuncOption(options, ResourceWatchAdd) {
		r.AddFunc = func(obj interface{}) {
			objMeta := obj.(metav1.Object)
			log.Debugf("Create event received for resource %s/%s", objMeta.GetName(), objMeta.GetNamespace())
			w.Add(types.NamespacedName{
				Name:      objMeta.GetName(),
				Namespace: objMeta.GetNamespace(),
			})
		}
	}

	if len(options) == 0 || hasFuncOption(options, ResourceWatchUpdate) {
		r.UpdateFunc = func(oldObj interface{}, newObj interface{}) {
			objMeta := newObj.(metav1.Object)
			log.Debugf("Create event received for resource %s/%s", objMeta.GetName(), objMeta.GetNamespace())
			w.Add(types.NamespacedName{
				Name:      objMeta.GetName(),
				Namespace: objMeta.GetNamespace(),
			})
		}
	}

	if len(options) == 0 || hasFuncOption(options, ResourceWatchDelete) {
		r.DeleteFunc = func(obj interface{}) {
			objMeta := obj.(metav1.Object)
			log.Debugf("Create event received for resource %s/%s", objMeta.GetName(), objMeta.GetNamespace())
			w.Add(types.NamespacedName{
				Name:      objMeta.GetName(),
				Namespace: objMeta.GetNamespace(),
			})
		}
	}
	return r
}

func hasFuncOption(options []ResourceWatch, search ResourceWatch) bool {
	for _, option := range options {
		if option == search {
			return true
		}
	}

	return false
}

// Run creates the resource watches then starts the worker. The worker will be started in a go routine, and workerCount
// determines how many routines are kicked off.
func (w *worker) Run(workerCount int, stop chan struct{}) {
	defer uruntime.HandleCrash()
	defer w.ShutDown()

	for _, watch := range w.watches {
		_, ctrl := cache.NewIndexerInformer(watch.listWatcher, watch.obj, 0, w.resourceEventHandlerFuncs(watch.handlers...),
			cache.Indexers{})

		go ctrl.Run(stop)
		for !ctrl.HasSynced() {
		}
	}

	for i := 0; i < workerCount; i++ {
		go wait.Until(w.startWorker, time.Second, stop)
	}

	<-stop
}

// startWorker starts processing items off the queue
func (w *worker) startWorker() {
	for w.processNextItem() {
	}
}

// processNextItem gets the next item off the queue and runs the Reconciler with that item
func (w *worker) processNextItem() bool {
	key, shutdown := w.Get()
	if shutdown {
		return false
	}
	defer w.Done(key)

	reqLogger := log.WithField("key", key)
	reqLogger.Debug("Processing next item")

	if err := w.reconciler.Reconcile(key.(types.NamespacedName)); err != nil {
		reqLogger.WithError(err).Error("An error occurred while processing the next item")
		if w.NumRequeues(key) < 5 {
			reqLogger.Debug("Rate limiting key")
			w.AddRateLimited(key)
			return true
		}

		reqLogger.Debug("Rate limiting retries reached, forgetting key")
		w.Forget(key)
		uruntime.HandleError(err)
	}

	reqLogger.Debug("Finished processing next item")

	return true
}
