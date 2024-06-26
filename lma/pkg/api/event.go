package api

import (
	"context"

	"github.com/olivere/elastic/v7"
)

const (
	EventIndexWildCardPattern = "tigera_secure_ee_events.*"
	AutoBulkFlush             = 500
	PaginationSize            = 500
	EventTime                 = "time"
)

type EventBulkProcessor interface{}

type EventsSearchFields struct {
	Description     string  `json:"description,omitempty"`
	DestIP          *string `json:"dest_ip,omitempty"`
	DestName        string  `json:"dest_name,omitempty"`
	DestNameAggr    string  `json:"dest_name_aggr,omitempty"`
	DestNamespace   string  `json:"dest_namespace,omitempty"`
	DestPort        *int64  `json:"dest_port,omitempty"`
	Dismissed       bool    `json:"dismissed,omitempty"`
	Host            string  `json:"host,omitempty"`
	Origin          string  `json:"origin,omitempty"`
	Severity        int     `json:"severity,omitempty"`
	SourceIP        *string `json:"source_ip,omitempty"`
	SourceName      string  `json:"source_name,omitempty"`
	SourceNameAggr  string  `json:"source_name_aggr,omitempty"`
	SourceNamespace string  `json:"source_namespace,omitempty"`
	SourcePort      *int64  `json:"source_port,omitempty"`
	Time            int64   `json:"time,omitempty"`
	Type            string  `json:"type,omitempty"`
}

// TODO: Replace this with the one from Linseed
type EventsData struct {
	Description string `json:"description" validate:"required"`
	Origin      string `json:"origin" validate:"required"`
	Severity    int    `json:"severity" validate:"required"`
	Time        int64  `json:"time" validate:"required"`
	Type        string `json:"type" validate:"required"`

	DestIP          *string `json:"dest_ip,omitempty"`
	DestName        string  `json:"dest_name,omitempty"`
	DestNameAggr    string  `json:"dest_name_aggr,omitempty"`
	DestNamespace   string  `json:"dest_namespace,omitempty"`
	DestPort        *int64  `json:"dest_port,omitempty"`
	Dismissed       bool    `json:"dismissed,omitempty"`
	Host            string  `json:"host,omitempty"`
	SourceIP        *string `json:"source_ip,omitempty"`
	SourceName      string  `json:"source_name,omitempty"`
	SourceNameAggr  string  `json:"source_name_aggr,omitempty"`
	SourceNamespace string  `json:"source_namespace,omitempty"`
	SourcePort      *int64  `json:"source_port,omitempty"`

	Record interface{} `json:"record,omitempty"`
}

type EventResult struct {
	*EventsData
	ID  string
	Err error
}

type EventHandler interface {
	// CreateEventsIndex is called by every component writing into events index if index doesn't exist.
	CreateEventsIndex(ctx context.Context) error

	PutBulkSecurityEvent(data EventsData) error

	BulkProcessorInitialize(ctx context.Context, afterFn elastic.BulkAfterFunc) error
	BulkProcessorClose() error
}
