// Copyright (c) 2019-2021 Tigera, Inc. All rights reserved.
package server_test

import (
	"net/http"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/stretchr/testify/mock"

	authzv1 "k8s.io/api/authorization/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	"github.com/tigera/api/pkg/client/clientset_generated/clientset/fake"
	"github.com/projectcalico/calico/compliance/pkg/datastore"
	"github.com/projectcalico/calico/compliance/pkg/server"
	"github.com/projectcalico/calico/lma/pkg/api"
	lmaauth "github.com/projectcalico/calico/lma/pkg/auth"
	"github.com/projectcalico/calico/lma/pkg/elastic"
)

func newArchivedReportData(reportName, reportTypeName string) *api.ArchivedReportData {
	return &api.ArchivedReportData{
		UISummary: `{"foobar":"hello-100-goodbye"}`,
		ReportData: &v3.ReportData{
			ReportName:     reportName,
			ReportTypeName: reportTypeName,
			StartTime:      now,
			EndTime:        nowPlusHour,
			EndpointsSummary: v3.EndpointsSummary{
				NumTotal: 100,
			},
			GenerationTime: now,
		},
	}
}

func newGlobalReportType(typeName string) v3.GlobalReportType {
	return v3.GlobalReportType{
		ObjectMeta: v1.ObjectMeta{
			Name: typeName,
		},
		Spec: v3.ReportTypeSpec{
			UISummaryTemplate: v3.ReportTemplate{
				Template: "{\"foobar\":\"hello-{{ .EndpointsSummary.NumTotal }}-goodbye\"}",
			},
			DownloadTemplates: []v3.ReportTemplate{
				{
					Name:        "boo.csv",
					Description: "This is a boo file",
					Template:    "xxx-{{ .EndpointsSummary.NumTotal }} BOO!",
				},
				{
					Name:        "bar.csv",
					Description: "This is a bar file",
					Template:    "yyy-{{ .EndpointsSummary.NumTotal }} BAR!",
				},
			},
		},
	}
}

var (
	now         = v1.Time{Time: time.Unix(time.Now().Unix(), 0)}
	nowPlusHour = v1.Time{Time: now.Add(time.Hour)}

	reportTypeGettable    = newGlobalReportType("inventoryGet")
	reportTypeNotGettable = newGlobalReportType("inventoryNoGo")

	reportGetTypeGet     = newArchivedReportData("Get", "inventoryGet")
	reportNoGetTypeNoGet = newArchivedReportData("somethingelse", "inventoryNoGo")
	reportGetTypeNoGet   = newArchivedReportData("Get", "inventoryNoGo")

	forecastFile1 = forecastFile{
		Format:      "boo.csv",
		FileContent: "xxx-100 BOO!",
	}

	forecastFile2 = forecastFile{
		Format:      "bar.csv",
		FileContent: "yyy-100 BAR!",
	}
)

var _ = Describe("List", func() {
	var mockClientSetFactory *datastore.MockClusterCtxK8sClientFactory
	var mockESFactory *elastic.MockClusterContextClientFactory

	var mockAuthenticator *lmaauth.MockJWTAuth
	var mockRBACAuthorizer *lmaauth.MockRBACAuthorizer
	var mockESClient *elastic.MockClient

	BeforeEach(func() {
		mockClientSetFactory = new(datastore.MockClusterCtxK8sClientFactory)
		mockESFactory = new(elastic.MockClusterContextClientFactory)

		mockAuthenticator = new(lmaauth.MockJWTAuth)
		mockRBACAuthorizer = new(lmaauth.MockRBACAuthorizer)
		mockESClient = new(elastic.MockClient)
	})

	AfterEach(func() {
		mockClientSetFactory.AssertExpectations(GinkgoT())
		mockESFactory.AssertExpectations(GinkgoT())
		mockAuthenticator.AssertExpectations(GinkgoT())
		mockRBACAuthorizer.AssertExpectations(GinkgoT())
		mockESClient.AssertExpectations(GinkgoT())
	})

	It("tests can list with Gettable Report and ReportType", func() {
		By("Starting a test server")

		mockAuthenticator.On("Authenticate", mock.Anything).Return(&user.DefaultInfo{}, 0, nil)

		mockRBACAuthorizer.On("Authorize", mock.Anything, &authzv1.ResourceAttributes{
			Verb: "get", Group: "projectcalico.org", Resource: "globalreports", Name: "Get",
		}, mock.Anything).Return(true, nil)
		mockRBACAuthorizer.On("Authorize", mock.Anything, &authzv1.ResourceAttributes{
			Verb: "get", Group: "projectcalico.org", Resource: "globalreporttypes", Name: "inventoryGet",
		}, mock.Anything).Return(true, nil)
		mockRBACAuthorizer.On("Authorize", mock.Anything, &authzv1.ResourceAttributes{
			Verb: "get", Group: "projectcalico.org", Resource: "globalreporttypes", Name: "inventoryNoGo",
		}, mock.Anything).Return(false, nil)
		mockRBACAuthorizer.On("Authorize", mock.Anything, &authzv1.ResourceAttributes{
			Verb: "list", Group: "projectcalico.org", Resource: "globalreports",
		}, mock.Anything).Return(true, nil)
		mockRBACAuthorizer.On("Authorize", mock.Anything, &authzv1.ResourceAttributes{
			Verb: "get", Group: "projectcalico.org", Resource: "globalreports", Name: "somethingelse",
		}, mock.Anything).Return(false, nil)

		mockClientSetFactory.On("RBACAuthorizerForCluster", mock.Anything).Return(mockRBACAuthorizer, nil)

		mockESClient.On("RetrieveArchivedReportTypeAndNames", mock.Anything, mock.Anything).Return([]api.ReportTypeAndName{
			{ReportTypeName: reportGetTypeGet.ReportTypeName, ReportName: reportGetTypeGet.ReportName},
			{ReportTypeName: reportGetTypeNoGet.ReportTypeName, ReportName: reportGetTypeNoGet.ReportName},
			{ReportTypeName: reportNoGetTypeNoGet.ReportTypeName, ReportName: reportNoGetTypeNoGet.ReportName},
		}, nil)

		mockESClient.On("RetrieveArchivedReportSummaries", mock.Anything, mock.Anything).Return(&api.ArchivedReportSummaries{
			Count:   3,
			Reports: []*api.ArchivedReportData{reportGetTypeGet, reportGetTypeNoGet, reportNoGetTypeNoGet},
		}, nil)

		mockESFactory.On("ClientForCluster", mock.Anything).Return(mockESClient, nil)

		calicoCli := fake.NewSimpleClientset(&reportTypeGettable, &reportTypeNotGettable)
		mockClientSetFactory.On("ClientSetForCluster", mock.Anything).Return(datastore.NewClientSet(nil, calicoCli.ProjectcalicoV3()), nil)

		t := startTester(mockClientSetFactory, mockESFactory, mockAuthenticator)

		By("Setting responses")

		By("Running a list query")
		t.list(http.StatusOK, []server.Report{
			{
				Id:          reportGetTypeGet.UID(),
				Name:        reportGetTypeGet.ReportName,
				Type:        reportGetTypeGet.ReportTypeName,
				StartTime:   now,
				EndTime:     nowPlusHour,
				UISummary:   map[string]interface{}{"foobar": "hello-100-goodbye"},
				DownloadURL: "/compliance/reports/" + reportGetTypeGet.UID() + "/download",
				DownloadFormats: []server.Format{
					{
						Name:        "boo.csv",
						Description: "This is a boo file",
					},
					{
						Name:        "bar.csv",
						Description: "This is a bar file",
					},
				},
				GenerationTime: now,
			},
			{
				Id:              reportGetTypeNoGet.UID(),
				Name:            reportGetTypeNoGet.ReportName,
				Type:            reportGetTypeNoGet.ReportTypeName,
				StartTime:       now,
				EndTime:         nowPlusHour,
				UISummary:       map[string]interface{}{"foobar": "hello-100-goodbye"},
				DownloadURL:     "",
				DownloadFormats: nil,
				GenerationTime:  now,
			},
		})

		By("Stopping the server")
		t.stop()
	})

	It("returns unauthorized if the user is not authorized to list reports", func() {
		mockAuthenticator.On("Authenticate", mock.Anything).Return(&user.DefaultInfo{}, 0, nil)

		mockRBACAuthorizer := new(lmaauth.MockRBACAuthorizer)
		mockRBACAuthorizer.On("Authorize", mock.Anything, &authzv1.ResourceAttributes{
			Verb: "list", Group: "projectcalico.org", Resource: "globalreports",
		}, mock.Anything).Return(false, nil)

		mockClientSetFactory.On("RBACAuthorizerForCluster", mock.Anything).Return(mockRBACAuthorizer, nil)

		By("Starting a test server")
		t := startTester(mockClientSetFactory, mockESFactory, mockAuthenticator)

		By("Running a list query")
		t.list(http.StatusUnauthorized, nil)

		By("Stopping the server")
		t.stop()
	})

	It("Can handle list queries with no items", func() {
		mockAuthenticator.On("Authenticate", mock.Anything).Return(&user.DefaultInfo{}, 0, nil)

		mockRBACAuthorizer.On("Authorize", mock.Anything, &authzv1.ResourceAttributes{
			Verb: "list", Group: "projectcalico.org", Resource: "globalreports",
		}, mock.Anything).Return(true, nil)
		mockRBACAuthorizer.On("Authorize", mock.Anything, &authzv1.ResourceAttributes{
			Verb: "get", Group: "projectcalico.org", Resource: "globalreports", Name: "somethingelse",
		}, mock.Anything).Return(false, nil)

		mockClientSetFactory.On("RBACAuthorizerForCluster", mock.Anything).Return(mockRBACAuthorizer, nil)

		mockESClient.On("RetrieveArchivedReportTypeAndNames", mock.Anything, mock.Anything).Return([]api.ReportTypeAndName{
			{ReportTypeName: reportNoGetTypeNoGet.ReportTypeName, ReportName: reportNoGetTypeNoGet.ReportName},
		}, nil)

		mockESFactory.On("ClientForCluster", mock.Anything).Return(mockESClient, nil)

		t := startTester(mockClientSetFactory, mockESFactory, mockAuthenticator)

		By("Running a list query")
		t.list(http.StatusOK, []server.Report{})

		By("Stopping the server")
		t.stop()
	})
})

var _ = Describe("List query parameters", func() {
	It("Can parse parameters correctly", func() {
		By("parsing no query params in the URL")
		maxItems := 100
		v, _ := url.ParseQuery("")
		qp, err := server.GetListReportsQueryParams(v)
		Expect(err).NotTo(HaveOccurred())
		Expect(qp).To(Equal(&api.ReportQueryParams{
			Reports:  nil,
			FromTime: "",
			ToTime:   "",
			Page:     0,
			MaxItems: &maxItems,
			SortBy:   []api.ReportSortBy{{Field: "startTime", Ascending: false}, {Field: "reportTypeName", Ascending: true}, {Field: "reportName", Ascending: true}},
		}))

		By("parsing all query params in the URL")
		v, _ = url.ParseQuery("reportTypeName=type1&reportTypeName=type2&reportName=name1&reportName=name2&" +
			"page=2&fromTime=now-2d&toTime=now-4d&maxItems=4&sortBy=endTime&sortBy=reportName/ascending&" +
			"sortBy=reportTypeName/descending")
		maxItems = 4
		qp, err = server.GetListReportsQueryParams(v)
		Expect(err).NotTo(HaveOccurred())
		Expect(qp).To(Equal(&api.ReportQueryParams{
			Reports:  []api.ReportTypeAndName{{ReportTypeName: "type1", ReportName: ""}, {ReportTypeName: "type2", ReportName: ""}, {ReportTypeName: "", ReportName: "name1"}, {ReportTypeName: "", ReportName: "name2"}},
			FromTime: "now-2d",
			ToTime:   "now-4d",
			Page:     2,
			MaxItems: &maxItems,
			SortBy:   []api.ReportSortBy{{Field: "endTime", Ascending: false}, {Field: "reportName", Ascending: true}, {Field: "reportTypeName", Ascending: false}, {Field: "startTime", Ascending: false}},
		}))

		By("parsing maxItems=all with page=0")
		v, _ = url.ParseQuery("maxItems=all&page=0")
		maxItems = 4
		qp, err = server.GetListReportsQueryParams(v)
		Expect(err).NotTo(HaveOccurred())
		Expect(qp).To(Equal(&api.ReportQueryParams{
			Reports:  nil,
			FromTime: "",
			ToTime:   "",
			Page:     0,
			MaxItems: nil,
			SortBy:   []api.ReportSortBy{{Field: "startTime", Ascending: false}, {Field: "reportTypeName", Ascending: true}, {Field: "reportName", Ascending: true}},
		}))
	})

	It("Errors when supplied with invalid query parameters", func() {
		By("parsing an invalid sort name")
		v, _ := url.ParseQuery("sortBy=fred")
		_, err := server.GetListReportsQueryParams(v)
		Expect(err).To(HaveOccurred())

		By("parsing a negative page number")
		v, _ = url.ParseQuery("page=-1")
		_, err = server.GetListReportsQueryParams(v)
		Expect(err).To(HaveOccurred())

		By("parsing an invalid page number")
		v, _ = url.ParseQuery("page=x")
		_, err = server.GetListReportsQueryParams(v)
		Expect(err).To(HaveOccurred())

		By("parsing a negative maxItems")
		v, _ = url.ParseQuery("maxItems=-1")
		_, err = server.GetListReportsQueryParams(v)
		Expect(err).To(HaveOccurred())

		By("parsing an invalid maxItems")
		v, _ = url.ParseQuery("maxItems=x")
		_, err = server.GetListReportsQueryParams(v)
		Expect(err).To(HaveOccurred())

		By("parsing a non-zero page number when maxItems=all")
		v, _ = url.ParseQuery("page=1&maxItems=all")
		_, err = server.GetListReportsQueryParams(v)
		Expect(err).To(HaveOccurred())
	})
})
