---
title: Global Alert
description: API for this Calico Enterprise resource. 
---

A global alert resource represents a query that is periodically run
against data sets collected by {{site.prodname}} whose findings are
added to the "Alerts" page in {{site.prodname}} Manager. Alerts may
search for the existence of rows in a query, or when aggregated metrics
satisfy a condition.

{{site.prodname}} supports alerts on the following data sets:

 * [Audit logs]
 * [DNS logs]
 * [Flow logs]

For `calicoctl` [commands]({{site.baseurl}}/reference/calicoctl/), the following case-insensitive aliases
can be used to specify the resource type on the CLI:
`globalalert`, `globalalerts`.

For `kubectl` [commands](https://kubernetes.io/docs/reference/kubectl/overview/), the following case-insensitive aliases
can be used to specify the resource type on the CLI:
`globalalert.projectcalico.org`, `globalalerts.projectcalico.org` and abbreviations such as
`globalalert.p` and `globalalerts.p`.

### Sample YAML

```yaml
apiVersion: projectcalico.org/v3
kind: GlobalAlert
metadata:
  name: sample
spec:
  description: "Sample"
  severity: 100
  dataSet: flows
  query: action=allow
  aggregateBy: [client_namespace, client_name_aggr]
  field: num_flows
  metric: sum
  condition: gt
  threshold: 0
```

### GlobalAlert definition

#### Metadata

| Field | Description | Accepted Values | Schema |
|---|---|---|---|
| name | The name of this alert. | Lower-case alphanumeric with optional `-`  | string |

#### Spec

| Field | Description | Type | Required | Acceptable Values | Default |
|---|---|---|---|---|---|
| description | Template for the description field in generated events. See the description section below for more details. | string | yes |
| severity | Severity of the alert for display in Manager. | int | yes | 1 - 100 |
| dataSet | Which data set to execute the alert against. | string | yes | audit, dns, flows |
| period | How often the query is run. | duration | no | 1h 2m 3s | 5m |
| lookback | How much data to gather at once. Must exceed audit log flush interval, `dnsLogsFlushInterval`, or `flowLogsFlushInterval` as appropriate. | duration | no | 1h 2m 3s | 10m |
| query | Which data to include from the source data set. Written in a domain-specific query language. See the query section below. | string | no |
| aggregateBy | An optional list of fields to aggregate results. | string array | no |
| field | Which field to aggregate results by if using a metric other than count. | string | if metric is one of avg, max, min, or sum |
| metric | A metric to apply to aggregated results. `count` is the number of log entries matching the aggregation pattern. Others are applied only to numeric fields in the logs. | string | no | avg, max, min, sum, count |
| condition | Compare the value of the metric to the threshold using this condition. | string | if metric defined | eq, not_eq, lt, lte, gt, gte |
| threshold | A numeric value to compare the value of the metric against. | float | if metric defined |

#### Status

| Field | Description |
|---|---|---|
| lastUpdate | When the alert was last modified on the backend. |
| active | Whether the alert is active on the backend. |
| healthy | Whether the alert is in an error state or not. |
| executionState | The raw execution state of the generated Elastic watcher. |
| lastExecuted | When the query for the alert last ran. |
| lastEvent | When the condition of the alert was last satisfied and an alert was successfully generated. |
| errorConditions | List of errors preventing operation of the updates or search. |

### Query

Alerts use a domain-specific query language to select which records
from the data set should be used in the alert. This could be used to
identify flows with specific features, or to select (or omit) certain
namespaces from consideration.

The query language is composed of any number of selectors, combined
with boolean expressions (AND, OR, and NOT) and bracketed
subexpressions. These are translated by {{site.product}} to Elastic
DSL queries that are executed on the backend.

A selector consists of a key, comparator, and value. Keys and values
may be identifiers consisting of alphanumerics and underscores (\_)
with the first character being alphabetic or an underscore, or may be
quoted strings. Values may also be integer or floating point numbers.
Comparators may be `=` (equal), `!=` (not equal), `<` (less than),
`<=` (less than or equal), `>` (greater than), or `>=` (greater than
or equal).

Keys must be indexed fields in their corresponding data set. See the
appendix for a list of valid keys in each data set.

Examples:

 * `query: "count > 0"`
 * `query: "\"servers.ip\" = \"127.0.0.1\""`

Selectors may be combined using AND, OR, and NOT boolean expressions
and bracketed subexpressions.

Examples:

 * `query: "count > 100 AND client_name=mypod"`
 * `query: "client_namespace = ns1 OR client_namespace = ns2"`
 * `query: "count > 100 AND NOT (client_namespace = ns1 OR client_namespace = ns2)"`
 * `query: "(qtype = A OR qtype = AAAA) AND rcode != NoError"`

### Aggregation

Results from the query can be aggregated by any number of data fields.
Only these data fields will be included in the generated alerts, and
each unique combination of aggregations will generate a unique alert.
Careful consideration of fields for aggregation will yield the best
results.

Some good choices for aggregations on the `flows` data set are
`[source_namespace, source_name_aggr, source_name]`, `[source_ip]`,
`[dest_namespace, dest_name_aggr, dest_name]`, and `[dest_ip]`
depending on your use case. For the `dns` data set,
`[client_namespace, client_name_aggr, client_name]` is a good choice
for an aggregation pattern.

### Metrics and conditions

Results from the query can be further aggregated using a metric that
is applied to a numeric field, or counts the number of rows in an
aggregation. Search hits satisfying the condition are output as
alerts.

| Metric | Description | Applied to Field |
|---|---|---|
| count | Counts the number of rows | No |
| min | The minimal value of the field | Yes |
| max | The maximal value of the field | Yes |
| sum | The sum of all values of the field | Yes |
| avg | The average value of the field | Yes |

| Condition | Description |
|---|---|
| eq | Equals |
| not_eq | Not equals |
| lt | Less than |
| lte | Less than or equal |
| gt | Greater than |
| gte | Greater than or equal |

Example:

```yaml
apiVersion: projectcalico.org/v3
kind: GlobalAlert
metadata:
  name: frequent-dns-responses
spec:
  description: "Observed ${sum} NXDomain responses for ${qname}"
  severity: 100
  dataSet: dns
  query: rcode = NXDomain AND (rtype = A or rtype = AAAA)
  aggregateBy: qname
  field: count
  metric: sum
  condition: gte
  threshold: 100
```

This alert identifies non-existing DNS responses for Internet addresses
that were observed more than 100 times in the past 10 minutes.

#### Unconditional alerts

If the `field`, `metric`, `condition`, and `threshold` fields of an
alert are left blank then the alert will trigger whenever its query
returns any data. Each hit (or aggregation pattern, if `aggregateBy`
is non-empty) returned will cause an event to be created. This should
be used **only** when the query is highly specific to avoid filling
the Alerts page and index with a large number of events. The use of
`aggregateBy` is strongly recommended to reduce the number of entries
added to the Alerts page.

The following example would alert on incoming connections to postgres
pods from the Internet that were not denied by policy. It runs hourly
to reduce the noise. Noise could be further reduced by removing
`source_ip` from the `aggregateBy` clause at the cost of removing
`source_ip` from the generated events.

```yaml
period: 1h
lookback: 75m
query: "dest_labels=\"application=postgres\" AND source_type=net AND action=allow AND proto=tcp AND dest_port=5432"
aggregateBy: [dest_namespace, dest_name, source_ip]
```

### Description template

Alerts may include a description template to provide context for the
alerts in the {{site.prodname}} Manager Alert user interface. Any field
in the `aggregateBy` section, or the value of the `metric` may be
substituted in the description using a bracketed variable syntax.

Example:

```yaml
description: "Observed ${sum} NXDomain responses for ${qname}"
```


### Period and lookback

The interval between alerts, and the amount of data considered by the
alert may be controlled using the `period` and `lookback` parameters
respectively. These fields are formatted as [duration] strings.

 > A duration string is a possibly signed sequence of decimal numbers,
 > each with optional fraction and a unit suffix, such as "300ms",
 > "-1.5h" or "2h45m". Valid time units are "ns", "us" (or "µs"),
 > "ms", "s", "m", "h".

The default period is 5 minutes, and lookback is 10 minutes. The
lookback should always be greater than the sum of the period and
the configured `FlowLogsFlushInterval` or `DNSLogsFlushInterval` as
appropriate to avoid gaps in coverage.

### Alert records

With only aggregations and no metrics, the alert will generate one event
per aggregation pattern returned by the query. The record field will
contain only the aggregated fields. As before, this should be used
with specific queries.

The addition of a metric will include the value of that metric in the
record, along with any aggregations. This, combined with queries as
necessary, will yield the best results in most cases.

With no aggregations the alert will generate one event per record
returned by the query. The record will be included in its entirety
in the record field of the event. This should only be used with very
narrow and specific queries.


### Templates

{{site.prodname}} supports the `GlobalAlertTemplate` resource type.
These are used in the {{site.prodname}} Manager to create alerts
with prepopulated fields that can be modified to suit your needs.
The `GlobalAlertTemplate` resource is configured identically to the
`GlobalAlert` resource. {{site.prodname}} includes some sample Alert
templates; add your own templates as needed.

#### Sample YAML

```yaml
apiVersion: projectcalico.org/v3
kind: GlobalAlertTemplate
metadata:
  name: http.connections
spec:
  description: "HTTP connections from ${source_namespace}/${source_name_aggr} to <desired_namespace>/${dest_name_aggr}"
  severity: 50
  dataSet: flows 
  query: dest_namespace="<desired namespace>" AND dest_port=80
  aggregateBy: [source_namespace, dest_name_aggr, source_name_aggr]
  field: count
  metric: sum
  condition: gte
  threshold: 1
```

### Appendix: Valid fields for queries

#### Audit logs

See [audit.k8s.io group v1] for descriptions of fields.

| Key | Type | Acceptable Values | Example |
|---|---|---|---|
| apiVersion | domain
| auditID | string
| kind | string
| level | string | None, Metadata, Request, RequestResponse
| name | domain
| objectRef.apiGroup | string
| objectRef.apiVersion | URL
| objectRef.name | domain
| objectRef.resource | string
| objectRef.namespace | domain
| requestReceivedTimestamp | date
| requestURI | URL
| responseObject.apiVersion | string | | projectcalico.org/v3
| responseObject.kind | string
| responseStatus.code | int | 100-599 | 200 404
| sourceIPs | IP, CIDR | | 198.51.100.1 192.0.2.0/24 2001:db8::1:2:3:4 2001:db8:0:2:/64
| stage | string | RequestReceived, ResponseStarted, ResponseComplete, Panic
| stageTimestamp | date
| timestamp | date
| user.groups | string
| user.username | string
| verb | string | get, list, watch, create, update, patch, delete

#### DNS logs

See [DNS logs] for description of fields.

| Key | Type | Acceptable Values | Example |
|---|---|---|---|
| start_time | date
| end_time | date
| count | int | positive
| client_name | domain
| client_name_aggr | domain
| client_namespace | domain
| client_labels.LABEL_NAME | string
| client_ip | IP, CIDR
| servers.name | domain
| servers.name_aggr | domain
| servers.namespace | domain
| servers.labels.LABEL_NAME | string
| servers.ip | IP, CIDR
| qname | domain
| qtype | dns type | A NS MD MF CNAME SOA MB MG MR NULL WKS PTR HINFO MINFO MX TXT AAAA SRV #0 - #65535
| rclass | string
| rcode | string
| rrsets.name | domain
| rrsets.type | dns type | see qtype
| rrsets.class | string
| rrsets.rdata | string
| 127.0.0.1 | projectcalico.org

#### Flow logs

See [Flow logs] for description of fields.

| Key | Type | Acceptable Values | Example |
|---|---|---|---|
| start_time | date
| end_time | date
| action | string | allow, deny
| bytes_in | int | positive
| bytes_out | int | positive
| dest_ip | IP, CIDR
| dest_name | domain
| dest_name_aggr | domain
| dest_namespace | domain
| dest_port | int | 0 - 65535
| dest_type | string | wep, hep, ns, net
| dest_labels.labels | string | LABEL_NAME=LABEL_VALUE | application=intrusion-detection-controller
| reporter | string | src, dst
| num_flows | int | positive
| num_flows_completed | int | positive
| num_flows_started | int | positive
| http_requests_allowed_in | int | positive
| http_requests_denied_in | int | positive
| packets_in | int | positive
| packets_out | int | positive
| proto | string, int | icmp tcp udp ipip esp icmp6
| policies.all_policies | string
| source_ip | IP, CIDR
| source_name | domain
| source_name_aggr | domain
| source_namespace | domain
| source_port | int | 0 - 65535
| source_type | string | wep, hep, ns, net
| source_labels.labels | string | LABEL_NAME=LABEL_VALUE | application=intrusion-detection-controller
| original_source_ips | IP, CIDR
| num_original_source_ips | int | positive

[Audit logs]: ../../security/logs/elastic/ee-audit
[audit.k8s.io group v1]: https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/apiserver/pkg/apis/audit/v1/types.go
[DNS logs]: ../../security/logs/elastic/dns
[Flow logs]: ../../security/logs/elastic/flow
[duration]: https://golang.org/pkg/time/#ParseDuration
