## Applying your license key

{% if include.cli == 'calicoctl' %}
1. [Install calicoctl]({{site.baseurl}}/getting-started/clis/calicoctl/install#installing-calicoctl-as-a-kubernetes-pod).
{% endif %}

1. Use the following command to apply your [license key]({{site.baseurl}}/reference/resources/licensekey).

   **Command**
{% if include.cli == 'calicoctl' %}
   ```
   {{include.cli}} apply -f - < <customer-name>-license.yaml
   ```
{% else %}
   ```
   {{include.cli}} apply -f <customer-name>-license.yaml
   ```
{% endif %}


   **Example**
{% if include.cli == 'calicoctl' %}
   ```
   {{include.cli}} apply -f - < awesome>-license.yaml
   ```
{% else %}
   ```
   {{include.cli}} apply -f awesome-corp-license.yaml
   ```
{% endif %}
   {: .no-select-button}

1. Confirm that the license was applied:
{% if include.cli == 'calicoctl' %}
   ```
   {{include.cli}} get license
   ```
{% else %}
   ```
   {{include.cli}} get licensekey default
   ```
{% endif %}
