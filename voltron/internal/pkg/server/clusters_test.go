// Copyright (c) 2019-2023 Tigera, Inc. All rights reserved.

package server

// test is in pkg server to be able to access internal clusters without
// exporting them outside, not part of the pkg API

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"

	jclust "github.com/projectcalico/calico/voltron/internal/pkg/clusters"
	"github.com/projectcalico/calico/voltron/internal/pkg/config"
	vcfg "github.com/projectcalico/calico/voltron/internal/pkg/config"
	"github.com/projectcalico/calico/voltron/internal/pkg/test"

	"sigs.k8s.io/controller-runtime/pkg/client"
	runtimeClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
)

func describe(name string, testFn func(string)) bool {
	Describe(name+" cluster-scoped", func() { testFn("") })
	Describe(name+" namespace-scoped", func() { testFn("resource-ns") })
	return true
}

var updateError = false

func InterceptUpdate(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
	if updateError {
		return fmt.Errorf("update errors for testing purposes")
	}
	return client.Update(ctx, obj, opts...)
}

var _ = describe("Clusters", func(clusterNamespace string) {
	logrus.SetLevel(logrus.DebugLevel)
	const clusterID = "resource-name"

	var myClusters *clusters
	var fakeClient runtimeClient.WithWatch
	var ctx context.Context
	var cancel context.CancelFunc

	voltronConfig := config.Config{
		TenantNamespace: clusterNamespace,
		TenantClaim:     "tenant_claim",
	}

	Context("Watch is up and running", func() {
		BeforeEach(func() {
			ctx, cancel = context.WithCancel(context.Background())
			scheme := kscheme.Scheme
			err := v3.AddToScheme(scheme)
			Expect(err).NotTo(HaveOccurred())
			fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()

			su := NewStatusUpdater(ctx, fakeClient, voltronConfig, testStatusConfig)
			myClusters = &clusters{
				clusters:         make(map[string]*cluster),
				client:           fakeClient,
				voltronCfg:       &vcfg.Config{TenantNamespace: clusterNamespace},
				statusUpdateFunc: su.SetStatus,
			}

			go func() {
				_ = myClusters.watchK8s(ctx, nil)
			}()
		})
		AfterEach(func() {
			cancel()
		})

		It("should be possible to add/update/delete a cluster", func() {
			By("should be possible to add a cluster", func() {
				annotations := map[string]string{
					AnnotationActiveCertificateFingerprint: "active-fingerprint-hash-1",
				}
				err := fakeClient.Create(context.Background(), &v3.ManagedCluster{
					TypeMeta: metav1.TypeMeta{
						Kind:       v3.KindManagedCluster,
						APIVersion: v3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:        clusterID,
						Namespace:   clusterNamespace,
						Annotations: annotations,
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Eventually(func() int { return len(myClusters.List()) }).Should(Equal(1))
			})

			By("should be able to update cluster active fingerprint", func() {
				Expect(myClusters.clusters[clusterID].ActiveFingerprint).To(Equal("active-fingerprint-hash-1"))
				mc := &v3.ManagedCluster{}
				err := fakeClient.Get(context.Background(), types.NamespacedName{Name: clusterID, Namespace: clusterNamespace}, mc)
				Expect(err).NotTo(HaveOccurred())
				Expect(mc.GetAnnotations()).To(HaveKeyWithValue(AnnotationActiveCertificateFingerprint, "active-fingerprint-hash-1"))

				err = myClusters.clusters[clusterID].updateActiveFingerprint("active-fingerprint-hash-2")
				Expect(err).NotTo(HaveOccurred())

				Expect(myClusters.clusters[clusterID].ActiveFingerprint).To(Equal("active-fingerprint-hash-2"))

				mc = &v3.ManagedCluster{}
				err = fakeClient.Get(context.Background(), types.NamespacedName{Name: clusterID, Namespace: clusterNamespace}, mc)

				Expect(err).NotTo(HaveOccurred())
				Expect(mc.GetAnnotations()).To(HaveKeyWithValue(AnnotationActiveCertificateFingerprint, "active-fingerprint-hash-2"))
			})

			By("should be possible to delete a cluster", func() {
				Expect(fakeClient.Delete(context.Background(), &v3.ManagedCluster{
					TypeMeta: metav1.TypeMeta{
						Kind:       v3.KindManagedCluster,
						APIVersion: v3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterID,
						Namespace: clusterNamespace,
					},
				})).ShouldNot(HaveOccurred())
				Eventually(func() int { return len(myClusters.List()) }).Should(Equal(0))
			})
		})
	})

	When("Watch is down", func() {
		BeforeEach(func() {
			ctx, cancel = context.WithCancel(context.Background())
			scheme := kscheme.Scheme
			err := v3.AddToScheme(scheme)
			Expect(err).NotTo(HaveOccurred())
			fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()

			su := NewStatusUpdater(ctx, fakeClient, voltronConfig, testStatusConfig)
			myClusters = &clusters{
				clusters:         make(map[string]*cluster),
				client:           fakeClient,
				voltronCfg:       &vcfg.Config{TenantNamespace: clusterNamespace},
				statusUpdateFunc: su.SetStatus,
			}

			go func() {
				_ = myClusters.watchK8s(ctx, nil)
			}()
		})
		AfterEach(func() {
			cancel()
		})
		It("should cluster added should be seen after watch restarts", func() {
			Expect(len(myClusters.List())).To(Equal(0))
			Expect(fakeClient.Create(context.Background(), &v3.ManagedCluster{
				TypeMeta: metav1.TypeMeta{
					Kind:       v3.KindManagedCluster,
					APIVersion: v3.GroupVersionCurrent,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterID,
					Namespace: clusterNamespace,
				},
			})).NotTo(HaveOccurred())
			go func() {
				_ = myClusters.watchK8s(ctx, nil)
			}()
			Eventually(func() int { return len(myClusters.List()) }).Should(Equal(1))
		})
	})

	When("watch restarts", func() {
		var statusCtx context.Context
		var statusCancel context.CancelFunc

		BeforeEach(func() {
			ctx, cancel = context.WithCancel(context.Background())
			statusCtx, statusCancel = context.WithCancel(context.Background())
			scheme := kscheme.Scheme
			err := v3.AddToScheme(scheme)
			Expect(err).NotTo(HaveOccurred())
			fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()

			su := NewStatusUpdater(statusCtx, fakeClient, voltronConfig, testStatusConfig)
			myClusters = &clusters{
				clusters:         make(map[string]*cluster),
				client:           fakeClient,
				voltronCfg:       &vcfg.Config{TenantNamespace: clusterNamespace},
				statusUpdateFunc: su.SetStatus,
			}

			go func() {
				_ = myClusters.watchK8s(ctx, nil)
			}()
		})
		AfterEach(func() {
			cancel()
			statusCancel()
		})
		It("should add a cluster after watch restarted due to an error", func() {
			mcList := &v3.ManagedClusterList{}
			watch, err := fakeClient.Watch(context.Background(), mcList, &runtimeClient.ListOptions{})
			watch.Stop()
			Expect(err).NotTo(HaveOccurred())
			Expect(len(myClusters.List())).To(Equal(0))
			go func() {
				_ = myClusters.watchK8s(ctx, nil)
			}()
			Expect(fakeClient.Create(context.Background(), &v3.ManagedCluster{
				TypeMeta: metav1.TypeMeta{
					Kind:       v3.KindManagedCluster,
					APIVersion: v3.GroupVersionCurrent,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "X",
					Namespace: clusterNamespace,
				},
			})).NotTo(HaveOccurred())
			Eventually(func() int { return len(myClusters.List()) }).Should(Equal(1))
		})

		It("should delete a cluster deleted while watch was down", func() {
			By("adding a cluster when watch is running", func() {
				annotations := map[string]string{
					AnnotationActiveCertificateFingerprint: "active-fingerprint-hash-1",
				}
				err := fakeClient.Create(context.Background(), &v3.ManagedCluster{
					TypeMeta: metav1.TypeMeta{
						Kind:       v3.KindManagedCluster,
						APIVersion: v3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:        clusterID,
						Namespace:   clusterNamespace,
						Annotations: annotations,
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Eventually(func() int { return len(myClusters.List()) }).Should(Equal(1))
			})

			By("Closing the watch", func() {
				cancel()
			})

			By("Deleting a managed cluster while the watch is down", func() {
				Expect(len(myClusters.List())).To(Equal(1))
				Expect(fakeClient.Delete(context.Background(), &v3.ManagedCluster{
					TypeMeta: metav1.TypeMeta{
						Kind:       v3.KindManagedCluster,
						APIVersion: v3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterID,
						Namespace: clusterNamespace,
					},
				})).ShouldNot(HaveOccurred())
				ctx, cancel = context.WithCancel(context.Background())
				go func() {
					_ = myClusters.watchK8s(ctx, nil)
				}()
				Eventually(func() int { return len(myClusters.List()) }).Should(Equal(0))
			})
		})
	})

	When("ManagedCluster update fails", func() {
		BeforeEach(func() {
			ctx, cancel = context.WithCancel(context.Background())
			scheme := kscheme.Scheme
			err := v3.AddToScheme(scheme)
			Expect(err).NotTo(HaveOccurred())
			fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{Update: InterceptUpdate}).Build()

			su := NewStatusUpdater(ctx, fakeClient, voltronConfig, testStatusConfig)
			myClusters = &clusters{
				clusters:         make(map[string]*cluster),
				client:           fakeClient,
				voltronCfg:       &vcfg.Config{TenantNamespace: clusterNamespace},
				statusUpdateFunc: su.SetStatus,
			}

			go func() {
				_ = myClusters.watchK8s(ctx, nil)
			}()
		})
		AfterEach(func() {
			cancel()
		})
		It("should retry until the update succeeds", func() {
			By("setting the update to fail", func() {
				updateError = true
			})
			Expect(fakeClient.Create(context.Background(), &v3.ManagedCluster{
				TypeMeta: metav1.TypeMeta{
					Kind:       v3.KindManagedCluster,
					APIVersion: v3.GroupVersionCurrent,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "blocked",
					Namespace: clusterNamespace,
				},
				Status: v3.ManagedClusterStatus{
					Conditions: []v3.ManagedClusterStatusCondition{{
						Type:   v3.ManagedClusterStatusTypeConnected,
						Status: v3.ManagedClusterStatusValueTrue,
					}},
				},
			})).NotTo(HaveOccurred())

			Consistently(func() v3.ManagedClusterStatusValue {
				mc := &v3.ManagedCluster{}
				err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "blocked", Namespace: clusterNamespace}, mc)
				if err != nil {
					return v3.ManagedClusterStatusValueUnknown
				}
				for _, v := range mc.Status.Conditions {
					if v.Type == v3.ManagedClusterStatusTypeConnected {
						return v.Status
					}
				}
				return v3.ManagedClusterStatusValueUnknown
			}, "1s").Should(Equal(v3.ManagedClusterStatusValueTrue), "Managed cluster connection status should remain true since the update is failing")

			By("setting the update to succeed", func() {
				updateError = false
			})
			Eventually(func() v3.ManagedClusterStatusValue {
				mc := &v3.ManagedCluster{}
				err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "blocked", Namespace: clusterNamespace}, mc)
				if err != nil {
					return v3.ManagedClusterStatusValueUnknown
				}
				for _, v := range mc.Status.Conditions {
					if v.Type == v3.ManagedClusterStatusTypeConnected {
						return v.Status
					}
				}
				return v3.ManagedClusterStatusValueUnknown
			}, "3s").Should(Equal(v3.ManagedClusterStatusValueFalse), "Managed cluster connection status should be set false when the update succeeds")
			Expect(fakeClient.Delete(context.Background(), &v3.ManagedCluster{
				TypeMeta: metav1.TypeMeta{
					Kind:       v3.KindManagedCluster,
					APIVersion: v3.GroupVersionCurrent,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "blocked",
					Namespace: clusterNamespace,
				},
			})).ShouldNot(HaveOccurred())
			Eventually(func() int { return len(myClusters.List()) }).Should(Equal(1))
		})
	})

	Context("New watch", func() {
		const clusterName = "sample-restart-cluster"

		BeforeEach(func() {
			ctx, cancel = context.WithCancel(context.Background())
			scheme := kscheme.Scheme
			err := v3.AddToScheme(scheme)
			Expect(err).NotTo(HaveOccurred())
			fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()

			su := NewStatusUpdater(ctx, fakeClient, voltronConfig, testStatusConfig)
			myClusters = &clusters{
				clusters:         make(map[string]*cluster),
				client:           fakeClient,
				voltronCfg:       &vcfg.Config{TenantNamespace: clusterNamespace},
				statusUpdateFunc: su.SetStatus,
			}
		})
		AfterEach(func() { cancel() })

		It("should set ManagedClusterConnected status to false if it is true during startup.", func() {
			Expect(fakeClient.Create(context.Background(), &v3.ManagedCluster{
				TypeMeta: metav1.TypeMeta{
					Kind:       v3.KindManagedCluster,
					APIVersion: v3.GroupVersionCurrent,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Status: v3.ManagedClusterStatus{
					Conditions: []v3.ManagedClusterStatusCondition{
						{
							Status: v3.ManagedClusterStatusValueTrue,
							Type:   "ManagedClusterConnected",
						},
					},
				},
			})).NotTo(HaveOccurred())

			go func() {
				_ = myClusters.watchK8s(ctx, nil)
			}()

			Eventually(func() v3.ManagedClusterStatusValue {
				mc := &v3.ManagedCluster{}
				_ = fakeClient.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, mc)
				return mc.Status.Conditions[0].Status
			}, 5*time.Second, 5*time.Millisecond).Should(Equal(v3.ManagedClusterStatusValueFalse))

			Expect(len(myClusters.List())).To(Equal(1))
		})
	})
})

var _ = describe("Update certificates", func(clusterNamespace string) {

	k8sAPI := test.NewK8sSimpleFakeClient(nil, nil)
	clusters := &clusters{
		clusters:              make(map[string]*cluster),
		k8sCLI:                k8sAPI,
		voltronCfg:            &vcfg.Config{},
		clientCertificatePool: x509.NewCertPool(),
		statusUpdateFunc:      func(string, v3.ManagedClusterStatusValue) {},
	}

	var (
		err                  error
		voltronTunnelCert    *x509.Certificate
		voltronTunnelPrivKey *rsa.PrivateKey

		cluster1Cert *x509.Certificate
		cluster2Cert *x509.Certificate

		cluster1CertTemplate *x509.Certificate
		cluster2CertTemplate *x509.Certificate
	)

	const (
		cluster1ID = "cluster-1"
		cluster2ID = "cluster-2"
		cluster3ID = "cluster-3"
	)

	BeforeEach(func() {
		voltronTunnelCertTemplate := test.CreateCACertificateTemplate("voltron")
		voltronTunnelPrivKey, voltronTunnelCert, err = test.CreateCertPair(voltronTunnelCertTemplate, nil, nil)
		Expect(err).ShouldNot(HaveOccurred())

	})

	It("should update the certificate pool when a managed cluster containing a certificate is added", func() {
		cluster1CertTemplate = test.CreateClientCertificateTemplate(cluster1ID, "localhost")
		_, cluster1Cert, err = test.CreateCertPair(cluster1CertTemplate, voltronTunnelCert, voltronTunnelPrivKey)
		Expect(err).NotTo(HaveOccurred())

		cluster2CertTemplate = test.CreateClientCertificateTemplate(cluster2ID, "localhost")
		_, cluster2Cert, err = test.CreateCertPair(cluster2CertTemplate, voltronTunnelCert, voltronTunnelPrivKey)
		Expect(err).NotTo(HaveOccurred())

		// Add a cluster
		mc := &jclust.ManagedCluster{
			ID:          cluster1ID,
			Certificate: test.CertToPemBytes(cluster1Cert),
		}

		_, err = clusters.add(mc)
		Expect(err).NotTo(HaveOccurred())
		// Add a second cluster
		mc = &jclust.ManagedCluster{
			ID:          cluster2ID,
			Certificate: test.CertToPemBytes(cluster2Cert),
		}
		_, err = clusters.add(mc)
		Expect(err).NotTo(HaveOccurred())

		// Validate the certificates are in the map
		expectedCertCluster1, err := parseCertificatePEMBlock(test.CertToPemBytes(cluster1Cert))
		Expect(err).NotTo(HaveOccurred())
		expectedCertCluster2, err := parseCertificatePEMBlock(test.CertToPemBytes(cluster2Cert))
		Expect(err).NotTo(HaveOccurred())

		// Validate the certificates are in the pool
		//nolint:staticcheck // Ignore SA1019 deprecated
		Expect(clusters.clientCertificatePool.Subjects()).To(HaveLen(2))
		//nolint:staticcheck // Ignore SA1019 deprecated
		Expect(clusters.clientCertificatePool.Subjects()).To(ContainElement(expectedCertCluster1.RawSubject))
		//nolint:staticcheck // Ignore SA1019 deprecated
		Expect(clusters.clientCertificatePool.Subjects()).To(ContainElement(expectedCertCluster2.RawSubject))
	})

	It("should add a new certificate to the pool when a cluster certificate has been updated", func() {
		cluster1CertTemplate = test.CreateClientCertificateTemplate("cluster-1-update", "localhost")
		_, cluster1Cert, err = test.CreateCertPair(cluster1CertTemplate, voltronTunnelCert, voltronTunnelPrivKey)
		Expect(err).NotTo(HaveOccurred())

		// Update the certificate for cluster-1
		mc := &jclust.ManagedCluster{
			ID:          cluster1ID,
			Certificate: test.CertToPemBytes(cluster1Cert),
		}

		err = clusters.update(mc)
		Expect(err).NotTo(HaveOccurred())

		expectedCertCluster1, err := parseCertificatePEMBlock(test.CertToPemBytes(cluster1Cert))
		Expect(err).NotTo(HaveOccurred())

		// Validate the certificates are in the pool
		//nolint:staticcheck // Ignore SA1019 deprecated
		Expect(clusters.clientCertificatePool.Subjects()).To(HaveLen(3))
		//nolint:staticcheck // Ignore SA1019 deprecated
		Expect(clusters.clientCertificatePool.Subjects()).To(ContainElement(expectedCertCluster1.RawSubject))
	})
})
