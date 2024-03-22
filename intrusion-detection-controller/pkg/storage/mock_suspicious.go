// Copyright 2019 Tigera Inc. All rights reserved.

package storage

import (
	"context"
	"time"

	geodb "github.com/projectcalico/calico/intrusion-detection-controller/pkg/feeds/geodb"
	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"

	apiV3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
)

type MockSuspicious struct {
	Error                error
	Events               []v1.Event
	LastSuccessfulSearch time.Time
	SetHash              string
}

func (m *MockSuspicious) QuerySet(ctx context.Context, geoDB geodb.GeoDatabase, feed *apiV3.GlobalThreatFeed) ([]v1.Event, time.Time, string, error) {
	return m.Events, m.LastSuccessfulSearch, m.SetHash, m.Error
}
