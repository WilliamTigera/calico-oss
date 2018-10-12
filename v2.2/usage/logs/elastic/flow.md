---
title: Flow logs
redirect_from: latest/usage/logs/elastic/flow
canonical_url: https://docs.tigera.io/v2.2/usage/logs/elastic/flow
---

{{site.prodname}} pushes the following data up to Elasticsearch. The following table
details the key/value pairs in the JSON blob, including their
[Elasticsearch datatype](https://www.elastic.co/guide/en/elasticsearch/reference/current/mapping-types.html).
This information should assist you in constructing queries.


| Name                  | Datatype          | Description |
| --------------------- | ----------------- | ----------- |
| `start_time`          | date              | When the collection of the log began in UNIX timestamp format. |
| `end_time`            | date              | When the collection of the log concluded in UNIX timestamp format. |
| `action`              | keyword           | {::nomarkdown}<p>&#x25cf;&nbsp;<code>allow</code>: {{site.prodname}} accepted the flow.<br>&#x25cf;&nbsp;<code>deny</code>: {{site.prodname}} denied the flow.</p>{:/}  |
| `bytes_in`            | long              | Number of incoming bytes since the last export. |
| `bytes_out`           | long              | Number of outgoing bytes since the last export. |
| `dest_ip`             | ip                | The IP address of the destination pod. |
| `dest_name`           | keyword           | {::nomarkdown}<p>&#x25cf;&nbsp;Name of the destination pod. A trailing asterisk indicates aggregation.<br>&#x25cf;&nbsp;<code>pvt</code>: a destination IP address in a private network<br>&#x25cf;&nbsp;<code>pub</code>: a destination IP address in a public network, e.g., the internet</p>{:/} |
| `dest_namespace`      | keyword           | Namespace of the destination pod. |
| `dest_port`           | long              | The destination port. |
| `dest_type`           | keyword           | Destination endpoint type: wep: pod net: not a pod |
| `dest_labels`         | array of keywords | Labels applied to the destination pod. A hyphen indicates aggregation. |
| `reporter`            | keyword           | {::nomarkdown}<p>&#x25cf;&nbsp;<code>src</code>: this flow came from the pod that initiated the connection.<br>&#x25cf;&nbsp;<code>dst</code>: this flow came from the pod that received the initial connection.</p>{:/} |
| `num_flows`           | long              | The number of flows aggregated into this entry during this export interval. |
| `num_flows_completed` | long              | The number of flows that were completed during the export interval. |
| `num_flows_started`   | long              | The number of flows that were started during the export interval. |
| `packets_in`          | long              | Number of incoming packets since the last export. |
| `packets_out`         | long              | Number of outgoing packets since the last export. |
| `proto`               | keyword           | Protocol. |
| `policies`            | array of keywords | The policy or policies that allowed or denied this flow. |
| `source_ip`           | ip                | The IP address of the source pod. A hyphen indicates aggregation. |
| `source_name`         | keyword           | {::nomarkdown}<p>&#x25cf;&nbsp;Name of the source pod. A trailing asterisk indicates aggregation.<br>&#x25cf;&nbsp;<code>pvt</code>: a source IP address in a private network<br>&#x25cf;&nbsp;<code>pub</code>: a source IP address in a public network, e.g., the internet</p>{:/} |
| `source_namespace`    | keyword           | Namespace of the source pod. |
| `source_port`         | long              | The source port. |
| `source_type`         | keyword           | Source endpoint type:wep: podnet: not a pod |
| `source_labels`       | array of keywords | Labels applied to the source pod. A hyphen indicates aggregation. |
