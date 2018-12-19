// Copyright (c) 2018 Tigera, Inc. All rights reserved.

package main_windows_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"os"
	"time"

	"github.com/Microsoft/hcsshim"
	"github.com/containernetworking/cni/pkg/types/current"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/projectcalico/cni-plugin/internal/pkg/testutils"
	"github.com/projectcalico/cni-plugin/internal/pkg/utils"
	"github.com/projectcalico/cni-plugin/pkg/k8s"
	"github.com/projectcalico/cni-plugin/pkg/types"
	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	k8sconversion "github.com/projectcalico/libcalico-go/lib/backend/k8s/conversion"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/logutils"
	"github.com/projectcalico/libcalico-go/lib/names"
	"github.com/projectcalico/libcalico-go/lib/numorstring"
	"github.com/projectcalico/libcalico-go/lib/options"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func ensureNamespace(clientset *kubernetes.Clientset, name string) {
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	_, err := clientset.CoreV1().Namespaces().Create(ns)
	if errors.IsAlreadyExists(err) {
		return
	}
	Expect(err).NotTo(HaveOccurred())
}

func deleteNamespace(clientset *kubernetes.Clientset, name string) {
	err := clientset.CoreV1().Namespaces().Delete(name, &metav1.DeleteOptions{})
	if err != nil {
		panic(err)
	}
}

func createExternalNetwork() {
	req := map[string]interface{}{
		"Name": "External",
		"Type": "L2Bridge",
		"Subnets": []interface{}{
			map[string]interface{}{
				"AddressPrefix":  "172.10.20.192/26",
				"GatewayAddress": "172.10.20.193",
			},
		},
	}

	reqStr, err := json.Marshal(req)
	if err != nil {
		log.Errorf("Error in converting to json format")
		panic(err)
	}

	log.Infof("Attempting to create HNS network, request: %v", string(reqStr))
	_, err = hcsshim.HNSNetworkRequest("POST", "", string(reqStr))
	if err != nil {
		log.Infof("Failed to create external network :%v", err)
	}
}

var _ = Describe("Kubernetes CNI tests", func() {
	var hostname string
	var ctx context.Context
	var calicoClient client.Interface
	var err error
	BeforeSuite(func() {
		// Create dummy external network
		createExternalNetwork()
		// Create a random seed
		rand.Seed(time.Now().UTC().UnixNano())
		log.SetFormatter(&logutils.Formatter{})
		log.AddHook(&logutils.ContextHook{})
		log.SetOutput(GinkgoWriter)
		log.SetLevel(log.InfoLevel)
		hostname, _ = names.Hostname()
		ctx = context.Background()
		calicoClient, err = client.NewFromEnv()
		if err != nil {
			panic(err)
		}
	})

	BeforeEach(func() {
		testutils.WipeEtcd()
	})

	utils.ConfigureLogging("info")
	cniVersion := os.Getenv("CNI_SPEC_VERSION")

	Context("using host-local IPAM", func() {
		var nsName, name string
		var clientset *kubernetes.Clientset
		netconf := fmt.Sprintf(`
	   		{
	   			"cniVersion": "%s",
	   			"name": "net1",
	   			"type": "calico",
	   			"etcd_endpoints": "%s",
	   			"datastore_type": "%s",
	   			"windows_use_single_network":true,
	   			"ipam": {
	   				"type": "host-local",
	   				"subnet": "10.254.112.0/20"
	   			},
	   			"kubernetes": {
	   				"k8s_api_root": "%s",
					"kubeconfig": "C:\\k\\config"
	   			},
	   			"policy": {"type": "k8s"},
	   			"nodename_file_optional": true,
	   			"log_level":"debug"
	   		}`, cniVersion, os.Getenv("ETCD_ENDPOINTS"), os.Getenv("DATASTORE_TYPE"), os.Getenv("KUBERNETES_MASTER"))

		cleanup := func() {
			// Cleanup hns network
			hnsNetwork, _ := hcsshim.GetHNSNetworkByName("net1")
			if hnsNetwork != nil {
				_, err := hnsNetwork.Delete()
				Expect(err).NotTo(HaveOccurred())
			}
			// Delete node
			_ = clientset.CoreV1().Nodes().Delete(hostname, &metav1.DeleteOptions{})
		}

		BeforeEach(func() {
			testutils.WipeK8sPods(netconf)
			conf := types.NetConf{}
			if err := json.Unmarshal([]byte(netconf), &conf); err != nil {
				panic(err)
			}
			logger := log.WithFields(log.Fields{
				"Namespace": testutils.HnsNoneNs,
			})
			clientset, err = k8s.NewK8sClient(conf, logger)
			if err != nil {
				panic(err)
			}

			nsName = fmt.Sprintf("ns%d", rand.Uint32())
			name = fmt.Sprintf("run%d", rand.Uint32())
			cleanup()
			time.Sleep(10000 * time.Millisecond)

			// Create namespace
			ensureNamespace(clientset, nsName)

			// Create a K8s Node object with PodCIDR and name equal to hostname.
			_, err = clientset.CoreV1().Nodes().Create(&v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: hostname},
				Spec: v1.NodeSpec{
					PodCIDR: "10.0.0.0/24",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Create a K8s pod w/o any special params
			_, err = clientset.CoreV1().Pods(nsName).Create(&v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec: v1.PodSpec{
					Containers: []v1.Container{{
						Name:  name,
						Image: "ignore",
					}},
					NodeName: hostname,
				},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			cleanup()
			// Delete namespace
			deleteNamespace(clientset, nsName)
		})

		It("successfully networks the namespace", func() {
			log.Infof("Creating container")
			containerID, result, contVeth, contAddresses, contRoutes, err := testutils.CreateContainer(netconf, name, testutils.HnsNoneNs, "", nsName)
			Expect(err).ShouldNot(HaveOccurred())
			defer func() {
				log.Infof("Container Delete  call")
				_, err = testutils.DeleteContainerWithId(netconf, name, testutils.HnsNoneNs, containerID, nsName)
				Expect(err).ShouldNot(HaveOccurred())

				// Make sure there are no endpoints anymore
				endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				Expect(endpoints.Items).Should(HaveLen(0))
			}()
			log.Debugf("containerID :%v , result: %v ,icontVeth : %v , contAddresses : %v ,contRoutes : %v ", containerID, result, contVeth, contAddresses, contRoutes)

			Expect(len(result.IPs)).Should(Equal(1))
			ip := result.IPs[0].Address.IP.String()
			log.Debugf("ip is %v ", ip)
			result.IPs[0].Address.IP = result.IPs[0].Address.IP.To4() // Make sure the IP is respresented as 4 bytes
			Expect(result.IPs[0].Address.Mask.String()).Should(Equal("fffff000"))

			// datastore things:
			ids := names.WorkloadEndpointIdentifiers{
				Node:         hostname,
				Orchestrator: api.OrchestratorKubernetes,
				Endpoint:     "eth0",
				Pod:          name,
				ContainerID:  containerID,
			}

			wrkload, err := ids.CalculateWorkloadEndpointName(false)
			log.Debugf("workload endpoint: %v", wrkload)
			Expect(err).NotTo(HaveOccurred())

			// The endpoint is created
			endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(endpoints.Items).Should(HaveLen(1))

			Expect(endpoints.Items[0].Name).Should(Equal(wrkload))
			Expect(endpoints.Items[0].Namespace).Should(Equal(nsName))
			Expect(endpoints.Items[0].Labels).Should(Equal(map[string]string{
				"projectcalico.org/namespace":      nsName,
				"projectcalico.org/orchestrator":   api.OrchestratorKubernetes,
				"projectcalico.org/serviceaccount": "default",
			}))
			Expect(endpoints.Items[0].Spec.Pod).Should(Equal(name))
			Expect(endpoints.Items[0].Spec.IPNetworks[0]).Should(Equal(result.IPs[0].Address.IP.String() + "/32"))
			Expect(endpoints.Items[0].Spec.Node).Should(Equal(hostname))
			Expect(endpoints.Items[0].Spec.Endpoint).Should(Equal("eth0"))
			Expect(endpoints.Items[0].Spec.Workload).Should(Equal(""))
			Expect(endpoints.Items[0].Spec.ContainerID).Should(Equal(containerID))
			Expect(endpoints.Items[0].Spec.Orchestrator).Should(Equal(api.OrchestratorKubernetes))

			// Ensure network is created
			hnsNetwork, err := hcsshim.GetHNSNetworkByName("net1")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(hnsNetwork.Subnets[0].AddressPrefix).Should(Equal("10.254.112.0/20"))
			Expect(hnsNetwork.Subnets[0].GatewayAddress).Should(Equal("10.254.112.1"))
			Expect(hnsNetwork.Type).Should(Equal("L2Bridge"))

			// Ensure host and container endpoints are created
			hostEP, err := hcsshim.GetHNSEndpointByName("net1_ep")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(hostEP.GatewayAddress).Should(Equal("10.254.112.1"))
			Expect(hostEP.IPAddress.String()).Should(Equal("10.254.112.2"))
			Expect(hostEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
			Expect(hostEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))

			containerEP, err := hcsshim.GetHNSEndpointByName(containerID + "_net1")
			Expect(containerEP.GatewayAddress).Should(Equal("10.254.112.2"))
			Expect(containerEP.IPAddress.String()).Should(Equal(ip))
			Expect(containerEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
			Expect(containerEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))
		})

		Context("when a named port is set", func() {
			It("it is added to the workload endpoint", func() {
				name := fmt.Sprintf("run%d", rand.Uint32())

				// Create a K8s pod w/o any special params
				_, err = clientset.CoreV1().Pods(nsName).Create(&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: name},
					Spec: v1.PodSpec{
						Containers: []v1.Container{{
							Name:  fmt.Sprintf("container-%s", name),
							Image: "ignore",
							Ports: []v1.ContainerPort{{
								Name:          "anamedport",
								ContainerPort: 555,
							}},
						}},
						NodeName: hostname,
					},
				})
				defer clientset.CoreV1().Pods(nsName).Delete(name, &metav1.DeleteOptions{})
				Expect(err).ShouldNot(HaveOccurred())

				containerID, result, contVeth, _, _, err := testutils.CreateContainer(netconf, name, testutils.HnsNoneNs, "", nsName)
				Expect(err).ShouldNot(HaveOccurred())
				defer func() {
					_, err = testutils.DeleteContainerWithId(netconf, name, testutils.HnsNoneNs, containerID, nsName)
					Expect(err).ShouldNot(HaveOccurred())

					// Make sure there are no endpoints anymore
					endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
					Expect(err).ShouldNot(HaveOccurred())
					Expect(endpoints.Items).Should(HaveLen(0))
				}()
				log.Debugf("contVeth %v ", contVeth)
				log.Debugf("containerID %v ", containerID)
				log.Debugf("result %v ", result)
				Expect(len(result.IPs)).Should(Equal(1))
				result.IPs[0].Address.IP = result.IPs[0].Address.IP.To4() // Make sure the IP is respresented as 4 bytes
				Expect(result.IPs[0].Address.Mask.String()).Should(Equal("fffff000"))

				// datastore things:

				ids := names.WorkloadEndpointIdentifiers{
					Node:         hostname,
					Orchestrator: api.OrchestratorKubernetes,
					Endpoint:     "eth0",
					Pod:          name,
					ContainerID:  containerID,
				}

				wrkload, err := ids.CalculateWorkloadEndpointName(false)
				interfaceName := k8sconversion.VethNameForWorkload(nsName, name)
				Expect(err).NotTo(HaveOccurred())
				log.Debugf("interfaceName : %v", interfaceName)

				// The endpoint is created
				endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				Expect(endpoints.Items).Should(HaveLen(1))
				log.Debugf("workload endpoints :", endpoints)

				Expect(endpoints.Items[0].Name).Should(Equal(wrkload))
				Expect(endpoints.Items[0].Namespace).Should(Equal(nsName))
				Expect(endpoints.Items[0].Labels).Should(Equal(map[string]string{
					"projectcalico.org/namespace":      nsName,
					"projectcalico.org/orchestrator":   api.OrchestratorKubernetes,
					"projectcalico.org/serviceaccount": "default",
				}))
				Expect(endpoints.Items[0].Spec.Pod).Should(Equal(name))
				Expect(endpoints.Items[0].Spec.InterfaceName).Should(Equal(interfaceName))
				Expect(endpoints.Items[0].Spec.Node).Should(Equal(hostname))
				Expect(endpoints.Items[0].Spec.Endpoint).Should(Equal("eth0"))
				Expect(endpoints.Items[0].Spec.ContainerID).Should(Equal(containerID))
				Expect(endpoints.Items[0].Spec.Orchestrator).Should(Equal(api.OrchestratorKubernetes))
				Expect(endpoints.Items[0].Spec.Ports).Should(Equal([]api.EndpointPort{{
					Name:     "anamedport",
					Protocol: numorstring.ProtocolFromString("TCP"),
					Port:     555,
				}}))
			})

		})

		Context("when the same hostVeth exists", func() {
			It("successfully networks the namespace", func() {
				// Check if network exists, if not, create one
				hnsNetwork, err := testutils.CreateNetwork(netconf)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(hnsNetwork.Subnets[0].AddressPrefix).Should(Equal("10.254.112.0/20"))
				Expect(hnsNetwork.Subnets[0].GatewayAddress).Should(Equal("10.254.112.1"))
				Expect(hnsNetwork.Type).Should(Equal("L2Bridge"))

				// Check for host endpoint, if doesn't exist, create endpoint
				hostEP, err := testutils.CreateEndpoint(hnsNetwork, netconf)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(hostEP.GatewayAddress).Should(Equal("10.254.112.1"))
				Expect(hostEP.IPAddress.String()).Should(Equal("10.254.112.2"))
				Expect(hostEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
				Expect(hostEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))

				log.Infof("Creating container")
				containerID, result, contVeth, contAddresses, contRoutes, err := testutils.CreateContainer(netconf, name, testutils.HnsNoneNs, "", nsName)
				Expect(err).ShouldNot(HaveOccurred())
				defer func() {
					log.Infof("Container Delete  call")
					_, err := testutils.DeleteContainerWithId(netconf, name, testutils.HnsNoneNs, containerID, nsName)
					Expect(err).ShouldNot(HaveOccurred())

					// Make sure there are no endpoints anymore
					endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
					Expect(err).ShouldNot(HaveOccurred())
					Expect(endpoints.Items).Should(HaveLen(0))
				}()
				log.Debugf("containerID :%v , result: %v ,icontVeth : %v , contAddresses : %v ,contRoutes : %v ", containerID, result, contVeth, contAddresses, contRoutes)

				Expect(len(result.IPs)).Should(Equal(1))
				ip := result.IPs[0].Address.IP.String()
				log.Debugf("ip is %v ", ip)
				result.IPs[0].Address.IP = result.IPs[0].Address.IP.To4() // Make sure the IP is respresented as 4 bytes
				Expect(result.IPs[0].Address.Mask.String()).Should(Equal("fffff000"))

				ids := names.WorkloadEndpointIdentifiers{
					Node:         hostname,
					Orchestrator: api.OrchestratorKubernetes,
					Endpoint:     "eth0",
					Pod:          name,
					ContainerID:  containerID,
				}

				wrkload, err := ids.CalculateWorkloadEndpointName(false)
				log.Debugf("workload endpoint: %v", wrkload)
				Expect(err).NotTo(HaveOccurred())

				// The endpoint is created
				endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				Expect(endpoints.Items).Should(HaveLen(1))

				Expect(endpoints.Items[0].Name).Should(Equal(wrkload))
				Expect(endpoints.Items[0].Namespace).Should(Equal(nsName))
				Expect(endpoints.Items[0].Labels).Should(Equal(map[string]string{
					"projectcalico.org/namespace":      nsName,
					"projectcalico.org/orchestrator":   api.OrchestratorKubernetes,
					"projectcalico.org/serviceaccount": "default",
				}))
				Expect(endpoints.Items[0].Spec.Pod).Should(Equal(name))
				Expect(endpoints.Items[0].Spec.IPNetworks[0]).Should(Equal(result.IPs[0].Address.IP.String() + "/32"))
				Expect(endpoints.Items[0].Spec.Node).Should(Equal(hostname))
				Expect(endpoints.Items[0].Spec.Endpoint).Should(Equal("eth0"))
				Expect(endpoints.Items[0].Spec.Workload).Should(Equal(""))
				Expect(endpoints.Items[0].Spec.ContainerID).Should(Equal(containerID))
				Expect(endpoints.Items[0].Spec.Orchestrator).Should(Equal(api.OrchestratorKubernetes))

				// Ensure network is created
				hnsNetwork, err = hcsshim.GetHNSNetworkByName("net1")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(hnsNetwork.Subnets[0].AddressPrefix).Should(Equal("10.254.112.0/20"))
				Expect(hnsNetwork.Subnets[0].GatewayAddress).Should(Equal("10.254.112.1"))
				Expect(hnsNetwork.Type).Should(Equal("L2Bridge"))

				// Ensure host and container endpoints are created
				hostEP, err = hcsshim.GetHNSEndpointByName("net1_ep")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(hostEP.GatewayAddress).Should(Equal("10.254.112.1"))
				Expect(hostEP.IPAddress.String()).Should(Equal("10.254.112.2"))
				Expect(hostEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
				Expect(hostEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))

				containerEP, err := hcsshim.GetHNSEndpointByName(containerID + "_net1")
				Expect(containerEP.GatewayAddress).Should(Equal("10.254.112.2"))
				Expect(containerEP.IPAddress.String()).Should(Equal(ip))
				Expect(containerEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
				Expect(containerEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))
			})
		})

		Context("after a pod has already been networked once", func() {
			It("an ADD for NETNS != \"none\" should return existing IP", func() {
				log.Infof("Creating container")
				containerID, result, contVeth, contAddresses, contRoutes, err := testutils.CreateContainer(netconf, name, testutils.HnsNoneNs, "", nsName)
				Expect(err).ShouldNot(HaveOccurred())
				defer func() {
					log.Infof("Container Delete  call")
					_, err := testutils.DeleteContainerWithId(netconf, name, testutils.HnsNoneNs, containerID, nsName)
					Expect(err).ShouldNot(HaveOccurred())

					// Make sure there are no endpoints anymore
					endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
					Expect(err).ShouldNot(HaveOccurred())
					Expect(endpoints.Items).Should(HaveLen(0))
				}()
				log.Debugf("containerID :%v , result: %v ,icontVeth : %v , contAddresses : %v ,contRoutes : %v ", containerID, result, contVeth, contAddresses, contRoutes)

				Expect(err).ShouldNot(HaveOccurred())
				Expect(len(result.IPs)).Should(Equal(1))
				ip := result.IPs[0].Address.IP.String()
				log.Debugf("ip is %v ", ip)
				result.IPs[0].Address.IP = result.IPs[0].Address.IP.To4() // Make sure the IP is respresented as 4 bytes
				Expect(result.IPs[0].Address.Mask.String()).Should(Equal("fffff000"))

				// datastore things:
				ids := names.WorkloadEndpointIdentifiers{
					Node:         hostname,
					Orchestrator: api.OrchestratorKubernetes,
					Endpoint:     "eth0",
					Pod:          name,
					ContainerID:  containerID,
				}

				wrkload, err := ids.CalculateWorkloadEndpointName(false)
				log.Debugf("workload endpoint: %v", wrkload)
				Expect(err).NotTo(HaveOccurred())

				// The endpoint is created
				endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				Expect(endpoints.Items).Should(HaveLen(1))

				Expect(endpoints.Items[0].Name).Should(Equal(wrkload))
				Expect(endpoints.Items[0].Namespace).Should(Equal(nsName))
				Expect(endpoints.Items[0].Labels).Should(Equal(map[string]string{
					"projectcalico.org/namespace":      nsName,
					"projectcalico.org/orchestrator":   api.OrchestratorKubernetes,
					"projectcalico.org/serviceaccount": "default",
				}))
				Expect(endpoints.Items[0].Spec.Pod).Should(Equal(name))
				Expect(endpoints.Items[0].Spec.IPNetworks[0]).Should(Equal(result.IPs[0].Address.IP.String() + "/32"))
				Expect(endpoints.Items[0].Spec.Node).Should(Equal(hostname))
				Expect(endpoints.Items[0].Spec.Endpoint).Should(Equal("eth0"))
				Expect(endpoints.Items[0].Spec.Workload).Should(Equal(""))
				Expect(endpoints.Items[0].Spec.ContainerID).Should(Equal(containerID))
				Expect(endpoints.Items[0].Spec.Orchestrator).Should(Equal(api.OrchestratorKubernetes))

				// Ensure network is created
				hnsNetwork, err := hcsshim.GetHNSNetworkByName("net1")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(hnsNetwork.Subnets[0].AddressPrefix).Should(Equal("10.254.112.0/20"))
				Expect(hnsNetwork.Subnets[0].GatewayAddress).Should(Equal("10.254.112.1"))
				Expect(hnsNetwork.Type).Should(Equal("L2Bridge"))

				// Ensure host and container endpoints are created
				hostEP, err := hcsshim.GetHNSEndpointByName("net1_ep")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(hostEP.GatewayAddress).Should(Equal("10.254.112.1"))
				Expect(hostEP.IPAddress.String()).Should(Equal("10.254.112.2"))
				Expect(hostEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
				Expect(hostEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))

				containerEP, err := hcsshim.GetHNSEndpointByName(containerID + "_net1")
				Expect(containerEP.GatewayAddress).Should(Equal("10.254.112.2"))
				Expect(containerEP.IPAddress.String()).Should(Equal(ip))
				Expect(containerEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
				Expect(containerEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))

				result2, _, _, _, err := testutils.RunCNIPluginWithId(netconf, name, testutils.K8S_TEST_NS, ip, containerID, "", nsName)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(len(result2.IPs)).Should(Equal(1))
				ip2 := result2.IPs[0].Address.IP.String()
				Expect(ip2).Should(Equal(ip))

			})
		})

		Context("With pod not networked", func() {
			It("an ADD for NETNS != \"none\" should return error rather than networking the pod", func() {
				log.Infof("Creating container")
				containerID, result, contVeth, contAddresses, contRoutes, err := testutils.CreateContainer(netconf, name, testutils.K8S_TEST_NS, "", nsName)
				log.Debugf("containerID :%v , result: %v ,icontVeth : %v , contAddresses : %v ,contRoutes : %v ", containerID, result, contVeth, contAddresses, contRoutes)
				Expect(err).Should(HaveOccurred())

				endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				Expect(endpoints.Items).Should(HaveLen(0))
			})
		})

		Context("Windows corner cases", func() {
			It("Network exists but wrong subnet, should be recreated", func() {
				log.Infof("Creating container")
				containerID, result, contVeth, contAddresses, contRoutes, err := testutils.CreateContainer(netconf, name, testutils.HnsNoneNs, "", nsName)
				Expect(err).ShouldNot(HaveOccurred())
				defer func() {
					log.Infof("Container Delete  call")
					_, err := testutils.DeleteContainerWithId(netconf, name, testutils.HnsNoneNs, containerID, nsName)
					Expect(err).ShouldNot(HaveOccurred())

					// Make sure there are no endpoints anymore
					endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
					Expect(err).ShouldNot(HaveOccurred())
					Expect(endpoints.Items).Should(HaveLen(0))
				}()
				log.Debugf("containerID :%v , result: %v ,icontVeth : %v , contAddresses : %v ,contRoutes : %v ", containerID, result, contVeth, contAddresses, contRoutes)

				Expect(len(result.IPs)).Should(Equal(1))
				ip := result.IPs[0].Address.IP.String()
				log.Debugf("ip is %v ", ip)
				result.IPs[0].Address.IP = result.IPs[0].Address.IP.To4() // Make sure the IP is respresented as 4 bytes
				Expect(result.IPs[0].Address.Mask.String()).Should(Equal("fffff000"))

				// datastore things:
				ids := names.WorkloadEndpointIdentifiers{
					Node:         hostname,
					Orchestrator: api.OrchestratorKubernetes,
					Endpoint:     "eth0",
					Pod:          name,
					ContainerID:  containerID,
				}

				wrkload, err := ids.CalculateWorkloadEndpointName(false)
				log.Debugf("workload endpoint: %v", wrkload)
				Expect(err).NotTo(HaveOccurred())

				// The endpoint is created
				endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				Expect(endpoints.Items).Should(HaveLen(1))
				Expect(endpoints.Items[0].Name).Should(Equal(wrkload))
				Expect(endpoints.Items[0].Namespace).Should(Equal(nsName))
				Expect(endpoints.Items[0].Labels).Should(Equal(map[string]string{
					"projectcalico.org/namespace":      nsName,
					"projectcalico.org/orchestrator":   api.OrchestratorKubernetes,
					"projectcalico.org/serviceaccount": "default",
				}))
				Expect(endpoints.Items[0].Spec.Pod).Should(Equal(name))
				Expect(endpoints.Items[0].Spec.IPNetworks[0]).Should(Equal(result.IPs[0].Address.IP.String() + "/32"))
				Expect(endpoints.Items[0].Spec.Node).Should(Equal(hostname))
				Expect(endpoints.Items[0].Spec.Endpoint).Should(Equal("eth0"))
				Expect(endpoints.Items[0].Spec.Workload).Should(Equal(""))
				Expect(endpoints.Items[0].Spec.ContainerID).Should(Equal(containerID))
				Expect(endpoints.Items[0].Spec.Orchestrator).Should(Equal(api.OrchestratorKubernetes))

				// Ensure network is created
				hnsNetwork, err := hcsshim.GetHNSNetworkByName("net1")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(hnsNetwork.Subnets[0].AddressPrefix).Should(Equal("10.254.112.0/20"))
				Expect(hnsNetwork.Subnets[0].GatewayAddress).Should(Equal("10.254.112.1"))
				Expect(hnsNetwork.Type).Should(Equal("L2Bridge"))

				// Ensure host and container endpoints are created
				hostEP, err := hcsshim.GetHNSEndpointByName("net1_ep")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(hostEP.GatewayAddress).Should(Equal("10.254.112.1"))
				Expect(hostEP.IPAddress.String()).Should(Equal("10.254.112.2"))
				Expect(hostEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
				Expect(hostEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))

				containerEP, err := hcsshim.GetHNSEndpointByName(containerID + "_net1")
				Expect(containerEP.GatewayAddress).Should(Equal("10.254.112.2"))
				Expect(containerEP.IPAddress.String()).Should(Equal(ip))
				Expect(containerEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
				Expect(containerEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))

				// Create network with new subnet
				podIP, subnet, _ := net.ParseCIDR("20.0.0.20/8")
				result.IPs[0].Address = *subnet
				result.IPs[0].Address.IP = podIP

				netconf2 := fmt.Sprintf(`
	   				{
	   					"cniVersion": "%s",
	   					"name": "net1",
	   					"type": "calico",
	   					"etcd_endpoints": "%s",
	   					"datastore_type": "%s",
	   					"windows_use_single_network":true,
	   					"ipam": {
	   						"type": "host-local",
	   						"subnet": "20.0.0.0/8"
	   					},
	   					"kubernetes": {
	   						"k8s_api_root": "%s",
							"kubeconfig": "C:\\k\\config"
	   					},
	   					"policy": {"type": "k8s"},
	   					"nodename_file_optional": true,
	   					"log_level":"debug"
	   				}`, cniVersion, os.Getenv("ETCD_ENDPOINTS"), os.Getenv("DATASTORE_TYPE"), os.Getenv("KUBERNETES_MASTER"))

				err = testutils.NetworkPod(netconf2, name, ip, ctx, calicoClient, result, containerID, testutils.HnsNoneNs, nsName)
				Expect(err).ShouldNot(HaveOccurred())
				ip = result.IPs[0].Address.IP.String()

				hnsNetwork, err = hcsshim.GetHNSNetworkByName("net1")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(hnsNetwork.Subnets[0].AddressPrefix).Should(Equal("20.0.0.0/8"))
				Expect(hnsNetwork.Subnets[0].GatewayAddress).Should(Equal("20.0.0.1"))
				Expect(hnsNetwork.Type).Should(Equal("L2Bridge"))

				hostEP, err = hcsshim.GetHNSEndpointByName("net1_ep")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(hostEP.GatewayAddress).Should(Equal("20.0.0.1"))
				Expect(hostEP.IPAddress.String()).Should(Equal("20.0.0.2"))
				Expect(hostEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
				Expect(hostEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))

				containerEP, err = hcsshim.GetHNSEndpointByName(containerID + "_net1")
				Expect(containerEP.GatewayAddress).Should(Equal("20.0.0.2"))

				Expect(containerEP.IPAddress.String()).Should(Equal(ip))
				Expect(containerEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
				Expect(containerEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))

			})

			It("Network exists but missing management endpoint, should be added", func() {
				log.Infof("Creating container")
				containerID, result, contVeth, contAddresses, contRoutes, err := testutils.CreateContainer(netconf, name, testutils.HnsNoneNs, "", nsName)
				Expect(err).ShouldNot(HaveOccurred())
				defer func() {
					log.Infof("Container Delete  call")
					_, err := testutils.DeleteContainerWithId(netconf, name, testutils.HnsNoneNs, containerID, nsName)
					Expect(err).ShouldNot(HaveOccurred())

					// Make sure there are no endpoints anymore
					endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
					Expect(err).ShouldNot(HaveOccurred())
					Expect(endpoints.Items).Should(HaveLen(0))
				}()
				log.Debugf("containerID :%v , result: %v ,icontVeth : %v , contAddresses : %v ,contRoutes : %v ", containerID, result, contVeth, contAddresses, contRoutes)

				Expect(len(result.IPs)).Should(Equal(1))
				ip := result.IPs[0].Address.IP.String()
				log.Debugf("ip is %v ", ip)
				result.IPs[0].Address.IP = result.IPs[0].Address.IP.To4() // Make sure the IP is respresented as 4 bytes
				Expect(result.IPs[0].Address.Mask.String()).Should(Equal("fffff000"))

				// datastore things:
				ids := names.WorkloadEndpointIdentifiers{
					Node:         hostname,
					Orchestrator: api.OrchestratorKubernetes,
					Endpoint:     "eth0",
					Pod:          name,
					ContainerID:  containerID,
				}

				wrkload, err := ids.CalculateWorkloadEndpointName(false)
				log.Debugf("workload endpoint: %v", wrkload)
				Expect(err).NotTo(HaveOccurred())

				// The endpoint is created
				endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				Expect(endpoints.Items).Should(HaveLen(1))
				Expect(endpoints.Items[0].Name).Should(Equal(wrkload))
				Expect(endpoints.Items[0].Namespace).Should(Equal(nsName))
				Expect(endpoints.Items[0].Labels).Should(Equal(map[string]string{
					"projectcalico.org/namespace":      nsName,
					"projectcalico.org/orchestrator":   api.OrchestratorKubernetes,
					"projectcalico.org/serviceaccount": "default",
				}))
				Expect(endpoints.Items[0].Spec.Pod).Should(Equal(name))
				Expect(endpoints.Items[0].Spec.IPNetworks[0]).Should(Equal(result.IPs[0].Address.IP.String() + "/32"))
				Expect(endpoints.Items[0].Spec.Node).Should(Equal(hostname))
				Expect(endpoints.Items[0].Spec.Endpoint).Should(Equal("eth0"))
				Expect(endpoints.Items[0].Spec.Workload).Should(Equal(""))
				Expect(endpoints.Items[0].Spec.ContainerID).Should(Equal(containerID))
				Expect(endpoints.Items[0].Spec.Orchestrator).Should(Equal(api.OrchestratorKubernetes))

				// Ensure network is created
				hnsNetwork, err := hcsshim.GetHNSNetworkByName("net1")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(hnsNetwork.Subnets[0].AddressPrefix).Should(Equal("10.254.112.0/20"))
				Expect(hnsNetwork.Subnets[0].GatewayAddress).Should(Equal("10.254.112.1"))
				Expect(hnsNetwork.Type).Should(Equal("L2Bridge"))

				// Ensure host and container endpoints are created
				hostEP, err := hcsshim.GetHNSEndpointByName("net1_ep")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(hostEP.GatewayAddress).Should(Equal("10.254.112.1"))
				Expect(hostEP.IPAddress.String()).Should(Equal("10.254.112.2"))
				Expect(hostEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
				Expect(hostEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))

				containerEP, err := hcsshim.GetHNSEndpointByName(containerID + "_net1")
				Expect(containerEP.GatewayAddress).Should(Equal("10.254.112.2"))

				Expect(containerEP.IPAddress.String()).Should(Equal(ip))
				Expect(containerEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
				Expect(containerEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))

				hnsEndpoint, err := hcsshim.GetHNSEndpointByName("net1_ep")
				_, err = hnsEndpoint.Delete()
				Expect(err).ShouldNot(HaveOccurred())

				err = testutils.NetworkPod(netconf, name, ip, ctx, calicoClient, result, containerID, testutils.HnsNoneNs, nsName)
				Expect(err).ShouldNot(HaveOccurred())

				hostEP, err = hcsshim.GetHNSEndpointByName("net1_ep")
				Expect(err).ShouldNot(HaveOccurred())
				Expect(hostEP.GatewayAddress).Should(Equal("10.254.112.1"))
				Expect(hostEP.IPAddress.String()).Should(Equal("10.254.112.2"))
				Expect(hostEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
				Expect(hostEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))

				containerEP, err = hcsshim.GetHNSEndpointByName(containerID + "_net1")
				Expect(containerEP.GatewayAddress).Should(Equal("10.254.112.2"))

				Expect(containerEP.IPAddress.String()).Should(Equal(ip))
				Expect(containerEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
				Expect(containerEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))

			})
		})

		hostLocalIPAMConfigs := []struct {
			description, cniVersion, config string
		}{
			{
				description: "old-style inline subnet",
				cniVersion:  cniVersion,
				config: `
	   				{
	   					"cniVersion": "%s",
	   					"name": "net1",
	   					"nodename_file_optional": true,
	   					"type": "calico",
	   					"etcd_endpoints": "%s",
	   					"windows_use_single_network":true,
	   					"datastore_type": "%s",
	   					"ipam": {
	   						"type": "host-local",
	   						"subnet": "usePodCidr"
	   					},
	   					"kubernetes": {
	   						"k8s_api_root": "%s",
							"kubeconfig": "C:\\k\\config"
	   					},
	   					"policy": {"type": "k8s"},
	   					"log_level":"debug"
	   				}`,
			},
		}

		Context("Using host-local IPAM ("+hostLocalIPAMConfigs[0].description+"): request an IP then release it, and then request it again", func() {
			It("should successfully assign IP both times and successfully release it in the middle", func() {
				netconfHostLocalIPAM := fmt.Sprintf(hostLocalIPAMConfigs[0].config, hostLocalIPAMConfigs[0].cniVersion, os.Getenv("ETCD_ENDPOINTS"), os.Getenv("DATASTORE_TYPE"), os.Getenv("KUBERNETES_MASTER"))

				requestedIP := "10.0.0.130"
				expectedIP := requestedIP

				containerID, result, _, _, _, err := testutils.CreateContainer(netconfHostLocalIPAM, name, testutils.HnsNoneNs, requestedIP, nsName)
				Expect(err).NotTo(HaveOccurred())

				podIP := result.IPs[0].Address.IP.String()
				log.Debugf("container IPs: %v", podIP)
				Expect(podIP).Should(Equal(expectedIP))

				By("Deleting the pod we created earlier")
				_, err = testutils.DeleteContainerWithId(netconfHostLocalIPAM, name, testutils.HnsNoneNs, containerID, nsName)
				Expect(err).ShouldNot(HaveOccurred())

				By("Creating a second pod with the same IP address as the first pod")
				name2 := fmt.Sprintf("run2%d", rand.Uint32())
				_, err = clientset.CoreV1().Pods(nsName).Create(&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: name2},
					Spec: v1.PodSpec{
						Containers: []v1.Container{{
							Name:  fmt.Sprintf("container-%s", name2),
							Image: "ignore",
						}},
						NodeName: hostname,
					},
				})
				Expect(err).NotTo(HaveOccurred())

				containerID, result, _, _, _, err = testutils.CreateContainer(netconfHostLocalIPAM, name2, testutils.HnsNoneNs, requestedIP, nsName)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					_, err = testutils.DeleteContainerWithId(netconfHostLocalIPAM, name2, testutils.HnsNoneNs, containerID, nsName)
					Expect(err).ShouldNot(HaveOccurred())
				}()

				pod2IP := result.IPs[0].Address.IP.String()
				log.Debugf("container IPs: %v", pod2IP)
				Expect(pod2IP).Should(Equal(expectedIP))
			})
		})
		Context("With DNS capability in CNI conf", func() {
			netconf = fmt.Sprintf(`
	   			{
	   				"cniVersion": "%s",
	   				"name": "net1",
	   				"type": "calico",
	   				"etcd_endpoints": "%s",
	   				"datastore_type": "%s",
	   				"windows_use_single_network":true,
	   				"ipam": {
	   					"type": "host-local",
	   					"subnet": "10.254.112.0/20"
	   				},
	   				"kubernetes": {
	   					"k8s_api_root": "%s",
						"kubeconfig": "C:\\k\\config"
	   				},
	   				"policy": {"type": "k8s"},
	   				"nodename_file_optional": true,
	   				"log_level":"debug",
	   				"DNS":  {
	   					"Nameservers":  [
	   					"10.96.0.10"
	   					],
	   					"Search":  [
	   					"pod.cluster.local"
	   					]
	   				}
	   			}`, cniVersion, os.Getenv("ETCD_ENDPOINTS"), os.Getenv("DATASTORE_TYPE"), os.Getenv("KUBERNETES_MASTER"))
			Context("and no runtimeConf entry", func() {
				It("should network the pod but fall back on DNS values from main CNI conf", func() {
					log.Infof("Creating container")
					containerID, result, contVeth, contAddresses, contRoutes, err := testutils.CreateContainer(netconf, name, testutils.HnsNoneNs, "", nsName)
					Expect(err).ShouldNot(HaveOccurred())
					defer func() {
						log.Infof("Container Delete  call")
						_, err := testutils.DeleteContainerWithId(netconf, name, testutils.HnsNoneNs, containerID, nsName)
						Expect(err).ShouldNot(HaveOccurred())

						// Make sure there are no endpoints anymore
						endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
						Expect(err).ShouldNot(HaveOccurred())
						Expect(endpoints.Items).Should(HaveLen(0))
					}()
					log.Debugf("containerID :%v , result: %v ,icontVeth : %v , contAddresses : %v ,contRoutes : %v ", containerID, result, contVeth, contAddresses, contRoutes)

					Expect(len(result.IPs)).Should(Equal(1))
					ip := result.IPs[0].Address.IP.String()
					log.Debugf("ip is %v ", ip)
					result.IPs[0].Address.IP = result.IPs[0].Address.IP.To4() // Make sure the IP is respresented as 4 bytes
					Expect(result.IPs[0].Address.Mask.String()).Should(Equal("fffff000"))

					// datastore things:
					ids := names.WorkloadEndpointIdentifiers{
						Node:         hostname,
						Orchestrator: api.OrchestratorKubernetes,
						Endpoint:     "eth0",
						Pod:          name,
						ContainerID:  containerID,
					}

					wrkload, err := ids.CalculateWorkloadEndpointName(false)
					log.Debugf("workload endpoint: %v", wrkload)
					Expect(err).NotTo(HaveOccurred())

					// The endpoint is created
					endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
					Expect(err).ShouldNot(HaveOccurred())
					Expect(endpoints.Items).Should(HaveLen(1))

					Expect(endpoints.Items[0].Name).Should(Equal(wrkload))
					Expect(endpoints.Items[0].Namespace).Should(Equal(nsName))
					Expect(endpoints.Items[0].Labels).Should(Equal(map[string]string{
						"projectcalico.org/namespace":      nsName,
						"projectcalico.org/orchestrator":   api.OrchestratorKubernetes,
						"projectcalico.org/serviceaccount": "default",
					}))
					Expect(endpoints.Items[0].Spec.Pod).Should(Equal(name))
					Expect(endpoints.Items[0].Spec.IPNetworks[0]).Should(Equal(result.IPs[0].Address.IP.String() + "/32"))
					Expect(endpoints.Items[0].Spec.Node).Should(Equal(hostname))
					Expect(endpoints.Items[0].Spec.Endpoint).Should(Equal("eth0"))
					Expect(endpoints.Items[0].Spec.Workload).Should(Equal(""))
					Expect(endpoints.Items[0].Spec.ContainerID).Should(Equal(containerID))
					Expect(endpoints.Items[0].Spec.Orchestrator).Should(Equal(api.OrchestratorKubernetes))

					// Ensure network is created
					hnsNetwork, err := hcsshim.GetHNSNetworkByName("net1")
					Expect(err).ShouldNot(HaveOccurred())
					Expect(hnsNetwork.Subnets[0].AddressPrefix).Should(Equal("10.254.112.0/20"))
					Expect(hnsNetwork.Subnets[0].GatewayAddress).Should(Equal("10.254.112.1"))
					Expect(hnsNetwork.Type).Should(Equal("L2Bridge"))
					Expect(hnsNetwork.DNSSuffix).Should(Equal("pod.cluster.local"))
					Expect(hnsNetwork.DNSServerList).Should(Equal("10.96.0.10"))

					// Ensure host and container endpoints are created
					hostEP, err := hcsshim.GetHNSEndpointByName("net1_ep")
					Expect(err).ShouldNot(HaveOccurred())
					Expect(hostEP.GatewayAddress).Should(Equal("10.254.112.1"))
					Expect(hostEP.IPAddress.String()).Should(Equal("10.254.112.2"))
					Expect(hostEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
					Expect(hostEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))
					Expect(hostEP.DNSSuffix).Should(Equal("pod.cluster.local"))
					Expect(hostEP.DNSServerList).Should(Equal("10.96.0.10"))

					containerEP, err := hcsshim.GetHNSEndpointByName(containerID + "_net1")
					Expect(containerEP.GatewayAddress).Should(Equal("10.254.112.2"))
					Expect(containerEP.IPAddress.String()).Should(Equal(ip))
					Expect(containerEP.VirtualNetwork).Should(Equal(hnsNetwork.Id))
					Expect(containerEP.VirtualNetworkName).Should(Equal(hnsNetwork.Name))
					Expect(containerEP.DNSSuffix).Should(Equal("pod.cluster.local"))
					Expect(containerEP.DNSServerList).Should(Equal("10.96.0.10"))

				})
			})
		})
	})

	Context("after a pod has already been networked once", func() {
		var nc types.NetConf
		var netconf string
		var workloadName, containerID, name string
		var endpointSpec api.WorkloadEndpointSpec
		var result *current.Result

		checkIPAMReservation := func() {
			// IPAM reservation should still be in place.
			handleID, _ := utils.GetHandleID("calico-uts", containerID, workloadName)
			ipamIPs, err := calicoClient.IPAM().IPsByHandle(context.Background(), handleID)
			ExpectWithOffset(1, err).NotTo(HaveOccurred(), "error getting IPs")
			ExpectWithOffset(1, ipamIPs).To(HaveLen(1),
				"There should be an IPAM handle for endpoint")
			ExpectWithOffset(1, ipamIPs[0].String()+"/32").To(Equal(endpointSpec.IPNetworks[0]))
		}

		var nsName string
		var clientset *kubernetes.Clientset

		BeforeEach(func() {
			// Create a network config.
			nc = types.NetConf{
				CNIVersion:              cniVersion,
				Name:                    "calico-uts",
				Type:                    "calico",
				EtcdEndpoints:           os.Getenv("ETCD_ENDPOINTS"),
				DatastoreType:           os.Getenv("DATASTORE_TYPE"),
				Kubernetes:              types.Kubernetes{K8sAPIRoot: os.Getenv("KUBERNETES_MASTER"), Kubeconfig: "C:\\k\\config"},
				Policy:                  types.Policy{PolicyType: "k8s"},
				NodenameFileOptional:    true,
				LogLevel:                "info",
				WindowsUseSingleNetwork: true,
			}
			nc.IPAM.Type = "calico-ipam"
			ncb, err := json.Marshal(nc)
			if err != nil {
				panic(err)
			}
			netconf = string(ncb)

			testutils.WipeK8sPods(netconf)
			conf := types.NetConf{}
			if err := json.Unmarshal([]byte(netconf), &conf); err != nil {
				panic(err)
			}
			logger := log.WithFields(log.Fields{
				"Namespace": testutils.HnsNoneNs,
			})
			clientset, err = k8s.NewK8sClient(conf, logger)
			if err != nil {
				panic(err)
			}

			// Now create a K8s pod.
			time.Sleep(30000 * time.Millisecond)
			// Create a new ipPool.
			testutils.MustCreateNewIPPoolBlockSize(calicoClient, "10.0.0.0/24", false, false, true, 26)

			nsName = fmt.Sprintf("ns%d", rand.Uint32())
			ensureNamespace(clientset, nsName)
			// Create a K8s Node object with PodCIDR and name equal to hostname.
			_, err = clientset.CoreV1().Nodes().Create(&v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: hostname},
				Spec: v1.NodeSpec{
					PodCIDR: "10.0.0.0/24",
				},
			})
			defer clientset.CoreV1().Nodes().Delete(hostname, &metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())

			name = fmt.Sprintf("run%d", rand.Uint32())
			pod, err := clientset.CoreV1().Pods(nsName).Create(
				&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{{
							Name:  name,
							Image: "ignore",
						}},
						NodeName: hostname,
					},
				})
			Expect(err).NotTo(HaveOccurred())
			log.Infof("Created POD object: %v", pod)

			// Run the CNI plugin.
			containerID, result, _, _, _, err = testutils.CreateContainer(netconf, name, testutils.HnsNoneNs, "", nsName)
			Expect(err).ShouldNot(HaveOccurred())
			log.Debugf("Unmarshalled result from first ADD: %v", result)

			// The endpoint is created in etcd
			endpoints, err := calicoClient.WorkloadEndpoints().List(ctx, options.ListOptions{})
			log.Debugf("workload endpoint: %v", endpoints)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(endpoints.Items).Should(HaveLen(1))
			ids := names.WorkloadEndpointIdentifiers{
				Node:         hostname,
				Orchestrator: api.OrchestratorKubernetes,
				Endpoint:     "eth0",
				Pod:          name,
				ContainerID:  containerID,
			}
			workloadName, err = ids.CalculateWorkloadEndpointName(false)
			log.Debugf("workloadName: %v", workloadName)
			Expect(err).NotTo(HaveOccurred())
			Expect(endpoints.Items[0].Name).Should(Equal(workloadName))
			Expect(endpoints.Items[0].Namespace).Should(Equal(nsName))
			Expect(endpoints.Items[0].Labels).Should(Equal(map[string]string{
				"projectcalico.org/namespace":      nsName,
				"projectcalico.org/orchestrator":   api.OrchestratorKubernetes,
				"projectcalico.org/serviceaccount": "default",
			}))
			endpointSpec = endpoints.Items[0].Spec
			log.Debugf("endpointSpec: %v", endpointSpec)
			Expect(endpointSpec.ContainerID).Should(Equal(containerID))
			checkIPAMReservation()
			time.Sleep(30000 * time.Millisecond)
		})

		AfterEach(func() {
			// Cleanup hns network
			hnsNetwork, _ := hcsshim.GetHNSNetworkByName("calico-uts")
			if hnsNetwork != nil {
				_, err := hnsNetwork.Delete()
				Expect(err).NotTo(HaveOccurred())
			}
			// Delete node
			_ = clientset.CoreV1().Nodes().Delete(hostname, &metav1.DeleteOptions{})
			_, err = testutils.DeleteContainerWithId(netconf, name, testutils.HnsNoneNs, containerID, nsName)
			Expect(err).ShouldNot(HaveOccurred())
			deleteNamespace(clientset, nsName)
		})

		It("a second ADD for the same container shouldn't work, returning already assigned IP", func() {
			time.Sleep(10000 * time.Millisecond)
			resultSecondAdd, _, _, _, err := testutils.RunCNIPluginWithId(netconf, name, testutils.HnsNoneNs, "", "new-container-id", "eth0", nsName)
			log.Debugf("resultSecondAdd: %v", resultSecondAdd)
			Expect(err).NotTo(HaveOccurred())
			log.Debugf("Unmarshalled result from second ADD: %v", resultSecondAdd)

			// The IP addresses should be the same
			log.Debugf("resultSecondAdd.IPs: %v and result.IPs: %v ", resultSecondAdd.IPs, result.IPs)
			Expect(resultSecondAdd.IPs[0].Address.IP).Should(Equal(result.IPs[0].Address.IP))

			// results should be the same.
			resultSecondAdd.IPs = nil
			result.IPs = nil
			Expect(resultSecondAdd).Should(Equal(result))

			// IPAM reservation should still be in place.
			checkIPAMReservation()
			time.Sleep(30000 * time.Millisecond)
		})
	})

	Context("With a /29 IPAM blockSize", func() {
		var nsName string
		networkName := "net10"
		var clientset *kubernetes.Clientset
		netconf := fmt.Sprintf(`
		{
			"cniVersion": "%s",
			"name": "%s",
			"type": "calico",
			"etcd_endpoints": "%s",
			"datastore_type": "%s",
			"nodename_file_optional": true,
			"windows_use_single_network":true,
			"log_level": "debug",
			"ipam": {
				"type": "calico-ipam"
			},
			"kubernetes": {
				"k8s_api_root": "%s",
				"kubeconfig": "C:\\k\\config"
			},
			"policy": {"type": "k8s"}
		}`, cniVersion, networkName, os.Getenv("ETCD_ENDPOINTS"), os.Getenv("DATASTORE_TYPE"), os.Getenv("KUBERNETES_MASTER"))

		BeforeEach(func() {
			testutils.WipeK8sPods(netconf)
			time.Sleep(10000 * time.Millisecond)
			// Create a new ipPool.
			testutils.MustCreateNewIPPoolBlockSize(calicoClient, "10.0.0.0/26", false, false, true, 29)

			conf := types.NetConf{}
			if err := json.Unmarshal([]byte(netconf), &conf); err != nil {
				panic(err)
			}
			logger := log.WithFields(log.Fields{
				"Namespace": testutils.HnsNoneNs,
			})
			clientset, err = k8s.NewK8sClient(conf, logger)
			if err != nil {
				panic(err)
			}

			// Create a K8s Node object with PodCIDR and name equal to hostname.
			_, err = clientset.CoreV1().Nodes().Create(&v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: hostname},
				Spec: v1.NodeSpec{
					PodCIDR: "10.0.0.0/24",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			nsName = fmt.Sprintf("ns%d", rand.Uint32())
			// Create namespace
			ensureNamespace(clientset, nsName)
		})

		AfterEach(func() {
			// Delete the IP Pools.
			testutils.MustDeleteIPPool(calicoClient, "10.0.0.0/26")
			// Delete namespace
			deleteNamespace(clientset, nsName)
			clientset.CoreV1().Nodes().Delete(hostname, &metav1.DeleteOptions{})
			// Ensure network is created
			hnsNetwork, err := hcsshim.GetHNSNetworkByName(networkName)
			Expect(err).ShouldNot(HaveOccurred())
			_, err = hnsNetwork.Delete()
			Expect(err).ShouldNot(HaveOccurred())
		})

		It("with windows single network flag set,should successfully network 4 pods but reject networking 5th", func() {

			// Now create a K8s pod.
			name := ""
			var containerid []string
			var podName []string
			defer func() {
				for i, id := range containerid {
					time.Sleep(30000 * time.Millisecond)
					_, err := testutils.DeleteContainerWithId(netconf, podName[i], testutils.HnsNoneNs, id, nsName)
					Expect(err).ShouldNot(HaveOccurred())
				}
			}()
			for i := 0; i < 4; i++ {
				time.Sleep(30000 * time.Millisecond)
				name = fmt.Sprintf("run%d", rand.Uint32())
				pod, err := clientset.CoreV1().Pods(nsName).Create(
					&v1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: name,
						},
						Spec: v1.PodSpec{
							Containers: []v1.Container{{
								Name:  name,
								Image: "ignore",
							}},
							NodeName: hostname,
						},
					})

				Expect(err).NotTo(HaveOccurred())
				podName = append(podName, name)
				log.Infof("Created POD object: %v", pod)

				// Create the container, which will call CNI and by default it will create the container with interface name 'eth0'.
				containerID, _, _, _, _, err := testutils.CreateContainer(netconf, name, testutils.HnsNoneNs, "", nsName)
				containerid = append(containerid, containerID)
				Expect(err).ShouldNot(HaveOccurred())
				time.Sleep(10000 * time.Millisecond)
			}
			name = fmt.Sprintf("run%d", rand.Uint32())
			pod, err := clientset.CoreV1().Pods(nsName).Create(
				&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{{
							Name:  name,
							Image: "ignore",
						}},
						NodeName: hostname,
					},
				})

			Expect(err).NotTo(HaveOccurred())
			log.Infof("Created POD object: %v", pod)
			podName = append(podName, name)

			// Create the container, which will call CNI and by default it will create the container with interface name 'eth0'.
			containerID, _, _, _, _, err := testutils.CreateContainer(netconf, name, testutils.HnsNoneNs, "", nsName)
			containerid = append(containerid, containerID)
			Expect(err).Should(HaveOccurred())
		})
	})
	Context("With a /29 IPAM blockSize, without single network flag", func() {
		var nsName string
		var nwsName []string
		tempNWName := ""
		var nwName string
		networkName := "net10"
		var clientset *kubernetes.Clientset
		netconf := fmt.Sprintf(`
		{
			"cniVersion": "%s",
			"name": "%s",
			"type": "calico",
			"etcd_endpoints": "%s",
			"datastore_type": "%s",
			"nodename_file_optional": true,
			"log_level": "debug",
			"ipam": {
				"type": "calico-ipam"
			},
			"kubernetes": {
				"k8s_api_root": "%s",
				"kubeconfig": "C:\\k\\config"
			},
			"policy": {"type": "k8s"}
		}`, cniVersion, networkName, os.Getenv("ETCD_ENDPOINTS"), os.Getenv("DATASTORE_TYPE"), os.Getenv("KUBERNETES_MASTER"))

		BeforeEach(func() {
			testutils.WipeK8sPods(netconf)
			// Create a new ipPool.
			time.Sleep(10000 * time.Millisecond)
			testutils.MustCreateNewIPPoolBlockSize(calicoClient, "10.0.0.0/26", false, false, true, 29)

			conf := types.NetConf{}
			if err := json.Unmarshal([]byte(netconf), &conf); err != nil {
				panic(err)
			}
			logger := log.WithFields(log.Fields{
				"Namespace": testutils.HnsNoneNs,
			})
			clientset, err = k8s.NewK8sClient(conf, logger)
			if err != nil {
				panic(err)
			}

			// Create a K8s Node object with PodCIDR and name equal to hostname.
			_, err = clientset.CoreV1().Nodes().Create(&v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: hostname},
				Spec: v1.NodeSpec{
					PodCIDR: "10.0.0.0/24",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			nsName = fmt.Sprintf("ns%d", rand.Uint32())
			// Create namespace
			ensureNamespace(clientset, nsName)
		})

		AfterEach(func() {
			// Delete the IP Pools.
			testutils.MustDeleteIPPool(calicoClient, "10.0.0.0/26")
			// Delete namespace
			deleteNamespace(clientset, nsName)
			clientset.CoreV1().Nodes().Delete(hostname, &metav1.DeleteOptions{})
			for i := 0; i < len(nwsName); i++ {
				// Ensure network is deleted
				log.Debugf("Deleting Network : %v", nwsName[i])
				hnsNetwork, err := hcsshim.GetHNSNetworkByName(nwsName[i])
				Expect(err).ShouldNot(HaveOccurred())
				_, err = hnsNetwork.Delete()
				Expect(err).ShouldNot(HaveOccurred())
			}
		})
		It("with windows single network flag not set,should successfully network 4 pods and sucessfully create new network for 5th", func() {
			// Now create a K8s pod.
			name := ""
			var containerid []string
			var podName []string
			// Make sure the pod gets cleaned up, whether we fail or not.
			defer func() {
				log.Debugf("containerid = %v", containerid)
				for i, id := range containerid {
					_, err := testutils.DeleteContainerWithId(netconf, podName[i], testutils.HnsNoneNs, id, nsName)
					Expect(err).ShouldNot(HaveOccurred())
					time.Sleep(30000 * time.Millisecond)
				}
			}()

			for i := 0; i < 5; i++ {
				time.Sleep(30000 * time.Millisecond)
				name = fmt.Sprintf("run%d", rand.Uint32())
				_, err := clientset.CoreV1().Pods(nsName).Create(
					&v1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: name,
						},
						Spec: v1.PodSpec{
							Containers: []v1.Container{{
								Name:  name,
								Image: "ignore",
							}},
							NodeName: hostname,
						},
					})

				Expect(err).NotTo(HaveOccurred())
				podName = append(podName, name)
				// Create the container, which will call CNI and by default it will create the container with interface name 'eth0'.
				containerID, result, _, _, _, err := testutils.CreateContainer(netconf, name, testutils.HnsNoneNs, "", nsName)
				containerid = append(containerid, containerID)
				Expect(err).ShouldNot(HaveOccurred())
				_, subNet, _ := net.ParseCIDR(result.IPs[0].Address.String())
				nwName := utils.CreateNetworkName(networkName, subNet)
				if nwName != tempNWName {
					tempNWName = nwName
					nwsName = append(nwsName, nwName)
				}
			}
			Expect(nwsName).To(HaveLen(2))
		})
		It("create 4 pods; delete 3 pods; create 3 pods, should still have only one network", func() {
			// Now create a K8s pod.
			podName := []string{}
			containerid := []string{}
			nwsName = []string{}
			name := ""
			defer func() {
				log.Debugf("containerid = %v", containerid)
				for i, id := range containerid {
					_, err := testutils.DeleteContainerWithId(netconf, podName[i], testutils.HnsNoneNs, id, nsName)
					Expect(err).ShouldNot(HaveOccurred())
					clientset.CoreV1().Pods(nsName).Delete(podName[i], nil)
					Expect(err).ShouldNot(HaveOccurred())
				}
			}()

			for i := 0; i < 4; i++ {
				name = fmt.Sprintf("run%d", rand.Uint32())
				_, err := clientset.CoreV1().Pods(nsName).Create(
					&v1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: name,
						},
						Spec: v1.PodSpec{
							Containers: []v1.Container{{
								Name:  name,
								Image: "ignore",
							}},
							NodeName: hostname,
						},
					})

				Expect(err).NotTo(HaveOccurred())
				podName = append(podName, name)

				// Create the container, which will call CNI and by default it will create the container with interface name 'eth0'.
				containerID, result, _, _, _, err := testutils.CreateContainer(netconf, name, testutils.HnsNoneNs, "", nsName)
				containerid = append(containerid, containerID)
				Expect(err).ShouldNot(HaveOccurred())
				_, subNet, _ := net.ParseCIDR(result.IPs[0].Address.String())
				nwName = utils.CreateNetworkName(networkName, subNet)
				if nwName != tempNWName {
					tempNWName = nwName
					nwsName = append(nwsName, nwName)
				}
				time.Sleep(30000 * time.Millisecond)
			}
			for i := 0; i < 3; i++ {
				time.Sleep(30000 * time.Millisecond)
				_, err := testutils.DeleteContainerWithId(netconf, podName[i], testutils.HnsNoneNs, containerid[i], nsName)
				Expect(err).ShouldNot(HaveOccurred())
				clientset.CoreV1().Pods(nsName).Delete(podName[i], nil)
				Expect(err).ShouldNot(HaveOccurred())
			}
			for i := 0; i < 3; i++ {
				name = fmt.Sprintf("run%d", rand.Uint32())
				_, err := clientset.CoreV1().Pods(nsName).Create(
					&v1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: name,
						},
						Spec: v1.PodSpec{
							Containers: []v1.Container{{
								Name:  name,
								Image: "ignore",
							}},
							NodeName: hostname,
						},
					})

				Expect(err).NotTo(HaveOccurred())
				podName[i] = name

				// Create the container, which will call CNI and by default it will create the container with interface name 'eth0'.
				containerID, result, _, _, _, err := testutils.CreateContainer(netconf, name, testutils.HnsNoneNs, "", nsName)
				containerid[i] = containerID
				Expect(err).ShouldNot(HaveOccurred())
				_, subNet, _ := net.ParseCIDR(result.IPs[0].Address.String())
				nwNames := utils.CreateNetworkName(networkName, subNet)
				if nwNames != tempNWName {
					tempNWName = nwNames
					nwsName = append(nwsName, nwNames)
				}
				//Network should  be same
				Expect(nwName).Should(Equal(nwNames))
				time.Sleep(30000 * time.Millisecond)
			}
		})
	})
})
