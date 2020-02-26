---
title: Configure RBAC for tiered policies
description: Configure RBAC to control access to policies and tiers. 
canonical_url: /reference/cnx/rbac-tiered-policies
show_toc: false
---

### Big picture

Configure fine-grained user access controls for tiered policies.

### Value

Self-service is an important part of CI/CD processes for containerization and microservices. {{site.prodname}} provides fine-grained access control (RBAC) for:

- {{site.prodname}} policy and tiers 
- Kubernetes network policy

### Features

This how-to guide uses the following {{site.prodname}} features:

- **{{site.prodname}} API server**

### Concepts

#### Standard Kubernetes RBAC

{{site.prodname}} implements the standard **Kubernetes RBAC Authorization APIs** with `Role` and `ClusterRole` types. The {{site.prodname}} API server integrates with Kubernetes RBAC Authorization APIs as an extension API server. 

#### RBAC for policies and tiers

In {{site.prodname}}, global network policy and network policy resources are associated with a specific tier. Admins can configure access control for these {{site.prodname}} policies using standard Kubernetes `Role` and `ClusterRole` resource types. This makes it easy to manage RBAC for both Kubernetes network policies and {{site.prodname}} tiered network policies. RBAC permissions include managing resources using {{site.prodname}} Manager, `calicoctl`, `calicoq`, and `kubectl`. 

#### Fine-grained RBAC for policies and tiers

RBAC permissions can be split by resources ({{site.prodname}} and Kubernetes), and by actions (CRUD). Tiers should be created by administrators. Full CRUD operations on tiers is synonymous with full management of network policy. Full management to network policy and global network policy also requires `GET` permissions to 1) any tier a user can view/manage, and 2) the required access to the tiered policy resources. 

Here are a few examples of how you can fine-tune RBAC for tiers and policies.  

| **User**  | **Permissions**                                              |
| --------- | ------------------------------------------------------------ |
| Admin     | The default **tigera-network-admin** role lets you create, update, delete, get, watch, and list all {{site.prodname}} resources (full control). Examples of limiting Admin access: <li>List tiers only</li><li>List only specific tiers</li> |
| Non-Admin | The default **tigera-ui-user** role allows users to only list {{site.prodname}} policy and tier resources. Examples of limiting user access: <li>Read-only access to all policy resources across all tiers, but only write access for NetworkPolicies with a specific tier and namespace.</li> <li>Perform any operations on NetworkPolicies and GlobalNetworkPolicies. </li><li>List tiers only.</li> <li>List or modify any policies in any tier.Fully manage only Kubernetes network policies in the default tier, in the default namespace, with read-only access for all other tiers.</li> |

#### RBAC definitions for Calico Enterprise network policy

To specify per-tier RBAC for the {{site.prodname}} network policy and {{site.prodname}} global network policy, use pseudo resource kinds and names in the `Role` and `ClusterRole` definitions. For example,

```
kind: ClusterRole
apiVersion: rbac.authorization.k8s.io/v1beta1
metadata:
  name: tier-default-reader
rules:
- apiGroups: ["projectcalico.org"]
  resources: ["tiers"]
  resourceNames: ["default"]
  verbs: ["get"]
- apiGroups: ["projectcalico.org"]
  resources: ["tier.networkpolicies"]
  resourceNames: ["default.*"]
  verbs: ["get", "list"]
```
Where:
- **resources**: `tier.globalnetworkpolicies` and `tier.networkpolicies`
- **resourceNames**:
  - Blank - any policy of the specified kind across all tiers.
  - `<tiername>.*` - any policy of the specified kind within the named tier.
  - `<tiername.policyname>` - the specific policy of the specified kind. Because the policy name is prefixed with the tier name, this also specifies the tier.

### Before you begin...

**Required**

A **tigera-network-admin** role with full permissions to create and modify resources. See [Log in to {{site.prodname}} Enterprise Manager]({{site.baseurl}}/getting-started/create-user-login).

**Recommended**

A rough idea of your tiered policy workflow, and who should access what. See [Configure tiered policies]({{site.baseurl}}/security/tiered-policy).

### How to

- [Create Admin users, full permissions](#create-admin-users-full-permissions)
- [Create minimum permissions for all non-Admin users](#create-minimum-permissions-for-all-non-admin-users)

> **Note**: ` kubectl auth can-i` cannot be used the check RBAC for tiered policy. 
{: .alert .alert-info}

#### Create Admin users, full permissions

Create an Admin user with full access to the {{site.prodname}} Manager (as well as everything else in the cluster) using the following command. See the Kubernetes documentation to identify users based on your chosen [authentication method](https://kubernetes.io/docs/admin/authentication/), and how to use the [RBAC resources](https://kubernetes.io/docs/reference/access-authn-authz/rbac/).

```
kubectl create clusterrolebinding permissive-binding \
    --clusterrole=cluster-admin \
    --user=<USER>
```
#### Create minimum permissions for all non-Admin users

All users using {{site.prodname}} Manager should be able to list and watch tiers, policies, networksets, and licenses. 

1. Download the [min-ui-user-rbac.yaml manifest]({{ "/getting-started/kubernetes/installation/hosted/cnx/demo-manifests/min-ui-user-rbac.yaml" | absolute_url }}).
   The roles and bindings in this file provide a minimum starting point for setting up RBAC for your users according to your specific security requirements.

1. Run the following command to replace <USER> with the name or email of the user you are providing permissions to:

   ```
   sed -i -e 's/<USER>/<name or email>/g' min-ui-user-rbac.yaml
   ```
1. Use the following command to install the bindings:

   ```
   kubectl apply -f min-ui-user-rbac.yaml
   ```

### Tutorial

This tutorial shows how to use RBAC to control access to resources and CRUD actions for a non-Admin user called, **john**.

```
# Users:
- john (non-Admin)
- kubernetes-admin (Admin)
```

RBAC examples include:

- [User can view all policies, and modify policies in the default namespace and tier](#user-can-view-all-policies-and-modify-policies-in-the-default-namespace-and-tier)
- [User cannot read policies in any tier](#user-cannot-read-policies-in-any-tier)
- [User can read policies in the default tier](#user-can-read-policies-in-the-default-tier) 
- [User can read policies only in a specific tier](#user-can-read-policies-only-in-a-specific-tier)
- [User can view only a specific tier](#user-can-view-only-a-specific-tier)
- [User can read all policies across all tiers](#user-can-read-all-policies-across-all-tiers)
- [User has full control over NetworkPolicy resources in a specific tier](#user-has-full-control-over-networkpolicy-resources-in-a-specific-tier)

#### User can view all policies, and modify policies in the default namespace and tier

1. Download the [`read-all-crud-default-rbac.yaml` manifest]({{ "/getting-started/kubernetes/installation/hosted/cnx/demo-manifests/read-all-crud-default-rbac.yaml" | absolute_url }}).

1. Run the following command to replace `<USER>` with the `name or email` of
   the user you are providing permissions to:

   ```
   sed -i -e 's/<USER>/<name or email>/g' read-all-crud-default-rbac.yaml
   ```

1. Use the following command to install the bindings:

   ```
   kubectl apply -f read-all-crud-default-rbac.yaml
   ```

The roles and bindings in this file provide the permissions to read all policies across all tiers and to fully manage
policies in the default tier and default namespace. This file includes the minimum required `ClusterRole` and `ClusterRoleBinding` definitions for all UI users (see `min-ui-user-rbac.yaml` above).

#### User cannot read policies in any tier

User 'john' is forbidden from reading policies in any tier (default tier, and net-sec tier).

When John issues the following command:

```
kubectl get networkpolicies.p
```

It returns:

```
Error from server (Forbidden): networkpolicies.projectcalico.org is forbidden: User "john" cannot list networkpolicies.projectcalico.org in tier "default" and namespace "default" (user cannot get tier)
```
{: .no-select-button}

Similarly, when John issues this command:

```
kubectl get networkpolicies.p -l projectcalico.org/tier==net-sec
```

It returns:

```
Error from server (Forbidden): networkpolicies.projectcalico.org is forbidden: User "john" cannot list networkpolicies.projectcalico.org in tier "net-sec" and namespace "default" (user cannot get tier)
```
{: .no-select-button}

> **Note**: The .p' extension (`networkpolicies.p`) is short 
  for "networkpolicies.projectcalico.org" and used to
  differentiate it from the Kubernetes NetworkPolicy resource and
  the underlying CRDs (if using the Kubernetes Datastore Driver).
{: .alert .alert-info}

> **Note**: Currently, the tier collection on a Policy resource through the
  `kubectl` client (pre 1.9) of the APIs, is implemented using labels because
  `kubectl` lacks field selector support. The label used for tier collection
  is `projectcalico.org/tier`. When a label selection is not specified, the
  server defaults the collection to the `default` tier. Field selection based
  policy collection is enabled at the API level. spec.tier is the field to select
  on for the purpose.
{: .alert .alert-info}

#### User can read policies in the default tier

In this example, we give user 'john' permission to read the default tier.

```yaml
kind: ClusterRole
apiVersion: rbac.authorization.k8s.io/v1beta1
metadata:
  name: tier-default-reader
rules:
- apiGroups: ["projectcalico.org"]
  resources: ["tiers"]
  resourceNames: ["default"]
  verbs: ["get"]
- apiGroups: ["projectcalico.org"]
  resources: ["tier.networkpolicies"]
  resourceNames: ["default.*"]
  verbs: ["get", "list"]

---

kind: ClusterRoleBinding
apiVersion: rbac.authorization.k8s.io/v1beta1
metadata:
  name: read-tier-default-global
subjects:
- kind: User
  name: john
  apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: ClusterRole
  name: tier-default-reader
  apiGroup: rbac.authorization.k8s.io
```

With the above, user john is able to read NetworkPolicy resources in default tier.

```bash
kubectl get networkpolicies.p
```

If no NetworkPolicy resources exist it returns:

```
No resources found.
```
{: .no-select-button}

But John still cannot access tier, **net-sec**.

```bash
kubectl get networkpolicies.p -l projectcalico.org/tier==net-sec
```

This returns:

```
Error from server (Forbidden): networkpolicies.projectcalico.org is forbidden: User "john" cannot list networkpolicies.projectcalico.org in tier "net-sec" and namespace "default" (user cannot get tier)
```
{: .no-select-button}

#### User can read policies only in a specific tier

Let's assume that the kubernetes-admin gives user 'john' the permission to read tier, **net-sec**.
To provide permission to user 'john' to read policies under 'net-sec' tier, use the following `ClusterRole` and `ClusterRolebindings`.

```yaml
kind: ClusterRole
apiVersion: rbac.authorization.k8s.io/v1beta1
metadata:
  # "namespace" omitted since ClusterRoles are not namespaced
  name: tier-net-sec-reader
rules:
- apiGroups: ["projectcalico.org"]
  resources: ["tiers"]
  resourceNames: ["net-sec"]
  verbs: ["get"]
- apiGroups: ["projectcalico.org"]
  resources: ["tier.networkpolicies"]
  resourceNames: ["net-sec.*"]
  verbs: ["get", "list"]

---

kind: ClusterRoleBinding
apiVersion: rbac.authorization.k8s.io/v1beta1
metadata:
  name: read-tier-net-sec-global
subjects:
- kind: User
  name: john
  apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: ClusterRole
  name: tier-net-sec-reader
  apiGroup: rbac.authorization.k8s.io
```

#### User can view only a specific tier

In this example, the following `ClusterRole` can be used to provide 'get' access to the **net-sec**
tier. This has the effect of making the net-sec tier visible in the {{site.prodname}} Manager. To modify or view policies within the net-sec tier, additional RBAC permissions are required.

```yaml
kind: ClusterRole
apiVersion: rbac.authorization.k8s.io/v1beta1
metadata:
  name: net-sec-tier-visible
rules:
- apiGroups: ["projectcalico.org"]
  resources: ["tiers"]
  verbs: ["get"]
  resourceNames: ["net-sec"]
```

#### User can read all policies across all tiers

In this example, the `ClusterRole` is used to provide read access to all policy resource types across all tiers.

```yaml
kind: ClusterRole
apiVersion: rbac.authorization.k8s.io/v1beta1
metadata:
  name: all-tier-policy-reader
rules:
# To access Calico policy in a tier, the user requires get access to that tier. This provides get
# access to all tiers.
- apiGroups: ["projectcalico.org"]
  resources: ["tiers"]
  verbs: ["get"]
# This allows read access of the kubernetes NetworkPolicy resources (these are always in the default tier).
- apiGroups: ["networking.k8s.io", "extensions"]
  resources: ["networkpolicies"]
  verbs: ["get","watch","list"]
# This allows read access of the Calico NetworkPolicy and GlobalNetworkPolicy resources in all tiers.
- apiGroups: ["projectcalico.org"]
  resources: ["tier.networkpolicies","tier.globalnetworkpolicies"]
  verbs: ["get","watch","list"]
```

##### User has full control over NetworkPolicy resources in a specific tier

In this example, the `ClusterRole` is used to provide full access control of Calico NetworkPolicy
resource types in the **net-sec** tier.
-  If this is bound to a user using a `ClusterRoleBinding`, then the user has full access of these
   policies across all namespaces.
-  If this is bound to a user using a `RoleBinding`, then the user has full access of these
   policies within a specific namespace. (This is useful because you only need this one `ClusterRol`e to be
   defined, but it can be "reused" for users in different namespaces using a `RoleBinding`).

> **Note**: The Kubernetes NetworkPolicy resources are bound to the default tier, and so this `ClusterRole`
> does not contain any Kubernetes resource types.
{: .alert .alert-info}

```yaml
kind: ClusterRole
apiVersion: rbac.authorization.k8s.io/v1beta1
metadata:
  name: net-sec-tier-policy-cruder
rules:
# To access Calico policy in a tier, the user requires get access to that tier.
- apiGroups: ["projectcalico.org"]
  resources: ["tiers"]
  resourceNames: ["net-sec"]
  verbs: ["get"]
# This allows configuration of the Calico NetworkPolicy resources in the net-sec tier.
- apiGroups: ["projectcalico.org"]
  resources: ["tier.networkpolicies"]
  resourceNames: ["net-sec.*"]
  verbs: ["*"]
```
