{% if include.orch != "openshift" %}
  {% assign cli = "kubectl" %}
{% else %}
  {% assign cli = "oc" %}
{% endif %}

1. Set up secret with username and password for Fluentd to authenticate with Elasticsearch.
   Replace `<fluentd-elasticsearch-password>` with the password.
   ```
   {{cli}} create secret generic elastic-fluentd-user \
   --from-literal=username=tigera-ee-fluentd \
   --from-literal=password=<fluentd-elasticsearch-password> \
   -n calico-monitoring
   ```

1. Set up secret with username and password for Curator to authenticate with Elasticsearch.
   Replace `<curator-elasticsearch-password>` with the password.
   ```
   {{cli}} create secret generic elastic-curator-user \
   --from-literal=username=tigera-ee-curator \
   --from-literal=password=<curator-elasticsearch-password> \
   -n calico-monitoring
   ```

1. Set up secret with username and password for {{site.prodname}} intrusion detection controller to authenticate with Elasticsearch.
   Replace `<intrusion-detection-password>` with the password.
   ```
   {{cli}} create secret generic elastic-ee-intrusion-detection \
   --from-literal=username=tigera-ee-intrusion-detection \
   --from-literal=password=<intrusion-detection-password> \
   -n calico-monitoring
   ```


1. Set up secret with username and password for {{site.prodname}} compliance report and dashboard to authenticate with Elasticsearch.
   Replace `<compliance-elasticsearch-password>` with the password.
   ```
   {{cli}} create secret generic elastic-compliance-user \
   --from-literal=username=tigera-ee-compliance \
   --from-literal=password=<compliance-elasticsearch-password> \
   -n calico-monitoring
   ```



1. Set up configmap with the certificate authority certificate to authenticate Elasticsearch & Kibana.
   Replace `<ElasticsearchCA.pem>` with the path to your Elasticsearch/Kibana CA certificate.

   ```bash
   cp <ElasticsearchCA.pem> ca.pem
   {{cli}} create configmap -n calico-monitoring elastic-ca-config --from-file=ca.pem
   ```

1. Create a Secret containing
   * TLS certificate and the private key used to sign it enable TLS connection from the kube-apiserver to the es-proxy
   * certificate authority certificate to authenticate Elasticsearch backend
   * Base64 encoded `<username>:<password>` for the es-proxy to authenticate with Elasticsearch

   Replace `<ee-manager-elasticsearch-password>` with the password.

   ```
   {{cli}} create secret generic tigera-es-proxy \
   --from-file=frontend.crt=frontend-server.crt \
   --from-file=frontend.key=frontend-server.key \
   --from-file=backend-ca.crt=ElasticSearchCA.pem \
   --from-literal=backend.authHeader=$(echo -n tigera-ee-manager:<ee-manager-elasticsearch-password> | base64) \
   -n calico-monitoring
   ```

1. Set up secret with username and password for the {{site.prodname}} job installer to authenticate with Elasticsearch.
   Replace `<installer-password>` with the password.
   ```
   {{cli}} create secret generic elastic-ee-installer \
   --from-literal=username=tigera-ee-installer \
   --from-literal=password=<installer-password> \
   -n calico-monitoring
   ```

1. Create a configmap with information on how to reach the Elasticsearch cluster.
   Replace `<elasticsearch-host>` with the hostname (or IP) {{site.prodname}} should access Elasticsearch through.
   If your cluster is listening on a port other than `9200`, replace that too.
   Replace `<kibana-host>` with the hostname (or IP) {{site.prodname}} should access Kibana through. If Kibana
   is listening on a port other than `5601`, replace that too.
   ```
   {{cli}} create configmap tigera-es-proxy \
   --from-literal=elasticsearch.backend.host="<elasticsearch-host>" \
   --from-literal=elasticsearch.backend.port="9200" \
   --from-literal=kibana.backend.host="<kibana-host>" \
   --from-literal=kibana.backend.port="5601" \
   -n calico-monitoring
   ```

1. Download the configmap patch for {{site.prodname}} Manager.
    ```
    curl --compressed -O {{site.url}}/{{page.version}}/getting-started/kubernetes/installation/hosted/cnx/1.7/secure-es/patch-cnx-manager-configmap.yaml
    ```
    Edit the Kibana URL in the patch file to point to your Kibana.

1. Apply the configmap patch.
   ```
   {{cli}} patch configmap tigera-cnx-manager-config -n kube-system -p "$(cat patch-cnx-manager-configmap.yaml)"
   ```
1. Restart {{site.prodname}} Manager pod
   ```
   kubectl delete pod -n kube-system -l k8s-app=cnx-manager
   ```
