---
title: PacketCapture
description: API for this Calico Enterprise resource. 
canonical_url: '/reference/resources/packetcapture'
---

A Packet Capture resource (`PacketCapture`) represents captured live traffic for debugging microservices and application
interaction inside a Kubernetes cluster.

{{site.prodname}} supports selecting one or multiple [WorkloadEndpoints resources]({{site.baseurl}}/reference/resources/workloadendpoint)
as described in the [Packet Capture] guide.

For `calicoctl` [commands]({{site.baseurl}}/reference/calicoctl/), the following case-insensitive aliases
may be used to specify the resource type on the CLI:
`packetcapture`, `packetcaptures`.

For `kubectl` [commands](https://kubernetes.io/docs/reference/kubectl/overview/), the following case-insensitive aliases may be used to specify the resource type on the CLI: 
`packetcapture`,`packetcaptures`, `packetcapture.projectcalico.org`, `packetcaptures.projectcalico.org` as well as
abbreviations such as `packetcapture.p` and `packetcaptures.p`.

### Sample YAML

```yaml
apiVersion: projectcalico.org/v3
kind: PacketCapture
metadata:
  name: sample-capture
  namespace: sample-namespace
spec:
  selector: k8s-app == "sample-app"
```

### Packet capture definition

#### Metadata

| Field     | Description                                                        | Accepted Values                                     | Schema | Default   |
|-----------|--------------------------------------------------------------------|-----------------------------------------------------|--------|-----------|
| name      | The name of the packet capture. Required.                          | Alphanumeric string with optional `.`, `_`, or `-`. | string |           |
| namespace | Namespace provides an additional qualification to a resource name. |                                                     | string | "default" |


#### Spec

| Field    | Description                                                                                         | Accepted Values | Schema                | Default |
|----------|-----------------------------------------------------------------------------------------------------|-----------------|-----------------------|---------|
| selector | Selects the endpoints to which this packet capture applies.                                          |                 | [selector](#selector) |         |


#### Selector

{% include content/selectors.md %}

[Packet Capture]: /threat/packetcapture
