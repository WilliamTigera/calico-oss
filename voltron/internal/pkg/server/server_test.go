// Copyright (c) 2019-2023 Tigera, Inc. All rights reserved.

package server_test

import (
	"context"
	"crypto/md5"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	authnv1 "k8s.io/api/authentication/v1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"golang.org/x/net/http2"

	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/projectcalico/calico/apiserver/pkg/authentication"
	"github.com/projectcalico/calico/lma/pkg/auth"
	"github.com/projectcalico/calico/lma/pkg/auth/testing"
	"github.com/projectcalico/calico/voltron/internal/pkg/bootstrap"
	vcfg "github.com/projectcalico/calico/voltron/internal/pkg/config"
	"github.com/projectcalico/calico/voltron/internal/pkg/proxy"
	"github.com/projectcalico/calico/voltron/internal/pkg/regex"
	"github.com/projectcalico/calico/voltron/internal/pkg/server"
	"github.com/projectcalico/calico/voltron/internal/pkg/server/accesslog"
	accesslogtest "github.com/projectcalico/calico/voltron/internal/pkg/server/accesslog/test"
	"github.com/projectcalico/calico/voltron/internal/pkg/test"
	"github.com/projectcalico/calico/voltron/internal/pkg/utils"
	"github.com/projectcalico/calico/voltron/pkg/tunnel"

	calicov3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	"github.com/tigera/api/pkg/client/clientset_generated/clientset/fake"
	clientv3 "github.com/tigera/api/pkg/client/clientset_generated/clientset/typed/projectcalico/v3"
)

const (
	k8sIssuer           = "kubernetes/serviceaccount"
	managerSAAuthHeader = "Bearer tigera-manager-token"
)

var (
	clusterA = "clusterA"
	clusterB = "clusterB"
	clusterC = "clusterC"
	config   = &rest.Config{BearerToken: "tigera-manager-token"}

	// Tokens issued by k8s.
	janeBearerToken = testing.NewFakeJWT(k8sIssuer, "jane@example.io")
	bobBearerToken  = testing.NewFakeJWT(k8sIssuer, "bob@example.io")

	watchSync chan error
)

func init() {
	log.SetOutput(GinkgoWriter)
	log.SetLevel(log.DebugLevel)
}

type k8sClient struct {
	kubernetes.Interface
	clientv3.ProjectcalicoV3Interface
}

var _ = Describe("Server Proxy to tunnel", func() {
	var (
		fakeK8s *k8sfake.Clientset
		k8sAPI  bootstrap.K8sClient

		voltronTunnelCert      *x509.Certificate
		voltronTunnelTLSCert   tls.Certificate
		voltronTunnelPrivKey   *rsa.PrivateKey
		voltronExtHttpsCert    *x509.Certificate
		voltronExtHttpsPrivKey *rsa.PrivateKey
		voltronIntHttpsCert    *x509.Certificate
		voltronIntHttpsPrivKey *rsa.PrivateKey
		voltronTunnelCAs       *x509.CertPool
		voltronHttpsCAs        *x509.CertPool
	)

	BeforeEach(func() {
		var err error

		fakeK8s = k8sfake.NewSimpleClientset()
		k8sAPI = &k8sClient{
			Interface:                fakeK8s,
			ProjectcalicoV3Interface: fake.NewSimpleClientset().ProjectcalicoV3(),
		}

		voltronTunnelCertTemplate := test.CreateCACertificateTemplate("voltron")
		voltronTunnelPrivKey, voltronTunnelCert, err = test.CreateCertPair(voltronTunnelCertTemplate, nil, nil)
		Expect(err).ShouldNot(HaveOccurred())

		// convert x509 cert to tls cert
		voltronTunnelTLSCert, err = test.X509CertToTLSCert(voltronTunnelCert, voltronTunnelPrivKey)
		Expect(err).NotTo(HaveOccurred())

		voltronExtHttpCertTemplate := test.CreateServerCertificateTemplate("localhost")
		voltronExtHttpsPrivKey, voltronExtHttpsCert, err = test.CreateCertPair(voltronExtHttpCertTemplate, nil, nil)
		Expect(err).ShouldNot(HaveOccurred())

		voltronIntHttpCertTemplate := test.CreateServerCertificateTemplate("tigera-manager.tigera-manager.svc")
		voltronIntHttpsPrivKey, voltronIntHttpsCert, err = test.CreateCertPair(voltronIntHttpCertTemplate, nil, nil)
		Expect(err).ShouldNot(HaveOccurred())

		voltronTunnelCAs = x509.NewCertPool()
		voltronTunnelCAs.AppendCertsFromPEM(test.CertToPemBytes(voltronTunnelCert))

		voltronHttpsCAs = x509.NewCertPool()
		voltronHttpsCAs.AppendCertsFromPEM(test.CertToPemBytes(voltronExtHttpsCert))
		voltronHttpsCAs.AppendCertsFromPEM(test.CertToPemBytes(voltronIntHttpsCert))
	})

	It("should fail to start the server when the paths to the external credentials are invalid", func() {
		mockAuthenticator := new(auth.MockJWTAuth)
		_, err := server.New(
			k8sAPI,
			config,
			vcfg.Config{},
			mockAuthenticator,
			server.WithExternalCredFiles("dog/gopher.crt", "dog/gopher.key"),
			server.WithInternalCredFiles("dog/gopher.crt", "dog/gopher.key"),
		)
		Expect(err).To(HaveOccurred())
	})

	Context("Server is running", func() {
		var (
			httpsAddr, tunnelAddr string
			srvWg                 *sync.WaitGroup
			srv                   *server.Server
			defaultServer         *httptest.Server
			mockAuthorize         *mock.Call
		)

		BeforeEach(func() {
			var err error

			mockAuthenticator := new(auth.MockJWTAuth)
			mockAuthenticator.On("Authenticate", mock.Anything).Return(
				&user.DefaultInfo{
					Name:   "jane@example.io",
					Groups: []string{"developers"},
				}, 0, nil)
			mockAuthorize = mockAuthenticator.On("Authorize", mock.Anything, mock.Anything, mock.Anything).Return(true, nil)

			defaultServer = httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Echo the token, such that we can determine if the auth header was successfully swapped.
					w.Header().Set(authentication.AuthorizationHeader, r.Header.Get(authentication.AuthorizationHeader))
					w.Header().Set(authnv1.ImpersonateUserHeader, r.Header.Get(authnv1.ImpersonateUserHeader))
					w.Header().Set(authnv1.ImpersonateGroupHeader, r.Header.Get(authnv1.ImpersonateGroupHeader))
				}))

			defaultURL, err := url.Parse(defaultServer.URL)
			Expect(err).NotTo(HaveOccurred())

			defaultProxy, e := proxy.New([]proxy.Target{
				{Path: "/", Dest: defaultURL},
				{Path: "/compliance/", Dest: defaultURL},
			})
			Expect(e).NotTo(HaveOccurred())

			tunnelTargetWhitelist, err := regex.CompileRegexStrings([]string{`^/$`, `^/some/path$`})
			Expect(err).ShouldNot(HaveOccurred())

			k8sTargets, err := regex.CompileRegexStrings([]string{`^/api/?`, `^/apis/?`})
			Expect(err).ShouldNot(HaveOccurred())

			srv, httpsAddr, _, tunnelAddr, srvWg = createAndStartServer(k8sAPI,
				config,
				mockAuthenticator,
				server.WithTunnelSigningCreds(voltronTunnelCert),
				server.WithTunnelCert(voltronTunnelTLSCert),
				server.WithExternalCreds(test.CertToPemBytes(voltronExtHttpsCert), test.KeyToPemBytes(voltronExtHttpsPrivKey)),
				server.WithInternalCreds(test.CertToPemBytes(voltronIntHttpsCert), test.KeyToPemBytes(voltronIntHttpsPrivKey)),
				server.WithDefaultProxy(defaultProxy),
				server.WithKubernetesAPITargets(k8sTargets),
				server.WithTunnelTargetWhitelist(tunnelTargetWhitelist),
				server.WithCheckManagedClusterAuthorizationBeforeProxy(true, 0),
			)
		})

		AfterEach(func() {
			Expect(srv.Close()).NotTo(HaveOccurred())
			defaultServer.Close()
			srvWg.Wait()
		})

		Context("Adding / removing managed clusters", func() {
			It("should get an empty list if no managed clusters are registered", func() {
				list, err := k8sAPI.ManagedClusters().List(context.Background(), metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(list.Items).To(HaveLen(0))
			})

			It("should be able to register multiple clusters", func() {
				_, err := k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: clusterA},
				}, metav1.CreateOptions{})

				Expect(err).ShouldNot(HaveOccurred())
				_, err = k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: clusterB},
				}, metav1.CreateOptions{})

				Expect(err).ShouldNot(HaveOccurred())

				list, err := k8sAPI.ManagedClusters().List(context.Background(), metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(list.Items).To(HaveLen(2))
				Expect(list.Items[0].GetObjectMeta().GetName()).To(Equal("clusterA"))
				Expect(list.Items[1].GetObjectMeta().GetName()).To(Equal("clusterB"))
			})

			It("should be able to list the remaining clusters after deleting one", func() {
				By("adding two cluster")
				_, err := k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: clusterA},
				}, metav1.CreateOptions{})
				Expect(err).ShouldNot(HaveOccurred())

				_, err = k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: clusterB},
				}, metav1.CreateOptions{})
				Expect(err).ShouldNot(HaveOccurred())

				list, err := k8sAPI.ManagedClusters().List(context.Background(), metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(list.Items).To(HaveLen(2))
				Expect(list.Items[0].GetObjectMeta().GetName()).To(Equal("clusterA"))
				Expect(list.Items[1].GetObjectMeta().GetName()).To(Equal("clusterB"))

				By("removing one cluster")
				Expect(k8sAPI.ManagedClusters().Delete(context.Background(), clusterB, metav1.DeleteOptions{})).ShouldNot(HaveOccurred())

				list, err = k8sAPI.ManagedClusters().List(context.Background(), metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(list.Items).To(HaveLen(1))
				Expect(list.Items[0].GetObjectMeta().GetName()).To(Equal("clusterA"))
			})

			It("should be able to register clusterB after it's been deleted again", func() {
				_, err := k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: clusterB},
				}, metav1.CreateOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				Expect(k8sAPI.ManagedClusters().Delete(context.Background(), clusterB, metav1.DeleteOptions{})).ShouldNot(HaveOccurred())

				list, err := k8sAPI.ManagedClusters().List(context.Background(), metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(list.Items).To(HaveLen(0))

				_, err = k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: clusterB},
				}, metav1.CreateOptions{})
				Expect(err).ShouldNot(HaveOccurred())

				list, err = k8sAPI.ManagedClusters().List(context.Background(), metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(list.Items).To(HaveLen(1))
			})
		})

		Context("Proxying requests over the tunnel", func() {
			It("should not proxy anywhere without valid headers", func() {
				resp, err := http.Get("http://" + httpsAddr + "/")
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(400))
			})

			It("Should reject requests to clusters that don't exist", func() {
				req, err := http.NewRequest("GET", "http://"+httpsAddr+"/", nil)
				Expect(err).NotTo(HaveOccurred())
				req.Header.Add(utils.ClusterHeaderField, "zzzzzzz")
				resp, err := http.DefaultClient.Do(req)
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(400))
			})

			It("Should not proxy anywhere - multiple headers", func() {
				tr := &http.Transport{
					TLSClientConfig: &tls.Config{
						InsecureSkipVerify: true,
						ServerName:         "localhost",
					},
				}
				client := &http.Client{Transport: tr}
				req, err := http.NewRequest("GET", "https://"+httpsAddr+"/", nil)
				Expect(err).NotTo(HaveOccurred())
				req.Header.Add(utils.ClusterHeaderField, clusterA)
				req.Header.Add(utils.ClusterHeaderField, "helloworld")
				resp, err := client.Do(req)
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(400))
			})

			It("should not be able to proxy to a cluster without a tunnel", func() {
				_, err := k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
					TypeMeta: metav1.TypeMeta{
						Kind:       calicov3.KindManagedCluster,
						APIVersion: calicov3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: clusterA,
					},
				}, metav1.CreateOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				clientHelloReq(httpsAddr, clusterA, 400)
			})

			It("Should proxy to default if no header", func() {
				resp, err := http.Get(defaultServer.URL)
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(200))
			})

			It("Should proxy to default even with header, if request path matches one of bypass tunnel targets", func() {
				req, err := http.NewRequest(
					"GET",
					"https://"+httpsAddr+"/compliance/reports",
					strings.NewReader("HELLO"),
				)
				Expect(err).NotTo(HaveOccurred())
				req.Header.Add(utils.ClusterHeaderField, clusterA)
				req.Header.Set(authentication.AuthorizationHeader, janeBearerToken.BearerTokenHeader())

				httpClient := &http.Client{
					Transport: &http.Transport{
						TLSClientConfig: &tls.Config{
							InsecureSkipVerify: true,
						},
					},
				}
				resp, err := httpClient.Do(req)
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(200))
			})

			It("Should swap the auth header and impersonate the user for requests to k8s (a)api server", func() {
				req, err := http.NewRequest("GET", fmt.Sprintf("https://%s%s", httpsAddr, "/api/v1/namespaces"), nil)
				Expect(err).NotTo(HaveOccurred())
				req.Header.Set(authentication.AuthorizationHeader, janeBearerToken.BearerTokenHeader())

				resp, err := configureHTTPSClient().Do(req)
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(200))
				Expect(resp.Header.Get(authentication.AuthorizationHeader)).To(Equal(managerSAAuthHeader))
				Expect(resp.Header.Get(authnv1.ImpersonateUserHeader)).To(Equal(janeBearerToken.UserName()))
				Expect(resp.Header.Get(authnv1.ImpersonateGroupHeader)).To(Equal("developers"))
			})

			It("should not overwrite impersonation headers if they have already been configured by client", func() {
				req, err := http.NewRequest("GET", fmt.Sprintf("https://%s%s", httpsAddr, "/api/v1/namespaces"), nil)
				Expect(err).NotTo(HaveOccurred())

				impersonatedUser := "impersonated-user"
				impersonatedGroup := "impersonated-group"

				req.Header.Set(authentication.AuthorizationHeader, janeBearerToken.BearerTokenHeader())
				req.Header.Set(authnv1.ImpersonateUserHeader, impersonatedUser)
				req.Header.Set(authnv1.ImpersonateGroupHeader, impersonatedGroup)

				resp, err := configureHTTPSClient().Do(req)
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(200))
				Expect(resp.Header.Get(authentication.AuthorizationHeader)).To(Equal(managerSAAuthHeader))
				Expect(resp.Header.Get(authnv1.ImpersonateUserHeader)).To(Equal(impersonatedUser))
				Expect(resp.Header.Get(authnv1.ImpersonateGroupHeader)).To(Equal(impersonatedGroup))
			})

			Context("A single cluster is registered", func() {
				var clusterATLSCert tls.Certificate

				BeforeEach(func() {
					clusterACertTemplate := test.CreateClientCertificateTemplate(clusterA, "localhost")
					clusterAPrivKey, clusterACert, err := test.CreateCertPair(clusterACertTemplate, voltronTunnelCert, voltronTunnelPrivKey)
					Expect(err).ShouldNot(HaveOccurred())

					_, err = k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:        clusterA,
							Annotations: map[string]string{server.AnnotationActiveCertificateFingerprint: utils.GenerateFingerprint(clusterACert)},
						},
					}, metav1.CreateOptions{})
					Expect(err).ShouldNot(HaveOccurred())

					clusterATLSCert, err = test.X509CertToTLSCert(clusterACert, clusterAPrivKey)
					Expect(err).NotTo(HaveOccurred())
				})

				It("can send requests from the server to the cluster", func() {
					tun, err := tunnel.DialTLS(tunnelAddr, &tls.Config{
						Certificates: []tls.Certificate{clusterATLSCert},
						RootCAs:      voltronTunnelCAs,
					}, 5*time.Second)
					Expect(err).NotTo(HaveOccurred())

					WaitForClusterToConnect(k8sAPI, clusterA)

					cli := &http.Client{
						Transport: &http2.Transport{
							TLSClientConfig: &tls.Config{
								NextProtos: []string{"h2"},
								RootCAs:    voltronHttpsCAs,
								ServerName: "localhost",
							},
						},
					}

					req, err := http.NewRequest("GET", "https://"+httpsAddr+"/some/path", strings.NewReader("HELLO"))
					Expect(err).NotTo(HaveOccurred())

					req.Header[utils.ClusterHeaderField] = []string{clusterA}
					req.Header.Set(authentication.AuthorizationHeader, janeBearerToken.BearerTokenHeader())

					var wg sync.WaitGroup
					wg.Add(1)
					go func() {
						defer GinkgoRecover()
						defer wg.Done()

						_, err := cli.Do(req)
						Expect(err).ShouldNot(HaveOccurred())
					}()

					serve := &http.Server{
						Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							f, ok := w.(http.Flusher)
							Expect(ok).To(BeTrue())

							body, err := io.ReadAll(r.Body)
							Expect(err).ShouldNot(HaveOccurred())

							Expect(string(body)).Should(Equal("HELLO"))

							f.Flush()
						}),
					}

					defer serve.Close()
					go func() {
						defer GinkgoRecover()
						err := serve.Serve(tls.NewListener(tun, &tls.Config{
							Certificates: []tls.Certificate{clusterATLSCert},
							NextProtos:   []string{"h2"},
						}))
						Expect(err).Should(Equal(fmt.Errorf("http: Server closed")))
					}()

					wg.Wait()
				})

				It("should not send requests if not authorized on that managedcluster", func() {
					mockAuthorize.Unset()
					mockAuthorize.On("Authorize", mock.Anything, &authzv1.ResourceAttributes{
						Verb:     "get",
						Group:    "projectcalico.org",
						Version:  "v3",
						Resource: "managedclusters",
						Name:     clusterA,
					}, (*authzv1.NonResourceAttributes)(nil)).Return(false, nil)
					resp := clientHelloReq(httpsAddr, clusterA, http.StatusForbidden)
					bits, err := io.ReadAll(resp.Body)
					Expect(err).ToNot(HaveOccurred())
					Expect(string(bits)).To(Equal("not authorized for managed cluster\n"))
				})

				Context("A second cluster is registered", func() {
					var clusterBTLSCert tls.Certificate
					BeforeEach(func() {
						clusterBCertTemplate := test.CreateClientCertificateTemplate(clusterB, "localhost")
						clusterBPrivKey, clusterBCert, err := test.CreateCertPair(clusterBCertTemplate, voltronTunnelCert, voltronTunnelPrivKey)

						Expect(err).NotTo(HaveOccurred())
						_, err = k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
							ObjectMeta: metav1.ObjectMeta{
								Name:        clusterB,
								Annotations: map[string]string{server.AnnotationActiveCertificateFingerprint: utils.GenerateFingerprint(clusterBCert)},
							},
						}, metav1.CreateOptions{})
						Expect(err).ShouldNot(HaveOccurred())

						clusterBTLSCert, err = test.X509CertToTLSCert(clusterBCert, clusterBPrivKey)
						Expect(err).NotTo(HaveOccurred())
					})

					It("can send requests from the server to the second cluster", func() {
						tun, err := tunnel.DialTLS(tunnelAddr, &tls.Config{
							Certificates: []tls.Certificate{clusterBTLSCert},
							RootCAs:      voltronTunnelCAs,
						}, 5*time.Second)
						Expect(err).NotTo(HaveOccurred())

						WaitForClusterToConnect(k8sAPI, clusterB)

						cli := &http.Client{
							Transport: &http2.Transport{
								TLSClientConfig: &tls.Config{
									NextProtos: []string{"h2"},
									RootCAs:    voltronHttpsCAs,
									ServerName: "localhost",
								},
							},
						}

						req, err := http.NewRequest("GET", "https://"+httpsAddr+"/some/path", strings.NewReader("HELLO"))
						Expect(err).NotTo(HaveOccurred())

						req.Header[utils.ClusterHeaderField] = []string{clusterB}
						req.Header.Set(authentication.AuthorizationHeader, janeBearerToken.BearerTokenHeader())

						var wg sync.WaitGroup
						wg.Add(1)
						go func() {
							defer GinkgoRecover()
							defer wg.Done()

							_, err := cli.Do(req)
							Expect(err).ShouldNot(HaveOccurred())
						}()

						serve := &http.Server{
							Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								f, ok := w.(http.Flusher)
								Expect(ok).To(BeTrue())

								body, err := io.ReadAll(r.Body)
								Expect(err).ShouldNot(HaveOccurred())

								Expect(string(body)).Should(Equal("HELLO"))

								f.Flush()
							}),
						}

						defer serve.Close()
						go func() {
							defer GinkgoRecover()
							err := serve.Serve(tls.NewListener(tun, &tls.Config{
								Certificates: []tls.Certificate{clusterBTLSCert},
								NextProtos:   []string{"h2"},
							}))
							Expect(err).Should(Equal(fmt.Errorf("http: Server closed")))
						}()

						wg.Wait()
					})

					It("should not be possible to open a two tunnels to the same cluster", func() {
						_, err := tunnel.DialTLS(tunnelAddr, &tls.Config{
							Certificates: []tls.Certificate{clusterBTLSCert},
							RootCAs:      voltronTunnelCAs,
						}, 5*time.Second)
						Expect(err).NotTo(HaveOccurred())

						tunB2, err := tunnel.DialTLS(tunnelAddr, &tls.Config{
							Certificates: []tls.Certificate{clusterBTLSCert},
							RootCAs:      voltronTunnelCAs,
						}, 5*time.Second)
						Expect(err).NotTo(HaveOccurred())

						_, err = tunB2.Accept()
						Expect(err).Should(HaveOccurred())
					})
				})

				Context("A third cluster with certificate is registered", func() {
					var clusterCTLSCert tls.Certificate
					BeforeEach(func() {
						clusterCCertTemplate := test.CreateClientCertificateTemplate(clusterC, "localhost")
						clusterCPrivKey, clusterCCert, err := test.CreateCertPair(clusterCCertTemplate, voltronTunnelCert, voltronTunnelPrivKey)
						Expect(err).NotTo(HaveOccurred())

						_, err = k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
							ObjectMeta: metav1.ObjectMeta{
								Name: clusterC,
							},
							Spec: calicov3.ManagedClusterSpec{
								Certificate: test.CertToPemBytes(clusterCCert),
							},
						}, metav1.CreateOptions{})
						Expect(err).ShouldNot(HaveOccurred())

						clusterCTLSCert, err = test.X509CertToTLSCert(clusterCCert, clusterCPrivKey)
						Expect(err).NotTo(HaveOccurred())
					})

					It("can send requests from the server to the third cluster", func() {
						tun, err := tunnel.DialTLS(tunnelAddr, &tls.Config{
							Certificates: []tls.Certificate{clusterCTLSCert},
							RootCAs:      voltronTunnelCAs,
						}, 5*time.Second)
						Expect(err).NotTo(HaveOccurred())

						WaitForClusterToConnect(k8sAPI, clusterC)

						cli := &http.Client{
							Transport: &http2.Transport{
								TLSClientConfig: &tls.Config{
									NextProtos: []string{"h2"},
									RootCAs:    voltronHttpsCAs,
									ServerName: "localhost",
								},
							},
						}

						req, err := http.NewRequest("GET", "https://"+httpsAddr+"/some/path", strings.NewReader("HELLO"))
						Expect(err).NotTo(HaveOccurred())

						req.Header[utils.ClusterHeaderField] = []string{clusterC}
						req.Header.Set(authentication.AuthorizationHeader, janeBearerToken.BearerTokenHeader())

						var wg sync.WaitGroup
						wg.Add(1)
						go func() {
							defer GinkgoRecover()
							defer wg.Done()

							_, err := cli.Do(req)
							Expect(err).ShouldNot(HaveOccurred())
						}()

						serve := &http.Server{
							Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								f, ok := w.(http.Flusher)
								Expect(ok).To(BeTrue())

								body, err := io.ReadAll(r.Body)
								Expect(err).ShouldNot(HaveOccurred())

								Expect(string(body)).Should(Equal("HELLO"))

								f.Flush()
							}),
						}

						defer serve.Close()
						go func() {
							defer GinkgoRecover()
							err := serve.Serve(tls.NewListener(tun, &tls.Config{
								Certificates: []tls.Certificate{clusterCTLSCert},
								NextProtos:   []string{"h2"},
							}))
							Expect(err).Should(Equal(fmt.Errorf("http: Server closed")))
						}()

						wg.Wait()
					})
				})
			})
		})

	})

	Context("with logging, metrics & auth caching enabled", func() {
		var (
			cancelFunc    context.CancelFunc
			srvWg         *sync.WaitGroup
			srv           *server.Server
			defaultServer *httptest.Server
			httpsAddr     string
			internalAddr  string
			tunnelAddr    string

			publicHTTPClient   *http.Client
			internalHTTPClient *http.Client

			defaultProxy  *proxy.Proxy
			k8sTargets    []regexp.Regexp
			accessLogFile *os.File
		)

		const (
			managedCluster1 = "mc-one"
			managedCluster2 = "mc-two"
			authCacheTTL    = 500 * time.Millisecond
		)

		BeforeEach(func() {
			var err error
			var ctx context.Context

			ctx, cancelFunc = context.WithCancel(context.Background())

			accessLogFile, err = os.CreateTemp("", "voltron-access-log")
			Expect(err).ToNot(HaveOccurred())

			authenticator, err := auth.NewJWTAuth(&rest.Config{BearerToken: janeBearerToken.ToString()}, k8sAPI,
				auth.WithTokenReviewCacheTTL(ctx, authCacheTTL),
			)
			Expect(err).NotTo(HaveOccurred())

			testing.SetTokenReviewsReactor(fakeK8s, janeBearerToken, bobBearerToken)
			testing.SetSubjectAccessReviewsReactor(fakeK8s,
				testing.UserPermissions{
					Username: janeBearerToken.UserName(),
					Attrs: []authzv1.ResourceAttributes{
						{
							Verb:     "get",
							Group:    "projectcalico.org",
							Version:  "v3",
							Resource: "managedclusters",
							Name:     managedCluster1,
						},
						{
							Verb:     "get",
							Group:    "projectcalico.org",
							Version:  "v3",
							Resource: "managedclusters",
							Name:     managedCluster2,
						},
					},
				},
				testing.UserPermissions{
					Username: bobBearerToken.UserName(),
					Attrs: []authzv1.ResourceAttributes{
						{
							Verb:     "get",
							Group:    "projectcalico.org",
							Version:  "v3",
							Resource: "managedclusters",
							Name:     managedCluster1,
						},
					},
				},
			)

			defaultServer = httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					log.Info("request received, path=", r.URL.Path)
					http.Error(w, "an error occurred", http.StatusBadGateway)
				}))

			defaultURL, err := url.Parse(defaultServer.URL)
			Expect(err).NotTo(HaveOccurred())

			defaultProxy, err = proxy.New([]proxy.Target{
				{Path: "/", Dest: defaultURL},
				{Path: "/metrics", Dest: defaultURL},
			})
			Expect(err).NotTo(HaveOccurred())

			tunnelTargetWhitelist, err := regex.CompileRegexStrings([]string{`^/$`, `^/some/path$`})
			Expect(err).ShouldNot(HaveOccurred())

			srv, httpsAddr, internalAddr, tunnelAddr, srvWg = createAndStartServer(k8sAPI,
				config,
				authenticator,
				server.WithTunnelSigningCreds(voltronTunnelCert),
				server.WithTunnelCert(voltronTunnelTLSCert),
				server.WithExternalCreds(test.CertToPemBytes(voltronExtHttpsCert), test.KeyToPemBytes(voltronExtHttpsPrivKey)),
				server.WithInternalCreds(test.CertToPemBytes(voltronIntHttpsCert), test.KeyToPemBytes(voltronIntHttpsPrivKey)),
				server.WithDefaultProxy(defaultProxy),
				server.WithKubernetesAPITargets(k8sTargets),
				server.WithUnauthenticatedTargets([]string{"/metrics"}), // we want /metrics on the public server to reach the defaultProxy
				server.WithTunnelTargetWhitelist(tunnelTargetWhitelist),
				server.WithCheckManagedClusterAuthorizationBeforeProxy(true, authCacheTTL),
				server.WithInternalMetricsEndpointEnabled(true),
				server.WithHTTPAccessLogging(
					accesslog.WithPath(accessLogFile.Name()),
					accesslog.WithRequestHeader(server.ClusterHeaderFieldCanon, "xClusterID"),
					accesslog.WithStandardJWTClaims(),
					accesslog.WithStringJWTClaim("email", "username"),
					accesslog.WithStringArrayJWTClaim("groups", "groups"),
					accesslog.WithErrorResponseBodyCaptureSize(250),
				),
			)

			publicHTTPClient = &http.Client{
				Transport: &http2.Transport{
					TLSClientConfig: &tls.Config{
						NextProtos: []string{"h2"},
						RootCAs:    voltronHttpsCAs,
						ServerName: "localhost",
					},
				},
			}
			internalHTTPClient = &http.Client{
				Transport: &http2.Transport{
					TLSClientConfig: &tls.Config{
						NextProtos: []string{"h2"},
						RootCAs:    voltronHttpsCAs,
						ServerName: "tigera-manager.tigera-manager.svc",
					},
				},
			}
		})

		AfterEach(func() {
			cancelFunc()
			Expect(srv.Close()).NotTo(HaveOccurred())
			defaultServer.Close()
			srvWg.Wait()
		})

		scrapeCacheMetrics := func() []string {
			resp, err := internalHTTPClient.Get("https://" + internalAddr + "/metrics")
			Expect(err).ToNot(HaveOccurred())
			respBody, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			lines := strings.Split(string(respBody), "\n")
			var result []string
			for _, line := range lines {
				if strings.HasPrefix(line, "tigera_cache") {
					result = append(result, line)
				}
			}
			return result
		}

		It("should write access logs", func() {
			req, err := http.NewRequest("GET", "https://"+httpsAddr+"/?foo=bar", nil)
			Expect(err).NotTo(HaveOccurred())
			req.Header.Set(authentication.AuthorizationHeader, janeBearerToken.BearerTokenHeader())
			req.Header.Set(server.ClusterHeaderFieldCanon, "tigera-labs")
			resp, err := publicHTTPClient.Do(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))

			// as the access log is written after the http response was written, we may get here before the logs are written, so wait for them to appear
			log.Info("before sync")
			Eventually(func() bool {
				srv.FlushAccessLogs()
				info, _ := accessLogFile.Stat()
				return info.Size() > 0
			}).Should(BeTrue())
			log.Info("after sync")

			logMessage, err := accesslogtest.ReadLastAccessLog(accessLogFile)
			Expect(err).ToNot(HaveOccurred())
			Expect(logMessage.Response.Status).To(Equal(http.StatusBadRequest))
			Expect(logMessage.Response.BytesWritten).To(Equal(95))
			Expect(logMessage.Response.Body).To(ContainSubstring("Cluster with ID tigera-labs not found"))
			Expect(logMessage.Request.Method).To(Equal(http.MethodGet))
			Expect(logMessage.Request.Host).To(Equal(httpsAddr))
			Expect(logMessage.Request.Path).To(Equal("/"))
			Expect(logMessage.Request.Query).To(Equal("foo=bar"))
			Expect(logMessage.Request.ClusterID).To(Equal("tigera-labs"))
			Expect(logMessage.Request.Auth.Iss).To(Equal(k8sIssuer))
			Expect(logMessage.Request.Auth.Sub).To(Equal("jane@example.io"))
			Expect(logMessage.Request.Auth.Username).To(Equal("jane@example.io"))
			Expect(logMessage.Request.Auth.Groups).To(Equal([]string{"system:authenticated"}))
			Expect(logMessage.TLS.ServerName).To(Equal("localhost"))
			Expect(logMessage.TLS.CipherSuite).To(Equal("TLS_AES_128_GCM_SHA256"))
		})

		It("metrics should not be available on the public addr", func() {
			resp, err := publicHTTPClient.Get("https://" + httpsAddr + "/metrics")
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusBadGateway))
		})

		It("should cache token requests", func() {

			closeCluster1 := createAndStartManagedCluster(managedCluster1, tunnelAddr, voltronTunnelCAs, voltronTunnelCert, voltronTunnelPrivKey, k8sAPI, newEchoHandler(managedCluster1))
			closeCluster2 := createAndStartManagedCluster(managedCluster2, tunnelAddr, voltronTunnelCAs, voltronTunnelCert, voltronTunnelPrivKey, k8sAPI, newEchoHandler(managedCluster2))
			defer closeCluster1()
			defer closeCluster2()

			doHttpRequest := func(fakeJWT *testing.FakeJWT, clusterName string) {
				req, err := http.NewRequest(http.MethodPost, "https://"+httpsAddr+"/some/path", strings.NewReader("foo"))
				Expect(err).ToNot(HaveOccurred())
				req.Header.Set(utils.ClusterHeaderField, clusterName)
				req.Header.Set(authentication.AuthorizationHeader, fakeJWT.BearerTokenHeader())

				resp, err := publicHTTPClient.Do(req)
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(http.StatusOK))
				Expect(resp.Header.Get("x-echoed-by")).To(Equal(clusterName))
				Expect(resp.Header.Get(authnv1.ImpersonateUserHeader)).To(Equal(fakeJWT.UserName()))
				respBody, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())
				Expect(string(respBody)).To(Equal("foo"))
			}

			Expect(scrapeCacheMetrics()).To(BeEmpty())

			type authCacheMetrics struct {
				AuthnHits   int
				AuthnMisses int
				AuthzHits   int
				AuthzMisses int
			}
			scrapeAuthCacheMetrics := func() authCacheMetrics {
				metrics := scrapeCacheMetrics()
				metricExtractor := regexp.MustCompile(`.* (\d+)$`)
				metricValue := func(prefix string) int {
					for _, metric := range metrics {
						if strings.HasPrefix(metric, prefix) {
							valueStr := metricExtractor.FindStringSubmatch(metric)[1]
							value, err := strconv.Atoi(valueStr)
							if err != nil {
								return -2
							} else {
								return value
							}
						}
					}
					return -1
				}
				return authCacheMetrics{
					AuthnHits:   metricValue("tigera_cache_hits_total{cacheName=\"lma-token-reviewer\"}"),
					AuthnMisses: metricValue("tigera_cache_misses_total{cacheName=\"lma-token-reviewer\"}"),
					AuthzHits:   metricValue("tigera_cache_hits_total{cacheName=\"managedcluster-access-authorizer\"}"),
					AuthzMisses: metricValue("tigera_cache_misses_total{cacheName=\"managedcluster-access-authorizer\"}"),
				}
			}
			expectMetrics := func(exp authCacheMetrics) {
				metrics := scrapeCacheMetrics()
				if exp.AuthnHits > 0 {
					Expect(metrics).To(ContainElement(fmt.Sprintf("tigera_cache_hits_total{cacheName=\"lma-token-reviewer\"} %d", exp.AuthnHits)))
				}
				if exp.AuthnMisses > 0 {
					Expect(metrics).To(ContainElement(fmt.Sprintf("tigera_cache_misses_total{cacheName=\"lma-token-reviewer\"} %d", exp.AuthnMisses)))
				}
				if exp.AuthzHits > 0 {
					Expect(metrics).To(ContainElement(fmt.Sprintf("tigera_cache_hits_total{cacheName=\"managedcluster-access-authorizer\"} %d", exp.AuthzHits)))
				}
				if exp.AuthzMisses > 0 {
					Expect(metrics).To(ContainElement(fmt.Sprintf("tigera_cache_misses_total{cacheName=\"managedcluster-access-authorizer\"} %d", exp.AuthzMisses)))
				}
			}

			By("making the first request", func() {
				doHttpRequest(janeBearerToken, managedCluster1)
				expectMetrics(authCacheMetrics{AuthnHits: 0, AuthnMisses: 1, AuthzHits: 0, AuthzMisses: 1})
			})

			By("making a second request for the same user & cluster", func() {
				doHttpRequest(janeBearerToken, managedCluster1)
				expectMetrics(authCacheMetrics{AuthnHits: 1, AuthnMisses: 1, AuthzHits: 1, AuthzMisses: 1})
			})

			By("making a third request for the same user & cluster", func() {
				doHttpRequest(janeBearerToken, managedCluster1)
				expectMetrics(authCacheMetrics{AuthnHits: 2, AuthnMisses: 1, AuthzHits: 2, AuthzMisses: 1})
			})

			By("making a request for a different user", func() {
				doHttpRequest(bobBearerToken, managedCluster1)
				expectMetrics(authCacheMetrics{AuthnHits: 2, AuthnMisses: 2, AuthzHits: 2, AuthzMisses: 2})
			})

			By("making a request for a different cluster", func() {
				doHttpRequest(janeBearerToken, managedCluster2)
				expectMetrics(authCacheMetrics{AuthnHits: 3, AuthnMisses: 2, AuthzHits: 2, AuthzMisses: 3})
			})

			By("repeatedly requesting the same value will eventually increase misses due to cache expiry", func() {
				type result struct { // only interested in misses
					authnMisses int
					authzMisses int
				}
				Eventually(func() result {
					doHttpRequest(janeBearerToken, managedCluster1)
					metrics := scrapeAuthCacheMetrics()
					return result{
						authnMisses: metrics.AuthnMisses,
						authzMisses: metrics.AuthzMisses,
					}
				}, 3*authCacheTTL, 100*time.Millisecond).Should(Equal(result{authnMisses: 3, authzMisses: 4}))

				Eventually(func() result {
					doHttpRequest(janeBearerToken, managedCluster1)
					metrics := scrapeAuthCacheMetrics()
					return result{
						authnMisses: metrics.AuthnMisses,
						authzMisses: metrics.AuthzMisses,
					}
				}, 3*authCacheTTL, 100*time.Millisecond).Should(Equal(result{authnMisses: 4, authzMisses: 5}))
			})

		})
	})

	Context("auth cache TTLs are configured above the maximum permitted", func() {
		k8sAPI = &k8sClient{
			Interface: k8sfake.NewSimpleClientset(),
		}

		It("creating an authenticator should fail when TokenReviewCacheTTL is too large", func() {
			_, err := auth.NewJWTAuth(&rest.Config{BearerToken: janeBearerToken.ToString()}, k8sAPI,
				auth.WithTokenReviewCacheTTL(context.Background(), auth.TokenReviewCacheMaxTTL+time.Second),
			)
			Expect(err).To(MatchError(MatchRegexp("configured cacheTTL of 21s exceeds maximum permitted of 20s")))
		})

		It("creating a server should fail when CheckManagedClusterAuthorizationBeforeProxyTTL is too large", func() {
			authenticator, err := auth.NewJWTAuth(&rest.Config{BearerToken: janeBearerToken.ToString()}, k8sAPI,
				auth.WithTokenReviewCacheTTL(context.Background(), auth.TokenReviewCacheMaxTTL),
			)
			Expect(err).NotTo(HaveOccurred())

			_, err = server.New(k8sAPI, config, vcfg.Config{}, authenticator,
				server.WithCheckManagedClusterAuthorizationBeforeProxy(true, 42*time.Second),
			)
			Expect(err).To(MatchError(MatchRegexp("configured cacheTTL of 42s exceeds maximum permitted of 20s")))
		})

	})

	Context("A managed cluster connects to voltron and the current active fingerprint is in the md5 format", func() {
		var (
			tunnelAddr    string
			srvWg         *sync.WaitGroup
			srv           *server.Server
			defaultServer *httptest.Server

			defaultProxy          *proxy.Proxy
			k8sTargets            []regexp.Regexp
			mockAuthenticator     *auth.MockJWTAuth
			tunnelTargetWhitelist []regexp.Regexp
		)

		BeforeEach(func() {
			var err error

			mockAuthenticator = new(auth.MockJWTAuth)
			mockAuthenticator.On("Authenticate", mock.Anything).Return(
				&user.DefaultInfo{
					Name:   "jane@example.io",
					Groups: []string{"developers"},
				}, 0, nil)

			defaultServer = httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Echo the token, such that we can determine if the auth header was successfully swapped.
					w.Header().Set(authentication.AuthorizationHeader, r.Header.Get(authentication.AuthorizationHeader))
				}))

			defaultURL, err := url.Parse(defaultServer.URL)
			Expect(err).NotTo(HaveOccurred())

			defaultProxy, err = proxy.New([]proxy.Target{
				{Path: "/", Dest: defaultURL},
				{Path: "/compliance/", Dest: defaultURL},
			})
			Expect(err).NotTo(HaveOccurred())

			tunnelTargetWhitelist, err = regex.CompileRegexStrings([]string{`^/$`, `^/some/path$`})
			Expect(err).ShouldNot(HaveOccurred())

			k8sTargets, err = regex.CompileRegexStrings([]string{`^/api/?`, `^/apis/?`})
			Expect(err).ShouldNot(HaveOccurred())

			watchSync = make(chan error)
			srv, _, _, tunnelAddr, srvWg = createAndStartServer(k8sAPI,
				config,
				mockAuthenticator,
				server.WithTunnelSigningCreds(voltronTunnelCert),
				server.WithTunnelCert(voltronTunnelTLSCert),
				server.WithExternalCreds(test.CertToPemBytes(voltronExtHttpsCert), test.KeyToPemBytes(voltronExtHttpsPrivKey)),
				server.WithInternalCreds(test.CertToPemBytes(voltronIntHttpsCert), test.KeyToPemBytes(voltronIntHttpsPrivKey)),
				server.WithDefaultProxy(defaultProxy),
				server.WithKubernetesAPITargets(k8sTargets),
				server.WithTunnelTargetWhitelist(tunnelTargetWhitelist),
			)
		})

		AfterEach(func() {
			Expect(srv.Close()).NotTo(HaveOccurred())
			defaultServer.Close()
			srvWg.Wait()
		})

		When("the connecting clusters fingerprint matches the md5 active fingerprint in non-FIPS mode", func() {
			It("upgrades the active fingerprint to sha256", func() {
				certTemplate := test.CreateClientCertificateTemplate(clusterA, "localhost")
				privKey, cert, err := test.CreateCertPair(certTemplate, voltronTunnelCert, voltronTunnelPrivKey)
				Expect(err).NotTo(HaveOccurred())

				_, err = k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: clusterA,
						Annotations: map[string]string{
							server.AnnotationActiveCertificateFingerprint: fmt.Sprintf("%x", md5.Sum(cert.Raw)), // old md5 sum
						},
					},
				}, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				list, err := k8sAPI.ManagedClusters().List(context.Background(), metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(list.Items).To(HaveLen(1))

				tlsCert, err := test.X509CertToTLSCert(cert, privKey)
				Expect(err).NotTo(HaveOccurred())

				t, err := tunnel.DialTLS(tunnelAddr, &tls.Config{
					Certificates: []tls.Certificate{tlsCert},
					RootCAs:      voltronTunnelCAs,
					ServerName:   "voltron",
				}, 5*time.Second)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() string {
					mc, err := k8sAPI.ManagedClusters().Get(context.Background(), clusterA, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					return mc.Annotations[server.AnnotationActiveCertificateFingerprint]
				}, 3*time.Second, 500*time.Millisecond).Should(Equal(utils.GenerateFingerprint(cert))) // new sha256 sum

				Expect(t.Close()).NotTo(HaveOccurred())
			})
		})

		When("the connecting clusters fingerprint matches the md5 active fingerprint in FIPS mode", func() {
			It("doesn't modify the existing md5 active fingerprint", func() {
				// recreate server to enable fips mode
				srv, _, _, tunnelAddr, srvWg = createAndStartServer(k8sAPI,
					config,
					mockAuthenticator,
					server.WithTunnelSigningCreds(voltronTunnelCert),
					server.WithTunnelCert(voltronTunnelTLSCert),
					server.WithExternalCreds(test.CertToPemBytes(voltronExtHttpsCert), test.KeyToPemBytes(voltronExtHttpsPrivKey)),
					server.WithInternalCreds(test.CertToPemBytes(voltronIntHttpsCert), test.KeyToPemBytes(voltronIntHttpsPrivKey)),
					server.WithDefaultProxy(defaultProxy),
					server.WithKubernetesAPITargets(k8sTargets),
					server.WithTunnelTargetWhitelist(tunnelTargetWhitelist),
					server.WithFIPSModeEnabled(true),
				)

				certTemplate := test.CreateClientCertificateTemplate(clusterA, "localhost")
				privKey, cert, err := test.CreateCertPair(certTemplate, voltronTunnelCert, voltronTunnelPrivKey)
				Expect(err).NotTo(HaveOccurred())

				fingerprintMD5 := fmt.Sprintf("%x", md5.Sum(cert.Raw))
				_, err = k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: clusterA,
						Annotations: map[string]string{
							server.AnnotationActiveCertificateFingerprint: fingerprintMD5, // old md5 sum
						},
					},
				}, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				list, err := k8sAPI.ManagedClusters().List(context.Background(), metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(list.Items).To(HaveLen(1))

				tlsCert, err := test.X509CertToTLSCert(cert, privKey)
				Expect(err).NotTo(HaveOccurred())

				t, err := tunnel.DialTLS(tunnelAddr, &tls.Config{
					Certificates: []tls.Certificate{tlsCert},
					RootCAs:      voltronTunnelCAs,
					ServerName:   "voltron",
				}, 5*time.Second)
				Expect(err).NotTo(HaveOccurred())

				// wait for one cycle of clusters.watchK8sFrom() to complete
				Expect(<-watchSync).NotTo(HaveOccurred())

				mc, err := k8sAPI.ManagedClusters().Get(context.Background(), clusterA, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(mc.Annotations[server.AnnotationActiveCertificateFingerprint]).To(Equal(fingerprintMD5)) // old md5 sum

				Expect(t.Close()).NotTo(HaveOccurred())
			})
		})

		When("the connecting clusters fingerprint doesn't match the md5 active fingerprint", func() {
			It("doesn't modify the existing md5 active fingerprint", func() {
				certTemplate := test.CreateClientCertificateTemplate(clusterB, "localhost")
				privKey, cert, err := test.CreateCertPair(certTemplate, voltronTunnelCert, voltronTunnelPrivKey)
				Expect(err).NotTo(HaveOccurred())

				_, err = k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: clusterB,
						Annotations: map[string]string{
							server.AnnotationActiveCertificateFingerprint: "md5-sum-can-not-be-matched",
						},
					},
				}, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				list, err := k8sAPI.ManagedClusters().List(context.Background(), metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(list.Items).To(HaveLen(1))

				tlsCert, err := test.X509CertToTLSCert(cert, privKey)
				Expect(err).NotTo(HaveOccurred())

				t, err := tunnel.DialTLS(tunnelAddr, &tls.Config{
					Certificates: []tls.Certificate{tlsCert},
					RootCAs:      voltronTunnelCAs,
					ServerName:   "voltron",
				}, 5*time.Second)
				Expect(err).NotTo(HaveOccurred())

				// wait for one cycle of clusters.watchK8sFrom() to complete
				Expect(<-watchSync).NotTo(HaveOccurred())

				mc, err := k8sAPI.ManagedClusters().Get(context.Background(), clusterB, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(mc.Annotations[server.AnnotationActiveCertificateFingerprint]).To(Equal("md5-sum-can-not-be-matched"))

				Expect(t.Close()).NotTo(HaveOccurred())
			})
		})
	})

	Context("Voltron tunnel configured with tls certificate with invalid Key Extension", func() {
		var (
			wg            *sync.WaitGroup
			srv           *server.Server
			tunnelAddr    string
			defaultServer *httptest.Server
		)

		BeforeEach(func() {
			mockAuthenticator := new(auth.MockJWTAuth)
			mockAuthenticator.On("Authenticate", mock.Anything).Return(
				&user.DefaultInfo{
					Name:   "jane@example.io",
					Groups: []string{"developers"},
				}, 0, nil)
			defaultServer = httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			defaultURL, err := url.Parse(defaultServer.URL)
			Expect(err).NotTo(HaveOccurred())

			defaultProxy, e := proxy.New([]proxy.Target{
				{Path: "/", Dest: defaultURL},
				{Path: "/compliance/", Dest: defaultURL},
				{Path: "/api/v1/namespaces", Dest: defaultURL},
			})
			Expect(e).NotTo(HaveOccurred())

			tunnelTargetWhitelist, _ := regex.CompileRegexStrings([]string{
				`^/$`,
				`^/some/path$`,
			})

			// Recreate the voltron certificate specifying client auth key usage
			voltronTunnelCertTemplate := test.CreateCACertificateTemplate("voltron")
			voltronTunnelCertTemplate.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}

			voltronTunnelPrivKey, voltronTunnelCert, err = test.CreateCertPair(voltronTunnelCertTemplate, nil, nil)
			Expect(err).ShouldNot(HaveOccurred())

			// convert x509 cert to tls cert
			voltronTunnelTLSCert, err = test.X509CertToTLSCert(voltronTunnelCert, voltronTunnelPrivKey)
			Expect(err).NotTo(HaveOccurred())

			voltronTunnelCAs = x509.NewCertPool()
			voltronTunnelCAs.AppendCertsFromPEM(test.CertToPemBytes(voltronTunnelCert))

			srv, _, _, tunnelAddr, wg = createAndStartServer(
				k8sAPI,
				config,
				mockAuthenticator,
				server.WithTunnelSigningCreds(voltronTunnelCert),
				server.WithTunnelCert(voltronTunnelTLSCert),
				server.WithDefaultProxy(defaultProxy),
				server.WithTunnelTargetWhitelist(tunnelTargetWhitelist),
				server.WithInternalCreds(test.CertToPemBytes(voltronIntHttpsCert), test.KeyToPemBytes(voltronIntHttpsPrivKey)),
				server.WithExternalCreds(test.CertToPemBytes(voltronExtHttpsCert), test.KeyToPemBytes(voltronExtHttpsPrivKey)),
			)

			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			Expect(srv.Close()).NotTo(HaveOccurred())
			wg.Wait()
		})

		It("server with invalid key types will not accept connections", func() {
			var err error

			certTemplate := test.CreateClientCertificateTemplate(clusterA, "localhost")
			privKey, cert, err := test.CreateCertPair(certTemplate, voltronTunnelCert, voltronTunnelPrivKey)
			Expect(err).ShouldNot(HaveOccurred())

			By("adding ClusterA")
			_, err = k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        clusterA,
					Annotations: map[string]string{server.AnnotationActiveCertificateFingerprint: utils.GenerateFingerprint(cert)},
				},
			}, metav1.CreateOptions{})
			Expect(err).ShouldNot(HaveOccurred())
			list, err := k8sAPI.ManagedClusters().List(context.Background(), metav1.ListOptions{})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(list.Items).To(HaveLen(1))

			// Try to connect clusterA to the new fake voltron, should fail
			tlsCert, err := test.X509CertToTLSCert(cert, privKey)
			Expect(err).NotTo(HaveOccurred())

			_, err = tunnel.DialTLS(tunnelAddr, &tls.Config{
				Certificates: []tls.Certificate{tlsCert},
				RootCAs:      voltronTunnelCAs,
				ServerName:   "voltron",
			}, 5*time.Second)
			Expect(err).Should(MatchError("tcp.tls.Dial failed: tls: failed to verify certificate: x509: certificate specifies an incompatible key usage"))
		})
	})
})

func configureHTTPSClient() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				NextProtos:         []string{"h2"},
			},
		},
	}
}

func clientHelloReq(addr string, target string, expectStatus int) (resp *http.Response) {
	Eventually(func() error {
		req, err := http.NewRequest("GET", "https://"+addr+"/some/path", strings.NewReader("HELLO"))
		Expect(err).NotTo(HaveOccurred())

		req.Header[utils.ClusterHeaderField] = []string{target}
		req.Header.Set(authentication.AuthorizationHeader, janeBearerToken.BearerTokenHeader())
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				ServerName:         "localhost",
			},
		}
		client := &http.Client{Transport: tr}
		resp, err = client.Do(req)
		if err != nil || resp.StatusCode != expectStatus {
			return fmt.Errorf("err=%v status=%d expectedStatus=%d", err, resp.StatusCode, expectStatus)
		}
		return nil
	}, 2*time.Second, 400*time.Millisecond).ShouldNot(HaveOccurred())
	return
}

func createAndStartServer(k8sAPI bootstrap.K8sClient, config *rest.Config, authenticator auth.JWTAuth,
	options ...server.Option,
) (*server.Server, string, string, string, *sync.WaitGroup) {
	srv, err := server.New(k8sAPI, config, vcfg.Config{}, authenticator, options...)
	Expect(err).ShouldNot(HaveOccurred())

	lisHTTPS, err := net.Listen("tcp", "localhost:0")
	Expect(err).NotTo(HaveOccurred())

	lisInternalHTTPS, err := net.Listen("tcp", "localhost:0")
	Expect(err).NotTo(HaveOccurred())

	lisTun, err := net.Listen("tcp", "localhost:0")
	Expect(err).NotTo(HaveOccurred())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.ServeHTTPS(lisHTTPS, "", "")
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.ServeInternalHTTPS(lisInternalHTTPS, "", "")
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.ServeTunnelsTLS(lisTun)
	}()

	go func() {
		_ = srv.WatchK8sWithSync(watchSync)
	}()

	return srv, lisHTTPS.Addr().String(), lisInternalHTTPS.Addr().String(), lisTun.Addr().String(), &wg
}

func WaitForClusterToConnect(k8sAPI bootstrap.K8sClient, clusterName string) {
	Eventually(func() calicov3.ManagedClusterStatus {
		managedCluster, err := k8sAPI.ManagedClusters().Get(context.Background(), clusterName, metav1.GetOptions{})
		Expect(err).ShouldNot(HaveOccurred())
		return managedCluster.Status
	}, 5*time.Second, 100*time.Millisecond).Should(Equal(calicov3.ManagedClusterStatus{
		Conditions: []calicov3.ManagedClusterStatusCondition{
			{Status: calicov3.ManagedClusterStatusValueTrue, Type: calicov3.ManagedClusterStatusTypeConnected},
		},
	}))
}

func createAndStartManagedCluster(
	clusterName string,
	tunnelAddr string,
	tunnelCA *x509.CertPool,
	voltronTunnelCert *x509.Certificate,
	voltronTunnelPrivKey *rsa.PrivateKey,
	k8sAPI bootstrap.K8sClient,
	handler http.Handler,
) (closer func()) {

	certTemplate := test.CreateClientCertificateTemplate(clusterName, "localhost")
	clusterKey, clusterCert, err := test.CreateCertPair(certTemplate, voltronTunnelCert, voltronTunnelPrivKey)
	Expect(err).ShouldNot(HaveOccurred())

	_, err = k8sAPI.ManagedClusters().Create(context.Background(), &calicov3.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        clusterName,
			Annotations: map[string]string{server.AnnotationActiveCertificateFingerprint: utils.GenerateFingerprint(clusterCert)},
		},
	}, metav1.CreateOptions{})
	Expect(err).ShouldNot(HaveOccurred())

	time.Sleep(2 * time.Second)

	tlsCert, err := test.X509CertToTLSCert(clusterCert, clusterKey)
	Expect(err).NotTo(HaveOccurred())

	tun, err := tunnel.DialTLS(tunnelAddr, &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		RootCAs:      tunnelCA,
	}, 5*time.Second)
	Expect(err).NotTo(HaveOccurred())

	WaitForClusterToConnect(k8sAPI, clusterName)

	httpServer := &http.Server{
		Handler: handler,
	}

	go func() {
		defer GinkgoRecover()
		err := httpServer.Serve(tls.NewListener(tun, &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			NextProtos:   []string{"h2"},
		}))
		Expect(err).Should(Equal(fmt.Errorf("http: Server closed")))
	}()

	return func() {
		Expect(httpServer.Close()).NotTo(HaveOccurred())
	}
}

func newEchoHandler(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqBody, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

		copyHeaders := func(name string) {
			if v := r.Header[name]; len(v) > 0 {
				w.Header()[name] = v
			}
		}

		w.Header().Set("x-echoed-by", name)
		copyHeaders(authnv1.ImpersonateUserHeader)
		copyHeaders(authnv1.ImpersonateGroupHeader)

		_, _ = w.Write(reqBody)
	})

}
