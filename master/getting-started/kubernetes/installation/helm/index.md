---
title: Installing Tigera Secure EE using Helm
---

{% capture chart_version_name %}{{ site.data.versions[page.version].first.title }}{% endcapture %}

This article describes how to install and configure Tigera Secure EE using Helm. After completing the steps you will have a functioning Tigera Secure EE cluster.

## Before you begin

Ensure that you have the following:

- [credentials for the Tigera private registry]({{ site.basuerl }}/{{ page.version }}/getting-started/#obtain-the-private-registry-credentials), (`config.json`)
- A [license key]({{ site.baseurl }}/{{ page.version }}/getting-started/#obtain-a-license-key) (`license.yaml`)
- Tiller is running, and your local helm CLI tool is configured to speak to it.

The high-level steps to a functioning cluster with access to the user interface are:

- [Step 1: Acquire the helm charts](#step-1-acquire-the-helm-charts)
- [Step 2: Create values.yaml for {{ site.prodname }} Core](#step-2-create-valuesyaml-for-tigera-secure-ee-core)
- [Step 3: Install {{ site.prodname }} Core](#step-3-install-tigera-secure-ee-core)
- [Step 4: Create values.yaml for {{ site.prodname }}](#step-4-create-valuesyaml-for-tigera-secure-ee)
- [Step 5: Install {{ site.prodname }}](#step-5-install-tigera-secure-ee)
- [Step 6: Grant access to user interface](#step-6-grant-access-to-user-interface)
- [Step 7: Log in to the Manager UI](#step-7-log-in-to-the-manager-ui)

## Step 1: Acquire the Helm charts

```
curl -O -L https://s3.amazonaws.com/tigera-public/ee/charts/tigera-secure-ee-core-{{ chart_version_name }}.tgz
curl -O -L https://s3.amazonaws.com/tigera-public/ee/charts/tigera-secure-ee-{{ chart_version_name }}.tgz
```

## Step 2: Create values.yaml for {{ site.prodname }} Core

In this step, you create a <my_values>.yaml file with your configuration values to build a running cluster.

For the purposes of this install guide, we will cover options which must be set in order to achieve a functioning cluster. For a full reference of all available options, inspect the helm chart:

    helm inspect tigera-secure-ee-core-{{ chart_version_name }}.tgz

### Configure your Datastore Connection

**Kubernetes Datastore**

```yaml
datastore: kubernetes
```

**Etcd datastore**

```yaml
datastore: etcd
etcd:
  endpoints: http://etcd.co
```

**Etcd secured by TLS**

Set the following flags to specify TLS certs to use when connecting to etcd:

```
--set-file etcd.tls.crt=./etcd.crt \
--set-file etcd.tls.ca=./etcd.ca \
--set-file etcd.tls.key=./etcd.key
```

### Network settings

>**Warning**: Changing any network settings in the `initialPool` block after installation will have no effect. For information on changing IP Pools after installation, see [configuring IP Pools]({{site.url}}/{{page.version}}/networking/changing-ip-pools)
{: .alert .alert-warning}

**Turn off IPIP**

By default, {{ site.prodname }} installs with IPIP encapsulation enabled. To disable it:

```yaml
initialPool:
  ipIpMode: None
```

**Use IPIP across subnets only**

```yaml
initialPool:
  ipIpMode: CrossSubnet
```

**Default Pool CIDR**

By default, {{ site.prodname }} creates an IPv4 Pool with CIDR `192.168.0.0/16` when it launches. To change this CIDR:

```yaml
initialPool:
  cidr: 10.0.0.0/8
```

>**Note**: This should fall within `--cluster-cidr` configured for the cluster
{: .alert .alert-info}

## Step 3: Install {{ site.prodname }} Core

1. Install the chart, passing in the `my-values.yaml` file you created from the previous section, an additionally passing your image pull secrets:

   ```
   helm install ./tigera-secure-ee-core-{{ chart_version_name }}.tgz \
     -f my-values.yaml \
     --set-file imagePullSecrets.cnx-pull-secret=./config.json
   ```

2. Wait for the 'cnx-apiserver' pod to become ready:

   ```
   kubectl rollout status -n kube-system deployment/cnx-apiserver
   ```

3. Install your {{ site.prodname }} license:

   ```
   kubectl apply -f ./license.yaml
   ```

4. Apply the following manifest to set network policy that secures access to {{ site.prodname }}:

   ```
   kubectl apply -f {{ site.url }}/{{ page.version }}/getting-started/kubernetes/installation/hosted/cnx/1.7/cnx-policy.yaml
   ```

Now that the **{{ site.prodname }} Core** chart is installed, please move on to the next step to install the **{{ site.prodname }}** chart.

## Step 4: Create values.yaml for {{ site.prodname }}

Before we install, we must build a helm values file to configure {{ site.prodname }} for your environment. We will refer to this values file as `my-values.yaml` at the time of installation.

For the purposes of this install guide, we will cover options which must be set in order to achieve a functioning cluster. For a full reference of all available options, inspect the helm chart:

    helm inspect tigera-secure-ee-{{ chart_version_name }}.tgz

### Connect to Elasticsearch & Kibana

By default, {{ site.prodname }} launches Elasticsearch Operator to bootstrap an unsecured elasticsearch cluster with kibana for demonstrative purposes. To disable this behavior and instead connect to your own elasticsearch & kibana, define the address in your yaml:

```yaml
elasticsearch:
  host: my.elasticsearch.co
  port: 9200
kibana:
  host: my.kibana.co
  port: 5601
```

Additionally, provide the CA and passwords for each of the roles:

```
--set-file elasticsearch.tls.ca=./elastic.ca \
--set elasticsearch.fluentd.password=$FLUENTD_PASSWORD \
--set elasticsearch.manager.password=$MANAGER_PASSWORD \
--set elasticsearch.curator.password=$CURATOR_PASSWORD \
--set elasticsearch.compliance.password=$COMPLIANCE_PASSWORD \
--set elasticsearch.intrusionDetection.password=$IDS_PASSWORD \
--set elasticsearch.elasticInstaller.password=$ELASTIC_INSTALLER_PASSWORD
```

For help setting up these roles in your Elasticsearch cluster, see  [Setting up Elasticsearch roles]({{site.baseurl}}/{{page.version}}/getting-started/kubernetes/installation/byo-elasticsearch#before-you-begin).

### Setting an Auth Type

**Basic auth**

```yaml
manager:
  auth:
    type: basic
```

**OIDC**

```yaml
manager:
  auth:
    type: oidc
    authority: "https://accounts.google.com"
    clientID: "<oidc-client-id>"
```

**Oauth**

```yaml
manager:
  auth:
    type: oauth
    authority: "https://<oauth-authority>/oauth/authorize"
    clientID: "cnx-manager"
```

## Step 5: Install {{ site.prodname }}

0. Pre-install the CRDs.

   Due to [a bug in helm](https://github.com/helm/helm/issues/4925), it is possible for the CRDs that are created by this chart to fail to get fully deployed before Helm attempts to create resources that require them. This affects all versions of Helm with a potential fix pending. In order to work around this issue when installing the chart you will need to make sure all CRDs exist in the cluster first:

   ```
   kubectl apply -f {{ site.url }}/{{ page.version }}/getting-started/kubernetes/installation/helm/tigera-secure-ee/operator-crds.yaml
   ```

   >[Click to view this manifest directly]({{ site.baseurl }}/{{ page.version }}/getting-started/kubernetes/installation/helm/tigera-secure-ee/operator-crds.yaml)

1. Install the tigera-secure-ee helm chart with custom resource provisioning disabled:

   ```
   helm install ./tigera-secure-ee-{{ chart_version_name }}.tgz \
     --namespace calico-monitoring \
     --set createCustomResources=false \
     --set-file imagePullSecrets.cnx-pull-secret=./config.json
   ```

   >Note: This version of the Tigera Secure EE Helm chart **must** be installed with `--namespace calico-monitoring`.

## Step 6: Grant access to user interface

Grant users permission to access the Tigera Secure EE Manager in your cluster. Run one of the following commands, replacing <USER> with the name of the user you wish to grant access.

**User manager**

The `tigera-manager-user`  role grants permission to use the Tigera Secure EE Manager UI, view flow logs, audit logs, and network statistics, and access the default policy tier.

```
kubectl create clusterrolebinding <USER>-tigera \
  --clusterrole=tigera-manager-user \
  --user=<USER>
```

**Network Admin**

The `network-admin` role grants permission to use the Tigera Secure EE Manager UI, view flow logs, audit logs, and network statistics, and administer all network policies and tiers.

```
kubectl create clusterrolebinding <USER>-network-admin \
  --clusterrole=network-admin \
  --user=<USER>
```

To grant access to additional tiers, or create your own roles, see the RBAC documentation.

## Step 7. Log in to the Manager UI

```
kubectl port-forward -n calico-monitoring svc/cnx-manager 9443
```

Sign in by navigating to https://localhost:9443 and login.

## Next steps

Consult the {{site.prodname}} for Kubernetes [demo](/{{page.version}}/security/simple-policy-cnx), which
demonstrates the main features.
