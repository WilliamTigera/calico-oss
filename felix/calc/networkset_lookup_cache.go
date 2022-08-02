// Copyright (c) 2018-2021 Tigera, Inc. All rights reserved.

package calc

import (
	"net"
	"reflect"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/felix/dispatcher"
	"github.com/projectcalico/calico/felix/ip"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/calico/libcalico-go/lib/set"
)

var (
	gaugeNetworkSetCacheLength = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "felix_collector_lookupcache_networksets",
		Help: "Total number of entries currently residing in the network set lookup cache.",
	})
)

func init() {
	prometheus.MustRegister(gaugeNetworkSetCacheLength)
}

type networkSetData struct {
	cidrs                set.Set[ip.CIDR]
	allowedEgressDomains set.Set[string]
	endpointData         *EndpointData
}

// Networkset data is stored in the EndpointData object for easier type processing for flow logs.
type NetworkSetLookupsCache struct {
	nsMutex                  sync.RWMutex
	networkSets              map[model.Key]*networkSetData
	ipTree                   *IpTrie
	networksetToEgressDomain map[string]set.Set[string]
	egressDomainToNetworkset map[string]set.Set[model.Key]
}

func NewNetworkSetLookupsCache() *NetworkSetLookupsCache {
	nc := &NetworkSetLookupsCache{
		nsMutex: sync.RWMutex{},

		// NetworkSet data.
		networkSets: make(map[model.Key]*networkSetData),

		// Reverse lookups by CIDR and egress domain.
		ipTree:                   NewIpTrie(),
		egressDomainToNetworkset: make(map[string]set.Set[model.Key]),
	}

	return nc
}

func (nc *NetworkSetLookupsCache) RegisterWith(allUpdateDispatcher *dispatcher.Dispatcher) {
	allUpdateDispatcher.Register(model.NetworkSetKey{}, nc.OnUpdate)
}

// OnUpdate is the callback method registered with the AllUpdatesDispatcher for
// the model.NetworkSet type. This method updates the mapping between networkSets
// and the corresponding CIDRs that they contain.
func (nc *NetworkSetLookupsCache) OnUpdate(nsUpdate api.Update) (_ bool) {
	switch k := nsUpdate.Key.(type) {
	case model.NetworkSetKey:
		if nsUpdate.Value == nil {
			nc.removeNetworkSet(k)
		} else {
			networkset := nsUpdate.Value.(*model.NetworkSet)
			nc.addOrUpdateNetworkset(&networkSetData{
				endpointData: &EndpointData{
					Key:        k,
					Networkset: nsUpdate.Value,
				},
				cidrs:                set.FromArrayBoxed(ip.CIDRsFromCalicoNets(networkset.Nets)),
				allowedEgressDomains: set.FromArray(networkset.AllowedEgressDomains),
			})
		}
	default:
		log.Infof("ignoring unexpected update: %v %#v",
			reflect.TypeOf(nsUpdate.Key), nsUpdate)
		return
	}
	log.Infof("Updating networkset cache with networkset data %v", nsUpdate.Key)
	return
}

// addOrUpdateNetworkset tracks networkset to CIDR mapping as well as the reverse
// mapping from CIDR to networkset.
func (nc *NetworkSetLookupsCache) addOrUpdateNetworkset(data *networkSetData) {
	// If the networkset exists, it was updated, then we might have to add or
	// remove CIDRs and allowed egress domains.
	nc.nsMutex.Lock()
	defer nc.nsMutex.Unlock()

	currentData, exists := nc.networkSets[data.endpointData.Key]
	if currentData == nil {
		currentData = &networkSetData{
			cidrs:                set.NewBoxed[ip.CIDR](),
			allowedEgressDomains: set.New[string](),
		}
	}
	nc.networkSets[data.endpointData.Key] = data

	set.IterDifferencesBoxed[ip.CIDR](data.cidrs, currentData.cidrs,
		// In new, not current.  Add new entry to mappings.
		func(newCIDR ip.CIDR) error {
			nc.ipTree.InsertKey(newCIDR, data.endpointData.Key)
			return nil
		},
		// In current, not new.  Remove old entry from mappings.
		func(oldCIDR ip.CIDR) error {
			nc.ipTree.DeleteKey(oldCIDR, data.endpointData.Key)
			return nil
		},
	)
	set.IterDifferences[string](data.allowedEgressDomains, currentData.allowedEgressDomains,
		// In new, not current.  Add new entry to mappings.
		func(newDomain string) error {
			nc.addDomainMapping(newDomain, data.endpointData.Key)
			return nil
		},
		// In current, not new.  Remove old entry from mappings.
		func(oldDomain string) error {
			nc.removeDomainMapping(oldDomain, data.endpointData.Key)
			return nil
		},
	)
	if !exists {
		nc.reportNetworksetCacheMetrics()
	}
}

// removeNetworkSet removes the networkset from the NetworksetLookupscache.networkSets map
// and also removes all corresponding CIDR to networkset mappings as well.
// This method should acquire (and release) the NetworkSetLookupsCache.nsMutex before (and after)
// manipulating the maps.
func (nc *NetworkSetLookupsCache) removeNetworkSet(key model.Key) {
	nc.nsMutex.Lock()
	defer nc.nsMutex.Unlock()
	currentData, ok := nc.networkSets[key]
	if !ok {
		// We don't know about this networkset. Nothing to do.
		return
	}
	currentData.cidrs.Iter(func(oldCIDR ip.CIDR) error {
		nc.ipTree.DeleteKey(oldCIDR, key)
		return nil
	})
	currentData.allowedEgressDomains.Iter(func(oldDomain string) error {
		nc.removeDomainMapping(oldDomain, key)
		return nil
	})
	delete(nc.networkSets, key)
	nc.reportNetworksetCacheMetrics()
}

func (nc *NetworkSetLookupsCache) addDomainMapping(domain string, key model.Key) {
	// Add the networkset key to the set specific to this domain, creating a new set if this is the first.
	current := nc.egressDomainToNetworkset[domain]
	if current == nil {
		current = set.NewBoxed[model.Key]()
		nc.egressDomainToNetworkset[domain] = current
	}
	current.Add(key)
}

func (nc *NetworkSetLookupsCache) removeDomainMapping(domain string, key model.Key) {
	current := nc.egressDomainToNetworkset[domain]
	if current == nil {
		return
	}
	// Remove the networkset key from the set specific to this domain, and remove the domain if no longer in any
	// networkSets.
	current.Discard(key)
	if current.Len() == 0 {
		delete(nc.egressDomainToNetworkset, domain)
	}
}

// GetNetworkSetFromIP finds Longest Prefix Match CIDR from given IP ADDR and return last observed
// Networkset for that CIDR
func (nc *NetworkSetLookupsCache) GetNetworkSetFromIP(addr [16]byte) (ed *EndpointData, ok bool) {
	nc.nsMutex.RLock()
	defer nc.nsMutex.RUnlock()
	// Find the first cidr that contains the ip address to use for the lookup.
	ipAddr := ip.FromNetIP(net.IP(addr[:]))
	if key, _ := nc.ipTree.GetLongestPrefixCidr(ipAddr); key != nil {
		if ns := nc.networkSets[key]; ns != nil {
			// Found a NetworkSet, so set the return variables.
			ed = ns.endpointData
			ok = true
		}
	}
	return
}

// GetNetworkSetFromEgressDomain returns an arbitrary NetworkSet that contains the suppled egress domain. This does not do
// any pattern matching as it is assumed the domain will be in the format configured in either a networkset or a policy.
func (nc *NetworkSetLookupsCache) GetNetworkSetFromEgressDomain(domain string) (ed *EndpointData, ok bool) {
	nc.nsMutex.RLock()
	defer nc.nsMutex.RUnlock()

	keys := nc.egressDomainToNetworkset[domain]
	if keys == nil {
		return
	}
	keys.Iter(func(key model.Key) error {
		// If we locate the networkset (we should unless our data is corrupt) then update the return values and stop
		// further iteration
		if ns := nc.networkSets[key]; ns != nil {
			ed, ok = ns.endpointData, true
			return set.StopIteration
		}
		return nil
	})

	return
}

func (nc *NetworkSetLookupsCache) DumpNetworksets() string {
	nc.nsMutex.RLock()
	defer nc.nsMutex.RUnlock()
	lines := []string{}
	lines = nc.ipTree.DumpCIDRKeys()
	lines = append(lines, "-------")
	for key, ns := range nc.networkSets {
		cidrStr := []string{}
		ns.cidrs.Iter(func(cidr ip.CIDR) error {
			cidrStr = append(cidrStr, cidr.String())
			return nil
		})
		domainStr := []string{}
		ns.allowedEgressDomains.Iter(func(domain string) error {
			domainStr = append(domainStr, domain)
			return nil
		})
		lines = append(lines,
			key.(model.NetworkSetKey).Name,
			"   cidrs: "+strings.Join(cidrStr, ","),
			" domains: "+strings.Join(domainStr, ","),
		)
	}
	return strings.Join(lines, "\n")
}

// reportNetworksetCacheMetrics reports networkset cache performance metrics to prometheus
func (nc *NetworkSetLookupsCache) reportNetworksetCacheMetrics() {
	gaugeNetworkSetCacheLength.Set(float64(len(nc.networkSets)))
}
