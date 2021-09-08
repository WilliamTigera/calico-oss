// Copyright 2021 Tigera Inc. All rights reserved.

package controller

import "context"

type Controller interface {
	// Run will set up workers and add watches to k8s resource we are interested in and starts the worker.
	// It will block until parent's Done channel is closed, at which point it will cancel the current context
	// and that will trigger workers to shutdown the workqueue and finish processing their current work items.
	Run(ctx context.Context)

	// Close cancel the context created by the Run function and all the internal goroutines.
	Close()
}
