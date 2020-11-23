// Copyright (c) 2018-2020 Tigera, Inc. All rights reserved.

package federationsyncer_test

import (
	"context"
	"reflect"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	kapiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/projectcalico/libcalico-go/lib/apiconfig"
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/backend"
	"github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/k8s"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/backend/syncersv1/federationsyncer"
	"github.com/projectcalico/libcalico-go/lib/testutils"

	log "github.com/sirupsen/logrus"
)

// Our test framework has an etcd and a k8s datastore running.  For simplicity, we'll test with the following:
// - Local etcd (for Calico config)
// - Local and remote k8s using the same k8s client
// Since the local and remote k8s are pointing to the same cluster, both will return the same set of resources, except
// the remote ones will include the cluster details.
var _ = testutils.E2eDatastoreDescribe("Remote cluster federationsyncer tests", testutils.DatastoreEtcdV3, func(etcdConfig apiconfig.CalicoAPIConfig) {
	testutils.E2eDatastoreDescribe("Successful connection to cluster", testutils.DatastoreK8s, func(k8sConfig apiconfig.CalicoAPIConfig) {

		ctx := context.Background()
		var err error
		var etcdBackend api.Client
		var k8sBackend api.Client
		var k8sClientset *kubernetes.Clientset
		var syncer api.Syncer
		var syncTester *testutils.SyncerTester

		isBuiltInService := func(name, namespace string) bool {
			return (name == "kubernetes" && namespace == "default") || (name == "kube-controller-manager" && namespace == "kube-system")
		}

		removeTestK8sConfig := func() {
			if k8sBackend != nil {
				// Clean up any endpoints left over by the test.
				eps, err := k8sClientset.CoreV1().Endpoints("").List(ctx, metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())

				for _, ep := range eps.Items {
					if isBuiltInService(ep.Name, ep.Namespace) {
						continue
					}
					err = k8sClientset.CoreV1().Endpoints(ep.Namespace).Delete(ctx, ep.Name, metav1.DeleteOptions{})
					Expect(err).NotTo(HaveOccurred())
				}

				// Clean up any services left over by the test.
				svcs, err := k8sClientset.CoreV1().Services("").List(ctx, metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())

				for _, svc := range svcs.Items {
					if isBuiltInService(svc.Name, svc.Namespace) {
						continue
					}
					err = k8sClientset.CoreV1().Services(svc.Namespace).Delete(ctx, svc.Name, metav1.DeleteOptions{})
					Expect(err).NotTo(HaveOccurred())
				}

			}
		}

		// Function to remove default k8s services and endpoints from the syncer, and to remove syncer status
		// updates (since these are tested in the felix tests).
		updateSanitizer := func(u *api.Update) *api.Update {
			var rk model.ResourceKey
			switch k := u.Key.(type) {
			case model.ResourceKey:
				rk = k
			case model.RemoteClusterResourceKey:
				rk = k.ResourceKey
			case model.RemoteClusterStatusKey:
				return nil
			default:
				return u
			}

			if (rk.Kind == apiv3.KindK8sEndpoints || rk.Kind == apiv3.KindK8sService) && isBuiltInService(rk.Name, rk.Namespace) {
				return nil
			}
			return u
		}

		BeforeEach(func() {
			// Create the local backend client and clean the datastore.
			etcdBackend, err = backend.NewClient(etcdConfig)
			Expect(err).NotTo(HaveOccurred())
			etcdBackend.Clean()

			// Create the remote backend client to clean the datastore.
			k8sBackend, err = backend.NewClient(k8sConfig)
			Expect(err).NotTo(HaveOccurred())
			k8sClientset = k8sBackend.(*k8s.KubeClient).ClientSet
			k8sBackend.Clean()
			removeTestK8sConfig()
		})

		AfterEach(func() {
			if syncer != nil {
				syncer.Stop()
				syncer = nil
			}

			if etcdBackend != nil {
				etcdBackend.Clean()
				etcdBackend.Close()
				etcdBackend = nil
			}
			if k8sBackend != nil {
				removeTestK8sConfig()
				k8sBackend.Clean()
				k8sBackend.Close()
				k8sBackend = nil
				k8sClientset = nil
			}
		})

		It("Should connect to the remote cluster and sync the remote data", func() {
			By("Creating the local syncer using etcd for config and k8s for services and endpoints")
			// Create the syncer
			syncTester = testutils.NewSyncerTester()
			syncer = federationsyncer.New(etcdBackend, k8sClientset, syncTester)
			syncer.Start()

			By("Checking status is updated to sync'd at start of day")
			syncTester.ExpectStatusUpdate(api.WaitForDatastore)
			syncTester.ExpectStatusUpdate(api.ResyncInProgress)
			syncTester.ExpectStatusUpdate(api.InSync)

			By("Checking we received no events so far")
			syncTester.ExpectUpdatesSanitized([]api.Update{}, false, updateSanitizer)

			By("Configuring some services and endpoints")
			s1, err := k8sClientset.CoreV1().Services("namespace-1").Create(ctx,
				&kapiv1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "service1", Namespace: "namespace-1"},
					Spec: kapiv1.ServiceSpec{
						Ports: []kapiv1.ServicePort{
							{
								Name:       "nginx",
								Port:       80,
								TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "nginx"},
								Protocol:   kapiv1.ProtocolTCP,
							},
						},
					},
				},
				metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			e1, err := k8sClientset.CoreV1().Endpoints("namespace-1").Create(ctx,
				&kapiv1.Endpoints{
					ObjectMeta: metav1.ObjectMeta{Name: "service1", Namespace: "namespace-1"},
					Subsets:    []kapiv1.EndpointSubset{},
				},
				metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			s2, err := k8sClientset.CoreV1().Services("namespace-2").Create(ctx,
				&kapiv1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "service1000", Namespace: "namespace-2"},
					Spec: kapiv1.ServiceSpec{
						Ports: []kapiv1.ServicePort{
							{
								Name:       "nginx",
								Port:       8000,
								TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: 80},
								Protocol:   kapiv1.ProtocolUDP,
							},
						},
					},
				},
				metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Checking we received updates for the local services and endpoints")
			expectedUpdates := []api.Update{
				{
					KVPair: model.KVPair{
						Key: model.ResourceKey{
							Kind:      apiv3.KindK8sService,
							Name:      "service1",
							Namespace: "namespace-1",
						},
						Value:    s1,
						Revision: s1.ResourceVersion,
					},
					UpdateType: api.UpdateTypeKVNew,
				},
				{
					KVPair: model.KVPair{
						Key: model.ResourceKey{
							Kind:      apiv3.KindK8sEndpoints,
							Name:      "service1",
							Namespace: "namespace-1",
						},
						Value:    e1,
						Revision: e1.ResourceVersion,
					},
					UpdateType: api.UpdateTypeKVNew,
				},
				{
					KVPair: model.KVPair{
						Key: model.ResourceKey{
							Kind:      apiv3.KindK8sService,
							Name:      "service1000",
							Namespace: "namespace-2",
						},
						Value:    s2,
						Revision: s2.ResourceVersion,
					},
					UpdateType: api.UpdateTypeKVNew,
				},
			}
			syncTester.ExpectUpdatesSanitized(expectedUpdates, false, updateSanitizer)

			By("Configuring the RemoteClusterConfiguration for the remote")
			rcc := &apiv3.RemoteClusterConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "remote-cluster"}}
			rcc.Spec.DatastoreType = string(k8sConfig.Spec.DatastoreType)
			rcc.Spec.Kubeconfig = k8sConfig.Spec.Kubeconfig
			rcc.Spec.K8sAPIEndpoint = k8sConfig.Spec.K8sAPIEndpoint
			rcc.Spec.K8sKeyFile = k8sConfig.Spec.K8sKeyFile
			rcc.Spec.K8sCertFile = k8sConfig.Spec.K8sCertFile
			rcc.Spec.K8sCAFile = k8sConfig.Spec.K8sCAFile
			rcc.Spec.K8sAPIToken = k8sConfig.Spec.K8sAPIToken
			rcc.Spec.K8sInsecureSkipTLSVerify = k8sConfig.Spec.K8sInsecureSkipTLSVerify
			_, outError := etcdBackend.Create(ctx, &model.KVPair{
				Key: model.ResourceKey{
					Kind: apiv3.KindRemoteClusterConfiguration,
					Name: "remote-cluster",
				},
				Value: rcc,
			})
			Expect(outError).NotTo(HaveOccurred())

			By("Configuring the RemoteClusterConfiguration with etcd only configuration")
			rcc = &apiv3.RemoteClusterConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "remote-cluster-etcd-only"}}
			rcc.Spec.DatastoreType = "etcdv3"
			_, outError = etcdBackend.Create(ctx, &model.KVPair{
				Key: model.ResourceKey{
					Kind: apiv3.KindRemoteClusterConfiguration,
					Name: "remote-cluster-etcd-only",
				},
				Value: rcc,
			})
			Expect(outError).NotTo(HaveOccurred())

			By("Checking we received updates for the remote services and endpoints (same as local k8s ones)")
			// Since we are using the same k8s datastore, the remote endpoints will be the same as the local ones
			// except the key will be a RemoteClusterResourceKey.
			remoteExpectedUpdates := []api.Update{}
			for i := range expectedUpdates {
				remoteExpectedUpdates = append(remoteExpectedUpdates, api.Update{
					KVPair: model.KVPair{
						Key: model.RemoteClusterResourceKey{
							ResourceKey: expectedUpdates[i].Key.(model.ResourceKey),
							Cluster:     "remote-cluster",
						},
						Value:    expectedUpdates[i].Value,
						Revision: expectedUpdates[i].Revision,
					},
					UpdateType: expectedUpdates[i].UpdateType,
				})
			}
			syncTester.ExpectUpdatesSanitized(remoteExpectedUpdates, false, updateSanitizer)

			By("Deleting service1000")
			err = k8sClientset.CoreV1().Services("namespace-2").Delete(ctx, "service1000", metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Checking we received updates for both the local and remote service")
			expectedUpdates = []api.Update{
				{
					KVPair: model.KVPair{
						Key: model.ResourceKey{
							Kind:      apiv3.KindK8sService,
							Name:      "service1000",
							Namespace: "namespace-2",
						},
					},
					UpdateType: api.UpdateTypeKVDeleted,
				},
				{
					KVPair: model.KVPair{
						Key: model.RemoteClusterResourceKey{
							ResourceKey: model.ResourceKey{
								Kind:      apiv3.KindK8sService,
								Name:      "service1000",
								Namespace: "namespace-2",
							},
							Cluster: "remote-cluster",
						},
					},
					UpdateType: api.UpdateTypeKVDeleted,
				},
			}
			syncTester.ExpectUpdatesSanitized(expectedUpdates, false, updateSanitizer)
		})
	})
})
var _ = testutils.E2eDatastoreDescribe("Remote cluster federationsyncer tests", testutils.DatastoreEtcdV3, func(etcdConfig apiconfig.CalicoAPIConfig) {
	testutils.E2eDatastoreDescribe("Successfuly connect to cluster with secret config", testutils.DatastoreK8sInline, func(k8sConfig apiconfig.CalicoAPIConfig) {

		log.SetLevel(log.DebugLevel)

		ctx := context.Background()
		var err error
		var etcdBackend api.Client
		var k8sBackend api.Client
		var k8sClientset *kubernetes.Clientset
		var syncer api.Syncer
		var syncTester *testutils.SyncerTester
		var expectedUpdates []api.Update

		isBuiltInService := func(name, namespace string) bool {
			return (name == "kubernetes" && namespace == "default") || (name == "kube-controller-manager" && namespace == "kube-system")
		}

		removeTestK8sConfig := func() {
			if k8sBackend != nil {
				// Clean up any endpoints left over by the test.
				eps, err := k8sClientset.CoreV1().Endpoints("").List(ctx, metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())

				for _, ep := range eps.Items {
					if isBuiltInService(ep.Name, ep.Namespace) {
						continue
					}
					err = k8sClientset.CoreV1().Endpoints(ep.Namespace).Delete(ctx, ep.Name, metav1.DeleteOptions{})
					Expect(err).NotTo(HaveOccurred())
				}

				// Clean up any services left over by the test.
				svcs, err := k8sClientset.CoreV1().Services("").List(ctx, metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())

				for _, svc := range svcs.Items {
					if isBuiltInService(svc.Name, svc.Namespace) {
						continue
					}
					err = k8sClientset.CoreV1().Services(svc.Namespace).Delete(ctx, svc.Name, metav1.DeleteOptions{})
					Expect(err).NotTo(HaveOccurred())
				}

			}
		}

		// Function to remove default k8s services and endpoints from the syncer, and to remove syncer status
		// updates (since these are tested in the felix tests).
		updateSanitizer := func(u *api.Update) *api.Update {
			var rk model.ResourceKey
			switch k := u.Key.(type) {
			case model.ResourceKey:
				rk = k
			case model.RemoteClusterResourceKey:
				rk = k.ResourceKey
			default:
				return u
			}

			if (rk.Kind == apiv3.KindK8sEndpoints || rk.Kind == apiv3.KindK8sService) && isBuiltInService(rk.Name, rk.Namespace) {
				return nil
			}
			return u
		}

		BeforeEach(func() {
			// Create the local backend client and clean the datastore.
			etcdBackend, err = backend.NewClient(etcdConfig)
			Expect(err).NotTo(HaveOccurred())
			etcdBackend.Clean()

			// Create the remote backend client to clean the datastore.
			k8sBackend, err = backend.NewClient(k8sConfig)
			Expect(err).NotTo(HaveOccurred())
			k8sClientset = k8sBackend.(*k8s.KubeClient).ClientSet
			k8sBackend.Clean()
			removeTestK8sConfig()

			By("Creating the local syncer using etcd for config and k8s for services and endpoints")
			// Create the syncer
			syncTester = testutils.NewSyncerTester()
			syncer = federationsyncer.New(etcdBackend, k8sClientset, syncTester)
			syncer.Start()

			By("Checking status is updated to sync'd at start of day")
			syncTester.ExpectStatusUpdate(api.WaitForDatastore)
			syncTester.ExpectStatusUpdate(api.ResyncInProgress)
			syncTester.ExpectStatusUpdate(api.InSync)

			By("Checking we received no events so far")
			syncTester.ExpectUpdatesSanitized([]api.Update{}, false, updateSanitizer)

			By("Configuring some services and endpoints")
			s1, err := k8sClientset.CoreV1().Services("namespace-1").Create(ctx,
				&kapiv1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "service1", Namespace: "namespace-1"},
					Spec: kapiv1.ServiceSpec{
						Ports: []kapiv1.ServicePort{
							{
								Name:       "nginx",
								Port:       80,
								TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "nginx"},
								Protocol:   kapiv1.ProtocolTCP,
							},
						},
					},
				},
				metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			e1, err := k8sClientset.CoreV1().Endpoints("namespace-1").Create(ctx,
				&kapiv1.Endpoints{
					ObjectMeta: metav1.ObjectMeta{Name: "service1", Namespace: "namespace-1"},
					Subsets:    []kapiv1.EndpointSubset{},
				},
				metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			s2, err := k8sClientset.CoreV1().Services("namespace-2").Create(ctx,
				&kapiv1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "service1000", Namespace: "namespace-2"},
					Spec: kapiv1.ServiceSpec{
						Ports: []kapiv1.ServicePort{
							{
								Name:       "nginx",
								Port:       8000,
								TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: 80},
								Protocol:   kapiv1.ProtocolUDP,
							},
						},
					},
				},
				metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Checking we received updates for the local services and endpoints")
			expectedUpdates = []api.Update{
				{
					KVPair: model.KVPair{
						Key: model.ResourceKey{
							Kind:      apiv3.KindK8sService,
							Name:      "service1",
							Namespace: "namespace-1",
						},
						Value:    s1,
						Revision: s1.ResourceVersion,
					},
					UpdateType: api.UpdateTypeKVNew,
				},
				{
					KVPair: model.KVPair{
						Key: model.ResourceKey{
							Kind:      apiv3.KindK8sEndpoints,
							Name:      "service1",
							Namespace: "namespace-1",
						},
						Value:    e1,
						Revision: e1.ResourceVersion,
					},
					UpdateType: api.UpdateTypeKVNew,
				},
				{
					KVPair: model.KVPair{
						Key: model.ResourceKey{
							Kind:      apiv3.KindK8sService,
							Name:      "service1000",
							Namespace: "namespace-2",
						},
						Value:    s2,
						Revision: s2.ResourceVersion,
					},
					UpdateType: api.UpdateTypeKVNew,
				},
			}
			syncTester.ExpectUpdatesSanitized(expectedUpdates, false, updateSanitizer)
		})

		AfterEach(func() {
			_, _ = etcdBackend.Delete(ctx,
				model.ResourceKey{
					Kind: apiv3.KindRemoteClusterConfiguration,
					Name: "remote-cluster",
				}, "",
			)
			_ = k8sClientset.CoreV1().Secrets("namespace-1").Delete(
				ctx, "remote-cluster-config", metav1.DeleteOptions{})
			removeTestK8sConfig()

			if etcdBackend != nil {
				etcdBackend.Clean()
				etcdBackend.Close()
				etcdBackend = nil
			}
			if k8sBackend != nil {
				k8sBackend.Clean()
				k8sBackend.Close()
				k8sBackend = nil
				k8sClientset = nil
			}
			if syncer != nil {
				syncer.Stop()
				syncer = nil
			}
		})

		createSecret := func() {
			By("Creating secret for the RemoteClusterConfiguration for the remote")
			_, err = k8sClientset.CoreV1().Secrets("namespace-1").Create(ctx,
				&kapiv1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "remote-cluster-config", Namespace: "namespace-1"},
					StringData: map[string]string{
						"datastoreType": string(k8sConfig.Spec.DatastoreType),
						"kubeconfig":    k8sConfig.Spec.KubeconfigInline,
					},
				},
				metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
		}
		createRCC := func() {
			By("Configuring the RemoteClusterConfiguration for the remote")
			rcc := &apiv3.RemoteClusterConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "remote-cluster"}}
			rcc.Spec.ClusterAccessSecret = &kapiv1.ObjectReference{
				Kind:      reflect.TypeOf(kapiv1.Secret{}).String(),
				Namespace: "namespace-1",
				Name:      "remote-cluster-config",
			}
			_, outError := etcdBackend.Create(ctx, &model.KVPair{
				Key: model.ResourceKey{
					Kind: apiv3.KindRemoteClusterConfiguration,
					Name: "remote-cluster",
				},
				Value: rcc,
			})
			Expect(outError).NotTo(HaveOccurred())
		}

		checkForRemoteUpdates := func() {
			By("Checking we received updates for the remote services and endpoints (same as local k8s ones)")
			// Since we are using the same k8s datastore, the remote endpoints will be the same as the local ones
			// except the key will be a RemoteClusterResourceKey.
			remoteExpectedUpdates := []api.Update{
				{
					KVPair: model.KVPair{
						Key: model.RemoteClusterStatusKey{Name: "remote-cluster"},
						Value: &model.RemoteClusterStatus{
							Status: model.RemoteClusterConnecting,
						},
					},
					UpdateType: api.UpdateTypeKVNew,
				},
				{
					KVPair: model.KVPair{
						Key: model.RemoteClusterStatusKey{Name: "remote-cluster"},
						Value: &model.RemoteClusterStatus{
							Status: model.RemoteClusterResyncInProgress,
						},
					},
					UpdateType: api.UpdateTypeKVUpdated,
				},
				{
					KVPair: model.KVPair{
						Key: model.RemoteClusterStatusKey{Name: "remote-cluster"},
						Value: &model.RemoteClusterStatus{
							Status: model.RemoteClusterInSync,
						},
					},
					UpdateType: api.UpdateTypeKVUpdated,
				},
			}
			for i := range expectedUpdates {
				remoteExpectedUpdates = append(remoteExpectedUpdates, api.Update{
					KVPair: model.KVPair{
						Key: model.RemoteClusterResourceKey{
							ResourceKey: expectedUpdates[i].Key.(model.ResourceKey),
							Cluster:     "remote-cluster",
						},
						Value:    expectedUpdates[i].Value,
						Revision: expectedUpdates[i].Revision,
					},
					UpdateType: expectedUpdates[i].UpdateType,
				})
			}
			syncTester.ExpectUpdatesSanitized(remoteExpectedUpdates, false, updateSanitizer)
		}

		Describe("Create create events when adding config", func() {
			It("should have events after Secret then RCC created", func() {
				createSecret()
				By("Checking we received no events so far")
				createRCC()
				checkForRemoteUpdates()
			})

			It("should see events after RCC then Secret created", func() {
				createRCC()
				By("Checking we received no events so far")
				syncTester.ExpectUpdatesSanitized([]api.Update{
					{
						KVPair: model.KVPair{
							Key: model.RemoteClusterStatusKey{Name: "remote-cluster"},
							Value: &model.RemoteClusterStatus{
								Status: model.RemoteClusterConnecting,
							},
						},
						UpdateType: api.UpdateTypeKVNew,
					},
				}, false, updateSanitizer)
				createSecret()
				remoteExpectedUpdates := []api.Update{
					{
						KVPair: model.KVPair{
							Key: model.RemoteClusterStatusKey{Name: "remote-cluster"},
							Value: &model.RemoteClusterStatus{
								Status: model.RemoteClusterResyncInProgress,
							},
						},
						UpdateType: api.UpdateTypeKVUpdated,
					},
					{
						KVPair: model.KVPair{
							Key: model.RemoteClusterStatusKey{Name: "remote-cluster"},
							Value: &model.RemoteClusterStatus{
								Status: model.RemoteClusterInSync,
							},
						},
						UpdateType: api.UpdateTypeKVUpdated,
					},
				}
				for i := range expectedUpdates {
					remoteExpectedUpdates = append(remoteExpectedUpdates, api.Update{
						KVPair: model.KVPair{
							Key: model.RemoteClusterResourceKey{
								ResourceKey: expectedUpdates[i].Key.(model.ResourceKey),
								Cluster:     "remote-cluster",
							},
							Value:    expectedUpdates[i].Value,
							Revision: expectedUpdates[i].Revision,
						},
						UpdateType: expectedUpdates[i].UpdateType,
					})
				}
				syncTester.ExpectUpdatesSanitized(remoteExpectedUpdates, false, updateSanitizer)
			})
		})

		Describe("Create delete events when cleaning config", func() {
			BeforeEach(func() {
				createSecret()
				createRCC()
				checkForRemoteUpdates()
			})

			It("should create delete event for RCC when deleting the RCC", func() {
				By("Deleting the RCC for remote-cluster")
				_, outError := etcdBackend.Delete(ctx,
					model.ResourceKey{
						Kind: apiv3.KindRemoteClusterConfiguration,
						Name: "remote-cluster",
					}, "",
				)
				Expect(outError).NotTo(HaveOccurred())
				remoteExpectedDeletes := []api.Update{
					{
						KVPair: model.KVPair{
							Key: model.RemoteClusterStatusKey{Name: "remote-cluster"},
						},
						UpdateType: api.UpdateTypeKVDeleted,
					},
				}
				for i := range expectedUpdates {
					remoteExpectedDeletes = append(remoteExpectedDeletes, api.Update{
						KVPair: model.KVPair{
							Key: model.RemoteClusterResourceKey{
								ResourceKey: expectedUpdates[i].Key.(model.ResourceKey),
								Cluster:     "remote-cluster",
							},
						},
						UpdateType: api.UpdateTypeKVDeleted,
					})
				}

				By("Expecting deletes for the remote-cluster and resources")
				syncTester.ExpectUpdatesSanitized(remoteExpectedDeletes, false, updateSanitizer)
			})

			It("should create delete events when deleting the access secret", func() {
				By("deleting remote-cluster-config secret")
				err = k8sClientset.CoreV1().Secrets("namespace-1").Delete(
					ctx, "remote-cluster-config", metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred())

				remoteExpectedDeletes := []api.Update{
					{
						KVPair: model.KVPair{
							Key: model.RemoteClusterStatusKey{Name: "remote-cluster"},
							Value: &model.RemoteClusterStatus{
								Status: model.RemoteClusterConfigIncomplete,
								Error:  "Config is incomplete, stopping watch remote",
							},
						},
						UpdateType: api.UpdateTypeKVUpdated,
					},
				}
				for i := range expectedUpdates {
					remoteExpectedDeletes = append(remoteExpectedDeletes, api.Update{
						KVPair: model.KVPair{
							Key: model.RemoteClusterResourceKey{
								ResourceKey: expectedUpdates[i].Key.(model.ResourceKey),
								Cluster:     "remote-cluster",
							},
						},
						UpdateType: api.UpdateTypeKVDeleted,
					})
				}

				By("expecting an incomplete remote update and delete for resources")
				syncTester.ExpectUpdatesSanitized(remoteExpectedDeletes, false, updateSanitizer)
				By("Deleting the RCC for remote-cluster")
				_, outError := etcdBackend.Delete(ctx,
					model.ResourceKey{
						Kind: apiv3.KindRemoteClusterConfiguration,
						Name: "remote-cluster",
					}, "",
				)
				Expect(outError).NotTo(HaveOccurred())
				expectedDeletes := []api.Update{
					{
						KVPair: model.KVPair{
							Key: model.RemoteClusterStatusKey{Name: "remote-cluster"},
						},
						UpdateType: api.UpdateTypeKVDeleted,
					},
				}
				By("expecting the delete for the remote-cluster RCC")
				syncTester.ExpectUpdatesSanitized(expectedDeletes, false, updateSanitizer)
			})
		})
	})
})
