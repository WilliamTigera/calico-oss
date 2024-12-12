package client

import (
	"github.com/projectcalico/calico/queryserver/pkg/querycache/api"
	"github.com/projectcalico/calico/queryserver/queryserver/auth"
)

type QueryLabelsReq struct {
	api.ResourceType
	auth.Permission
}

type QueryLabelsResp struct {
	ResourceTypeLabelMap map[api.ResourceType][]LabelKeyValuePair `json:"resourceTypeLabelMap"`
	RBACWarnings         []string                                 `json:"RBACWarnings,omitempty"`
}

type LabelKeyValuePair struct {
	LabelKey    string   `json:"labelKey"`
	LabelValues []string `json:"labelValues"`
}
