---
title: Job descriptions
description: Summary of the anomaly detection jobs.
canonical_url: /threat/anomaly-detection/job-descriptions
---

The following anomaly detection jobs are included in {{site.prodname}}.

### IP Sweep detection
**Job ID**: `ip_sweep`

The job looks for pods in your cluster that are sending packets to many destinations. This may indicate
an attacker has gained control of a pod and is gathering reconnaissance on what else they can reach. The job
compares pods both with other pods in their replica set, and with other pods in the cluster generally. 

### Port Scan detection
**Job ID**: `port_scan`

The job looks for pods in your cluster that are sending packets to one destination on multiple ports. This may indicate
an attacker has gained control of a pod and is gathering reconnaissance on what else they can reach. The job
compares pods both with other pods in their replica set, and with other pods in the cluster generally.

### Inbound Service bytes anomaly 
**Job ID**: `bytes_in`

The job looks for services that receive an anomalously high amount of data.  This could indicate a
denial of service attack, data exfiltrating, or other attacks. The job looks for services that are unusual
with respect to their replica set, and replica sets which are unusual with respect to the rest of the cluster.

### Outbound Service bytes anomaly 
**Job ID**: `bytes_out`

The job looks for pods that send an anomalously high amount of data.  This could indicate a
denial of service attack, data exfiltrating, or other attacks. The job looks for pods that are unusual
with respect to their replica set, and replica sets which are unusual with respect to the rest of the cluster.

### DNS Latency anomaly 
**Job ID**: `dns_latency`

The job looks for the clients that have too high latency of the DNS requests. This could indicate a 
denial of service attack.


### L7 Latency anomaly 
**Job ID**: `l7_latency`

The job looks for the pods that have too high latency of the L7 requests. All HTTP requests measured here. 
This anomaly could indicate a denial of service attack or other attacks.

