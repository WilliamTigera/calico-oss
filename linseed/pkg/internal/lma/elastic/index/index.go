// Copyright (c) 2021 Tigera, Inc. All rights reserved.
package index

import (
	"time"

	"github.com/olivere/elastic/v7"

	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"

	apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
)

// Helper provides a set of functions to provide access to index-specific data. This hides
// the fact that the different index mappings are subtly different.
type Helper interface {
	// NewSelectorQuery creates an elasticsearch query from a selector string.
	NewSelectorQuery(selector string) (elastic.Query, error)

	// NewRBACQuery creates an elasticsearch query from an RBAC authorization matrix.
	NewRBACQuery(resources []apiv3.AuthorizedResourceVerbs) (elastic.Query, error)

	// NewTimeQuery creates an elasticsearch timerange query using the appropriate time field.
	NewTimeRangeQuery(from, to time.Time) elastic.Query

	// GetTimeField returns the time field used for the query.
	GetTimeField() string

	// BaseQuery returns the base query for the index, to which additional query clauses can be added.
	BaseQuery(i bapi.ClusterInfo) *elastic.BoolQuery
}
