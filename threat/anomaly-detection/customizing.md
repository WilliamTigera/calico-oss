---
title:  Configuration
description: Review your options for customizing the anomaly detections jobs.
canonical_url: /threat/anomaly-detection/customizing

---

{{site.prodname}} ships with proprietary anomaly detection jobs.
All proprietary anomaly detection jobs are included in one deployment manifest and work in one pod.

## Install Jobs
Use these commands to deploy a pod with the anomaly detection jobs:

1. Download a manifest:
There are two options, the anomaly detection in the **managed** cluster and in the **management/standalone** cluster.
   See the [Multi-cluster management] for more details.

    1.1 For the **management** or **standalone** cluster:
    ```bash
    curl {{ "/manifests/threatdef/ad-jobs-deployment.yaml" | absolute_url }} -O
    ```
    1.2 For the **managed** cluster:
    ```bash
    curl {{ "/manifests/threatdef/ad-jobs-deployment-managed.yaml" | absolute_url }} -O
    ```

2. Configure the jobs by setting the environment variables (see below).

   If it is a managed cluster, you have to set up the **CLUSTER_NAME** environment variable.

   All other settings are optional.

3. Apply the manifest

    1.1 For the **management** or **standalone** cluster:
    ```bash
    kubectl apply -f ad-jobs-deployment.yaml
    ```
    1.2 For the **managed** cluster:
    ```bash
    kubectl apply -f ad-jobs-deployment-managed.yaml
    ```

## Configure Jobs
You can configure the jobs using the environment variables.
The recommended way to set them up is in the deployment manifest (the yaml file).

Example:

```
env:
 - name: AD_max_docs
   value: "2000000"
 - name: AD_train_interval_minutes
   value: "20"
```

### Disabling jobs
Jobs can be disabled. It usually happens if some job creates too many alerts,
and its configuration requires time for research.
- **AD_DISABLED_JOBS** - Default: "". By default, all jobs are enabled.
To disable several jobs, separate job names with `,`.
For example: `http_connection_spike,dga`




### Variables of the Elasticsearch
-   **CLUSTER_NAME** - Default: "cluster".
In a multi-cluster deployment, the name of the cluster where the AD job should detect the anomalies.
-   **ES_query_size** - Default: 10000.
The job reads the data in portions. This is the number of rows in each of this portion.
-   **ES_scroll_time** - Default: "20s".
This is the timeout for reading each data portion.
-   **ES_bucket_size_minutes** - Default: 5.
The log rows are aggregated into the buckets. This is the size of these buckets. The bucket always starts at the averaged minute. For example, for the bucket size of 5 minutes, a bucket starts on the x0th or x5th minute.

### Variables of all Anomaly Detection Jobs
-   **AD_train_interval_minutes** - Default: 1440, a full day. It is an interval between retraining the existing models, if models should be retrained.
-   **AD_search_interval_minutes** - Default: 30. It is an interval between the searching for the anomalies.
-   **AD_max_docs** - Default: 500000. This is the size of the dataset used for the training. The bigger it is, the more precise are the trained models, but the more data read from the Elasticsearch storage, and the training takes more time.

### Variables of specific Anomaly Detection Jobs

#### port_scan Job
-   **AD_port_scan_threshold** - Default: 500. It is a threshold for triggering an anomaly for the **port_scan** job. This is a number of unique destination ports called from the specific source_name_aggr in the same source_namespace, and the same bucket.

#### ip_sweep Job
-   **AD_ip_sweep_threshold** - Default: 32. It is a threshold for triggering an anomaly for the **ip_sweep** job. This is a number of unique destination IPs called from the specific source_name_aggr in the same source_namespace, and the same bucket.

#### bytes_out Job
-   **AD_BytesOutModel_min_size_for_train** - Default: 1000. There should be enough data samples to train models.
    The models trained only if the number of the data samples is bigger than this threshold parameter.
-   **AD_BytesOutModel_SeasonalAD_c** - Default: 500. Increase this parameter if you want fewer alerts.
    Decrease it if you want more alerts.

#### bytes_in Job
-   **AD_BytesInModel_min_size_for_train** - Default: 1000. There should be enough data samples to train models.
    The models trained only if the number of the data samples is bigger than this threshold parameter.
-   **AD_BytesInModel_SeasonalAD_c** - Default: 500. Increase this parameter if you want fewer alerts.
    Decrease it if you want more alerts.

#### process_restarts Job
Now the [IsolationForest model] from scikit-learn is used in this detector.
-   **AD_ProcessRestarts_IsolationForest_score_threshold** - Default: -0.78. Note: it is a negative value!
    Decrease this parameter if you want fewer alerts. Increase it if you want more alerts.
-   **AD_ProcessRestarts_threshold** - Default: 4. Increase this parameter if you want fewer alerts.
    Decrease it if you want more alerts.

#### dns_latency Job
Now the [IsolationForest model] from scikit-learn is used in this detector.
-   **AD_DnsLatency_IsolationForest_n_estimators** - Default: 100. The more data samples presented to train model, the more
    estimators needed.
-   **AD_DnsLatency_IsolationForest_score_threshold** - Default: -0.836. It is a negative number!
    Decrease this parameter if you want fewer alerts.
    Increase it if you want more alerts.

#### l7_latency Job
Now the [IsolationForest model] from scikit-learn is used in this detector.
-   **AD_L7Latency_IsolationForest_n_estimators** - Default: 100. The more data samples presented to train model, the more
    estimators needed.
-   **AD_L7Latency_IsolationForest_score_threshold** - Default: -0.836. It is a negative number!
    Decrease this parameter if you want fewer alerts.
    Increase it if you want more alerts.

#### http_connection_spike Job
Now the [IsolationForest model] from scikit-learn is used in this detector.
-   **AD_HttpConnectionSpike_IsolationForest_n_estimators** - Default: 100. The more data samples presented to train
    model, the more estimators needed.
-   **AD_HttpConnectionSpike_IsolationForest_score_threshold** - Default: -0.64. It is a negative number!
    Decrease this parameter if you want fewer alerts.
    Increase it if you want more alerts.

#### dga Job
Now the CatBoostClassifier model from the [CatBoost] package is used in this detector.
-   **AD_DGA_SCORE_THRESHOLD** - Default: 0.5. It separates the DGA domain names from "good" domain names.
    Increase this parameter if you want fewer alerts.
    Decrease it if you want more alerts.

#### generic_flows Job
Now the [IsolationForest model] from scikit-learn is used in this detector.
- **AD_GENERIC_FLOWS_FIELDS** - Default: "bytes_in,bytes_out,num_flows,num_flows_started,
    num_flows_completed,packets_in,packets_out,num_process_names,num_process_ids,num_original_source_ips" 
    
    It is a list of the `flow` log numeric fields separated by `,`.
    A separate model is trained for each field in this list.
    Remove a field from this list if you don't want to detect anomalies for it.
- **AD_GENERIC_FLOWS_ALERTS_PER_DAY** - Default: 2.5 
 
  Each model automatically tuned up to detect a desired number of alerts per day. It doesn't mean
  the model detects exactly this number of anomalies. This parameter changes the model sensitivity.

#### generic_dns Job
Now the [IsolationForest model] from scikit-learn is used in this detector.
- **AD_GENERIC_DNS_FIELDS** - Default: "count,latency_count,latency_mean,latency_max"
    
    It is a list of the `DNS` log numeric fields separated by `,`.
    A separate model is trained for each field in this list.
    Remove a field from this list if you don't want to detect anomalies for it.
- **AD_GENERIC_DNS_ALERTS_PER_DAY** - Default: 2.5 
 
  Each model automatically tuned up to detect a desired number of alerts per day. It doesn't mean
  the model detects exactly this number of anomalies. This parameter changes the model sensitivity.

#### generic_l7 Job
Now the [IsolationForest model] from scikit-learn is used in this detector.
- **AD_GENERIC_L7_FIELDS** - Default: "duration_mean,duration_max,bytes_in,bytes_out,count"
    
    It is a list of the `L7` log numeric fields separated by `,`.
    A separate model is trained for each field in this list.
    Remove a field from this list if you don't want to detect anomalies for it.
- **AD_GENERIC_L7_ALERTS_PER_DAY** - Default: 2.5 
 
  Each model automatically tuned up to detect a desired number of alerts per day. It doesn't mean
  the model detects exactly this number of anomalies. This parameter changes the model sensitivity.

[Multi-cluster management]: /multicluster/index
[IsolationForest model]: https://scikit-learn.org/stable/modules/generated/sklearn.ensemble.IsolationForest.html
[CatBoost]: https://catboost.ai/docs
