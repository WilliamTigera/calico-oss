// Copyright (c) 2019 Tigera, Inc. All rights reserved.

package server_test

import (
	"crypto"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"golang.org/x/crypto/ssh"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/rest"

	"golang.org/x/net/http2"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"

	"github.com/tigera/voltron/internal/pkg/clusters"
	"github.com/tigera/voltron/internal/pkg/proxy"
	"github.com/tigera/voltron/internal/pkg/regex"
	"github.com/tigera/voltron/internal/pkg/server"
	"github.com/tigera/voltron/internal/pkg/test"
	"github.com/tigera/voltron/internal/pkg/utils"
	"github.com/tigera/voltron/pkg/tunnel"
)

var (
	srvCert    *x509.Certificate
	srvPrivKey *rsa.PrivateKey
	rootCAs    *x509.CertPool

	clusterA = "clusterA"
	clusterB = "clusterB"
)

func init() {
	log.SetOutput(GinkgoWriter)
	log.SetLevel(log.DebugLevel)

	srvCert, _ = test.CreateSelfSignedX509Cert("voltron", true)

	block, _ := pem.Decode([]byte(test.PrivateRSA))
	srvPrivKey, _ = x509.ParsePKCS1PrivateKey(block.Bytes)

	rootCAs = x509.NewCertPool()
	rootCAs.AddCert(srvCert)
}

var _ = Describe("Server", func() {
	var (
		err error
		wg  sync.WaitGroup
		srv *server.Server
		lis net.Listener
	)

	k8sAPI := test.NewK8sSimpleFakeClient(nil, nil)
	k8sAPI.AddJaneIdentity()
	watchSync := make(chan error)

	It("should fail to use invalid path", func() {
		_, err := server.New(
			nil,
			server.WithCredsFiles("dog/gopher.crt", "dog/gopher.key"),
		)
		Expect(err).To(HaveOccurred())
	})

	It("should start a server", func() {
		var e error
		lis, e = net.Listen("tcp", "localhost:0")
		Expect(e).NotTo(HaveOccurred())

		srv, err = server.New(
			k8sAPI,
			server.WithKeepClusterKeys(),
			server.WithTunnelCreds(srvCert, srvPrivKey),
			server.WithAuthentication(&rest.Config{}),
			server.WithWatchAdded(),
		)
		Expect(err).NotTo(HaveOccurred())
		wg.Add(1)
		go func() {
			defer wg.Done()
			err = srv.ServeHTTP(lis)
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = srv.WatchK8sWithSync(watchSync)
		}()
	})

	Context("when server is up", func() {
		It("should get empty list", func() {
			var list []clusters.ManagedCluster

			Eventually(func() int {
				var code int
				list, code = listClusters(lis.Addr().String())
				return code
			}).Should(Equal(200))
			Expect(len(list)).To(Equal(0))
		})

		It("should be able to register a new cluster", func() {
			Expect(k8sAPI.AddCluster(clusterA, clusterA)).ShouldNot(HaveOccurred())
			Expect(<-watchSync).NotTo(HaveOccurred())
		})

		It("should be able to get the clusters creds", func() {
			cert, key, err := srv.ClusterCreds(clusterA)
			Expect(err).NotTo(HaveOccurred())
			Expect(cert != nil && key != nil).To(BeTrue())
		})

		It("should be able to list the cluster", func() {
			list, code := listClusters(lis.Addr().String())
			Expect(code).To(Equal(200))
			Expect(len(list)).To(Equal(1))
			Expect(list[0].ID).To(Equal(clusterA))
		})

		It("should be able to register another cluster", func() {
			Expect(k8sAPI.AddCluster(clusterB, clusterB)).ShouldNot(HaveOccurred())
			Expect(<-watchSync).NotTo(HaveOccurred())
		})

		It("should be able to get sorted list of clusters", func() {
			list, code := listClusters(lis.Addr().String())
			Expect(code).To(Equal(200))
			Expect(len(list)).To(Equal(2))
			Expect(list[0].ID).To(Equal(clusterA))
			Expect(list[1].ID).To(Equal(clusterB))
		})

		It("should be able to delete a cluster", func() {
			Expect(k8sAPI.DeleteCluster(clusterB)).ShouldNot(HaveOccurred())
			Expect(<-watchSync).NotTo(HaveOccurred())
		})

		It("should be able to get list without the deleted cluster", func() {
			list, code := listClusters(lis.Addr().String())
			Expect(code).To(Equal(200))
			Expect(len(list)).To(Equal(1))
			Expect(list[0].ID).To(Equal(clusterA))
		})

		It("Should not proxy anywhere - no header", func() {
			resp, err := http.Get("http://" + lis.Addr().String() + "/")
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(400))
		})
	})

	It("should stop the server", func(done Done) {
		cerr := srv.Close()
		Expect(cerr).NotTo(HaveOccurred())
		wg.Wait()
		Expect(err).To(HaveOccurred())
		close(done)
	})
})

var _ = Describe("Server Proxy to tunnel", func() {
	var (
		err    error
		wg     sync.WaitGroup
		srv    *server.Server
		lis    net.Listener
		lis2   net.Listener
		lisTun net.Listener
		key    []byte
		cert   []byte
	)

	k8sAPI := test.NewK8sSimpleFakeClient(nil, nil)
	k8sAPI.AddJaneIdentity()
	watchSync := make(chan error)

	defaultServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	It("Should get some credentials for the server", func() {
		key, _ = utils.KeyPEMEncode(srvPrivKey)
		cert = utils.CertPEMEncode(srvCert)
	})

	startServer := func(opts ...server.Option) {
		var e error
		lis, e = net.Listen("tcp", "localhost:0")
		Expect(e).NotTo(HaveOccurred())

		xcert, _ := tls.X509KeyPair(cert, key)

		lis2, e = tls.Listen("tcp", "localhost:0", &tls.Config{
			Certificates: []tls.Certificate{xcert},
			NextProtos:   []string{"h2"},
		})
		Expect(e).NotTo(HaveOccurred())

		lisTun, e = net.Listen("tcp", "localhost:0")
		Expect(e).NotTo(HaveOccurred())

		defaultURL, e := url.Parse(defaultServer.URL)
		Expect(e).NotTo(HaveOccurred())

		defaultProxy, e := proxy.New([]proxy.Target{
			{
				Path: "/",
				Dest: defaultURL,
			},
			{
				Path: "/compliance/",
				Dest: defaultURL,
			},
		})
		Expect(e).NotTo(HaveOccurred())

		tunnelTargetWhitelist, _ := regex.CompileRegexStrings([]string{
			`^/$`,
			`^/some/path$`,
		})

		opts = append(opts,
			server.WithKeepClusterKeys(),
			server.WithTunnelCreds(srvCert, srvPrivKey),
			server.WithAuthentication(&rest.Config{}),
			server.WithDefaultProxy(defaultProxy),
			server.WithTunnelTargetWhitelist(tunnelTargetWhitelist),
			server.WithWatchAdded(),
		)

		srv, err = server.New(
			k8sAPI,
			opts...,
		)
		Expect(err).NotTo(HaveOccurred())

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = srv.ServeHTTP(lis)
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = srv.ServeHTTP(lis2)
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = srv.ServeTunnelsTLS(lisTun)
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = srv.WatchK8sWithSync(watchSync)
		}()
	}

	It("should start a server", func() {
		startServer()
	})

	Context("when server is up", func() {
		It("Should not proxy anywhere - invalid cluster", func() {
			req, err := http.NewRequest("GET", "http://"+lis.Addr().String()+"/", nil)
			Expect(err).NotTo(HaveOccurred())
			req.Header.Add(server.ClusterHeaderField, "zzzzzzz")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(400))
		})

		It("Should not proxy anywhere - multiple headers", func() {
			req, err := http.NewRequest("GET", "http://"+lis.Addr().String()+"/", nil)
			Expect(err).NotTo(HaveOccurred())
			req.Header.Add(server.ClusterHeaderField, clusterA)
			req.Header.Add(server.ClusterHeaderField, "helloworld")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(400))
		})

		It("should not be able to proxy to a cluster without a tunnel", func() {
			Expect(k8sAPI.AddCluster(clusterA, clusterA)).ShouldNot(HaveOccurred())
			Expect(<-watchSync).NotTo(HaveOccurred())
			clientHelloReq(lis.Addr().String(), clusterA, 400)
		})

		It("Should proxy to default if no header", func() {
			resp, err := http.Get(defaultServer.URL)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(200))
		})

		It("Should proxy to default even with header, if request path matches one of bypass tunnel targets", func() {
			req, err := http.NewRequest(
				"GET",
				"http://"+lis.Addr().String()+"/compliance/reports",
				strings.NewReader("HELLO"),
			)
			Expect(err).NotTo(HaveOccurred())
			req.Header.Add(server.ClusterHeaderField, clusterA)
			test.AddJaneToken(req)
			resp, err := http.DefaultClient.Do(req)
			Expect(err).NotTo(HaveOccurred())
			log.Infof("\n\n\nSTEVE GAO TEST resp = %+v\n", resp)
			Expect(resp.StatusCode).To(Equal(200))
		})

		var clnT *tunnel.Tunnel

		var certPemA, keyPemA []byte

		It("should be possible to open a tunnel", func() {
			var err error

			certPemA, keyPemA, err = srv.ClusterCreds(clusterA)
			Expect(err).NotTo(HaveOccurred())

			cert, err := tls.X509KeyPair(certPemA, keyPemA)
			Expect(err).NotTo(HaveOccurred())

			cfg := &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      rootCAs,
			}

			clnT, err = tunnel.DialTLS(lisTun.Addr().String(), cfg)
			Expect(err).NotTo(HaveOccurred())
		})

		// assumes clnT to be set to the tunnel we test
		testClnT := func() {
			It("should be possible to make HTTP/2 connection", func() {
				var wg sync.WaitGroup

				wg.Add(1)
				go func() {
					defer wg.Done()
					http2Srv(clnT)
				}()

				clnt := &http.Client{
					Transport: &http2.Transport{
						TLSClientConfig: &tls.Config{
							InsecureSkipVerify: true,
							NextProtos:         []string{"h2"},
						},
					},
				}

				req, err := http.NewRequest("GET",
					"https://"+lis2.Addr().String()+"/some/path", strings.NewReader("HELLO"))
				Expect(err).NotTo(HaveOccurred())
				req.Header[server.ClusterHeaderField] = []string{clusterA}
				test.AddJaneToken(req)

				var resp *http.Response

				Eventually(func() bool {
					var err error
					resp, err = clnt.Do(req)
					return err == nil && resp.StatusCode == 200
				}).Should(BeTrue())

				i := 0
				for {
					data := make([]byte, 100)
					n, err := resp.Body.Read(data)
					if err != nil {
						break
					}
					Expect(string(data[:n])).To(Equal(fmt.Sprintf("tick %d\n", i)))
					i++
				}
				wg.Wait()
			})
		}

		Context("when client tunnel exists", func() {
			testClnT()
		})

		When("opening another tunnel", func() {
			var certPem, keyPem []byte

			It("should fail to get creds if it does not exist yet", func() {
				var err error
				certPem, keyPem, err = srv.ClusterCreds(clusterB)
				Expect(err).To(HaveOccurred())
			})

			It("should be able to register another cluster", func() {
				Expect(k8sAPI.AddCluster(clusterB, clusterB)).ShouldNot(HaveOccurred())
				Expect(<-watchSync).NotTo(HaveOccurred())
			})

			When("another cluster is registered", func() {
				var cfgB *tls.Config

				It("should be possible to get creds for clusterB", func() {
					var err error
					certPem, keyPem, err = srv.ClusterCreds(clusterB)
					Expect(err).NotTo(HaveOccurred())

					cert, err := tls.X509KeyPair(certPem, keyPem)
					Expect(err).NotTo(HaveOccurred())

					cfgB = &tls.Config{
						Certificates: []tls.Certificate{cert},
						RootCAs:      rootCAs,
					}
				})

				var tunB *tunnel.Tunnel

				It("should be possible to open tunnel from clusterB", func() {
					var err error

					tunB, err = tunnel.DialTLS(lisTun.Addr().String(), cfgB)
					Expect(err).NotTo(HaveOccurred())
				})

				It("eventually accepting connections succeeds", func() {
					testSrv := httptest.NewUnstartedServer(
						http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
					testSrv.Listener = tunB
					testSrv.Start()

					clientHelloReq(lis.Addr().String(), clusterB, 502 /* non-TLS srv -> err */)
				})

				It("should be possible to open a second tunnel from clusterB", func() {
					var err error

					tunB, err = tunnel.DialTLS(lisTun.Addr().String(), cfgB)
					Expect(err).NotTo(HaveOccurred())
				})

				It("eventually accepting connections fails as the tunnel is rejected", func() {
					_, err := tunB.Accept()
					Expect(err).Should(HaveOccurred())
				})

				It("should be possible to delete the cluster", func() {
					Expect(k8sAPI.DeleteCluster(clusterB)).ShouldNot(HaveOccurred())
					Expect(<-watchSync).NotTo(HaveOccurred())
				})

				It("eventually accepting connections fails", func() {
					_, err := tunB.Accept()
					Expect(err).Should(HaveOccurred())
				})

				It("should be possible to open tunnel from unregistered clusterB", func() {
					var err error

					tunB, err = tunnel.DialTLS(lisTun.Addr().String(), cfgB)
					Expect(err).NotTo(HaveOccurred())
				})

				It("eventually accepting connections fails as the tunnel is rejected", func() {
					_, err := tunB.Accept()
					Expect(err).Should(HaveOccurred())
				})

				It("should be able to register clusterB again", func() {
					Expect(k8sAPI.AddCluster(clusterB, clusterB)).ShouldNot(HaveOccurred())
					Expect(<-watchSync).NotTo(HaveOccurred())
				})

				It("should be possible to open tunnel from clusterB with outdated creds", func() {
					var err error

					tunB, err = tunnel.DialTLS(lisTun.Addr().String(), cfgB)
					Expect(err).NotTo(HaveOccurred())
				})

				It("eventually accepting connections fails as the tunnel is rejected", func() {
					_, err := tunB.Accept()
					Expect(err).Should(HaveOccurred())
				})
			})
		})

		// Will be fixed in SAAS-769
/*		When("long lasting connection is in progress", func() {
			var slowTun *tunnel.Tunnel
			var xCert tls.Certificate

			It("should get some certs for test server", func() {
				key, _ := utils.KeyPEMEncode(srvPrivKey)
				cert := utils.CertPEMEncode(srvCert)

				xCert, _ = tls.X509KeyPair(cert, key)
			})

			It("Should add cluster", func() {
				Expect(k8sAPI.AddCluster("slow", "slow")).ShouldNot(HaveOccurred())
				Expect(<-watchSync).NotTo(HaveOccurred())
			})

			var slow *test.HTTPSBin
			slowC := make(chan struct{})
			slowWaitC := make(chan struct{})

			It("Should open a tunnel", func() {
				certPem, keyPem, _ := srv.ClusterCreds("slow")
				cert, _ := tls.X509KeyPair(certPem, keyPem)

				cfg := &tls.Config{
					Certificates: []tls.Certificate{cert},
					RootCAs:      rootCAs,
				}

				Eventually(func() error {
					var err error
					slowTun, err = tunnel.DialTLS(lisTun.Addr().String(), cfg)
					return err
				}).ShouldNot(HaveOccurred())

				slow = test.NewHTTPSBin(slowTun, xCert, func(r *http.Request) {
					// the connection is set up, let the test proceed
					close(slowWaitC)
					// block here to emulate long lasting connection
					<-slowC
				})

			})

			It("should be able to update a cluster - test race SAAS-226", func() {
				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()
					clnt := configureHTTPSClient()
					req, err := http.NewRequest("GET",
						"https://"+lis2.Addr().String()+"/some/path", strings.NewReader("HELLO"))
					Expect(err).NotTo(HaveOccurred())
					req.Header[server.ClusterHeaderField] = []string{"slow"}
					test.AddJaneToken(req)
					resp, err := clnt.Do(req)
					log.Infof("resp = %+v\n", resp)
					log.Infof("err = %+v\n", err)
					Expect(err).NotTo(HaveOccurred())
				}()

				<-slowWaitC
				Expect(k8sAPI.UpdateCluster("slow")).ShouldNot(HaveOccurred())
				Expect(<-watchSync).NotTo(HaveOccurred())
				close(slowC) // let the call handler exit
				slow.Close()
				wg.Wait()
			})
		})*/

		It("should stop the servers", func(done Done) {
			err := srv.Close()
			Expect(err).NotTo(HaveOccurred())
			wg.Wait()
			close(done)
		})

		It("should re-start a server with auto registration", func() {
			startServer(server.WithAutoRegister())
		})

		Context("When auto-registration is enabled", func() {
			It("should be possible to open a tunnel with certs for clusterA", func() {
				cert, err := tls.X509KeyPair(certPemA, keyPemA)
				Expect(err).NotTo(HaveOccurred())

				cfg := &tls.Config{
					Certificates: []tls.Certificate{cert},
					RootCAs:      rootCAs,
				}

				clnT, err = tunnel.DialTLS(lisTun.Addr().String(), cfg)
				Expect(err).NotTo(HaveOccurred())
			})

			Context("when client tunnel exists", func() {
				testClnT()
			})
		})

		It("should delete clusterA", func() {
			Expect(k8sAPI.DeleteCluster(clusterA)).ShouldNot(HaveOccurred())
			Expect(<-watchSync).NotTo(HaveOccurred())
		})

		It("should stop the servers again", func(done Done) {
			err := srv.Close()
			Expect(err).NotTo(HaveOccurred())
			wg.Wait()
			close(done)
		})

		It("should re-start a server again", func() {
			startServer(server.WithAutoRegister())
		})

		Context("When clusterA was previously deleted", func() {
			It("should be possible to open a tunnel with certs for clusterA", func() {
				cert, err := tls.X509KeyPair(certPemA, keyPemA)
				Expect(err).NotTo(HaveOccurred())

				cfg := &tls.Config{
					Certificates: []tls.Certificate{cert},
					RootCAs:      rootCAs,
				}

				clnT, err = tunnel.DialTLS(lisTun.Addr().String(), cfg)
				Expect(err).NotTo(HaveOccurred())
			})

			It("eventually accepting connections fails as the tunnel is rejected", func() {
				_, err := clnT.Accept()
				Expect(err).Should(HaveOccurred())
			})

		})

	})

	It("should stop the servers", func(done Done) {
		err := srv.Close()
		Expect(err).NotTo(HaveOccurred())
		wg.Wait()
		close(done)
	})
})

var _ = Describe("Using the generated guardian certs as tunnel certs", func() {
	var (
		err    error
		wg     sync.WaitGroup
		srv    *server.Server
		lisTun net.Listener
	)
	k8sAPI := test.NewK8sSimpleFakeClient(nil, nil)
	watchSync := make(chan error)

	defaultServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	startServer := func(withTunnelCreds bool, opts ...server.Option) {
		var e error

		lisTun, e = net.Listen("tcp", "localhost:0")
		Expect(e).NotTo(HaveOccurred())

		defaultURL, e := url.Parse(defaultServer.URL)
		Expect(e).NotTo(HaveOccurred())

		defaultProxy, e := proxy.New([]proxy.Target{
			{
				Path: "/",
				Dest: defaultURL,
			},
			{
				Path: "/compliance/",
				Dest: defaultURL,
			},
		})
		Expect(e).NotTo(HaveOccurred())

		tunnelTargetWhitelist, _ := regex.CompileRegexStrings([]string{
			`^/$`,
			`^/some/path$`,
		})

		if withTunnelCreds {
			opts = append(opts,
				server.WithKeepClusterKeys(),
				server.WithAuthentication(&rest.Config{}),
				server.WithDefaultProxy(defaultProxy),
				server.WithTunnelTargetWhitelist(tunnelTargetWhitelist),
				server.WithWatchAdded(),
			)
		} else {
			opts = append(opts,
				server.WithKeepClusterKeys(),
				server.WithTunnelCreds(srvCert, srvPrivKey),
				server.WithAuthentication(&rest.Config{}),
				server.WithDefaultProxy(defaultProxy),
				server.WithTunnelTargetWhitelist(tunnelTargetWhitelist),
				server.WithWatchAdded(),
			)
		}

		srv, err = server.New(
			k8sAPI,
			opts...,
		)
		Expect(err).NotTo(HaveOccurred())

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = srv.ServeTunnelsTLS(lisTun)
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = srv.WatchK8sWithSync(watchSync)
		}()

		k8sAPI.WaitForManagedClustersWatched()
	}

	JustBeforeEach(func() {
		startServer(false)
	})

	JustAfterEach(func() {
		By("Closing the server")
		err := srv.Close()
		Expect(err).NotTo(HaveOccurred())
		wg.Wait()
	})

	It("shouldn't be possible to open a tunnel using client cert as the tunnel cert", func() {
		var err error

		By("adding ClusterA")

		Expect(k8sAPI.AddCluster(clusterA, clusterA)).ShouldNot(HaveOccurred())
		Expect(<-watchSync).NotTo(HaveOccurred())

		By("adding a managed cluster named voltron to spoof the management cluster")
		Expect(k8sAPI.AddCluster("voltron", "voltron")).ShouldNot(HaveOccurred())
		Expect(<-watchSync).NotTo(HaveOccurred())

		By("Trying to connect clusterA to the fake voltron")
		certPemA, keyPemA, err := srv.ClusterCreds(clusterA)
		Expect(err).NotTo(HaveOccurred())

		certPem, keyPem, err := srv.ClusterCreds("voltron")
		Expect(err).NotTo(HaveOccurred())

		//close the server
		err = srv.Close()
		Expect(err).NotTo(HaveOccurred())
		wg.Wait()

		k, _ := ssh.ParseRawPrivateKey(keyPem)
		block, _ := pem.Decode(certPem)
		c, _ := x509.ParseCertificate(block.Bytes)

		// Restart the server with the guardian certificates as the new tunnel credentials
		startServer(true, server.WithTunnelCreds(c, k.(crypto.Signer)))

		// Try to connect clusterA to the new fake voltron, should fail
		cert, err := tls.X509KeyPair(certPemA, keyPemA)
		Expect(err).NotTo(HaveOccurred())

		cfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      rootCAs,
			ServerName:   "voltron",
		}

		_, err = tunnel.DialTLS(lisTun.Addr().String(), cfg)
		Expect(err).Should(MatchError("tcp.tls.Dial failed: x509: certificate specifies an incompatible key usage"))
	})

	It("should stop the servers", func(done Done) {
		err := srv.Close()
		Expect(err).NotTo(HaveOccurred())
		wg.Wait()
		close(done)
	})
})

var _ = Describe("Server authenticates requests", func() {

	var wg sync.WaitGroup
	var srv *server.Server
	var tun *tunnel.Tunnel
	var lisHTTPS net.Listener
	var lisHTTP net.Listener
	var lisTun net.Listener
	var xCert tls.Certificate

	k8sAPI := test.NewK8sSimpleFakeClient(nil, nil)
	watchSync := make(chan error)

	By("Creating credentials for server", func() {
		srvCert, _ = test.CreateSelfSignedX509Cert("voltron", true)

		block, _ := pem.Decode([]byte(test.PrivateRSA))
		srvPrivKey, _ = x509.ParsePKCS1PrivateKey(block.Bytes)

		rootCAs = x509.NewCertPool()
		rootCAs.AddCert(srvCert)

		key, _ := utils.KeyPEMEncode(srvPrivKey)
		cert := utils.CertPEMEncode(srvCert)

		xCert, _ = tls.X509KeyPair(cert, key)
	})

	It("Should start the server", func() {
		var err error

		lisHTTP, err = net.Listen("tcp", "localhost:0")
		Expect(err).NotTo(HaveOccurred())

		lisHTTPS, err = tls.Listen("tcp", "localhost:0", &tls.Config{
			Certificates: []tls.Certificate{xCert},
			NextProtos:   []string{"h2"},
		})
		Expect(err).NotTo(HaveOccurred())

		lisTun, err = net.Listen("tcp", "localhost:0")
		Expect(err).NotTo(HaveOccurred())

		tunnelTargetWhitelist, _ := regex.CompileRegexStrings([]string{
			`^/?`,
		})

		srv, err = server.New(
			k8sAPI,
			server.WithKeepClusterKeys(),
			server.WithTunnelCreds(srvCert, srvPrivKey),
			server.WithAuthentication(&rest.Config{}),
			server.WithTunnelTargetWhitelist(tunnelTargetWhitelist),
			server.WithWatchAdded(),
		)
		Expect(err).NotTo(HaveOccurred())

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = srv.ServeHTTP(lisHTTPS)
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = srv.ServeHTTP(lisHTTP)
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = srv.ServeTunnelsTLS(lisTun)
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = srv.WatchK8sWithSync(watchSync)
		}()

		k8sAPI.WaitForManagedClustersWatched()
	})

	It("Should add cluster A", func() {
		Expect(k8sAPI.AddCluster(clusterA, clusterA)).ShouldNot(HaveOccurred())
		Expect(<-watchSync).NotTo(HaveOccurred())
	})

	var bin *test.HTTPSBin
	binC := make(chan struct{}, 1)

	It("Should open a tunnel for cluster A", func() {
		certPem, keyPem, _ := srv.ClusterCreds(clusterA)
		cert, _ := tls.X509KeyPair(certPem, keyPem)

		cfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      rootCAs,
		}

		Eventually(func() error {
			var err error
			tun, err = tunnel.DialTLS(lisTun.Addr().String(), cfg)
			return err
		}).ShouldNot(HaveOccurred())

		bin = test.NewHTTPSBin(tun, xCert, func(r *http.Request) {
			Expect(r.Header.Get("Impersonate-User")).To(Equal(test.Jane))
			Expect(r.Header.Get("Impersonate-Group")).To(Equal(test.Developers))
			Expect(r.Header.Get("Authorization")).NotTo(Equal(test.JaneBearerToken))
			binC <- struct{}{}
		})

	})

	authJane := func() {
		clnt := configureHTTPSClient()
		req := requestToClusterA(lisHTTPS.Addr().String())
		test.AddJaneToken(req)
		resp, err := clnt.Do(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(200))

		// would timeout the test if the reply is not from the serve and the test were not executed
		<-binC
	}

	It("should authenticate Jane", func() {
		k8sAPI.AddJaneIdentity()
		authJane()
	})

	It("should not authenticate Bob - Bob exists, does not have rights", func() {
		k8sAPI.AddBobIdentity()
		clnt := configureHTTPSClient()
		req := requestToClusterA(lisHTTPS.Addr().String())
		test.AddBobToken(req)
		resp, err := clnt.Do(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(401))
	})

	It("should not authenticate user that does not exist", func() {
		k8sAPI.AddBobIdentity()
		clnt := configureHTTPSClient()
		req := requestToClusterA(lisHTTPS.Addr().String())
		req.Header.Add("Authorization", "Bearer "+"someRandomTokenThatShouldNotMatch")
		resp, err := clnt.Do(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(401))
	})

	It("should return 401 on missing tokens", func() {
		clnt := configureHTTPSClient()
		req := requestToClusterA(lisHTTPS.Addr().String())
		resp, err := clnt.Do(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(401))
	})

	It("should authenticate Jane again after errors", func() {
		authJane()
	})

	It("should be able to delete a cluster - test race SAAS-226", func() {
		Expect(k8sAPI.DeleteCluster(clusterA)).ShouldNot(HaveOccurred())
		Expect(<-watchSync).NotTo(HaveOccurred())
	})

	It("should stop the server", func(done Done) {
		err := srv.Close()
		Expect(err).NotTo(HaveOccurred())
		wg.Wait()
		bin.Close()
		close(done)
	})
})

var _ = Describe("Creating an HTTP server that proxies traffic", func() {
	var k8sAPI = test.NewK8sSimpleFakeClient(nil, nil)
	var srv *server.Server
	var certFile string
	var keyFile string
	var listener net.Listener

	JustBeforeEach(func() {
		By("Creating a server that only serves HTTPS traffic")
		var err error
		listener, err = net.Listen("tcp", "localhost:0")
		address := listener.Addr()
		Expect(address).ShouldNot(BeNil())
		Expect(err).NotTo(HaveOccurred())

		var opts = []server.Option{
			server.WithDefaultAddr(address.String()),
			server.WithKeepAliveSettings(true, 100),
			server.WithCredsFiles(certFile, keyFile),
		}

		srv, err = server.New(
			k8sAPI,
			opts...,
		)
		Expect(err).NotTo(HaveOccurred())
	})

	JustAfterEach(func() {
		By("Closing the server")
		var err = srv.Close()
		Expect(err).NotTo(HaveOccurred())
	})

	Context("Using self signed certs", func() {
		BeforeEach(func() {
			certFile = "testdata/self-signed-cert.pem"
			keyFile = "testdata/self-signed-key.pem"
		})

		It("Does not initiate a tunnel server when the tunnel destination doesn't have tls certificates", func() {
			var err = srv.ServeTunnelsTLS(listener)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("Using certs", func() {
		BeforeEach(func() {
			certFile = "testdata/cert.pem"
			keyFile = "testdata/key.pem"
		})

		It("Does not initiate a tunnel server when the tunnel destination doesn't have tls certificates", func() {
			var err = srv.ServeTunnelsTLS(listener)
			Expect(err).To(HaveOccurred())
		})
	})
})

func requestToClusterA(address string) *http.Request {
	defer GinkgoRecover()
	req, err := http.NewRequest("GET",
		"https://"+address+"/some/path", strings.NewReader("HELLO"))
	Expect(err).ShouldNot(HaveOccurred())
	req.Header[server.ClusterHeaderField] = []string{clusterA}
	Expect(err).NotTo(HaveOccurred())
	return req
}

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

func listClusters(server string) ([]clusters.ManagedCluster, int) {
	resp, err := http.Get("http://" + server + "/voltron/api/clusters")
	Expect(err).NotTo(HaveOccurred())

	if resp.StatusCode != 200 {
		return nil, resp.StatusCode
	}

	var list []clusters.ManagedCluster

	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&list)
	Expect(err).NotTo(HaveOccurred())

	return list, 200
}

func clientHelloReq(addr string, target string, expectStatus int) *http.Response {
	defer GinkgoRecover()
	req, err := http.NewRequest("GET", "http://"+addr+"/some/path", strings.NewReader("HELLO"))
	Expect(err).NotTo(HaveOccurred())

	req.Header[server.ClusterHeaderField] = []string{target}
	test.AddJaneToken(req)

	var resp *http.Response

	Eventually(func() bool {
		var err error
		resp, err := http.DefaultClient.Do(req)
		return err == nil && resp.StatusCode == expectStatus
	}, 2*time.Second, 400*time.Millisecond).Should(BeTrue())

	return resp
}

func http2Srv(t *tunnel.Tunnel) {
	// we need some credentials
	key, _ := utils.KeyPEMEncode(srvPrivKey)
	cert := utils.CertPEMEncode(srvCert)

	xcert, _ := tls.X509KeyPair(cert, key)

	mux := http.NewServeMux()
	httpsrv := &http.Server{
		Handler: mux,
	}

	var reqWg sync.WaitGroup
	reqWg.Add(1)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		defer reqWg.Done()
		f, ok := w.(http.Flusher)
		Expect(ok).To(BeTrue())

		for i := 0; i < 3; i++ {
			_, err := fmt.Fprintf(w, "tick %d\n", i)
			Expect(err).ShouldNot(HaveOccurred())
			f.Flush()
			time.Sleep(300 * time.Millisecond)
		}
	})

	lisTLS := tls.NewListener(t, &tls.Config{
		Certificates: []tls.Certificate{xcert},
		NextProtos:   []string{"h2"},
	})

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = httpsrv.Serve(lisTLS)
	}()

	// we only handle one request, we wait until it is done
	reqWg.Wait()

	Expect(httpsrv.Close()).ShouldNot(HaveOccurred())
	wg.Wait()
}
