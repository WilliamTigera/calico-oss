// Copyright (c) 2020-2021 Tigera, Inc. All rights reserved.

package calc

import (
	"reflect"
	"strings"
	"sync"

	"github.com/projectcalico/felix/k8sutils"

	log "github.com/sirupsen/logrus"

	kapiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/proxy"

	v3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"

	"github.com/projectcalico/felix/dispatcher"
	"github.com/projectcalico/felix/ip"
)

const (
	multiPortsSameService = "*"
)

// IP/port/proto key used to lookup a service.
type ipPortProtoKey struct {
	ip    [16]byte
	port  int
	proto int
}

type portProtoKey struct {
	port  int
	proto int
}

// ServiceUpdateHandler handles all the service related updates coming from
// update dispatcher.
//
// This component should included in other components which can register
// themselves with update dispatcher. Ex ServiceLookupsCache and TproxyEndPointsResolver
//
// It processes the updates passed to it and creates three maps. One contains all services
// passed to it, other two contains node port services, regular services.
type ServiceUpdateHandler struct {
	mutex sync.RWMutex

	// Service relationships and cached resource info.
	ipPortProtoToServices map[ipPortProtoKey][]proxy.ServicePortName
	nodePortServices      map[portProtoKey][]proxy.ServicePortName
	services              map[model.ResourceKey]kapiv1.ServiceSpec
}

func NewServiceUpdateHandler() *ServiceUpdateHandler {
	suh := &ServiceUpdateHandler{
		mutex:                 sync.RWMutex{},
		ipPortProtoToServices: make(map[ipPortProtoKey][]proxy.ServicePortName),
		nodePortServices:      make(map[portProtoKey][]proxy.ServicePortName),
		services:              make(map[model.ResourceKey]kapiv1.ServiceSpec),
	}
	return suh
}

func (suh *ServiceUpdateHandler) handleService(
	key model.ResourceKey, svc kapiv1.ServiceSpec,
	epOperator func(key ipPortProtoKey, svc proxy.ServicePortName),
	nodePortOperator func(key portProtoKey, svc proxy.ServicePortName),
) {
	log.Infof("Handle service %s", key)

	// Construct a full set of service IPs from the cluster IP and other ExternalIPs.
	serviceIPs := make([][16]byte, 0, len(svc.ExternalIPs)+1)
	if ipa, ok := IPStringToArray(svc.ClusterIP); ok {
		serviceIPs = append(serviceIPs, ipa)
	}
	for _, e := range svc.ExternalIPs {
		if ipa, ok := IPStringToArray(e); ok {
			serviceIPs = append(serviceIPs, ipa)
		}
	}

	// Track the port names for the various port and node port combinations.
	ipPortProtoNames := map[ipPortProtoKey]string{}
	nodePortProtoNames := map[portProtoKey]string{}
	for _, p := range svc.Ports {
		// Loop through all of the ports in the service and process an entry for each ip/port/proto combination.
		for _, ipa := range serviceIPs {
			key := ipPortProtoKey{ip: ipa, port: int(p.Port), proto: k8sutils.GetProtocolAsInt(p.Protocol)}
			if existing, ok := ipPortProtoNames[key]; ok && existing != p.Name {
				ipPortProtoNames[key] = multiPortsSameService
			} else {
				ipPortProtoNames[key] = p.Name
			}
		}

		// If this also contains a node port then store that mapping too.
		key := portProtoKey{port: int(p.NodePort), proto: k8sutils.GetProtocolAsInt(p.Protocol)}
		if existing, ok := nodePortProtoNames[key]; ok && existing != p.Name {
			nodePortProtoNames[key] = multiPortsSameService
		} else {
			nodePortProtoNames[key] = p.Name
		}
	}

	// Operate on each of the ip port proto combinations with port names.
	for ik, name := range ipPortProtoNames {
		epOperator(ik, proxy.ServicePortName{
			NamespacedName: types.NamespacedName{
				Name: key.Name, Namespace: key.Namespace,
			},
			Port:     name,
			Protocol: k8sutils.GetProtocolFromInt(ik.proto),
		})
	}

	// Operate on the node port data.
	for pk, name := range nodePortProtoNames {
		nodePortOperator(pk, proxy.ServicePortName{
			NamespacedName: types.NamespacedName{
				Name: key.Name, Namespace: key.Namespace,
			},
			Port:     name,
			Protocol: k8sutils.GetProtocolFromInt(pk.proto),
		})
	}
}

func (suh *ServiceUpdateHandler) addServiceMap(key ipPortProtoKey, svc proxy.ServicePortName) {
	suh.ipPortProtoToServices[key] = append(suh.ipPortProtoToServices[key], svc)
}

func (suh *ServiceUpdateHandler) removeServiceMap(key ipPortProtoKey, svc proxy.ServicePortName) {
	svcs := suh.ipPortProtoToServices[key]
	// Remove entry from the services slice and update the mapping. We can just iterate through the slice and
	// shift across once we find the entry.
	found := false
	for i := range svcs {
		if found {
			svcs[i-1] = svcs[i]
		} else {
			found = svcs[i] == svc
		}
	}
	if found {
		svcs = svcs[:len(svcs)-1]
		suh.ipPortProtoToServices[key] = svcs
	}
	if len(svcs) == 0 {
		// No more services for the cluster IP, so just remove the cluster IP to service mapping
		delete(suh.ipPortProtoToServices, key)
		return
	}
}

// addOrUpdateService tracks service cluster IP to service mappings.
func (suh *ServiceUpdateHandler) addOrUpdateService(key model.ResourceKey, service *kapiv1.Service) {
	suh.mutex.Lock()
	defer suh.mutex.Unlock()

	if existing, ok := suh.services[key]; ok {
		if reflect.DeepEqual(existing, service.Spec) {
			// Service data has not changed. Do nothing.
			return
		}

		// Service data has changed, keep the logic simple by removing the old service and re-adding the new one.
		suh.handleService(key, existing, suh.removeServiceMap, suh.removeNodePortMap)
	}

	suh.handleService(key, service.Spec, suh.addServiceMap, suh.addNodePortMap)
	suh.services[key] = service.Spec
}

func (suh *ServiceUpdateHandler) removeService(key model.ResourceKey) {
	suh.mutex.Lock()
	defer suh.mutex.Unlock()

	// Look up service by key and remove the entry.
	if existing, ok := suh.services[key]; ok {
		// Remove the service maps.
		suh.handleService(key, existing, suh.removeServiceMap, suh.removeNodePortMap)
		delete(suh.services, key)
	}
}

func (suh *ServiceUpdateHandler) addNodePortMap(key portProtoKey, svc proxy.ServicePortName) {
	suh.nodePortServices[key] = append(suh.nodePortServices[key], svc)
}

func (suh *ServiceUpdateHandler) removeNodePortMap(key portProtoKey, svc proxy.ServicePortName) {
	// Remove entry from the services slice and update the mapping. We can just iterate through the slice and
	// shift across once we find the entry.
	svcs := suh.nodePortServices[key]
	found := false
	for i := range svcs {
		if found {
			svcs[i-1] = svcs[i]
		} else {
			found = svcs[i] == svc
		}
	}
	if found {
		svcs = svcs[:len(svcs)-1]
		suh.nodePortServices[key] = svcs
	}

	if len(svcs) == 0 {
		// This is the only service for the node port, so just remove the node port to service mapping
		delete(suh.nodePortServices, key)
		return
	}
}

// uniqueService converts a slice of services into a single service by marking multiple valued fields with a "*".
func uniqueService(svcs []proxy.ServicePortName) proxy.ServicePortName {
	if len(svcs) == 0 {
		return proxy.ServicePortName{}
	} else if len(svcs) == 1 {
		return svcs[0]
	}

	svc := svcs[0]
	for i := 1; i < len(svcs); i++ {
		if svc.Name != svcs[i].Name {
			svc.Name = "*"
		}
		if svc.Port != svcs[i].Port {
			svc.Port = "*"
		}
		if svc.Namespace != svcs[i].Namespace {
			svc.Namespace = "*"
		}
	}
	return svc
}

// ServiceLookupsCache provides an API to lookup endpoint information given
// an IP address.
//
// To do this, the ServiceLookupsCache hooks into the calculation graph
// by handling callbacks for updated local endpoint tier information.
//
// It also functions as a node that is part of the calculation graph
// to handle remote endpoint information. To do this, it registers
// with the remote endpoint dispatcher and updates the endpoint
// cache appropriately.
type ServiceLookupsCache struct {
	suh *ServiceUpdateHandler

	mutex sync.RWMutex
}

func NewServiceLookupsCache() *ServiceLookupsCache {
	slc := &ServiceLookupsCache{
		suh:   NewServiceUpdateHandler(),
		mutex: sync.RWMutex{},
	}
	return slc
}

func (slc *ServiceLookupsCache) RegisterWith(allUpdateDisp *dispatcher.Dispatcher) {
	allUpdateDisp.Register(model.ResourceKey{}, slc.OnResourceUpdate)
}

// OnResourceUpdate is the callback method registered with the allUpdates dispatcher. We filter out everything except
// kubernetes services updates.
func (slc *ServiceLookupsCache) OnResourceUpdate(update api.Update) (_ bool) {
	log.Infof("OnResourceUpdate", update)
	switch k := update.Key.(type) {
	case model.ResourceKey:
		switch k.Kind {
		case v3.KindK8sService:
			log.Infof("processing update for service %s", k)
			if update.Value == nil {
				slc.suh.removeService(k)
			} else {
				slc.suh.addOrUpdateService(k, update.Value.(*kapiv1.Service))
			}
		default:
			log.Debugf("Ignoring update for resource: %s", k)
		}
	default:
		log.Errorf("Ignoring unexpected update: %v %#v",
			reflect.TypeOf(update.Key), update)
	}
	return
}

func (slc *ServiceLookupsCache) GetServiceSpecFromResourceKey(key model.ResourceKey) (kapiv1.ServiceSpec, bool) {
	spec, found := slc.suh.services[key]
	return spec, found
}

// GetNodePortService returns a matching node port service.
func (slc *ServiceLookupsCache) GetNodePortService(port int, proto int) (svc proxy.ServicePortName, found bool) {
	slc.mutex.Lock()
	defer slc.mutex.Unlock()

	// Check to see if the port/protocol corresponds to a node port service.
	if nps := slc.suh.nodePortServices[portProtoKey{port: port, proto: proto}]; len(nps) == 0 {
		log.Debugf("Port/Protocol (%d/%d) combination does not match a known node port", port, proto)
		return
	} else {
		log.Debugf("Port/Protocol (%d/%d) combination matches %d service(s)", port, proto, len(nps))
		return uniqueService(nps), true
	}
}

// GetServiceFromNATedDest returns the service associated with a pre-DNAT destination address.
func (slc *ServiceLookupsCache) GetServiceFromPreDNATDest(ipPreDNAT [16]byte, portPreDNAT int, proto int) (svc proxy.ServicePortName, found bool) {
	slc.mutex.Lock()
	defer slc.mutex.Unlock()

	svcs := slc.suh.ipPortProtoToServices[ipPortProtoKey{ip: ipPreDNAT, port: portPreDNAT, proto: proto}]
	if len(svcs) != 0 {
		log.Debug("Pre-DNAT matches cluster/externalIP service")
		return uniqueService(svcs), true
	}
	log.Debugf("IP/Port/Protocol (%v/%d/%d) combination does not match a known node port", ipPreDNAT, portPreDNAT, proto)

	// The pre-DNAT entry was not a cluster IP or external IP.  If the port/proto match a node port service then assume
	// this is a node port.
	if nps := slc.suh.nodePortServices[portProtoKey{port: portPreDNAT, proto: proto}]; len(nps) == 0 {
		log.Debugf("Port/Protocol (%d/%d) combination does not match a known node port", portPreDNAT, proto)
		return
	} else {
		log.Debugf("Port/Protocol (%d/%d) combination matches %d service(s)", portPreDNAT, proto, len(nps))
		return uniqueService(nps), true
	}
}

// IPStringToArray converts the cluster IP into a [16]bytes array.  Returns nil if not a valid IP.
func IPStringToArray(str string) (res [16]byte, parsed bool) {
	// Remove subnet len if included.
	parts := strings.Split(str, "/")
	str = parts[0]

	if str == "" || str == kapiv1.ClusterIPNone {
		return
	}
	if cidr, err := ip.ParseCIDROrIP(str); err == nil {
		var addrB [16]byte
		copy(addrB[:], cidr.ToIPNet().IP.To16()[:16])
		return addrB, true
	}
	return
}
