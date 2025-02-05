// Copyright (c) 2021 Tigera, Inc. All rights reserved.
package index

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/olivere/elastic/v7"
	apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/calico/libcalico-go/lib/validator/v3/query"
	v1 "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	bapi "github.com/projectcalico/calico/linseed/pkg/backend/api"
	lmav1 "github.com/projectcalico/calico/lma/pkg/apis/v1"
	"github.com/projectcalico/calico/lma/pkg/httputils"
)

// SingleIndexFlowLogs returns an instance of the flow logs index helper that uses a single index.
func SingleIndexFlowLogs() Helper {
	return flowLogsIndexHelper{singleIndex: true}
}

// MultiIndexFlowLogs returns an instance of the flow logs multi-index helper.
func MultiIndexFlowLogs() Helper {
	return flowLogsIndexHelper{}
}

// flowLogsIndexHelper implements the Helper interface for flow logs.
type flowLogsIndexHelper struct {
	singleIndex bool
}

// NewFlowLogsConverter returns a Converter instance defined for flow logs.
func NewFlowLogsConverter() converter {
	return converter{flowLogsAtomToElastic}
}

// flowLogsAtomToElastic returns a flow log atom as an elastic JsonObject.
func flowLogsAtomToElastic(a *query.Atom) JsonObject {
	switch a.Key {
	case "dest_labels.labels", "policies.all_policies", "source_labels.labels":
		path := a.Key[:strings.Index(a.Key, ".")]
		return JsonObject{
			"nested": JsonObject{
				"path":  path,
				"query": basicAtomToElastic(a),
			},
		}
	default:
		return basicAtomToElastic(a)
	}
}

// Helper.

func (h flowLogsIndexHelper) BaseQuery(i bapi.ClusterInfo, params v1.Params) (*elastic.BoolQuery, error) {
	return defaultBaseQuery(i, h.singleIndex, params)
}

func (h flowLogsIndexHelper) NewSelectorQuery(selector string) (elastic.Query, error) {
	q, err := query.ParseQuery(selector)
	if err != nil {
		return nil, &httputils.HttpStatusError{
			Status: http.StatusBadRequest,
			Err:    err,
			Msg:    fmt.Sprintf("Invalid selector (%s) in request: %v", selector, err),
		}
	} else if err := query.Validate(q, query.IsValidFlowsAtom); err != nil {
		return nil, &httputils.HttpStatusError{
			Status: http.StatusBadRequest,
			Err:    err,
			Msg:    fmt.Sprintf("Invalid selector (%s) in request: %v", selector, err),
		}
	}
	converter := NewFlowLogsConverter()
	return JsonObjectElasticQuery(converter.Convert(q)), nil
}

func (h flowLogsIndexHelper) NewRBACQuery(resources []apiv3.AuthorizedResourceVerbs) (elastic.Query, error) {
	// Convert the permissions into a query that each flow must satisfy - essentially a source or
	// destination must be listable by the user to be included in the response.
	var should []elastic.Query
	for _, r := range resources {
		for _, v := range r.Verbs {
			if v.Verb != "list" {
				// Only interested in the list verbs.
				continue
			}
			for _, rg := range v.ResourceGroups {
				switch r.Resource {
				case "hostendpoints":
					// HostEndpoints are neither tiered nor namespaced, and AuthorizationReview does not
					// determine RBAC at the instance level, so must be able to list all HostEndpoints.
					should = append(should,
						elastic.NewTermQuery("source_type", "hep"),
						elastic.NewTermQuery("dest_type", "hep"),
					)
				case "networksets":
					if rg.Namespace == "" {
						// Can list all NetworkSets. Check type is "ns" and namespace is not "-" (which would
						//  be a GlobalNetworkSet).
						should = append(should,
							elastic.NewBoolQuery().Must(
								elastic.NewTermQuery("source_type", "ns"),
							).MustNot(
								elastic.NewTermQuery("source_namespace", "-"),
							),
							elastic.NewBoolQuery().Must(
								elastic.NewTermQuery("dest_type", "ns"),
							).MustNot(
								elastic.NewTermQuery("dest_namespace", "-"),
							),
						)
					} else {
						// Can list NetworkSets in a specific namespace. Check type is "ns" and namespace
						// matches.
						should = append(should,
							elastic.NewBoolQuery().Must(
								elastic.NewTermQuery("source_type", "ns"),
								elastic.NewTermQuery("source_namespace", rg.Namespace),
							),
							elastic.NewBoolQuery().Must(
								elastic.NewTermQuery("dest_type", "ns"),
								elastic.NewTermQuery("dest_namespace", rg.Namespace),
							),
						)
					}
				case "globalnetworksets":
					// GlobalNetworkSets are neither tiered nor namespaced, and AuthorizationReview does not
					// determine RBAC at the instance level, so must be able to list all GlobalNetworkSets.
					// Check type is "ns" and namespace is "-".
					should = append(should,
						elastic.NewBoolQuery().Must(
							elastic.NewTermQuery("source_type", "ns"),
							elastic.NewTermQuery("source_namespace", "-"),
						),
						elastic.NewBoolQuery().Must(
							elastic.NewTermQuery("dest_type", "ns"),
							elastic.NewTermQuery("dest_namespace", "-"),
						),
					)
				case "pods":
					if rg.Namespace == "" {
						// Can list all Pods. Check type is "wep".
						should = append(should,
							elastic.NewTermQuery("source_type", "wep"),
							elastic.NewTermQuery("dest_type", "wep"),
						)
					} else {
						// Can list Pods in a specific namespace. Check type is "wep" and namespace matches.
						should = append(should,
							elastic.NewBoolQuery().Must(
								elastic.NewTermQuery("source_type", "wep"),
								elastic.NewTermQuery("source_namespace", rg.Namespace),
							),
							elastic.NewBoolQuery().Must(
								elastic.NewTermQuery("dest_type", "wep"),
								elastic.NewTermQuery("dest_namespace", rg.Namespace),
							),
						)
					}
				}
			}
			break
		}
	}

	if len(should) == 0 {
		return nil, &httputils.HttpStatusError{
			Status: http.StatusForbidden,
			Msg:    "Forbidden",
			Err:    errors.New("user is not permitted to access any documents for this index"),
		}
	}

	return elastic.NewBoolQuery().Should(should...), nil
}

func (h flowLogsIndexHelper) NewTimeRangeQuery(r *lmav1.TimeRange) elastic.Query {
	timeField := GetTimeFieldForQuery(h, r)
	var from, to interface{}
	if timeField == "generated_time" {
		from = r.From
		to = r.To
	} else {
		from = strconv.FormatInt(r.From.Unix(), 10)
		to = strconv.FormatInt(r.To.Unix(), 10)
	}
	return elastic.NewRangeQuery(timeField).Gt(from).Lte(to)
}

func (h flowLogsIndexHelper) GetTimeField() string {
	return "end_time"
}
