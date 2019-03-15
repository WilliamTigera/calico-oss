---
<<<<<<< HEAD
title: Amazon Web Services (AWS)
canonical_url: https://docs.tigera.io/v2.3/reference/public-cloud/aws
---

## Overview

{{site.prodname}} provides fine-grained network security policy for individual containers on AWS.

For networking, you can use:

- [{{site.prodname}} networking](#using-{{site.prodnamedash}}-networking)

- [AWS networking](#using-aws-networking) (recommended with federation)

## Using {{site.prodname}} networking

### About {{site.prodname}} networking

{{site.prodname}} networking on AWS allows you to achieve the following.

- **No Overlays**: Within each VPC subnet {{site.prodname}} doesn’t need an overlay, which means high performance networking for your containers.

- **No 50 Node Limit**: With {{site.prodname}}, you can exceed the 50 node limit, which exists as a consequence of the AWS 50 route limit when using the VPC routing table.

### Routing traffic within a single VPC subnet
=======
title: Amazon Web Services
redirect_from: latest/reference/public-cloud/aws
canonical_url: https://docs.projectcalico.org/v3.5/reference/public-cloud/aws
---

{{site.prodname}} provides the following advantages when running in Amazon Web Services (AWS):

- **Network Policy for Containers**: {{site.prodname}} provides fine-grained network security policy for individual containers.
- **No Overlays**: Within each VPC subnet {{site.prodname}} doesn't need an overlay, which means high performance networking for your containers.
- **No 50 Node Limit**: {{site.prodname}} allows you to surpass the 50 node limit, which exists as a consequence of the [AWS 50 route limit](http://docs.aws.amazon.com/AmazonVPC/latest/UserGuide/VPC_Appendix_Limits.html#vpc-limits-route-tables) when using the VPC routing table.

## Routing traffic within a single VPC subnet
>>>>>>> open/master

Since {{site.prodname}} assigns IP addresses outside the range used by AWS for EC2 instances, you must disable AWS src/dst
checks on each EC2 instance in your cluster
[as described in the AWS documentation](http://docs.aws.amazon.com/AmazonVPC/latest/UserGuide/VPC_NAT_Instance.html#EIP_Disable_SrcDestCheck).  This
allows {{site.prodname}} to route traffic natively within a single VPC subnet without using an overlay or any of the limited VPC routing table entries.

<<<<<<< HEAD
### Routing traffic across different VPC subnets / VPCs
=======
## Routing traffic across different VPC subnets / VPCs
>>>>>>> open/master

If you need to split your deployment across multiple AZs for high availability then each AZ will have its own VPC subnet.  To
use {{site.prodname}} across multiple different VPC subnets or [peered VPCs](http://docs.aws.amazon.com/AmazonVPC/latest/UserGuide/vpc-peering.html),
in addition to disabling src/dst checks as described above you must also enable IPIP encapsulation and outgoing NAT
on your {{site.prodname}} IP pools.

See the [IP pool configuration reference]({{site.baseurl}}/{{page.version}}/reference/calicoctl/resources/ippool)
for information on how to configure {{site.prodname}} IP pools.

By default, {{site.prodname}}'s IPIP encapsulation applies to all container-to-container traffic.  However,
encapsulation is only required for container traffic that crosses a VPC subnet boundary.  For better
performance, you can configure {{site.prodname}} to perform IPIP encapsulation only across VPC subnet boundaries.

To enable the "CrossSubnet" IPIP feature, configure your {{site.prodname}} IP pool resources
to enable IPIP and set the mode to "CrossSubnet".

> **Note**: This feature was introduced in {{site.prodname}} v2.1, if your deployment was created with
> an older version of {{site.prodname}}, or if you if you are unsure whether your deployment
<<<<<<< HEAD
> is configured correctly, follow the [Configuring IP-in-IP guide]({{site.baseurl}}/{{page.version}}/usage/configuration/ip-in-ip)
=======
> is configured correctly, follow the [Configuring IP-in-IP guide]({{site.baseurl}}/{{page.version}}/networking/ip-in-ip)
>>>>>>> open/master
> which discusses this in more detail.
>
{: .alert .alert-info}

The following `calicoctl` command will create or modify an IPv4 pool with
CIDR 192.168.0.0/16 using IPIP mode `CrossSubnet`. Adjust the pool CIDR for your deployment.

```
calicoctl apply -f - << EOF
apiVersion: projectcalico.org/v3
kind: IPPool
metadata:
  name: ippool-cs-1
spec:
  cidr: 192.168.0.0/16
  ipipMode: CrossSubnet
EOF
```

<<<<<<< HEAD
### Enabling workload-to-WAN traffic
=======
## Enabling workload-to-WAN traffic
>>>>>>> open/master

To allow {{site.prodname}} networked containers to reach resources outside of AWS,
you must configure outgoing NAT on your [{{site.prodname}} IP pool]({{site.baseurl}}/{{page.version}}/reference/calicoctl/resources/ippool).

AWS will perform outbound NAT on any traffic which has the source address of an EC2 virtual
machine instance.  By enabling outgoing NAT on your {{site.prodname}} IP pool, {{site.prodname}} will
NAT any outbound traffic from the containers hosted on the EC2 virtual machine instances.

The following `calicoctl` command will create or modify an IPv4 pool with
CIDR 192.168.0.0/16 using IPIP mode `CrossSubnet` and enables outgoing NAT.
Adjust the pool CIDR for your deployment.

```
calicoctl apply -f - << EOF
apiVersion: projectcalico.org/v3
kind: IPPool
metadata:
  name: ippool-1
spec:
  cidr: 192.168.0.0/16
  ipipMode: CrossSubnet
  natOutgoing: true
EOF
```
<<<<<<< HEAD

## Using AWS networking

{{site.prodname}} supports the AWS VPC CNI plugin, which creates ENI interfaces for the pods that fall within the VPC of
the cluster. Routing to these pods is automatically handled by AWS.

We recommend using the AWS VPC CNI plugin with [federation](../../usage/federation/index) as it provides seamless IP connectivity
between your AWS cluster and a remote cluster. Ensure that you use version 1.1 or later.

Install the AWS VPC CNI plugin in your Kubernetes cluster as follows.

1. Download the AWS VPC CNI manifest.

   ```bash
   curl \
   https://raw.githubusercontent.com/aws/amazon-vpc-cni-k8s/release-1.1/config/v1.1/aws-k8s-cni.yaml \
   -O
   ```

1. By default, the AWS CNI plugin performs SNAT for any packet routed outside the VPC. You must disable SNAT
   on external packets to allow clusters in other VPCs or connected via VPN to communicate with pods.

   > **Important**: Required for [federation](../../usage/federation/index).
   {: .alert .alert-danger}

   To disable SNAT on external packets, open the AWS VPC CNI manifest in your favorite editor
   and add an `AWS_VPC_K8S_CNI_EXTERNALSNAT` environment variable set to `true` in the `aws-node` container.
   An example follows.

   ```yaml
   kind: DaemonSet
   apiVersion: extensions/v1beta1
   metadata:
     name: aws-node
     namespace: kube-system
     labels:
       k8s-app: aws-node
   spec:
     updateStrategy:
       type: RollingUpdate
     selector:
       matchLabels:
         k8s-app: aws-node
     template:
       metadata:
         labels:
           k8s-app: aws-node
         annotations:
           scheduler.alpha.kubernetes.io/critical-pod: ''
       spec:
         serviceAccountName: aws-node
         hostNetwork: true
         tolerations:
         - operator: Exists
         containers:
         - image: 602401143452.dkr.ecr.us-west-2.amazonaws.com/amazon-k8s-cni:1.1.0
           name: aws-node
           env:
             - name: AWS_VPC_K8S_CNI_LOGLEVEL
               value: DEBUG
             - name: MY_NODE_NAME
               valueFrom:
                 fieldRef:
                   fieldPath: spec.nodeName
             - name: AWS_VPC_K8S_CNI_EXTERNALSNAT
               value: "true"
   ...
   ```

   > **Note**: For details see the
   > [Amazon VPC CNI Plugin Version 1.1](https://aws.amazon.com/blogs/opensource/vpc-cni-plugin-v1-1-available)
   > release notes.
   {: .alert .alert-info}

1. Apply the manifest using kubectl.

   ```bash
   kubectl apply -f aws-k8s-cni.yaml
   ```

1. Follow the standard {{site.prodname}} instructions to install [{{site.prodname}} for policy only](/{{page.version}}/getting-started/kubernetes/installation/other),
   making sure to download the correct manifest.
=======
>>>>>>> open/master
