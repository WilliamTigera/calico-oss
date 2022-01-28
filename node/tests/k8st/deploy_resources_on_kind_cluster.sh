#!/bin/bash -e

# test directory.
TEST_DIR=./tests/k8st

# gcr.io pull secrect credential file.
: ${GCR_IO_PULL_SECRET:=./docker_auth.json}

# Path to Enteprise product license
: ${TSEE_TEST_LICENSE:=/home/semaphore/secrets/new-test-customer-license.yaml}

# kubectl binary.
: ${kubectl:=./bin/kubectl}

# kind binary.
: ${KIND:=./bin/kind}

function checkModule(){
  MODULE="$1"
  echo "Checking kernel module $MODULE ..."
  if lsmod | grep "$MODULE" &> /dev/null ; then
    return 0
  else
    return 1
  fi
}

function load_image() {
    local node=$1
    docker cp ./cnx-node.tar ${node}:/cnx-node.tar
    docker cp ./calico-apiserver.tar ${node}:/calico-apiserver.tar
    docker cp ./calicoctl.tar ${node}:/calicoctl.tar
    docker cp ./calico-cni.tar ${node}:/calico-cni.tar
    docker cp ./pod2daemon.tar ${node}:/pod2daemon.tar
    docker cp ./kube-controllers.tar ${node}:/kube-controllers.tar
    docker exec -t ${node} ctr -n=k8s.io images import /cnx-node.tar
    docker exec -t ${node} ctr -n=k8s.io images import /calico-apiserver.tar
    docker exec -t ${node} ctr -n=k8s.io images import /calicoctl.tar
    docker exec -t ${node} ctr -n=k8s.io images import /calico-cni.tar
    docker exec -t ${node} ctr -n=k8s.io images import /pod2daemon.tar
    docker exec -t ${node} ctr -n=k8s.io images import /kube-controllers.tar
    docker exec -t ${node} rm /cnx-node.tar /calicoctl.tar /calico-cni.tar /pod2daemon.tar /kube-controllers.tar /calico-apiserver.tar
}

function update_calico_manifest() {
    local yaml=$1
	# Based on instructions in http://docs.projectcalico.org/master/networking/dual-stack.md
	# add assign_ipv4 and assign_ipv6 to CNI config
	sed -i -e '/"type": "calico-ipam"/r /dev/stdin' "${yaml}" <<EOF
              "assign_ipv4": "true",
              "assign_ipv6": "true"
EOF
	sed -i -e 's/"type": "calico-ipam"/"type": "calico-ipam",/' "${yaml}"

	sed -i -e '/"type": "calico"/r /dev/stdin' "${yaml}" <<EOF
     "feature_control": {
         "floating_ips": true
     },
EOF

	# And add all the IPV6 env vars
	sed -i '/# Enable IPIP/r /dev/stdin' "${yaml}" << EOF
            - name: IP6
              value: "autodetect"
            - name: CALICO_IPV6POOL_CIDR
              value: "fd00:10:244::/64"
EOF
	# update FELIX_IPV6SUPPORT=true
	sed -i '/FELIX_IPV6SUPPORT/!b;n;c\              value: "true"' "${yaml}"

	# We don't want any IP-IP or VXLAN overlay.
	sed -i 's/Always/Never/' "${yaml}"

    # update calico/node image
    sed -i 's,image: .*calico/node:.*,image: tigera/cnx-node:latest-amd64,' "${yaml}"
}

echo "Set ipv6 address on each node"
docker exec kind-control-plane ip -6 a a 2001:20::8/64 dev eth0
docker exec kind-worker ip -6 a a 2001:20::1/64 dev eth0
docker exec kind-worker2 ip -6 a a 2001:20::2/64 dev eth0
docker exec kind-worker3 ip -6 a a 2001:20::3/64 dev eth0
echo

echo "Load calico/node docker images onto each node"
load_image kind-control-plane
load_image kind-worker
load_image kind-worker2
load_image kind-worker3

for image in calico/cni:master calico/pod2daemon-flexvol:master; do
    docker pull ${image}
    rm -f image.tar
    docker save --output image.tar ${image}
    for node in kind-control-plane kind-worker kind-worker2 kind-worker3; do
	docker cp image.tar ${node}:/image.tar
	docker exec -t ${node} ctr -n=k8s.io images import /image.tar
	docker exec -t ${node} rm /image.tar
    done
done

# Install pull secret so we can pull the right calicoctl.
${kubectl} -n kube-system create secret generic cnx-pull-secret \
   --from-file=.dockerconfigjson=${GCR_IO_PULL_SECRET} \
   --type=kubernetes.io/dockerconfigjson

echo "Install Calico and Calicoctl for dualstack"
cp $TEST_DIR/infra/calico-kdd.yaml $TEST_DIR/infra/calico.yaml.tmp
update_calico_manifest $TEST_DIR/infra/calico.yaml.tmp
${kubectl} apply -f $TEST_DIR/infra/calico.yaml.tmp
rm $TEST_DIR/infra/calico.yaml.tmp

# Install Calicoctl on master node.
${kubectl} apply -f ${TEST_DIR}/infra/calicoctl.yaml

echo
echo "Wait Calico to be ready..."
while ! time ${kubectl} wait pod -l k8s-app=calico-node --for=condition=Ready -n kube-system --timeout=300s; do
    # This happens when no matching resources exist yet,
    # i.e. immediately after application of the Calico YAML.
    sleep 5
done
time ${kubectl} wait pod -l k8s-app=calico-kube-controllers --for=condition=Ready -n kube-system --timeout=300s
time ${kubectl} wait pod -l k8s-app=kube-dns --for=condition=Ready -n kube-system --timeout=300s
time ${kubectl} wait pod calicoctl --for=condition=Ready -n kube-system --timeout=300s
echo "Calico is running."
echo

# Apply the enterprise license.
# FIXME(karthik): Applying the enterprise license here since the test written don't test for invalid or no license.
# Once such tests are added, this will have to move into the test itself.
${kubectl} exec -i -n kube-system calicoctl -- /calicoctl --allow-version-mismatch apply -f - < ${TSEE_TEST_LICENSE}

function test_connection() {
    local svc="webserver-ipv$1"
    output=$(${kubectl} exec client -- wget $svc -T 5 -O -)
    echo $output
    if [[ $output != *test-webserver* ]]; then
	echo "connection to $svc service failed"
	exit 1
    fi
}

echo "Install MetalLB controller for allocating LoadBalancer IPs"
${kubectl} create ns metallb-system
${kubectl} apply -f $TEST_DIR/infra/metallb.yaml
${kubectl} apply -f $TEST_DIR/infra/metallb-config.yaml

# Create and monitor a test webserver service for dual stack.
echo "Create test-webserver deployment..."
${kubectl} apply -f tests/k8st/infra/test-webserver.yaml

echo "Deploy Calico apiserver"
${kubectl} create -f ${TEST_DIR}/infra/apiserver.yaml
openssl req -x509 -nodes -newkey rsa:4096 -keyout apiserver.key -out apiserver.crt -days 365 -subj "/" -addext "subjectAltName = DNS:calico-api.calico-apiserver.svc"
${kubectl} create secret -n calico-apiserver generic calico-apiserver-certs --from-file=apiserver.key --from-file=apiserver.crt
${kubectl} patch apiservice v3.projectcalico.org -p \
    "{\"spec\": {\"caBundle\": \"$(${kubectl} get secret -n calico-apiserver calico-apiserver-certs -o go-template='{{ index .data "apiserver.crt" }}')\"}}"
time ${kubectl} wait pod -l k8s-app=calico-apiserver --for=condition=Ready -n calico-apiserver --timeout=30s
echo "Calico apiserver is running."

if dual_stack; then
    # Create and monitor a test webserver service for dual stack.
    echo "Create test-webserver deployment..."
    ${kubectl} apply -f tests/k8st/infra/test-webserver.yaml

    echo "Wait for client and webserver pods to be ready..."
    while ! time ${kubectl} wait pod -l pod-name=client --for=condition=Ready --timeout=300s; do
	sleep 5
    done
    while ! time ${kubectl} wait pod -l app=webserver --for=condition=Ready --timeout=300s; do
	sleep 5
    done
    echo "client and webserver pods are running."
    echo

    ${kubectl} get po --all-namespaces -o wide
    ${kubectl} get svc

    # Run ipv4 ipv6 connection test
    test_connection 4
    test_connection 6
fi
