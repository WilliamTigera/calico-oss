package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/olivere/elastic/v7"
	log "github.com/sirupsen/logrus"
	lmaelastic "github.com/tigera/lma/pkg/elastic"
)

const (
	sourceNamesAggName = "source_name_aggrs"
	destNamesAggName   = "dest_name_aggrs"
)

type FlowLogNamesParams struct {
	Limit         int32     `json:"limit"`
	Actions       []string  `json:"actions"`
	ClusterName   string    `json:"cluster"`
	Namespace     string    `json:"namespace"`
	Prefix        string    `json:"prefix"`
	Unprotected   bool      `json:"unprotected"`
	StartDateTime time.Time `json:"startDateTime"`
	EndDateTime   time.Time `json:"endDateTime"`
}

func FlowLogNamesHandler(esClient lmaelastic.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// validate request
		params, err := validateFlowLogNamesRequest(req)
		if err != nil {
			log.WithError(err).Info("Error validating request")
			switch err {
			case errInvalidMethod:
				http.Error(w, err.Error(), http.StatusMethodNotAllowed)
			case errParseRequest:
				http.Error(w, err.Error(), http.StatusBadRequest)
			case errInvalidAction:
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			}
			return
		}

		response, err := getNamesFromElastic(params, esClient)
		if err != nil {
			log.WithError(err).Info("Error getting names from elastic")
			http.Error(w, errGeneric.Error(), http.StatusInternalServerError)
		}

		// return array of strings with unique names
		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(response)
		if err != nil {
			log.WithError(err).Info("Encoding names array failed")
			http.Error(w, errGeneric.Error(), http.StatusInternalServerError)
			return
		}
	})
}

func validateFlowLogNamesRequest(req *http.Request) (*FlowLogNamesParams, error) {
	// Validate http method
	if req.Method != http.MethodGet {
		return nil, errInvalidMethod
	}

	// extract params from request
	url := req.URL.Query()
	limit, err := extractLimitParam(url)
	if err != nil {
		return nil, errParseRequest
	}
	actions := lowerCaseParams(url["actions"])
	cluster := strings.ToLower(url.Get("cluster"))
	prefix := strings.ToLower(url.Get("prefix"))
	namespace := strings.ToLower(url.Get("namespace"))
	unprotected := false
	if unprotectedValue := url.Get("unprotected"); unprotectedValue != "" {
		if unprotected, err = strconv.ParseBool(unprotectedValue); err != nil {
			return nil, errParseRequest
		}
	}
	startDateTime, err := parseAndValidateTime(url.Get("startDateTime"))
	if err != nil {
		log.WithError(err).Info("Error parsing startDateTime")
		return nil, errParseRequest
	}
	endDateTime, err := parseAndValidateTime(url.Get("endDateTime"))
	if err != nil {
		log.WithError(err).Info("Error parsing endDateTime")
		return nil, errParseRequest
	}
	params := &FlowLogNamesParams{
		Actions:       actions,
		Limit:         limit,
		ClusterName:   cluster,
		Prefix:        prefix,
		Namespace:     namespace,
		Unprotected:   unprotected,
		StartDateTime: startDateTime,
		EndDateTime:   endDateTime,
	}

	// Check whether the params are provided in the request and set default values if not
	if params.ClusterName == "" {
		params.ClusterName = "cluster"
	}
	if params.Prefix != "" {
		params.Prefix = fmt.Sprintf("%s.*", params.Prefix)
	}
	valid := validateActions(params.Actions)
	if !valid {
		return nil, errInvalidAction
	}

	valid = validateActionsAndUnprotected(params.Actions, params.Unprotected)
	if !valid {
		return nil, errInvalidActionUnprotected
	}

	return params, nil
}

func buildNamesQuery(params *FlowLogNamesParams) *elastic.BoolQuery {
	var termFilterValues []interface{}
	query := elastic.NewBoolQuery()
	nestedQuery := elastic.NewBoolQuery()
	if len(params.Actions) > 0 {
		for _, action := range params.Actions {
			termFilterValues = append(termFilterValues, action)
		}
		nestedQuery = nestedQuery.Filter(elastic.NewTermsQuery("action", termFilterValues...))
	}
	if params.Unprotected {
		query = query.Filter(UnprotectedQuery())
	}

	if !params.StartDateTime.IsZero() {
		startFilter := elastic.NewRangeQuery("start_time").Gt(params.StartDateTime.Unix())
		query = query.Filter(startFilter)
	}
	if !params.EndDateTime.IsZero() {
		endFilter := elastic.NewRangeQuery("end_time").Lt(params.EndDateTime.Unix())
		query = query.Filter(endFilter)
	}

	if params.Namespace != "" {
		nestedQuery = nestedQuery.
			Should(
				elastic.NewTermQuery("source_namespace", params.Namespace),
				elastic.NewTermQuery("dest_namespace", params.Namespace),
			).
			MinimumNumberShouldMatch(1)
	}
	query = query.Filter(nestedQuery)

	return query
}

func buildNameAggregations(params *FlowLogNamesParams) (*elastic.TermsAggregation, *elastic.TermsAggregation) {
	baseAgg := elastic.NewTermsAggregation().Size(int(params.Limit))
	if params.Prefix != "" {
		baseAgg = baseAgg.Include(params.Prefix)
	}

	sourceNameAggregation := *baseAgg.Field("source_name_aggr")
	destNameAggregation := *baseAgg.Field("dest_name_aggr")
	return &sourceNameAggregation, &destNameAggregation
}

func getNamesFromElastic(params *FlowLogNamesParams, esClient lmaelastic.Client) ([]string, error) {
	// form query
	query := buildNamesQuery(params)
	sourceNameAggregation, destNameAggregation := buildNameAggregations(params)
	index := getClusterFlowIndex(params.ClusterName)

	// perform Aggregated ES query
	searchQuery := esClient.Backend().Search().
		Index(index).
		Query(query).
		Size(0)
	searchQuery = searchQuery.Aggregation(sourceNamesAggName, sourceNameAggregation).Aggregation(destNamesAggName, destNameAggregation)
	searchResult, err := esClient.Do(context.Background(), searchQuery)

	if err != nil {
		return nil, err
	}

	if searchResult.Aggregations == nil {
		return []string{}, nil
	}

	sourceNameAggregationBuckets, sourceFound := searchResult.Aggregations.Terms(sourceNamesAggName)
	destNameAggregationBuckets, destFound := searchResult.Aggregations.Terms(destNamesAggName)

	if !sourceFound && !destFound {
		return []string{}, nil
	}

	buckets := make([]*elastic.AggregationBucketKeyItem, 0)
	if sourceFound {
		buckets = append(buckets, sourceNameAggregationBuckets.Buckets...)
	}

	if destFound {
		buckets = append(buckets, destNameAggregationBuckets.Buckets...)
	}

	uniqueNames := make(map[string]bool)
	names := make([]string, 0)

	for _, bucket := range buckets {
		namesInf := bucket.Key
		name := namesInf.(string)
		if _, exists := uniqueNames[name]; !exists {
			uniqueNames[name] = true
			names = append(names, name)
			if len(names) == int(params.Limit) {
				break
			}
		}
	}
	return names, nil
}
