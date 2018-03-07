// Copyright (c) 2017-2018 Tigera, Inc. All rights reserved.

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

package migrator

import (
	gnet "net"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/net"
	"github.com/projectcalico/libcalico-go/lib/upgrade/converters"
	"github.com/projectcalico/libcalico-go/lib/upgrade/migrator/clients"
)

var _ = Describe("UT for checking the version for migration.", func() {

	DescribeTable("Checking canMigrate.",
		func(ver string, result bool, resultHasError bool) {
			yes, err := versionRequiresMigration(ver)

			if resultHasError {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(yes).To(Equal(result))
			}
		},

		Entry("Expect v2.6.5 to migrate", "v2.6.5", true, false),
		Entry("Expect v2.6.5-rc1 to migrate", "v2.6.5-rc1", true, false),
		Entry("Expect v2.6.5-15-g0986c6cd to migrate", "v2.6.5-15-g0986c6cd", true, false),
		Entry("Expect v2.6.6 to migrate", "v2.6.6", true, false),
		Entry("Expect v2.6.6-rc1 to migrate", "v2.6.6-rc1", true, false),
		Entry("Expect v2.7.0 to migrate", "v2.7.0", true, false),
		Entry("Expect v2.7.0-rc1 to migrate", "v2.7.0-rc1", true, false),
		Entry("Expect v2.6.4 to not migrate", "v2.6.4", false, true),
		Entry("Expect v2.6.4-rc1 to not migrate", "v2.6.4-rc1", false, true),
		Entry("Expect v2.6.4-15-g0986c6cd to not migrate", "v2.6.4-15-g0986c6cd", false, true),
		Entry("Expect v2.6.x-deadbeef to not migrate", "v2.6.x-deadbeef", false, true),
		Entry("Expect v3.0 to not migrate", "v3.0", false, true),
		Entry("Expect v3.0.0 to not migrate", "v3.0.0", false, false),
		Entry("Expect v3.0.0-beta1 to not migrate", "v3.0.0-beta1", false, false),
		Entry("Expect v3.0.0-0 to not migrate", "v3.0.0-0", false, false),
		Entry("Expect v3.0.0-a to not migrate", "v3.0.0-a", false, false),
		Entry("Expect v3.0.0-beta1-128-g1caef47d to not migrate", "v3.0.0-beta1-128-g1caef47d", false, false),
		Entry("Expect master to not migrate", "master", false, true),
		Entry("Expect empty string to not migrate", "", false, true),
		Entry("Expect garbage to not migrate", "garbage", false, true),
		Entry("Expect 1.2.3.4.5 to not migrate", "1.2.3.4.5", false, true),
	)
})

func convertAndCheckResourcesConverted(client clients.V1ClientInterface, expectedConversionCount int) {
	// Convert the data back to a set of resources.
	mh := &migrationHelper{clientv1: client}
	data, err := mh.queryAndConvertResources()
	Expect(err).NotTo(HaveOccurred())
	Expect(data.ConversionErrors).To(HaveLen(0))
	By("Checking total conversion")
	Expect(data.Resources).To(HaveLen(expectedConversionCount))
}

var _ = Describe("Test OpenStack migration filters", func() {

	wk := model.WorkloadEndpointKey{
		Hostname:       "ahost",
		OrchestratorID: "orchestrator",
		WorkloadID:     "wkid",
		EndpointID:     "endID",
	}
	mac, _ := gnet.ParseMAC("ee:ee:ee:ee:ee:ee")
	wv := model.WorkloadEndpoint{
		State:            "Running",
		Name:             "wepName",
		ActiveInstanceID: "wepActInstID",
		Mac:              &net.MAC{mac},
		ProfileIDs:       []string{"wepProfIDs"},
		IPv4Nets:         []net.IPNet{},
		IPv6Nets:         []net.IPNet{},
	}

	It("should not filter WorkloadEndpoints without openstack as OrchestratorID", func() {
		clientv1 := fakeClientV1{
			kvps: []*model.KVPair{
				&model.KVPair{
					Key:   wk,
					Value: &wv,
				},
			},
		}

		convertAndCheckResourcesConverted(clientv1, 1)
	})

	It("should filter WorkloadEndpoints with openstack as OrchestratorID", func() {
		wepOSKey := wk
		wepOSKey.OrchestratorID = v3.OrchestratorOpenStack
		clientv1 := fakeClientV1{
			kvps: []*model.KVPair{
				&model.KVPair{
					Key:   wepOSKey,
					Value: &wv,
				},
			},
		}

		convertAndCheckResourcesConverted(clientv1, 0)
	})

	It("should not filter Profiles without openstack-sg prefix", func() {
		clientv1 := fakeClientV1{
			kvps: []*model.KVPair{
				&model.KVPair{
					Key: model.ProfileKey{
						Name: "profilename",
					},
					Value: &model.Profile{
						Rules: model.ProfileRules{
							InboundRules: []model.Rule{converters.V1ModelInRule1},
						},
						Tags:   []string{},
						Labels: map[string]string{"label1": "value1"},
					},
				},
			},
		}

		convertAndCheckResourcesConverted(clientv1, 1)
	})
	It("should filter Profiles with openstack-sg- prefix", func() {
		clientv1 := fakeClientV1{
			kvps: []*model.KVPair{
				&model.KVPair{
					Key: model.ProfileKey{
						Name: "openstack-sg-profilename",
					},
					Value: &model.Profile{
						Rules: model.ProfileRules{
							InboundRules: []model.Rule{converters.V1ModelInRule1},
						},
						Tags:   []string{},
						Labels: map[string]string{"label1": "value1"},
					},
				},
			},
		}

		convertAndCheckResourcesConverted(clientv1, 0)
	})
})
