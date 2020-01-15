---
title: Installing Calico Enterprise for policy only
canonical_url: https://docs.tigera.io/v2.3/getting-started/kubernetes/installation/other
---

## Overview

You can use {{site.prodname}} just for policy enforcement and achieve networking
with another solution, such as:

- [Amazon Web Services (AWS) VPC CNI plugin]({{site.baseurl}}/reference/public-cloud/aws#using-aws-networking)
  (recommended for those on AWS who wish to [federate clusters]({{site.baseurl}}/networking/federation/index))
- Static routes
- Kubernetes cloud provider integration

## Before you begin

- Ensure that you have a Kubernetes cluster that meets the {{site.prodname}}
  [system requirements]({{site.baseurl}}/getting-started/kubernetes/requirements) and can [network](#overview). If you don't, follow the steps in
  [Using kubeadm to create a cluster](http://kubernetes.io/docs/getting-started-guides/kubeadm/).

- Ensure that you have the [credentials for the Tigera private registry]({{site.baseurl}}/getting-started/#obtain-the-private-registry-credentials)
  and a [license key]({{site.baseurl}}/getting-started/#obtain-a-license-key).

{% include content/load-docker.md yaml="calico" orchestrator="kubernetes" %}

{% include content/pull-secret.md %}

## <a name="install-cnx"></a>Installing {{site.prodname}} for policy only

### About installing for policy only

The installation procedure differs according to whether or not you want to
[federate clusters]({{site.baseurl}}/networking/federation/index). Refer to the section that matches your
configuration.

- [Without federation](#install-ee-typha-nofed)

- [With federation](#install-ee-fed)

> **Important**: At this time, we include steps for Kubernetes API datastore only. Should you wish
> to install {{site.prodname}} for policy only using the etcd datastore type, contact Tigera support.
{: .alert .alert-danger}

### <a name="install-ee-typha-nofed"></a>Installing {{site.prodname}} for policy only without federation

1. Ensure that the Kubernetes controller manager has the following flags
   set: <br>
   `--cluster-cidr={your pod CIDR}` and `--allocate-node-cidrs=true`.

   > **Tip**: On kubeadm, you can pass `--pod-network-cidr={your pod CIDR}`
   > to kubeadm to set both Kubernetes controller flags.
   {: .alert .alert-success}

1. Download the {{site.prodname}} policy-only manifest for the Kubernetes API datastore that matches your
   networking method.

   - **AWS VPC CNI plugin**
     ```bash
     curl -o calico.yaml \
     {{ "/getting-started/kubernetes/installation/hosted/kubernetes-datastore/policy-only-ecs/1.7/calico-typha.yaml" | absolute_url }} \
     -O
     ```

   - **All others**
     ```bash
     curl -o calico.yaml \
     {{ "/getting-started/kubernetes/installation/hosted/kubernetes-datastore/policy-only/1.7/calico-typha.yaml" | absolute_url }} \
     -O
     ```

{% include content/cnx-cred-sed.md yaml="calico" %}

{% include content/config-typha.md %}

1. Apply the manifest using the following command.

   ```bash
   kubectl apply -f calico.yaml
   ```

1. Continue to [Installing the {{site.prodname}} API Server](#installing-the-{{site.prodnamedash}}-api-server)

### <a name="install-ee-fed"></a>Installing {{site.prodname}} for policy only with federation

The following procedure describes how to install {{site.prodname}} on a single cluster that uses the
Kubernetes API datastore.

**Prerequisite**: Complete the steps in [Creating kubeconfig files]({{site.baseurl}}/networking/federation/kubeconfig)
for each [remote cluster]({{site.baseurl}}/networking/federation/index#terminology). Ensure that the
[local cluster]({{site.baseurl}}/networking/federation/index#terminology) can access all of the necessary `kubeconfig` files.

1. Access the local cluster using a `kubeconfig` with administrative privileges.

1. Create a secret containing the `kubeconfig` files for all of the remote clusters that
   the local cluster should federate with. A command to achieve this follows. Adjust the `--from-file`
   flags to include all of the kubeconfig files you created in [Creating kubeconfig files]({{site.baseurl}}/networking/federation/kubeconfig).

   > **Tip**: We recommend naming this secret `tigera-federation-remotecluster` as shown below to
   > make the rest of the procedure easier to follow.
   {: .alert .alert-success}

   ```bash
   kubectl create secret generic tigera-federation-remotecluster \
   --from-file=kubeconfig-rem-cluster-1 --from-file=kubeconfig-rem-cluster-2 \
   --namespace=kube-system
   ```

1. Ensure that the Kubernetes controller manager has the following flags set:<br>
   `--cluster-cidr=<cidr>`: Ensure that the `<cidr>` value matches or includes the IPV4 pool
   (`CALICO_IPV4POOL_CIDR`) in the manifest and does not overlap with the IPV4 pools of any other
   federated clusters. Example: `--cluster-cidr=192.168.0.0/16 --allocate-node-cidrs=true`

   > **Tip**: On kubeadm, you can pass `--pod-network-cidr=<cidr>`
   > to kubeadm to set both Kubernetes controller flags.
   {: .alert .alert-success}

1. Download the {{site.prodname}} policy-only manifest for the Kubernetes API datastore that matches your
   networking method.

   - **AWS VPC CNI plugin**
     ```bash
     curl -o calico.yaml \
     {{ "/getting-started/kubernetes/installation/hosted/kubernetes-datastore/policy-only-ecs/1.7/calico-federation.yaml" | absolute_url }} \
     -O
     ```

   - **All others**
     ```bash
     curl -o calico.yaml \
     {{ "/getting-started/kubernetes/installation/hosted/kubernetes-datastore/policy-only/1.7/calico-federation.yaml" | absolute_url }} \
     -O
     ```

{% include content/cnx-cred-sed.md yaml="calico" %}

{% include content/config-typha.md %}

1. Apply the manifest using the following command.

   ```bash
   kubectl apply -f calico.yaml
   ```

1. Continue to [Installing the {{site.prodname}} API Server](#installing-the-{{site.prodnamedash}}-api-server)

{% include content/cnx-api-install.md init="kubernetes" net="other" %}

1. Continue to [Applying your license key](#applying-your-license-key).

{% include content/apply-license.md cli="kubectl" %}

{% include content/cnx-monitor-install.md elasticsearch="operator" type="policy-only" %}

1. Continue to [Installing the {{site.prodname}} Manager](#installing-the-{{site.prodnamedash}}-manager)

{% include content/cnx-mgr-install.md init="kubernetes" net="other" %}

{% include content/gs-next-steps.md %}
