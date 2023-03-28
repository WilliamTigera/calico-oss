// Copyright 2019 Tigera Inc. All rights reserved.

package elastic

import (
	"context"

	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/controller"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/feeds/cacher"
	"github.com/projectcalico/calico/intrusion-detection-controller/pkg/storage"
)

type ipSetData struct {
	ipSet storage.IPSet
}

func NewIPSetController(ipSet storage.IPSet) controller.Controller {
	return controller.NewController(ipSetData{ipSet}, cacher.ElasticSyncFailed)
}

func (d ipSetData) Put(ctx context.Context, name string, value interface{}) error {
	return d.ipSet.PutIPSet(ctx, name, value.(storage.IPSetSpec))
}

func (d ipSetData) List(ctx context.Context) ([]storage.Meta, error) {
	return d.ipSet.ListIPSets(ctx)
}

func (d ipSetData) Delete(ctx context.Context, m storage.Meta) error {
	return d.ipSet.DeleteIPSet(ctx, m)
}
