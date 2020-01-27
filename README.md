# Honeypod
The basic idea of Honeypod is to place canary pods or resources within a K8s cluster such that all defined and valid resources will never attempt to access or make connections to the Honeypods. If any resources reaches these Honeypods, we can automatically assume the connection is suspicious at minimum and that the source resource may be been compromised.

Honeypod may be used to detect resources enumeration, privilege escalation, data exfiltration, denial of service and vulnerability exploitation attempts. 


## Default Naming
* Pod:  “tigera-internal-\*”, where \* is the type number
* Namespace: “tigera-internal”
* Exposed services: 
  * (Vulnerable) “tigera-dashboard-internal-debug”
  * (Unreachable) “tigera-dashboard-internal-service”
* Global Network Policies: “tigera-internal-\*”
* Tier: “tigera-internal”
* Clusterolebinding: tigera-internal-binding
* Clusterole: tigera-internal-role

## Prerequisite
0. Ensure Calico Enterprise version 2.6+ is installed
1. Access to gcr.io/tigera-dev pull secret file

## Demo Installation
1. `kubectl apply -f honeypod\_sample\_setup.yaml`
2. `kubectl create secret -n tigera-internal generic tigera-pull-secret \
    --from-file=.dockerconfigjson=<PATH/TO/PULL/SECRET> --type=kubernetes.io/dockerconfigjson'

## Installation
1. `kubectl apply -f common/common.yaml`
2. `kubectl create secret -n tigera-internal generic tigera-pull-secret \
    --from-file=.dockerconfigjson=<PATH/TO/PULL/SECRET> --type=kubernetes.io/dockerconfigjson'
3. Navigate to relevant scenarios folder and apply the YAMLs (Modify naming if needed)

## Testing
To test, run the 'attacker' pod on the k8s cluster, the pod will periodically nmap/scan the network 

## Scenarios
We currently have several Honeypod deployments that can be used to detect different scenarios:

### Compromised Pod
* IP Enumeration
  * By not setting a service, the pod can only be reach locally (adjecant pods within same subnet).
* Exposed Service (nginx)
  * Expose a nginx service that serves a generic page. The pod can be discovered via clusterip or DNS lookup.
* Vulnerable Service (MySQL)
  * Expose a SQL service that contains an empty database with easy (root, no password) access. The pod can be discovered via clusterip or DNS lookup.
