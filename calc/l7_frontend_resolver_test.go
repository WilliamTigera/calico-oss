// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package calc_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	"github.com/stretchr/testify/mock"
	kapiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/projectcalico/felix/config"

	"github.com/projectcalico/felix/calc"
	"github.com/projectcalico/felix/ip"
	"github.com/projectcalico/felix/labelindex"
	"github.com/projectcalico/felix/proto"
	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	"github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
)

// Mocked callbacks for ipSetUpdateCallbacks
type ipSetMockCallbacks struct {
	mock.Mock
}

func (m *ipSetMockCallbacks) OnIPSetAdded(setID string, ipSetType proto.IPSetUpdate_IPSetType) {
	_ = m.Called(setID, ipSetType)
}

func (m *ipSetMockCallbacks) OnIPSetRemoved(setID string) {
	_ = m.Called(setID)
}

func (m *ipSetMockCallbacks) OnIPSetMemberAdded(setID string, ip labelindex.IPSetMember) {
	_ = m.Called(setID, ip)
}

func (m *ipSetMockCallbacks) OnIPSetMemberRemoved(setID string, setMember labelindex.IPSetMember) {
	_ = m.Called(setID, setMember)
}

type output struct {
	setId    string
	ipAddr   string
	port     int32
	protocol labelindex.IPSetPortProtocol

	// ipv6Port is set only for IPv6 ports, which are signalled by the Family field on the IPSetMember.
	ipv6Port bool
}

var _ = Describe("L7FrontEndResolver", func() {

	var configEnabled = &config.Config{TPROXYMode: "EnabledAllServices"}

	DescribeTable("Check ipset callbacks for updates",
		func(updates []api.Update, addedMembers []output, removedMembers []output, conf *config.Config) {
			var mockCallbacks = &ipSetMockCallbacks{}

			mockCallbacks.On("OnIPSetAdded", calc.TPROXYServicesIPSet, proto.IPSetUpdate_IP_AND_PORT)
			mockCallbacks.On("OnIPSetAdded", calc.TPROXYNodePortsTCPIPSet, proto.IPSetUpdate_PORTS)

			for _, addedMember := range addedMembers {
				switch addedMember.setId {
				case calc.TPROXYServicesIPSet:
					member := labelindex.IPSetMember{
						PortNumber: uint16(addedMember.port),
						Protocol:   addedMember.protocol,
					}
					if addedMember.ipAddr != "" {
						member.CIDR = ip.FromString(addedMember.ipAddr).AsCIDR()
					}
					mockCallbacks.On("OnIPSetMemberAdded", addedMember.setId, member)
				case calc.TPROXYNodePortsTCPIPSet:
					member := labelindex.IPSetMember{
						PortNumber: uint16(addedMember.port),
					}
					if addedMember.ipv6Port {
						member.Family = 6
					} else {
						member.Family = 4
					}
					mockCallbacks.On("OnIPSetMemberAdded", addedMember.setId, member)
				}
			}

			for _, removedMember := range removedMembers {
				switch removedMember.setId {
				case calc.TPROXYServicesIPSet:
					member := labelindex.IPSetMember{
						PortNumber: uint16(removedMember.port),
						Protocol:   removedMember.protocol,
					}
					if removedMember.ipAddr != "" {
						member.CIDR = ip.FromString(removedMember.ipAddr).AsCIDR()
					}
					mockCallbacks.On("OnIPSetMemberRemoved", removedMember.setId, member)
				case calc.TPROXYNodePortsTCPIPSet:
					member := labelindex.IPSetMember{
						PortNumber: uint16(removedMember.port),
					}
					if removedMember.ipv6Port {
						member.Family = 6
					} else {
						member.Family = 4
					}
					mockCallbacks.On("OnIPSetMemberRemoved", removedMember.setId, member)
				}
			}

			var resolver = calc.NewL7FrontEndResolver(mockCallbacks, conf)

			for _, update := range updates {
				resolver.OnResourceUpdate(update)
			}

			mockCallbacks.AssertNumberOfCalls(GinkgoT(), "OnIPSetMemberAdded", len(addedMembers))
			mockCallbacks.AssertNumberOfCalls(GinkgoT(), "OnIPSetMemberRemoved", len(removedMembers))
			mockCallbacks.AssertExpectations(GinkgoT())
		},
		Entry("Service update without L7 annotation should result in no updates",
			[]api.Update{{
				KVPair: model.KVPair{
					Key: model.ResourceKey{Kind: v3.KindK8sService, Name: "service1", Namespace: "ns1"},
					Value: &kapiv1.Service{
						Spec: kapiv1.ServiceSpec{
							ClusterIP: "10.0.0.0",
							ExternalIPs: []string{
								"10.0.0.10",
								"10.0.0.20",
							},
							Ports: []kapiv1.ServicePort{
								{
									Port:     int32(123),
									Protocol: kapiv1.ProtocolTCP,
									Name:     "namedport",
								},
							},
						},
					},
				},
				UpdateType: api.UpdateTypeKVNew,
			}},
			[]output{},
			[]output{},
			&config.Config{},
		),
		Entry("Config with TPROXYMode EnabledDebug should update without annotation",
			[]api.Update{{
				KVPair: model.KVPair{
					Key: model.ResourceKey{Kind: v3.KindK8sService, Name: "service1", Namespace: "ns1"},
					Value: &kapiv1.Service{
						Spec: kapiv1.ServiceSpec{
							ClusterIP: "10.0.0.0",
							ExternalIPs: []string{
								"10.0.0.10",
								"10.0.0.20",
							},
							Ports: []kapiv1.ServicePort{
								{
									Port:     int32(123),
									Protocol: kapiv1.ProtocolTCP,
									NodePort: 456,
									Name:     "namedport",
								},
							},
						},
					},
				},
				UpdateType: api.UpdateTypeKVNew,
			}},
			[]output{{
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.0",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.10",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.20",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				// There are always 2 port updates, one for v4 and one for v6
				setId: calc.TPROXYNodePortsTCPIPSet,
				port:  456,
			}, {
				setId:    calc.TPROXYNodePortsTCPIPSet,
				port:     456,
				ipv6Port: true,
			}},
			[]output{},
			configEnabled,
		),
		Entry("Service with L7 annotation (Cluster Ip, Node Port)",
			[]api.Update{{
				KVPair: model.KVPair{
					Key: model.ResourceKey{Kind: v3.KindK8sService, Name: "service1", Namespace: "ns1"},
					Value: &kapiv1.Service{
						ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"projectcalico.org/l7-logging": "true"}},
						Spec: kapiv1.ServiceSpec{
							ClusterIP: "10.0.0.0",
							ExternalIPs: []string{
								"10.0.0.10",
								"10.0.0.20",
							},
							Ports: []kapiv1.ServicePort{
								{
									Port:     123,
									NodePort: 456,
									Protocol: kapiv1.ProtocolTCP,
									Name:     "namedport",
								},
							},
						},
					},
				},
				UpdateType: api.UpdateTypeKVNew,
			}},
			[]output{{
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.0",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.10",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.20",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId: calc.TPROXYNodePortsTCPIPSet,
				port:  456,
			}, {
				setId:    calc.TPROXYNodePortsTCPIPSet,
				port:     456,
				ipv6Port: true,
			}},
			[]output{},
			&config.Config{},
		),
		Entry("Service with L7 annotation other than TCP protocol should result in no updates",
			[]api.Update{{
				KVPair: model.KVPair{
					Key: model.ResourceKey{Kind: v3.KindK8sService, Name: "service1", Namespace: "ns1"},
					Value: &kapiv1.Service{
						ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"projectcalico.org/l7-logging": "true"}},
						Spec: kapiv1.ServiceSpec{
							ClusterIP: "10.0.0.0",
							ExternalIPs: []string{
								"10.0.0.10",
								"10.0.0.20",
							},
							Ports: []kapiv1.ServicePort{
								{
									Port:     123,
									NodePort: 234,
									Protocol: kapiv1.ProtocolUDP,
									Name:     "namedport",
								},
							},
						},
					},
				},
				UpdateType: api.UpdateTypeKVNew,
			}},
			[]output{},
			[]output{},
			&config.Config{},
		),
		Entry("delete update for nodeport with L7 annotation should remove the nodeport only ",
			[]api.Update{{
				KVPair: model.KVPair{
					Key: model.ResourceKey{Kind: v3.KindK8sService, Name: "service1", Namespace: "ns1"},
					Value: &kapiv1.Service{
						ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"projectcalico.org/l7-logging": "true"}},
						Spec: kapiv1.ServiceSpec{
							ClusterIP: "10.0.0.0",
							ExternalIPs: []string{
								"10.0.0.10",
								"10.0.0.20",
							},
							Ports: []kapiv1.ServicePort{
								{
									Port:     123,
									NodePort: 456,
									Protocol: kapiv1.ProtocolTCP,
									Name:     "namedport",
								},
							},
						},
					},
				},
				UpdateType: api.UpdateTypeKVNew,
			}, {
				KVPair: model.KVPair{
					Key: model.ResourceKey{Kind: v3.KindK8sService, Name: "service1", Namespace: "ns1"},
					Value: &kapiv1.Service{
						ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"projectcalico.org/l7-logging": "true"}},
						Spec: kapiv1.ServiceSpec{
							ClusterIP: "10.0.0.0",
							ExternalIPs: []string{
								"10.0.0.10",
								"10.0.0.20",
							},
							Ports: []kapiv1.ServicePort{
								{
									Port:     123,
									Protocol: kapiv1.ProtocolTCP,
									Name:     "namedport",
								},
							},
						},
					},
				},
				UpdateType: api.UpdateTypeKVUpdated,
			}},
			[]output{{
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.0",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.10",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.20",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId: calc.TPROXYNodePortsTCPIPSet,
				port:  456,
			}, {
				setId:    calc.TPROXYNodePortsTCPIPSet,
				port:     456,
				ipv6Port: true,
			}},
			[]output{{
				setId: calc.TPROXYNodePortsTCPIPSet,
				port:  456,
			}, {
				setId:    calc.TPROXYNodePortsTCPIPSet,
				port:     456,
				ipv6Port: true,
			}},
			&config.Config{},
		),
		Entry("delete update for service with L7 annotation should remove endpoints from ipset ",
			[]api.Update{{
				KVPair: model.KVPair{
					Key: model.ResourceKey{Kind: v3.KindK8sService, Name: "service1", Namespace: "ns1"},
					Value: &kapiv1.Service{
						ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"projectcalico.org/l7-logging": "true"}},
						Spec: kapiv1.ServiceSpec{
							ClusterIP: "10.0.0.0",
							ExternalIPs: []string{
								"10.0.0.10",
								"10.0.0.20",
							},
							Ports: []kapiv1.ServicePort{
								{
									Port:     123,
									NodePort: 456,
									Protocol: kapiv1.ProtocolTCP,
									Name:     "namedport",
								},
							},
						},
					},
				},
				UpdateType: api.UpdateTypeKVNew,
			}, {
				KVPair: model.KVPair{
					Key: model.ResourceKey{Kind: v3.KindK8sService, Name: "service1", Namespace: "ns1"},
				},
				UpdateType: api.UpdateTypeKVDeleted,
			}},
			[]output{{
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.0",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.10",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.20",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId: calc.TPROXYNodePortsTCPIPSet,
				port:  456,
			}, {
				setId:    calc.TPROXYNodePortsTCPIPSet,
				port:     456,
				ipv6Port: true,
			}},
			[]output{{
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.0",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.10",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.20",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId: calc.TPROXYNodePortsTCPIPSet,
				port:  456,
			}, {
				setId:    calc.TPROXYNodePortsTCPIPSet,
				port:     456,
				ipv6Port: true,
			}},
			&config.Config{},
		),
		Entry("update for L7 annotated service without L7 annotation anymore should remove them from ipset",
			[]api.Update{{
				KVPair: model.KVPair{
					Key: model.ResourceKey{Kind: v3.KindK8sService, Name: "service1", Namespace: "ns1"},
					Value: &kapiv1.Service{
						ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"projectcalico.org/l7-logging": "true"}},
						Spec: kapiv1.ServiceSpec{
							ClusterIP: "10.0.0.0",
							ExternalIPs: []string{
								"10.0.0.10",
								"10.0.0.20",
							},
							Ports: []kapiv1.ServicePort{
								{
									Port:     123,
									NodePort: 456,
									Protocol: kapiv1.ProtocolTCP,
									Name:     "namedport",
								},
							},
						},
					},
				},
				UpdateType: api.UpdateTypeKVNew,
			}, {
				KVPair: model.KVPair{
					Key: model.ResourceKey{Kind: v3.KindK8sService, Name: "service1", Namespace: "ns1"},
					Value: &kapiv1.Service{
						ObjectMeta: metav1.ObjectMeta{},
					},
				},
				UpdateType: api.UpdateTypeKVNew,
			}},
			[]output{{
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.0",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.10",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.20",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId: calc.TPROXYNodePortsTCPIPSet,
				port:  456,
			}, {
				setId:    calc.TPROXYNodePortsTCPIPSet,
				port:     456,
				ipv6Port: true,
			}},
			[]output{{
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.0",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.10",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "10.0.0.20",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId: calc.TPROXYNodePortsTCPIPSet,
				port:  456,
			}, {
				setId:    calc.TPROXYNodePortsTCPIPSet,
				port:     456,
				ipv6Port: true,
			}},
			&config.Config{},
		),
		Entry("Service with L7 annotation with IPV6",
			[]api.Update{{
				KVPair: model.KVPair{
					Key: model.ResourceKey{Kind: v3.KindK8sService, Name: "service1", Namespace: "ns1"},
					Value: &kapiv1.Service{
						ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"projectcalico.org/l7-logging": "true"}},
						Spec: kapiv1.ServiceSpec{
							ClusterIP: "2001:569:7007:1a00:45ac:2caa:a3be:5e10",
							Ports: []kapiv1.ServicePort{
								{
									Port:     123,
									NodePort: 456,
									Protocol: kapiv1.ProtocolTCP,
									Name:     "namedport",
								},
							},
						},
					},
				},
				UpdateType: api.UpdateTypeKVNew,
			}},
			[]output{{
				setId:    calc.TPROXYServicesIPSet,
				ipAddr:   "2001:569:7007:1a00:45ac:2caa:a3be:5e10",
				port:     123,
				protocol: labelindex.ProtocolTCP,
			}, {
				setId: calc.TPROXYNodePortsTCPIPSet,
				port:  456,
			}, {
				setId:    calc.TPROXYNodePortsTCPIPSet,
				port:     456,
				ipv6Port: true,
			}},
			[]output{},
			&config.Config{},
		),
	)
})
