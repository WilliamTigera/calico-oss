// Copyright (c) 2018-2023 Tigera, Inc. All rights reserved.
package fv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	log "github.com/sirupsen/logrus"

	"k8s.io/client-go/kubernetes/fake"

	apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/calico/calicoctl/calicoctl/resourcemgr"
	"github.com/projectcalico/calico/libcalico-go/lib/apiconfig"
	libapi "github.com/projectcalico/calico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/calico/libcalico-go/lib/backend"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/calico/libcalico-go/lib/testutils"
	"github.com/projectcalico/calico/ts-queryserver/pkg/querycache/client"
	"github.com/projectcalico/calico/ts-queryserver/queryserver/config"
	authhandler "github.com/projectcalico/calico/ts-queryserver/queryserver/handlers/auth"
	queryhdr "github.com/projectcalico/calico/ts-queryserver/queryserver/handlers/query"
	"github.com/projectcalico/calico/ts-queryserver/queryserver/server"
)

var _ = testutils.E2eDatastoreDescribe("Query tests", testutils.DatastoreEtcdV3, func(config apiconfig.CalicoAPIConfig) {

	DescribeTable("Query tests (e2e with server)",
		func(tqds []testQueryData, crossCheck func(tqd testQueryData, addr string, netClient *http.Client)) {
			By("Creating a v3 client interface")
			c, err := clientv3.New(config)
			Expect(err).NotTo(HaveOccurred())

			By("Cleaning the datastore")
			be, err := backend.NewClient(config)
			Expect(err).NotTo(HaveOccurred())
			err = be.Clean()
			Expect(err).NotTo(HaveOccurred())

			// Choose an arbitrary port for the server to listen on.
			By("Choosing an arbitrary available local port for the queryserver")
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).NotTo(HaveOccurred())
			addr := listener.Addr().String()
			listener.Close()

			// Get server configuration variables meant for FVs.
			servercfg := getDummyConfigFromEnvFv(addr, "", "")

			fakeK8sClient := fake.NewSimpleClientset()
			mh := &mockHandler{}

			By("Starting the queryserver")
			srv := server.NewServer(fakeK8sClient, &config, servercfg, mh)
			err = srv.Start()
			Expect(err).NotTo(HaveOccurred())
			defer srv.Stop()

			var configured map[model.ResourceKey]resourcemgr.ResourceObject
			var netClient = &http.Client{Timeout: time.Second * 10}
			for _, tqd := range tqds {
				By(fmt.Sprintf("Creating the resources for test: %s", tqd.description))
				configured = createResources(c, tqd.resources, configured)

				By(fmt.Sprintf("Running query for test: %s", tqd.description))
				queryFn := getQueryFunction(tqd, addr, netClient)
				Eventually(queryFn).Should(Equal(tqd.response), tqd.description)
				Consistently(queryFn).Should(Equal(tqd.response), tqd.description)

				if crossCheck != nil {
					By("Running a cross-check query")
					crossCheck(tqd, addr, netClient)
				}
			}
		},

		Entry("Summary queries", summaryTestQueryData(), nil),
		Entry("Node queries", nodeTestQueryData(), nil),
		Entry("Endpoint queries", endpointTestQueryData(), crossCheckEndpointQuery),
		Entry("Policy queries", policyTestQueryData(), crossCheckPolicyQuery),
	)
})

func getQueryFunction(tqd testQueryData, addr string, netClient *http.Client) func() interface{} {

	By(fmt.Sprintf("Creating the query function for test: %s", tqd.description))
	return func() interface{} {
		By(fmt.Sprintf("Calculating the URL for the test: %s", tqd.description))
		qurl, httpMethod := calculateQueryUrl(addr, tqd.query)
		qbody := calculateQueryBody(tqd.query)

		// Return the result if we have it, otherwise the error, this allows us to use Eventually to
		// check both values and errors.
		log.WithField("url", qurl).Debug("Running query")

		var r *http.Response
		var err error
		switch httpMethod {
		case authhandler.MethodPOST:
			r, err = netClient.Post(qurl, "Application/Json", qbody)
		default:
			r, err = netClient.Get(qurl)
		}
		if err != nil {
			return err
		}
		defer r.Body.Close()
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}
		bodyString := string(bodyBytes)
		if r.StatusCode != http.StatusOK {
			return errorResponse{
				text: strings.TrimSpace(bodyString),
				code: r.StatusCode,
			}
		}

		if _, ok := tqd.response.(errorResponse); ok {
			// We are expecting an error but didn't get one, we'll have to return an error containing
			// the raw json.
			return fmt.Errorf("expecting error but command was successful: %s", bodyString)
		}

		// The response body should be json and the same type as the expected response object.
		ro := reflect.New(reflect.TypeOf(tqd.response).Elem()).Interface()
		err = json.Unmarshal(bodyBytes, ro)
		if err != nil {
			return fmt.Errorf("unmarshal error: %v: %v: %v", reflect.TypeOf(ro), err, bodyString)
		}
		return ro
	}
}

func calculateQueryUrl(addr string, query interface{}) (string, authhandler.HTTPMethod) {
	var parms []string
	u := "http://" + addr + "/"
	httpMethod := authhandler.MethodGET

	switch qt := query.(type) {
	case client.QueryEndpointsReq:
		u += "endpoints"
		if qt.Endpoint != nil {
			u = u + "/" + getNameFromResource(qt.Endpoint)
			break
		}

		httpMethod = authhandler.MethodPOST
	case client.QueryPoliciesReq:
		u += "policies"
		if qt.Policy != nil {
			u = u + "/" + getNameFromResource(qt.Policy)
			break
		}
		parms = appendResourceParm(parms, queryhdr.QueryEndpoint, qt.Endpoint)
		parms = appendResourceParm(parms, queryhdr.QueryNetworkSet, qt.NetworkSet)
		parms = appendStringParm(parms, queryhdr.QueryTier, qt.Tier)
		parms = appendStringParm(parms, queryhdr.QueryUnmatched, fmt.Sprint(qt.Unmatched))
		for k, v := range qt.Labels {
			parms = append(parms, queryhdr.QueryLabelPrefix+k+"="+v)
		}
		parms = appendPageParms(parms, qt.Page)
		parms = appendSortParms(parms, qt.Sort)
	case client.QueryNodesReq:
		u += "nodes"
		if qt.Node != nil {
			u = u + "/" + getNameFromResource(qt.Node)
			break
		}
		parms = appendPageParms(parms, qt.Page)
		parms = appendSortParms(parms, qt.Sort)
	case client.QueryClusterReq:
		u += "summary?from=now-15m&to=now-0m"
	}

	if len(parms) == 0 {
		return u, httpMethod
	}
	return u + "?" + strings.Join(parms, "&"), httpMethod
}

func calculateQueryBody(query interface{}) io.Reader {
	switch qt := query.(type) {
	case client.QueryEndpointsReq:
		var policy []string
		if qt.Policy != nil {
			policy = []string{getNameFromResource(qt.Policy)}
		}
		body := client.QueryEndpointsReqBody{
			Policy:              policy,
			RuleDirection:       qt.RuleDirection,
			RuleIndex:           qt.RuleIndex,
			RuleEntity:          qt.RuleEntity,
			RuleNegatedSelector: qt.RuleNegatedSelector,
			Selector:            qt.Selector,
			Unprotected:         qt.Unprotected,
			EndpointsList:       qt.EndpointsList,
			Node:                qt.Node,
			Unlabelled:          qt.Unlabelled,
			Page:                qt.Page,
			Sort:                qt.Sort,
		}

		bodyData, err := json.Marshal(body)
		Expect(err).ShouldNot(HaveOccurred())

		return bytes.NewReader(bodyData)
	}

	return nil
}

func appendPageParms(parms []string, page *client.Page) []string {
	if page == nil {
		return append(parms, queryhdr.QueryNumPerPage+"="+queryhdr.AllResults)
	}
	return append(parms,
		fmt.Sprintf("%s=%d", queryhdr.QueryPageNum, page.PageNum),
		fmt.Sprintf("%s=%d", queryhdr.QueryNumPerPage, page.NumPerPage),
	)
}

func appendSortParms(parms []string, sort *client.Sort) []string {
	if sort == nil {
		return parms
	}
	for _, f := range sort.SortBy {
		parms = append(parms, fmt.Sprintf("%s=%s", queryhdr.QuerySortBy, f))
	}
	return append(parms, fmt.Sprintf("%s=%v", queryhdr.QueryReverseSort, sort.Reverse))
}

func appendStringParm(parms []string, key, value string) []string {
	if value == "" {
		return parms
	}
	return append(parms, key+"="+url.QueryEscape(value))
}

func appendResourceParm(parms []string, key string, value model.Key) []string {
	if value == nil {
		return parms
	}
	return append(parms, key+"="+getNameFromResource(value))
}

func getNameFromResource(k model.Key) string {
	rk := k.(model.ResourceKey)
	if rk.Namespace != "" {
		return rk.Namespace + "/" + rk.Name
	}
	return rk.Name
}

func crossCheckPolicyQuery(tqd testQueryData, addr string, netClient *http.Client) {
	qpr, ok := tqd.response.(*client.QueryPoliciesResp)
	if !ok {
		// Don't attempt to cross check errored queries since we have nothing to cross-check.
		return
	}
	for _, p := range qpr.Items {
		policy := p.Name
		if p.Namespace != "" {
			policy = p.Namespace + "/" + policy
		}

		By(fmt.Sprintf("Running endpoint query for policy: %s", policy))
		qurl := "http://" + addr + "/endpoints"
		body := client.QueryEndpointsReqBody{
			Policy: []string{policy},
			Page:   nil,
		}
		bodyData, err := json.Marshal(body)
		Expect(err).ShouldNot(HaveOccurred())

		r, err := netClient.Post(qurl, "Application/Json", bytes.NewReader(bodyData))
		Expect(err).NotTo(HaveOccurred())
		defer r.Body.Close()
		bodyBytes, err := io.ReadAll(r.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.StatusCode).To(Equal(http.StatusOK))
		output := client.QueryEndpointsResp{}
		err = json.Unmarshal(bodyBytes, &output)
		Expect(err).NotTo(HaveOccurred())
		var numWeps, numHeps int
		for _, i := range output.Items {
			if i.Kind == libapi.KindWorkloadEndpoint {
				numWeps++
			} else {
				numHeps++
			}
		}
		Expect(numHeps).To(Equal(p.NumHostEndpoints))
		Expect(numWeps).To(Equal(p.NumWorkloadEndpoints))
	}
}

func crossCheckEndpointQuery(tqd testQueryData, addr string, netClient *http.Client) {
	qpr, ok := tqd.response.(*client.QueryEndpointsResp)
	if !ok {
		// Don't attempt to cross check errored queries since we have nothing to cross-check.
		return
	}
	for _, p := range qpr.Items {
		endpoint := p.Name
		if p.Namespace != "" {
			endpoint = p.Namespace + "/" + endpoint
		}

		By(fmt.Sprintf("Running policy query for endpoint: %s", endpoint))
		qurl := "http://" + addr + "/policies?endpoint=" + endpoint + "&page=all"

		r, err := netClient.Get(qurl)
		Expect(err).NotTo(HaveOccurred())
		defer r.Body.Close()
		bodyBytes, err := io.ReadAll(r.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.StatusCode).To(Equal(http.StatusOK))
		output := client.QueryPoliciesResp{}
		err = json.Unmarshal(bodyBytes, &output)
		Expect(err).NotTo(HaveOccurred())
		var numNps, numGnps int
		for _, i := range output.Items {
			if i.Kind == apiv3.KindNetworkPolicy {
				numNps++
			} else {
				numGnps++
			}
		}
		Expect(numNps).To(Equal(p.NumNetworkPolicies))
		Expect(numGnps).To(Equal(p.NumGlobalNetworkPolicies))
	}
}

// getDummyConfigFromEnvFv returns the server configuration variables meant for FV tests.
func getDummyConfigFromEnvFv(addr, webKey, webCert string) *config.Config {
	config := &config.Config{
		ListenAddr: addr,
		TLSCert:    webCert,
		TLSKey:     webKey,
	}

	return config
}

type mockHandler struct {
}

func (mh *mockHandler) AuthenticationHandler(handlerFunc http.HandlerFunc, httpMethodAllowed authhandler.HTTPMethod) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != string(httpMethodAllowed) {
			// Operation not allowed
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, err := w.Write([]byte("Method Not Allowed"))
			if err != nil {
				log.WithError(err).Error("failed to write body to response.")
			}
			return
		}

		handlerFunc.ServeHTTP(w, req)
	}
}

// TODO(rlb):
// - reorder policies
// - re-node a HostEndpoint
