## Node requirements

- AMD64 processor

- Linux kernel 3.10 or later with [required dependencies](#kernel-dependencies).
  The following distributions have the required kernel, its dependencies, and are
  known to work well with {{site.prodname}} and {{include.orch}}.
  - RedHat Linux 7{% if include.orch == "Kubernetes" or include.orch == "host protection" %}
  - CentOS 7
  - CoreOS Container Linux stable
  - Ubuntu 16.04
  - Debian 9
  {% endif %}{% if include.orch == "OpenShift" %}
  - CentOS 7
  {% endif %}{% if include.orch == "OpenStack" %}
  - Ubuntu 16.04
  - CentOS 7
  {% endif %}<br><br>

- {{site.prodname}} must be able to manage `cali*` interfaces on the host. When IPIP is
  enabled (the default), {{site.prodname}} also needs to be able to manage `tunl*` interfaces.

  > **Note**: Many Linux distributions, such as most of the above, include NetworkManager.
  > By default, NetworkManager does not allow {{site.prodname}} to manage interfaces.
  > If your nodes have NetworkManager, complete the steps in
  > [Preventing NetworkManager from controlling {{site.prodname}} interfaces](../../usage/troubleshooting/#prevent-networkmanager-from-controlling-cnx-interfaces)
  > before installing {{site.prodname}}.
  {: .alert .alert-info}

## Key/value store

{{site.prodname}} {{page.version}} requires a key/value store accessible by all
{{site.prodname}} components. {% if include.orch == "Kubernetes" %} On Kubernetes,
you can configure {{site.prodname}} to access an etcdv3 cluster directly or to
use the Kubernetes API datastore.{% endif %}{% if include.orch == "OpenShift" %} On
OpenShift, {{site.prodname}} can share an etcdv3 cluster with OpenShift, or
you can set up an etcdv3 cluster dedicated to {{site.prodname}}.{% endif %}
{% if include.orch == "OpenStack" %}If you don't already have an etcdv3 cluster
to connect to, we provide instructions in the [installation documentation](./installation/).{% endif %}{% if include.orch == "host protection" %}The key/value store must be etcdv3.{% endif %}


## Network requirements

Ensure that your hosts and firewalls allow the following traffic.

| Configuration                                                                         | Host                | Connection type | Port/protocol                                                 |
|---------------------------------------------------------------------------------------|---------------------|-----------------|---------------------------------------------------------------|
| {{site.prodname}} networking                                                          | All                 | Bidirectional   | TCP 179                                                       |
| {{site.prodname}} networking in IP-in-IP mode (default mode)                          | All                 | Bidirectional   | IP-in-IP, often represented by its protocol number `4`        |
{%- if include.orch == "Kubernetes" %}
| etcd datastore                                                                        | etcd hosts          | Incoming        | Often TCP 2379 but varies per etcd configuration              |
| {{site.prodname}} networking, Kubernetes API datastore, cluster of more than 50 nodes | Typha agent hosts   | Incoming        | TCP 5473 (default)                                            |
{%- else %}
| All                                                                                   | etcd hosts          | Incoming        | Often TCP 2379 but varies per etcd configuration              |
{%- endif %}
{%- if include.orch == "Kubernetes" or include.orch == "OpenShift" %}
| All                                                                                   | kube-apiserver host | Incoming        | Often TCP 443, but check the `port` value returned by `kubectl get svc kubernetes -o yaml` |
| All                                                                                   | {{site.prodname}} API server hosts | Incoming | TCP 8080 and 5443 (default)                           |
| All                                                                                   | agent hosts         | Incoming        | TCP 9081 (default)                                            |
| All                                                                                   | Prometheus hosts    | Incoming        | TCP 9090 (default)                                            |
| All                                                                                   | Alertmanager hosts  | Incoming        | TCP 9093 (default)                                            |
| All                                                                                   | {{site.prodname}} Manager host | Incoming | TCP 30003 and 9443 (defaults)                             |
{%- endif %}
{%- if include.orch == "OpenStack" %}
| All                                                                                   | nova-api host       | Incoming        | TCP 8775 (default)                                            |

\* _If your compute hosts connect directly and don't use IP-in-IP, you don't need to allow IP-in-IP traffic._
{% endif -%}
