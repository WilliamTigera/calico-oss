// Copyright (c) 2019-2020 Tigera, Inc. All rights reserved.

package calico

import (
	"k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
	"k8s.io/klog"
)

// NewStorage creates a new libcalico-based storage.Interface implementation
func NewStorage(opts Options) (registry.DryRunnableStorage, factory.DestroyFunc) {
	klog.V(4).Infoln("Constructing Calico Storage")

	switch opts.RESTOptions.ResourcePrefix {
	case "projectcalico.org/networkpolicies":
		return NewNetworkPolicyStorage(opts)
	case "projectcalico.org/stagedkubernetesnetworkpolicies":
		return NewStagedKubernetesNetworkPolicyStorage(opts)
	case "projectcalico.org/stagednetworkpolicies":
		return NewStagedNetworkPolicyStorage(opts)
	case "projectcalico.org/tiers":
		return NewTierStorage(opts)
	case "projectcalico.org/globalnetworkpolicies":
		return NewGlobalNetworkPolicyStorage(opts)
	case "projectcalico.org/stagedglobalnetworkpolicies":
		return NewStagedGlobalNetworkPolicyStorage(opts)
	case "projectcalico.org/globalnetworksets":
		return NewGlobalNetworkSetStorage(opts)
	case "projectcalico.org/networksets":
		return NewNetworkSetStorage(opts)
	case "projectcalico.org/licensekeys":
		return NewLicenseKeyStorage(opts)
	case "projectcalico.org/globalalerts":
		return NewGlobalAlertStorage(opts)
	case "projectcalico.org/globalalerttemplates":
		return NewGlobalAlertTemplateStorage(opts)
	case "projectcalico.org/globalthreatfeeds":
		return NewGlobalThreatFeedStorage(opts)
	case "projectcalico.org/hostendpoints":
		return NewHostEndpointStorage(opts)
	case "projectcalico.org/globalreports":
		return NewGlobalReportStorage(opts)
	case "projectcalico.org/globalreporttypes":
		return NewGlobalReportTypeStorage(opts)
	case "projectcalico.org/ippools":
		return NewIPPoolStorage(opts)
	case "projectcalico.org/bgpconfigurations":
		return NewBGPConfigurationStorage(opts)
	case "projectcalico.org/bgppeers":
		return NewBGPPeerStorage(opts)
	case "projectcalico.org/profiles":
		return NewProfileStorage(opts)
	case "projectcalico.org/remoteclusterconfigurations":
		return NewRemoteClusterConfigurationStorage(opts)
	case "projectcalico.org/felixconfigurations":
		return NewFelixConfigurationStorage(opts)
	case "projectcalico.org/kubecontrollersconfigurations":
		return NewKubeControllersConfigurationStorage(opts)
	case "projectcalico.org/managedclusters":
		return NewManagedClusterStorage(opts)
	case "projectcalico.org/clusterinformations":
		return NewClusterInformationStorage(opts)
	case "projectcalico.org/packetcaptures":
		return NewPacketCaptureStorage(opts)
	default:
		klog.Fatalf("Unable to create storage for resource %v", opts.RESTOptions.ResourcePrefix)
		return registry.DryRunnableStorage{}, nil
	}
}
