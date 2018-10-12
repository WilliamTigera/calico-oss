---
title: Quickstart for Tigera Secure EE on Kubernetes
redirect_from: latest/getting-started/kubernetes/
canonical_url: https://docs.tigera.io/v2.2/getting-started/kubernetes/
---


### Overview

This quickstart gets you a single-host Kubernetes cluster with {{site.prodname}}
in approximately 10 minutes. You can use this cluster for testing and development.

To deploy a cluster suitable for production, refer to [Installation](/{{page.version}}/getting-started/kubernetes/installation/).


### Host requirements

- AMD64 processor
- 2CPU
- 7.5GB RAM
- 20GB free disk space
- Ubuntu Server 16.04
- [jq](https://stedolan.github.io/jq/download/){:target="_blank"}
- Internet access
- [Sufficient virtual memory](https://www.elastic.co/guide/en/elasticsearch/reference/current/vm-max-map-count.html){:target="_blank"}


### Before you begin

- Ensure that you have the following files in your current working directory:
  - [`config.json` containing the Tigera private registry credentials](/{{page.version}}/getting-started/#obtain-the-private-registry-credentials)
  - [`<customer-name>-license.yaml` containing your license key](/{{page.version}}/getting-started/#obtain-a-license-key)
<br><br>

- [Follow the Kubernetes instructions to install kubeadm](https://kubernetes.io/docs/setup/independent/install-kubeadm/){:target="_blank"}.

> **Note**: After installing kubeadm, do not power down or restart
the host. Instead, continue directly to the
[next section to create your cluster](#create-a-single-host-kubernetes-cluster).
{: .alert .alert-info}

### Create a single-host Kubernetes cluster

1. As a regular user with sudo privileges, open a terminal on the host that
   you installed kubeadm on.

1. Initialize the master using the following command.

   ```bash
   sudo kubeadm init --pod-network-cidr=192.168.0.0/16 \
   --apiserver-cert-extra-sans=127.0.0.1
   ```

1. Execute the commands to configure kubectl as returned by
   `kubeadm init`. Most likely they will be as follows:

   ```bash
   mkdir -p $HOME/.kube
   sudo cp -i /etc/kubernetes/admin.conf $HOME/.kube/config
   sudo chown $(id -u):$(id -g) $HOME/.kube/config
   ```
1. Download the installation script.

   ```bash
   curl --compressed \
   {{site.url}}/{{page.version}}/getting-started/kubernetes/install-cnx.sh -O
   ```

1. Set the `install-cnx.sh` file to be executable.

   ```bash
   chmod +x install-cnx.sh
   ```
1. Use the following command to execute the script, replacing `<customer-name>`
   with your customer name.

   **Command**
   ```
   ./install-cnx.sh -l <customer-name>-license.yaml
   ```

   **Example**
   ```
   ./install-cnx.sh -l awesome-corp-license.yaml
   ```

1. Launch a browser and type `https://127.0.0.1:30003` in the address bar.

   > **Note**: Your browser will warn you of an insecure connection due to
   > the self-signed certificate. Click past this warning to access the
   > {{site.prodname}} Manager.
   {: .alert .alert-info}

1. Type **jane** in the **Login** box and **welc0me** in the **Password** box.
   Then click **Sign In**.

Congratulations! You now have a single-host Kubernetes cluster
equipped with {{site.prodname}}.

### Next steps
**[Experiment with OIDC authentication strategy](/{{page.version}}/reference/cnx/authentication)**

**[Experiment with non-admin users and the {{site.prodname}} manager](/{{page.version}}/reference/cnx/rbac-tiered-policies)**

**[Secure a simple application using the Kubernetes `NetworkPolicy` API](tutorials/simple-policy)**

**[Control ingress and egress traffic using the Kubernetes `NetworkPolicy` API](tutorials/advanced-policy)**

**[Create a user interface that shows blocked and allowed connections in real time](tutorials/stars-policy/)**

**[Install and configure calicoctl](/{{page.version}}/usage/calicoctl/install)**
