---
title: Installing Calico Enterprise on Kubernetes
canonical_url: /getting-started/kubernetes/installation/
---

We provide a number of manifests to get you up and running with {{site.prodname}} in
just a few steps. Refer to the section that corresponds to your desired networking
for instructions.

- [Installing {{site.prodname}} for policy and networking](calico)

- [Installing {{site.prodname}} for policy](other): recommended for those on AWS who wish to
  [federate clusters]({{site.baseurl}}/networking/federation/index).

After installing {{site.prodname}}, you can [enable application layer policy]({{site.baseurl}}/getting-started/kubernetes/installation/app-layer-policy).
Enabling application layer policy also secures workload-to-workload communications with mutual
TLS authentication.

Should you wish to modify the manifests before applying them, refer to
[Customizing the Calico manifests](config-options) and
[Customizing the {{site.prodname}} manifests](hosted/cnx/cnx).
