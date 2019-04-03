// Copyright 2019 Tigera Inc. All rights reserved.

package puller

import (
	"context"

	"github.com/tigera/calico-k8sapiserver/pkg/apis/projectcalico/v3"

	"github.com/tigera/intrusion-detection/controller/pkg/statser"
)

type SyncFailFunction func()

type Puller interface {
	// Run activates the puller to start pulling from the feed.
	Run(context.Context, statser.Statser)

	// SetFeed updates the feed the puller should use.
	SetFeed(*v3.GlobalThreatFeed)

	// Close stops the puller and ends its goroutines
	Close()
}
