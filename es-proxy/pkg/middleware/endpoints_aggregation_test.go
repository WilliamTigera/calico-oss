package middleware

import (
	"bytes"
	"context"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	libcalicov3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/projectcalico/calico/libcalico-go/lib/json"
	lapi "github.com/projectcalico/calico/linseed/pkg/apis/v1"
	lmaapi "github.com/projectcalico/calico/lma/pkg/apis/v1"
	"github.com/projectcalico/calico/lma/pkg/httputils"
	querycacheclient "github.com/projectcalico/calico/ts-queryserver/pkg/querycache/client"
)

// The user authentication review mock struct implementing the authentication review interface.
type userAuthorizationReviewMock struct {
	verbs []libcalicov3.AuthorizedResourceVerbs
	err   error
}

// PerformReviewForElasticLogs wraps a mocked version of the authorization review method
// PerformReviewForElasticLogs.
func (a userAuthorizationReviewMock) PerformReview(
	ctx context.Context, cluster string,
) ([]libcalicov3.AuthorizedResourceVerbs, error) {
	return a.verbs, a.err
}

var _ = Describe("", func() {
	var (
		req *http.Request
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("test buildQueryServerEndpointKeyString result", func() {
		result := buildQueryServerEndpointKeyString("ns", "name", "nameaggr")
		Expect(result).To(Equal(".*ns/.*-name"))

		result = buildQueryServerEndpointKeyString("ns", "-", "nameaggr")
		Expect(result).To(Equal(".*ns/.*-nameaggr"))
	})

	Context("test validateEndpointsAggregationRequest", func() {
		DescribeTable("validate ClusterName",
			func(clusterName, clusterIdHeader, expectedCluster string) {
				endpointReq := &EndpointsAggregationRequest{}

				if len(clusterName) > 0 {
					endpointReq.ClusterName = clusterName
				}

				reqBodyBytes, err := json.Marshal(endpointReq)
				Expect(err).ShouldNot(HaveOccurred())

				req, err = http.NewRequest("POST", "https://test", bytes.NewBuffer(reqBodyBytes))
				Expect(err).ShouldNot(HaveOccurred())

				if len(clusterIdHeader) > 0 {
					req.Header.Add("x-cluster-id", clusterIdHeader)
				}

				err = validateEndpointsAggregationRequest(req, endpointReq)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(endpointReq.ClusterName).To(Equal(expectedCluster))

			},
			Entry("should not change ClusterName if it is set in the request body", "cluster-a", "cluster-b", "cluster-a"),
			Entry("should set ClusterName from request header if it is not provided in the request body", "", "cluster-b", "cluster-b"),
			Entry("should set ClusterName to default if it neither provided in the request body nor header", "", "", "cluster"),
		)

		DescribeTable("validate ShowDeniedEndpoints",
			func(filterDeniedEndpoints bool, endpointList []string, expectErr bool, errMsg string) {

				epReq := EndpointsAggregationRequest{
					ClusterName: "",
					TimeRange:   nil,
					Timeout:     nil,
				}

				epReq.ShowDeniedEndpoints = filterDeniedEndpoints

				if len(endpointList) > 0 {
					epReq.EndpointsList = endpointList
				}

				reqBodyBytes, err := json.Marshal(epReq)
				Expect(err).ShouldNot(HaveOccurred())

				req, err = http.NewRequest("POST", "https://test", bytes.NewBuffer(reqBodyBytes))
				Expect(err).ShouldNot(HaveOccurred())

				err = validateEndpointsAggregationRequest(req, &epReq)

				if expectErr {
					Expect(err).Should(HaveOccurred())
					Expect(err.(*httputils.HttpStatusError).Msg).To(Equal(errMsg))
				} else {
					Expect(err).ShouldNot(HaveOccurred())
				}

			},
			Entry("pass validation when both ShowDeniedEndpoints and endpointlist are not set",
				false, []string{}, false, nil),
			Entry("pass validation when only endpointlist is provided ",
				false, []string{"endpoint1"}, false, nil),
			Entry("fail validation when both ShowDeniedEndpoints and endpointlist are provided",
				true, []string{"endpoint1"}, true, "both ShowDeniedEndpoints and endpointList can not be provided in the same request"),
			Entry("pass validation when ShowDeniedEndpoints is set to true",
				true, []string{}, false, nil),
		)

		DescribeTable("validate TimeOut",
			func(timeout, expectedTimeout *v1.Duration) {
				endpointReq := &EndpointsAggregationRequest{}

				if timeout != nil {
					endpointReq.Timeout = timeout
				}

				reqBodyBytes, err := json.Marshal(endpointReq)
				Expect(err).ShouldNot(HaveOccurred())

				req, err = http.NewRequest("POST", "https://test", bytes.NewBuffer(reqBodyBytes))
				Expect(err).ShouldNot(HaveOccurred())

				err = validateEndpointsAggregationRequest(req, endpointReq)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(endpointReq.Timeout).To(Equal(expectedTimeout))
			},
			Entry("should not change timeout if provided",
				&v1.Duration{Duration: 10 * time.Second}, &v1.Duration{Duration: 10 * time.Second}),
			Entry("should set default timeout if not provided",
				nil, &v1.Duration{Duration: DefaultRequestTimeout}),
		)

		testTime := time.Now().UTC()
		DescribeTable("validate TimeRange",
			func(timerange *lmaapi.TimeRange, expectedFrom *time.Time, expectErr bool, errMsg string) {
				endpointReq := &EndpointsAggregationRequest{}

				if timerange != nil {
					endpointReq.TimeRange = timerange
				}

				reqBodyBytes, err := json.Marshal(endpointReq)
				Expect(err).ShouldNot(HaveOccurred())

				req, err = http.NewRequest("POST", "https://test", bytes.NewBuffer(reqBodyBytes))
				Expect(err).ShouldNot(HaveOccurred())

				err = validateEndpointsAggregationRequest(req, endpointReq)

				if expectErr {
					Expect(err).Should(HaveOccurred())
					Expect(err.(*httputils.HttpStatusError).Msg).To(Equal("time_range \"to\" should not be provided"))
				} else {
					Expect(err).ShouldNot(HaveOccurred())
					Expect(endpointReq.TimeRange.To).ToNot(BeNil())

					if expectedFrom != nil {
						Expect(endpointReq.TimeRange.From).To(Equal(*expectedFrom))
					}
				}

			},
			Entry("should fail if timeRange.To is set",
				&lmaapi.TimeRange{To: time.Now().UTC()}, nil, true, "time_range \"to\" should not be provided"),
			Entry("should set timeRange.To to Now if timeRange.From is set",
				&lmaapi.TimeRange{From: testTime}, &testTime, false, ""),
			Entry("should not set timeRange.To if timeRange.From is not set",
				&lmaapi.TimeRange{}, nil, false, ""),
		)
	})

	Context("test getQueryServerRequestParams", func() {
		DescribeTable("validate getQueryServerRequestParams result",
			func(endpoints []string, showDeniedEndpoint bool, expectedList []string) {
				params := EndpointsAggregationRequest{
					ShowDeniedEndpoints: showDeniedEndpoint,
				}

				queryEndpointsRespBody := getQueryServerRequestParams(&params, endpoints)

				Expect(queryEndpointsRespBody.EndpointsList).To(Equal(expectedList))

			},
			Entry("should not add endpoints list when endpoints map is nil", nil, false, nil),
			Entry("should add endpoints list when endpoints list is empty", []string{}, true, []string{}),
			Entry("should add endpoints list when endpoints map has values", []string{"pod1", "pod2", "pod10", "pod20"},
				true,
				[]string{"pod1", "pod2", "pod10", "pod20"}),
		)
	})

	Context("test buildFlowLogParamsForDeniedTrafficSearch", func() {
		It("policyMatch action=deny should be added to params when calling linseed", func() {
			req := &EndpointsAggregationRequest{}
			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			authReview := userAuthorizationReviewMock{
				verbs: []libcalicov3.AuthorizedResourceVerbs{},
				err:   nil,
			}
			pageNumber := 2
			pageSize := 1

			flParams, err := buildFlowLogParamsForDeniedTrafficSearch(ctx, authReview, req, pageNumber, pageSize)

			Expect(err).ShouldNot(HaveOccurred())
			Expect(flParams.PolicyMatches).ToNot(BeNil())
			Expect(flParams.PolicyMatches).To(HaveLen(1))
			Expect(*flParams.PolicyMatches[0].Action).To(Equal(lapi.FlowActionDeny))

		})
	})

	Context("test updateResults", func() {
		var (
			endpointsRespBody querycacheclient.QueryEndpointsResp
			deniedEndpoints   []string
		)
		BeforeEach(func() {
			deniedEndpoints = []string{".*ns1/.*-ep1", ".*ns2/.*-ep10"}

			endpointsRespBody = querycacheclient.QueryEndpointsResp{
				Count: 5,
				Items: []querycacheclient.Endpoint{
					{Name: "ep1", Namespace: "ns1", Node: "node1", Pod: "ep1"},
					{Name: "ep2", Namespace: "ns1", Node: "node2", Pod: "ep2"},
					{Name: "ep10", Namespace: "ns2", Node: "node1", Pod: "ep10"},
					{Name: "ep11", Namespace: "ns2", Node: "node1", Pod: "bp11"},
					{Name: "ep10", Namespace: "ns3", Node: "node2", Pod: "ep10"},
				},
			}
		})
		It("should add hasDeniedTraffic: true for endpoints in the deniedEndponts map", func() {
			endpointsResponse, err := updateResults(&endpointsRespBody, deniedEndpoints)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(endpointsResponse.Count).To(Equal(5))

			for _, item := range endpointsResponse.Item {
				if item.Namespace == "ns1" && item.Pod == "ep1" {
					Expect(item.HasDeniedTraffic).To(BeTrue())
				} else if item.Namespace == "ns2" && item.Pod == "ep10" {
					Expect(item.HasDeniedTraffic).To(BeTrue())
				} else {
					Expect(item.HasDeniedTraffic).To(BeFalse())
				}
			}
		})
	})
})
