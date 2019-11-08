#!/bin/sh

# Patch the following for CNX Manager containers: 
# - For cnx-manager, enable ENABLE_MULTI_CLUSTER_MANAGEMENT
# - For tigera-voltron, mount volume to use secret cnx-voltron-tunnel
# - For tigera-voltron, open 9449 port to accept tunnels

# Create certs that will be used for Voltron
mkdir -p /tmp/certs
echo ${PWD}

bash $(pwd)/clean-self-signed.sh /tmp/certs
bash self-signed.sh /tmp/certs

os=$(uname -s)
BASE64_ARGS=""
if [ "${os}" = "Linux" ]; then
    BASE64_ARGS="-w 0"
fi

CERT64=$(base64 ${BASE64_ARGS} /tmp/certs/cert)
KEY64=$(base64 ${BASE64_ARGS} /tmp/certs/key)

kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: cnx-voltron-tunnel
  namespace: tigera-manager
type: Opaque
data:
  cert: ${CERT64}
  key: ${KEY64}
EOF


# Extract the internal ip of a the master to populate VOLTRON_PUBLIC_IP
INTERNAL_IP=$(kubectl get nodes -o wide | grep "master" | awk '{print $6}')
echo "Using Voltron Public Ip ${INTERNAL_IP}"
kubectl set env deployment tigera-manager  -ntigera-manager -c tigera-voltron VOLTRON_PUBLIC_IP=${INTERNAL_IP}:30449

kubectl patch deployment -n tigera-manager tigera-manager --patch \
'{"metadata": {"annotations": {"unsupported.operator.tigera.io/ignore": "true"}}, "spec":{"template":{"spec":{"containers":[{"name":"tigera-manager","env":[{"name": "ENABLE_MULTI_CLUSTER_MANAGEMENT", "value": "true"}]},{ "name":"tigera-voltron","env":[{"name": "VOLTRON_TUNNEL_PORT", "value": "9449"}, {"name": "VOLTRON_ENABLE_MULTI_CLUSTER_MANAGEMENT", "value": "true"}], "volumeMounts":[{"mountPath":"/certs/tunnel/","name":"cnx-voltron-tunnel"}]}],"volumes":[{"name":"cnx-voltron-tunnel","secret":{"secretName":"cnx-voltron-tunnel"}}]}}}}'

kubectl apply -f - <<EOF
apiVersion: v1
kind: Service
metadata:
  name: tigera-voltron
  namespace: tigera-manager
spec:
  type: NodePort
  ports:
    - port: 9449
      nodePort: 30449
      protocol: TCP
      name: tunnels
  selector:
    k8s-app: tigera-manager
EOF

# Monitor deployment for cnx-manager
kubectl rollout status -n tigera-manager deployment/tigera-manager
if [ $? -ne 0 ]; then
  echo >&2 "Patching cnx-manager deployment failed"
  exit 1
fi