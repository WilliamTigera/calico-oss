// Copyright (c) 2019 Tigera, Inc. All rights reserved.

package projectcalico

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	calico "github.com/projectcalico/libcalico-go/lib/apis/v3"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NetworkPolicyList is a list of Policy objects.
type NetworkPolicyList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []NetworkPolicy
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type NetworkPolicy struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.NetworkPolicySpec
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// StagedKubernetesNetworkPolicyList is a list of Policy objects.
type StagedKubernetesNetworkPolicyList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []StagedKubernetesNetworkPolicy
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type StagedKubernetesNetworkPolicy struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.StagedKubernetesNetworkPolicySpec
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// StagedNetworkPolicyList is a list of Policy objects.
type StagedNetworkPolicyList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []StagedNetworkPolicy
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type StagedNetworkPolicy struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.StagedNetworkPolicySpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TierList is a list of Tier objects.
type TierList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []Tier
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Tier struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.TierSpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GlobalNetworkPolicyList is a list of Policy objects.
type GlobalNetworkPolicyList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []GlobalNetworkPolicy
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type GlobalNetworkPolicy struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.GlobalNetworkPolicySpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// StagedGlobalNetworkPolicyList is a list of Policy objects.
type StagedGlobalNetworkPolicyList struct {
	metav1.TypeMeta
	metav1.ListMeta
	Items []StagedGlobalNetworkPolicy
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type StagedGlobalNetworkPolicy struct {
	metav1.TypeMeta
	metav1.ObjectMeta
	Spec calico.StagedGlobalNetworkPolicySpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GlobalNetworkPolicyList is a list of Policy objects.
type GlobalNetworkSetList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []GlobalNetworkSet
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type GlobalNetworkSet struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.GlobalNetworkSetSpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NetworkPolicyList is a list of Policy objects.
type NetworkSetList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []NetworkSet
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type NetworkSet struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.NetworkSetSpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LicenseKeyList is a list of LicenseKey objects.
type LicenseKeyList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []LicenseKey
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type LicenseKey struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.LicenseKeySpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GlobalAlertList is a list of Policy objects.
type GlobalAlertList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []GlobalAlert
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status

type GlobalAlert struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   calico.GlobalAlertSpec
	Status calico.GlobalAlertStatus
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GlobalAlertTemplateList is a list of Policy objects.
type GlobalAlertTemplateList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []GlobalAlertTemplate
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status

type GlobalAlertTemplate struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.GlobalAlertSpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GlobalThreatFeedList is a list of Policy objects.
type GlobalThreatFeedList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []GlobalThreatFeed
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status

type GlobalThreatFeed struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   calico.GlobalThreatFeedSpec
	Status calico.GlobalThreatFeedStatus
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HostEndpointList is a list of Policy objects.
type HostEndpointList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []HostEndpoint
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type HostEndpoint struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.HostEndpointSpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GlobalReportList is a list of objects to generate compliance reports.
type GlobalReportList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []GlobalReport
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status

type GlobalReport struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   calico.ReportSpec
	Status calico.ReportStatus
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GlobalReportTypeList is a list of objects used by GlobalReports to define report template.
type GlobalReportTypeList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []GlobalReportType
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type GlobalReportType struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.ReportTypeSpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// IPPoolList contains a list of IPPool resources.
type IPPoolList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []IPPool
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type IPPool struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.IPPoolSpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// BGPConfigurationList is a list of BGPConfiguration objects.
type BGPConfigurationList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []BGPConfiguration
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type BGPConfiguration struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.BGPConfigurationSpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// BGPPeerList is a list of BGPPeer objects.
type BGPPeerList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []BGPPeer
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type BGPPeer struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.BGPPeerSpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ProfileList is a list of Profile objects.
type ProfileList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []Profile
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Profile struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.ProfileSpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// RemoteClusterConfigurationList is a list of RemoteClusterConfiguration objects.
type RemoteClusterConfigurationList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []RemoteClusterConfiguration
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type RemoteClusterConfiguration struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.RemoteClusterConfigurationSpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// FelixConfigurationList is a list of FelixConfiguration objects.
type FelixConfigurationList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []FelixConfiguration
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type FelixConfiguration struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.FelixConfigurationSpec
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ManagedClusterList is a list of ManagedCluster objects (used for multi-cluster management).
type ManagedClusterList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []ManagedCluster
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
type ManagedCluster struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   calico.ManagedClusterSpec
	Status calico.ManagedClusterStatus
}

// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterInformationList is a list of ClusterInformation objects.
type ClusterInformationList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []ClusterInformation
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ClusterInformation struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec calico.ClusterInformationSpec
}
