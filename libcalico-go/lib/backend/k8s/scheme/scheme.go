// Copyright (c) 2025 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package scheme

import (
	"sync"

	log "github.com/sirupsen/logrus"
	apiv3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"

	libapiv3 "github.com/projectcalico/calico/libcalico-go/lib/apis/v3"
)

var addToSchemeOnce sync.Once

func AddCalicoResourcesToScheme() {
	addToSchemeOnce.Do(func() {
		// We also need to register resources.
		schemeBuilder := runtime.NewSchemeBuilder(
			func(scheme *runtime.Scheme) error {
				scheme.AddKnownTypes(
					schema.GroupVersion{
						Group:   "crd.projectcalico.org",
						Version: "v1",
					},
					&apiv3.FelixConfiguration{},
					&apiv3.FelixConfigurationList{},
					&apiv3.IPPool{},
					&apiv3.IPPoolList{},
					&apiv3.IPReservation{},
					&apiv3.IPReservationList{},
					&apiv3.BGPPeer{},
					&apiv3.BGPPeerList{},
					&apiv3.BGPConfiguration{},
					&apiv3.BGPConfigurationList{},
					&apiv3.ClusterInformation{},
					&apiv3.ClusterInformationList{},
					&apiv3.LicenseKey{},
					&apiv3.LicenseKeyList{},
					&apiv3.GlobalNetworkSet{},
					&apiv3.GlobalNetworkSetList{},
					&apiv3.NetworkSet{},
					&apiv3.NetworkSetList{},
					&apiv3.GlobalNetworkPolicy{},
					&apiv3.GlobalNetworkPolicyList{},
					&apiv3.StagedGlobalNetworkPolicy{},
					&apiv3.StagedGlobalNetworkPolicyList{},
					&apiv3.NetworkPolicy{},
					&apiv3.NetworkPolicyList{},
					&apiv3.StagedNetworkPolicy{},
					&apiv3.StagedNetworkPolicyList{},
					&apiv3.StagedKubernetesNetworkPolicy{},
					&apiv3.StagedKubernetesNetworkPolicyList{},
					&apiv3.PolicyRecommendationScope{},
					&apiv3.PolicyRecommendationScopeList{},
					&apiv3.Tier{},
					&apiv3.TierList{},
					&apiv3.HostEndpoint{},
					&apiv3.HostEndpointList{},
					&apiv3.RemoteClusterConfiguration{},
					&apiv3.RemoteClusterConfigurationList{},
					&libapiv3.BlockAffinity{},
					&libapiv3.BlockAffinityList{},
					&libapiv3.IPAMBlock{},
					&libapiv3.IPAMBlockList{},
					&libapiv3.IPAMHandle{},
					&libapiv3.IPAMHandleList{},
					&libapiv3.IPAMConfig{},
					&libapiv3.IPAMConfigList{},
					&apiv3.KubeControllersConfiguration{},
					&apiv3.KubeControllersConfigurationList{},
					&apiv3.AlertException{},
					&apiv3.AlertExceptionList{},
					&apiv3.GlobalAlert{},
					&apiv3.GlobalAlertList{},
					&apiv3.GlobalAlertTemplate{},
					&apiv3.GlobalAlertTemplateList{},
					&apiv3.GlobalThreatFeed{},
					&apiv3.GlobalThreatFeedList{},
					&apiv3.GlobalReport{},
					&apiv3.GlobalReportList{},
					&apiv3.GlobalReportType{},
					&apiv3.GlobalReportTypeList{},
					&apiv3.ManagedCluster{},
					&apiv3.ManagedClusterList{},
					&apiv3.PacketCapture{},
					&apiv3.PacketCaptureList{},
					&apiv3.DeepPacketInspection{},
					&apiv3.DeepPacketInspectionList{},
					&apiv3.UISettingsGroup{},
					&apiv3.UISettingsGroupList{},
					&apiv3.UISettings{},
					&apiv3.UISettingsList{},
					&apiv3.CalicoNodeStatus{},
					&apiv3.CalicoNodeStatusList{},
					&apiv3.BGPFilter{},
					&apiv3.BGPFilterList{},
					&apiv3.ExternalNetwork{},
					&apiv3.ExternalNetworkList{},
					&apiv3.EgressGatewayPolicy{},
					&apiv3.EgressGatewayPolicyList{},
					&apiv3.SecurityEventWebhook{},
					&apiv3.SecurityEventWebhookList{},
					&apiv3.BFDConfiguration{},
					&apiv3.BFDConfigurationList{},
				)
				return nil
			})

		err := schemeBuilder.AddToScheme(scheme.Scheme)
		if err != nil {
			log.WithError(err).Fatal("failed to add calico resources to scheme")
		}
		metav1.AddToGroupVersion(scheme.Scheme, schema.GroupVersion{Group: "crd.projectcalico.org", Version: "v1"})
	})
}
