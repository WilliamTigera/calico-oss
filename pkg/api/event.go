package api

import (
	"context"
	"time"

	"github.com/olivere/elastic/v7"
)

const (
	EventIndexWildCardPattern = "tigera_secure_ee_events.*"
	AutoBulkFlush             = 500
	PaginationSize            = 500
	EventTime                 = "time"
)

type EventBulkProcessor interface {
}

type EventsSearchFields struct {
	Time            int64   `json:"time,omitempty"`
	Type            string  `json:"type,omitempty"`
	Description     string  `json:"description,omitempty"`
	Severity        int     `json:"severity,omitempty"`
	Origin          string  `json:"origin,omitempty"`
	SourceIP        *string `json:"source_ip,omitempty"`
	SourcePort      *int64  `json:"source_port,omitempty"`
	SourceNamespace string  `json:"source_namespace,omitempty"`
	SourceName      string  `json:"source_name,omitempty"`
	SourceNameAggr  string  `json:"source_name_aggr,omitempty"`
	DestIP          *string `json:"dest_ip,omitempty"`
	DestPort        *int64  `json:"dest_port,omitempty"`
	DestNamespace   string  `json:"dest_namespace,omitempty"`
	DestName        string  `json:"dest_name,omitempty"`
	DestNameAggr    string  `json:"dest_name_aggr,omitempty"`
	Host            string  `json:"host,omitempty"`
}

type EventsData struct {
	EventsSearchFields
	Record interface{} `json:"record,omitempty"`
}

type EventResult struct {
	*EventsData
	ID  string
	Err error
}

type EventHandler interface {
	// EventsIndexExists checks if index exists, all components sending data to elasticsearch should
	// check if Events index exists during startup.
	EventsIndexExists(ctx context.Context) (bool, error)

	// CreateEventsIndex is called by every component writing into events index if index doesn't exist.
	CreateEventsIndex(ctx context.Context) error

	PutSecurityEvent(ctx context.Context, data EventsData) (*elastic.IndexResponse, error)
	PutSecurityEventWithID(ctx context.Context, data EventsData, docId string) (*elastic.IndexResponse, error)
	PutBulkSecurityEvent(data EventsData) error

	SearchSecurityEvents(ctx context.Context, start, end *time.Time, filterData []EventsSearchFields, allClusters bool) <-chan *EventResult

	BulkProcessorInitialize(ctx context.Context, afterFn elastic.BulkAfterFunc) error
	BulkProcessorInitializeWithFlush(ctx context.Context, afterFn elastic.BulkAfterFunc, bulkActions int) error
	BulkProcessorFlush() error
	BulkProcessorClose() error
}
