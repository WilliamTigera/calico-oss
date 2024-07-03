package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	lapi "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	"github.com/projectcalico/calico/linseed/pkg/client"
	lmav1 "github.com/projectcalico/calico/lma/pkg/apis/v1"
	"github.com/projectcalico/calico/lma/pkg/httputils"
	querycacheclient "github.com/projectcalico/calico/ts-queryserver/pkg/querycache/client"
	qsutils "github.com/projectcalico/calico/ts-queryserver/pkg/querycache/utils"
	queryserverclient "github.com/projectcalico/calico/ts-queryserver/queryserver/client"
)

type EndpointsAggregationRequest struct {
	// ClusterName defines the name of the cluster a connection will be performed on.
	ClusterName string `json:"cluster"`

	// QueryServer params, inlined
	querycacheclient.QueryEndpointsReqBody

	// Enable filtering endpoints in denied traffic
	ShowDeniedEndpoints bool `json:"showDeniedEndpoints,omitempty" validate:"omitempty"`

	// Time range
	TimeRange *lmav1.TimeRange `json:"time_range" validate:"omitempty"`

	// Timeout for the request. Defaults to 60s.
	Timeout *metav1.Duration `json:"timeout" validate:"omitempty"`
}

type EndpointsAggregationResponse struct {
	Count int                  `json:"count"`
	Item  []AggregatedEndpoint `json:"endpoints"`
}

type AggregatedEndpoint struct {
	querycacheclient.Endpoint
	HasDeniedTraffic bool `json:"hasDeniedTraffic"`
}

// EndpointsAggregationHandler is a handler for /endpoints/aggregation api
//
// returns a http handler function for getting list of endpoints (and filtering them by a set of parameters including:
// 1. network traffic (retrieved from flowlogs), and 2. static info (from endpoints, policies,
// nodes, and labels which are retrieved from queryserver cache))
//
// *note: this handler aggregates endpoints info with denied flowlogs - If timerange.from is provided from the client,
// linseed will return denied flowlogs from the provided time until now (linseed) time. timerange.to is set to Now()
// when linseed is processing the request and is different from when client is calling this api or when results are back
// to the client. So in very rare cases, results might be missing denied flowlog information if it happens in the very
// small timeframe between the call to linseed and response getting back to client. In this case, the api has to be called
// again (maybe just refreshing a page from user's point of view).
func EndpointsAggregationHandler(authreview AuthorizationReview, qsConfig *queryserverclient.QueryServerConfig,
	lsclient client.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// Validate http method.
		if r.Method != http.MethodPost {
			logrus.WithError(ErrInvalidMethod).Info("Invalid http method.")

			err := &httputils.HttpStatusError{
				Status: http.StatusMethodNotAllowed,
				Msg:    ErrInvalidMethod.Error(),
				Err:    ErrInvalidMethod,
			}

			httputils.EncodeError(w, err)
			return
		}

		// Parse request body.
		endpointsAggregationRequest, err := ParseBody[EndpointsAggregationRequest](w, r)
		if err != nil {
			logrus.WithError(err).Error("call to ParseBody failed.")
			httputils.EncodeError(w, err)
			return
		}

		// Validate parameters.
		err = validateEndpointsAggregationRequest(r, endpointsAggregationRequest)
		if err != nil {
			logrus.WithError(err).Error("call to validateEndpointsAggregationRequest failed.")
			httputils.EncodeError(w, err)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), endpointsAggregationRequest.Timeout.Duration)
		defer cancel()

		// filter deniedEndpoints based on flowlogs (via linseed)
		deniedEndpoints, err := getDeniedEndpointsFromLinseed(ctx, endpointsAggregationRequest, lsclient, authreview)
		if err != nil {
			logrus.WithError(err).Error("call to getDeniedEndpointsFromLinseed failed.")
			httputils.EncodeError(w, &httputils.HttpStatusError{
				Status: http.StatusInternalServerError,
				Msg:    "request to get deniedEndpoints from flowlogs has failed",
				Err:    errors.New("fetching deniedEndpoints from flowlogs has failed"),
			})
			return
		}

		// Filter deniedEndpoints by other parameters (via queryserver)
		qsEndpointsResp, err := getEndpointsFromQueryServer(qsConfig, endpointsAggregationRequest, deniedEndpoints)
		if err != nil {
			httputils.EncodeError(w, &httputils.HttpStatusError{
				Status: http.StatusInternalServerError,
				Msg:    "failed to get deniedEndpoints from queryserver",
				Err:    errors.New("failed to get deniedEndpoints from queryserver"),
			})
			return
		}

		// Enrich deniedEndpoints results with denied traffic info
		respBodyUpdated, err := updateResults(qsEndpointsResp, deniedEndpoints)
		if err != nil {
			httputils.EncodeError(w, &httputils.HttpStatusError{
				Status: http.StatusInternalServerError,
				Msg:    "failed to update deniedEndpoints with denied traffic info",
				Err:    errors.New("failed to update deniedEndpoints with denied traffic info"),
			})
			return
		}
		httputils.Encode(w, respBodyUpdated)
	})
}

// getEndpointsFromQueryServer is a handler for queryserver endpoint search
//
// returns QueryEndpointsResp: list of endpoints from queryserver based on the search parameters provided.
func getEndpointsFromQueryServer(qsConfig *queryserverclient.QueryServerConfig, params *EndpointsAggregationRequest,
	deniedEndpoints []string) (*querycacheclient.QueryEndpointsResp, error) {

	// build queryserver getEndpoints api params
	qsReqParams := getQueryServerRequestParams(params, deniedEndpoints)

	// Create queryserverClient client.
	queryserverClient, err := queryserverclient.NewQueryServerClient(qsConfig)
	if err != nil {
		logrus.WithError(err).Error("call to create NewQueryServerClient failed.")
		return nil, err
	}

	// call to queryserver client to get deniedEndpoints
	qsEndpointsResp, err := queryserverClient.SearchEndpoints(qsConfig, qsReqParams, params.ClusterName)
	if err != nil {
		logrus.WithError(err).Error("call to SearchEndpoints failed.")
		return nil, err

	}

	return qsEndpointsResp, nil
}

// updateResults enriches list of endpoints with denied traffic information
//
// return EndpointsAggregationResponse
func updateResults(endpointsResp *querycacheclient.QueryEndpointsResp,
	deniedEndpoints []string) (*EndpointsAggregationResponse, error) {
	epAggrList := []AggregatedEndpoint{}

	var epPatterns *regexp.Regexp

	if len(deniedEndpoints) > 0 {
		var err error
		epPatterns, err = qsutils.BuildSubstringRegexMatcher(deniedEndpoints)
		if err != nil {
			logrus.WithError(err).Error("call to BuildSubstringRegexMatcher failed.")
			return nil, err
		}
	}

	for _, item := range endpointsResp.Items {
		epAggregate := AggregatedEndpoint{
			Endpoint: item,
		}

		if epPatterns != nil {
			epKey := buildQueryServerEndpointKeyString(item.Namespace, item.Pod, "")

			if epPatterns.MatchString(epKey) {
				epAggregate.HasDeniedTraffic = true
			}
		}

		epAggrList = append(epAggrList, epAggregate)
	}

	epAggrResponse := EndpointsAggregationResponse{
		Count: endpointsResp.Count,
		Item:  epAggrList,
	}
	return &epAggrResponse, nil
}

// validateEndpointsAggregationRequest validates the request params for /endpoints/aggregation api
//
// return error if an unacceptable set of parameters are provided
func validateEndpointsAggregationRequest(r *http.Request, endpointReq *EndpointsAggregationRequest) error {
	// Set cluster name to default: "cluster", if empty.
	if endpointReq.ClusterName == "" {
		endpointReq.ClusterName = MaybeParseClusterNameFromRequest(r)
	}

	if endpointReq.ShowDeniedEndpoints {
		// validate queryserver params to not include endpoints list when ShowDeniedEndpoints is set to true
		if endpointReq.EndpointsList != nil {
			return &httputils.HttpStatusError{
				Status: http.StatusBadRequest,
				Msg:    "both ShowDeniedEndpoints and endpointList can not be provided in the same request",
				Err:    errors.New("invalid combination of parameters are provided: \"ShowDeniedEndpoints\" and / or \"endpointsList\""),
			}
		}
	}

	if endpointReq.Timeout == nil {
		endpointReq.Timeout = &metav1.Duration{Duration: DefaultRequestTimeout}
	}

	// validate time range and only allow "from"
	// We want to allow user to be able to select using only From the UI
	if endpointReq.TimeRange != nil {
		if endpointReq.TimeRange.To.IsZero() && !endpointReq.TimeRange.From.IsZero() {
			endpointReq.TimeRange.To = time.Now().UTC()
		} else if !endpointReq.TimeRange.To.IsZero() {
			return &httputils.HttpStatusError{
				Status: http.StatusBadRequest,
				Msg:    "time_range \"to\" should not be provided",
				Err:    errors.New("prohibited parameter is set: \"time_range\".\"to\""),
			}
		}
	}

	return nil
}

// buildFlowLogParamsForDeniedTrafficSearch prepares the parameters for flowlog search call to linseed to get denied flowlogs.
//
// returns FlowLogParams to be passed to linseed client, and an error.
func buildFlowLogParamsForDeniedTrafficSearch(ctx context.Context, authReview AuthorizationReview, params *EndpointsAggregationRequest,
	pageNumber, pageSize int) (*lapi.FlowLogParams, error) {
	fp := &lapi.FlowLogParams{}

	if params.TimeRange != nil {
		fp.SetTimeRange(params.TimeRange)
	}

	// set policy match to filter denied flowlogs
	action := lapi.FlowActionDeny
	fp.PolicyMatches = []lapi.PolicyMatch{
		{
			Action: &action,
		},
	}

	// Get the user's permissions. We'll pass these to Linseed to filter out logs that
	// the user doens't have permission to view.
	verbs, err := authReview.PerformReview(ctx, params.ClusterName)
	if err != nil {
		logrus.WithError(err).Error("call to authorization PerformReview failed.")
		return nil, err
	}
	fp.SetPermissions(verbs)

	// Configure pagination, timeout, etc.
	fp.SetTimeout(params.Timeout)

	fp.SetMaxPageSize(pageSize)
	if pageNumber != 0 {
		fp.SetAfterKey(map[string]interface{}{
			"startFrom": pageNumber * (fp.GetMaxPageSize()),
		})
	}

	return fp, nil
}

// getDeniedEndpointsFromLinseed extracts list of endpoints from denied flowlogs.
//
// It calls linseed.FlowLogs().List to fetch denied flowlogs.
// returns:
// 1. map[string][]string that includes extracted endpoints (src or dst) from denied flowlogs. Endpoint formatting is
// compatible with endpoint keys in datastore (used in queryserver). The map contains endpoints key patterns for each namespace.
//
//	example : {
//					"tigera-compliance": [".*tigera-compliance/.*-compliance--controller--6769fc95b4--gslxr"],
//					"calico-system":  [".*calico-system/.*-csi--node--driver--kz6wx"],
//					"tigera-elasticsearch": [".*tigera-elasticsearch/.*-tigera--linseed--558b55b7b8--n7ggg"]
//	          }
//
// 2. error
func getDeniedEndpointsFromLinseed(ctx context.Context, endpointsAggregationRequest *EndpointsAggregationRequest,
	lsclient client.Client, authreview AuthorizationReview) ([]string, error) {

	var endpoints []string
	pageNumber := 0
	pageSize := 1000
	var afterKey map[string]interface{}
	deniedFlowLogsParams, err := buildFlowLogParamsForDeniedTrafficSearch(ctx, authreview, endpointsAggregationRequest, pageNumber, pageSize)
	if err != nil {
		logrus.WithError(err).Error("call to buildFlowLogParamsForDeniedTrafficSearch failed.")
		return nil, &httputils.HttpStatusError{
			Status: http.StatusInternalServerError,
			Msg:    "error preparing flowlog search parameters",
			Err:    err,
		}
	}

	// iterate over all the page to get all flowlogs returned by flowlogs search
	for pageNumber == 0 || afterKey != nil {
		listFn := lsclient.FlowLogs(endpointsAggregationRequest.ClusterName).List

		items, err := listFn(ctx, deniedFlowLogsParams)
		if err != nil {
			logrus.WithError(err).Error("call to get flowLogs list from linseed failed.")
			return nil, &httputils.HttpStatusError{
				Status: http.StatusInternalServerError,
				Msg:    "error performing flowlog search",
				Err:    err,
			}
		}

		for _, item := range items.Items {
			if endpoints == nil {
				endpoints = []string{}
			}
			// Extract endpoints from flowlog item: both src and dst.
			sourcePattern := buildQueryServerEndpointKeyString(item.SourceNamespace, item.SourceName, item.SourceNameAggr)
			endpoints = append(endpoints, sourcePattern)

			destPattern := buildQueryServerEndpointKeyString(item.DestNamespace, item.DestName, item.DestNameAggr)
			endpoints = append(endpoints, destPattern)
		}
		pageNumber++

		afterKey = items.AfterKey
		deniedFlowLogsParams.SetAfterKey(items.AfterKey)
	}

	return endpoints, nil
}

// buildQueryServerEndpointKeyString is building endpoints key in the format expected by queryserver.
//
// Here is one example of an endpoint key in queryserver:
// WorkloadEndpoint(tigera-fluentd/afra--bz--vaxb--kadm--ms-k8s-fluentd--node--dfpzf-eth0)
// In this code, we create the following string that will be a match for the above endpoint:
//
//	.*tigera-fluentd/afra--bz--vaxb--kadm--ms-k8s-fluentd--node--*
func buildQueryServerEndpointKeyString(ns, name, nameaggr string) string {
	if name == "-" {
		return fmt.Sprintf(".*%s/.*-%s",
			ns,
			strings.Replace(nameaggr, "-", "--", -1))

	} else {
		return fmt.Sprintf(".*%s/.*-%s",
			ns,
			strings.Replace(name, "-", "--", -1))

	}
}

// getQueryServerRequestParams prepare the queryserver params
//
// return *querycacheclient.QueryEndpointsReq
func getQueryServerRequestParams(params *EndpointsAggregationRequest, deniedEndpoints []string) *querycacheclient.QueryEndpointsReqBody {
	// don't copy endpointsList initially from params.
	qsReqParams := querycacheclient.QueryEndpointsReqBody{
		Policy:              params.Policy,
		RuleDirection:       params.RuleDirection,
		RuleIndex:           params.RuleIndex,
		RuleEntity:          params.RuleEntity,
		RuleNegatedSelector: params.RuleNegatedSelector,
		Selector:            params.Selector,
		Endpoint:            params.Endpoint,
		Unprotected:         params.Unprotected,
		Node:                params.Node,
		Namespace:           params.Namespace,
		Unlabelled:          params.Unlabelled,
		Page:                params.Page,
		Sort:                params.Sort,
	}

	if params.ShowDeniedEndpoints {
		if deniedEndpoints == nil {
			qsReqParams.EndpointsList = []string{}
		} else {
			qsReqParams.EndpointsList = deniedEndpoints
		}
	}

	return &qsReqParams
}
