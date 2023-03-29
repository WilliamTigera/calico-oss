# Exposed Service (apache)

Have a nginx container serving HTTP with a generic response on port 8888. Two services is then applied to expose port 443 and 8888 to the honeypod. This will generate 2 entries into our DNS record: `tigera-dashboard-internal-service.tigera-internal.svc.cluster.local` (443) and `tigera-dashboard-internal-debug.tigera-internal.svc.cluster.local` (8888). This mimics a web application with a secure endpoint (443) and a debug instance. This entice the attacker in trying to connect to `tigera-dashboard-internal-debug.tigera-internal.svc.cluster.local` (8888). Since no pods should be accessing these services, we can indicate that a pod has been compromised and is attempting to move laterally.

Manifest is located at [manifests/threatdef/honeypod/expose-svc.yaml](/manifests/threatdef/honeypod/expose-svc.yaml).

## Detection

If anyone talks to it (other than health checks) we create an alert.

## Alert

* `[Honeypod] Exposed Service accessed by ${source_namespace}/${source_name_aggr} on port 80`
  * Detection of a Pod reaching the Honeypod services on specified destination port.

### Pros

* Easy to setup.

### Cons

* Only provide generic response.
* Cannot determine if request is malicious.

## Customizations

* Target an actual running service instead of our honeypod service/namespace.
