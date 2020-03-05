---
title: Configure alerts
description: Define alert criteria for the Alerts page in Calico Enterprise Manager. 
canonical_url: /security/threat-detection-and-prevention/alerts
---

### Big picture

Define alert criteria for the Alerts page in {{site.prodname}} Manager based on collected flow, DNS, and audit logs. 

### Value 

When it comes to alerts that indicate cluster compromise, cluster administrator need flexibility to ensure fine-grain tuning; too many alerts become noise. With {{site.prodname}}, you can configure alerts to detect log entries that match patterns, or aggregate log entries over key fields and alert when entry counts or metrics on aggregated fields meet a condition. For higher fidelity, you can use the alerts domain-specific query language to select only relevant data.

### Features

This how-to guide uses the following {{site.prodname}} features:

- **GlobalAlert** resource

### Before you begin...

**Required**

Privileges to manage GlobalAlert

**Recommended**

We recommend that you turn down the aggregation of flow logs sent to Elasticsearch for configuring threat feeds. If you do not adjust flow logs, {{site.prodname}} aggregates over the external IPs for allowed traffic, and alerts will not provide pod-specific results (unless the traffic is denied by policy). Go to: [FelixConfiguration]({{site.baseurl}}/reference/resources/felixconfig) and set the field, **flowLogsFileAggregationKindForAllowed** to **1**.

### How To

#### Create a global alert

1. Create a yaml file containing one or more alerts.
1. Apply the alert to your cluster.

   ```shell
   kubectl apply -f <your_alert_filename>
   ```

1. Wait until the alert runs, and check the status.

   ```shell
   kubectl get globalalert <your_alert_name> -o yaml
   ```
1. In {{site.prodname}} Manager, go the **Alerts** page to view events
as alert conditions are satisfied.

#### Examples

Following is the basic example to trigger the alert when we see 100 flows in the entire cluster in last 5 mins.

```yaml
apiVersion: projectcalico.org/v3
kind: GlobalAlert
metadata:
  name: example-flows
spec:
  description: "100 flows Example"
  severity: 100
  dataSet: flows
  metric: count
  condition: gt
  threshold: 100
```

In the following example, we detect ssh traffic in default namespace.

```yaml
apiVersion: projectcalico.org/v3
kind: GlobalAlert
metadata:
  name: network.ssh
spec:
  description: "[flows] ssh flow in default namespace detected from ${source_namespace}/${source_name_aggr}"
  severity: 100
  period: 10m
  lookback: 10m
  dataSet: flows
  query: proto='tcp' AND action='allow' AND dest_port='22' AND (source_namespace='default' OR dest_namespace='default') AND reporter=src
  aggregateBy: [source_namespace, source_name_aggr]
  field: num_flows
  metric: sum
  condition: gt
  threshold: 0
```

In the following example, we are monitoring priviledge access within your cluster and detect any modification to `globalnetworksets`

```
apiVersion: projectcalico.org/v3
kind: GlobalAlert
metadata:
  name: policy.globalnetworkset
spec:
  description: "[audit] [privileged access] change detected for ${objectRef.resource} ${objectRef.name}"
  severity: 100
  period: 10m
  lookback: 10m
  dataSet: audit
  query: (verb=create OR verb=update OR verb=delete OR verb=patch) AND "objectRef.resource"=globalnetworksets
  aggregateBy: [objectRef.resource, objectRef.name]
  metric: count
  condition: gt
  threshold: 0
```

### Templates

{{site.prodname}} includes a set of Alert templates. These are used
by the {{site.prodname}} Manager to create alerts
for common tasks that can then be modified to suit your needs. They
are identical to GlobalAlerts with the addition of the `summary` field.
The `summary` field is a human-readable description of the template
that is displayed in the user interface, as opposed to `description`
which is a template that should contain variable substitutions.

### Above and beyond

For all global alert and template options, see [GlobalAlert]({{site.baseurl}}/reference/resources/globalalert)
To troubleshoot alerts, see [Troubleshooting]({{site.baseurl}}/maintenance/troubleshooting)

[flow]: ../logs/elastic/flow
[dns]: ../logs/elastic/dns
[audit logs]: ../logs/elastic/ee-audit
[45925]: https://www.exploit-db.com/exploits/45925
