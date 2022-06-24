// Copyright (c) 2018-2020 Tigera, Inc. All rights reserved.

package fv_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"time"

	v1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/calico/felix/fv/containers"
	"github.com/projectcalico/calico/felix/fv/infrastructure"
	"github.com/projectcalico/calico/kube-controllers/pkg/controllers/federatedservices"
	"github.com/projectcalico/calico/kube-controllers/tests/testutils"
	"github.com/projectcalico/calico/libcalico-go/lib/apiconfig"
	client "github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/calico/libcalico-go/lib/options"
)

var (
	eventuallyTimeout   = "15s"
	eventuallyPoll      = "500ms"
	consistentlyTimeout = "2s"
	consistentlyPoll    = "500ms"

	node1Name = "node-1"
	node2Name = "node-2"
	ns1Name   = "ns-1"
	ns2Name   = "ns-2"
	ctx       = context.Background()

	// svc1 has the federation label
	svc1 = &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"federate": "yes",
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name:     "port1",
					Port:     1234,
					Protocol: v1.ProtocolUDP,
				},
				{
					Name:     "port2",
					Port:     1234,
					Protocol: v1.ProtocolTCP,
				},
				{
					Name:     "port3",
					Port:     1200,
					Protocol: v1.ProtocolTCP,
				},
			},
		},
	}

	eps1 = &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "1.0.0.1",
						NodeName: &node1Name,
					},
				},
				NotReadyAddresses: []v1.EndpointAddress{
					{
						IP:       "1.0.0.2",
						NodeName: &node2Name,
						TargetRef: &v1.ObjectReference{
							Kind:      "Pod",
							Namespace: ns1Name,
							Name:      "pod1",
						},
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "1.0.0.1",
						NodeName: &node1Name,
					},
				},
				NotReadyAddresses: []v1.EndpointAddress{
					{
						IP:       "1.0.0.2",
						NodeName: &node2Name,
						TargetRef: &v1.ObjectReference{
							Kind:      "Pod",
							Namespace: ns1Name,
							Name:      "pod1",
						},
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port3",
						Port:     1200,
						Protocol: v1.ProtocolTCP,
					},
				},
			},
			{
				Addresses: []v1.EndpointAddress{
					{
						IP: "2.0.0.1",
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port2",
						Port:     1234,
						Protocol: v1.ProtocolTCP,
					},
				},
			},
			{
				Addresses: []v1.EndpointAddress{
					{
						IP: "3.0.0.1",
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port3",
						Port:     1200,
						Protocol: v1.ProtocolTCP,
					},
				},
			},
		},
	}

	// svc2 has the federation label and a different set of endpoints from svc1
	svc2 = &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"federate": "yes",
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name:     "port1",
					Port:     1234,
					Protocol: v1.ProtocolUDP,
				},
				{
					Name:     "port2",
					Port:     1234,
					Protocol: v1.ProtocolTCP,
				},
				{
					Name:     "port3",
					Port:     1200,
					Protocol: v1.ProtocolTCP,
				},
			},
		},
	}

	eps2 = &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"federate": "yes",
			},
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.10.10.1",
						NodeName: &node1Name,
					},
				},
				NotReadyAddresses: []v1.EndpointAddress{
					{
						IP:       "10.10.10.2",
						NodeName: &node2Name,
						TargetRef: &v1.ObjectReference{
							Kind:      "Pod",
							Namespace: ns1Name,
							Name:      "pod1",
						},
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
		},
	}
)

var _ = Describe("[federation] kube-controllers Federated Services FV tests", func() {
	var (
		localEtcd            *containers.Container
		localApiserver       *containers.Container
		localCalicoClient    client.Interface
		localK8sClient       *kubernetes.Clientset
		federationController *containers.Container
		remoteEtcd           *containers.Container
		remoteApiserver      *containers.Container
		remoteK8sClient      *kubernetes.Clientset
		remoteKubeconfig     string
		localKubeconfig      string
	)

	getSubsets := func(namespace, name string) []v1.EndpointSubset {
		eps, err := localK8sClient.CoreV1().Endpoints(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil && kerrors.IsNotFound(err) {
			return nil
		}
		Expect(err).NotTo(HaveOccurred())
		return federatedservices.GetOrderedEndpointSubsets(eps.Subsets)
	}

	getSubsetsFn := func(namespace, name string) func() []v1.EndpointSubset {
		return func() []v1.EndpointSubset {
			return getSubsets(namespace, name)
		}
	}

	setup := func(isCalicoEtcdDatastore bool) {
		// Create local etcd and run the local apiserver. Wait for the API server to come online.
		localEtcd = testutils.RunEtcd()
		localApiserver = testutils.RunK8sApiserver(localEtcd.IP)

		// Write out a kubeconfig file for the local API server, and create a k8s client.
		lkubeconfig, err := ioutil.TempFile("", "ginkgo-localcluster")
		Expect(err).NotTo(HaveOccurred())
		// Change ownership of the kubeconfig file  so it is accessible by all users in the container
		err = lkubeconfig.Chmod(os.ModePerm)
		Expect(err).NotTo(HaveOccurred())
		localKubeconfig = lkubeconfig.Name()
		data := testutils.BuildKubeconfig(localApiserver.IP)
		_, err = lkubeconfig.Write([]byte(data))
		Expect(err).NotTo(HaveOccurred())
		localK8sClient, err = testutils.GetK8sClient(localKubeconfig)
		Expect(err).NotTo(HaveOccurred())

		// Create the appropriate local Calico client depending on whether this is an etcd or kdd test.
		if isCalicoEtcdDatastore {
			localCalicoClient = testutils.GetCalicoClient(apiconfig.EtcdV3, localEtcd.IP, localKubeconfig)
		} else {
			localCalicoClient = testutils.GetCalicoKubernetesClient(localKubeconfig)
		}

		// Create remote etcd and run the remote apiserver.
		remoteEtcd = testutils.RunEtcd()
		remoteApiserver = testutils.RunK8sApiserver(remoteEtcd.IP)

		// Write out a kubeconfig file for the remote API server.
		rkubeconfig, err := ioutil.TempFile("", "ginkgo-remotecluster")
		Expect(err).NotTo(HaveOccurred())
		// Change ownership of the kubeconfig file  so it is accessible by all users in the container
		err = rkubeconfig.Chmod(os.ModePerm)
		Expect(err).NotTo(HaveOccurred())
		remoteKubeconfig = rkubeconfig.Name()
		data = testutils.BuildKubeconfig(remoteApiserver.IP)
		_, err = rkubeconfig.Write([]byte(data))
		Expect(err).NotTo(HaveOccurred())
		remoteK8sClient, err = testutils.GetK8sClient(remoteKubeconfig)
		Expect(err).NotTo(HaveOccurred())

		// Wait for the api servers to be available.
		Eventually(func() error {
			_, err := localK8sClient.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
			return err
		}, eventuallyTimeout, eventuallyPoll).Should(BeNil())
		Eventually(func() error {
			_, err := remoteK8sClient.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
			return err
		}, eventuallyTimeout, eventuallyPoll).Should(BeNil())

		if !isCalicoEtcdDatastore {
			// Apply the necessary CRDs. There can somtimes be a delay between starting
			// the API server and when CRDs are apply-able, so retry here.
			apply := func() error {
				out, err := localApiserver.ExecOutput("kubectl", "apply", "-f", "/crds/")
				if err != nil {
					return fmt.Errorf("%s: %s", err, out)
				}
				return nil
			}
			Eventually(apply, 10*time.Second).ShouldNot(HaveOccurred())
		}

		// Run the federation controller on the local cluster.
		federationController = testutils.RunFederationController(
			localEtcd.IP,
			localKubeconfig,
			[]string{remoteKubeconfig},
			isCalicoEtcdDatastore,
		)

		// Create two test namespaces in both kubernetes clusters.
		ns := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns1Name,
			},
			Spec: v1.NamespaceSpec{},
		}
		_, err = localK8sClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		_, err = remoteK8sClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		ns = &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns2Name,
			},
			Spec: v1.NamespaceSpec{},
		}
		_, err = localK8sClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		_, err = remoteK8sClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
	}

	makeService := func(base *v1.Service, name string, prev *v1.Service) *v1.Service {
		copy := base.DeepCopy()
		if prev == nil {
			copy.Name = name
			return copy
		}
		copy.ObjectMeta = *prev.ObjectMeta.DeepCopy()
		return copy
	}
	makeEndpoints := func(base *v1.Endpoints, name string, prev *v1.Endpoints) *v1.Endpoints {
		copy := base.DeepCopy()
		if prev == nil {
			copy.Name = name
			return copy
		}
		copy.ObjectMeta = *prev.ObjectMeta.DeepCopy()
		return copy
	}

	AfterEach(func() {
		By("Cleaning up after the test should complete")
		federationController.Stop()
		federationController.Remove()
		localApiserver.Stop()
		localEtcd.Stop()
		remoteApiserver.Stop()
		remoteEtcd.Stop()
		os.Remove(remoteKubeconfig)
		os.Remove(localKubeconfig)
	})

	DescribeTable("Test with specific local Calico datastore type", func(isCalicoEtcdDatastore bool) {

		By("Setting up the local and remote clusters")
		setup(isCalicoEtcdDatastore)

		By("Creating two identical backing services and endpoints")
		svcBacking1, err := localK8sClient.CoreV1().Services(ns1Name).Create(ctx, makeService(svc1, "backing1", nil), metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		_, err = localK8sClient.CoreV1().Endpoints(ns1Name).Create(ctx, makeEndpoints(eps1, "backing1", nil), metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		_, err = localK8sClient.CoreV1().Services(ns1Name).Create(ctx, makeService(svc1, "backing2", nil), metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		epsBacking2, err := localK8sClient.CoreV1().Endpoints(ns1Name).Create(ctx, makeEndpoints(eps1, "backing2", nil), metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Creating a service with endpoints in a different namespace but the correct labels")
		_, err = localK8sClient.CoreV1().Services(ns2Name).Create(ctx, makeService(svc2, "wrongns", nil), metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		_, err = localK8sClient.CoreV1().Endpoints(ns2Name).Create(ctx, makeEndpoints(eps2, "wrongns", nil), metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Creating a federated service which matches the two indentical backing services")
		fedCfg, err := localK8sClient.CoreV1().Services(ns1Name).Create(ctx, &v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "federated",
				Namespace: ns1Name,
				Annotations: map[string]string{
					"federation.tigera.io/serviceSelector": "federate == 'yes'",
				},
			},
			Spec: v1.ServiceSpec{
				Ports: []v1.ServicePort{
					{
						Name:     "port1",
						Port:     8080,
						Protocol: v1.ProtocolUDP,
					},
					{
						Name:     "port2",
						Port:     80,
						Protocol: v1.ProtocolTCP,
					},
				},
			},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Checking the federated endpoints have not been created without a license applied")
		Consistently(getSubsetsFn(ns1Name, "federated"), consistentlyTimeout, consistentlyPoll).Should(BeNil())

		By("Applying a valid license to enable the federated services controller")
		infrastructure.ApplyValidLicense(localCalicoClient)

		By("Checking the federated endpoints contain the expected set ips/ports")
		eSubset := []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "1.0.0.1",
						NodeName: &node1Name,
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				NotReadyAddresses: []v1.EndpointAddress{
					{
						IP:       "1.0.0.2",
						NodeName: &node2Name,
						TargetRef: &v1.ObjectReference{
							Kind:      "Pod",
							Namespace: ns1Name,
							Name:      "pod1",
						},
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				Addresses: []v1.EndpointAddress{
					{
						IP: "2.0.0.1",
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port2",
						Port:     1234,
						Protocol: v1.ProtocolTCP,
					},
				},
			},
		}
		Eventually(getSubsetsFn(ns1Name, "federated"), eventuallyTimeout, eventuallyPoll).Should(Equal(eSubset))

		// Stop the federationController container so we can register the watch on Stdout
		federationController.Stop()
		watchChan := federationController.WatchStdoutFor(regexp.MustCompile("Received exit status [[:digit:]]*, restarting"))
		federationController.Start()

		By("Updating the license to an invalid license")
		infrastructure.ApplyExpiredLicense(localCalicoClient)
		Eventually(watchChan, 10*time.Second).Should(BeClosed())

		By("Updating back2 to have a different set of endpoints while the controller should be stopped")
		epsBacking2, err = localK8sClient.CoreV1().Endpoints(ns1Name).Update(ctx, makeEndpoints(eps2, "backing2", epsBacking2), metav1.UpdateOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		By("Checking the federated endpoints remain unchanged")
		Consistently(getSubsetsFn(ns1Name, "federated"), consistentlyTimeout, consistentlyPoll).Should(Equal(eSubset))

		By("Adding a valid license back")
		infrastructure.ApplyValidLicense(localCalicoClient)

		By("Updating backing2 to have a different set of endpoints")
		_, err = localK8sClient.CoreV1().Endpoints(ns1Name).Update(ctx, makeEndpoints(eps2, "backing2", epsBacking2), metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Checking the federated endpoints contain the expected set ips/ports [2]")
		Eventually(getSubsetsFn(ns1Name, "federated"), eventuallyTimeout, eventuallyPoll).Should(Equal([]v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "1.0.0.1",
						NodeName: &node1Name,
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				NotReadyAddresses: []v1.EndpointAddress{
					{
						IP:       "1.0.0.2",
						NodeName: &node2Name,
						TargetRef: &v1.ObjectReference{
							Kind:      "Pod",
							Namespace: ns1Name,
							Name:      "pod1",
						},
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.10.10.1",
						NodeName: &node1Name,
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				NotReadyAddresses: []v1.EndpointAddress{
					{
						IP:       "10.10.10.2",
						NodeName: &node2Name,
						TargetRef: &v1.ObjectReference{
							Kind:      "Pod",
							Namespace: ns1Name,
							Name:      "pod1",
						},
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				Addresses: []v1.EndpointAddress{
					{
						IP: "2.0.0.1",
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port2",
						Port:     1234,
						Protocol: v1.ProtocolTCP,
					},
				},
			},
		}))

		By("Updating backing1 to have no labels")
		svcBacking1.Labels = nil
		_, err = localK8sClient.CoreV1().Services(ns1Name).Update(ctx, svcBacking1, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Checking the federated endpoints contain the expected set ips/ports [3]")
		// Store this set of expected endpoints as we'll use it a few times below.
		es := []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.10.10.1",
						NodeName: &node1Name,
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				NotReadyAddresses: []v1.EndpointAddress{
					{
						IP:       "10.10.10.2",
						NodeName: &node2Name,
						TargetRef: &v1.ObjectReference{
							Kind:      "Pod",
							Namespace: ns1Name,
							Name:      "pod1",
						},
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
		}
		Eventually(getSubsetsFn(ns1Name, "federated"), eventuallyTimeout, eventuallyPoll).Should(Equal(es))

		By("Removing the federation annotation")
		fedCfg.Annotations = nil
		fedCfg, err = localK8sClient.CoreV1().Services(ns1Name).Update(ctx, fedCfg, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Checking the federated endpoints has been deleted")
		Eventually(getSubsetsFn(ns1Name, "federated"), eventuallyTimeout, eventuallyPoll).Should(BeNil())

		By("Adding the federation annotation back")
		fedCfg.Annotations = map[string]string{
			"federation.tigera.io/serviceSelector": "federate == 'yes'",
		}
		fedCfg, err = localK8sClient.CoreV1().Services(ns1Name).Update(ctx, fedCfg, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Checking the federated endpoints contain the expected set ips/ports [4]")
		Eventually(getSubsetsFn(ns1Name, "federated"), eventuallyTimeout, eventuallyPoll).Should(Equal(es))

		By("Removing the federation selector from the federated endpoints to disable controller updates")
		eps, err := localK8sClient.CoreV1().Endpoints(ns1Name).Get(ctx, "federated", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		eps.Annotations = nil
		_, err = localK8sClient.CoreV1().Endpoints(ns1Name).Update(ctx, eps, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Modifying the federation annotation to be a no match")
		fedCfg.Annotations = map[string]string{
			"federation.tigera.io/serviceSelector": "federate == 'idontthinkso'",
		}
		fedCfg, err = localK8sClient.CoreV1().Services(ns1Name).Update(ctx, fedCfg, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Checking the federated endpoints remains unchanged")
		Consistently(getSubsetsFn(ns1Name, "federated"), consistentlyTimeout, consistentlyPoll).Should(Equal(es))
		Expect(getSubsets(ns1Name, "federated")).ToNot(BeNil())

		By("Removing the federated endpoints completely")
		err = localK8sClient.CoreV1().Endpoints(ns1Name).Delete(ctx, "federated", metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Modifying the federation annotation again - but still is a no match")
		fedCfg.Annotations = map[string]string{
			"federation.tigera.io/serviceSelector": "foo == 'bar'",
		}
		fedCfg, err = localK8sClient.CoreV1().Services(ns1Name).Update(ctx, fedCfg, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Checking the federated endpoints is recreated but contains no subsets")
		Eventually(getSubsetsFn(ns1Name, "federated"), eventuallyTimeout, eventuallyPoll).ShouldNot(BeNil())
		Expect(getSubsets(ns1Name, "federated")).To(HaveLen(0))

		By("Adding the federation annotation back")
		fedCfg.Annotations = map[string]string{
			"federation.tigera.io/serviceSelector": "federate == 'yes'",
		}
		_, err = localK8sClient.CoreV1().Services(ns1Name).Update(ctx, fedCfg, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Checking the federated endpoints contain the expected set ips/ports [5]")
		Eventually(getSubsetsFn(ns1Name, "federated"), eventuallyTimeout, eventuallyPoll).Should(Equal(es))

		By("Adding backing service to the remote cluster")
		_, err = remoteK8sClient.CoreV1().Services(ns1Name).Create(ctx, makeService(svc1, "backing1", nil), metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		_, err = remoteK8sClient.CoreV1().Endpoints(ns1Name).Create(ctx, makeEndpoints(eps1, "backing1", nil), metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Adding remote cluster config")
		rcc, err := localCalicoClient.RemoteClusterConfigurations().Create(ctx, &apiv3.RemoteClusterConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-remote",
			},
			Spec: apiv3.RemoteClusterConfigurationSpec{
				DatastoreType: "kubernetes",
				KubeConfig: apiv3.KubeConfig{
					Kubeconfig: remoteKubeconfig,
				},
			},
		}, options.SetOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Checking the federated endpoints contain the expected set ips/ports [6]")
		// Any port that has a name in the target object, should be updated to include the remote cluster name.
		Eventually(getSubsetsFn(ns1Name, "federated"), eventuallyTimeout, eventuallyPoll).Should(Equal([]v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "1.0.0.1",
						NodeName: &node1Name,
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				NotReadyAddresses: []v1.EndpointAddress{
					{
						IP:       "1.0.0.2",
						NodeName: &node2Name,
						TargetRef: &v1.ObjectReference{
							Kind:      "Pod",
							Namespace: ns1Name,
							Name:      "my-remote/pod1",
						},
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.10.10.1",
						NodeName: &node1Name,
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				NotReadyAddresses: []v1.EndpointAddress{
					{
						IP:       "10.10.10.2",
						NodeName: &node2Name,
						TargetRef: &v1.ObjectReference{
							Kind:      "Pod",
							Namespace: ns1Name,
							Name:      "pod1",
						},
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				Addresses: []v1.EndpointAddress{
					{
						IP: "2.0.0.1",
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port2",
						Port:     1234,
						Protocol: v1.ProtocolTCP,
					},
				},
			},
		}))

		By("Deleting the local services")
		err = localK8sClient.CoreV1().Services(ns1Name).Delete(ctx, "backing1", metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())
		err = localK8sClient.CoreV1().Services(ns1Name).Delete(ctx, "backing2", metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())
		err = localK8sClient.CoreV1().Services(ns2Name).Delete(ctx, "wrongns", metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Checking the federated endpoints contain the expected set ips/ports [7]")
		Eventually(getSubsetsFn(ns1Name, "federated"), eventuallyTimeout, eventuallyPoll).Should(Equal([]v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "1.0.0.1",
						NodeName: &node1Name,
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				NotReadyAddresses: []v1.EndpointAddress{
					{
						IP:       "1.0.0.2",
						NodeName: &node2Name,
						TargetRef: &v1.ObjectReference{
							Kind:      "Pod",
							Namespace: ns1Name,
							Name:      "my-remote/pod1",
						},
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port1",
						Port:     1234,
						Protocol: v1.ProtocolUDP,
					},
				},
			},
			{
				Addresses: []v1.EndpointAddress{
					{
						IP: "2.0.0.1",
					},
				},
				Ports: []v1.EndpointPort{
					{
						Name:     "port2",
						Port:     1234,
						Protocol: v1.ProtocolTCP,
					},
				},
			},
		}))

		By("Deleting the RemoteClusterConfiguration")
		_, err = localCalicoClient.RemoteClusterConfigurations().Delete(ctx, "my-remote", options.DeleteOptions{ResourceVersion: rcc.ResourceVersion})
		Expect(err).NotTo(HaveOccurred())

		By("Checking the federated endpoints is present but contains no subsets")
		Eventually(getSubsetsFn(ns1Name, "federated"), eventuallyTimeout, eventuallyPoll).Should(HaveLen(0))
		Expect(getSubsets(ns1Name, "federated")).ToNot(BeNil())
	},
		Entry("etcd datastore", true),
		Entry("kubernetes datastore", false))
})
