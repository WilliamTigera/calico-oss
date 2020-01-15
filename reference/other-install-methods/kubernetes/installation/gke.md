---
title: Installing Calico Enterprise on Google GKE
canonical_url: /reference/other-install-methods/kubernetes/installation/other
---

## Overview

This guide covers installing {{site.prodname}} for policy enforcement on Google GKE.

## Before you begin

- Create a GKE cluster with the following settings:

  - Version `v1.14.5-gke.5` or greater.  At the time of writing, `v1.14.5-gke.5` is available through the "Rapid" [release channel](https://cloud.google.com/kubernetes-engine/docs/concepts/release-channels).  `v1.14.5-gke.5` includes a specific compatibility fix to enable {{site.prodname}}; {{site.prodname}} will fail to install on earlier versions.

  - [Intranode visibility](https://cloud.google.com/kubernetes-engine/docs/how-to/intranode-visibility) *enabled*.  This setting configures GKE to use the GKE CNI plugin, which is required.

  - Network policy *disabled*.  The Network Policy setting configures GKE to install Tigera Calico in a way that locks down its configuration, which prevents installation of {{site.prodname}}.

  - Istio *disabled*.  The Istio setting on the GKE cluster configures GKE to install Istio in a way that locks down its configuration, which prevents configuration of {{site.prodname}} application layer policy.  If you wish to use Istio in your cluster, follow [this GKE tutorial](https://cloud.google.com/kubernetes-engine/docs/tutorials/installing-istio), which explains how to install the open source version of Istio on GKE.

- The GKE master must be able to access the {{site.prodname}} API server, which runs with host networking on port 5443.  For multi-zone clusters and clusters with the "master IP range" configured, you will need to add a GCP firewall rule to allow access to that port from the master.  For example, you could add a network tag to your nodes and then refer to that tag in a firewall rule.  From the command line, this can be done by:

  - Passing `--tags allow-5443` to `gcloud cluster create` when creating the cluster.
  - Creating the firewall rule: `gcloud compute firewall-rules create allow-5443-fwr --target-tags allow-5443 --allow tcp:5443 --network default --source-ranges <master-ipv4-cidr-range>`.

  If you have already created your cluster and you are unable to add the tag, another alternative is to allow traffic that targets the cluster's node CIDR.

  If the master is not able to reach the API server then the step that applies the license key with `kubectl` will fail with an error:
  ```
  no matches for kind "LicenseKey" in version "projectcalico.org/v3"
  ```
  and
  ```
  kubectl describe apiservice v3.projectcalico.org
  ```
  will show a failed condition:
  ```
  Message:               no response from https://10.40.1.8:5443 ...
  Reason:                FailedDiscoveryCheck
  Status:                False
  Type:                  Available
  ```

- Ensure that your Google account has sufficient IAM permissions.  To apply the {{site.prodname}} manifests requires permissions to create Kubernetes ClusterRoles and ClusterRoleBindings.  The easiest way to grant such permissions is to assign the "Kubernetes Engine Developer" IAM role to your user account as described in the [Creating Cloud IAM policies](https://cloud.google.com/kubernetes-engine/docs/how-to/iam) section of the GKE documentation.

  > **Tip**: By default, GCP users often have permissions to create basic Kubernetes resources (such as Pods and Services) but lack the permissions to create ClusterRoles and other admin resources.  Even if you can create basic resources, it's worth double checking that you can create admin resources before continuing.
  {: .alert .alert-success}

- Ensure your cluster has sufficient RAM to install {{site.prodname}}.  The datastore and indexing components pre-allocate significant resources.  At least 10GB is required.

- Ensure that you have the [credentials for the Tigera private registry]({{site.baseurl}}/getting-started/calico-enterprise#obtain-the-private-registry-credentials)
  and a [license key]({{site.baseurl}}/getting-started/calico-enterprise#obtain-a-license-key).

- To follow the TLS certificate and key creation instructions below you'll need openssl.

{% include content/load-docker.md yaml="calico" orchestrator="kubernetes" %}

{% include content/pull-secret.md %}

### <a name="install-cnx"></a><a name="install-ee-typha-nofed"></a>Installing {{site.prodname}} without federation

1. Download the {{site.prodname}} policy-only manifest for the Kubernetes API datastore with GKE CNI plugin support.

   ```bash
   curl \
   {{ "/manifests/gke/calico-typha.yaml" | absolute_url }} \
   -o calico.yaml
   ```

{% include content/cnx-cred-sed.md yaml="calico" %}

{% include content/config-typha.md autoscale="true" %}

1. Apply the manifest using the following command.

   ```bash
   kubectl apply -f calico.yaml
   ```

1. Continue to [Installing the {{site.prodname}} API Server](#installing-the-{{site.prodnamedash}}-api-server)

{% include content/cnx-api-install.md init="kubernetes" net="other" platform="gke" %}

1. Continue to [Applying your license key](#applying-your-license-key).

{% include content/apply-license.md platform="gke" cli="kubectl" %}

{% include content/cnx-monitor-install.md elasticsearch="operator" platform="gke" %}

1. Continue to [Installing the {{site.prodname}} Manager](#installing-the-{{site.prodnamedash}}-manager)

{% include content/cnx-mgr-install.md init="kubernetes" net="other" platform="gke" %}

{% include content/gs-next-steps.md %}
