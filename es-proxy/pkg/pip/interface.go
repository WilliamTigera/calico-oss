package pip

import (
	"context"
	"time"

	pelastic "github.com/projectcalico/calico/lma/pkg/elastic"

	"github.com/projectcalico/calico/es-proxy/pkg/pip/policycalc"
	lapi "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	"github.com/projectcalico/calico/linseed/pkg/client"
)

type PIP interface {
	// This is the main entrypoint into PIP.
	GetFlows(ctx context.Context, params *PolicyImpactParams, flowFilter pelastic.FlowFilter) (*FlowLogResults, error)

	// The following public interface methods are here more for convenience than anything else. The PIPHandler
	// should just use GetCompositeAggrFlows().
	GetPolicyCalculator(ctx context.Context, r *PolicyImpactParams) (policycalc.PolicyCalculator, error)
	SearchAndProcessFlowLogs(
		ctx context.Context,
		pager client.ListPager[lapi.L3Flow],
		cluster string,
		calc policycalc.PolicyCalculator,
		limit int32,
		impactedOnly bool,
		flowFilter pelastic.FlowFilter,
	) (<-chan ProcessedFlows, <-chan error)
}

type PolicyImpactParams struct {
	FlowParams      *lapi.L3FlowParams `json:"-"`
	ResourceActions []ResourceChange   `json:"resourceActions"`
	FromTime        *time.Time         `json:"-"`
	ToTime          *time.Time         `json:"-"`
	ClusterName     string             `json:"-"`
	Limit           int32              `json:"-"`
	ImpactedOnly    bool               `json:"-"`
}
