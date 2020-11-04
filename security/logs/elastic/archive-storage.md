---
title: Archive logs to storage
description: Archive logs to S3, Syslog, or Splunk for maintaining compliance data.
---

### Big picture

Archive logs to Amazon S3, Syslog, or Splunk to meet data storage requirements for compliance.

### Value

Archiving your {{site.prodname}} Elasticsearch logs to storage services like Amazon S3, Syslog, or Splunk are reliable 
options for maintaining and consolidating your compliance data long term

### Features

This how-to guide uses the following {{site.prodname}} features:
- **LogCollector** resource

### How to

* [Archive logs to Amazon S3](#archive-logs-to-amazon-s3)
* [Archive logs to Syslog](#archive-logs-to-syslog)
* [Archive logs to Splunk](#archive-logs-to-splunk)


#### Archive logs to Amazon S3

1. Create an AWS bucket to store your logs.
   You will need the bucket name, region, key, secret key, and the path in the following steps.

2. Create a Secret in the `tigera-operator` namespace named `log-collector-s3-credentials` with the fields `key-id` and `key-secret`.
   Example:

   ```
    kubectl create secret generic log-collector-s3-credentials \
    --from-literal=key-id=<AWS-access-key-id> \
    --from-literal=key-secret=<AWS-secret-key> \
    -n tigera-operator
   ```

3. Update the [LogCollector]({{site.baseurl}}/reference/installation/api#operator.tigera.io/v1.LogCollector)
   resource named, `tigera-secure` to include an [S3 section]({{site.baseurl}}/reference/installation/api#operator.tigera.io/v1.S3StoreSpec)
   with your information noted from above.
   Example:

   ```
   apiVersion: operator.tigera.io/v1
   kind: LogCollector
   metadata:
     name: tigera-secure
   spec:
     additionalStores:
       s3:
         bucketName: <S3-bucket-name>
         bucketPath: <path-in-S3-bucket>
         region: <S3-bucket region>
   ```
   This can be done during installation by editing the custom-resources.yaml
   by applying it, or after installation by editing the resource with the command:

   ```
   kubectl edit logcollector tigera-secure
   ```

#### Archive logs to Syslog

1. Update the
   [LogCollector]({{site.baseurl}}/reference/installation/api#operator.tigera.io/v1.LogCollector)
   resource named `tigera-secure` to include
   a [Syslog section]({{site.baseurl}}/reference/installation/api#operator.tigera.io/v1.SyslogStoreSpec)
   with your syslog information.
   Example:
   ```
   apiVersion: operator.tigera.io/v1
   kind: LogCollector
   metadata:
     name: tigera-secure
   spec:
     additionalStores:
       syslog:
         # (Required) Syslog endpoint, in the format protocol://host:port
         endpoint: tcp://1.2.3.4:514
         # (Optional) If messages are being truncated set this field
         packetSize: 1024
   ```
   This can be done during installation by editing the custom-resources.yaml by applying it or after installation by editing the resource with the command:
   ```
   kubectl edit logcollector tigera-secure
   ```
2. You can control which types of {{site.prodname}} log data you would like to send to syslog. 
   The [Syslog section]({{site.baseurl}}/reference/installation/api#operator.tigera.io/v1.SyslogStoreSpec) 
   contains a field called `logTypes` which allows you to list which log types you would like to include. 
   The allowable log types are:
    - Audit
    - DNS
    - Flows
    - IDSEvents

   Refer to the [Syslog section]({{site.baseurl}}/reference/installation/api#operator.tigera.io/v1.SyslogStoreSpec) for more details on what data each log type represents.

   Building on the example from the previous step:
   ```
   apiVersion: operator.tigera.io/v1
   kind: LogCollector
   metadata:
     name: tigera-secure
   spec:
     additionalStores:
       syslog:
         # (Required) Syslog endpoint, in the format protocol://host:port
         endpoint: tcp://1.2.3.4:514
         # (Optional) If messages are being truncated set this field
         packetSize: 1024
         # (Required) Types of logs to forward to Syslog (must specify at least one option)
         logTypes:
         - Audit
         - DNS
         - Flows
         - IDSEvents
   ```

   > **Note**: The log type `IDSEvents` is only supported for a cluster that has [LogStorage]({{site.baseurl}}/reference/installation/api#operator.tigera.io/v1.LogStorage) configured. Is is because intrusion detection event data is pulled from the corresponding LogStorage datastore directly. 
   {: .alert .alert-info}

   The `logTypes` field is a required, which means you must specify at least one type of log to export to syslog.

#### Archive logs to Splunk

{{site.prodname}} uses Splunk's **HTTP Event Collector** to send data to Splunk server. To copy the flow, audit, and dns logs to Splunk, follow these steps:

1. Create a HTTP Event Collector token by following the steps listed in Splunk's documentation for your specific Splunk version. Here is the link to do this for {% include open-new-window.html text='Splunk version 8.0.0' url='https://docs.splunk.com/Documentation/Splunk/8.0.0/Data/UsetheHTTPEventCollector' %}.

2. Create a Secret in the `tigera-operator` namespace named `logcollector-splunk-credentials` with the field `token`.
   Example:

   ```
    kubectl create secret generic logcollector-splunk-credentials \
    --from-literal=token=<splunk-hec-token> \
    -n tigera-operator
   ```

3. Update the
   [LogCollector]({{site.baseurl}}/reference/installation/api#operator.tigera.io/v1.LogCollector)
   resource named `tigera-secure` to include
   a [Splunk section]({{site.baseurl}}/reference/installation/api#operator.tigera.io/v1.SplunkStoreSpec)
   with your Splunk information.
   Example:
   ```
   apiVersion: operator.tigera.io/v1
   kind: LogCollector
   metadata:
     name: tigera-secure
   spec:
     additionalStores:
       splunk:
         # Splunk HTTP Event Collector endpoint, in the format protocol://host:port
         endpoint: https://1.2.3.4:8088
   ```
   This can be done during installation by editing the custom-resources.yaml
   by applying it or after installation by editing the resource with the command:
   ```
   kubectl edit logcollector tigera-secure
   ```
