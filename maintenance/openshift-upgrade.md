---
title: Upgrade Calico Enterprise from an earlier release on OpenShift
description: Upgrading from an earlier release of Calico Enterprise on OpenShift.
canonical_url: /maintenance/openshift-upgrade
show_toc: false
openshift_manifests_ignore_pullsecret: true
openshift_manifests_ignore_installation_cr: true
---

## Prerequisites

Ensure that your {{site.prodname}} OpenShift cluster is running OpenShift Container Platform 
{{site.ocpMinVersion}} or {{site.ocpMaxVersion}}, and the {{site.prodname}} operator version is v1.2.4 or greater.

**Note**: You can check if you are running the operator by checking for the existence of the operator namespace
with `oc get ns tigera-operator` or issuing `oc get tigerastatus`; a successful return means your installation is
using the operator.
{: .alert .alert-info}

### Prepare your cluster for the upgrade

During upgrade, the {{site.prodname}} LogStorage CR is temporarily removed so Elasticsearch can be upgraded. Features
that depend on LogStorage are temporarily unavailable, including dashboards in the Manager UI. Data ingestion is paused
temporarily, but resumes when the LogStorage is up and running again.

To retain data from your current installation (optional), ensure that the currently mounted persistent volumes
have their reclaim policy set to [retain data](https://kubernetes.io/docs/tasks/administer-cluster/change-pv-reclaim-policy/){:target="_blank"}.
Data retention is recommended only for users that have a valid Elasticsearch license. (Trial licenses can be invalidated
during upgrade).

If your cluster has Windows nodes and uses custom TLS certificates for log storage, prior to upgrade, prepare and apply new certificates for [log storage]({{site.baseurl}}/security/comms/log-storage-tls) that include the required service DNS names.

For {{site.prodname}} v3.5 and v3.7, upgrading multi-cluster management setups must include updating all managed and management clusters.

### Download the new manifests

Make a manifests directory.

```bash
mkdir manifests
```

{% include content/openshift-manifests.md %}

## Upgrade from 3.0 or later
**Note**: The steps differ based on your cluster type. If you are unsure of your cluster type, look at the field `clusterManagementType` when you run `oc get installation -o yaml` before you proceed.
{: .alert .alert-info}

1. Apply the updated manifests.
   ```bash
   oc apply -f manifests/
   ```

1. {% include content/openshift-prometheus-operator.md %}

1. If your cluster is a management cluster, apply a [ManagementCluster]({{site.baseurl}}/reference/installation/api#operator.tigera.io/v1.ManagementCluster)
   CR to your cluster.
   ```bash
   oc apply -f - <<EOF
   apiVersion: operator.tigera.io/v1
   kind: ManagementCluster
   metadata:
     name: tigera-secure
   EOF
   ```

1. If your cluster is v3.7 or older, apply a new [Monitor]({{site.baseurl}}/reference/installation/api#operator.tigera.io/v1.Monitor)
   CR to your cluster.

   ```bash
   oc apply -f - <<EOF
   apiVersion: operator.tigera.io/v1
   kind: Monitor
   metadata:
     name: tigera-secure
   EOF
   ```

1. Wait until the `apiserver` shows a status of `Available`, then proceed to the next section. You can monitor progress with the following command:

   ```bash
   watch oc get tigerastatus/apiserver
   ```

1. If your cluster is management or standalone cluster using v3.8 or older, follow these steps:
   
   a. Install the network policies to secure {{site.prodname}} component communications with ElasticSearch

   ```bash
   kubectl apply -f {{ "/manifests/ocp/tigera-policies-es-access.yaml" | absolute_url }}
   ```

   b. Wait until all components of tigerastatus shows a status of `Available`, then proceed to the next section. You can monitor progress with the following 

   ```bash
   watch kubectl get tigerastatus
   ```

1. To secure the components which make up {{site.prodname}}, install the following set of network policies.

   If your cluster is a **managed** cluster, apply this manifest.
   
   ```bash
   oc apply -f {{ "/manifests/ocp/tigera-policies-managed.yaml" | absolute_url }}
   ```
   
   For other clusters, use this manifest.
   
   ```bash
   oc apply -f {{ "/manifests/ocp/tigera-policies.yaml" | absolute_url }}
   ```

1. You can now monitor the upgrade progress with the following command:
   ```bash
   watch oc get tigerastatus
   ```

     **Note**: If there are any problems you can use `kubectl get tigerastatus -o yaml` to get more details.
     {: .alert .alert-info}

1. Remove unused policies in your cluster.

   If your cluster is a **managed** cluster, run this command:

   ```bash
   kubectl delete -f {{ "/manifests/default-tier-policies-managed.yaml" | absolute_url }}
   ```

   For other clusters, run this command:

   ```bash
   kubectl delete -f {{ "/manifests/default-tier-policies.yaml" | absolute_url }}
   ```
