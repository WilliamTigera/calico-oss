// Copyright 2021 Tigera Inc. All rights reserved.

package elastic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/olivere/elastic/v7"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	esClient "github.com/tigera/intrusion-detection/controller/pkg/elastic"
)

const (
	baseURI   = "http://127.0.0.1:9200"
	alertName = "sample-test"
)

var (
	searchWithScrollCounter int
)
var _ = Describe("GlobalAlert", func() {
	var (
		ecli *elastic.Client
		rt   *testRoundTripper
	)
	BeforeEach(func() {

		// set es client
		u, err := url.Parse(baseURI)
		Expect(err).ShouldNot(HaveOccurred())
		rt = newTestRoundTripper()
		client := &http.Client{
			Transport: http.RoundTripper(rt),
		}

		ecli, err = esClient.NewClient(client, u, "", "", false)
		Expect(err).ShouldNot(HaveOccurred())
	})

	Context("with count as metric and without any aggregation", func() {
		It("should query elasticsearch", func() {
			// Uses file with prefix 1_with_count_and_no_aggregation_* for testing this scenario
			ga := &v3.GlobalAlert{
				ObjectMeta: v1.ObjectMeta{
					Name: alertName,
				},
				Spec: v3.GlobalAlertSpec{
					Description: fmt.Sprintf("test alert: %s", alertName),
					Severity:    100,
					DataSet:     "flows",
					Metric:      "count",
					Threshold:   100,
					Condition:   "gt",
					Query:       "action=allow",
				},
			}

			e, err := GetTestElasticService(ecli, "test-cluster", ga)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e.eventIndexName).Should(Equal("tigera_secure_ee_events.test-cluster"))
			Expect(e.sourceIndexName).Should(Equal("tigera_secure_ee_flows.test-cluster.*"))

			e.globalAlert = ga
			e.executeQuery()
		})
	})

	Context("with min/max/avg/sum as metric and without aggregateBy", func() {
		It("should query Elasticsearch and insert doc into events index", func() {
			// Uses file with prefix 2_with_max_and_no_aggregateby_* for testing this scenario
			a := &v3.GlobalAlert{
				ObjectMeta: v1.ObjectMeta{
					Name: alertName,
				},
				Spec: v3.GlobalAlertSpec{
					Description: fmt.Sprintf("test alert: %s", alertName),
					Severity:    100,
					DataSet:     "dns",
					Metric:      "max",
					Threshold:   100,
					Condition:   "gt",
					Query:       "qtype=AAAA",
					Field:       "count",
				},
			}

			e, err := GetTestElasticService(ecli, "test-cluster", a)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e.eventIndexName).Should(Equal("tigera_secure_ee_events.test-cluster"))
			Expect(e.sourceIndexName).Should(Equal("tigera_secure_ee_dns.test-cluster.*"))

			e.globalAlert = a
			e.executeQuery()
		})
	})

	Context("with count as metric and with aggregateBy", func() {
		It("single aggregation - should query elasticsearch", func() {
			// Uses file with prefix 3_with_count_and_aggregateby_* for testing this scenario
			ga := &v3.GlobalAlert{
				ObjectMeta: v1.ObjectMeta{
					Name: alertName,
				},
				Spec: v3.GlobalAlertSpec{
					Summary:     "test alert summary ${source_namespace} ${count}",
					Severity:    100,
					DataSet:     "flows",
					Metric:      "count",
					AggregateBy: []string{"source_namespace"},
					Threshold:   100,
					Condition:   "gte",
					Query:       "action=allow",
				},
			}

			e, err := GetTestElasticService(ecli, "test-cluster", ga)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e.eventIndexName).Should(Equal("tigera_secure_ee_events.test-cluster"))
			Expect(e.sourceIndexName).Should(Equal("tigera_secure_ee_flows.test-cluster.*"))

			e.globalAlert = ga
			e.executeCompositeQuery()
			rt.reset()
			// Successive query to elasticsearch should be same as first query
			e.executeCompositeQuery()
			rt.reset()
			e.executeCompositeQuery()
			rt.reset()
		})
		It("multiple aggregation-should query elasticsearch", func() {
			// Uses file with prefix 3_1_with_count_and_aggregateby_* for testing this scenario
			ga := &v3.GlobalAlert{
				ObjectMeta: v1.ObjectMeta{
					Name: alertName,
				},
				Spec: v3.GlobalAlertSpec{
					Description: fmt.Sprintf("test alert: %s", alertName),
					Severity:    100,
					DataSet:     "flows",
					Metric:      "count",
					AggregateBy: []string{"source_namespace", "source_name_aggr"},
					Threshold:   100,
					Condition:   "not_eq",
					Query:       "action=allow",
				},
			}

			e, err := GetTestElasticService(ecli, "test-cluster", ga)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e.eventIndexName).Should(Equal("tigera_secure_ee_events.test-cluster"))
			Expect(e.sourceIndexName).Should(Equal("tigera_secure_ee_flows.test-cluster.*"))
			e.globalAlert = ga
			e.executeCompositeQuery()
		})
	})

	Context("with min/max/avg/sum as metric and with aggregateBy", func() {
		It("multiple aggregation-should query elasticsearch", func() {
			// Uses file with prefix 4_with_max_and_aggregateby_* for testing this scenario
			ga := &v3.GlobalAlert{
				ObjectMeta: v1.ObjectMeta{
					Name: alertName,
				},
				Spec: v3.GlobalAlertSpec{
					Description: "test alert description ${source_namespace}/${source_name_aggr} ${max}",
					Severity:    100,
					DataSet:     "flows",
					Metric:      "max",
					Field:       "num_flows",
					AggregateBy: []string{"source_namespace", "source_name_aggr"},
					Threshold:   100,
					Condition:   "gt",
					Query:       "action=allow",
				},
			}

			e, err := GetTestElasticService(ecli, "test-cluster", ga)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e.eventIndexName).Should(Equal("tigera_secure_ee_events.test-cluster"))
			Expect(e.sourceIndexName).Should(Equal("tigera_secure_ee_flows.test-cluster.*"))

			e.globalAlert = ga
			e.executeCompositeQuery()
		})
	})

	Context("without metric and without aggregateBy", func() {
		It("should query elasticsearch", func() {
			// Uses file with prefix 5_with_no_metric_and_no_aggregation* for testing this scenario
			searchWithScrollCounter = 0
			ga := &v3.GlobalAlert{
				ObjectMeta: v1.ObjectMeta{
					Name: alertName,
				},
				Spec: v3.GlobalAlertSpec{
					Description: fmt.Sprintf("test alert: %s", alertName),
					Severity:    100,
					DataSet:     "flows",
					Threshold:   100,
					Query:       "action=allow",
				},
			}

			e, err := GetTestElasticService(ecli, "test-cluster", ga)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e.eventIndexName).Should(Equal("tigera_secure_ee_events.test-cluster"))
			Expect(e.sourceIndexName).Should(Equal("tigera_secure_ee_flows.test-cluster.*"))

			// This makes 3 API calls and the expected request and response are validated in the rountripper
			// IDS calls /_search?scroll=5m&size=500 end point with scroll set
			// resulting hits are transformed to docs that needs to go in events index, a /bulk request is made with transformed data
			// IDS again calls /_search?scroll=5m&size=500 to get next batch of documents
			e.globalAlert = ga
			e.executeQueryWithScroll()
		})
	})

	Context("without metric and with aggregateBy", func() {
		It("should query elasticsearch", func() {
			// Uses file with prefix 6_without_metric_and_with_aggregateby_* for testing this scenario
			ga := &v3.GlobalAlert{
				ObjectMeta: v1.ObjectMeta{
					Name: alertName,
				},
				Spec: v3.GlobalAlertSpec{
					Description: fmt.Sprintf("test alert: %s", alertName),
					Severity:    100,
					DataSet:     "flows",
					Query:       "action=allow",
					AggregateBy: []string{"source_name_aggr"},
				},
			}

			e, err := GetTestElasticService(ecli, "test-cluster", ga)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e.eventIndexName).Should(Equal("tigera_secure_ee_events.test-cluster"))
			Expect(e.sourceIndexName).Should(Equal("tigera_secure_ee_flows.test-cluster.*"))

			e.globalAlert = ga
			e.executeCompositeQuery()
		})
	})

	Context("query with set", func() {
		It("Operator IN with count and without aggregation", func() {
			// Uses file with prefix 7_with_in_and_count_and_no_aggregation_* for testing this scenario
			ga := &v3.GlobalAlert{
				ObjectMeta: v1.ObjectMeta{
					Name: alertName,
				},
				Spec: v3.GlobalAlertSpec{
					Description: fmt.Sprintf("test alert: %s", alertName),
					Severity:    100,
					DataSet:     "flows",
					Metric:      "count",
					Threshold:   3,
					Condition:   "gt",
					Query:       `process_name IN {"*voltron", "?es-proxy"}`,
				},
			}

			e, err := GetTestElasticService(ecli, "test-cluster", ga)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e.eventIndexName).Should(Equal("tigera_secure_ee_events.test-cluster"))
			Expect(e.sourceIndexName).Should(Equal("tigera_secure_ee_flows.test-cluster.*"))

			e.globalAlert = ga
			e.executeQuery()
		})
		It("Operator NOTIN with count and without aggregation", func() {
			// Uses file with prefix 7_with_notin_and_count_and_no_aggregation_* for testing this scenario
			ga := &v3.GlobalAlert{
				ObjectMeta: v1.ObjectMeta{
					Name: alertName,
				},
				Spec: v3.GlobalAlertSpec{
					Description: fmt.Sprintf("test alert: %s", alertName),
					Severity:    100,
					DataSet:     "flows",
					Metric:      "count",
					Threshold:   3,
					Condition:   "gt",
					Query:       `process_name NOTIN {"*voltron", "?es-proxy"}`,
				},
			}

			e, err := GetTestElasticService(ecli, "test-cluster", ga)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e.eventIndexName).Should(Equal("tigera_secure_ee_events.test-cluster"))
			Expect(e.sourceIndexName).Should(Equal("tigera_secure_ee_flows.test-cluster.*"))

			e.globalAlert = ga
			e.executeQuery()
		})
		It("Operator IN with count and with aggregation", func() {
			// Uses file with prefix 8_with_in_and_count_and_aggregateby_* for testing this scenario
			ga := &v3.GlobalAlert{
				ObjectMeta: v1.ObjectMeta{
					Name: alertName,
				},
				Spec: v3.GlobalAlertSpec{
					Description: fmt.Sprintf("test alert: %s", alertName),
					Severity:    100,
					DataSet:     "flows",
					Metric:      "count",
					Condition:   "gt",
					Threshold:   3,
					Query:       `process_name IN {"*voltron", "?es-proxy"}`,
					AggregateBy: []string{"source_namespace", "source_name_aggr"},
				},
			}

			e, err := GetTestElasticService(ecli, "test-cluster", ga)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e.eventIndexName).Should(Equal("tigera_secure_ee_events.test-cluster"))
			Expect(e.sourceIndexName).Should(Equal("tigera_secure_ee_flows.test-cluster.*"))

			e.globalAlert = ga
			e.executeQuery()
		})
		It("Operator NOTIN with count and with aggregation", func() {
			// Uses file with prefix 8_with_notin_and_count_and_aggregateby_* for testing this scenario
			ga := &v3.GlobalAlert{
				ObjectMeta: v1.ObjectMeta{
					Name: alertName,
				},
				Spec: v3.GlobalAlertSpec{
					Description: fmt.Sprintf("test alert: %s", alertName),
					Severity:    100,
					DataSet:     "flows",
					Metric:      "count",
					Condition:   "gt",
					Threshold:   3,
					Query:       `process_name NOTIN {"*voltron", "?es-proxy"}`,
					AggregateBy: []string{"source_namespace", "source_name_aggr"},
				},
			}

			e, err := GetTestElasticService(ecli, "test-cluster", ga)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e.eventIndexName).Should(Equal("tigera_secure_ee_events.test-cluster"))
			Expect(e.sourceIndexName).Should(Equal("tigera_secure_ee_flows.test-cluster.*"))

			e.globalAlert = ga
			e.executeQuery()
		})
		It("Operator IN without metric and without aggregation", func() {
			// Uses file with prefix 9_with_in_without_metric_and_no_aggregation_* for testing this scenario
			ga := &v3.GlobalAlert{
				ObjectMeta: v1.ObjectMeta{
					Name: alertName,
				},
				Spec: v3.GlobalAlertSpec{
					Description: fmt.Sprintf("test alert: %s", alertName),
					Severity:    100,
					DataSet:     "flows",
					Query:       `process_name IN {"*voltron", "?es-proxy"}`,
				},
			}

			e, err := GetTestElasticService(ecli, "test-cluster", ga)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e.eventIndexName).Should(Equal("tigera_secure_ee_events.test-cluster"))
			Expect(e.sourceIndexName).Should(Equal("tigera_secure_ee_flows.test-cluster.*"))

			e.globalAlert = ga
			e.executeQuery()
		})
		It("Operator NOTIN without metric and without aggregation", func() {
			// Uses file with prefix 9_with_notin_without_metric_and_no_aggregation_* for testing this scenario
			ga := &v3.GlobalAlert{
				ObjectMeta: v1.ObjectMeta{
					Name: alertName,
				},
				Spec: v3.GlobalAlertSpec{
					Description: fmt.Sprintf("test alert: %s", alertName),
					Severity:    100,
					DataSet:     "flows",
					Query:       `process_name NOTIN {"*voltron", "?es-proxy"}`,
				},
			}

			e, err := GetTestElasticService(ecli, "test-cluster", ga)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(e.eventIndexName).Should(Equal("tigera_secure_ee_events.test-cluster"))
			Expect(e.sourceIndexName).Should(Equal("tigera_secure_ee_flows.test-cluster.*"))

			e.globalAlert = ga
			e.executeQuery()
		})
	})

	Context("on error", func() {
		It("should store only recent errors", func() {
			var errs []v3.ErrorCondition
			for i := 0; i < 12; i++ {
				errs = appendError(errs, v3.ErrorCondition{Message: fmt.Sprintf("Error %v", i)})
			}
			Expect(len(errs)).Should(Equal(10))
			Expect(errs[MaxErrorsSize-1].Message).Should(Equal("Error 11"))
			Expect(errs[0].Message).Should(Equal("Error 2"))
		})
	})

})

func GetTestElasticService(esCLI *elastic.Client, clusterName string, alert *v3.GlobalAlert) (*service, error) {
	e := &service{
		esCLI:       esCLI,
		clusterName: clusterName,
	}
	e.buildIndexName(alert)

	err := e.buildEsQuery(alert)
	if err != nil {
		return nil, err
	}

	e.esBulkProcessor, err = e.esCLI.BulkProcessor().
		BulkActions(AutoBulkFlush).
		Do(context.Background())
	if err != nil {
		Expect(err).ShouldNot(HaveOccurred())
		return nil, err
	}

	return e, nil
}

type elasticQuery struct {
	Aggs  map[string]interface{} `json:"aggs,omitempty"`
	Query struct {
		Bool struct {
			Filter map[string]interface{} `json:"filter,omitempty"`
			Must   map[string]interface{} `json:"must"`
		} `json:"bool"`
	} `json:"query"`
	Size int
}

type testRoundTripper struct {
	e                   error
	isStartOfAlertCycle bool
}

func newTestRoundTripper() *testRoundTripper {
	return &testRoundTripper{isStartOfAlertCycle: true}
}

func (t *testRoundTripper) reset() {
	t.isStartOfAlertCycle = true
}

func (t *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.e != nil {
		return nil, t.e
	}
	switch req.Method {
	case "HEAD":
		switch req.URL.String() {
		case baseURI:
			return &http.Response{
				StatusCode: 200,
				Request:    req,
				Body:       ioutil.NopCloser(strings.NewReader("")),
			}, nil
		}

	case "POST":
		originalReqBody, err := ioutil.ReadAll(req.Body)
		Expect(err).ShouldNot(HaveOccurred())

		reqBody := alterRequestBodyForComparison(originalReqBody)
		switch req.URL.String() {
		case baseURI + "/tigera_secure_ee_flows.test-cluster.%2A/_search":
			switch string(reqBody) {
			case mustGetQueryAsString("test_files/1_with_count_and_no_aggregation_query.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/1_with_count_and_no_aggregation_response.json"),
				}, nil
			case mustGetQueryAsString("test_files/3_with_count_and_aggregateby_query.json"):
				// First call made to elasticsearch should be with 3_with_count_and_aggregateby_query.json
				Expect(t.isStartOfAlertCycle).Should(BeTrue())
				t.isStartOfAlertCycle = false
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/3_with_count_and_aggregateby_response.json"),
				}, nil
			case mustGetQueryAsString("test_files/3_with_count_and_aggregateby_query_after_key.json"):
				// Second call made to elasticsearch should be with 3_with_count_and_aggregateby_query_after_key.json
				Expect(t.isStartOfAlertCycle).Should(BeFalse())
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/3_with_count_and_aggregateby_response_after_key.json"),
				}, nil
			case mustGetQueryAsString("test_files/3_1_with_count_and_aggregateby_query.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/3_1_with_count_and_aggregateby_response.json"),
				}, nil
			case mustGetQueryAsString("test_files/4_with_max_and_aggregateby_query.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/4_with_max_and_aggregateby_response.json"),
				}, nil
			case mustGetQueryAsString("test_files/6_without_metric_and_with_aggregateby_query.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/6_without_metric_and_with_aggregateby_response.json"),
				}, nil
			case mustGetQueryAsString("test_files/7_with_in_and_count_and_no_aggregation_query.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/7_with_in_and_count_and_no_aggregation_response.json"),
				}, nil
			case mustGetQueryAsString("test_files/7_with_notin_and_count_and_no_aggregation_query.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/7_with_notin_and_count_and_no_aggregation_response.json"),
				}, nil
			case mustGetQueryAsString("test_files/8_with_in_and_count_and_aggregateby_query.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/8_with_in_and_count_and_aggregateby_response.json"),
				}, nil
			case mustGetQueryAsString("test_files/8_with_notin_and_count_and_aggregateby_query.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/8_with_notin_and_count_and_aggregateby_response.json"),
				}, nil
			case mustGetQueryAsString("test_files/9_with_in_without_metric_and_no_aggregation_query.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/9_with_in_without_metric_and_no_aggregation_response.json"),
				}, nil
			case mustGetQueryAsString("test_files/9_with_notin_without_metric_and_no_aggregation_query.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/9_with_notin_without_metric_and_no_aggregation_response.json"),
				}, nil
			default:
				Fail(fmt.Sprintf("Unexpected/malformed Elasticsearch query :%s", reqBody))
			}
		case baseURI + "/tigera_secure_ee_dns.test-cluster.%2A/_search":
			switch string(reqBody) {
			case mustGetQueryAsString("test_files/2_with_max_and_no_aggregateby_query.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/2_with_max_and_no_aggregateby_response.json"),
				}, nil
			default:
				Fail(fmt.Sprintf("Unexpected/malformed Elasticsearch query :%s", reqBody))
			}
		case baseURI + "/tigera_secure_ee_flows.test-cluster.%2A/_search?scroll=5m&size=500":
			if searchWithScrollCounter == 1 {
				// return EOF
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body: ioutil.NopCloser(strings.NewReader(`{
					"hits": {
						"total": {
							"value": 1,
							"relation": "eq"
						},
						"hits": []}}`)),
				}, nil
			}
			switch string(reqBody) {
			case mustGetQueryAsString("test_files/5_with_no_metric_and_no_aggregation_query.json"):
				searchWithScrollCounter++
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/5_with_no_metric_and_no_aggregation_response.json"),
				}, nil
			default:
				Fail(fmt.Sprintf("Unexpected/malformed Elasticsearch query :%s", reqBody))
			}
		case baseURI + "/_bulk":
			reqBody = alterBulkRequestBodyForComparison(originalReqBody)
			switch string(reqBody) {
			case mustGetEventIndexDocAsString("test_files/1_with_count_and_no_aggregation_events_doc.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/bulk_response.json"),
				}, nil
			case mustGetEventIndexDocAsString("test_files/2_with_max_and_no_aggregateby_events_doc.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/bulk_response.json"),
				}, nil
			case mustGetEventIndexDocAsString("test_files/3_with_count_and_aggregateby_events_doc.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/bulk_response.json"),
				}, nil
			case mustGetEventIndexDocAsString("test_files/3_1_with_count_and_aggregateby_events_doc.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/bulk_response.json"),
				}, nil
			case mustGetEventIndexDocAsString("test_files/4_with_max_and_aggregateby_events_doc.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/bulk_response.json"),
				}, nil
			case mustGetEventIndexDocAsString("test_files/5_with_no_metric_and_no_aggregation_events_doc.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/bulk_response.json"),
				}, nil
			case mustGetEventIndexDocAsString("test_files/6_without_metric_and_with_aggregateby_events_doc.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/bulk_response.json"),
				}, nil
			case mustGetEventIndexDocAsString("test_files/7_with_in_and_count_and_no_aggregation_events_doc.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/bulk_response.json"),
				}, nil
			case mustGetEventIndexDocAsString("test_files/7_with_notin_and_count_and_no_aggregation_events_doc.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/bulk_response.json"),
				}, nil
			case mustGetEventIndexDocAsString("test_files/8_with_in_and_count_and_aggregatedby_events_doc.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/bulk_response.json"),
				}, nil
			case mustGetEventIndexDocAsString("test_files/8_with_notin_and_count_and_aggregatedby_events_doc.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/bulk_response.json"),
				}, nil
			case mustGetEventIndexDocAsString("test_files/9_with_in_without_metric_and_no_aggregation_events_doc.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/bulk_response.json"),
				}, nil
			case mustGetEventIndexDocAsString("test_files/9_with_notin_without_metric_and_no_aggregation_events_doc.json"):
				return &http.Response{
					StatusCode: 200,
					Request:    req,
					Body:       mustOpen("test_files/bulk_response.json"),
				}, nil
			default:
				Fail(fmt.Sprintf("Unexpected/malformed data sent to Elasticsearch events index: %s", reqBody))
			}
		default:
			Fail(fmt.Sprintf("Unexpected query URI :%s", req.URL.String()))
		}
	}

	if os.Getenv("ELASTIC_TEST_DEBUG") == "yes" {
		_, _ = fmt.Fprintf(os.Stderr, "%s %s\n", req.Method, req.URL)
		if req.Body != nil {
			b, _ := ioutil.ReadAll(req.Body)
			_ = req.Body.Close()
			body := string(b)
			req.Body = ioutil.NopCloser(bytes.NewReader(b))
			_, _ = fmt.Fprintln(os.Stderr, body)
		}
	}

	return &http.Response{
		Request:    req,
		StatusCode: 500,
		Body:       ioutil.NopCloser(strings.NewReader("")),
	}, nil
}

// alterBulkRequestBodyForComparison returns byte array of the request.Body with time field set to 0,
// so the actual request.Body can be compared with expected request.Body
func alterBulkRequestBodyForComparison(reqBody []byte) []byte {
	var actualBody []interface{}
	decoder := json.NewDecoder(strings.NewReader(string(reqBody)))
	for {
		var doc interface{}
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		Expect(err).ShouldNot(HaveOccurred())
		if jdoc, ok := doc.(map[string]interface{}); ok {
			if _, exists := jdoc["time"]; exists {
				jdoc["time"] = 0
			}
			actualBody = append(actualBody, jdoc)
		} else {
			actualBody = append(actualBody, doc)
		}
	}
	out, err := json.Marshal(actualBody)
	Expect(err).ShouldNot(HaveOccurred())
	fmt.Printf("\n bulk: %s", string(out))
	return out
}

// alterRequestBodyForComparison returns byte array of the request.Body with time range set to nil,
// so the actual request.Body can be compared with expected request.Body
func alterRequestBodyForComparison(req []byte) []byte {
	var q elasticQuery
	reader := bytes.NewBuffer(req)
	decoder := json.NewDecoder(reader)
	err := decoder.Decode(&q)
	Expect(err).ShouldNot(HaveOccurred())
	out, err := json.Marshal(q)
	Expect(err).ShouldNot(HaveOccurred())
	return out
}

func mustOpen(name string) io.ReadCloser {
	f, err := os.Open(name)
	if err != nil {
		panic(err)
	}
	return f
}

func mustGetQueryAsString(name string) string {
	f, err := os.Open(name)
	if err != nil {
		Expect(err).ShouldNot(HaveOccurred())
	}
	b, err := ioutil.ReadAll(f)
	if err != nil {
		Expect(err).ShouldNot(HaveOccurred())
	}
	err = f.Close()
	if err != nil {
		Expect(err).ShouldNot(HaveOccurred())
	}
	var q elasticQuery
	err = json.Unmarshal(b, &q)
	Expect(err).ShouldNot(HaveOccurred())
	// alter time range for comparison
	Expect(q.Query.Bool.Filter).NotTo(BeNil())
	q.Query.Bool.Filter["range"] = map[string]interface{}{
		"start_time": map[string]string{
			"gte": fmt.Sprintf("now-%ds", int64(DefaultLookback.Seconds())),
			"lte": "now",
		},
	}
	out, err := json.Marshal(q)
	Expect(err).ShouldNot(HaveOccurred())
	return string(out)
}

func mustGetEventIndexDocAsString(name string) string {
	f, err := os.Open(name)
	if err != nil {
		Expect(err).ShouldNot(HaveOccurred())
	}
	b, err := ioutil.ReadAll(f)
	if err != nil {
		Expect(err).ShouldNot(HaveOccurred())
	}
	err = f.Close()
	if err != nil {
		Expect(err).ShouldNot(HaveOccurred())
	}

	var actualBody []interface{}
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	for {
		var doc interface{}
		err := decoder.Decode(&doc)
		if err == io.EOF {
			// all done
			break
		}
		Expect(err).ShouldNot(HaveOccurred())
		if jdoc, ok := doc.(map[string]interface{}); ok {
			if _, exists := jdoc["time"]; exists {
				jdoc["time"] = 0
			}
			actualBody = append(actualBody, jdoc)
		} else {
			actualBody = append(actualBody, doc)
		}
	}
	out, err := json.Marshal(actualBody)
	Expect(err).ShouldNot(HaveOccurred())
	return string(out)
}
