// Copyright 2019 Tigera Inc. All rights reserved.

package puller

import (
	"context"
	"errors"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	v32 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	v3 "github.com/tigera/calico-k8sapiserver/pkg/apis/projectcalico/v3"
	v12 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tigera/intrusion-detection/controller/pkg/db"
	"github.com/tigera/intrusion-detection/controller/pkg/mock"
	"github.com/tigera/intrusion-detection/controller/pkg/statser"
	"github.com/tigera/intrusion-detection/controller/pkg/util"
)

var (
	testGlobalThreatFeed = v3.GlobalThreatFeed{
		ObjectMeta: v1.ObjectMeta{
			Name:      "mock",
			Namespace: util.FeedsNamespace,
		},
		Spec: v32.GlobalThreatFeedSpec{
			Content: "IPSet",
			GlobalNetworkSet: &v32.GlobalNetworkSetSync{
				Labels: map[string]string{
					"level": "high",
				},
			},
			Pull: &v32.Pull{
				Period: "12h",
				HTTP: &v32.HTTPPull{
					Format: "NewlineDelimited",
					URL:    "http://mock.feed/v1",
					Headers: []v32.HTTPHeader{
						{
							Name:  "Accept",
							Value: "text/plain",
						},
						{
							Name:  "Key",
							Value: "ELIDED",
						},
						{
							Name: "Config",
							ValueFrom: &v32.HTTPHeaderSource{
								ConfigMapKeyRef: &v12.ConfigMapKeySelector{
									Key: "config",
								},
							},
						},
						{
							Name: "Secret",
							ValueFrom: &v32.HTTPHeaderSource{
								SecretKeyRef: &v12.SecretKeySelector{
									Key: "secret",
								},
							},
						},
						{
							Name:  "Invalid",
							Value: "ghi",
							ValueFrom: &v32.HTTPHeaderSource{
								ConfigMapKeyRef: &v12.ConfigMapKeySelector{
									Key: "config",
								},
								SecretKeyRef: &v12.SecretKeySelector{
									Key: "secret",
								},
							},
						},
						{
							Name: "CM Optional",
							ValueFrom: &v32.HTTPHeaderSource{
								ConfigMapKeyRef: &v12.ConfigMapKeySelector{
									Key:      "invalid",
									Optional: util.BoolPtr(true),
								},
							},
						},
						{
							Name: "Secret Optional",
							ValueFrom: &v32.HTTPHeaderSource{
								SecretKeyRef: &v12.SecretKeySelector{
									Key:      "invalid",
									Optional: util.BoolPtr(true),
								},
							},
						},
					},
				},
			},
		},
	}
	configMapData = map[string]string{
		"config": "abc",
	}
	secretsData = map[string][]byte{
		"secret": []byte("def"),
	}
)

func TestQuery(t *testing.T) {
	g := NewGomegaWithT(t)

	input := db.IPSetSpec{
		"1.2.3.4",
		"5.6.7.8 ",
		"2.0.0.0/8",
		"2.3.4.5/32 ",
		"2000::1",
		"2000::/5",
	}
	expected := db.IPSetSpec{
		"1.2.3.4/32",
		"5.6.7.8/32",
		"2.0.0.0/8",
		"2.3.4.5/32",
		"2000::1/128",
		"2000::/5",
	}

	client := &http.Client{}
	resp := &http.Response{
		StatusCode: 200,
		Body:       ioutil.NopCloser(strings.NewReader(strings.Join([]string(append(input, "# comment", "", " ", "junk", "junk/")), "\n"))),
	}
	client.Transport = &mock.RoundTripper{
		Response: resp,
	}
	s := &mock.Statser{}
	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, client, gns, eip).(*httpPuller)

	go func() {
		err := puller.query(ctx, s, 1, 0)
		g.Expect(err).ShouldNot(HaveOccurred())
	}()

	gn := util.GlobalNetworkSetNameFromThreatFeed(testGlobalThreatFeed.Name)
	g.Eventually(gns.Local).Should(HaveKey(gn))
	g.Eventually(eip.Sets).Should(HaveKey(testGlobalThreatFeed.Name))
	set, ok := gns.Local()[gn]
	g.Expect(ok).Should(BeTrue(), "Received a snapshot")
	g.Expect(set.Spec.Nets).Should(HaveLen(len(expected)))
	for idx, actual := range set.Spec.Nets {
		g.Expect(actual).Should(Equal(expected[idx]))
	}
	dset, ok := eip.Sets()[testGlobalThreatFeed.Name]
	g.Expect(ok).Should(BeTrue(), "Received a snapshot")
	g.Expect(dset).Should(HaveLen(len(expected)))
	for idx, actual := range dset {
		g.Expect(actual).Should(Equal(expected[idx]))
	}

	status := s.Status()
	g.Expect(status.LastSuccessfulSync).Should(Equal(time.Time{}), "Sync was not successful")
	g.Expect(status.LastSuccessfulSearch).Should(Equal(time.Time{}), "Search was not successful")
	g.Expect(status.ErrorConditions).Should(HaveLen(0), "Statser errors were not reported")
}

func TestNewHTTPPuller(t *testing.T) {
	g := NewGomegaWithT(t)

	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, nil, nil).(*httpPuller)

	g.Expect(puller.needsUpdate).Should(BeTrue())
	g.Expect(puller.url).Should(BeNil())
	g.Expect(puller.header).Should(HaveLen(0))
}

func TestQueryHTTPError(t *testing.T) {
	g := NewGomegaWithT(t)

	client := &http.Client{}
	rt := &mock.RoundTripper{
		Error: TemporaryError("mock error"),
	}
	client.Transport = rt

	s := &mock.Statser{}

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, client, gns, eip).(*httpPuller)

	attempts := uint(5)
	go func() {
		err := puller.query(ctx, s, attempts, 0)
		g.Expect(err).Should(HaveOccurred())
	}()

	gn := util.GlobalNetworkSetNameFromThreatFeed(testGlobalThreatFeed.Name)
	g.Consistently(gns.Local).ShouldNot(HaveKey(gn))
	g.Consistently(eip.Sets).ShouldNot(HaveKey(testGlobalThreatFeed.Name))
	g.Expect(rt.Count).Should(Equal(attempts), "Retried max times")

	status := s.Status()
	g.Expect(status.LastSuccessfulSync).Should(Equal(time.Time{}), "Sync was not successful")
	g.Expect(status.LastSuccessfulSearch).Should(Equal(time.Time{}), "Search was not successful")
	g.Expect(status.ErrorConditions).Should(HaveLen(1), "1 error should have been reported")
	g.Expect(status.ErrorConditions[0].Type).Should(Equal(statser.PullFailed), "Error condition type is set correctly")
}

func TestQueryHTTPStatus404(t *testing.T) {
	g := NewGomegaWithT(t)

	client := &http.Client{}
	rt := &mock.RoundTripper{
		Response: &http.Response{
			StatusCode: 404,
		},
	}
	client.Transport = rt

	s := &mock.Statser{}

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, client, gns, eip).(*httpPuller)

	attempts := uint(5)
	go func() {
		err := puller.query(ctx, s, attempts, 0)
		g.Expect(err).Should(HaveOccurred())
	}()

	gn := util.GlobalNetworkSetNameFromThreatFeed(testGlobalThreatFeed.Name)
	g.Consistently(gns.Local).ShouldNot(HaveKey(gn))
	g.Consistently(eip.Sets).ShouldNot(HaveKey(testGlobalThreatFeed.Name))
	g.Expect(rt.Count).Should(Equal(uint(1)), "Does not retry on error 404")

	status := s.Status()
	g.Expect(status.LastSuccessfulSync).Should(Equal(time.Time{}), "Sync was not successful")
	g.Expect(status.LastSuccessfulSearch).Should(Equal(time.Time{}), "Search was not successful")
	g.Expect(status.ErrorConditions).Should(HaveLen(1), "1 error should have been reported")
	g.Expect(status.ErrorConditions[0].Type).Should(Equal(statser.PullFailed), "Error condition type is set correctly")
}

func TestQueryHTTPStatus500(t *testing.T) {
	g := NewGomegaWithT(t)

	client := &http.Client{}
	rt := &mock.RoundTripper{
		Response: &http.Response{
			StatusCode: 500,
		},
	}
	client.Transport = rt

	s := &mock.Statser{}

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, client, gns, eip).(*httpPuller)

	attempts := uint(5)
	go func() {
		err := puller.query(ctx, s, attempts, 0)
		g.Expect(err).Should(HaveOccurred())
	}()

	gn := util.GlobalNetworkSetNameFromThreatFeed(testGlobalThreatFeed.Name)
	g.Consistently(gns.Local).ShouldNot(HaveKey(gn))
	g.Consistently(eip.Sets).ShouldNot(HaveKey(testGlobalThreatFeed.Name))
	g.Expect(rt.Count).Should(Equal(attempts))

	status := s.Status()
	g.Expect(status.LastSuccessfulSync).Should(Equal(time.Time{}), "Sync was not successful")
	g.Expect(status.LastSuccessfulSearch).Should(Equal(time.Time{}), "Search was not successful")
	g.Expect(status.ErrorConditions).Should(HaveLen(1), "1 error should have been reported")
	g.Expect(status.ErrorConditions[0].Type).Should(Equal(statser.PullFailed), "Error condition type is set correctly")
}

func TestNewHTTPPullerWithNilPull(t *testing.T) {
	g := NewGomegaWithT(t)

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	f := testGlobalThreatFeed.DeepCopy()
	f.Spec.Pull = nil

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	g.Expect(func() { _ = puller.query(ctx, &mock.Statser{}, 1, 0) }).Should(Panic())
}

func TestGetStartupDelay(t *testing.T) {
	g := NewGomegaWithT(t)

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{
		Time: time.Now().Add(-time.Hour),
	}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	delay := puller.getStartupDelay(ctx, statser.Status{})

	g.Expect(delay).Should(BeNumerically("~", puller.period-time.Hour, time.Minute))
}

func TestGetStartupDelayWithZeroLastSyncTime(t *testing.T) {
	g := NewGomegaWithT(t)

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	delay := puller.getStartupDelay(ctx, statser.Status{})

	g.Expect(delay).Should(BeNumerically("==", 0))
}

func TestGetStartupDelayWithOlderLastSyncTime(t *testing.T) {
	g := NewGomegaWithT(t)

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{
		Time: time.Now().Add(-24 * time.Hour),
	}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	delay := puller.getStartupDelay(ctx, statser.Status{})

	g.Expect(delay).Should(BeNumerically("==", 0))
}

func TestGetStartupDelayWithRecentLastSyncTime(t *testing.T) {
	g := NewGomegaWithT(t)

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{
		Time: time.Now(),
	}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	delay := puller.getStartupDelay(ctx, statser.Status{})

	g.Expect(delay).Should(BeNumerically("~", puller.period, time.Minute))
}

func TestSetFeedURIAndHeader(t *testing.T) {
	g := NewGomegaWithT(t)

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	err := puller.setFeedURIAndHeader(&testGlobalThreatFeed)
	g.Expect(err).ShouldNot(HaveOccurred())
	g.Expect(puller.needsUpdate).Should(BeFalse(), "Update is no longer needed")
	g.Expect(puller.url.String()).Should(Equal(testGlobalThreatFeed.Spec.Pull.HTTP.URL))
	g.Expect(puller.header.Get("Accept")).Should(Equal("text/plain"))
	g.Expect(puller.header.Get("Key")).Should(Equal("ELIDED"))
	g.Expect(puller.header.Get("Config")).Should(Equal("abc"))
	g.Expect(puller.header.Get("Secret")).Should(Equal("def"))
	g.Expect(puller.header.Get("Invalid")).Should(Equal("ghi"))
}

func TestSetFeedURIAndHeaderWithNilPull(t *testing.T) {
	g := NewGomegaWithT(t)

	f := testGlobalThreatFeed.DeepCopy()

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(f, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	f.Spec.Pull = nil
	g.Expect(func() { _ = puller.setFeedURIAndHeader(f) }).Should(Panic())
}

func TestSetFeedURIAndHeaderWithNilPullHTTP(t *testing.T) {
	g := NewGomegaWithT(t)

	f := testGlobalThreatFeed.DeepCopy()

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(f, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	f.Spec.Pull.HTTP = nil
	g.Expect(func() { _ = puller.setFeedURIAndHeader(f) }).Should(Panic())
}

func TestSetFeedURIAndHeaderWithInvalidURL(t *testing.T) {
	g := NewGomegaWithT(t)

	f := testGlobalThreatFeed.DeepCopy()

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(f, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	f.Spec.Pull.HTTP.URL = ":/"
	err := puller.setFeedURIAndHeader(f)
	g.Expect(err).Should(HaveOccurred())
	g.Expect(puller.needsUpdate).Should(BeTrue(), "Update is needed")
}

func TestSetFeedURIAndHeaderWithConfigMapError(t *testing.T) {
	g := NewGomegaWithT(t)

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData, Error: errors.New("error")}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	err := puller.setFeedURIAndHeader(puller.feed)
	g.Expect(err).Should(HaveOccurred())
	g.Expect(puller.needsUpdate).Should(BeTrue(), "Update is needed")
}

func TestSetFeedURIAndHeaderWithConfigMapOptional(t *testing.T) {
	g := NewGomegaWithT(t)

	f := testGlobalThreatFeed.DeepCopy()
	f.Spec.Pull.HTTP.Headers = []v32.HTTPHeader{
		{
			Name: "Header",
			ValueFrom: &v32.HTTPHeaderSource{
				ConfigMapKeyRef: &v12.ConfigMapKeySelector{
					Key:      "invalid",
					Optional: util.BoolPtr(true),
				},
			},
		}}

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	err := puller.setFeedURIAndHeader(f)
	g.Expect(err).ShouldNot(HaveOccurred())
	g.Expect(puller.header).ShouldNot(HaveKey(f.Spec.Pull.HTTP.Headers[0].Name))
	g.Expect(puller.needsUpdate).Should(BeFalse(), "Update is not needed")
}

func TestSetFeedURIAndHeaderWithConfigMapNotOptional(t *testing.T) {
	g := NewGomegaWithT(t)

	f := testGlobalThreatFeed.DeepCopy()
	f.Spec.Pull.HTTP.Headers = []v32.HTTPHeader{
		{
			Name: "Header",
			ValueFrom: &v32.HTTPHeaderSource{
				ConfigMapKeyRef: &v12.ConfigMapKeySelector{
					Key:      "invalid",
					Optional: util.BoolPtr(false),
				},
			},
		}}

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(f, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	err := puller.setFeedURIAndHeader(f)
	g.Expect(err).Should(HaveOccurred())
	g.Expect(puller.needsUpdate).Should(BeTrue(), "Update is needed")
}

func TestSetFeedURIAndHeaderWithConfigMapOptionalNotSpecified(t *testing.T) {
	g := NewGomegaWithT(t)

	f := testGlobalThreatFeed.DeepCopy()
	f.Spec.Pull.HTTP.Headers = []v32.HTTPHeader{
		{
			Name: "Header",
			ValueFrom: &v32.HTTPHeaderSource{
				ConfigMapKeyRef: &v12.ConfigMapKeySelector{
					Key: "invalid",
				},
			},
		}}

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(f, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	err := puller.setFeedURIAndHeader(f)
	g.Expect(err).Should(HaveOccurred())
	g.Expect(puller.needsUpdate).Should(BeTrue(), "Update is needed")
}

func TestSetFeedURIAndHeaderWithSecretsError(t *testing.T) {
	g := NewGomegaWithT(t)

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData, Error: errors.New("error")}, nil, gns, eip).(*httpPuller)

	err := puller.setFeedURIAndHeader(puller.feed)
	g.Expect(err).Should(HaveOccurred())
	g.Expect(puller.needsUpdate).Should(BeTrue(), "Update is needed")
}

func TestSetFeedURIAndHeaderWithSecretOptional(t *testing.T) {
	g := NewGomegaWithT(t)

	f := testGlobalThreatFeed.DeepCopy()
	f.Spec.Pull.HTTP.Headers = []v32.HTTPHeader{
		{
			Name: "Header",
			ValueFrom: &v32.HTTPHeaderSource{
				SecretKeyRef: &v12.SecretKeySelector{
					Key:      "invalid",
					Optional: util.BoolPtr(true),
				},
			},
		}}

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(f, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	err := puller.setFeedURIAndHeader(f)
	g.Expect(err).ShouldNot(HaveOccurred())
	g.Expect(puller.header).ShouldNot(HaveKey(f.Spec.Pull.HTTP.Headers[0].Name))
}

func TestSetFeedURIAndHeaderWithSecretNotOptional(t *testing.T) {
	g := NewGomegaWithT(t)

	f := testGlobalThreatFeed.DeepCopy()
	f.Spec.Pull.HTTP.Headers = []v32.HTTPHeader{
		{
			Name: "Header",
			ValueFrom: &v32.HTTPHeaderSource{
				SecretKeyRef: &v12.SecretKeySelector{
					Key:      "invalid",
					Optional: util.BoolPtr(false),
				},
			},
		}}

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(f, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	err := puller.setFeedURIAndHeader(f)
	g.Expect(err).Should(HaveOccurred())
	g.Expect(puller.header).ShouldNot(HaveKey(f.Spec.Pull.HTTP.Headers[0].Name))
}

func TestSetFeedURIAndHeaderWithSecretOptionalNotSpecified(t *testing.T) {
	g := NewGomegaWithT(t)

	f := testGlobalThreatFeed.DeepCopy()
	f.Spec.Pull.HTTP.Headers = []v32.HTTPHeader{
		{
			Name: "Header",
			ValueFrom: &v32.HTTPHeaderSource{
				SecretKeyRef: &v12.SecretKeySelector{
					Key: "invalid",
				},
			},
		}}

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(f, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	err := puller.setFeedURIAndHeader(f)
	g.Expect(err).Should(HaveOccurred())
	g.Expect(puller.header).ShouldNot(HaveKey(f.Spec.Pull.HTTP.Headers[0].Name))
}

func TestSetFeedURIAndHeaderWithMissingRefs(t *testing.T) {
	g := NewGomegaWithT(t)

	f := testGlobalThreatFeed.DeepCopy()

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(f, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	f.Spec.Pull.HTTP.Headers[2].ValueFrom.ConfigMapKeyRef = nil
	err := puller.setFeedURIAndHeader(f)
	g.Expect(err).Should(HaveOccurred())
}

func TestSetFeed(t *testing.T) {
	g := NewGomegaWithT(t)

	gns := mock.NewGlobalNetworkSetController()
	eip := mock.NewElasticIPSetController()
	puller := NewHTTPPuller(&testGlobalThreatFeed, &mock.IPSet{}, &mock.ConfigMap{ConfigMapData: configMapData}, &mock.Secrets{SecretsData: secretsData}, nil, gns, eip).(*httpPuller)

	f2 := testGlobalThreatFeed.DeepCopy()
	f2.Name = "set feed"
	f2.Spec.Pull.HTTP.URL = "http://updated"

	puller.SetFeed(f2)
	g.Expect(puller.feed).Should(Equal(f2), "Feed contents should match")
	g.Expect(puller.feed).ShouldNot(BeIdenticalTo(f2), "Feed pointer should not be the same")
	g.Expect(puller.feed.Name).Should(Equal(f2.Name), "Feed name was updated")
	g.Expect(puller.needsUpdate).Should(BeTrue(), "Needs Update must be set")
	g.Expect(puller.url).Should(BeNil(), "Feed URL is still nil")
	g.Expect(puller.header).Should(HaveLen(0), "Header is still empty")
}
