// Copyright (c) 2020 Tigera, Inc. All rights reserved.

package calc

import (
	"fmt"

	v3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/model"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ActiveEgressCalculator", func() {

	var (
		aec *ActiveEgressCalculator
		cbs *testCallbacks
	)

	we1Key := model.WorkloadEndpointKey{WorkloadID: "we1"}
	we1Value1 := &model.WorkloadEndpoint{
		Name:           "we1",
		EgressSelector: "black == 'white'",
	}
	we1Value2 := &model.WorkloadEndpoint{
		Name:           "we1",
		EgressSelector: "black == 'red'",
	}
	we2Key := model.WorkloadEndpointKey{WorkloadID: "we2"}

	BeforeEach(func() {
		aec = NewActiveEgressCalculator()
		cbs = &testCallbacks{}
		aec.OnIPSetActive = cbs.OnIPSetActive
		aec.OnIPSetInactive = cbs.OnIPSetInactive
		aec.OnEgressIPSetIDUpdate = cbs.OnEgressIPSetIDUpdate
	})

	It("generates expected callbacks for a single WorkloadEndpoint", func() {

		By("creating a WorkloadEndpoint with egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key:   we1Key,
				Value: we1Value1,
			},
			UpdateType: api.UpdateTypeKVNew,
		})

		// Expect IPSetActive and EgressIPSetIDUpdate.
		ipSetID1 := cbs.ExpectActive()
		cbs.ExpectEgressUpdate(we1Key, ipSetID1)
		cbs.ExpectNoMoreCallbacks()

		By("changing WorkloadEndpoint's egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key:   we1Key,
				Value: we1Value2,
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})

		// Expect IPSetInactive for old selector.
		cbs.ExpectInactive(ipSetID1)

		// Expect IPSetActive and EgressIPSetIDUpdate with new ID.
		ipSetID2 := cbs.ExpectActive()
		cbs.ExpectEgressUpdate(we1Key, ipSetID2)
		cbs.ExpectNoMoreCallbacks()
		Expect(ipSetID2).NotTo(Equal(ipSetID1))

		By("deleting WorkloadEndpoint")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key:   we1Key,
				Value: nil,
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})

		// Expect IPSetInactive for old selector.
		cbs.ExpectInactive(ipSetID2)
		cbs.ExpectNoMoreCallbacks()
	})

	It("generates expected callbacks for two WorkloadEndpoints with same selector", func() {

		By("creating two WorkloadEndpoints with the same egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key:   we1Key,
				Value: we1Value1,
			},
			UpdateType: api.UpdateTypeKVNew,
		})
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key:   we2Key,
				Value: we1Value1,
			},
			UpdateType: api.UpdateTypeKVNew,
		})

		// Expect 1 IPSetActive and 2 EgressIPSetIDUpdates.
		ipSetID := cbs.ExpectActive()
		cbs.ExpectEgressUpdate(we1Key, ipSetID)
		cbs.ExpectEgressUpdate(we2Key, ipSetID)
		cbs.ExpectNoMoreCallbacks()

		By("deleting WorkloadEndpoint #1")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key:   we1Key,
				Value: nil,
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})

		// Expect no change.
		cbs.ExpectNoMoreCallbacks()

		By("deleting WorkloadEndpoint #2")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key:   we2Key,
				Value: nil,
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})

		// Expect IPSetInactive for old selector.
		cbs.ExpectInactive(ipSetID)
		cbs.ExpectNoMoreCallbacks()
	})

	It("generates expected callbacks for WorkloadEndpoint with profile", func() {

		By("creating WorkloadEndpoint with profile ID but no egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: we1Key,
				Value: &model.WorkloadEndpoint{
					Name:       "we1",
					ProfileIDs: []string{"webclient"},
				},
			},
			UpdateType: api.UpdateTypeKVNew,
		})

		cbs.ExpectEgressUpdate(we1Key, "")
		cbs.ExpectNoMoreCallbacks()

		By("adding Profile with egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.ResourceKey{Kind: v3.KindProfile, Name: "webclient"},
				Value: &v3.Profile{
					Spec: v3.ProfileSpec{
						EgressGateway: &v3.EgressSpec{
							Selector: "server == 'bump'",
						},
					},
				},
			},
			UpdateType: api.UpdateTypeKVNew,
		})

		// Expect IPSetActive and EgressIPSetIDUpdate.
		ipSetID1 := cbs.ExpectActive()
		cbs.ExpectEgressUpdate(we1Key, ipSetID1)
		cbs.ExpectNoMoreCallbacks()

		By("updating Profile with different selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.ResourceKey{Kind: v3.KindProfile, Name: "webclient"},
				Value: &v3.Profile{
					Spec: v3.ProfileSpec{
						EgressGateway: &v3.EgressSpec{
							Selector: "server == 'wire'",
						},
					},
				},
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})

		// Expect IPSetInactive for old selector.
		cbs.ExpectInactive(ipSetID1)

		// Expect IPSetActive and EgressIPSetIDUpdate with new ID.
		ipSetID2 := cbs.ExpectActive()
		cbs.ExpectEgressUpdate(we1Key, ipSetID2)
		cbs.ExpectNoMoreCallbacks()
		Expect(ipSetID2).NotTo(Equal(ipSetID1))

		By("updating WorkloadEndpoint with its own egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: we1Key,
				Value: &model.WorkloadEndpoint{
					Name:           "we1",
					ProfileIDs:     []string{"webclient"},
					EgressSelector: "black == 'red'",
				},
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})

		// Expect IPSetInactive for old selector.
		cbs.ExpectInactive(ipSetID2)

		// Expect IPSetActive and EgressIPSetIDUpdate for new WE selector.
		ipSetID3 := cbs.ExpectActive()
		cbs.ExpectEgressUpdate(we1Key, ipSetID3)
		cbs.ExpectNoMoreCallbacks()
		Expect(ipSetID3).NotTo(Equal(ipSetID1))
		Expect(ipSetID3).NotTo(Equal(ipSetID2))

		By("updating WorkloadEndpoint with no egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: we1Key,
				Value: &model.WorkloadEndpoint{
					Name:       "we1",
					ProfileIDs: []string{"webclient"},
				},
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})

		// Expect IPSetInactive for old (WE) selector.
		cbs.ExpectInactive(ipSetID3)

		// Expect IPSetActive and EgressIPSetIDUpdate for new (profile) selector.
		Expect(cbs.ExpectActive()).To(Equal(ipSetID2))
		cbs.ExpectEgressUpdate(we1Key, ipSetID2)
		cbs.ExpectNoMoreCallbacks()

		By("updating Profile with no egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.ResourceKey{Kind: v3.KindProfile, Name: "webclient"},
				Value: &v3.Profile{
					Spec: v3.ProfileSpec{},
				},
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})

		// Expect IPSetInactive for old (profile) selector.
		cbs.ExpectInactive(ipSetID2)

		// Expect EgressIPSetIDUpdate with IP set ID "".
		cbs.ExpectEgressUpdate(we1Key, "")
		cbs.ExpectNoMoreCallbacks()

		By("deleting the WorkloadEndpoint")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key:   we1Key,
				Value: nil,
			},
			UpdateType: api.UpdateTypeKVDeleted,
		})
		cbs.ExpectNoMoreCallbacks()

		By("deleting the Profile")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key:   model.ResourceKey{Kind: v3.KindProfile, Name: "webclient"},
				Value: nil,
			},
			UpdateType: api.UpdateTypeKVDeleted,
		})
		cbs.ExpectNoMoreCallbacks()
	})

	It("generates expected callbacks for multiple WorkloadEndpoints and Profiles", func() {

		By("creating 5 WEs with profile A, 5 WEs with profile B")
		for _, profile := range []string{"a", "b"} {
			for i := 0; i < 5; i++ {
				name := fmt.Sprintf("we%v-%v", i, profile)
				aec.OnUpdate(api.Update{
					KVPair: model.KVPair{
						Key: model.WorkloadEndpointKey{WorkloadID: name},
						Value: &model.WorkloadEndpoint{
							Name:       name,
							ProfileIDs: []string{profile},
						},
					},
					UpdateType: api.UpdateTypeKVNew,
				})
				cbs.ExpectEgressUpdate(model.WorkloadEndpointKey{WorkloadID: name}, "")
			}
		}
		cbs.ExpectNoMoreCallbacks()

		By("creating profile A with egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.ResourceKey{Kind: v3.KindProfile, Name: "a"},
				Value: &v3.Profile{
					Spec: v3.ProfileSpec{
						EgressGateway: &v3.EgressSpec{
							Selector: "server == 'a'",
						},
					},
				},
			},
			UpdateType: api.UpdateTypeKVNew,
		})

		// Expect Active for that selector and EgressIPSetIDUpdate for the 5 using WEs.
		ipSetA := cbs.ExpectActive()
		for i := 0; i < 5; i++ {
			name := fmt.Sprintf("we%v-a", i)
			cbs.ExpectEgressUpdate(model.WorkloadEndpointKey{WorkloadID: name}, ipSetA)
		}
		cbs.ExpectNoMoreCallbacks()

		By("changing profile A’s selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.ResourceKey{Kind: v3.KindProfile, Name: "a"},
				Value: &v3.Profile{
					Spec: v3.ProfileSpec{
						EgressGateway: &v3.EgressSpec{
							Selector: "server == 'aprime'",
						},
					},
				},
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})

		// Expect Inactive for old, Active for new, EgressIPSetIDUpdate for the 5 using WEs.
		cbs.ExpectInactive(ipSetA)
		ipSetAPrime := cbs.ExpectActive()
		for i := 0; i < 5; i++ {
			name := fmt.Sprintf("we%v-a", i)
			cbs.ExpectEgressUpdate(model.WorkloadEndpointKey{WorkloadID: name}, ipSetAPrime)
		}
		cbs.ExpectNoMoreCallbacks()

		By("creating profile B with different egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.ResourceKey{Kind: v3.KindProfile, Name: "b"},
				Value: &v3.Profile{
					Spec: v3.ProfileSpec{
						EgressGateway: &v3.EgressSpec{
							Selector: "server == 'b'",
						},
					},
				},
			},
			UpdateType: api.UpdateTypeKVNew,
		})

		// Expect Active for that selector and EgressIPSetIDUpdate for the 5 using WEs.
		ipSetB := cbs.ExpectActive()
		for i := 0; i < 5; i++ {
			name := fmt.Sprintf("we%v-b", i)
			cbs.ExpectEgressUpdate(model.WorkloadEndpointKey{WorkloadID: name}, ipSetB)
		}
		cbs.ExpectNoMoreCallbacks()

		By("deleting profile A")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key:   model.ResourceKey{Kind: v3.KindProfile, Name: "a"},
				Value: nil,
			},
			UpdateType: api.UpdateTypeKVDeleted,
		})

		// Expect Inactive for its selector and EgressIPSetIDUpdate ““ for the 5 using WEs.
		cbs.ExpectInactive(ipSetAPrime)
		for i := 0; i < 5; i++ {
			name := fmt.Sprintf("we%v-a", i)
			cbs.ExpectEgressUpdate(model.WorkloadEndpointKey{WorkloadID: name}, "")
		}
		cbs.ExpectNoMoreCallbacks()

		By("deleting the endpoints using profile A")
		for i := 0; i < 5; i++ {
			name := fmt.Sprintf("we%v-a", i)
			aec.OnUpdate(api.Update{
				KVPair: model.KVPair{
					Key:   model.WorkloadEndpointKey{WorkloadID: name},
					Value: nil,
				},
				UpdateType: api.UpdateTypeKVDeleted,
			})
		}
		cbs.ExpectNoMoreCallbacks()

	})

	It("ignores unexpected update", func() {
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.ResourceKey{Kind: v3.KindNode, Name: "a"},
				Value: &v3.Node{
					Spec: v3.NodeSpec{},
				},
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})
		cbs.ExpectNoMoreCallbacks()

		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.HostConfigKey{
					Hostname: "myhost",
					Name:     "IPv4VXLANTunnelAddr",
				},
				Value: "10.0.0.0",
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})
		cbs.ExpectNoMoreCallbacks()
	})

	It("handles when profile is defined before endpoint", func() {

		By("defining profile A with egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.ResourceKey{Kind: v3.KindProfile, Name: "a"},
				Value: &v3.Profile{
					Spec: v3.ProfileSpec{
						EgressGateway: &v3.EgressSpec{
							Selector: "server == 'a'",
						},
					},
				},
			},
			UpdateType: api.UpdateTypeKVNew,
		})
		cbs.ExpectNoMoreCallbacks()

		By("defining an endpoint that uses that profile")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.WorkloadEndpointKey{WorkloadID: "we1"},
				Value: &model.WorkloadEndpoint{
					Name:       "we1",
					ProfileIDs: []string{"a"},
				},
			},
			UpdateType: api.UpdateTypeKVNew,
		})
		ipSetID := cbs.ExpectActive()
		cbs.ExpectEgressUpdate(model.WorkloadEndpointKey{WorkloadID: "we1"}, ipSetID)
		cbs.ExpectNoMoreCallbacks()

		By("updating profile with same selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.ResourceKey{Kind: v3.KindProfile, Name: "a"},
				Value: &v3.Profile{
					Spec: v3.ProfileSpec{
						EgressGateway: &v3.EgressSpec{
							Selector: "server == 'a'",
						},
						LabelsToApply: map[string]string{"a": "b"},
					},
				},
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})
		cbs.ExpectNoMoreCallbacks()

	})

	It("handles when WorkloadEndpoint and profile both specify selectors", func() {

		By("creating WorkloadEndpoint with profile ID and egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: we1Key,
				Value: &model.WorkloadEndpoint{
					Name:           "we1",
					ProfileIDs:     []string{"webclient"},
					EgressSelector: "black == 'red'",
				},
			},
			UpdateType: api.UpdateTypeKVNew,
		})

		ipSetWE := cbs.ExpectActive()
		cbs.ExpectEgressUpdate(we1Key, ipSetWE)
		cbs.ExpectNoMoreCallbacks()

		By("adding Profile with egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.ResourceKey{Kind: v3.KindProfile, Name: "webclient"},
				Value: &v3.Profile{
					Spec: v3.ProfileSpec{
						EgressGateway: &v3.EgressSpec{
							Selector: "server == 'bump'",
						},
					},
				},
			},
			UpdateType: api.UpdateTypeKVNew,
		})

		// Expect no change.
		cbs.ExpectNoMoreCallbacks()

		By("updating Profile with different selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.ResourceKey{Kind: v3.KindProfile, Name: "webclient"},
				Value: &v3.Profile{
					Spec: v3.ProfileSpec{
						EgressGateway: &v3.EgressSpec{
							Selector: "server == 'wire'",
						},
					},
				},
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})

		// Expect no change.
		cbs.ExpectNoMoreCallbacks()

		By("defining 2nd WorkloadEndpoint with no selector and different profile")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: we2Key,
				Value: &model.WorkloadEndpoint{
					Name:       "we2",
					ProfileIDs: []string{"other"},
				},
			},
			UpdateType: api.UpdateTypeKVNew,
		})

		// Expect IPSetActive and EgressIPSetIDUpdate with new ID.
		cbs.ExpectEgressUpdate(we2Key, "")
		cbs.ExpectNoMoreCallbacks()

		By("changing first profile not to have egress selector")
		aec.OnUpdate(api.Update{
			KVPair: model.KVPair{
				Key: model.ResourceKey{Kind: v3.KindProfile, Name: "webclient"},
				Value: &v3.Profile{
					Spec: v3.ProfileSpec{},
				},
			},
			UpdateType: api.UpdateTypeKVUpdated,
		})

		// Expect no change.
		cbs.ExpectNoMoreCallbacks()
	})

})

type testCallbacks struct {
	activeCalls      []*IPSetData
	inactiveCalls    []*IPSetData
	egressUpdateKeys []model.WorkloadEndpointKey
	egressUpdateIDs  []string
}

func (tc *testCallbacks) OnIPSetActive(ipSet *IPSetData) {
	tc.activeCalls = append(tc.activeCalls, ipSet)
}

func (tc *testCallbacks) OnIPSetInactive(ipSet *IPSetData) {
	tc.inactiveCalls = append(tc.inactiveCalls, ipSet)
}

func (tc *testCallbacks) OnEgressIPSetIDUpdate(key model.WorkloadEndpointKey, egressIPSetID string) {
	tc.egressUpdateKeys = append(tc.egressUpdateKeys, key)
	tc.egressUpdateIDs = append(tc.egressUpdateIDs, egressIPSetID)
}

func (tc *testCallbacks) ExpectActive() string {
	Expect(len(tc.activeCalls)).To(BeNumerically(">=", 1))
	Expect(tc.activeCalls[0].isEgressSelector).To(BeTrue())
	ipSetID := tc.activeCalls[0].cachedUID
	Expect(ipSetID).To(HavePrefix("e:"))
	tc.activeCalls = tc.activeCalls[1:]
	return ipSetID
}

func (tc *testCallbacks) ExpectInactive(id string) {
	Expect(len(tc.inactiveCalls)).To(BeNumerically(">=", 1))
	Expect(tc.inactiveCalls[0].isEgressSelector).To(BeTrue())
	Expect(tc.inactiveCalls[0].cachedUID).To(Equal(id))
	tc.inactiveCalls = tc.inactiveCalls[1:]
}

func (tc *testCallbacks) ExpectEgressUpdate(key model.WorkloadEndpointKey, id string) {
	Expect(tc.egressUpdateKeys).To(ContainElement(key))
	keyPos := -1
	for i, uk := range tc.egressUpdateKeys {
		if uk == key {
			Expect(tc.egressUpdateIDs[i]).To(Equal(id))
			keyPos = i
			break
		}
	}
	Expect(keyPos).NotTo(Equal(-1))
	tc.egressUpdateKeys = append(tc.egressUpdateKeys[:keyPos], tc.egressUpdateKeys[keyPos+1:]...)
	tc.egressUpdateIDs = append(tc.egressUpdateIDs[:keyPos], tc.egressUpdateIDs[keyPos+1:]...)
}

func (tc *testCallbacks) ExpectNoMoreCallbacks() {
	Expect(len(tc.activeCalls)).To(BeZero())
	Expect(len(tc.inactiveCalls)).To(BeZero())
	Expect(len(tc.egressUpdateKeys)).To(BeZero())
	Expect(len(tc.egressUpdateIDs)).To(BeZero())
}
