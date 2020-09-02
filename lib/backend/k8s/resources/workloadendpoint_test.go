// Copyright (c) 2017-2020 Tigera, Inc. All rights reserved.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package resources_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	nettypes "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	uuid "github.com/satori/go.uuid"
	"k8s.io/apimachinery/pkg/types"

	"github.com/projectcalico/libcalico-go/lib/backend/k8s/conversion"
	"github.com/projectcalico/libcalico-go/lib/backend/k8s/resources"
	cerrors "github.com/projectcalico/libcalico-go/lib/errors"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/names"

	k8sapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

var _ = Describe("WorkloadEndpointClient", func() {
	Describe("Create", func() {
		Context("WorkloadEndpoint has no IPs set", func() {
			It("does not set the cni.projectcalico.org/podIP and cni.projectcalico.org/podIPs annotations", func() {
				k8sClient := fake.NewSimpleClientset(&k8sapi.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "simplePod",
						Namespace: "testNamespace",
					},
					Spec: k8sapi.PodSpec{
						NodeName: "test-node",
					},
				})

				wepClient := resources.NewWorkloadEndpointClient(k8sClient)

				wepIDs := names.WorkloadEndpointIdentifiers{
					Orchestrator: "k8s",
					Node:         "test-node",
					Pod:          "simplePod",
					Endpoint:     "eth0",
				}

				wepName, err := wepIDs.CalculateWorkloadEndpointName(false)
				Expect(err).ShouldNot(HaveOccurred())
				wep := &apiv3.WorkloadEndpoint{
					ObjectMeta: metav1.ObjectMeta{
						Name:      wepName,
						Namespace: "testNamespace",
					},
					Spec: apiv3.WorkloadEndpointSpec{
						IPNetworks: []string{},
					},
				}

				kvp := &model.KVPair{
					Key: model.ResourceKey{
						Name:      wep.Name,
						Namespace: wep.Namespace,
						Kind:      apiv3.KindWorkloadEndpoint,
					},
					Value: wep,
				}

				_, err = wepClient.Create(context.Background(), kvp)
				Expect(err).ShouldNot(HaveOccurred())

				pod, err := k8sClient.CoreV1().Pods("testNamespace").Get("simplePod", metav1.GetOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				Expect(pod.GetAnnotations()).Should(BeNil())
			})
		})
		Context("WorkloadEndpoint has IPs set", func() {
			It("sets the cni.projectcalico.org/podIP and cni.projectcalico.org/podIPs annotations to the WorkloadEndpoint IPs", func() {
				k8sClient := fake.NewSimpleClientset(&k8sapi.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "simplePod",
						Namespace: "testNamespace",
					},
					Spec: k8sapi.PodSpec{
						NodeName: "test-node",
					},
				})

				wepClient := resources.NewWorkloadEndpointClient(k8sClient)
				wepIDs := names.WorkloadEndpointIdentifiers{
					Orchestrator: "k8s",
					Node:         "test-node",
					Pod:          "simplePod",
					Endpoint:     "eth0",
				}

				wepName, err := wepIDs.CalculateWorkloadEndpointName(false)
				Expect(err).ShouldNot(HaveOccurred())
				wep := &apiv3.WorkloadEndpoint{
					ObjectMeta: metav1.ObjectMeta{
						Name:      wepName,
						Namespace: "testNamespace",
					},
					Spec: apiv3.WorkloadEndpointSpec{
						IPNetworks: []string{"192.168.91.117/32", "192.168.91.118/32"},
					},
				}

				kvp := &model.KVPair{
					Key: model.ResourceKey{
						Name:      wep.Name,
						Namespace: wep.Namespace,
						Kind:      apiv3.KindWorkloadEndpoint,
					},
					Value: wep,
				}

				_, err = wepClient.Create(context.Background(), kvp)
				Expect(err).ShouldNot(HaveOccurred())

				pod, err := k8sClient.CoreV1().Pods("testNamespace").Get("simplePod", metav1.GetOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				Expect(pod.GetAnnotations()).Should(Equal(map[string]string{
					conversion.AnnotationPodIP:  "192.168.91.117/32",
					conversion.AnnotationPodIPs: "192.168.91.117/32,192.168.91.118/32",
				}))
			})
		})
	})
	Describe("CreateNonDefault", func() {
		It("doesn't update the Pod for the WorkloadEndpoint", func() {
			k8sClient := fake.NewSimpleClientset(&k8sapi.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "simplePod",
					Namespace: "testNamespace",
				},
				Spec: k8sapi.PodSpec{
					NodeName: "test-node",
				},
			})

			wepClient := resources.NewWorkloadEndpointClient(k8sClient).(*resources.WorkloadEndpointClient)
			wepIDs := names.WorkloadEndpointIdentifiers{
				Orchestrator: "k8s",
				Node:         "test-node",
				Pod:          "simplePod",
				Endpoint:     "eth0",
			}

			wepName, err := wepIDs.CalculateWorkloadEndpointName(false)
			Expect(err).ShouldNot(HaveOccurred())
			wep := &apiv3.WorkloadEndpoint{
				ObjectMeta: metav1.ObjectMeta{
					Name:      wepName,
					Namespace: "testNamespace",
				},
				Spec: apiv3.WorkloadEndpointSpec{
					IPNetworks: []string{"192.168.91.117/32", "192.168.91.118/32"},
				},
			}

			kvp := &model.KVPair{
				Key: model.ResourceKey{
					Name:      wep.Name,
					Namespace: wep.Namespace,
					Kind:      apiv3.KindWorkloadEndpoint,
				},
				Value: wep,
			}

			_, err = wepClient.CreateNonDefault(context.Background(), kvp)
			Expect(err).ShouldNot(HaveOccurred())

			pod, err := k8sClient.CoreV1().Pods("testNamespace").Get("simplePod", metav1.GetOptions{})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(pod.GetAnnotations()).Should(BeNil())
		})
	})
	Describe("Update", func() {
		Context("WorkloadEndpoint has no IPs set", func() {
			It("does not set the cni.projectcalico.org/podIP and cni.projectcalico.org/podIPs annotations", func() {
				k8sClient := fake.NewSimpleClientset(&k8sapi.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "simplePod",
						Namespace: "testNamespace",
					},
					Spec: k8sapi.PodSpec{
						NodeName: "test-node",
					},
				})

				wepClient := resources.NewWorkloadEndpointClient(k8sClient)
				wepIDs := names.WorkloadEndpointIdentifiers{
					Orchestrator: "k8s",
					Node:         "test-node",
					Pod:          "simplePod",
					Endpoint:     "eth0",
				}

				wepName, err := wepIDs.CalculateWorkloadEndpointName(false)
				Expect(err).ShouldNot(HaveOccurred())
				wep := &apiv3.WorkloadEndpoint{
					ObjectMeta: metav1.ObjectMeta{
						Name:      wepName,
						Namespace: "testNamespace",
					},
					Spec: apiv3.WorkloadEndpointSpec{
						IPNetworks: []string{},
					},
				}

				kvp := &model.KVPair{
					Key: model.ResourceKey{
						Name:      wep.Name,
						Namespace: wep.Namespace,
						Kind:      apiv3.KindWorkloadEndpoint,
					},
					Value: wep,
				}

				_, err = wepClient.Update(context.Background(), kvp)
				Expect(err).ShouldNot(HaveOccurred())

				pod, err := k8sClient.CoreV1().Pods("testNamespace").Get("simplePod", metav1.GetOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				Expect(pod.GetAnnotations()).Should(BeNil())
			})
		})
		Context("WorkloadEndpoint has IPs set", func() {
			It("sets the cni.projectcalico.org/podIP and cni.projectcalico.org/podIPs annotations to the WorkloadEndpoint IPs", func() {
				k8sClient := fake.NewSimpleClientset(&k8sapi.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "simplePod",
						Namespace: "testNamespace",
					},
					Spec: k8sapi.PodSpec{
						NodeName: "test-node",
					},
				})

				wepClient := resources.NewWorkloadEndpointClient(k8sClient)
				wepIDs := names.WorkloadEndpointIdentifiers{
					Orchestrator: "k8s",
					Node:         "test-node",
					Pod:          "simplePod",
					Endpoint:     "eth0",
				}

				wepName, err := wepIDs.CalculateWorkloadEndpointName(false)
				Expect(err).ShouldNot(HaveOccurred())
				wep := &apiv3.WorkloadEndpoint{
					ObjectMeta: metav1.ObjectMeta{
						Name:      wepName,
						Namespace: "testNamespace",
					},
					Spec: apiv3.WorkloadEndpointSpec{
						IPNetworks: []string{"192.168.91.117/32", "192.168.91.118/32"},
					},
				}

				kvp := &model.KVPair{
					Key: model.ResourceKey{
						Name:      wep.Name,
						Namespace: wep.Namespace,
						Kind:      apiv3.KindWorkloadEndpoint,
					},
					Value: wep,
				}

				_, err = wepClient.Update(context.Background(), kvp)
				Expect(err).ShouldNot(HaveOccurred())

				pod, err := k8sClient.CoreV1().Pods("testNamespace").Get("simplePod", metav1.GetOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				Expect(pod.GetAnnotations()).Should(Equal(map[string]string{
					conversion.AnnotationPodIP:  "192.168.91.117/32",
					conversion.AnnotationPodIPs: "192.168.91.117/32,192.168.91.118/32",
				}))
			})
		})
	})

	Describe("UpdateNonDefault", func() {
		It("doesn't update the Pod for the WorkloadEndpoint", func() {
			k8sClient := fake.NewSimpleClientset(&k8sapi.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "simplePod",
					Namespace: "testNamespace",
				},
				Spec: k8sapi.PodSpec{
					NodeName: "test-node",
				},
			})

			wepClient := resources.NewWorkloadEndpointClient(k8sClient).(*resources.WorkloadEndpointClient)
			wepIDs := names.WorkloadEndpointIdentifiers{
				Orchestrator: "k8s",
				Node:         "test-node",
				Pod:          "simplePod",
				Endpoint:     "eth0",
			}

			wepName, err := wepIDs.CalculateWorkloadEndpointName(false)
			Expect(err).ShouldNot(HaveOccurred())
			wep := &apiv3.WorkloadEndpoint{
				ObjectMeta: metav1.ObjectMeta{
					Name:      wepName,
					Namespace: "testNamespace",
				},
				Spec: apiv3.WorkloadEndpointSpec{
					IPNetworks: []string{"192.168.91.117/32", "192.168.91.118/32"},
				},
			}

			kvp := &model.KVPair{
				Key: model.ResourceKey{
					Name:      wep.Name,
					Namespace: wep.Namespace,
					Kind:      apiv3.KindWorkloadEndpoint,
				},
				Value: wep,
			}

			_, err = wepClient.UpdateNonDefault(context.Background(), kvp)
			Expect(err).ShouldNot(HaveOccurred())

			pod, err := k8sClient.CoreV1().Pods("testNamespace").Get("simplePod", metav1.GetOptions{})
			Expect(err).ShouldNot(HaveOccurred())
			Expect(pod.GetAnnotations()).Should(BeNil())

		})
	})

	Describe("Delete", func() {
		Context("WorkloadEndpoint has no IPs set", func() {
			It("zeros out the cni.projectcalico.org/podIP and cni.projectcalico.org/podIPs annotations", func() {
				podUID := types.UID(uuid.NewV4().String())
				k8sClient := fake.NewSimpleClientset(&k8sapi.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "simplePod",
						Namespace: "testNamespace",
						Annotations: map[string]string{
							conversion.AnnotationPodIP:  "192.168.91.117/32",
							conversion.AnnotationPodIPs: "192.168.91.117/32,192.168.91.118/32",
						},
						UID: podUID,
					},
					Spec: k8sapi.PodSpec{
						NodeName: "test-node",
					},
				})

				wepIDs := names.WorkloadEndpointIdentifiers{
					Orchestrator: "k8s",
					Node:         "test-node",
					Pod:          "simplePod",
					Endpoint:     "eth0",
				}

				wepName, err := wepIDs.CalculateWorkloadEndpointName(false)
				Expect(err).ShouldNot(HaveOccurred())

				wepClient := resources.NewWorkloadEndpointClient(k8sClient)
				key := model.ResourceKey{
					Name:      wepName,
					Namespace: "testNamespace",
					Kind:      apiv3.KindWorkloadEndpoint,
				}
				wep, err := wepClient.Get(context.Background(), key, "")
				Expect(err).NotTo(HaveOccurred())

				// Doesn't work because the fake k8s client allows the UID to be changed.
				//
				// By("Ignoring requests with the wrong UID.")
				// wrongUID := types.UID("19e9c0f4-501d-429f-b581-8954440883f4")
				// _, err = wepClient.Delete(context.Background(), key, wep.Revision, &wrongUID)
				// Expect(err).ShouldNot(HaveOccurred())
				// pod, err := k8sClient.CoreV1().Pods("testNamespace").Get("simplePod", metav1.GetOptions{})
				// Expect(err).ShouldNot(HaveOccurred())
				// Expect(pod.GetAnnotations()).Should(Equal(map[string]string{
				// 	conversion.AnnotationPodIP:  "192.168.91.117/32",
				// 	conversion.AnnotationPodIPs: "192.168.91.117/32,192.168.91.118/32",
				// }))

				By("Accepting requests with the right UID.")
				_, err = wepClient.Delete(context.Background(), key, wep.Revision, wep.UID)
				Expect(err).ShouldNot(HaveOccurred())
				pod, err := k8sClient.CoreV1().Pods("testNamespace").Get("simplePod", metav1.GetOptions{})
				Expect(err).ShouldNot(HaveOccurred())
				Expect(pod.GetAnnotations()).Should(Equal(map[string]string{
					conversion.AnnotationPodIP:  "",
					conversion.AnnotationPodIPs: "",
				}))
			})
		})
	})

	Describe("Get", func() {
		It("gets the WorkloadEndpoint using the given name", func() {
			k8sClient := fake.NewSimpleClientset(&k8sapi.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "simplePod",
					Namespace: "testNamespace",
				},
				Spec: k8sapi.PodSpec{
					NodeName: "test-node",
				},
			})

			wepClient := resources.NewWorkloadEndpointClient(k8sClient).(*resources.WorkloadEndpointClient)
			wepIDs := names.WorkloadEndpointIdentifiers{
				Orchestrator: "k8s",
				Node:         "test-node",
				Pod:          "simplePod",
				Endpoint:     "eth0",
			}

			wepName, err := wepIDs.CalculateWorkloadEndpointName(false)
			Expect(err).ShouldNot(HaveOccurred())

			wep, err := wepClient.Get(context.Background(), model.ResourceKey{
				Name:      wepName,
				Namespace: "testNamespace",
				Kind:      apiv3.KindWorkloadEndpoint,
			}, "")

			Expect(err).ShouldNot(HaveOccurred())
			Expect(wep.Value).Should(Equal(&apiv3.WorkloadEndpoint{
				TypeMeta: metav1.TypeMeta{
					Kind:       apiv3.KindWorkloadEndpoint,
					APIVersion: apiv3.GroupVersionCurrent,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      wepName,
					Namespace: "testNamespace",
					Labels: map[string]string{
						apiv3.LabelNamespace:    "testNamespace",
						apiv3.LabelOrchestrator: "k8s",
					},
				},
				Spec: apiv3.WorkloadEndpointSpec{
					Orchestrator:  "k8s",
					Node:          "test-node",
					Pod:           "simplePod",
					Endpoint:      "eth0",
					Profiles:      []string{"kns.testNamespace"},
					IPNetworks:    []string{},
					InterfaceName: "caliedff4356bd6",
				},
			}))
		})
	})
	Describe("List", func() {
		Context("name is specified", func() {
			Context("the name contains an end suffix", func() {
				It("returns a list of WorkloadEndpoints with the single WorkloadEndpoint with the given name", func() {
					testListWorkloadEndpoints(
						[]runtime.Object{&k8sapi.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "simplePod",
								Namespace: "testNamespace",
							},
							Spec: k8sapi.PodSpec{
								NodeName: "test-node",
							},
							Status: k8sapi.PodStatus{
								PodIP: "192.168.91.113",
							},
						}},
						model.ResourceListOptions{
							Name:      "test--node-k8s-simplePod-eth0",
							Namespace: "testNamespace",
							Kind:      apiv3.KindWorkloadEndpoint,
						},
						[]*apiv3.WorkloadEndpoint{{
							TypeMeta: metav1.TypeMeta{
								Kind:       apiv3.KindWorkloadEndpoint,
								APIVersion: apiv3.GroupVersionCurrent,
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test--node-k8s-simplePod-eth0",
								Namespace: "testNamespace",
								Labels: map[string]string{
									apiv3.LabelNamespace:    "testNamespace",
									apiv3.LabelOrchestrator: "k8s",
								},
							},
							Spec: apiv3.WorkloadEndpointSpec{
								Orchestrator:  "k8s",
								Node:          "test-node",
								Pod:           "simplePod",
								Endpoint:      "eth0",
								Profiles:      []string{"kns.testNamespace"},
								IPNetworks:    []string{"192.168.91.113/32"},
								InterfaceName: "caliedff4356bd6",
							},
						}},
					)
				})
				It("returns an empty list if the endpoint is specified and does not match the pods wep endpoint", func() {
					testListWorkloadEndpoints(
						[]runtime.Object{&k8sapi.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "simplePod",
								Namespace: "testNamespace",
							},
							Spec: k8sapi.PodSpec{
								NodeName: "test-node",
							},
							Status: k8sapi.PodStatus{
								PodIP: "192.168.91.113",
							},
						}},
						model.ResourceListOptions{
							Name:      "test--node-k8s-simplePod-ens4",
							Namespace: "testNamespace",
							Kind:      apiv3.KindWorkloadEndpoint,
						},
						[]*apiv3.WorkloadEndpoint(nil),
					)
				})
			})
			Context("the name does not contain endpoint suffix, but contains the Pod name midfix", func() {
				It("returns a list of WorkloadEndpoints with the single WorkloadEndpoint for the matching pod", func() {
					testListWorkloadEndpoints(
						[]runtime.Object{&k8sapi.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "simplePod",
								Namespace: "testNamespace",
							},
							Spec: k8sapi.PodSpec{
								NodeName: "test-node",
							},
							Status: k8sapi.PodStatus{
								PodIP: "192.168.91.113",
							},
						}},
						model.ResourceListOptions{
							Name:      "test--node-k8s-simplePod",
							Namespace: "testNamespace",
							Kind:      apiv3.KindWorkloadEndpoint,
						},
						[]*apiv3.WorkloadEndpoint{{
							TypeMeta: metav1.TypeMeta{
								Kind:       apiv3.KindWorkloadEndpoint,
								APIVersion: apiv3.GroupVersionCurrent,
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test--node-k8s-simplePod-eth0",
								Namespace: "testNamespace",
								Labels: map[string]string{
									apiv3.LabelNamespace:    "testNamespace",
									apiv3.LabelOrchestrator: "k8s",
								},
							},
							Spec: apiv3.WorkloadEndpointSpec{
								Orchestrator:  "k8s",
								Node:          "test-node",
								Pod:           "simplePod",
								Endpoint:      "eth0",
								Profiles:      []string{"kns.testNamespace"},
								IPNetworks:    []string{"192.168.91.113/32"},
								InterfaceName: "caliedff4356bd6",
							},
						}},
					)
				})
			})
			Context("name contains neither the endpoint suffix or the pod name midfix", func() {
				It("returns an error", func() {
					k8sClient := fake.NewSimpleClientset(&k8sapi.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "simplePod",
							Namespace: "testNamespace",
						},
						Spec: k8sapi.PodSpec{
							NodeName: "test-node",
						},
						Status: k8sapi.PodStatus{
							PodIP: "192.168.91.113",
						},
					})
					wepClient := resources.NewWorkloadEndpointClient(k8sClient).(*resources.WorkloadEndpointClient)

					_, err := wepClient.List(context.Background(), model.ResourceListOptions{
						Name:      "test--node-k8s",
						Namespace: "testNamespace",
						Kind:      apiv3.KindWorkloadEndpoint,
					}, "")

					Expect(err).Should(Equal(cerrors.ErrorResourceDoesNotExist{
						Identifier: model.ResourceListOptions{
							Name:      "test--node-k8s",
							Namespace: "testNamespace",
							Kind:      apiv3.KindWorkloadEndpoint,
						},
						Err: errors.New("malformed WorkloadEndpoint name - unable to determine Pod name"),
					}))
				})
			})
		})
		Context("name is not specified", func() {
			It("returns WorkloadEndpoints for each pod in the namespace", func() {
				testListWorkloadEndpoints(
					[]runtime.Object{
						&k8sapi.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "simplePod",
								Namespace: "testNamespace",
							},
							Spec: k8sapi.PodSpec{
								NodeName: "test-node",
							},
							Status: k8sapi.PodStatus{
								PodIP: "192.168.91.113",
							},
						},
						&k8sapi.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "simplePod2",
								Namespace: "testNamespace",
							},
							Spec: k8sapi.PodSpec{
								NodeName: "test-node",
							},
							Status: k8sapi.PodStatus{
								PodIP: "192.168.91.120",
							},
						},
					},
					model.ResourceListOptions{
						Namespace: "testNamespace",
						Kind:      apiv3.KindWorkloadEndpoint,
					},
					[]*apiv3.WorkloadEndpoint{
						{
							TypeMeta: metav1.TypeMeta{
								Kind:       apiv3.KindWorkloadEndpoint,
								APIVersion: apiv3.GroupVersionCurrent,
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test--node-k8s-simplePod-eth0",
								Namespace: "testNamespace",
								Labels: map[string]string{
									apiv3.LabelNamespace:    "testNamespace",
									apiv3.LabelOrchestrator: "k8s",
								},
							},
							Spec: apiv3.WorkloadEndpointSpec{
								Orchestrator:  "k8s",
								Node:          "test-node",
								Pod:           "simplePod",
								Endpoint:      "eth0",
								Profiles:      []string{"kns.testNamespace"},
								IPNetworks:    []string{"192.168.91.113/32"},
								InterfaceName: "caliedff4356bd6",
							},
						},
						{
							TypeMeta: metav1.TypeMeta{
								Kind:       apiv3.KindWorkloadEndpoint,
								APIVersion: apiv3.GroupVersionCurrent,
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test--node-k8s-simplePod2-eth0",
								Namespace: "testNamespace",
								Labels: map[string]string{
									apiv3.LabelNamespace:    "testNamespace",
									apiv3.LabelOrchestrator: "k8s",
								},
							},
							Spec: apiv3.WorkloadEndpointSpec{
								Orchestrator:  "k8s",
								Node:          "test-node",
								Pod:           "simplePod2",
								Endpoint:      "eth0",
								Profiles:      []string{"kns.testNamespace"},
								IPNetworks:    []string{"192.168.91.120/32"},
								InterfaceName: "cali4274eb44391",
							},
						},
					},
				)
			})
		})
	})
	Describe("Watch", func() {
		Context("Pod added", func() {
			It("returns a single event containing the Pod's WorkloadEndpoint", func() {
				testWatchWorkloadEndpoints([]*k8sapi.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "simplePod",
							Namespace: "testNamespace",
						},
						Spec: k8sapi.PodSpec{
							NodeName: "test-node",
						},
						Status: k8sapi.PodStatus{
							PodIP: "192.168.91.113",
						},
					},
				}, []*apiv3.WorkloadEndpoint{{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindWorkloadEndpoint,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test--node-k8s-simplePod-eth0",
						Namespace: "testNamespace",
						Labels: map[string]string{
							apiv3.LabelNamespace:    "testNamespace",
							apiv3.LabelOrchestrator: "k8s",
						},
					},
					Spec: apiv3.WorkloadEndpointSpec{
						Orchestrator:  "k8s",
						Node:          "test-node",
						Pod:           "simplePod",
						Endpoint:      "eth0",
						Profiles:      []string{"kns.testNamespace"},
						IPNetworks:    []string{"192.168.91.113/32"},
						InterfaceName: "caliedff4356bd6",
					},
				}})
			})
		})
		Context("Terminating Pods and normal Pod added", func() {
			It("should ignore the IPs of a deleted pod with released IPs", func() {
				now := metav1.Now()
				testWatchWorkloadEndpoints([]*k8sapi.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "termPod",
							Namespace:         "testNamespace",
							DeletionTimestamp: &now,
							Annotations: map[string]string{
								conversion.AnnotationPodIP:  "192.168.91.114",
								conversion.AnnotationPodIPs: "192.168.91.114",
							},
						},
						Spec: k8sapi.PodSpec{
							NodeName: "test-node",
						},
						Status: k8sapi.PodStatus{
							PodIP: "192.168.91.114",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "termPod2",
							Namespace:         "testNamespace",
							DeletionTimestamp: &now,
							Annotations: map[string]string{
								// Empty annotation signals that the CNI plugin has released the IP.
								conversion.AnnotationPodIP:  "",
								conversion.AnnotationPodIPs: "",
							},
						},
						Spec: k8sapi.PodSpec{
							NodeName: "test-node",
						},
						Status: k8sapi.PodStatus{
							PodIP: "192.168.91.115",
						},
					},
				}, []*apiv3.WorkloadEndpoint{
					{
						TypeMeta: metav1.TypeMeta{
							Kind:       apiv3.KindWorkloadEndpoint,
							APIVersion: apiv3.GroupVersionCurrent,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test--node-k8s-termPod-eth0",
							Namespace: "testNamespace",
							Labels: map[string]string{
								apiv3.LabelNamespace:    "testNamespace",
								apiv3.LabelOrchestrator: "k8s",
							},
						},
						Spec: apiv3.WorkloadEndpointSpec{
							Orchestrator:  "k8s",
							Node:          "test-node",
							Pod:           "termPod",
							Endpoint:      "eth0",
							Profiles:      []string{"kns.testNamespace"},
							IPNetworks:    []string{"192.168.91.114/32"},
							InterfaceName: "calidfce31fd9be",
						},
					},
					{
						TypeMeta: metav1.TypeMeta{
							Kind:       apiv3.KindWorkloadEndpoint,
							APIVersion: apiv3.GroupVersionCurrent,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test--node-k8s-termPod2-eth0",
							Namespace: "testNamespace",
							Labels: map[string]string{
								apiv3.LabelNamespace:    "testNamespace",
								apiv3.LabelOrchestrator: "k8s",
							},
						},
						Spec: apiv3.WorkloadEndpointSpec{
							Orchestrator:  "k8s",
							Node:          "test-node",
							Pod:           "termPod2",
							Endpoint:      "eth0",
							Profiles:      []string{"kns.testNamespace"},
							IPNetworks:    []string{},
							InterfaceName: "cali9591578421e",
						},
					},
				})
			})
		})
	})
})

var _ = Describe("WorkloadEndpointClient with multi-NICs enabled", func() {
	BeforeEach(func() {
		Expect(os.Setenv("MULTI_INTERFACE_MODE", "multus")).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.Setenv("MULTI_INTERFACE_MODE", "")).ShouldNot(HaveOccurred())
	})

	Describe("Get", func() {
		Context("no CNCF annotations on Pod", func() {
			It("gets the default WorkloadEndpoint using the default interface name", func() {
				k8sClient := fake.NewSimpleClientset(&k8sapi.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "simplePod",
						Namespace: "testNamespace",
					},
					Spec: k8sapi.PodSpec{
						NodeName: "test-node",
					},
				})

				wepClient := resources.NewWorkloadEndpointClient(k8sClient).(*resources.WorkloadEndpointClient)
				wepIDs := names.WorkloadEndpointIdentifiers{
					Orchestrator: "k8s",
					Node:         "test-node",
					Pod:          "simplePod",
					Endpoint:     "eth0",
				}

				wepName, err := wepIDs.CalculateWorkloadEndpointName(false)
				Expect(err).ShouldNot(HaveOccurred())

				wep, err := wepClient.Get(context.Background(), model.ResourceKey{
					Name:      wepName,
					Namespace: "testNamespace",
					Kind:      apiv3.KindWorkloadEndpoint,
				}, "")

				Expect(err).ShouldNot(HaveOccurred())
				Expect(wep.Value).Should(Equal(&apiv3.WorkloadEndpoint{
					TypeMeta: metav1.TypeMeta{
						Kind:       apiv3.KindWorkloadEndpoint,
						APIVersion: apiv3.GroupVersionCurrent,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      wepName,
						Namespace: "testNamespace",
						Labels: map[string]string{
							apiv3.LabelNamespace:        "testNamespace",
							apiv3.LabelOrchestrator:     "k8s",
							apiv3.LabelNetwork:          "k8s-pod-network",
							apiv3.LabelNetworkNamespace: "testNamespace",
							apiv3.LabelNetworkInterface: "eth0",
						},
					},
					Spec: apiv3.WorkloadEndpointSpec{
						Orchestrator:  "k8s",
						Node:          "test-node",
						Pod:           "simplePod",
						Endpoint:      "eth0",
						Profiles:      []string{"kns.testNamespace"},
						IPNetworks:    []string{},
						InterfaceName: "caliedff4356bd6",
					},
				}))
			})
		})

		Context("CNCF annotations on Pod", func() {
			When("the pods default calico interface is not eth0", func() {
				It("gets the default WorkloadEndpoint using the non eth0 name", func() {
					k8sClient := fake.NewSimpleClientset(&k8sapi.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "simplePod",
							Namespace: "testNamespace",
							Annotations: map[string]string{
								nettypes.NetworkStatusAnnot: mustMarshal([]*nettypes.NetworkStatus{
									{
										Name:      "calico-default-network",
										Interface: "ens4",
										IPs:       []string{"192.168.91.113"},
										Mac:       "9e:e7:7e:9d:8f:e0",
									},
								}),
							},
						},
						Spec: k8sapi.PodSpec{
							NodeName: "test-node",
						},
						Status: k8sapi.PodStatus{
							PodIP: "192.168.91.113",
						},
					})

					wepClient := resources.NewWorkloadEndpointClient(k8sClient).(*resources.WorkloadEndpointClient)
					wepIDs := names.WorkloadEndpointIdentifiers{
						Orchestrator: "k8s",
						Node:         "test-node",
						Pod:          "simplePod",
						Endpoint:     "ens4",
					}

					wepName, err := wepIDs.CalculateWorkloadEndpointName(false)
					Expect(err).ShouldNot(HaveOccurred())

					wep, err := wepClient.Get(context.Background(), model.ResourceKey{
						Name:      wepName,
						Namespace: "testNamespace",
						Kind:      apiv3.KindWorkloadEndpoint,
					}, "")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(wep.Value).Should(Equal(&apiv3.WorkloadEndpoint{
						TypeMeta: metav1.TypeMeta{
							Kind:       apiv3.KindWorkloadEndpoint,
							APIVersion: apiv3.GroupVersionCurrent,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      wepName,
							Namespace: "testNamespace",
							Labels: map[string]string{
								apiv3.LabelNamespace:        "testNamespace",
								apiv3.LabelOrchestrator:     "k8s",
								apiv3.LabelNetwork:          "calico-default-network",
								apiv3.LabelNetworkNamespace: "testNamespace",
								apiv3.LabelNetworkInterface: "ens4",
							},
						},
						Spec: apiv3.WorkloadEndpointSpec{
							Orchestrator:  "k8s",
							Node:          "test-node",
							Pod:           "simplePod",
							Endpoint:      "ens4",
							Profiles:      []string{"kns.testNamespace"},
							IPNetworks:    []string{"192.168.91.113/32"},
							InterfaceName: "caliedff4356bd6",
						},
					}))
				})
			})
			When("when the pod has multiple interfaces and an additional one is selected", func() {
				It("returns the correct Workload endpoint", func() {
					k8sClient := fake.NewSimpleClientset(&k8sapi.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "simplePod",
							Namespace: "testNamespace",
							Annotations: map[string]string{
								nettypes.NetworkAttachmentAnnot: "calico1,calico2",
								nettypes.NetworkStatusAnnot: mustMarshal([]*nettypes.NetworkStatus{
									{
										Name:      "calico-default-network",
										Interface: "ens4",
										IPs:       []string{"192.168.91.113"},
										Mac:       "9e:e7:7e:9d:8f:e0",
									},
									{
										Name:      "calico1",
										Interface: "net1",
										IPs:       []string{"192.168.91.114"},
										Mac:       "62:45:f5:10:97:c1",
									},
									{
										Name:      "calico2",
										Interface: "net2",
										IPs:       []string{"192.168.91.115"},
										Mac:       "62:76:f5:90:27:c1",
									},
								}),
							},
						},
						Spec: k8sapi.PodSpec{
							NodeName: "test-node",
						},
						Status: k8sapi.PodStatus{
							PodIP: "192.168.91.113",
						},
					})

					wepClient := resources.NewWorkloadEndpointClient(k8sClient).(*resources.WorkloadEndpointClient)
					wepIDs := names.WorkloadEndpointIdentifiers{
						Orchestrator: "k8s",
						Node:         "test-node",
						Pod:          "simplePod",
						Endpoint:     "net1",
					}

					wepName, err := wepIDs.CalculateWorkloadEndpointName(false)
					Expect(err).ShouldNot(HaveOccurred())

					wep, err := wepClient.Get(context.Background(), model.ResourceKey{
						Name:      wepName,
						Namespace: "testNamespace",
						Kind:      apiv3.KindWorkloadEndpoint,
					}, "")

					Expect(err).ShouldNot(HaveOccurred())
					Expect(wep.Value).Should(Equal(&apiv3.WorkloadEndpoint{
						TypeMeta: metav1.TypeMeta{
							Kind:       apiv3.KindWorkloadEndpoint,
							APIVersion: apiv3.GroupVersionCurrent,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      wepName,
							Namespace: "testNamespace",
							Labels: map[string]string{
								apiv3.LabelNamespace:        "testNamespace",
								apiv3.LabelOrchestrator:     "k8s",
								apiv3.LabelNetwork:          "calico1",
								apiv3.LabelNetworkNamespace: "testNamespace",
								apiv3.LabelNetworkInterface: "net1",
							},
						},
						Spec: apiv3.WorkloadEndpointSpec{
							Orchestrator:  "k8s",
							Node:          "test-node",
							Pod:           "simplePod",
							Endpoint:      "net1",
							Profiles:      []string{"kns.testNamespace"},
							IPNetworks:    []string{"192.168.91.114/32"},
							InterfaceName: "calim15X7UGVV5N",
						},
					}))
				})
			})
		})
	})

	Describe("List", func() {
		Context("name is specified", func() {
			Context("the name contains an interface suffix", func() {
				When("there is no WorkloadEndpoint matching the given name", func() {
					It("returns an empty list", func() {
						testListWorkloadEndpoints(
							[]runtime.Object{&k8sapi.Pod{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "simplePod",
									Namespace: "testNamespace",
								},
								Spec: k8sapi.PodSpec{
									NodeName: "test-node",
								},
								Status: k8sapi.PodStatus{
									PodIP: "192.168.91.113",
								},
							}},
							model.ResourceListOptions{
								Name:      "test--node-k8s-simplePod-ens4",
								Namespace: "testNamespace",
								Kind:      apiv3.KindWorkloadEndpoint,
							},
							[]*apiv3.WorkloadEndpoint(nil),
						)
					})
				})
			})
			Context("the name contains a Pod name midfix, but no interface suffix", func() {
				When("there are multiple calico interfaces for the matching pod", func() {
					It("returns a WorkloadEndpoint for each calico interface on the Pod", func() {
						testListWorkloadEndpoints(
							[]runtime.Object{&k8sapi.Pod{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "simplePod",
									Namespace: "testNamespace",
									Annotations: map[string]string{
										nettypes.NetworkAttachmentAnnot: "calico1,calico2",
										nettypes.NetworkStatusAnnot: mustMarshal([]*nettypes.NetworkStatus{
											{
												Name:      "calico-default-network",
												Interface: "ens4",
												IPs:       []string{"192.168.91.113"},
												Mac:       "9e:e7:7e:9d:8f:e0",
											},
											{
												Name:      "calico1",
												Interface: "net1",
												IPs:       []string{"192.168.91.114"},
												Mac:       "62:45:f5:10:97:c1",
											},
											{
												Name:      "calico2",
												Interface: "net2",
												IPs:       []string{"192.168.91.115"},
												Mac:       "62:76:f5:90:27:c1",
											},
										}),
									},
								},
								Spec: k8sapi.PodSpec{
									NodeName: "test-node",
								},
								Status: k8sapi.PodStatus{
									PodIP: "192.168.91.113",
								},
							},
								&k8sapi.Pod{
									ObjectMeta: metav1.ObjectMeta{
										Name:      "simplePod2",
										Namespace: "testNamespace",
										Annotations: map[string]string{
											nettypes.NetworkAttachmentAnnot: "calico1,calico2",
											nettypes.NetworkStatusAnnot: mustMarshal([]*nettypes.NetworkStatus{
												{
													Name:      "calico-default-network",
													Interface: "ens4",
													IPs:       []string{"192.168.91.120"},
													Mac:       "1e:a7:7e:8d:8f:e0",
												},
											}),
										},
									},
									Spec: k8sapi.PodSpec{
										NodeName: "test-node",
									},
									Status: k8sapi.PodStatus{
										PodIP: "192.168.91.120",
									},
								}},
							model.ResourceListOptions{
								Name:      "test--node-k8s-simplePod-",
								Namespace: "testNamespace",
								Kind:      apiv3.KindWorkloadEndpoint,
							},
							[]*apiv3.WorkloadEndpoint{
								{
									TypeMeta: metav1.TypeMeta{
										Kind:       apiv3.KindWorkloadEndpoint,
										APIVersion: apiv3.GroupVersionCurrent,
									},
									ObjectMeta: metav1.ObjectMeta{
										Name:      "test--node-k8s-simplePod-ens4",
										Namespace: "testNamespace",
										Labels: map[string]string{
											apiv3.LabelNamespace:        "testNamespace",
											apiv3.LabelOrchestrator:     "k8s",
											apiv3.LabelNetwork:          "calico-default-network",
											apiv3.LabelNetworkNamespace: "testNamespace",
											apiv3.LabelNetworkInterface: "ens4",
										},
									},
									Spec: apiv3.WorkloadEndpointSpec{
										Orchestrator:  "k8s",
										Node:          "test-node",
										Pod:           "simplePod",
										Endpoint:      "ens4",
										Profiles:      []string{"kns.testNamespace"},
										IPNetworks:    []string{"192.168.91.113/32"},
										InterfaceName: "caliedff4356bd6",
									},
								},
								{
									TypeMeta: metav1.TypeMeta{
										Kind:       apiv3.KindWorkloadEndpoint,
										APIVersion: apiv3.GroupVersionCurrent,
									},
									ObjectMeta: metav1.ObjectMeta{
										Name:      "test--node-k8s-simplePod-net1",
										Namespace: "testNamespace",
										Labels: map[string]string{
											apiv3.LabelNamespace:        "testNamespace",
											apiv3.LabelOrchestrator:     "k8s",
											apiv3.LabelNetwork:          "calico1",
											apiv3.LabelNetworkNamespace: "testNamespace",
											apiv3.LabelNetworkInterface: "net1",
										},
									},
									Spec: apiv3.WorkloadEndpointSpec{
										Orchestrator:  "k8s",
										Node:          "test-node",
										Pod:           "simplePod",
										Endpoint:      "net1",
										Profiles:      []string{"kns.testNamespace"},
										IPNetworks:    []string{"192.168.91.114/32"},
										InterfaceName: "calim15X7UGVV5N",
									},
								},
								{
									TypeMeta: metav1.TypeMeta{
										Kind:       apiv3.KindWorkloadEndpoint,
										APIVersion: apiv3.GroupVersionCurrent,
									},
									ObjectMeta: metav1.ObjectMeta{
										Name:      "test--node-k8s-simplePod-net2",
										Namespace: "testNamespace",
										Labels: map[string]string{
											apiv3.LabelNamespace:        "testNamespace",
											apiv3.LabelOrchestrator:     "k8s",
											apiv3.LabelNetwork:          "calico2",
											apiv3.LabelNetworkNamespace: "testNamespace",
											apiv3.LabelNetworkInterface: "net2",
										},
									},
									Spec: apiv3.WorkloadEndpointSpec{
										Orchestrator:  "k8s",
										Node:          "test-node",
										Pod:           "simplePod",
										Endpoint:      "net2",
										Profiles:      []string{"kns.testNamespace"},
										IPNetworks:    []string{"192.168.91.115/32"},
										InterfaceName: "calim25X7UGVV5N",
									},
								},
							},
						)
					})
				})
				When("the Pod only contains only a default non eth0 calico interface", func() {
					It("returns a list of WorkloadEndpoints containing a WorkloadEndpoint for the default interface", func() {
						testListWorkloadEndpoints(
							[]runtime.Object{
								&k8sapi.Pod{
									ObjectMeta: metav1.ObjectMeta{
										Name:      "simplePod",
										Namespace: "testNamespace",
										Annotations: map[string]string{
											nettypes.NetworkAttachmentAnnot: "calico1,calico2",
											nettypes.NetworkStatusAnnot: mustMarshal([]*nettypes.NetworkStatus{{
												Name:      "calico-default-network",
												Interface: "ens4",
												IPs:       []string{"192.168.91.113"},
												Mac:       "9e:e7:7e:9d:8f:e0",
											}}),
										},
									},
									Spec: k8sapi.PodSpec{
										NodeName: "test-node",
									},
									Status: k8sapi.PodStatus{
										PodIP: "192.168.91.113",
									},
								},
								&k8sapi.Pod{
									ObjectMeta: metav1.ObjectMeta{
										Name:      "simplePod2",
										Namespace: "testNamespace",
										Annotations: map[string]string{
											nettypes.NetworkAttachmentAnnot: "calico1,calico2",
											nettypes.NetworkStatusAnnot: mustMarshal([]*nettypes.NetworkStatus{
												{
													Name:      "calico-default-network",
													Interface: "ens4",
													IPs:       []string{"192.168.91.120"},
													Mac:       "1e:a7:7e:8d:8f:e0",
												},
											}),
										},
									},
									Spec: k8sapi.PodSpec{
										NodeName: "test-node",
									},
									Status: k8sapi.PodStatus{
										PodIP: "192.168.91.120",
									},
								},
							},
							model.ResourceListOptions{
								Name:      "test--node-k8s-simplePod-",
								Namespace: "testNamespace",
								Kind:      apiv3.KindWorkloadEndpoint,
							},
							[]*apiv3.WorkloadEndpoint{{
								TypeMeta: metav1.TypeMeta{
									Kind:       apiv3.KindWorkloadEndpoint,
									APIVersion: apiv3.GroupVersionCurrent,
								},
								ObjectMeta: metav1.ObjectMeta{
									Name:      "test--node-k8s-simplePod-ens4",
									Namespace: "testNamespace",
									Labels: map[string]string{
										apiv3.LabelNamespace:        "testNamespace",
										apiv3.LabelOrchestrator:     "k8s",
										apiv3.LabelNetwork:          "calico-default-network",
										apiv3.LabelNetworkNamespace: "testNamespace",
										apiv3.LabelNetworkInterface: "ens4",
									},
								},
								Spec: apiv3.WorkloadEndpointSpec{
									Orchestrator:  "k8s",
									Node:          "test-node",
									Pod:           "simplePod",
									Endpoint:      "ens4",
									Profiles:      []string{"kns.testNamespace"},
									IPNetworks:    []string{"192.168.91.113/32"},
									InterfaceName: "caliedff4356bd6",
								},
							}},
						)
					})
				})
			})
		})
		Context("name is not specified", func() {
			When("the specified namespace has multiple Pods with only default non eth0 interfaces", func() {
				It("returns WorkloadEndpoints for each interface on each pod in the namespace", func() {
					testListWorkloadEndpoints(
						[]runtime.Object{
							&k8sapi.Pod{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "simplePod",
									Namespace: "testNamespace",
									Annotations: map[string]string{
										nettypes.NetworkStatusAnnot: mustMarshal([]*nettypes.NetworkStatus{
											{
												Name:      "calico-default-network",
												Interface: "ens4",
												IPs:       []string{"192.168.91.113"},
												Mac:       "9e:e7:7e:9d:8f:e0",
											},
										}),
									},
								},
								Spec: k8sapi.PodSpec{
									NodeName: "test-node",
								},
								Status: k8sapi.PodStatus{
									PodIP: "192.168.91.113",
								},
							},
							&k8sapi.Pod{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "simplePod2",
									Namespace: "testNamespace",
									Annotations: map[string]string{
										nettypes.NetworkAttachmentAnnot: "calico1,calico2",
										nettypes.NetworkStatusAnnot: mustMarshal([]*nettypes.NetworkStatus{
											{
												Name:      "calico-default-network",
												Interface: "ens4",
												IPs:       []string{"192.168.91.120"},
												Mac:       "1e:a7:7e:8d:8f:e0",
											},
										}),
									},
								},
								Spec: k8sapi.PodSpec{
									NodeName: "test-node",
								},
								Status: k8sapi.PodStatus{
									PodIP: "192.168.91.120",
								},
							},
						},
						model.ResourceListOptions{
							Namespace: "testNamespace",
							Kind:      apiv3.KindWorkloadEndpoint,
						},
						[]*apiv3.WorkloadEndpoint{
							{
								TypeMeta: metav1.TypeMeta{
									Kind:       apiv3.KindWorkloadEndpoint,
									APIVersion: apiv3.GroupVersionCurrent,
								},
								ObjectMeta: metav1.ObjectMeta{
									Name:      "test--node-k8s-simplePod-ens4",
									Namespace: "testNamespace",
									Labels: map[string]string{
										apiv3.LabelNamespace:        "testNamespace",
										apiv3.LabelOrchestrator:     "k8s",
										apiv3.LabelNetwork:          "calico-default-network",
										apiv3.LabelNetworkNamespace: "testNamespace",
										apiv3.LabelNetworkInterface: "ens4",
									},
								},
								Spec: apiv3.WorkloadEndpointSpec{
									Orchestrator:  "k8s",
									Node:          "test-node",
									Pod:           "simplePod",
									Endpoint:      "ens4",
									Profiles:      []string{"kns.testNamespace"},
									IPNetworks:    []string{"192.168.91.113/32"},
									InterfaceName: "caliedff4356bd6",
								},
							},
							{
								TypeMeta: metav1.TypeMeta{
									Kind:       apiv3.KindWorkloadEndpoint,
									APIVersion: apiv3.GroupVersionCurrent,
								},
								ObjectMeta: metav1.ObjectMeta{
									Name:      "test--node-k8s-simplePod2-ens4",
									Namespace: "testNamespace",
									Labels: map[string]string{
										apiv3.LabelNamespace:        "testNamespace",
										apiv3.LabelOrchestrator:     "k8s",
										apiv3.LabelNetwork:          "calico-default-network",
										apiv3.LabelNetworkNamespace: "testNamespace",
										apiv3.LabelNetworkInterface: "ens4",
									},
								},
								Spec: apiv3.WorkloadEndpointSpec{
									Orchestrator:  "k8s",
									Node:          "test-node",
									Pod:           "simplePod2",
									Endpoint:      "ens4",
									Profiles:      []string{"kns.testNamespace"},
									IPNetworks:    []string{"192.168.91.120/32"},
									InterfaceName: "cali4274eb44391",
								},
							},
						},
					)
				})
			})
			When("the specified namespace has multiple Pods each with multiple interfaces", func() {
				It("returns WorkloadEndpoints for the default interfaces on each Pod in the namespace", func() {
					testListWorkloadEndpoints(
						[]runtime.Object{
							&k8sapi.Pod{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "simplePod",
									Namespace: "testNamespace",
									Annotations: map[string]string{
										nettypes.NetworkAttachmentAnnot: "cali1",
										nettypes.NetworkStatusAnnot: mustMarshal([]*nettypes.NetworkStatus{
											{
												Name:      "calico-default-network",
												Interface: "ens4",
												IPs:       []string{"192.168.91.113"},
												Mac:       "9e:e7:7e:9d:8f:e0",
											},
											{
												Name:      "cali1",
												Interface: "net1",
												IPs:       []string{"192.168.91.114"},
												Mac:       "7f:e7:3e:9d:8f:a0",
											},
										}),
									},
								},
								Spec: k8sapi.PodSpec{
									NodeName: "test-node",
								},
								Status: k8sapi.PodStatus{
									PodIP: "192.168.91.113",
								},
							},
							&k8sapi.Pod{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "simplePod2",
									Namespace: "testNamespace",
									Annotations: map[string]string{
										nettypes.NetworkAttachmentAnnot: "s2cali1",
										nettypes.NetworkStatusAnnot: mustMarshal([]*nettypes.NetworkStatus{
											{
												Name:      "calico-default-network",
												Interface: "ens4",
												IPs:       []string{"192.168.91.120"},
												Mac:       "1e:a7:7e:8d:8f:e0",
											},
											{
												Name:      "s2cali1",
												Interface: "net1",
												IPs:       []string{"192.168.91.121"},
												Mac:       "5a:a7:7e:8d:8f:e1",
											},
										}),
									},
								},
								Spec: k8sapi.PodSpec{
									NodeName: "test-node",
								},
								Status: k8sapi.PodStatus{
									PodIP: "192.168.91.120",
								},
							}},
						model.ResourceListOptions{
							Namespace: "testNamespace",
							Kind:      apiv3.KindWorkloadEndpoint,
						},
						[]*apiv3.WorkloadEndpoint{
							{
								TypeMeta: metav1.TypeMeta{
									Kind:       apiv3.KindWorkloadEndpoint,
									APIVersion: apiv3.GroupVersionCurrent,
								},
								ObjectMeta: metav1.ObjectMeta{
									Name:      "test--node-k8s-simplePod-ens4",
									Namespace: "testNamespace",
									Labels: map[string]string{
										apiv3.LabelNamespace:        "testNamespace",
										apiv3.LabelOrchestrator:     "k8s",
										apiv3.LabelNetwork:          "calico-default-network",
										apiv3.LabelNetworkNamespace: "testNamespace",
										apiv3.LabelNetworkInterface: "ens4",
									},
								},
								Spec: apiv3.WorkloadEndpointSpec{
									Orchestrator:  "k8s",
									Node:          "test-node",
									Pod:           "simplePod",
									Endpoint:      "ens4",
									Profiles:      []string{"kns.testNamespace"},
									IPNetworks:    []string{"192.168.91.113/32"},
									InterfaceName: "caliedff4356bd6",
								},
							},
							{
								TypeMeta: metav1.TypeMeta{
									Kind:       apiv3.KindWorkloadEndpoint,
									APIVersion: apiv3.GroupVersionCurrent,
								},
								ObjectMeta: metav1.ObjectMeta{
									Name:      "test--node-k8s-simplePod-net1",
									Namespace: "testNamespace",
									Labels: map[string]string{
										apiv3.LabelNamespace:        "testNamespace",
										apiv3.LabelOrchestrator:     "k8s",
										apiv3.LabelNetwork:          "cali1",
										apiv3.LabelNetworkNamespace: "testNamespace",
										apiv3.LabelNetworkInterface: "net1",
									},
								},
								Spec: apiv3.WorkloadEndpointSpec{
									Orchestrator:  "k8s",
									Node:          "test-node",
									Pod:           "simplePod",
									Endpoint:      "net1",
									Profiles:      []string{"kns.testNamespace"},
									IPNetworks:    []string{"192.168.91.114/32"},
									InterfaceName: "calim15X7UGVV5N",
								},
							},
							{
								TypeMeta: metav1.TypeMeta{
									Kind:       apiv3.KindWorkloadEndpoint,
									APIVersion: apiv3.GroupVersionCurrent,
								},
								ObjectMeta: metav1.ObjectMeta{
									Name:      "test--node-k8s-simplePod2-ens4",
									Namespace: "testNamespace",
									Labels: map[string]string{
										apiv3.LabelNamespace:        "testNamespace",
										apiv3.LabelOrchestrator:     "k8s",
										apiv3.LabelNetwork:          "calico-default-network",
										apiv3.LabelNetworkNamespace: "testNamespace",
										apiv3.LabelNetworkInterface: "ens4",
									},
								},
								Spec: apiv3.WorkloadEndpointSpec{
									Orchestrator:  "k8s",
									Node:          "test-node",
									Pod:           "simplePod2",
									Endpoint:      "ens4",
									Profiles:      []string{"kns.testNamespace"},
									IPNetworks:    []string{"192.168.91.120/32"},
									InterfaceName: "cali4274eb44391",
								},
							},
							{
								TypeMeta: metav1.TypeMeta{
									Kind:       apiv3.KindWorkloadEndpoint,
									APIVersion: apiv3.GroupVersionCurrent,
								},
								ObjectMeta: metav1.ObjectMeta{
									Name:      "test--node-k8s-simplePod2-net1",
									Namespace: "testNamespace",
									Labels: map[string]string{
										apiv3.LabelNamespace:        "testNamespace",
										apiv3.LabelOrchestrator:     "k8s",
										apiv3.LabelNetwork:          "s2cali1",
										apiv3.LabelNetworkNamespace: "testNamespace",
										apiv3.LabelNetworkInterface: "net1",
									},
								},
								Spec: apiv3.WorkloadEndpointSpec{
									Orchestrator:  "k8s",
									Node:          "test-node",
									Pod:           "simplePod2",
									Endpoint:      "net1",
									Profiles:      []string{"kns.testNamespace"},
									IPNetworks:    []string{"192.168.91.121/32"},
									InterfaceName: "calim1IJ2OWRBZC",
								},
							},
						},
					)
				})
			})
		})
	})
	Describe("Watch", func() {
		Context("Pod added", func() {
			When("the Pod has multiple calico interfaces", func() {
				It("returns an event for each WorkloadEndpoint for the Pod", func() {
					testWatchWorkloadEndpoints(&k8sapi.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "simplePod",
							Namespace: "testNamespace",
							Annotations: map[string]string{
								nettypes.NetworkAttachmentAnnot: "cali1,cal2",
								nettypes.NetworkStatusAnnot: mustMarshal([]*nettypes.NetworkStatus{
									{
										Name:      "calico-default-network",
										Interface: "ens4",
										IPs:       []string{"192.168.91.113"},
										Mac:       "9e:e7:7e:9d:8f:e0",
									},
									{
										Name:      "cali1",
										Interface: "net1",
										IPs:       []string{"192.168.91.114"},
										Mac:       "7f:e7:3e:9d:8f:a0",
									},
									{
										Name:      "cali2",
										Interface: "net2",
										IPs:       []string{"192.168.91.115"},
										Mac:       "2a:e7:7e:9d:8f:a3",
									},
								}),
							},
						},
						Spec: k8sapi.PodSpec{
							NodeName: "test-node",
						},
						Status: k8sapi.PodStatus{
							PodIP: "192.168.91.113",
						},
					}, []*apiv3.WorkloadEndpoint{
						{
							TypeMeta: metav1.TypeMeta{
								Kind:       apiv3.KindWorkloadEndpoint,
								APIVersion: apiv3.GroupVersionCurrent,
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test--node-k8s-simplePod-ens4",
								Namespace: "testNamespace",
								Labels: map[string]string{
									apiv3.LabelNamespace:        "testNamespace",
									apiv3.LabelOrchestrator:     "k8s",
									apiv3.LabelNetwork:          "calico-default-network",
									apiv3.LabelNetworkNamespace: "testNamespace",
									apiv3.LabelNetworkInterface: "ens4",
								},
							},
							Spec: apiv3.WorkloadEndpointSpec{
								Orchestrator:  "k8s",
								Node:          "test-node",
								Pod:           "simplePod",
								Endpoint:      "ens4",
								Profiles:      []string{"kns.testNamespace"},
								IPNetworks:    []string{"192.168.91.113/32"},
								InterfaceName: "caliedff4356bd6",
							},
						},
						{
							TypeMeta: metav1.TypeMeta{
								Kind:       apiv3.KindWorkloadEndpoint,
								APIVersion: apiv3.GroupVersionCurrent,
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test--node-k8s-simplePod-net1",
								Namespace: "testNamespace",
								Labels: map[string]string{
									apiv3.LabelNamespace:        "testNamespace",
									apiv3.LabelOrchestrator:     "k8s",
									apiv3.LabelNetwork:          "cali1",
									apiv3.LabelNetworkNamespace: "testNamespace",
									apiv3.LabelNetworkInterface: "net1",
								},
							},
							Spec: apiv3.WorkloadEndpointSpec{
								Orchestrator:  "k8s",
								Node:          "test-node",
								Pod:           "simplePod",
								Endpoint:      "net1",
								Profiles:      []string{"kns.testNamespace"},
								IPNetworks:    []string{"192.168.91.114/32"},
								InterfaceName: "calim15X7UGVV5N",
							},
						},
						{
							TypeMeta: metav1.TypeMeta{
								Kind:       apiv3.KindWorkloadEndpoint,
								APIVersion: apiv3.GroupVersionCurrent,
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test--node-k8s-simplePod-net2",
								Namespace: "testNamespace",
								Labels: map[string]string{
									apiv3.LabelNamespace:        "testNamespace",
									apiv3.LabelOrchestrator:     "k8s",
									apiv3.LabelNetwork:          "cali2",
									apiv3.LabelNetworkNamespace: "testNamespace",
									apiv3.LabelNetworkInterface: "net2",
								},
							},
							Spec: apiv3.WorkloadEndpointSpec{
								Orchestrator:  "k8s",
								Node:          "test-node",
								Pod:           "simplePod",
								Endpoint:      "net2",
								Profiles:      []string{"kns.testNamespace"},
								IPNetworks:    []string{"192.168.91.115/32"},
								InterfaceName: "calim25X7UGVV5N",
							},
						},
					})
				})
			})
		})
	})
})

func mustMarshal(v interface{}) string {
	jsonStr, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(jsonStr)
}

func testListWorkloadEndpoints(pods []runtime.Object, listOptions model.ResourceListOptions, expectedWEPs []*apiv3.WorkloadEndpoint) {
	k8sClient := fake.NewSimpleClientset(pods...)
	wepClient := resources.NewWorkloadEndpointClient(k8sClient).(*resources.WorkloadEndpointClient)

	kvps, err := wepClient.List(context.Background(), listOptions, "")
	Expect(err).ShouldNot(HaveOccurred())

	var weps []*apiv3.WorkloadEndpoint
	for _, kvp := range kvps.KVPairs {
		weps = append(weps, kvp.Value.(*apiv3.WorkloadEndpoint))
	}

	Expect(weps).Should(Equal(expectedWEPs))
}

func testWatchWorkloadEndpoints(pods []*k8sapi.Pod, expectedWEPs []*apiv3.WorkloadEndpoint) {
	k8sClient := fake.NewSimpleClientset()

	wepClient := resources.NewWorkloadEndpointClient(k8sClient).(*resources.WorkloadEndpointClient)
	wepWatcher, err := wepClient.Watch(context.Background(), model.ResourceListOptions{}, "")

	Expect(err).ShouldNot(HaveOccurred())

	timer := time.NewTimer(1 * time.Second)
	defer timer.Stop()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer GinkgoRecover()
		i := 0

		for {
			select {
			case event := <-wepWatcher.ResultChan():
				Expect(event.Error).ShouldNot(HaveOccurred())
				Expect(event.New.Value).Should(Equal(expectedWEPs[i]))

				i++
				if i == len(expectedWEPs) {
					return
				}
			case <-timer.C:
				Fail(fmt.Sprintf("expected exactly %d events before timer expired, received %d", len(expectedWEPs), i))
			}
		}
	}()

	for _, pod := range pods {
		_, err = k8sClient.CoreV1().Pods(pod.Namespace).Create(pod)
		Expect(err).ShouldNot(HaveOccurred())
	}

	wg.Wait()
	wepWatcher.Stop()
}
