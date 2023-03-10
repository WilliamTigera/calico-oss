// Copyright (c) 2018-2021 Tigera, Inc. All rights reserved.

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

package policystore

import (
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/app-policy/types"
	"github.com/projectcalico/calico/felix/proto"
)

// DropActionOverride is an enumeration of the available values for the DropActionOverride
// configuration option.
type DropActionOverride int

const (
	ACCEPT DropActionOverride = iota
	DROP
	LOG_AND_ACCEPT
	LOG_AND_DROP
)

// PolicyStore is a data store that holds Calico policy information.
type PolicyStore struct {
	// route looker upper
	IPToIndexes types.IPToEndpointsIndex

	// Config settings
	DropActionOverride              DropActionOverride
	DataplaneStatsEnabledForAllowed bool
	DataplaneStatsEnabledForDenied  bool

	// Cache data
	Endpoint           *proto.WorkloadEndpoint
	Endpoints          map[proto.WorkloadEndpointID]*proto.WorkloadEndpoint
	PolicyByID         map[proto.PolicyID]*proto.Policy
	ProfileByID        map[proto.ProfileID]*proto.Profile
	IPSetByID          map[string]IPSet
	ServiceAccountByID map[proto.ServiceAccountID]*proto.ServiceAccountUpdate
	NamespaceByID      map[proto.NamespaceID]*proto.NamespaceUpdate

	// has this store seen inSync?
	InSync bool

	wepUpdates *workloadUpdateHandler
}

func NewPolicyStore() *PolicyStore {
	ps := &PolicyStore{
		IPToIndexes:        types.NewIPToEndpointsIndex(),
		DropActionOverride: DROP,
		Endpoints:          make(map[proto.WorkloadEndpointID]*proto.WorkloadEndpoint),
		IPSetByID:          make(map[string]IPSet),
		ProfileByID:        make(map[proto.ProfileID]*proto.Profile),
		PolicyByID:         make(map[proto.PolicyID]*proto.Policy),
		ServiceAccountByID: make(map[proto.ServiceAccountID]*proto.ServiceAccountUpdate),
		NamespaceByID:      make(map[proto.NamespaceID]*proto.NamespaceUpdate),

		wepUpdates: newWorkloadEndpointUpdateHandler(),
	}
	return ps
}

type policyStoreManager struct {
	current, pending *PolicyStore
	rl, wl           sync.Locker
	toActive         bool
}

type PolicyStoreManager interface {
	// PolicyStoreManager reads from a current or pending policy store if
	// syncher has an established and in-sync connection; or not, respectively.
	Read(func(*PolicyStore))
	// PolicyStoreManager writes to a current or pending policy store if
	// syncher has an established and in-sync connection; or not, respectively.
	Write(func(*PolicyStore))

	// tells PSM of syncher state 'connection lost; reestablishing until inSync encountered'
	OnReconnecting()
	// tells PSM of syncher state 'connection (re-)established and in-sync'
	OnInSync()
}

type PolicyStoreManagerOption func(*policyStoreManager)

func WithLocker(mu sync.Locker) PolicyStoreManagerOption {
	return func(psm *policyStoreManager) {
		switch l := mu.(type) {
		case *sync.Mutex:
			psm.rl = l
			psm.wl = l
		case *sync.RWMutex:
			psm.rl = l.RLocker()
			psm.wl = l
		default:
			panic("unknown locker type")
		}
	}
}

func NewPolicyStoreManager() PolicyStoreManager {
	return NewPolicyStoreManagerWithOpts(WithLocker(&sync.Mutex{}))
}

func NewPolicyStoreManagerWithOpts(opts ...PolicyStoreManagerOption) *policyStoreManager {
	psm := &policyStoreManager{
		current: NewPolicyStore(),
		pending: NewPolicyStore(),
	}
	for _, o := range opts {
		o(psm)
	}
	return psm
}

func (m *policyStoreManager) Read(cb func(*PolicyStore)) {
	m.rl.Lock()
	defer m.rl.Unlock()
	log.Debugf("storeManager reading from current store at %p", m.current)

	cb(m.current)
}

func (m *policyStoreManager) Write(cb func(*PolicyStore)) {
	m.wl.Lock()
	defer m.wl.Unlock()
	if m.toActive {
		log.Debugf("storeManager writing to current store at %p", m.current)
		cb(m.current)
		return
	}

	log.Debugf("storeManager writing to pending store at %p", m.current)
	cb(m.pending)
}

// OnReconnecting - PSM creates a pending store and starts writing to it
func (m *policyStoreManager) OnReconnecting() {
	m.wl.Lock()
	defer m.wl.Unlock()

	// create store
	m.pending = NewPolicyStore()

	// route next writes to pending
	m.toActive = false
}

func (m *policyStoreManager) OnInSync() {
	m.wl.Lock()
	defer m.wl.Unlock()

	if m.toActive {
		// we're already in-sync..
		// exit this routine so we don't cause a swap in case
		// insync is called more than once
		return
	}
	// swap pending to active
	m.current = m.pending
	m.pending = nil
	// route next writes to active
	m.toActive = true
}
