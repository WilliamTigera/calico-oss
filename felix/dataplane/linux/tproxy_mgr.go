//go:build !windows
// +build !windows

// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package intdataplane

import (
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/felix/environment"

	"github.com/projectcalico/calico/felix/config"
	"github.com/projectcalico/calico/felix/ifacemonitor"
	"github.com/projectcalico/calico/felix/ip"
	"github.com/projectcalico/calico/felix/ipsets"
	"github.com/projectcalico/calico/felix/logutils"
	"github.com/projectcalico/calico/felix/netlinkshim"
	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/felix/routerule"
	"github.com/projectcalico/calico/felix/routetable"
	"github.com/projectcalico/calico/felix/tproxydefs"
	"github.com/projectcalico/calico/libcalico-go/lib/set"
)

type tproxyManager struct {
	enabled, enabled6 bool
	mark              uint32
	k8sProvider       config.Provider
	rt4, rt6          *routetable.RouteTable
	rr4, rr6          *routerule.RouteRules

	iptablesEqualIPsChecker *iptablesEqualIPsChecker
}

type tproxyIPSets interface {
	AddOrReplaceIPSet(meta ipsets.IPSetMetadata, members []string)
	AddMembers(setID string, newMembers []string)
	RemoveMembers(setID string, removedMembers []string)
}

type TProxyOption func(*tproxyManager)

func newTProxyManager(
	dpConfig Config,
	idx4, idx6 int,
	opRecorder logutils.OpRecorder,
	featureDetector environment.FeatureDetectorIface,
	opts ...TProxyOption,
) *tproxyManager {

	ipv6Enabled := dpConfig.IPv6Enabled
	mark := dpConfig.RulesConfig.IptablesMarkProxy

	enabled := dpConfig.RulesConfig.TPROXYModeEnabled()

	tpm := &tproxyManager{
		enabled:     enabled,
		enabled6:    ipv6Enabled,
		mark:        mark,
		k8sProvider: dpConfig.KubernetesProvider,
	}

	for _, opt := range opts {
		opt(tpm)
	}

	if idx4 == 0 {
		log.Fatal("RouteTable index for IPv4 is the default table")
	}

	rt := routetable.New(
		nil,
		4,
		false, // vxlan
		dpConfig.NetlinkTimeout,
		nil, // deviceRouteSourceAddress
		dpConfig.DeviceRouteProtocol,
		true, // removeExternalRoutes
		idx4,
		opRecorder,
		featureDetector,
	)

	rr, err := routerule.New(
		4,
		set.From(idx4),
		routerule.RulesMatchSrcFWMarkTable,
		routerule.RulesMatchSrcFWMarkTable,
		nil,
		dpConfig.NetlinkTimeout,
		func() (routerule.HandleIface, error) {
			return netlinkshim.NewRealNetlink()
		},
		opRecorder,
	)
	if err != nil {
		log.WithError(err).Panic("Unexpected error creating rule manager")
	}

	if enabled {
		anyV4, _ := ip.CIDRFromString("0.0.0.0/0")
		rt.RouteUpdate("lo", routetable.Target{
			Type: routetable.TargetTypeLocal,
			CIDR: anyV4,
		})

		rr.SetRule(routerule.NewRule(4, 1).
			GoToTable(idx4).
			MatchFWMarkWithMask(uint32(mark), uint32(mark)),
		)
	}

	tpm.rr4 = rr
	tpm.rt4 = rt

	if ipv6Enabled {
		if idx6 == 0 {
			log.Fatal("RouteTable index for IPv6 is the default table")
		}

		rt := routetable.New(
			nil,
			6,
			false, // vxlan
			dpConfig.NetlinkTimeout,
			nil, // deviceRouteSourceAddress
			dpConfig.DeviceRouteProtocol,
			true, // removeExternalRoutes
			idx6,
			opRecorder,
			featureDetector,
		)

		rr, err := routerule.New(
			6,
			set.From(idx6),
			routerule.RulesMatchSrcFWMarkTable,
			routerule.RulesMatchSrcFWMarkTable,
			nil,
			dpConfig.NetlinkTimeout,
			func() (routerule.HandleIface, error) {
				return netlinkshim.NewRealNetlink()
			},
			opRecorder,
		)
		if err != nil {
			log.WithError(err).Panic("Unexpected error creating rule manager")
		}

		if enabled {
			anyV6, _ := ip.CIDRFromString("::/0")
			rt.RouteUpdate("lo", routetable.Target{
				Type: routetable.TargetTypeLocal,
				CIDR: anyV6,
			})

			rr.SetRule(routerule.NewRule(6, 1).
				GoToTable(idx6).
				MatchFWMarkWithMask(uint32(mark), uint32(mark)),
			)
		}

		tpm.rr6 = rr
		tpm.rt6 = rt
	}

	return tpm
}

func tproxyWithIptablesEqualIPsChecker(checker *iptablesEqualIPsChecker) TProxyOption {
	return func(m *tproxyManager) {
		m.iptablesEqualIPsChecker = checker
	}
}

func (m *tproxyManager) OnUpdate(protoBufMsg interface{}) {
	if !m.enabled {
		return
	}

	switch msg := protoBufMsg.(type) {
	case *proto.WorkloadEndpointUpdate:
		if m.iptablesEqualIPsChecker != nil {
			// We get EP updates only for the endpoints local to the node.
			m.iptablesEqualIPsChecker.OnWorkloadEndpointUpdate(msg)
		}
	case *proto.WorkloadEndpointRemove:
		if m.iptablesEqualIPsChecker != nil {
			// We get EP updates only for the endpoints local to the node.
			m.iptablesEqualIPsChecker.OnWorkloadEndpointRemove(msg)
		}
	case *ifaceUpdate:
		if m.k8sProvider == config.ProviderGKE && msg.State == ifacemonitor.StateUp {
			// We need to set loose RPF check in GKE because of the complicated routing as
			// we run in the intra-node visibility mode. This mode make all packets from
			// pods to leave the node (usually via eth0) and thus when we deliver to the
			// proxy, RPF expect the packets to be from eth0 rather than from a pod's
			// (gke*) interface.  There is no easy way around to tell the RPF check that
			// gke* is correct.
			podIface := strings.HasPrefix(msg.Name, "gke")
			if podIface {
				err := writeProcSys(fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", msg.Name), "2")
				if err != nil {
					log.WithError(err).Warnf("Failed to set loose RPF check on %s", msg.Name)
				} else {
					log.Debugf("RPF check made loose on %s", msg.Name)
				}
			}

			// Why we need accept_local is a bit of a mistery, empirically it needs to be
			// set both on pod ifaces as well as on the main iface to work. It is likely a
			// combination of delivering via a local route and igress and egress iface not
			// matching. There are at least 2 checks in the kernel, neither specifically
			// tests for a local IP. See fib_validate_source() and __fib_validate_source().
			if podIface || strings.HasPrefix(msg.Name, "eth") {
				err := writeProcSys(fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/accept_local", msg.Name), "1")
				if err != nil {
					log.WithError(err).Warnf("Failed to set accept local %s", msg.Name)
				} else {
					log.Debugf("Accept local enabled on %s", msg.Name)
				}
			}
		}
	}
}

func (m *tproxyManager) CompleteDeferredWork() error {
	if m.enabled && m.iptablesEqualIPsChecker != nil {
		return m.iptablesEqualIPsChecker.CompleteDeferredWork()
	}

	return nil
}

func (m *tproxyManager) GetRouteTableSyncers() []routetable.RouteTableSyncer {
	var rts []routetable.RouteTableSyncer

	if m.rt4 != nil {
		rts = append(rts, m.rt4)
	}

	if m.rt6 != nil {
		rts = append(rts, m.rt6)
	}

	return rts
}

func (m *tproxyManager) GetRouteRules() []routeRules {
	var rrs []routeRules

	if m.rr4 != nil {
		rrs = append(rrs, m.rr4)
	}

	if m.rr6 != nil {
		rrs = append(rrs, m.rr6)
	}

	return rrs
}

type iptablesEqualIPsChecker struct {
	enabled6 bool

	ipSetsV4, ipSetsV6 tproxyIPSets

	v4Eps map[proto.WorkloadEndpointID][]string
	v6Eps map[proto.WorkloadEndpointID][]string

	ipv4 map[string]int
	ipv6 map[string]int

	ipv4ToAdd map[string]struct{}
	ipv4ToDel map[string]struct{}
	ipv6ToAdd map[string]struct{}
	ipv6ToDel map[string]struct{}
}

func newIptablesEqualIPsChecker(dpConfig Config, ipSetsV4, ipSetsV6 tproxyIPSets) *iptablesEqualIPsChecker {
	enabled6 := dpConfig.IPv6Enabled
	enabled := dpConfig.RulesConfig.TPROXYModeEnabled()

	if enabled && ipSetsV4 == nil {
		log.Fatal("no IPv4 ipsets")
	}
	if enabled && enabled6 && ipSetsV6 == nil {
		log.Fatal("no IPv6 ipsets when IPv6 enabled")
	}

	if enabled {
		ipSetsV4.AddOrReplaceIPSet(
			ipsets.IPSetMetadata{
				SetID:   tproxydefs.PodSelf,
				Type:    ipsets.IPSetTypeHashNetNet,
				MaxSize: dpConfig.MaxIPSetSize,
			},
			[]string{},
		)

		if enabled6 {
			ipSetsV6.AddOrReplaceIPSet(
				ipsets.IPSetMetadata{
					SetID:   tproxydefs.PodSelf,
					Type:    ipsets.IPSetTypeHashNetNet,
					MaxSize: dpConfig.MaxIPSetSize,
				},
				[]string{},
			)
		}
	}

	return &iptablesEqualIPsChecker{
		enabled6: enabled6,

		ipSetsV4: ipSetsV4,
		ipSetsV6: ipSetsV6,

		v4Eps: make(map[proto.WorkloadEndpointID][]string),
		v6Eps: make(map[proto.WorkloadEndpointID][]string),

		ipv4: make(map[string]int),
		ipv6: make(map[string]int),

		ipv4ToAdd: make(map[string]struct{}),
		ipv4ToDel: make(map[string]struct{}),
		ipv6ToAdd: make(map[string]struct{}),
		ipv6ToDel: make(map[string]struct{}),
	}
}

func includesNet(n string, set []string) bool {
	for _, s := range set {
		if s == n {
			return true
		}
	}
	return false
}

func diffNets(now, before []string) (add, del []string) {
	for _, n := range now {
		if !includesNet(n, before) {
			add = append(add, n)
		}
	}

	if len(now) == len(add) {
		return
	}

	for _, b := range before {
		if !includesNet(b, now) {
			del = append(del, b)
		}
	}

	return
}

func onWorkloadEndpointUpdate(
	id *proto.WorkloadEndpointID,
	nets []string,
	eps map[proto.WorkloadEndpointID][]string,
	refs map[string]int,
	toAdd, toDel map[string]struct{},
) {
	add, del := diffNets(nets, eps[*id])

	eps[*id] = nets

	for _, ip := range add {
		refs[ip]++
		if refs[ip] == 1 {
			toAdd[ip] = struct{}{}
		}
		delete(toDel, ip)
	}
	for _, ip := range del {
		refs[ip]--
		if refs[ip] <= 0 {
			delete(refs, ip)
			toDel[ip] = struct{}{}
			delete(toAdd, ip)
		}
	}
}

func (c *iptablesEqualIPsChecker) OnWorkloadEndpointUpdate(msg *proto.WorkloadEndpointUpdate) {
	onWorkloadEndpointUpdate(msg.Id, msg.Endpoint.Ipv4Nets, c.v4Eps, c.ipv4, c.ipv4ToAdd, c.ipv4ToDel)

	if c.enabled6 {
		onWorkloadEndpointUpdate(msg.Id, msg.Endpoint.Ipv6Nets, c.v6Eps, c.ipv6, c.ipv6ToAdd, c.ipv6ToDel)
	}
}

func onWorkloadEndpointRemove(
	id *proto.WorkloadEndpointID,
	eps map[proto.WorkloadEndpointID][]string,
	refs map[string]int,
	toAdd, toDel map[string]struct{},
) {
	for _, ip := range eps[*id] {
		refs[ip]--
		if refs[ip] <= 0 {
			delete(refs, ip)
			toDel[ip] = struct{}{}
			delete(toAdd, ip)
		}
	}
	delete(eps, *id)
}

func (c *iptablesEqualIPsChecker) OnWorkloadEndpointRemove(msg *proto.WorkloadEndpointRemove) {
	onWorkloadEndpointRemove(msg.Id, c.v4Eps, c.ipv4, c.ipv4ToAdd, c.ipv4ToDel)

	if c.enabled6 {
		onWorkloadEndpointRemove(msg.Id, c.v6Eps, c.ipv6, c.ipv6ToAdd, c.ipv6ToDel)
	}
}

func (c *iptablesEqualIPsChecker) CompleteDeferredWork() error {
	var add []string
	for ipv4 := range c.ipv4ToAdd {
		add = append(add, ipv4+","+ipv4)
	}
	c.ipSetsV4.AddMembers(tproxydefs.PodSelf, add)

	if c.enabled6 {
		var add6 []string
		for ipv6 := range c.ipv6ToAdd {
			add6 = append(add6, ipv6+","+ipv6)
		}
		c.ipSetsV6.AddMembers(tproxydefs.PodSelf, add6)
	}

	var del []string
	for ipv4 := range c.ipv4ToDel {
		del = append(del, ipv4+","+ipv4)
	}
	c.ipSetsV4.RemoveMembers(tproxydefs.PodSelf, del)

	if c.enabled6 {
		var del6 []string
		for ipv6 := range c.ipv6ToDel {
			del6 = append(del6, ipv6+","+ipv6)
		}
		c.ipSetsV6.RemoveMembers(tproxydefs.PodSelf, del6)
	}

	c.ipv4ToAdd = make(map[string]struct{})
	c.ipv4ToDel = make(map[string]struct{})
	c.ipv6ToAdd = make(map[string]struct{})
	c.ipv6ToDel = make(map[string]struct{})

	return nil
}
