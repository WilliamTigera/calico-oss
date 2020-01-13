package policycalc

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/net"
	"github.com/projectcalico/libcalico-go/lib/numorstring"
	"github.com/projectcalico/libcalico-go/lib/resources"

	pipcfg "github.com/tigera/es-proxy/pkg/pip/config"
)

// This file contains most of the policy calculation tests, by explicitly testing each match criteria.
// It's a bit tedious.

var (
	typesIngress = []v3.PolicyType{v3.PolicyTypeIngress}
	typesEgress  = []v3.PolicyType{v3.PolicyTypeEgress}

	int_1       = int(1)
	int_4       = int(4)
	int_6       = int(6)
	uint16_1000 = uint16(1000)
	uint8_17    = uint8(17)
)

var _ = Describe("Compiled tiers and policies tests", func() {
	var f *Flow
	var np *v3.NetworkPolicy
	var tiers Tiers
	var rd *ResourceData
	var impacted ImpactedResources
	var sel *EndpointSelectorHandler
	var compute func() EndpointResponse

	setup := func(cfg *pipcfg.Config) {
		np = &v3.NetworkPolicy{
			TypeMeta: resources.TypeCalicoNetworkPolicies,
			ObjectMeta: v1.ObjectMeta{
				Name:      "meh.policy",
				Namespace: "ns1",
			},
			Spec: v3.NetworkPolicySpec{
				Tier:     "meh",
				Selector: "all()",
				Types:    typesEgress,
				Ingress: []v3.Rule{{
					Action: v3.Deny,
				}},
				Egress: []v3.Rule{{
					Action: v3.Deny,
				}},
			},
		}
		tiers = Tiers{{{Policy: np}}}
		impacted = make(ImpactedResources)
		sel = NewEndpointSelectorHandler()
		rd = &ResourceData{
			Tiers: tiers,
			Namespaces: []*corev1.Namespace{{
				ObjectMeta: v1.ObjectMeta{
					Name: "ns1",
					Labels: map[string]string{
						"nsl1": "nsv1",
					},
				},
			}},
			ServiceAccounts: []*corev1.ServiceAccount{{
				ObjectMeta: v1.ObjectMeta{
					Name:      "sa1",
					Namespace: "ns1",
					Labels: map[string]string{
						"sal1": "sav1",
					},
				},
			}},
		}
		f = &Flow{
			ActionFlag: ActionFlagAllow,
			Source: FlowEndpointData{
				Type:   EndpointTypeNet,
				Labels: map[string]string{},
			},
			Destination: FlowEndpointData{
				Type:   EndpointTypeNet,
				Labels: map[string]string{},
			},
		}

		compute = func() EndpointResponse {
			ingress, egress := calculateCompiledTiersAndImpactedPolicies(cfg, rd, impacted, sel, false)

			// Tweak our flow reporter to match the policy type.
			flowCache := &flowCache{
				source:      endpointCache{selectors: sel.CreateSelectorCache()},
				destination: endpointCache{selectors: sel.CreateSelectorCache()},
				policies:    make(map[string]ActionFlag),
			}

			// Invoke the calculation twice - once to run through the before processing which will populate our cache
			// and once to run through the after processing.  We return the latter result.
			if np.Spec.Types[0] == v3.PolicyTypeIngress {
				f.Reporter = ReporterTypeDestination
				before := ingress.Calculate(f, flowCache, true)
				after := ingress.Calculate(f, flowCache, false)
				Expect(before).To(Equal(after))
				return after
			}
			f.Reporter = ReporterTypeSource
			before := egress.Calculate(f, flowCache, true)
			after := egress.Calculate(f, flowCache, false)
			Expect(before).To(Equal(after))
			return after
		}
	}

	BeforeEach(func() {
		setup(&pipcfg.Config{})
	})

	// ---- No tier match ----

	It("checking policy in different namespace - end of tiers allow through profile", func() {
		f.Proto = &ProtoICMP
		f.Source.Namespace = "ns2"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		r := compute()
		Expect(r.Action).To(Equal(ActionFlagAllow))
		Expect(r.Include).To(BeTrue())
		Expect(r.Policies.FlowLogPolicyStrings()).To(Equal([]string{"0|__PROFILE__|__PROFILE__.kns.ns2|allow"}))
	})

	It("HEP does not match namespaced policy - end of tiers deny (implicit deny through felix)", func() {
		f.Proto = &ProtoICMP
		f.Source.Type = EndpointTypeHep
		np.Spec.Ingress = nil
		r := compute()
		Expect(r.Action).To(Equal(ActionFlagDeny))
		Expect(r.Include).To(BeTrue())
		Expect(r.Policies.FlowLogPolicyStrings()).To(Equal([]string{"0|__PROFILE__|__PROFILE__.__NO_MATCH__|deny"}))
	})

	// ---- ICMP/NotICMP matcher ----

	It("checking source egress deny exact match when ICMP is non-nil and protocol is ICMP", func() {
		f.Proto = &ProtoICMP
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].ICMP = &v3.ICMPFields{}
		r := compute()
		Expect(r.Action).To(Equal(ActionFlagDeny))
		Expect(r.Include).To(BeTrue())
		Expect(r.Policies.FlowLogPolicyStrings()).To(Equal([]string{"0|meh|ns1/meh.policy|deny"}))
	})

	It("checking dest ingress deny exact match deny when ICMP is non-nil and protocol is ICMP", func() {
		f.Proto = &ProtoICMP
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].ICMP = &v3.ICMPFields{}
		Expect(compute().Action).To(Equal(ActionFlagDeny))
	})

	It("checking source egress deny inexact match when ICMP.Code is non-nil and protocol is ICMP", func() {
		f.Proto = &ProtoICMP
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].ICMP = &v3.ICMPFields{Code: &int_1}
		// Inexact deny and exact end of tier deny means overall a deny. The policies will contain a repeated entry
		// for the policy (once for the inexact deny rule match, once for the end of tier deny).
		r := compute()
		Expect(r.Action).To(Equal(ActionFlagDeny | ActionFlagEndOfTierDeny))
		Expect(r.Include).To(BeTrue())
		Expect(r.Policies.FlowLogPolicyStrings()).To(Equal([]string{
			"0|meh|ns1/meh.policy|deny", "0|meh|ns1/meh.policy|eot-deny",
		}))
	})

	It("checking dest ingress deny inexact match when ICMP.Code is non-nil and protocol is ICMP", func() {
		f.Proto = &ProtoICMP
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].ICMP = &v3.ICMPFields{Code: &int_1}
		// Inexact deny and exact end of tier deny means overall a deny.
		Expect(compute().Action).To(Equal(ActionFlagDeny | ActionFlagEndOfTierDeny))
	})

	It("checking source egress deny inexact match when ICMP.Code is non-nil and protocol is ICMP", func() {
		f.Proto = &ProtoICMP
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].ICMP = &v3.ICMPFields{Code: &int_1}
		// Inexact allow and exact end of tier deny means overall indeterminate. We'll have an entry for the same
		// policy once for allow and once for deny (end of tier implicit drop).
		r := compute()
		Expect(r.Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
		Expect(r.Include).To(BeTrue())
		Expect(r.Policies.FlowLogPolicyStrings()).To(Equal([]string{
			"0|meh|ns1/meh.policy|allow", "0|meh|ns1/meh.policy|eot-deny",
		}))
	})

	It("checking source egress deny inexact match when ICMP.Code is non-nil and protocol is unknown", func() {
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].ICMP = &v3.ICMPFields{Code: &int_1}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking source egress deny exact non-match when ICMP.Code is non-nil and protocol is not ICMP", func() {
		f.Proto = &ProtoTCP
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].ICMP = &v3.ICMPFields{Code: &int_1}
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))
	})

	It("checking dest ingress deny inexact match when ICMP.Code is non-nil and protocol is ICMP", func() {
		f.Proto = &ProtoICMP
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].ICMP = &v3.ICMPFields{Code: &int_1}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking source egress allow inexact match when ICMP.Type is non-nil and protocol is ICMP", func() {
		f.Proto = &ProtoICMP
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].ICMP = &v3.ICMPFields{Type: &int_1}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking dest ingress allow inexact match when ICMP.Type is non-nil and protocol is ICMP", func() {
		f.Proto = &ProtoICMP
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].ICMP = &v3.ICMPFields{Type: &int_1}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking dest ingress allow inexact match when NotICMP.Type is non-nil and protocol is ICMP", func() {
		f.Proto = &ProtoICMP
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].NotICMP = &v3.ICMPFields{Type: &int_1}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking dest ingress deny inexact match when NotICMP.Type is non-nil and protocol is ICMP", func() {
		f.Proto = &ProtoICMP
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Deny
		np.Spec.Ingress[0].NotICMP = &v3.ICMPFields{Type: &int_1}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagDeny | ActionFlagEndOfTierDeny))
	})

	// ---- HTTP matcher ----

	It("checking source egress deny exact match when HTTP is non-nil", func() {
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Deny
		np.Spec.Egress[0].HTTP = &v3.HTTPMatch{}
		Expect(compute().Action).To(Equal(ActionFlagDeny))
	})

	It("checking dest ingress deny exact match deny when HTTP is non-nil", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Deny
		np.Spec.Ingress[0].HTTP = &v3.HTTPMatch{}
		Expect(compute().Action).To(Equal(ActionFlagDeny))
	})

	It("checking source egress deny inexact match when HTTP.Methods is non-nil", func() {
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].HTTP = &v3.HTTPMatch{Methods: []string{"post"}}
		// Inexact deny and exact end of tier deny means overall a deny.
		Expect(compute().Action).To(Equal(ActionFlagDeny | ActionFlagEndOfTierDeny))
	})

	It("checking dest ingress deny inexact match when HTTP.Methods is non-nil", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].HTTP = &v3.HTTPMatch{Methods: []string{"post"}}
		// Inexact deny and exact end of tier deny means overall a deny.
		Expect(compute().Action).To(Equal(ActionFlagDeny | ActionFlagEndOfTierDeny))
	})

	It("checking source egress deny inexact match when HTTP.Methods is non-nil", func() {
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].HTTP = &v3.HTTPMatch{Methods: []string{"post"}}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking dest ingress deny inexact match when HTTP.Methods is non-nil", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].HTTP = &v3.HTTPMatch{Methods: []string{"post"}}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking source egress allow inexact match when HTTP.Paths is non-nil", func() {
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].HTTP = &v3.HTTPMatch{Paths: []v3.HTTPPath{{Exact: "/url"}}}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking dest ingress allow inexact match when HTTP.Paths is non-nil", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].HTTP = &v3.HTTPMatch{Paths: []v3.HTTPPath{{Exact: "/url"}}}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	// ---- Protocol/NotProtocol matcher ----

	It("checking source egress allow exact match when Protocol is non-nil", func() {
		f.Proto = &uint8_17
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		p := numorstring.ProtocolFromString("UDP")
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Protocol = &p
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source ingress allow exact match when Protocol is non-nil", func() {
		f.Proto = &uint8_17
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		p := numorstring.ProtocolFromInt(17)
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Protocol = &p
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source egress allow non-match when Protocol is non-nil", func() {
		f.Proto = &uint8_17
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		p := numorstring.ProtocolFromString("TCP")
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Protocol = &p
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))
	})

	It("checking source ingress allow inexact match when Protocol is non-nil", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		p := numorstring.ProtocolFromInt(17)
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Protocol = &p
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking source ingress allow exact non-match when NotProtocol is non-nil", func() {
		f.Proto = &uint8_17
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		p := numorstring.ProtocolFromInt(17)
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].NotProtocol = &p
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))
	})

	// ---- IPVersion matcher ----

	It("checking source egress allow exact match when IPVersion is non-nil", func() {
		f.IPVersion = &int_4
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].IPVersion = &int_4
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source ingress allow exact match when IPVersion is non-nil", func() {
		f.IPVersion = &int_4
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].IPVersion = &int_4
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source egress allow non-match when IPVersion is non-nil", func() {
		f.IPVersion = &int_4
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].IPVersion = &int_6
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))
	})

	It("checking source ingress allow inexact match when IPVersion is non-nil", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].IPVersion = &int_4
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	// ---- Serviceaccount matcher ----

	It("checking dest ingress allow exact match using serviceaccount selector", func() {
		sa := "sa1"
		f.Destination.ServiceAccount = &sa
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.ServiceAccountSelector = "sal1 == 'sav1'"
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Deny
		Expect(compute().Action).To(Equal(ActionFlagDeny))
	})

	It("checking dest ingress allow non-match using serviceaccount selector", func() {
		sa := "sa1"
		f.Destination.ServiceAccount = &sa
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.ServiceAccountSelector = "sal1 == 'nope'"
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	// ---- Source.Nets / Source.NotNets ----

	It("checking dest ingress allow exact match when Source.Nets is non-nil", func() {
		ip := net.MustParseIP("10.0.0.1")
		f.Source.IP = &ip
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Source.Nets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source egress allow exact match when Source.Nets is non-nil", func() {
		ip := net.MustParseIP("10.0.0.1")
		f.Source.IP = &ip
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.Nets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source egress allow inexact match when Source.Nets is non-nil", func() {
		By("Checking default behavior for Calico Endpoint is exact non-match")
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.Nets = []string{"10.0.0.0/16"}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))

		By("Checking CalicoEndpointNetMatchAlways=true for Calico Endpoint is inexact match")
		setup(&pipcfg.Config{CalicoEndpointNetMatchAlways: true})
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.Nets = []string{"10.0.0.0/16"}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking source egress allow non-match when Source.Nets is non-nil", func() {
		ip := net.MustParseIP("10.10.0.1")
		f.Source.IP = &ip
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.Nets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))
	})

	It("checking source egress allow non-match when Source.NotNets is non-nil", func() {
		ip := net.MustParseIP("10.10.0.1")
		f.Source.IP = &ip
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.NotNets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	// ---- Destination.Nets / Destination.NotNets ----

	It("checking dest ingress allow exact match when Destination.Nets is non-nil", func() {
		ip := net.MustParseIP("10.0.0.1")
		f.Destination.IP = &ip
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Destination.Nets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source egress allow exact match when Destination.Nets is non-nil", func() {
		ip := net.MustParseIP("10.0.0.1")
		f.Destination.IP = &ip
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Destination.Nets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source egress allow inexact match when Destination.Nets is non-nil", func() {
		By("Checking default behavior for Calico Endpoint is exact non-match")
		f.Destination.Type = EndpointTypeWep
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Destination.Nets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))

		By("Checking CalicoEndpointNetMatchAlways=true for Calico Endpoint is inexact match")
		setup(&pipcfg.Config{CalicoEndpointNetMatchAlways: true})
		f.Destination.Type = EndpointTypeWep
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Destination.Nets = []string{"10.0.0.0/16"}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking source egress allow non-match when Destination.Nets is non-nil", func() {
		ip := net.MustParseIP("10.10.0.1")
		f.Destination.IP = &ip
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Destination.Nets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))
	})

	It("checking source egress allow non-match when Destination.NotNets is non-nil", func() {
		ip := net.MustParseIP("10.10.0.1")
		f.Destination.IP = &ip
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Destination.NotNets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	// ---- Source.Nets / Source.NotNets ----

	It("checking dest ingress allow exact match when Source.Nets is non-nil", func() {
		ip := net.MustParseIP("10.0.0.1")
		f.Source.IP = &ip
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Source.Nets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source egress allow exact match when Source.Nets is non-nil", func() {
		ip := net.MustParseIP("10.0.0.1")
		f.Source.IP = &ip
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.Nets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source egress allow inexact match when Source.Nets is non-nil", func() {
		By("Checking default behavior for Calico Endpoint is exact non-match")
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.Nets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))

		By("Checking CalicoEndpointNetMatchAlways=true for Calico Endpoint is inexact match")
		setup(&pipcfg.Config{CalicoEndpointNetMatchAlways: true})
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.Nets = []string{"10.0.0.0/16"}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking dest egress allow non-match when Source.Nets is non-nil", func() {
		ip := net.MustParseIP("10.10.0.1")
		f.Source.IP = &ip
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.Nets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))
	})

	It("checking dest egress allow non-match when Source.NotNets is non-nil", func() {
		ip := net.MustParseIP("10.10.0.1")
		f.Source.IP = &ip
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.NotNets = []string{"10.0.0.0/16"}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	// ---- Destination.Ports / Destination.NotPorts ----

	It("checking dest ingress allow exact match when Destination.Ports is non-nil", func() {
		f.Destination.Port = &uint16_1000
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		p, _ := numorstring.PortFromRange(999, 1000)
		np.Spec.Ingress[0].Destination.Ports = []numorstring.Port{p}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking dest egress allow exact match when Destination.Ports is non-nil (contains named port plus exact numerical port match)", func() {
		f.Destination.Port = &uint16_1000
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		p1, _ := numorstring.PortFromRange(1000, 10000)
		p2, _ := numorstring.PortFromString("myport")
		np.Spec.Egress[0].Destination.Ports = []numorstring.Port{p1, p2}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking dest egress allow inexact match when Destination.Ports is non-nil and contains a named port only", func() {
		f.Destination.Port = &uint16_1000
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		p, _ := numorstring.PortFromString("myport")
		np.Spec.Egress[0].Destination.Ports = []numorstring.Port{p}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking dest egress allow inexact match when Destination.Ports is non-nil and flow contains no port", func() {
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		p, _ := numorstring.PortFromRange(1000, 10000)
		np.Spec.Egress[0].Destination.Ports = []numorstring.Port{p}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking dest egress allow non-match when Destination.Ports is non-nil", func() {
		f.Destination.Port = &uint16_1000
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		p, _ := numorstring.PortFromRange(1001, 10000)
		np.Spec.Egress[0].Action = v3.Deny
		np.Spec.Egress[0].Destination.Ports = []numorstring.Port{p}
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))
	})

	It("checking dest egress allow non-match when Destination.NotPorts is non-nil", func() {
		f.Destination.Port = &uint16_1000
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		p, _ := numorstring.PortFromRange(1001, 10000)
		np.Spec.Egress[0].Destination.NotPorts = []numorstring.Port{p}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	// ---- Source.Ports / Source.NotPorts ----

	It("checking source egress allow exact match when Source.Ports is non-nil", func() {
		f.Source.Port = &uint16_1000
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		p, _ := numorstring.PortFromRange(999, 1000)
		np.Spec.Egress[0].Source.Ports = []numorstring.Port{p}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source ingress allow exact match when Source.Ports is non-nil (contains named port plus exact numerical port match)", func() {
		f.Source.Port = &uint16_1000
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		p1, _ := numorstring.PortFromRange(1000, 10000)
		p2, _ := numorstring.PortFromString("myport")
		np.Spec.Ingress[0].Source.Ports = []numorstring.Port{p1, p2}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source ingress allow inexact match when Source.Ports is non-nil and contains a named port only", func() {
		f.Source.Port = &uint16_1000
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		p, _ := numorstring.PortFromString("myport")
		np.Spec.Ingress[0].Source.Ports = []numorstring.Port{p}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking source ingress allow inexact match when Source.Ports is non-nil and flow contains no port", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		p, _ := numorstring.PortFromRange(1000, 10000)
		np.Spec.Ingress[0].Source.Ports = []numorstring.Port{p}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking source ingress allow non-match when Source.Ports is non-nil", func() {
		f.Source.Port = &uint16_1000
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		p, _ := numorstring.PortFromRange(1001, 10000)
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Source.Ports = []numorstring.Port{p}
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))
	})

	It("checking source ingress allow non-match when Source.NotPorts is non-nil", func() {
		f.Source.Port = &uint16_1000
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		p, _ := numorstring.PortFromRange(1001, 10000)
		np.Spec.Ingress[0].Source.NotPorts = []numorstring.Port{p}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	// ---- Destination.Domains ----

	It("checking source egress allow exact match when Source.Domains is non-nil but empty", func() {
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Destination.Domains = []string{}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source egress deny inexact match when Source.Domains has domains", func() {
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Deny
		np.Spec.Egress[0].Destination.Domains = []string{"thing.com"}
		// Inexact deny and exact end of tier deny means overall a deny.
		Expect(compute().Action).To(Equal(ActionFlagDeny | ActionFlagEndOfTierDeny))
	})

	It("checking source egress allow inexact match when Source.Domains has domains", func() {
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Destination.Domains = []string{"thing.com"}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking dest ingress allow exact match when Source.Domains is non-nil but empty", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Destination.Domains = []string{}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking dest ingress deny inexact match when Source.Domains has domains", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Deny
		np.Spec.Ingress[0].Destination.Domains = []string{"thing.com"}
		// Inexact deny and exact end of tier deny means overall a deny.
		Expect(compute().Action).To(Equal(ActionFlagDeny | ActionFlagEndOfTierDeny))
	})

	It("checking dest ingress allow inexact match when Source.Domains has domains", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Destination.Domains = []string{"thing.com"}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	// ---- Source.Domains ----

	It("checking dest ingress allow exact match when Destination.Domains is non-nil but empty", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Source.Domains = []string{}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking dest ingress deny inexact match when Destination.Domains has domains", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Deny
		np.Spec.Ingress[0].Source.Domains = []string{"thing.com"}
		// Inexact deny and exact end of tier deny means overall a deny.
		Expect(compute().Action).To(Equal(ActionFlagDeny | ActionFlagEndOfTierDeny))
	})

	It("checking dest ingress allow inexact match when Destination.Domains has domains", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Source.Domains = []string{"thing.com"}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking source egress allow exact match when Destination.Domains is non-nil but empty", func() {
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.Domains = []string{}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source egress deny inexact match when Destination.Domains has domains", func() {
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Deny
		np.Spec.Egress[0].Source.Domains = []string{"thing.com"}
		// Inexact deny and exact end of tier deny means overall a deny.
		Expect(compute().Action).To(Equal(ActionFlagDeny | ActionFlagEndOfTierDeny))
	})

	It("checking source egress allow inexact match when Destination.Domains has domains", func() {
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.Domains = []string{"thing.com"}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	// ---- Destination.ServiceAccounts ----

	It("checking dest ingress allow exact match when Destination.ServiceAccounts is non-nil but empty", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Destination.ServiceAccounts = &v3.ServiceAccountMatch{}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking dest ingress allow exact match when Destination.ServiceAccounts is non-nil", func() {
		sa := "sa1"
		f.Destination.ServiceAccount = &sa
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Destination.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source egress allow exact match when Destination.ServiceAccounts is non-nil", func() {
		sa := "sa1"
		f.Destination.Type = EndpointTypeWep
		f.Destination.ServiceAccount = &sa
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Destination.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking source egress allow inexact match when Destination.ServiceAccounts is non-nil", func() {
		f.Destination.Type = EndpointTypeWep
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Destination.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking source egress allow non-match when Destination.ServiceAccounts is non-nil", func() {
		sa := "sa2"
		f.Destination.Type = EndpointTypeWep
		f.Destination.ServiceAccount = &sa
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Destination.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))
	})

	// ---- Source.ServiceAccounts ----

	It("checking source egress allow exact match when Source.ServiceAccounts is non-nil but empty", func() {
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		f.Policies = []PolicyHit{{
			MatchIndex: 0,
			Tier:       "meh",
			Name:       "ns1/meh.policy",
			Action:     ActionFlagAllow,
		}}
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.ServiceAccounts = &v3.ServiceAccountMatch{}
		Expect(compute().Action).To(Equal(ActionFlagAllow|ActionFlagFlowLogMatchesCalculated))
	})

	It("checking source egress allow exact match when Source.ServiceAccounts is non-nil", func() {
		sa := "sa1"
		f.Source.ServiceAccount = &sa
		f.Source.Namespace = "ns1"
		f.Source.Type = EndpointTypeWep
		np.Spec.Types = typesEgress
		np.Spec.Ingress = nil
		np.Spec.Egress[0].Action = v3.Allow
		np.Spec.Egress[0].Source.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking dest ingress allow exact match when Source.ServiceAccounts is non-nil", func() {
		sa := "sa1"
		f.Source.Type = EndpointTypeWep
		f.Source.ServiceAccount = &sa
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Source.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	It("checking dest ingress allow inexact match when Source.ServiceAccounts is non-nil", func() {
		f.Source.Type = EndpointTypeWep
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Source.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
	})

	It("checking dest ingress pass inexact match when Source.ServiceAccounts is non-nil", func() {
		f.Source.Type = EndpointTypeWep
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Pass // Pass shifts to profiles which will allow by default
		np.Spec.Ingress[0].Source.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		// Inexact allow (through inexact pass) and exact end of tier deny means overall indeterminate.
		r := compute()
		Expect(r.Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
		Expect(r.Policies.FlowLogPolicyStrings()).To(Equal([]string{"0|meh|ns1/meh.policy|pass", "0|meh|ns1/meh.policy|eot-deny", "1|__PROFILE__|__PROFILE__.kns.ns1|allow"}))
	})

	It("checking dest ingress allow non-match when Source.ServiceAccounts is non-nil", func() {
		sa := "sa2"
		f.Source.Type = EndpointTypeWep
		f.Source.ServiceAccount = &sa
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Source.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		Expect(compute().Action).To(Equal(ActionFlagEndOfTierDeny))
	})

	It("checking dest ingress allow inexact match is fixed from flow data", func() {
		f.Source.Type = EndpointTypeWep
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		f.Policies = []PolicyHit{{
			MatchIndex: 0,
			Tier:       "meh",
			Name:       "ns1/meh.policy",
			Action:     ActionFlagAllow,
		}}
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Source.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		// Inexact allow and exact end of tier deny means overall indeterminate.
		r := compute()
		Expect(r.Action).To(Equal(ActionFlagAllow | ActionFlagFlowLogRemovedUncertainty))
		Expect(r.Policies.FlowLogPolicyStrings()).To(Equal([]string{"0|meh|ns1/meh.policy|allow"}))
	})

	It("checking dest ingress pass inexact match is fixed from flow data", func() {
		f.Source.Type = EndpointTypeWep
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		f.Policies = []PolicyHit{{
			MatchIndex: 0,
			Tier:       "meh",
			Name:       "ns1/meh.policy",
			Action:     ActionFlagNextTier,
		}}
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Pass
		np.Spec.Ingress[0].Source.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		// Inexact pass confirmed by flow and exact end-of-all-tiers allow.
		r := compute()
		Expect(r.Action).To(Equal(ActionFlagAllow | ActionFlagFlowLogRemovedUncertainty))
		Expect(r.Policies.FlowLogPolicyStrings()).To(Equal([]string{"0|meh|ns1/meh.policy|pass", "1|__PROFILE__|__PROFILE__.kns.ns1|allow"}))
	})

	It("checking dest ingress allow inexact match is not fixed from flow data when action does not match", func() {
		f.Source.Type = EndpointTypeWep
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		f.Policies = []PolicyHit{{
			MatchIndex: 0,
			Tier:       "meh",
			Name:       "ns1/meh.policy",
			Action:     ActionFlagDeny,
		}}
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Source.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		// Inexact allow and exact end of tier deny means overall indeterminate. Flow data action does not match and
		// cannot be used.
		r := compute()
		Expect(r.Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny | ActionFlagFlowLogConflictsWithCalculated))
		Expect(r.Policies.FlowLogPolicyStrings()).To(Equal([]string{"0|meh|ns1/meh.policy|allow", "0|meh|ns1/meh.policy|eot-deny"}))
	})

	It("checking dest ingress allow inexact match is not fixed from flow data when flow contains multiple actions for same policy", func() {
		f.Source.Type = EndpointTypeWep
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		f.Policies = []PolicyHit{{
			MatchIndex: 0,
			Tier:       "meh",
			Name:       "ns1/meh.policy",
			Action:     ActionFlagAllow,
		}, {
			MatchIndex: 1,
			Tier:       "meh",
			Name:       "ns1/meh.policy",
			Action:     ActionFlagDeny,
		}}
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress[0].Action = v3.Pass
		np.Spec.Ingress[0].Source.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		// Inexact allow and exact end of tier deny means overall indeterminate. Flow data has multiple actions and
		// cannot be used. Therefore result can be pass or deny, and pass will hit profile allow.
		r := compute()
		Expect(r.Action).To(Equal(ActionFlagAllow | ActionFlagEndOfTierDeny))
		Expect(r.Policies.FlowLogPolicyStrings()).To(Equal([]string{
			"0|meh|ns1/meh.policy|pass", "0|meh|ns1/meh.policy|eot-deny", "1|__PROFILE__|__PROFILE__.kns.ns1|allow",
		}))
	})

	It("checking dest ingress allow and deny inexact match", func() {
		f.Source.Type = EndpointTypeWep
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress = make([]v3.Rule, 2)
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Source.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		np.Spec.Ingress[1].Action = v3.Deny
		np.Spec.Ingress[1].Source.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa2"}}
		// Inexact allow and inexact deny in same policy means overall indeterminate.
		Expect(compute().Action).To(Equal(ActionFlagAllow | ActionFlagDeny | ActionFlagEndOfTierDeny))
	})

	It("checking dest ingress allow and deny inexact match is fixed from flow data", func() {
		f.Source.Type = EndpointTypeWep
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		f.Policies = []PolicyHit{{
			MatchIndex: 0,
			Tier:       "meh",
			Name:       "ns1/meh.policy",
			Action:     ActionFlagDeny,
		}}
		np.Spec.Types = typesIngress
		np.Spec.Egress = nil
		np.Spec.Ingress = make([]v3.Rule, 2)
		np.Spec.Ingress[0].Action = v3.Allow
		np.Spec.Ingress[0].Source.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa1"}}
		np.Spec.Ingress[1].Action = v3.Deny
		np.Spec.Ingress[1].Source.ServiceAccounts = &v3.ServiceAccountMatch{Names: []string{"sa2"}}
		// Inexact allow and inexact deny in same policy. Flow contains exact match which agrees with one of the
		// possible values, so use that.
		r := compute()
		Expect(r.Action).To(Equal(ActionFlagDeny | ActionFlagFlowLogRemovedUncertainty))
		Expect(r.Policies.FlowLogPolicyStrings()).To(Equal([]string{"0|meh|ns1/meh.policy|deny"}))
	})
})

var _ = Describe("Compiled tiers and gnpolicies tests", func() {
	var f *Flow
	var gnp *v3.GlobalNetworkPolicy
	var tiers Tiers
	var rd *ResourceData
	var impacted ImpactedResources
	var sel *EndpointSelectorHandler
	var compute func() EndpointResponse

	setup := func(cfg *pipcfg.Config) {
		gnp = &v3.GlobalNetworkPolicy{
			TypeMeta: resources.TypeCalicoGlobalNetworkPolicies,
			ObjectMeta: v1.ObjectMeta{
				Name: "policy",
			},
			Spec: v3.GlobalNetworkPolicySpec{
				Tier:     "meh",
				Selector: "all()",
				Types:    typesIngress,
				Ingress: []v3.Rule{{
					Action: v3.Deny,
				}},
			},
		}

		tiers = Tiers{{{Policy: gnp}}}
		impacted = make(ImpactedResources)
		sel = NewEndpointSelectorHandler()
		rd = &ResourceData{
			Tiers: tiers,
			Namespaces: []*corev1.Namespace{{
				ObjectMeta: v1.ObjectMeta{
					Name: "ns1",
					Labels: map[string]string{
						"nsl1": "nsv1",
					},
				},
			}},
			ServiceAccounts: []*corev1.ServiceAccount{{
				ObjectMeta: v1.ObjectMeta{
					Name:      "sa1",
					Namespace: "ns1",
					Labels: map[string]string{
						"sal1": "sav1",
					},
				},
			}},
		}
		f = &Flow{
			ActionFlag: ActionFlagAllow,
			Source: FlowEndpointData{
				Type:   EndpointTypeNet,
				Labels: map[string]string{},
			},
			Destination: FlowEndpointData{
				Type:   EndpointTypeNet,
				Labels: map[string]string{},
			},
		}

		compute = func() EndpointResponse {
			ingress, egress := calculateCompiledTiersAndImpactedPolicies(cfg, rd, impacted, sel, false)

			// Tweak our flow reporter to match the policy type.
			flowCache := &flowCache{
				source:      endpointCache{selectors: sel.CreateSelectorCache()},
				destination: endpointCache{selectors: sel.CreateSelectorCache()},
				policies:    make(map[string]ActionFlag),
			}

			if gnp.Spec.Types[0] == v3.PolicyTypeIngress {
				f.Reporter = ReporterTypeDestination
				return ingress.Calculate(f, flowCache, false)
			}
			f.Reporter = ReporterTypeSource
			return egress.Calculate(f, flowCache, false)
		}
	}

	BeforeEach(func() {
		setup(&pipcfg.Config{})
	})

	// -- serviceaccounts --
	It("matches using serviceaccounselector", func() {
		sa := "sa1"
		f.Destination.ServiceAccount = &sa
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		gnp.Spec.ServiceAccountSelector = "sal1 == 'sav1'"
		Expect(compute().Action).To(Equal(ActionFlagDeny))
	})

	It("doesn't apply if serviceaccountselector doesn't match", func() {
		sa := "sa1"
		f.Destination.ServiceAccount = &sa
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		gnp.Spec.ServiceAccountSelector = "sal1 == 'nope'"
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})

	// -- namespace selectors --
	It("matches using namespaceselector", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		gnp.Spec.NamespaceSelector = "nsl1 == 'nsv1'"
		Expect(compute().Action).To(Equal(ActionFlagDeny))
	})

	It("doesn't apply if namespaceselector doesn't match", func() {
		f.Destination.Namespace = "ns1"
		f.Destination.Type = EndpointTypeWep
		gnp.Spec.NamespaceSelector = "nsl1 == 'nomatch'"
		Expect(compute().Action).To(Equal(ActionFlagAllow))
	})
})
