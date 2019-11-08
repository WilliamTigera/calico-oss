
# MCM Installation Guide

Setup for Multi-Cluster Management is now tailored for TSEE v2.6 using Operator. You will need to follow up the steps below to setup a management or a managed cluster on top of standalone cluster.

Note: [How to Manage Multiple Terraforms](#how-to-manage-multiple-terraforms).

## Standalone Cluster

This step is required to install TSEE as a standalone cluster. You can follow [the quick start guide] (https://docs.tigera.io/master/getting-started/kubernetes/)

## Management Cluster

This step is required to enable multi cluster mode on a standalone TSEE cluster. A standalone already has Voltron installed. All you need to do is to enable the multi cluster mode that allows an user to create managed clusters.

*This step is optional
Voltron needs an accessible IP to accept tunnels. The script will pick the internal ip of the master node. If you wish to change this, add the following environment variable VOLTRON_PUBLIC_IP to cnx-manager-proxy container.*

Next run the following script to enable a multi cluster mode.
Please follow the [setup-voltron-mgmt-cluster.bash](/install-scripts/setup-voltron-mgmt-cluster.bash).

## Managed Clusters

Create a managed cluster via CNX manager UI and apply the manifest for guardian.
This step is required to enable multi cluster client mode on a standalone TSEE cluster.

Please follow the [setup-guardian-app-cluster.bash](/install-scripts/setup-guardian-app-cluster.bash).

## How to Manage Multiple Terraforms

If your git clone of CRC was used to bring a cluster up, how do you bring another up?

1. git clone another clone of https://github.com/tigera/calico-ready-clusters
1. use `git worktree add ...` on your existing clone -- [inspiration](https://spin.atomicobject.com/2016/06/26/parallelize-development-git-worktrees/)
1. use `terraform workspace` [details](https://www.terraform.io/docs/state/workspaces.html)

```
terraform workspace new <clustername>
terraform apply -var prefix=<username-clustername>
cp master_ssh_key <clustername>-master_ssh
cp admin.conf <clustername>-admin.conf
```

