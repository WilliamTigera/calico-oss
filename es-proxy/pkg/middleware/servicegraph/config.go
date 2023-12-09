// Copyright (c) 2021 Tigera, Inc. All rights reserved.
package servicegraph

import (
	"time"
)

type Config struct {
	// The maximum number of entries that we keep in the warm cache.
	ServiceGraphCacheMaxEntries int

	// The maximum number of buckets per query. If specified can be used to increase the bucket size if the volume of
	// data is too large to handle with multuiple smaller queries.
	ServiceGraphCacheMaxBucketsPerQuery int

	// The maximum number of aggregated results per document type. If this is exceeded then the user should reduce the
	// time window.
	ServiceGraphCacheMaxAggregatedRecords int

	// The time after which cached entries that would be polled in the background are removed after the last time they
	// were accessed. This ensures cached entries are not polled forever if they are not being accessed.
	ServiceGraphCachePolledEntryAgeOut time.Duration

	// The time after which cached entries that are still being populated for the first time are removed after the last
	// time they were accessed. This is shorter than the entry age out to ensure we don't wait for long queries when no
	// client is requesting the data.
	ServiceGraphCacheSlowQueryEntryAgeOut time.Duration

	// The poll loop interval. The time between background polling of all cache entries that require periodic updates.
	ServiceGraphCachePollLoopInterval time.Duration

	// The min time between starting successive background data queries. This is used to ensure we are sending too many
	// requests in quick succession and overwhelming linseed or the kubernetes API. This does not gate user driven
	// queries.
	ServiceGraphCachePollQueryInterval time.Duration

	// The max time we expect it to take for data to be collected and stored. This is used to determine
	// whether a cache entry should be background polled for updates.
	ServiceGraphCacheDataSettleTime time.Duration

	// Whether or not to prefetch raw data when the cache is initialized.
	ServiceGraphCacheDataPrefetch bool

	// TenantNamespace is the namespace of the tenant this instance is serving, or empty if this is a single-tenant cluster.
	TenantNamespace string
}
