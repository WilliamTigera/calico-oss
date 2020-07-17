Download the {{site.prodname}} manifests for OpenShift and add them to the generated manifests directory:

```bash
curl {{ "/manifests/ocp/crds/01-crd-apiserver.yaml" | absolute_url }} -o manifests/01-crd-apiserver.yaml
curl {{ "/manifests/ocp/crds/01-crd-authentication.yaml" | absolute_url }} -o manifests/01-crd-authentication.yaml
curl {{ "/manifests/ocp/crds/01-crd-compliance.yaml" | absolute_url }} -o manifests/01-crd-compliance.yaml
curl {{ "/manifests/ocp/crds/01-crd-manager.yaml" | absolute_url }} -o manifests/01-crd-manager.yaml
curl {{ "/manifests/ocp/crds/01-crd-eck-apmserver.yaml" | absolute_url }} -o manifests/01-crd-eck-apmserver.yaml
curl {{ "/manifests/ocp/crds/01-crd-eck-elasticsearch.yaml" | absolute_url }} -o manifests/01-crd-eck-elasticsearch.yaml
curl {{ "/manifests/ocp/crds/01-crd-eck-kibana.yaml" | absolute_url }} -o manifests/01-crd-eck-kibana.yaml
curl {{ "/manifests/ocp/crds/01-crd-eck-trustrelationship.yaml" | absolute_url }} -o manifests/01-crd-eck-trustrelationship.yaml
curl {{ "/manifests/ocp/crds/01-crd-installation.yaml" | absolute_url }} -o manifests/01-crd-installation.yaml
curl {{ "/manifests/ocp/crds/01-crd-intrusiondetection.yaml" | absolute_url }} -o manifests/01-crd-intrusiondetection.yaml
curl {{ "/manifests/ocp/crds/01-crd-logstorage.yaml" | absolute_url }} -o manifests/01-crd-logstorage.yaml
curl {{ "/manifests/ocp/crds/01-crd-logcollector.yaml" | absolute_url }} -o manifests/01-crd-logcollector.yaml
curl {{ "/manifests/ocp/crds/01-crd-tigerastatus.yaml" | absolute_url }} -o manifests/01-crd-tigerastatus.yaml
curl {{ "/manifests/ocp/crds/01-crd-managementclusterconnection.yaml" | absolute_url }} -o manifests/01-crd-managementclusterconnection.yaml
curl {{ "/manifests/ocp/crds/01-crd-managementcluster.yaml" | absolute_url }} -o manifests/01-crd-managementcluster.yaml
{%- for data in site.static_files %}
{%- if data.path contains '/manifests/ocp/crds/calico' %}
curl {{ data.path | absolute_url }} -o manifests/{{data.name}}
{%- endif -%}
{% endfor %}
curl {{ "/manifests/ocp/tigera-operator/00-namespace-tigera-operator.yaml" | absolute_url }} -o manifests/00-namespace-tigera-operator.yaml
curl {{ "/manifests/ocp/tigera-operator/02-rolebinding-tigera-operator.yaml" | absolute_url }} -o manifests/02-rolebinding-tigera-operator.yaml
curl {{ "/manifests/ocp/tigera-operator/02-role-tigera-operator.yaml" | absolute_url }} -o manifests/02-role-tigera-operator.yaml
curl {{ "/manifests/ocp/tigera-operator/02-serviceaccount-tigera-operator.yaml" | absolute_url }} -o manifests/02-serviceaccount-tigera-operator.yaml
curl {{ "/manifests/ocp/tigera-operator/02-configmap-calico-resources.yaml" | absolute_url }} -o manifests/02-configmap-calico-resources.yaml
curl {{ "/manifests/ocp/tigera-operator/02-configmap-tigera-install-script.yaml" | absolute_url }} -o manifests/02-configmap-tigera-install-script.yaml
curl {{ "/manifests/ocp/tigera-operator/02-tigera-operator.yaml" | absolute_url }} -o manifests/02-tigera-operator.yaml
curl {{ "/manifests/ocp/misc/00-namespace-tigera-prometheus.yaml" | absolute_url }} -o manifests/00-namespace-tigera-prometheus.yaml
curl {{ "/manifests/ocp/prometheus-operator/04-clusterrolebinding-prometheus.yaml" | absolute_url }} -o manifests/04-clusterrolebinding-prometheus.yaml
curl {{ "/manifests/ocp/prometheus-operator/04-clusterrole-prometheus.yaml" | absolute_url }} -o manifests/04-clusterrole-prometheus.yaml
curl {{ "/manifests/ocp/prometheus-operator/04-serviceaccount-prometheus.yaml" | absolute_url }} -o manifests/04-serviceaccount-prometheus.yaml
curl {{ "/manifests/ocp/misc/99-alertmanager-secret.yaml" | absolute_url }} -o manifests/99-alertmanager-secret.yaml
curl {{ "/manifests/ocp/misc/99-alertmanager-service.yaml" | absolute_url }} -o manifests/99-alertmanager-service.yaml
curl {{ "/manifests/ocp/misc/99-prometheus-service.yaml" | absolute_url }} -o manifests/99-prometheus-service.yaml
```
