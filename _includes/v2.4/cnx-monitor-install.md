{% if include.orch != "openshift" %}
  {% capture docpath %}{{site.url}}/{{page.version}}/getting-started/kubernetes/installation/hosted/cnx/1.7{% endcapture %}
  {% assign cli = "kubectl" %}
{% else %}
  {% capture docpath %}{{site.url}}/{{page.version}}/getting-started/openshift{% endcapture %}
  {% assign cli = "oc" %}
{% endif %}
{% if include.elasticsearch == "operator" %}
  {% assign operators = "Prometheus and Elasticsearch" %}
  {% assign secure = "" %}
{% else %}
  {% assign operators = "Prometheus" %}
  {% assign secure = "/secure-es" %}
{% endif %}

{% unless include.upgrade %}
## Installing metrics and logs
{% endunless %}

{% if include.orch == "openshift" %}

### Enable Metrics

Below, we'll cover how to enable metrics in {{site.prodname}} and how to launch Prometheus using Prometheus-Operator.

**Prerequisite**: `calicoctl` [installed](/{{page.version}}/getting-started/calicoctl/install) and [configured](/{{page.version}}/getting-started/calicoctl/configure/). We recommend [installing](/{{page.version}}/getting-started/calicoctl/install#installing-calicoctl-as-a-container-on-a-single-host) calicoctl as a container in OpenShift.

Enable metrics in {{site.prodname}} for OpenShift by updating the global `FelixConfiguration` resource (`default`) and opening up the necessary port on the host.

{% include {{page.version}}/enable-felix-prometheus-reporting.md %}

1. Allow Prometheus to scrape the metrics by opening up the port on each host:

   ```
   iptables -I INPUT -p tcp --dport 9081 -j ACCEPT
   ```

### Configure metrics and logs

With metrics enabled, you are ready to monitor `{{site.nodecontainer}}` by scraping the endpoint on each node
in the cluster. If you do not have your own Prometheus, the following commands will launch a Prometheus
Operator, Prometheus, and Alertmanager instances for you. They will also deploy Fluentd{% if include.elasticsearch == "operator" %}, and
optionally Elasticsearch and Kibana{% endif %} in order to enable logs.

{% unless include.upgrade %}
1. For production installs, we recommend using your own Elasticsearch cluster. If you are performing a
   production install, do not complete any more steps on this page. Instead, refer to
   [Using your own Elasticsearch for logs](byo-elasticsearch) for the final steps.

   For demonstration or proof of concept installs, you can use the bundled
   [Elasticsearch operator](https://github.com/upmc-enterprises/elasticsearch-operator). Continue to the
   next step to complete a demonstration or proof of concept install.

   > **Important**: The bundled Elasticsearch operator does not provide reliable persistent storage
   of logs or authenticate access to Kibana.
   {: .alert .alert-danger}
{% endunless %}

1. Download the flow logs patch for {{site.prodname}} node.

   ```
   curl --compressed -O {{docpath}}/patch-flow-logs.yaml
   ```

1. Apply the flow logs patch.

   ```
   oc patch daemonset {{site.noderunning}} -n kube-system --patch "$(cat patch-flow-logs.yaml)"
   ```

{% else %}
{% unless include.elasticsearch == "external" %}

1. For production installs, follow the instructions [here](byo-elasticsearch) to configure {{site.prodname}}
   to use your own Elasticsearch cluster.  For demo / proof of concept installs using the bundled Elasticsearch
   operator continue to the next step instead.

   > **Important**: The bundled Elasticsearch operator does not provide reliable persistent storage
   of logs or authenticate access to Kibana.
   {: .alert .alert-danger}
{% endunless %}
{% endif %}

1. Apply the following manifest to set network policy that allows access to the {{site.prodname}} API server.

   ```bash
   {{cli}} apply -f \
   {{site.url}}/{{page.version}}/getting-started/kubernetes/installation/hosted/cnx/1.7/cnx-policy.yaml
   ```

   > **Note**: You can also
   > [view the manifest in a new tab]({{site.url}}/{{page.version}}/getting-started/kubernetes/installation/hosted/cnx/1.7/cnx-policy.yaml){:target="_blank"}.
   {: .alert .alert-info}

1. Download the `operator.yaml` manifest.

{% if include.platform == "docker-ee" %}
   ```bash
   curl --compressed -o operator.yaml \
   {{docpath}}{{secure}}/docker-ee-operator.yaml
   ```
   > **Important**: The bundled Elasticsearch operator requires running pods using Restricted Parameters
   (privileged mode, host PID, and `IPC_LOCK` capability) which requires a `ClusterRoleBinding` with
   `cluster-admin` permissions with Docker Enterprise. See [UCP release notes](https://docs.docker.com/ee/ucp/release-notes/)
   {: .alert .alert-danger}
{% else %}
   ```bash
   curl --compressed -O \
   {{docpath}}{{secure}}/operator.yaml
   ```
{% endif %}

1. Use the following commands to set an environment variable called `REGISTRY` containing the
   location of the private registry and replace the paths in the manifest to refer to
   the private registry.

    ```bash
    REGISTRY=my-registry.com \
    sed -i -e "s?quay.io?$REGISTRY?g" operator.yaml {% if include.elasticsearch == "operator" %}\
    sed -i -e "s?upmcenterprises?$REGISTRY/upmcenterprises?g" operator.yaml{% endif %}
    ```

    > **Tip**: If you're hosting your own private registry, you may need to include
    > a port number. For example, `my-registry.com:5000`.
    {: .alert .alert-success}

1. Apply the manifest.

   ```bash
   {{cli}} apply -f operator.yaml
   ```

{% if include.elasticsearch == "operator" %}
1. Wait for the `alertmanagers.monitoring.coreos.com`, `prometheuses.monitoring.coreos.com`, `servicemonitors.monitoring.coreos.com`,
   `prometheusrules.monitoring.coreos.com` and `elasticsearchclusters.enterprises.upmc.com` custom resource definitions to be created. Check by running:
{% else %}
1. Wait for the `alertmanagers.monitoring.coreos.com`, `prometheuses.monitoring.coreos.com`, `prometheusrules.monitoring.coreos.com`
   and `servicemonitors.monitoring.coreos.com` custom resource definitions to be created. Check by running:
{% endif %}

   ```
   {{cli}} get customresourcedefinitions
   ```

{% if include.orch == "openshift" %}
1. Allow the monitoring pods to be scheduled on the master node. This allows fluentd to be scheduled on master nodes to collect flow and audit logs.

   ```
   {{cli}} annotate ns calico-monitoring openshift.io/node-selector="" --overwrite
   ```

{% endif %}

{% if include.elasticsearch == "operator" %}
{% include {{page.version}}/elastic-storage.md orch=include.orch %}
{% endif %}

1.  Download the `monitor-calico.yaml` manifest.

{% if include.upgrade %}
    ```bash
    curl --compressed -o monitor-calico.yaml \
    {{docpath}}{{secure}}/monitor-calico-upgrade.yaml
    ```
{% else %}
    ```bash
    curl --compressed -O \
    {{docpath}}{{secure}}/monitor-calico.yaml
    ```
{% endif %}

1. Update the `tigera-es-config` configmap with information on how to reach the BYO Elasticsearch cluster.
   Replace `<elasticsearch-host>` with the hostname (or IP) {{site.prodname}} should access Elasticsearch through.
   Replace `<elasticsearch-port>` with the port Elasticsearch is listening on.
   Replace `<kibana-host>` with the hostname (or IP) {{site.prodname}} should access Kibana through.
   Replace `<kibana-port>` with the port Kibana is listening on.
   ```
   sed -i -e "s?__ELASTICSEARCH_HOST__?<elasticsearch-host>?g" monitor-calico.yaml
   sed -i -e "s?__ELASTICSEARCH_PORT__?<elasticsearch-port>?g" monitor-calico.yaml
   sed -i -e "s?__KIBANA_HOST__?<kibana-host>?g" monitor-calico.yaml
   sed -i -e "s?__KIBANA_PORT__?<kibana-port>?g" monitor-calico.yaml
   ```


{% include {{page.version}}/cnx-cred-sed.md yaml="monitor-calico" %}
{% if include.platform == "docker-ee" %}
1. Update the `calico-node-alertmanager` service section and replace `nodePort: 30903` with `nodePort: 33903`

1. Update the `calico-node-prometheus` service section and replace `nodePort: 30909` with `nodePort: 33909`

1. Update the `kibana-tigera-elasticsearch` service section and replace `nodePort: 30601` with `nodePort: 33601`

   > **Note**: Docker Enterprise requires non-reserved port ranges to be above 32000.
   {: .alert .alert-info}

{% endif %}
1. Apply the manifest.

   ```bash
   {{cli}} apply -f monitor-calico.yaml
   ```

{% if include.upgrade %}
1. Remove unused components from previous installation

   ```bash
   {{cli}} delete -n calico-monitoring service elasticsearch-tigera-elasticsearch
   {{cli}} delete globalnetworkset.projectcalico.org k8sapi-endpoints
   {{cli}} delete globalnetworkpolicy.projectcalico.org allow-cnx.es-proxy-access
   {{cli}} delete -n calico-monitoring deployment tigera-es-proxy
   {{cli}} delete -n calico-monitoring serviceaccount tigera-es-proxy
   {{cli}} delete -n calico-monitoring configmap tigera-es-proxy
   {{cli}} delete -n calico-monitoring configmap elastic-ca-config
   ```
{% endif %}

{% if include.orch == "openshift" %}
{% if include.elasticsearch == "operator" %}

1. Reconfigure the Elasticsearch deployment. The following command will save the current configuration
   to `tigera-elasticsearch.yaml`.

   ```
   oc get deployment es-client-tigera-elasticsearch -n calico-monitoring -o yaml --export > tigera-elasticsearch.yaml
   ```

   Run the following command which will fix the configuration for pods to start properly in OpenShift.

   ```
   sed -i '/capabilities/,+2 d' tigera-elasticsearch.yaml
   ```

   Replace the running deployment.
   ```
   oc replace -n calico-monitoring -f tigera-elasticsearch.yaml
   ```

1. Remove the ReplicaSet from the deployment we replaced. You can find this ReplicaSet with the following command.

   ```
   oc get rs -n calico-monitoring
   ```

   The ReplicaSet we will want to replace will have 0 `DESIRED`, `CURRENT`, and `READY` pods. In the following example
   output, `es-client-tigera-elasticsearch-5ddd8dfdfd` is the ReplicaSet we will want to remove.

   ```
   NAME                                        DESIRED   CURRENT   READY     AGE
   calico-prometheus-operator-74dd985b6f       1         1         1         3h
   elasticsearch-operator-5c84946f57           1         1         1         3h
   es-client-tigera-elasticsearch-5ddd8dfdfd   0         0         0         3h
   es-client-tigera-elasticsearch-759997fcbb   1         1         1         19m
   kibana-tigera-elasticsearch-6cb8879697      1         1         1         3h
   ```
   {: .no-select-button}

   Remove the chosen ReplicaSet with the following command.

   ```
   oc delete rs <YOUR-REPLICASET> -n calico-monitoring
   ```

1. Reconfigure the Elasticsearch data storage. The following will save the current configuration to
   `data-tigera-elasticsearch-storage.yaml`.

   ```
   oc get statefulset es-data-tigera-elasticsearch-elasticsearch-storage -n calico-monitoring -o yaml --export > data-tigera-elasticsearch-storage.yaml
   ```

   Run the following command which will fix the configuration for pods to start properly in OpenShift.

   ```
   sed -i '/capabilities/,+2 d' data-tigera-elasticsearch-storage.yaml
   ```

   Replace the running StatefulSet.

   ```
   oc replace -n calico-monitoring -f data-tigera-elasticsearch-storage.yaml
   ```

1. Reconfigure the Elasticsearch master storage. The following will save the current configuration to
   `master-tigera-elasticsearch-storage.yaml`.

   ```
   oc get statefulset es-master-tigera-elasticsearch-elasticsearch-storage -n calico-monitoring -o yaml --export > master-tigera-elasticsearch-storage.yaml
   ```

   Run the following command which will fix the configuration for pods to start properly in OpenShift.

   ```
   sed -i '/capabilities/,+2 d' master-tigera-elasticsearch-storage.yaml
   ```

   Replace the running StatefulSet.

   ```
   oc replace -n calico-monitoring -f master-tigera-elasticsearch-storage.yaml
   ```

1. Reconfigure the Elasticsearch sysctl operator. The following will save the current configuration to
   `elasticsearch-operator-sysctl.yaml`.

   ```
   oc get ds elasticsearch-operator-sysctl -n default -o yaml --export > elasticsearch-operator-sysctl.yaml
   ```

   Run the following command which will fix the configuration for pods to start properly in OpenShift.

   ```
   sed -i '/hostPID/d' elasticsearch-operator-sysctl.yaml
   ```

   Replace the running DaemonSet

   ```
   oc replace -n default -f elasticsearch-operator-sysctl.yaml
   ```

{% endif %}
{% endif %}

1. Access the Kibana UI.

{% if include.platform == "docker-ee" %}
   Kibana will be accessible on the `tigera.cnx-manager.kibana-url` value specified in `cnx.yaml` You may need to create a ssh tunnel if
   the node is not accessible. For example:

   ```bash
   ssh <jumpbox> -L 127.0.0.1:33601:<docker node>:33601
   ```

{% elsif include.elasticsearch == "operator" %}
   By default, Kibana is made accessible via a NodePort listening on port 30601

{% if include.orch == "openshift" %}
   You may need to create an OpenShift route or Ingress if the NodePort is not accessible.

   Ensure that the Route is created with tls termination set to Edge. Also, ensure that the host
   specified in the route is resolvable from within the cluster, and to update cnx.yaml with the
   hostname as specified in the route, apply the change and restart the cnx-manager pod.

{% else %}
   You may need to create an ssh tunnel if the node is not accessible - for example:

   ```bash
   ssh <jumpbox> -L 127.0.0.1:30601:<kubernetes node>:30601
   ```
{% endif %}
{% endif %}

1. Open the **Management** -> **Index Patterns** pane in Kibana, select one of the imported index patterns and click the star to set it as the
   default pattern. Refer to the [Kibana documentation](https://www.elastic.co/guide/en/kibana/current/index-patterns.html#set-default-pattern)
   for more details.

{% if include.type == "policy-only" and include.orch != "openshift" %}
1. Optionally enable either or both of the following:
   * To enforce application layer policies and secure workload-to-workload
    communications with mutually-authenticated TLS, continue to
	[Enabling application layer policy]({{site.baseurl}}/{{page.version}}/getting-started/kubernetes/installation/app-layer-policy).

   * If you are using the AWS CNI plugin and want to enforce granular access
    control between pods and AWS VPC resources, continue to
    [Enabling integration with AWS security groups]({{site.baseurl}}/{{page.version}}/getting-started/kubernetes/installation/aws-sg-integration).
{% endif %}
