---
title: Tigera Secure EE key and path prefixes in etcd v3
redirect_from: latest/reference/advanced/etcd-rbac/calico-etcdv3-paths
canonical_url: https://docs.tigera.io/v2.2/reference/advanced/etcd-rbac/calico-etcdv3-paths
---

The paths listed here are the key or path prefixes that a particular {{site.prodname}}
component needs access to in etcd to function successfully.

> **Note**: The path prefixes listed here may change in the future and at that point anything
> referencing them (like etcd roles) would need to be updated appropriately.
{: .alert .alert-info}


## {{site.nodecontainer}}

| Path                                      | Access |
|-------------------------------------------|--------|
| /calico/felix/v1/\*                       |   RW   |
| /calico/ipam/v2/\*                        |   RW   |
| /calico/resources/v3/projectcalico.org/\* |   RW   |

## Felix as a stand alone process

| Path                                      | Access |
|-------------------------------------------|--------|
| /calico/felix/v1/\*                       |   RW   |
| /calico/resources/v3/projectcalico.org/\* |   R    |

## CNI-plugin

| Path                                      | Access |
|-------------------------------------------|--------|
| /calico/ipam/v2/\*                        |   RW   |
| /calico/resources/v3/projectcalico.org/\* |   RW   |

## {{site.imageNames["kubeControllers"]}}

| Path                                      | Access |
|-------------------------------------------|--------|
| /calico/ipam/v2/\*                        |   RW   |
| /calico/resources/v3/projectcalico.org/\* |   RW   |

## calicoctl (read only access)

| Path                                      | Access |
|-------------------------------------------|--------|
| /calico/ipam/v2/\*                        |   R    |
| /calico/resources/v3/projectcalico.org/\* |   R    |

## calicoctl (policy editor access)

| Path                                                            | Access |
|-----------------------------------------------------------------|--------|
| /calico/ipam/v2/\*                                              |   R    |
| /calico/resources/v3/projectcalico.org/\*                       |   R    |
| /calico/resources/v3/projectcalico.org/globalnetworkpolicies/\* |   RW   |
| /calico/resources/v3/projectcalico.org/globalnetworksets/\*     |   RW   |
| /calico/resources/v3/projectcalico.org/networkpolicies/\*       |   RW   |
| /calico/resources/v3/projectcalico.org/profiles/\*              |   RW   |

## calicoctl (full read/write access)

| Path                                      | Access |
|-------------------------------------------|--------|
| /calico/ipam/v2/\*                        |   RW   |
| /calico/resources/v3/projectcalico.org/\* |   RW   |
