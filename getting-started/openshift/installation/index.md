---
title: Installing Calico Enterprise on OpenShift v4.x
description: Install Calico Enterprise on an OpenShift v4 cluster.
---

### Big picture

Install an OpenShift v4 cluster with {{site.prodname}}.

### Value

Augments the applicable steps in the [OpenShift documentation](https://cloud.redhat.com/openshift/install)
to install {{site.prodname}}.

### How to

#### Before you begin

- Ensure that your environment meets the {{site.prodname}} [system requirements]({{site.baseurl}}/getting-started/openshift/requirements).

- Ensure that you have the [private registry credentials]({{site.baseurl}}/getting-started/calico-enterprise#obtain-the-private-registry-credentials)
  and a [license key]({{site.baseurl}}/getting-started/calico-enterprise#obtain-a-license-key).

- **If installing on AWS**, ensure that you have [configured an AWS account](https://docs.openshift.com/container-platform/4.1/installing/installing_aws/installing-aws-account.html) appropriate for OpenShift v4,
  and have [set up your AWS credentials](https://docs.aws.amazon.com/sdk-for-java/v1/developer-guide/setup-credentials.html).
  Note that the OpenShift installer supports a subset of [AWS regions](https://docs.openshift.com/container-platform/4.1/installing/installing_aws/installing-aws-account.html#installation-aws-regions_installing-aws-account).

- Ensure that you have a [RedHat account](https://cloud.redhat.com/). A RedHat account is required to obtain the pull secret necessary to provision an OpenShift cluster.

- Ensure that you have installed the OpenShift installer **v4.2 or later** and OpenShift command line interface from [cloud.redhat.com](https://cloud.redhat.com/openshift/install/aws/installer-provisioned).

- Ensure that you have [generated a local SSH private key](https://docs.openshift.com/container-platform/4.1/installing/installing_aws/installing-aws-default.html#ssh-agent-using_installing-aws-default) and have added it to your ssh-agent

> **Note**: OpenShift v4.2 installation currently only supports {{site.prodname}} images pulled from quay.io
{: .alert .alert-info}

#### Create a configuration file for the OpenShift installer

First, create a staging directory for the installation. This directory will contain the configuration file, along with cluster state files, that OpenShift installer will create:

```
mkdir openshift-tigera-install && cd openshift-tigera-install
```

Now run OpenShift installer to create a default configuration file:

```
openshift-install create install-config
```

> **Note**: Refer to the OpenShift installer documentation found on [https://cloud.redhat.com/openshift/install](https://cloud.redhat.com/openshift/install) for more information
> about the installer and any configuration changes required for your platform.
{: .alert .alert-info}

Once the installer has finished, your staging directory will contain the configuration file `install-config.yaml`.

#### Update the configuration file to use {{site.prodname}}

Override the OpenShift networking to use Calico and update the AWS instance types to meet the [system requirements]({{site.baseurl}}/getting-started/openshift/requirements):

```bash
sed -i 's/OpenShiftSDN/Calico/' install-config.yaml
sed -i 's/platform: {}/platform:\n    aws:\n      type: m4.xlarge/g' install-config.yaml
```

#### Generate the install manifests

Now generate the Kubernetes manifests using your configuration file:

```bash
openshift-install create manifests
```

Download the {{site.prodname}} manifests for OpenShift and add them to the generated manifests directory:

```bash
curl {{ "/manifests/ocp/crds/01-crd-alertmanager.yaml" | absolute_url }} -o manifests/01-crd-alertmanager.yaml
curl {{ "/manifests/ocp/crds/01-crd-apiserver.yaml" | absolute_url }} -o manifests/01-crd-apiserver.yaml
curl {{ "/manifests/ocp/crds/01-crd-compliance.yaml" | absolute_url }} -o manifests/01-crd-compliance.yaml
curl {{ "/manifests/ocp/crds/01-crd-manager.yaml" | absolute_url }} -o manifests/01-crd-manager.yaml
curl {{ "/manifests/ocp/crds/01-crd-eck-apmserver.yaml" | absolute_url }} -o manifests/01-crd-eck-apmserver.yaml
curl {{ "/manifests/ocp/crds/01-crd-eck-elasticsearch.yaml" | absolute_url }} -o manifests/01-crd-eck-elasticsearch.yaml
curl {{ "/manifests/ocp/crds/01-crd-eck-kibana.yaml" | absolute_url }} -o manifests/01-crd-eck-kibana.yaml
curl {{ "/manifests/ocp/crds/01-crd-eck-trustrelationship.yaml" | absolute_url }} -o manifests/01-crd-eck-trustrelationship.yaml
curl {{ "/manifests/ocp/crds/01-crd-installation.yaml" | absolute_url }} -o manifests/01-crd-installation.yaml
curl {{ "/manifests/ocp/crds/01-crd-intrusiondetection.yaml" | absolute_url }} -o manifests/01-crd-intrusiondetection.yaml
curl {{ "/manifests/ocp/crds/01-crd-logstorage.yaml" | absolute_url }} -o manifests/01-crd-logstorage.yaml
curl {{ "/manifests/ocp/crds/01-crd-logcollector.yaml" | absolute_url }} -o manifests/01-crd-logcollector.yaml
curl {{ "/manifests/ocp/crds/01-crd-prometheusrule.yaml" | absolute_url }} -o manifests/01-crd-prometheusrule.yaml
curl {{ "/manifests/ocp/crds/01-crd-prometheus.yaml" | absolute_url }} -o manifests/01-crd-prometheus.yaml
curl {{ "/manifests/ocp/crds/01-crd-servicemonitor.yaml" | absolute_url }} -o manifests/01-crd-servicemonitor.yaml
curl {{ "/manifests/ocp/crds/01-crd-tigerastatus.yaml" | absolute_url }} -o manifests/01-crd-tigerastatus.yaml
curl {{ "/manifests/ocp/crds/01-crd-managementclusterconnection.yaml" | absolute_url }} -o manifests/01-crd-managementclusterconnection.yaml
curl {{ "/manifests/ocp/tigera-operator/00-namespace-tigera-operator.yaml" | absolute_url }} -o manifests/00-namespace-tigera-operator.yaml
curl {{ "/manifests/ocp/tigera-operator/02-rolebinding-tigera-operator.yaml" | absolute_url }} -o manifests/02-rolebinding-tigera-operator.yaml
curl {{ "/manifests/ocp/tigera-operator/02-role-tigera-operator.yaml" | absolute_url }} -o manifests/02-role-tigera-operator.yaml
curl {{ "/manifests/ocp/tigera-operator/02-serviceaccount-tigera-operator.yaml" | absolute_url }} -o manifests/02-serviceaccount-tigera-operator.yaml
curl {{ "/manifests/ocp/tigera-operator/02-tigera-operator.yaml" | absolute_url }} -o manifests/02-tigera-operator.yaml
curl {{ "/manifests/ocp/misc/00-namespace-tigera-prometheus.yaml" | absolute_url }} -o manifests/00-namespace-tigera-prometheus.yaml
curl {{ "/manifests/ocp/prometheus-operator/04-clusterrolebinding-prometheus-operator.yaml" | absolute_url }} -o manifests/04-clusterrolebinding-prometheus-operator.yaml
curl {{ "/manifests/ocp/prometheus-operator/04-clusterrolebinding-prometheus.yaml" | absolute_url }} -o manifests/04-clusterrolebinding-prometheus.yaml
curl {{ "/manifests/ocp/prometheus-operator/04-clusterrole-prometheus-operator.yaml" | absolute_url }} -o manifests/04-clusterrole-prometheus-operator.yaml
curl {{ "/manifests/ocp/prometheus-operator/04-clusterrole-prometheus.yaml" | absolute_url }} -o manifests/04-clusterrole-prometheus.yaml
curl {{ "/manifests/ocp/prometheus-operator/04-deployment-prometheus-operator.yaml" | absolute_url }} -o manifests/04-deployment-prometheus-operator.yaml
curl {{ "/manifests/ocp/prometheus-operator/04-serviceaccount-prometheus-operator.yaml" | absolute_url }} -o manifests/04-serviceaccount-prometheus-operator.yaml
curl {{ "/manifests/ocp/prometheus-operator/04-serviceaccount-prometheus.yaml" | absolute_url }} -o manifests/04-serviceaccount-prometheus.yaml
curl {{ "/manifests/ocp/misc/99-alertmanager-secret.yaml" | absolute_url }} -o manifests/99-alertmanager-secret.yaml
curl {{ "/manifests/ocp/misc/99-alertmanager-service.yaml" | absolute_url }} -o manifests/99-alertmanager-service.yaml
curl {{ "/manifests/ocp/misc/99-prometheus-service.yaml" | absolute_url }} -o manifests/99-prometheus-service.yaml
curl {{ "/manifests/ocp/01-cr-installation.yaml" | absolute_url }} -o manifests/01-cr-installation.yaml
curl {{ "/manifests/ocp/01-cr-apiserver.yaml" | absolute_url }} -o manifests/01-cr-apiserver.yaml
curl {{ "/manifests/ocp/01-cr-manager.yaml" | absolute_url }} -o manifests/01-cr-manager.yaml
curl {{ "/manifests/ocp/01-cr-compliance.yaml" | absolute_url }} -o manifests/01-cr-compliance.yaml
curl {{ "/manifests/ocp/01-cr-intrusiondetection.yaml" | absolute_url }} -o manifests/01-cr-intrusiondetection.yaml
curl {{ "/manifests/ocp/01-cr-alertmanager.yaml" | absolute_url }} -o manifests/01-cr-alertmanager.yaml
curl {{ "/manifests/ocp/01-cr-logstorage.yaml" | absolute_url }} -o manifests/01-cr-logstorage.yaml
curl {{ "/manifests/ocp/01-cr-logcollector.yaml" | absolute_url }} -o manifests/01-cr-logcollector.yaml
curl {{ "/manifests/ocp/01-cr-prometheus.yaml" | absolute_url }} -o manifests/01-cr-prometheus.yaml
curl {{ "/manifests/ocp/01-cr-prometheusrule.yaml" | absolute_url }} -o manifests/01-cr-prometheusrule.yaml
curl {{ "/manifests/ocp/01-cr-servicemonitor.yaml" | absolute_url }} -o manifests/01-cr-servicemonitor.yaml
```

> **Note**: The Tigera operator manifest downloaded above includes an initialization container which configures Amazon AWS
> security groups for {{site.prodname}}. If not running on AWS, you should remove the init container from `manifests/02-tigera-operator.yaml`.
{: .alert .alert-info}

#### Add an image pull secret

1. Download the pull secret manifest template into the manifests directory.

   ```
   curl {{ "/manifests/ocp/02-pull-secret.yaml" | absolute_url }} -o manifests/02-pull-secret.yaml
   ```

1. Update the contents of the secret with the image pull secret provided to you by Tigera.

   For example, if the secret is located at `~/.docker/config.json`, run the following commands.

   ```bash
   SECRET=$(cat ~/.docker/config.json | tr -d '\n\r\t ' | base64 -w 0)
   sed -i "s/SECRET/${SECRET}/" manifests/02-pull-secret.yaml
   ```

#### Create the cluster

Start the cluster creation with the following command and wait for it to complete.

```bash
openshift-install create cluster
```

#### Create storage class

{{site.prodname}} requires storage for logs and reports. Before finishing the installation, you must [create a StorageClass for {{site.prodname}}]({{site.baseurl}}/getting-started/create-storage).

#### Install the {{site.prodname}} license

In order to use {{site.prodname}}, you must install the license provided to you by Tigera.
Before applying the license, wait until the Tigera API server is ready with the following command:

```
watch oc get tigerastatus
```

Wait until the `apiserver` shows a status of `Available`.

Once the Tigera API server is ready, apply the license:

```
oc create -f </path/to/license.yaml>
```

You can now monitor progress with the following command:

```
watch oc get tigerastatus
```

When it shows all components with status `Available`, proceed to the next section.

#### Secure {{site.prodname}} with network policy

To secure the components which make up {{site.prodname}}, install the following set of network policies.

```
oc create -f {{ "/manifests/tigera-policies.yaml" | absolute_url }}
```

### Above and beyond

- [Configure access to the manager UI]({{site.baseurl}}/getting-started/access-the-manager)
- [Get started with Kubernetes network policy]({{site.baseurl}}/security/kubernetes-network-policy)
- [Get started with Calico network policy]({{site.baseurl}}/security/calico-network-policy)
- [Enable default deny for Kubernetes pods]({{site.baseurl}}/security/kubernetes-default-deny)
