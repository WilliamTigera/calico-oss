// Copyright (c) 2017-2021 Tigera, Inc. All rights reserved.

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

package felixsyncer

import (
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/libcalico-go/lib/apiconfig"
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/libcalico-go/lib/backend/k8s"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/backend/syncersv1/remotecluster"
	"github.com/projectcalico/libcalico-go/lib/backend/syncersv1/updateprocessors"
	"github.com/projectcalico/libcalico-go/lib/backend/watchersyncer"
)

const (
	calicoClientID = "calico"
	k8sClientID    = "ks"
)

// New creates a new Felix v1 Syncer.
func New(calicoClient api.Client, cfg apiconfig.CalicoAPIConfigSpec, callbacks api.SyncerCallbacks, includeServices bool, isLeader bool) api.Syncer {

	// Always include the Calico client.
	clients := map[string]api.Client{
		calicoClientID: calicoClient,
	}
	k8sClientSet := k8s.BestEffortGetKubernetesClientSet(calicoClient, &cfg)

	// Felix always needs ClusterInformation and FelixConfiguration resources.
	resourceTypes := []watchersyncer.ResourceType{
		{
			ListInterface:   model.ResourceListOptions{Kind: apiv3.KindClusterInformation},
			UpdateProcessor: updateprocessors.NewClusterInfoUpdateProcessor(),
			ClientID:        calicoClientID, // This is backed by the calico client
		},
		{
			ListInterface:   model.ResourceListOptions{Kind: apiv3.KindLicenseKey},
			UpdateProcessor: updateprocessors.NewLicenseKeyUpdateProcessor(),
			ClientID:        calicoClientID, // This is backed by the calico client
		},
		{
			ListInterface:   model.ResourceListOptions{Kind: apiv3.KindFelixConfiguration},
			UpdateProcessor: updateprocessors.NewFelixConfigUpdateProcessor(),
			ClientID:        calicoClientID, // This is backed by the calico client
		},
	}

	if isLeader {
		// These resources are only required if this is the active Felix instance on the node.
		additionalTypes := []watchersyncer.ResourceType{
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindGlobalNetworkPolicy},
				UpdateProcessor: updateprocessors.NewGlobalNetworkPolicyUpdateProcessor(),
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindStagedGlobalNetworkPolicy},
				UpdateProcessor: updateprocessors.NewStagedGlobalNetworkPolicyUpdateProcessor(),
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindGlobalNetworkSet},
				UpdateProcessor: updateprocessors.NewGlobalNetworkSetUpdateProcessor(),
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindIPPool},
				UpdateProcessor: updateprocessors.NewIPPoolUpdateProcessor(),
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindNode},
				UpdateProcessor: updateprocessors.NewFelixNodeUpdateProcessor(cfg.K8sUsePodCIDR),
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindProfile},
				UpdateProcessor: updateprocessors.NewProfileUpdateProcessor(),
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindWorkloadEndpoint},
				UpdateProcessor: updateprocessors.NewWorkloadEndpointUpdateProcessor(),
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindNetworkPolicy},
				UpdateProcessor: updateprocessors.NewNetworkPolicyUpdateProcessor(),
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindStagedNetworkPolicy},
				UpdateProcessor: updateprocessors.NewStagedNetworkPolicyUpdateProcessor(),
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindStagedKubernetesNetworkPolicy},
				UpdateProcessor: updateprocessors.NewStagedKubernetesNetworkPolicyUpdateProcessor(),
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindNetworkSet},
				UpdateProcessor: updateprocessors.NewNetworkSetUpdateProcessor(),
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindTier},
				UpdateProcessor: updateprocessors.NewTierUpdateProcessor(),
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindHostEndpoint},
				UpdateProcessor: updateprocessors.NewHostEndpointUpdateProcessor(),
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindRemoteClusterConfiguration},
				UpdateProcessor: nil,            // No need to process the updates so pass nil
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindPacketCapture},
				UpdateProcessor: nil,            // No need to process the updates so pass nil
				ClientID:        calicoClientID, // This is backed by the calico client
			},
			{
				ListInterface: model.ResourceListOptions{Kind: apiv3.KindBGPConfiguration},
				ClientID:      calicoClientID, // This is backed by the calico client
			},
		}
		resourceTypes = append(resourceTypes, additionalTypes...)

		// If running in kdd mode, also watch Kubernetes network policies directly.
		// We don't need this in etcd mode, since kube-controllers copies k8s policies into etcd.
		if cfg.DatastoreType == apiconfig.Kubernetes {
			additionalTypes = append(additionalTypes, watchersyncer.ResourceType{
				ListInterface:   model.ResourceListOptions{Kind: model.KindKubernetesNetworkPolicy},
				UpdateProcessor: updateprocessors.NewNetworkPolicyUpdateProcessor(),
			})
		}

		// If using Calico IPAM, include IPAM resources the felix cares about.
		if !cfg.K8sUsePodCIDR {
			additionalTypes := []watchersyncer.ResourceType{{
				ListInterface:   model.BlockListOptions{},
				UpdateProcessor: nil,
				ClientID:        calicoClientID, // This is backed by the calico client
			}}
			resourceTypes = append(resourceTypes, additionalTypes...)
		}

		if includeServices && k8sClientSet != nil {
			// We have a k8s clientset so we can also include services and endpoints in our sync'd data.  We'll use a
			// special k8s wrapped client for this (which is a calico API wrapped k8s API).
			clients[k8sClientID] = k8s.NewK8sResourceWrapperClient(k8sClientSet)
			additionalTypes = []watchersyncer.ResourceType{{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindK8sService},
				UpdateProcessor: nil,         // No need to process the updates so pass nil
				ClientID:        k8sClientID, // This is backed by the kubernetes wrapped client
			}}
			/* Future: Include k8s endpoints for service categorization from LB IP direct to endpoint.
			{
				ListInterface:   model.ResourceListOptions{Kind: apiv3.KindK8sEndpoints},
				UpdateProcessor: nil,         // No need to process the updates so pass nil
				ClientID:        k8sClientID, // This is backed by the kubernetes wrapped client
			}
			*/
			resourceTypes = append(resourceTypes, additionalTypes...)
		}
	}

	// The "main" watchersyncer will spawn additional watchersyncers for any remote clusters that are found.
	// The callbacks are wrapped to allow the messages to be intercepted so that the additional watchersyncers can be spawned.
	return watchersyncer.NewMultiClient(
		clients,
		resourceTypes,
		remotecluster.NewWrappedCallbacks(callbacks, k8sClientSet, felixRemoteClusterProcessor{}),
	)
}

// felixRemoteClusterProcessor provides the Felix syncer specific remote cluster processing.
type felixRemoteClusterProcessor struct{}

func (_ felixRemoteClusterProcessor) CreateResourceTypes() []watchersyncer.ResourceType {
	return []watchersyncer.ResourceType{
		{
			ListInterface:   model.ResourceListOptions{Kind: apiv3.KindWorkloadEndpoint},
			UpdateProcessor: updateprocessors.NewWorkloadEndpointUpdateProcessor(),
		},
		{
			ListInterface:   model.ResourceListOptions{Kind: apiv3.KindHostEndpoint},
			UpdateProcessor: updateprocessors.NewHostEndpointUpdateProcessor(),
		},
		{
			ListInterface:   model.ResourceListOptions{Kind: apiv3.KindProfile},
			UpdateProcessor: updateprocessors.NewProfileUpdateProcessor(),
		},
	}
}

func (_ felixRemoteClusterProcessor) ConvertUpdates(clusterName string, updates []api.Update) (propagatedUpdates []api.Update) {
	for i, update := range updates {
		if update.UpdateType == api.UpdateTypeKVUpdated || update.UpdateType == api.UpdateTypeKVNew {
			switch t := update.Key.(type) {
			default:
				log.Warnf("unexpected type %T\n", t)
			case model.HostEndpointKey:
				t.Hostname = clusterName + "/" + t.Hostname
				updates[i].Key = t
				for profileIndex, profile := range updates[i].Value.(*model.HostEndpoint).ProfileIDs {
					updates[i].Value.(*model.HostEndpoint).ProfileIDs[profileIndex] = clusterName + "/" + profile
				}
			case model.WorkloadEndpointKey:
				t.Hostname = clusterName + "/" + t.Hostname
				updates[i].Key = t
				for profileIndex, profile := range updates[i].Value.(*model.WorkloadEndpoint).ProfileIDs {
					updates[i].Value.(*model.WorkloadEndpoint).ProfileIDs[profileIndex] = clusterName + "/" + profile
				}
			case model.ProfileRulesKey:
				t.Name = clusterName + "/" + t.Name
				updates[i].Value.(*model.ProfileRules).InboundRules = []model.Rule{}
				updates[i].Value.(*model.ProfileRules).OutboundRules = []model.Rule{}
				updates[i].Key = t
			case model.ProfileLabelsKey:
				t.Name = clusterName + "/" + t.Name
				updates[i].Key = t
			case model.ProfileTagsKey:
				t.Name = clusterName + "/" + t.Name
				updates[i].Key = t
			case model.ResourceKey:
				if t.Kind == apiv3.KindProfile {
					// Suppress the v3 Profile resource that the
					// ProfileUpdateProcessor emits.  We only stream v3 Profile,
					// in addition to the v1 Profile keys above, for its
					// EgressGateway field, and there's no reason to federate
					// that field between clusters.
					continue
				}
				// As and when we need to federate v3 resources, we'll need to
				// decide whether and how to incorporate the remote cluster name.
				// We may not be able to prefix similarly as for v1, because that
				// could be confused with v3 namespacing.
				log.Panic("No prefixing design yet for federated v3 resources")
			}
		} else if update.UpdateType == api.UpdateTypeKVDeleted {
			switch t := update.Key.(type) {
			default:
				log.Warnf("unexpected type %T\n", t)
			case model.HostEndpointKey:
				t.Hostname = clusterName + "/" + t.Hostname
				updates[i].Key = t
			case model.WorkloadEndpointKey:
				t.Hostname = clusterName + "/" + t.Hostname
				updates[i].Key = t
			case model.ProfileRulesKey:
				t.Name = clusterName + "/" + t.Name
				updates[i].Key = t
			case model.ProfileLabelsKey:
				t.Name = clusterName + "/" + t.Name
				updates[i].Key = t
			case model.ProfileTagsKey:
				t.Name = clusterName + "/" + t.Name
				updates[i].Key = t
			case model.ResourceKey:
				// See comments for model.ResourceKey just above.
				if t.Kind == apiv3.KindProfile {
					continue
				}
				log.Panic("No prefixing design yet for federated v3 resources")
			}
		}
		propagatedUpdates = append(propagatedUpdates, updates[i])
	}

	return
}

func (_ felixRemoteClusterProcessor) GetCalicoAPIConfig(config *apiv3.RemoteClusterConfiguration) *apiconfig.CalicoAPIConfig {
	datastoreConfig := apiconfig.NewCalicoAPIConfig()
	datastoreConfig.Spec.DatastoreType = apiconfig.DatastoreType(config.Spec.DatastoreType)
	switch datastoreConfig.Spec.DatastoreType {
	case apiconfig.EtcdV3:
		datastoreConfig.Spec.EtcdEndpoints = config.Spec.EtcdEndpoints
		datastoreConfig.Spec.EtcdUsername = config.Spec.EtcdUsername
		datastoreConfig.Spec.EtcdPassword = config.Spec.EtcdPassword
		datastoreConfig.Spec.EtcdKeyFile = config.Spec.EtcdKeyFile
		datastoreConfig.Spec.EtcdCertFile = config.Spec.EtcdCertFile
		datastoreConfig.Spec.EtcdCACertFile = config.Spec.EtcdCACertFile
		datastoreConfig.Spec.EtcdKey = config.Spec.EtcdKey
		datastoreConfig.Spec.EtcdCert = config.Spec.EtcdCert
		datastoreConfig.Spec.EtcdCACert = config.Spec.EtcdCACert
		return datastoreConfig
	case apiconfig.Kubernetes:
		datastoreConfig.Spec.Kubeconfig = config.Spec.Kubeconfig
		datastoreConfig.Spec.K8sAPIEndpoint = config.Spec.K8sAPIEndpoint
		datastoreConfig.Spec.K8sKeyFile = config.Spec.K8sKeyFile
		datastoreConfig.Spec.K8sCertFile = config.Spec.K8sCertFile
		datastoreConfig.Spec.K8sCAFile = config.Spec.K8sCAFile
		datastoreConfig.Spec.K8sAPIToken = config.Spec.K8sAPIToken
		datastoreConfig.Spec.K8sInsecureSkipTLSVerify = config.Spec.K8sInsecureSkipTLSVerify
		datastoreConfig.Spec.KubeconfigInline = config.Spec.KubeconfigInline
		return datastoreConfig
	}
	return nil
}
