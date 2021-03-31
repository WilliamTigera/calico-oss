// Copyright (c) 2019 Tigera, Inc. All rights reserved.
package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/tigera/compliance/pkg/datastore"
	lmaerror "github.com/tigera/lma/pkg/api"
	"github.com/tigera/lma/pkg/policyrec"
	"github.com/tigera/lma/pkg/rbac"

	k8srequest "k8s.io/apiserver/pkg/endpoints/request"
)

const (
	defaultTierName = "default"
)

// The response that we return
type PolicyRecommendationResponse struct {
	*policyrec.Recommendation
}

// PolicyRecommendationHandler returns a handler that writes a json response containing recommended policies.
func PolicyRecommendationHandler(k8sClientFactory datastore.ClusterCtxK8sClientFactory, k8sClient datastore.ClientSet, c policyrec.CompositeAggregator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Check that the request has the appropriate method. Should it be GET or POST?

		// Extract the recommendation parameters
		params, err := policyrec.ExtractPolicyRecommendationParamsFromRequest(req)
		if err != nil {
			createAndReturnError(err, "Error extracting policy recommendation parameters", http.StatusBadRequest, lmaerror.PolicyRec, w)
			return
		}

		clusterID := req.Header.Get(clusterIdHeader)
		authorizer, err := k8sClientFactory.RBACAuthorizerForCluster(clusterID)
		if err != nil {
			log.WithError(err).Error("failed to get authorizer from client factory")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		user, ok := k8srequest.UserFrom(req.Context())
		if !ok {
			log.WithError(err).Error("user not found in context")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		flowHelper := rbac.NewCachedFlowHelper(user, authorizer)

		// Check that user has sufficient permissions to list flows for the requested endpoint. This happens in the
		// selected cluster from the UI drop-down menu.
		if stat, err := ValidateRecommendationPermissions(flowHelper, params); err != nil {
			createAndReturnError(err, "Not permitting user actions", stat, lmaerror.PolicyRec, w)
			return
		}

		// Query elasticsearch with the parameters provided
		flows, err := policyrec.QueryElasticsearchFlows(req.Context(), c, params)
		if err != nil {
			createAndReturnError(err, "Error querying elasticsearch", http.StatusInternalServerError, lmaerror.PolicyRec, w)
			return
		}

		if len(flows) == 0 {
			log.WithField("params", params).Info("No matching flows found")
			errorStr := fmt.Sprintf("No matching flows found for endpoint name '%v' in namespace '%v' within the time range '%v:%v'", params.EndpointName, params.Namespace, params.StartTime, params.EndTime)
			err := fmt.Errorf(errorStr)
			createAndReturnError(err, errorStr, http.StatusNotFound, lmaerror.PolicyRec, w)
			return
		}

		policyName := policyrec.GeneratePolicyName(k8sClient, params)
		// Setup the recommendation engine. We specify the default tier as the flows that we are fetching
		// is at the end of the default tier. Similarly we set the recommended policy order to nil as well.
		// TODO(doublek): Tier and policy order should be obtained from the observation point policy.
		// Set order to 1000 so that the policy is in the middle of the tier and can be moved up or down as necessary.
		recommendedPolicyOrder := float64(1000)
		recEngine := policyrec.NewEndpointRecommendationEngine(params.EndpointName, params.Namespace, policyName, defaultTierName, &recommendedPolicyOrder)
		for _, flow := range flows {
			log.WithField("flow", flow).Debug("Calling recommendation engine with flow")
			err := recEngine.ProcessFlow(*flow)
			if err != nil {
				log.WithError(err).WithField("flow", flow).Debug("Error processing flow")
			}
		}
		recommendation, err := recEngine.Recommend()
		if err != nil {
			createAndReturnError(err, err.Error(), http.StatusInternalServerError, lmaerror.PolicyRec, w)
			return
		}
		response := &PolicyRecommendationResponse{
			Recommendation: recommendation,
		}
		log.WithField("recommendation", recommendation).Debug("Policy recommendation response")
		recJSON, err := json.Marshal(response)
		if err != nil {
			createAndReturnError(err, "Error marshalling recommendation to JSON", http.StatusInternalServerError, lmaerror.PolicyRec, w)
			return
		}
		_, err = w.Write(recJSON)
		if err != nil {
			errorStr := fmt.Sprintf("Error writing JSON recommendation: %v", recommendation)
			createAndReturnError(err, errorStr, http.StatusInternalServerError, lmaerror.PolicyRec, w)
			return
		}
	})
}

// ValidateRecommendationPermissions checks that the user is able to list flows for the specified endpoint.
func ValidateRecommendationPermissions(flowHelper rbac.FlowHelper, params *policyrec.PolicyRecommendationParams) (int, error) {
	if params.Namespace != "" {
		if ok, err := flowHelper.CanListPods(params.Namespace); err != nil {
			return http.StatusInternalServerError, err
		} else if !ok {
			return http.StatusForbidden, fmt.Errorf("user cannot list flows for pods in namespace %s", params.Namespace)
		}
	} else {
		if ok, err := flowHelper.CanListHostEndpoints(); err != nil {
			return http.StatusInternalServerError, err
		} else if !ok {
			return http.StatusForbidden, fmt.Errorf("user cannot list flows for hostendpoints")
		}
	}
	return http.StatusOK, nil
}
