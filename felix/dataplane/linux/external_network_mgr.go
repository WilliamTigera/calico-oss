// Copyright (c) 2022 Tigera, Inc. All rights reserved.

package intdataplane

import (
	"github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/felix/ip"
	"github.com/projectcalico/calico/felix/logutils"
	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/felix/routerule"
	"github.com/projectcalico/calico/libcalico-go/lib/set"

	log "github.com/sirupsen/logrus"
)

// externalNetworkManager watches WorkloadEndpoint and ExternalNetwork updates.
// It sets up ip rules to direct egress traffic from a workloadendpoint via route tables defined
// by ExternalNetwork resources associated with the workloadendpoint.
type externalNetworkManager struct {
	dpConfig Config

	// The dataplane to program route rules.
	routeRules routeRules

	// rrGenerator dynamically creates routeRules instance to program route rules.
	rrGenerator routeRulesGenerator

	// externalNetworksInfoID maps workload endpoint ID to external networks information
	// associated with the workload.
	// This is refered as "cache" in this file.
	externalNetworksInfoByID map[proto.WorkloadEndpointID]*externalNetworksInfo

	// Active external networks information.
	activeNetworks map[string]*externalNetwork

	// activeRules holds rules which should be programmed.
	// This is derived from the cache.
	activeRules set.Set[*routerule.Rule]

	// Pending workload endpoints updates, we store these up as OnUpdate is called, then process them
	// in CompleteDeferredWork.
	pendingWorkloadUpdates map[proto.WorkloadEndpointID]*proto.WorkloadEndpoint

	// Pending ExternalNetwork updates, we store these up as OnUpdate is called, then process them
	// in CompleteDeferredWork.
	pendingExternalNetworkUpdates map[proto.ExternalNetworkID]*proto.ExternalNetwork

	opRecorder logutils.OpRecorder
}

type externalNetwork struct {
	RouteTableIndex int
}

type externalNetworksInfo struct {
	// V4 IPs of the workload endpoint.
	IPv4 []string

	// External network names linked with the workload endpoint.
	Networks set.Set[string]
}

func newExternalNetworkManager(
	dpConfig Config,
	opRecorder logutils.OpRecorder,
) *externalNetworkManager {
	logrus.Info("Creating ExternalNetwork manager.")

	mgr := newExternalNetworkManagerWithShims(
		dpConfig,
		&routeRulesFactory{count: 0},
		opRecorder,
	)
	return mgr
}

func newExternalNetworkManagerWithShims(
	dpConfig Config,
	rrGenerator routeRulesGenerator,
	opRecorder logutils.OpRecorder,
) *externalNetworkManager {
	mgr := externalNetworkManager{
		dpConfig:                      dpConfig,
		rrGenerator:                   rrGenerator,
		externalNetworksInfoByID:      map[proto.WorkloadEndpointID]*externalNetworksInfo{},
		activeNetworks:                map[string]*externalNetwork{},
		pendingWorkloadUpdates:        make(map[proto.WorkloadEndpointID]*proto.WorkloadEndpoint),
		pendingExternalNetworkUpdates: make(map[proto.ExternalNetworkID]*proto.ExternalNetwork),
		opRecorder:                    opRecorder,
	}

	return &mgr
}

func (m *externalNetworkManager) OnUpdate(msg interface{}) {
	switch msg := msg.(type) {
	case *proto.WorkloadEndpointUpdate:
		log.WithField("msg", msg).Debug("workload endpoint update")
		m.pendingWorkloadUpdates[*msg.Id] = msg.Endpoint
	case *proto.WorkloadEndpointRemove:
		log.WithField("msg", msg).Debug("workload endpoint remove")
		m.pendingWorkloadUpdates[*msg.Id] = nil
	case *proto.ExternalNetworkUpdate:
		log.WithField("msg", msg).Debug("external network update")
		m.pendingExternalNetworkUpdates[*msg.Id] = msg.Network
	case *proto.ExternalNetworkRemove:
		log.WithField("msg", msg).Debug("external network remove")
		m.pendingExternalNetworkUpdates[*msg.Id] = nil
	}
}

func (m *externalNetworkManager) CompleteDeferredWork() error {
	if len(m.pendingWorkloadUpdates) == 0 && len(m.pendingExternalNetworkUpdates) == 0 {
		log.Debug("No change since last application, nothing to do")
		return nil
	}

	// Handle network updates.
	for id, network := range m.pendingExternalNetworkUpdates {
		networkName := id.Name
		if network == nil {
			delete(m.activeNetworks, networkName)
		} else {
			// Update activeNetworks
			if _, ok := m.activeNetworks[networkName]; ok {
				m.activeNetworks[networkName].RouteTableIndex = int(network.RouteTableIndex)
			} else {
				m.activeNetworks[networkName] = &externalNetwork{
					RouteTableIndex: int(network.RouteTableIndex),
				}
			}
		}

		delete(m.pendingExternalNetworkUpdates, id)
	}

	// Handle workload updates.
	for id, workload := range m.pendingWorkloadUpdates {
		if workload == nil {
			// Workload is removed. Delete its cache if exists.
			delete(m.externalNetworksInfoByID, id)
			continue
		}

		if len(workload.ExternalNetworkNames) == 0 {
			// Workload is not linked to any external networks. Delete its cache if exists.
			delete(m.externalNetworksInfoByID, id)
			continue
		}

		// Makes sure there's entry in the cache for this workload.
		if _, ok := m.externalNetworksInfoByID[id]; !ok {
			// Create an entry with empty fields for the workload id.
			m.externalNetworksInfoByID[id] = &externalNetworksInfo{}
		}

		// Populate info.
		info := m.externalNetworksInfoByID[id]
		// Update V4 addresses for the workload.
		info.IPv4 = workload.Ipv4Nets
		// Initialise a new set with latest update.
		info.Networks = set.FromArray(workload.ExternalNetworkNames)

		delete(m.pendingWorkloadUpdates, id)
	}

	// Our cache is now up to date.

	if m.routeRules == nil {
		// Create routeRules to manage routing rules.
		// We create routerule inside CompleteDeferredWork to make sure datastore is in sync and all WEP/ExternalNetwork updates
		// will be processed before routerule's apply() been called.
		// Filter to accept any rule.
		var CreatedByExternalNetworkManager routerule.RuleFilterFunc = func(r *routerule.Rule) bool {
			// Return true if the rule is created by this manager.
			nlRule := r.NetLinkRule()
			if (nlRule.Mark == int(m.dpConfig.RulesConfig.IptablesMarkEgress)) &&
				(nlRule.Mask == int(m.dpConfig.RulesConfig.IptablesMarkEgress)) &&
				(nlRule.Priority == m.dpConfig.ExternalNetworkRoutingRulePriority) {
				return true
			}
			return false
		}

		m.routeRules = m.rrGenerator.NewRouteRules(
			4,
			set.New[int](),
			routerule.RulesMatchSrcFWMarkTable,
			routerule.RulesMatchSrcFWMarkTable,
			CreatedByExternalNetworkManager,
			m.dpConfig.NetlinkTimeout,
			m.opRecorder,
		)
	}

	// Program route rules after latest updates.
	// If there's any changes needed, routeRule dataplane will be
	// set to out of sync and trigger routerule.Apply().
	// Regardless of the routeRule dataplane is in sync or not,
	// it will try to program the latest rule set.
	m.ProgramRouteRules()

	return nil
}

// Return an active Rule if it matches a given Rule based on filter function RulesMatchSrcFWMarkTable.
// Return nil if no active Rule exists.
func (m *externalNetworkManager) getActiveRule(rule *routerule.Rule) *routerule.Rule {
	var active *routerule.Rule
	m.activeRules.Iter(func(p *routerule.Rule) error {
		if routerule.RulesMatchSrcFWMarkTable(p, rule) {
			active = p
			return set.StopIteration
		}
		return nil
	})

	return active
}

// Populate activeRules for the manager.
func (m *externalNetworkManager) PopulateActiveRules() {
	m.activeRules = set.New[*routerule.Rule]()
	// Walk through our cache, set up each rule.
	for _, info := range m.externalNetworksInfoByID {
		for _, srcIP := range info.IPv4 {
			info.Networks.Iter(func(name string) error {
				if network, ok := m.activeNetworks[name]; ok {
					rule := newRouteRule(m.dpConfig.ExternalNetworkRoutingRulePriority,
						m.dpConfig.RulesConfig.IptablesMarkEgress,
						ip.FromIPOrCIDRString(srcIP), int(network.RouteTableIndex))

					m.activeRules.Add(rule)
				}
				return nil
			})
		}
	}
}

// SetRouteRules sets up route rules for the dataplane to program.
func (m *externalNetworkManager) ProgramRouteRules() {
	// Prepare current snapshot of rules need to be programmed.
	m.PopulateActiveRules()

	// Prepare current snapshot of rules from routerule dataplane.
	rulesFromDataplane := m.routeRules.GetAllActiveRules()

	// Work out two sets, rules to add and rules to remove.
	toAdd := m.activeRules.Copy()
	toRemove := set.New[*routerule.Rule]()

	for _, dataplaneRule := range rulesFromDataplane {
		if activeRule := m.getActiveRule(dataplaneRule); activeRule != nil {
			// rule exists both in activeRules and dataplaneRules.
			toAdd.Discard(activeRule)
		} else {
			toRemove.Add(dataplaneRule)
		}
	}

	toRemove.Iter(func(rule *routerule.Rule) error {
		m.routeRules.RemoveRule(rule)
		rule.LogCxt().Debugf("Rule removed from routerule dataplane.")
		return nil
	})

	toAdd.Iter(func(rule *routerule.Rule) error {
		m.routeRules.SetRule(rule)
		rule.LogCxt().Debugf("Rule added to routerule dataplane.")
		return nil
	})
}
