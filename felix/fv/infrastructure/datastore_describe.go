// Copyright (c) 2018-2019 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package infrastructure

import (
	"fmt"
	"os"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/projectcalico/calico/libcalico-go/lib/apiconfig"
	"github.com/projectcalico/calico/libcalico-go/lib/set"
)

type InfraFactory func(...CreateOption) DatastoreInfra

// DatastoreDescribe is a replacement for ginkgo.Describe which invokes Describe
// multiple times for one or more different datastore drivers - passing in the
// function to retrieve the appropriate datastore infrastructure.  This allows
// easy construction of end-to-end tests covering multiple different datastore
// drivers.
//
// The *datastores* parameter is a slice of the DatastoreTypes to test.
func DatastoreDescribe(description string, datastores []apiconfig.DatastoreType, body func(InfraFactory)) bool {
	if len(datastores) > 1 {
		// Enterprise only supports KDD so skip running etcd tests if this test also runs
		// on KDD.
		var filtered []apiconfig.DatastoreType
		for _, d := range datastores {
			if d == apiconfig.EtcdV3 {
				continue
			}
			filtered = append(filtered, d)
		}
		datastores = filtered
	}

	for _, ds := range datastores {
		Describe(fmt.Sprintf("%s (%s backend)", description, ds), func() {
			var coreFilesAtStart set.Set[string]
			BeforeEach(func() {
				coreFilesAtStart = readCoreFiles()
			})

			switch ds {
			case apiconfig.EtcdV3:
				body(createEtcdDatastoreInfra)
			case apiconfig.Kubernetes:
				body(createK8sDatastoreInfra)
			default:
				panic(fmt.Errorf("Unknown DatastoreType, %s", ds))
			}

			AfterEach(func() {
				afterCoreFiles := readCoreFiles()
				coreFilesAtStart.Iter(func(item string) error {
					afterCoreFiles.Discard(item)
					return nil
				})
				if afterCoreFiles.Len() != 0 {
					if CurrentGinkgoTestDescription().Failed {
						Fail(fmt.Sprintf("Test FAILED and new core files were detected during tear-down: %v.  "+
							"Felix must have panicked during the test.", afterCoreFiles.Slice()))
						return
					}
					Fail(fmt.Sprintf("Test PASSED but new core files were detected during tear-down: %v.  "+
						"Felix must have panicked during the test.", afterCoreFiles.Slice()))
				}
			})
		})
	}

	return true
}

type LocalRemoteInfraFactories struct {
	Local  InfraFactory
	Remote InfraFactory
}

func (r *LocalRemoteInfraFactories) IsRemoteSetup() bool {
	return r.Remote != nil
}

func (r *LocalRemoteInfraFactories) AllFactories() []InfraFactory {
	factories := []InfraFactory{r.Local}
	if r.IsRemoteSetup() {
		factories = append(factories, r.Remote)
	}
	return factories
}

// DatastoreDescribeWithRemote is similar to DatastoreDescribe. It invokes Describe for the provided datastores, providing
// just a local datastore driver. However, it also invokes Describe for supported remote scenarios, providing both a local
// and remote datastore drivers. Currently, the only remote scenario is local kubernetes and remote kubernetes.
func DatastoreDescribeWithRemote(description string, localDatastores []apiconfig.DatastoreType, body func(factories LocalRemoteInfraFactories)) bool {
	if len(localDatastores) > 1 {
		// Enterprise only supports KDD so skip running etcd tests if this test also runs
		// on KDD.
		var filtered []apiconfig.DatastoreType
		for _, d := range localDatastores {
			if d == apiconfig.EtcdV3 {
				continue
			}
			filtered = append(filtered, d)
		}
		localDatastores = filtered
	}

	for _, ds := range localDatastores {
		Describe(fmt.Sprintf("%s (%s backend)", description, ds), func() {
			var coreFilesAtStart set.Set[string]
			BeforeEach(func() {
				coreFilesAtStart = readCoreFiles()
			})

			switch ds {
			case apiconfig.EtcdV3:
				body(LocalRemoteInfraFactories{Local: createEtcdDatastoreInfra})
			case apiconfig.Kubernetes:
				body(LocalRemoteInfraFactories{Local: createK8sDatastoreInfra})
			default:
				panic(fmt.Errorf("Unknown DatastoreType, %s", ds))
			}

			AfterEach(func() {
				afterCoreFiles := readCoreFiles()
				coreFilesAtStart.Iter(func(item string) error {
					afterCoreFiles.Discard(item)
					return nil
				})
				if afterCoreFiles.Len() != 0 {
					if CurrentGinkgoTestDescription().Failed {
						Fail(fmt.Sprintf("Test FAILED and new core files were detected during tear-down: %v.  "+
							"Felix must have panicked during the test.", afterCoreFiles.Slice()))
						return
					}
					Fail(fmt.Sprintf("Test PASSED but new core files were detected during tear-down: %v.  "+
						"Felix must have panicked during the test.", afterCoreFiles.Slice()))
				}
			})
		})
	}

	DatastoreDescribeRemoteOnly(description, body)

	return true
}

// DatastoreDescribeRemoteOnly invokes Describe, providing a factory that provides remote and local datastores. It creates just
// one Describe invocation - use DatastoreDescribeWithRemote to get both local and local/remote describes.
func DatastoreDescribeRemoteOnly(description string, body func(factories LocalRemoteInfraFactories)) bool {
	Describe(fmt.Sprintf("%s (local kubernetes, remote kubernetes)", description),
		func() {
			body(LocalRemoteInfraFactories{Local: createK8sDatastoreInfra, Remote: createRemoteK8sDatastoreInfra})
		})

	return true
}

func readCoreFiles() set.Set[string] {
	tmpFiles, err := os.ReadDir("/tmp")
	Expect(err).NotTo(HaveOccurred())
	var coreFiles []string
	for _, f := range tmpFiles {
		if strings.HasPrefix(f.Name(), "core_felix-") {
			coreFiles = append(coreFiles, f.Name())
		}
	}
	return set.From(coreFiles...)
}
