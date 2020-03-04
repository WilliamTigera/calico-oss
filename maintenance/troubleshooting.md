---
title: Troubleshooting
description: View logs and diagnostics, common issues, and where to report issues in github.
canonical_url: '/maintenance/troubleshooting'
---

* TOC
{:toc}

### Logs and diagnostics

To collect diagnostics, download and install the `kubectl-calico` plugin somewhere in your `$PATH`. For example:

```
sudo curl -o /usr/local/bin/kubectl-calico {{site.url}}/{{page.version}}/maintenance/kubectl-calico
```

Then, create a diagnostics bundle.

```
kubectl calico diags
```

To report a problem, contact Tigera Support.

### Alert diagnostics

The developer console can provide diagnostic information if you
have an issue with your alerts and need to contact Support. Please
execute the following queries and provide the output along with your
alert definition.

```
GET _watcher/watch/tigera_secure_ee.<cluster_name>.<alert_name>
POST _watcher/watch/tigera_secure_ee.<cluster_name>.<alert_name>/_execute
{
  "action_modes": {
    "index_events": "force_simulate"
  }
}
```

### Containers do not have network connectivity

#### Check for mismatched node names

If you notice that a workload has not received network connectivity, check
that the node name for that host is properly configured. The name for the [node resource](../reference/resources/node) must match
the node name in the [workload endpoint resources](../reference/resources/workloadendpoint) on that host. If the names are mismatched,
it is likely that all workloads on that node will not receive networking.

To check this, query one of the broken workload endpoints and check its node name:

	calicoctl get workloadendpoints -n <namespace>

Then, check to see if a single corresponding node resource exists:

	calicoctl get nodes

If the node resource either does not exist or there are multiple node resources representing the bad node, it is likely that the node's hostname has changed. This often happens
as a result of switching a node's hostname between its FQDN and its short DNS name.

To correct this, you must perform the following steps (with examples shown using Kubernetes):

1. Prevent new workloads from being scheduled on the bad node.
```
kubectl cordon mynode.internal.projectcalico.org
```
1. Drain all workloads from the node.
```
kubectl drain mynode.internal.projectcalico.org --ignore-daemonsets
```
1. On the bad node, set the hostname to the desired value.
```
sudo hostnamectl set-hostname <desired-hostname>
```
1. Delete the bad node configuration from {{site.prodname}}.
```
calicoctl delete node <name-of-bad-node>
```
1. Restart {{site.nodecontainer}} on the bad node to pick up the changes.
```
kubectl delete pod -n kube-system <name-of-calico-pod>
```
1. Reenable scheduling of worklods on the node.
```
kubectl uncordon mynode.internal.projectcalico.org
```

To prevent this problem from occurring, we recommend always mounting the `/var/lib/calico` directory into the `{{site.nodecontainer}}`
container when installing {{site.prodname}}. This allows all components to detect and use the same node name. See
[node name determination](../reference/node/configuration#node-name-determination) for more information.

#### Check BGP peer status

If you have connectivity between containers on the same host, and between
containers and the Internet, but not between containers on different hosts, it
probably indicates a problem in your BGP configuration.

Look at `calicoctl node status` on each host.  It should include output like this:

```
Calico process is running.

IPv4 BGP status
+--------------+-------------------+-------+----------+-------------+
| PEER ADDRESS |     PEER TYPE     | STATE |  SINCE   |    INFO     |
+--------------+-------------------+-------+----------+-------------+
| 172.17.8.102 | node-to-node mesh | up    | 23:30:04 | Established |
+--------------+-------------------+-------+----------+-------------+

IPv6 BGP status
No IPv6 peers found.
```
{: .no-select-button}

If you do not see this, please check the following.

- Make sure there is IP connectivity between your hosts.

- Make sure your network allows the requisite BGP traffic on TCP port 179.

#### Configure NetworkManager

Configure [NetworkManager](https://help.ubuntu.com/community/NetworkManager) before
attempting to use {{site.prodname}} networking.

NetworkManager manipulates the routing table for interfaces in the default network
namespace where {{site.prodname}} veth pairs are anchored for connections to containers.
This can interfere with the {{site.prodname}} agent's ability to route correctly.

The procedure for configuring NetworkManager to ignore {{site.prodname}} interfaces
varies by Linux distribution. The following steps work best on Ubuntu systems.

1. Create the following configuration file at `/etc/NetworkManager/conf.d/calico.conf`.

   ```
   [keyfile]
   unmanaged-devices=interface-name:cali*;interface-name:tunl*
   ```

1. Restart NetworkManager.

   ```bash
   sudo service network-manager stop
   sudo service network-manager start
   ```

1. Install {{site.prodname}}.

1. Check the interfaces that NetworkManager ignores.

   ```bash
   nmcli dev status
   ```

   It should return output indicating that the `cali` and `tunl` interfaces
   are `unmanaged`.

   If this does not to prevent NetworkManager from interfering with {{site.prodname}} networking, try disabling NetworkManager. If disabling NetworkManager does not stop it from interfering with {{site.prodname}} networking, you may need to remove NetworkManager. This will require manual network configuration.


### Errors when running sudo calicoctl

If you use `sudo` for commands like `calicoctl node run`, remember that your environment variables will not be transferred to the `sudo` environment.  You can run `sudo` with the `-E` flag to include your environment variables:

```shell
sudo -E calicoctl node run
```

or you can set environment variables for `sudo` commands like this:

```shell
sudo ETCD_ENDPOINTS=http://172.25.0.1:2379 calicoctl node run
```

Also be aware that connection information can be specified as a config file rather than using environment variables.  See [Installing calicoctl](../getting-started/calicoctl/install)
for details.

### Error: {{site.nodecontainer}} is not ready: BIRD is not ready: BGP not established with 10.0.0.1

In most cases, this "unready" status error in Kubernetes means that a particular peer is unreachable in the cluster. Check that BGP connectivity between the two peers is allowed in the environment.

This error can also occur if inactive Node resources are configured for node-to-node mesh. To fix this, [decommission the stale nodes](../maintenance/decommissioning-a-node).

This error can also occur when BGP connections to non-mesh peers go down. If this is a common occurrence in your BGP topology, you can disable BIRD readiness checks. See [node readiness]({{site.baseurl}}/reference/node/configuration#node-readiness)
for more information.

### Linux conntrack table is out of space

A common problem on Linux systems is running out of space in the conntrack table, which can cause poor iptables performance. This can
happen if you run a lot of workloads on a given host, or if your workloads create a lot of TCP connections or bidirectional UDP streams. To avoid this problem, we recommend increasing the conntrack table size using the following commands:

    sysctl -w net.netfilter.nf_conntrack_max=1000000
    echo "net.netfilter.nf_conntrack_max=1000000" >> /etc/sysctl.conf

### Compliance report is not generating at expected time

By design, reports are scheduled to generate 30 minutes after the specified end time. The reason for this is to allow a certain amount of
time to pass for all the relevant data within the specified start and end time to be fully processed and stored. This delay can be modified
by setting the `TIGERA_COMPLIANCE_JOB_START_DELAY` environment variable on the `compliance-controller` deployment to the
desired [Golang duration](https://godoc.org/time#Duration).

### GlobalAlert reports error "Trying to create too many buckets"

```
“Trying to create too many buckets. Must be less than or equal to: [10000] but was [10001]. This limit can be set by changing the [search.max_buckets] cluster level setting."
```

The GlobalAlert system has a hard limit of 10000 aggregation keys per
query, and will fail to generate generate alerts if nested aggregations
result in the number of keys exceeding this limit.  The “healthy“ status
of the GlobalAlert will be set to false until the number of aggregation
keys returned by the query no longer exceeds this limit.

Careful selection of queries and `aggregateBy` keys will mitigate this issue.
GlobalAlerts should consider the size of the keyspace used in the
`aggregateBy` field and order from least expansive to most. For example:
Namespace should precede pod name. Avoid aggregating by source or destination
port unless the query selects specific ports. Ephemeral ports used by clients
number in the tens of thousands and a single host can trigger this condition.
