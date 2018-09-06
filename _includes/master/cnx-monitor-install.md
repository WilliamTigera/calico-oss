1. If your cluster is connected to the internet, use the following command to apply the Prometheus
   and ElasticSearch operator manifest.

   ```
   kubectl apply -f \
   {{site.url}}/{{page.version}}/getting-started/kubernetes/installation/hosted/cnx/1.7/operator.yaml
   ```

   > **Note**: You can also
   > [view the manifest in a new tab]({{site.url}}/{{page.version}}/getting-started/kubernetes/installation/hosted/cnx/1.7/operator.yaml){:target="_blank"}.
   {: .alert .alert-info}

   > For offline installs, complete the following steps instead.
   >
   > 1. Download the Prometheus operator manifest.
   >
   >    ```bash
   >    curl --compressed -o \
   >    {{site.url}}/{{page.version}}/getting-started/kubernetes/installation/hosted/cnx/1.7/operator.yaml
   >    ```
   >
   > 1. Use the following commands to set an environment variable called `REGISTRY` containing the
   >    location of the private registry and replace `quay.io` in the manifest with the location
   >    of your private registry.
   >
   >    ```bash
   >    REGISTRY=my-registry.com \
   >    sed -i -e "s?quay.io?$REGISTRY?g" operator.yaml
   >    ```
   >
   >    **Tip**: If you're hosting your own private registry, you may need to include
   >    a port number. For example, `my-registry.com:5000`.
   >    {: .alert .alert-success}
   >    
   > 1. Apply the manifest.
   >    
   >    ```bash
   >    kubectl apply -f operator.yaml
   >    ```

1. Wait for the `alertmanagers.monitoring.coreos.com`, `prometheuses.monitoring.coreos.com`, `servicemonitors.monitoring.coreos.com` and
   `elasticsearchclusters.enterprises.upmc.com` custom resource definitions to be created. Check by running:

   ```
   kubectl get customresourcedefinitions
   ```

{% include {{page.version}}/elastic-storage.md %}

1. If your cluster is connected to the internet, use the following command to install Prometheus,
   Alertmanager and ElasticSearch.

   ```
   kubectl apply -f \
   {{site.url}}/{{page.version}}/getting-started/kubernetes/installation/hosted/cnx/1.7/monitor-calico.yaml
   ```

   > **Note**: You can also
   > [view the manifest in a new tab]({{site.url}}/{{page.version}}/getting-started/kubernetes/installation/hosted/cnx/1.7/monitor-calico.yaml){:target="_blank"}.
   {: .alert .alert-info}

   > For offline installs, complete the following steps instead.
   >
   > 1. Download the Prometheus and Alertmanager manifest.
   >
   >    ```
   >    curl --compressed -o \
   >    {{site.url}}/{{page.version}}/getting-started/kubernetes/installation/hosted/cnx/1.7/monitor-calico.yaml
   >    ```
   >      
   > 1. Use the following commands to set an environment variable called `REGISTRY` containing the
   >    location of the private registry and replace `quay.io` in the manifest with the location
   >    of your private registry.
   >
   >    ```bash
   >    REGISTRY=my-registry.com \
   >    sed -i -e "s?quay.io?$REGISTRY?g" monitor-calico.yaml
   >    ```
   >
   >    **Tip**: If you're hosting your own private registry, you may need to include
   >    a port number. For example, `my-registry.com:5000`.
   >    {: .alert .alert-success}
   >       
   > 1. Apply the manifest.
   >
   >    ```
   >    kubectl apply -f monitor-calico.yaml
   >    ```

1. If you wish to enforce application layer policies and secure workload-to-workload
   communications with mutual TLS authentication, continue to [Enabling application layer policy](app-layer-policy) (optional).
