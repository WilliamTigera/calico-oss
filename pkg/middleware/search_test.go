// Copyright (c) 2021 Tigera, Inc. All rights reserved.
package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"

	"github.com/olivere/elastic/v7"

	lmav1 "github.com/tigera/lma/pkg/apis/v1"
	lmaindex "github.com/tigera/lma/pkg/elastic/index"
	"github.com/tigera/lma/pkg/httputils"
	calicojson "github.com/tigera/lma/pkg/test/json"
	"github.com/tigera/lma/pkg/test/thirdpartymock"

	libcalicov3 "github.com/projectcalico/libcalico-go/lib/apis/v3"

	v1 "github.com/tigera/es-proxy/pkg/apis/v1"
)

const (
	validRequestBody = `
{
  "cluster": "c_val",
  "page_size": 152,
  "page_num": 1,
	"time_range": {
		"from": "2021-04-19T14:25:30.169821857-07:00",
		"to": "2021-04-19T14:25:30.169827009-07:00"
	}
}`
	validRequestBodyPageSizeGreaterThanLTE = `
{
  "cluster": "c_val",
  "page_size": 1001,
  "page_num": 1,
	"time_range": {
		"from": "2021-04-19T14:25:30.169821857-07:00",
		"to": "2021-04-19T14:25:30.169827009-07:00"
	}
}`
	validRequestBodyPageSizeLessThanGTE = `
{
  "cluster": "c_val",
  "page_size": -1,
  "page_num": 1,
	"time_range": {
		"from": "2021-04-19T14:25:30.169821857-07:00",
		"to": "2021-04-19T14:25:30.169827009-07:00"
	}
}`
	invalidRequestBodyBadlyFormedStringValue = `
{
  "cluster": c_val,
  "page_size": 152,
  "page_num": 1,
	"time_range": {
		"from": "2021-04-19T14:25:30.169821857-07:00",
		"to": "2021-04-19T14:25:30.169827009-07:00"
	}
}`

	invalidRequestBodyTimeRangeMissing = `
{
  "cluster": "c_val",
  "page_size": 152,
  "page_num": 1
}`

	invalidRequestBodyTimeRangeContainsInvalidTimeValue = `
{
  "cluster": "c_val",
  "page_size": 152,
  "page_num": 1,
	"time_range": {
		"from": 143435,
		"to": "2021-04-19T14:25:30.169827009-07:00"
	}
}`
)

// The user authentication review mock struct implementing the authentication review interface.
type userAuthorizationReviewMock struct {
	verbs []libcalicov3.AuthorizedResourceVerbs
	err   error
}

// PerformReviewForElasticLogs wraps a mocked version of the authorization review method
// PerformReviewForElasticLogs.
func (a userAuthorizationReviewMock) PerformReviewForElasticLogs(
	ctx context.Context, req *http.Request, cluster string,
) ([]libcalicov3.AuthorizedResourceVerbs, error) {
	return a.verbs, a.err
}

var _ = Describe("SearchElasticHits", func() {
	var (
		mockDoer *thirdpartymock.MockDoer
	)

	type SomeLog struct {
		Timestamp time.Time `json:"@timestamp"`
		StartTime time.Time `json:"start_time"`
		EndTime   time.Time `json:"end_time"`
		Action    string    `json:"action"`
		BytesIn   *uint64   `json:"bytes_in"`
		BytesOut  *uint64   `json:"bytes_out"`
	}

	BeforeEach(func() {
		mockDoer = new(thirdpartymock.MockDoer)
	})

	AfterEach(func() {
		mockDoer.AssertExpectations(GinkgoT())
	})

	Context("Elasticsearch /search request and response validation", func() {
		fromTime := time.Date(2021, 04, 19, 14, 25, 30, 169827009, time.Local)
		toTime := time.Date(2021, 04, 19, 15, 25, 30, 169827009, time.Local)

		esResponse := []*elastic.SearchHit{
			{
				Index: "tigera_secure_ee_flows",
				Type:  "_doc",
				Id:    "2021-04-19 14:25:30.169827011 -0700 PDT m=+0.121726716",
				Source: calicojson.MustMarshal(calicojson.Map{
					"@timestamp": "2021-04-19T14:25:30.169827011-07:00",
					"start_time": "2021-04-19T14:25:30.169821857-07:00",
					"end_time":   "2021-04-19T14:25:30.169827009-07:00",
					"action":     "action1",
					"bytes_in":   uint64(5456),
					"bytes_out":  uint64(48245),
				}),
			},
			{
				Index: "tigera_secure_ee_flows",
				Type:  "_doc",
				Id:    "2021-04-19 14:25:30.169827010 -0700 PDT m=+0.121726716",
				Source: calicojson.MustMarshal(calicojson.Map{
					"@timestamp": "2021-04-19T15:25:30.169827010-07:00",
					"start_time": "2021-04-19T15:25:30.169821857-07:00",
					"end_time":   "2021-04-19T15:25:30.169827009-07:00",
					"action":     "action2",
					"bytes_in":   uint64(3436),
					"bytes_out":  uint64(68547),
				}),
			},
		}

		t1, _ := time.Parse(time.RFC3339, "2021-04-19T14:25:30.169827011-07:00")
		st1, _ := time.Parse(time.RFC3339, "2021-04-19T14:25:30.169821857-07:00")
		et1, _ := time.Parse(time.RFC3339, "2021-04-19T14:25:30.169827009-07:00")
		bytesIn1 := uint64(5456)
		bytesOut1 := uint64(48245)
		t2, _ := time.Parse(time.RFC3339, "2021-04-19T15:25:30.169827010-07:00")
		st2, _ := time.Parse(time.RFC3339, "2021-04-19T15:25:30.169821857-07:00")
		et2, _ := time.Parse(time.RFC3339, "2021-04-19T15:25:30.169827009-07:00")
		bytesIn2 := uint64(3436)
		bytesOut2 := uint64(68547)
		expectedJSONResponse := []*SomeLog{
			{
				Timestamp: t1,
				StartTime: st1,
				EndTime:   et1,
				Action:    "action1",
				BytesIn:   &bytesIn1,
				BytesOut:  &bytesOut1,
			},
			{
				Timestamp: t2,
				StartTime: st2,
				EndTime:   et2,
				Action:    "action2",
				BytesIn:   &bytesIn2,
				BytesOut:  &bytesOut2,
			},
		}

		userAuthReview := userAuthorizationReviewMock{verbs: []libcalicov3.AuthorizedResourceVerbs{
			{
				APIGroup: "APIGroupVal1",
				Resource: "hostendpoints",
				Verbs: []libcalicov3.AuthorizedResourceVerb{
					{
						Verb: "list",
						ResourceGroups: []libcalicov3.AuthorizedResourceGroup{
							{
								Tier:      "tierVal1",
								Namespace: "namespaceVal1",
							},
							{
								Tier:      "tierVal2",
								Namespace: "namespaceVal2",
							},
						},
					},
					{
						Verb: "list",
						ResourceGroups: []libcalicov3.AuthorizedResourceGroup{
							{
								Tier:      "tierVal1",
								Namespace: "namespaceVal1",
							},
							{
								Tier:      "tierVal2",
								Namespace: "namespaceVal2",
							},
						},
					},
				},
			},
		},
			err: nil,
		}

		It("Should return a valid Elastic search response", func() {
			mockDoer = new(thirdpartymock.MockDoer)

			client, err := elastic.NewClient(
				elastic.SetHttpClient(mockDoer),
				elastic.SetSniff(false),
				elastic.SetHealthcheck(false),
			)
			Expect(err).NotTo(HaveOccurred())

			exp := calicojson.Map{
				"from": 0,
				"query": calicojson.Map{
					"bool": calicojson.Map{
						"filter": []calicojson.Map{
							{
								"range": calicojson.Map{
									"end_time": calicojson.Map{
										"from":          fromTime.Unix(),
										"include_lower": false,
										"include_upper": true,
										"to":            toTime.Unix(),
									},
								},
							},
							{
								"bool": calicojson.Map{
									"should": []calicojson.Map{
										{
											"term": calicojson.Map{"source_type": "hep"},
										},
										{
											"term": calicojson.Map{"dest_type": "hep"},
										},
										{
											"term": calicojson.Map{"source_type": "hep"},
										},
										{
											"term": calicojson.Map{"dest_type": "hep"},
										},
									},
								},
							},
						},
					},
				},
				"size": 100,
				"sort": []calicojson.Map{
					{
						"test": calicojson.Map{
							"order": "desc",
						},
					},
					{
						"test2": calicojson.Map{
							"order": "asc",
						},
					},
				},
			}

			mockDoer.On("Do", mock.AnythingOfType("*http.Request")).Run(func(args mock.Arguments) {
				defer GinkgoRecover()
				req := args.Get(0).(*http.Request)

				body, err := ioutil.ReadAll(req.Body)
				Expect(err).NotTo(HaveOccurred())
				Expect(req.Body.Close()).NotTo(HaveOccurred())

				req.Body = ioutil.NopCloser(bytes.NewBuffer(body))

				requestJson := map[string]interface{}{}
				Expect(json.Unmarshal(body, &requestJson)).NotTo(HaveOccurred())
				Expect(calicojson.MustUnmarshalToStandardObject(body)).
					To(Equal(calicojson.MustUnmarshalToStandardObject(exp)))
			}).Return(&http.Response{
				StatusCode: http.StatusOK,
				Body: esSearchHitsResultToResponseBody(elastic.SearchResult{
					TookInMillis: 631,
					TimedOut:     false,
					Hits: &elastic.SearchHits{
						Hits:      esResponse,
						TotalHits: &elastic.TotalHits{Value: 2},
					},
				}),
			}, nil)

			params := &v1.SearchRequest{
				ClusterName: "cl_name_val",
				PageSize:    100,
				PageNum:     0,
				TimeRange: &lmav1.TimeRange{
					From: fromTime,
					To:   toTime,
				},
				SortBy: []v1.SearchRequestSortBy{{
					Field:      "test",
					Descending: true,
				}, {
					Field:      "test2",
					Descending: false,
				}},
			}

			r, err := http.NewRequest(
				http.MethodGet, "", bytes.NewReader([]byte(validRequestBody)))
			Expect(err).NotTo(HaveOccurred())

			results, err := search(lmaindex.FlowLogs(), params, userAuthReview, client, r)
			Expect(err).NotTo(HaveOccurred())
			Expect(results.NumPages).To(Equal(1))
			Expect(results.TotalHits).To(Equal(2))
			Expect(results.TimedOut).To(BeFalse())
			Expect(results.Took.Milliseconds()).To(Equal(int64(631)))
			var someLog *SomeLog
			for i, hit := range results.Hits {
				s, _ := hit.MarshalJSON()
				umerr := json.Unmarshal(s, &someLog)
				Expect(umerr).NotTo(HaveOccurred())
				Expect(someLog.Timestamp).To(Equal(expectedJSONResponse[i].Timestamp))
				Expect(someLog.StartTime).To(Equal(expectedJSONResponse[i].StartTime))
				Expect(someLog.EndTime).To(Equal(expectedJSONResponse[i].EndTime))
				Expect(someLog.Action).To(Equal(expectedJSONResponse[i].Action))
				Expect(someLog.BytesIn).To(Equal(expectedJSONResponse[i].BytesIn))
				Expect(someLog.BytesOut).To(Equal(expectedJSONResponse[i].BytesOut))
			}
		})

		It("Should return no hits when TotalHits are equal to zero", func() {
			mockDoer = new(thirdpartymock.MockDoer)

			client, err := elastic.NewClient(
				elastic.SetHttpClient(mockDoer),
				elastic.SetSniff(false),
				elastic.SetHealthcheck(false),
			)
			Expect(err).NotTo(HaveOccurred())

			exp := calicojson.Map{
				"from": 0,
				"query": calicojson.Map{
					"bool": calicojson.Map{
						"filter": []calicojson.Map{
							{
								"range": calicojson.Map{
									"end_time": calicojson.Map{
										"from":          fromTime.Unix(),
										"include_lower": false,
										"include_upper": true,
										"to":            toTime.Unix(),
									},
								},
							},
							{
								"bool": calicojson.Map{
									"should": []calicojson.Map{
										{
											"term": calicojson.Map{"source_type": "hep"},
										},
										{
											"term": calicojson.Map{"dest_type": "hep"},
										},
										{
											"term": calicojson.Map{"source_type": "hep"},
										},
										{
											"term": calicojson.Map{"dest_type": "hep"},
										},
									},
								},
							},
						},
					},
				},
				"size": 100,
			}

			mockDoer.On("Do", mock.AnythingOfType("*http.Request")).Run(func(args mock.Arguments) {
				defer GinkgoRecover()
				req := args.Get(0).(*http.Request)

				body, err := ioutil.ReadAll(req.Body)
				Expect(err).NotTo(HaveOccurred())
				Expect(req.Body.Close()).NotTo(HaveOccurred())

				req.Body = ioutil.NopCloser(bytes.NewBuffer(body))

				requestJson := map[string]interface{}{}
				Expect(json.Unmarshal(body, &requestJson)).NotTo(HaveOccurred())
				Expect(calicojson.MustUnmarshalToStandardObject(body)).
					To(Equal(calicojson.MustUnmarshalToStandardObject(exp)))
			}).Return(&http.Response{
				StatusCode: http.StatusOK,
				Body: esSearchHitsResultToResponseBody(elastic.SearchResult{
					TookInMillis: 631,
					TimedOut:     false,
					Hits: &elastic.SearchHits{
						Hits:      esResponse,
						TotalHits: &elastic.TotalHits{Value: 0},
					},
				}),
			}, nil)

			params := &v1.SearchRequest{
				ClusterName: "cl_name_val",
				PageSize:    100,
				PageNum:     0,
				TimeRange: &lmav1.TimeRange{
					From: fromTime,
					To:   toTime,
				},
			}

			r, err := http.NewRequest(
				http.MethodGet, "", bytes.NewReader([]byte(validRequestBody)))
			Expect(err).NotTo(HaveOccurred())

			results, err := search(lmaindex.FlowLogs(), params, userAuthReview, client, r)
			Expect(err).NotTo(HaveOccurred())
			Expect(results.NumPages).To(Equal(1))
			Expect(results.TotalHits).To(Equal(0))
			Expect(results.TimedOut).To(BeFalse())
			Expect(results.Took.Milliseconds()).To(Equal(int64(631)))
			var emptyHitsResponse []json.RawMessage
			Expect(results.Hits).To(Equal(emptyHitsResponse))
		})

		It("Should return no hits when ElasticSearch Hits are empty (nil)", func() {
			mockDoer = new(thirdpartymock.MockDoer)

			client, err := elastic.NewClient(
				elastic.SetHttpClient(mockDoer),
				elastic.SetSniff(false),
				elastic.SetHealthcheck(false),
			)
			Expect(err).NotTo(HaveOccurred())

			exp := calicojson.Map{
				"from": 0,
				"query": calicojson.Map{
					"bool": calicojson.Map{
						"filter": []calicojson.Map{
							{
								"range": calicojson.Map{
									"end_time": calicojson.Map{
										"from":          fromTime.Unix(),
										"include_lower": false,
										"include_upper": true,
										"to":            toTime.Unix(),
									},
								},
							},
							{
								"bool": calicojson.Map{
									"should": []calicojson.Map{
										{
											"term": calicojson.Map{"source_type": "hep"},
										},
										{
											"term": calicojson.Map{"dest_type": "hep"},
										},
										{
											"term": calicojson.Map{"source_type": "hep"},
										},
										{
											"term": calicojson.Map{"dest_type": "hep"},
										},
									},
								},
							},
						},
					},
				},
				"size": 100,
			}

			mockDoer.On("Do", mock.AnythingOfType("*http.Request")).Run(func(args mock.Arguments) {
				defer GinkgoRecover()
				req := args.Get(0).(*http.Request)

				body, err := ioutil.ReadAll(req.Body)
				Expect(err).NotTo(HaveOccurred())
				Expect(req.Body.Close()).NotTo(HaveOccurred())

				req.Body = ioutil.NopCloser(bytes.NewBuffer(body))

				requestJson := map[string]interface{}{}
				Expect(json.Unmarshal(body, &requestJson)).NotTo(HaveOccurred())
				Expect(calicojson.MustUnmarshalToStandardObject(body)).
					To(Equal(calicojson.MustUnmarshalToStandardObject(exp)))
			}).Return(&http.Response{
				StatusCode: http.StatusOK,
				Body: esSearchHitsResultToResponseBody(elastic.SearchResult{
					TookInMillis: 631,
					TimedOut:     false,
					Hits: &elastic.SearchHits{
						Hits:      nil,
						TotalHits: &elastic.TotalHits{Value: 0},
					},
				}),
			}, nil)

			params := &v1.SearchRequest{
				ClusterName: "cl_name_val",
				PageSize:    100,
				PageNum:     0,
				TimeRange: &lmav1.TimeRange{
					From: fromTime,
					To:   toTime,
				},
			}

			r, err := http.NewRequest(
				http.MethodGet, "", bytes.NewReader([]byte(validRequestBody)))
			Expect(err).NotTo(HaveOccurred())

			results, err := search(lmaindex.FlowLogs(), params, userAuthReview, client, r)
			Expect(err).NotTo(HaveOccurred())
			Expect(results.NumPages).To(Equal(1))
			Expect(results.TotalHits).To(Equal(0))
			Expect(results.TimedOut).To(BeFalse())
			Expect(results.Took.Milliseconds()).To(Equal(int64(631)))
			var emptyHitsResponse []json.RawMessage
			Expect(results.Hits).To(Equal(emptyHitsResponse))
		})

		It("Should return an error with data when ElasticSearch returns TimeOut==true", func() {
			mockDoer = new(thirdpartymock.MockDoer)

			client, err := elastic.NewClient(
				elastic.SetHttpClient(mockDoer),
				elastic.SetSniff(false),
				elastic.SetHealthcheck(false),
			)
			Expect(err).NotTo(HaveOccurred())

			exp := calicojson.Map{
				"from": 0,
				"query": calicojson.Map{
					"bool": calicojson.Map{
						"filter": []calicojson.Map{
							{
								"range": calicojson.Map{
									"end_time": calicojson.Map{
										"from":          fromTime.Unix(),
										"include_lower": false,
										"include_upper": true,
										"to":            toTime.Unix(),
									},
								},
							},
							{
								"bool": calicojson.Map{
									"should": []calicojson.Map{
										{
											"term": calicojson.Map{"source_type": "hep"},
										},
										{
											"term": calicojson.Map{"dest_type": "hep"},
										},
										{
											"term": calicojson.Map{"source_type": "hep"},
										},
										{
											"term": calicojson.Map{"dest_type": "hep"},
										},
									},
								},
							},
						},
					},
				},
				"size": 100,
			}

			mockDoer.On("Do", mock.AnythingOfType("*http.Request")).Run(func(args mock.Arguments) {
				defer GinkgoRecover()
				req := args.Get(0).(*http.Request)

				body, err := ioutil.ReadAll(req.Body)
				Expect(err).NotTo(HaveOccurred())
				Expect(req.Body.Close()).NotTo(HaveOccurred())

				req.Body = ioutil.NopCloser(bytes.NewBuffer(body))

				requestJson := map[string]interface{}{}
				Expect(json.Unmarshal(body, &requestJson)).NotTo(HaveOccurred())
				Expect(calicojson.MustUnmarshalToStandardObject(body)).
					To(Equal(calicojson.MustUnmarshalToStandardObject(exp)))
			}).Return(&http.Response{
				StatusCode: http.StatusOK,
				Body: esSearchHitsResultToResponseBody(elastic.SearchResult{
					TookInMillis: 10000,
					TimedOut:     true,
					Hits: &elastic.SearchHits{
						Hits:      esResponse,
						TotalHits: &elastic.TotalHits{Value: 2},
					},
				}),
			}, nil)

			params := &v1.SearchRequest{
				ClusterName: "cl_name_val",
				PageSize:    100,
				PageNum:     0,
				TimeRange: &lmav1.TimeRange{
					From: fromTime,
					To:   toTime,
				},
			}

			r, err := http.NewRequest(
				http.MethodGet, "", bytes.NewReader([]byte(validRequestBody)))
			Expect(err).NotTo(HaveOccurred())
			results, err := search(lmaindex.FlowLogs(), params, userAuthReview, client, r)
			Expect(err).To(HaveOccurred())
			var se *httputils.HttpStatusError
			Expect(errors.As(err, &se)).To(BeTrue())
			Expect(se.Status).To(Equal(500))
			Expect(se.Msg).
				To(Equal("timed out querying tigera_secure_ee_flows.cl_name_val.*"))
			Expect(results).To(BeNil())
		})

		It("Should return an error when ElasticSearch returns an error", func() {
			mockDoer = new(thirdpartymock.MockDoer)

			client, err := elastic.NewClient(
				elastic.SetHttpClient(mockDoer),
				elastic.SetSniff(false),
				elastic.SetHealthcheck(false),
			)
			Expect(err).NotTo(HaveOccurred())

			exp := calicojson.Map{
				"from": 0,
				"query": calicojson.Map{
					"bool": calicojson.Map{
						"filter": []calicojson.Map{
							{
								"range": calicojson.Map{
									"end_time": calicojson.Map{
										"from":          fromTime.Unix(),
										"include_lower": false,
										"include_upper": true,
										"to":            toTime.Unix(),
									},
								},
							},
							{
								"bool": calicojson.Map{
									"should": []calicojson.Map{
										{
											"term": calicojson.Map{"source_type": "hep"},
										},
										{
											"term": calicojson.Map{"dest_type": "hep"},
										},
										{
											"term": calicojson.Map{"source_type": "hep"},
										},
										{
											"term": calicojson.Map{"dest_type": "hep"},
										},
									},
								},
							},
						},
					},
				},
				"size": 100,
			}

			mockDoer.On("Do", mock.AnythingOfType("*http.Request")).Run(func(args mock.Arguments) {
				defer GinkgoRecover()
				req := args.Get(0).(*http.Request)

				body, err := ioutil.ReadAll(req.Body)
				Expect(err).NotTo(HaveOccurred())
				Expect(req.Body.Close()).NotTo(HaveOccurred())

				req.Body = ioutil.NopCloser(bytes.NewBuffer(body))

				requestJson := map[string]interface{}{}
				Expect(json.Unmarshal(body, &requestJson)).NotTo(HaveOccurred())
				Expect(calicojson.MustUnmarshalToStandardObject(body)).
					To(Equal(calicojson.MustUnmarshalToStandardObject(exp)))
			}).Return(&http.Response{
				StatusCode: http.StatusOK,
				Body: esSearchHitsResultToResponseBody(elastic.SearchResult{
					TookInMillis: 10000,
					TimedOut:     true,
					Hits: &elastic.SearchHits{
						Hits:      esResponse,
						TotalHits: &elastic.TotalHits{Value: 2},
					},
				}),
			}, errors.New("ESError: Elastic search generic error"))

			params := &v1.SearchRequest{
				ClusterName: "cl_name_val",
				PageSize:    100,
				PageNum:     0,
				TimeRange: &lmav1.TimeRange{
					From: fromTime,
					To:   toTime,
				},
			}

			r, err := http.NewRequest(
				http.MethodGet, "", bytes.NewReader([]byte(validRequestBody)))
			Expect(err).NotTo(HaveOccurred())

			results, err := search(lmaindex.FlowLogs(), params, userAuthReview, client, r)
			Expect(err).To(HaveOccurred())

			var httpErr *httputils.HttpStatusError
			Expect(errors.As(err, &httpErr)).To(BeTrue())
			Expect(httpErr.Status).To(Equal(500))
			Expect(httpErr.Msg).To(Equal("ESError: Elastic search generic error"))
			Expect(results).To(BeNil())
		})
	})

	Context("parseRequestBodyForParams response validation", func() {
		It("Should return a SearchError when http request not POST or GET", func() {
			var w http.ResponseWriter
			r, err := http.NewRequest(http.MethodPut, "", bytes.NewReader([]byte(validRequestBody)))
			Expect(err).NotTo(HaveOccurred())

			_, err = parseRequestBodyForParams(w, r)
			Expect(err).To(HaveOccurred())
			var se *httputils.HttpStatusError
			Expect(errors.As(err, &se)).To(BeTrue())
		})

		It("Should return a HttpStatusError when parsing a http status error body", func() {
			r, err := http.NewRequest(
				http.MethodGet, "", bytes.NewReader([]byte(invalidRequestBodyBadlyFormedStringValue)))
			Expect(err).NotTo(HaveOccurred())

			var w http.ResponseWriter
			_, err = parseRequestBodyForParams(w, r)
			Expect(err).To(HaveOccurred())

			var mr *httputils.HttpStatusError
			Expect(errors.As(err, &mr)).To(BeTrue())
		})

		It("Should return an error when parsing a page size that is greater than lte", func() {
			r, err := http.NewRequest(
				http.MethodGet, "", bytes.NewReader([]byte(validRequestBodyPageSizeGreaterThanLTE)))
			Expect(err).NotTo(HaveOccurred())

			var w http.ResponseWriter
			_, err = parseRequestBodyForParams(w, r)
			Expect(err).To(HaveOccurred())

			var se *httputils.HttpStatusError
			Expect(errors.As(err, &se)).To(BeTrue())
			Expect(se.Status).To(Equal(400))
			Expect(se.Msg).To(Equal("error with field PageSize = '1001' (Reason: failed to validate Field: PageSize " +
				"because of Tag: lte )"))
		})

		It("Should return an error when parsing a page size that is less than gte", func() {
			r, err := http.NewRequest(
				http.MethodGet, "", bytes.NewReader([]byte(validRequestBodyPageSizeLessThanGTE)))
			Expect(err).NotTo(HaveOccurred())

			var w http.ResponseWriter
			_, err = parseRequestBodyForParams(w, r)
			Expect(err).To(HaveOccurred())

			var se *httputils.HttpStatusError
			Expect(errors.As(err, &se)).To(BeTrue())
			Expect(se.Status).To(Equal(400))
			Expect(se.Msg).To(Equal("error with field PageSize = '-1' (Reason: failed to validate Field: PageSize "+
				"because of Tag: gte )"), se.Msg)
		})

		It("Should return an error when parsing an empty value for time_range", func() {
			r, err := http.NewRequest(
				http.MethodGet, "", bytes.NewReader([]byte(invalidRequestBodyTimeRangeMissing)))
			Expect(err).NotTo(HaveOccurred())

			var w http.ResponseWriter
			_, err = parseRequestBodyForParams(w, r)
			Expect(err).To(HaveOccurred())

			var se *httputils.HttpStatusError
			Expect(errors.As(err, &se)).To(BeTrue())
			Expect(se.Status).To(Equal(400))
			Expect(se.Msg).To(Equal("error with field TimeRange = '<nil>' (Reason: failed to validate "+
				"Field: TimeRange because of Tag: required )"), se.Msg)
		})

		It("Should return an error when parsing an invalid value for time_range value", func() {
			r, err := http.NewRequest(
				http.MethodGet, "", bytes.NewReader([]byte(invalidRequestBodyTimeRangeContainsInvalidTimeValue)))
			Expect(err).NotTo(HaveOccurred())

			var w http.ResponseWriter
			_, err = parseRequestBodyForParams(w, r)
			Expect(err).To(HaveOccurred())

			var se *httputils.HttpStatusError
			Expect(errors.As(err, &se)).To(BeTrue())
			Expect(se.Status).To(Equal(400))
			Expect(se.Msg).To(Equal("Request body contains an invalid value for the \"time_range\" "+
				"field (at position 18)"), se.Msg)
		})
	})
})

func esSearchHitsResultToResponseBody(searchResult elastic.SearchResult) io.ReadCloser {
	byts, err := json.Marshal(searchResult)
	if err != nil {
		panic(err)
	}

	return ioutil.NopCloser(bytes.NewBuffer(byts))
}
