---
title: Upgrading Calico Enterprise from an earlier release using Helm
canonical_url: https://docs.tigera.io/master/maintenance/helm-upgrade-tsee
---

## Prerequisites

Ensure that your Kubernetes cluster is already running version 2.4.2 of
{{site.prodname}} and that {{site.prodname}} was installed using helm charts.
Upgrades from earlier versions to {{page.version}} is not supported.
Upgrades from install methods other than helm charts is also not supported.

## Preparing your cluster for the helm upgrade

1. Delete the install Job from the previous {{site.prodname}} install, if it exists
   (Kubernetes Jobs cannot be modified, they must be deleted and re-created).
   ```bash
   kubectl delete -n calico-monitoring job elastic-tsee-installer
   ```

## Upgrading to {{page.version}} {{site.prodname}} from a previous Helm install

If your {{site.prodname}} was previously installed using helm, use the following
steps to upgrade.

> **Note**: The following instructions assume that Tiller is still installed on
> your cluster.
>
> **Note**: For additional helm documentation, please refer to our
> [**helm installation docs**]({{site.url}}/{{page.version}}/reference/other-install-methods/kubernetes/installation/helm/).
{: .alert .alert-info}

1. Find the helm installation names. We will use these names in the following
   upgrade steps.
   ```bash
   helm list
   ```

   Your output should look like the following:
   ```bash
   NAME                    REVISION        UPDATED                         STATUS          CHART                  APP VERSION     NAMESPACE
   coiled-bat              1               Fri Jul 19 13:44:37 2019        DEPLOYED        tigera-secure-ee-core-                 default
   fashionable-anteater    1               Fri Jul 19 14:28:50 2019        DEPLOYED        tigera-secure-ee-
   ```

1. Run the Helm upgrade command for `tigera-secure-ee-core`
   ```bash
   helm upgrade <helm installation name for tigera-secure-ee-core> tigera-secure-ee-core-{% include chart_version_name %}.tgz
   ```

1. Run the Helm upgrade command for `tigera-secure-ee`
   ```bash
   helm upgrade <helm installation name for tigera-secure-ee> tigera-secure-ee-{% include chart_version_name %}.tgz
   ```
