# The build architecture is select by setting the ARCH variable.
# For example: When building on ppc64le you could use ARCH=ppc64le make <....>.
# When ARCH is undefined it defaults to amd64.
ifdef ARCH
	ARCHTAG:=-$(ARCH)
endif
ARCH?=amd64
ARCHTAG?=

ifeq ($(ARCH),amd64)
GO_BUILD_VER:=v0.6
endif

ifeq ($(ARCH),ppc64le)
GO_BUILD_VER:=latest
endif

GO_BUILD_CONTAINER?=calico/go-build$(ARCHTAG):$(GO_BUILD_VER)

help:
	@echo "Calico K8sapiserver Makefile"
	@echo "Builds:"
	@echo
	@echo "  make all                   Build all the binary packages."
	@echo "  make tigera/cnx-apiserver  Build tigera/cnx-apiserver docker image."
	@echo
	@echo "Tests:"
	@echo
	@echo "  make test                Run Tests."
	@echo "  sudo make kubeadm        Run a kubeadm master with the apiserver."
	@echo
	@echo "Maintenance:"
	@echo
	@echo "  make update-vendor  Update the vendor directory with new "
	@echo "                      versions of upstream packages.  Record results"
	@echo "                      in glide.lock."
	@echo "  make clean         Remove binary files."
# Disable make's implicit rules, which are not useful for golang, and slow down the build
# considerably.
.SUFFIXES:

all: tigera/cnx-apiserver
test: ut fv fv-kdd

# Some env vars that devs might find useful:
#  TEST_DIRS=   : only run the unit tests from the specified dirs
#  UNIT_TESTS=  : only run the unit tests matching the specified regexp

# Define some constants
#######################
K8S_VERSION    = v1.9.2
BINDIR        ?= bin
BUILD_DIR     ?= build
CAPI_PKG       = github.com/tigera/calico-k8sapiserver
TOP_SRC_DIRS   = pkg
SRC_DIRS       = $(shell sh -c "find $(TOP_SRC_DIRS) -name \\*.go \
                   -exec dirname {} \\; | sort | uniq")
TEST_DIRS     ?= $(shell sh -c "find $(TOP_SRC_DIRS) -name \\*_test.go \
                   -exec dirname {} \\; | sort | uniq")
ifeq ($(shell uname -s),Darwin)
STAT           = stat -f '%c %N'
else
STAT           = stat -c '%Y %n'
endif
K8SAPISERVER_GO_FILE = $(shell find $(SRC_DIRS) -name \*.go -exec $(STAT) {} \; \
                   | sort -r | head -n 1 | sed "s/.* //")
ifdef UNIT_TESTS
	UNIT_TEST_FLAGS=-run $(UNIT_TESTS) -v
endif

CALICOAPISERVER_VERSION?=$(shell git describe --tags --dirty --always)
CALICOAPISERVER_BUILD_DATE?=$(shell date -u +'%FT%T%z')
CALICOAPISERVER_GIT_REVISION?=$(shell git rev-parse --short HEAD)
CALICOAPISERVER_GIT_DESCRIPTION?=$(shell git describe --tags)

VERSION_FLAGS=-X $(CAPI_PKG)/cmd/apiserver/server.VERSION=$(CALICOAPISERVER_VERSION) \
	-X $(CAPI_PKG)/cmd/apiserver/server.BUILD_DATE=$(CALICOAPISERVER_BUILD_DATE) \
	-X $(CAPI_PKG)/cmd/apiserver/server.GIT_DESCRIPTION=$(CALICOAPISERVER_GIT_DESCRIPTION) \
	-X $(CAPI_PKG)/cmd/apiserver/server.GIT_REVISION=$(CALICOAPISERVER_GIT_REVISION)
BUILD_LDFLAGS=-ldflags "$(VERSION_FLAGS)"
RELEASE_LDFLAGS=-ldflags "$(VERSION_FLAGS) -s -w"
KUBECONFIG_DIR?=/etc/kubernetes/admin.conf

# Figure out the users UID/GID.  These are needed to run docker containers
# as the current user and ensure that files built inside containers are
# owned by the current user.
MY_UID:=$(shell id -u)
MY_GID:=$(shell id -g)

# Allow libcalico-go and the ssh auth sock to be mapped into the build container.
ifdef LIBCALICOGO_PATH
  EXTRA_DOCKER_ARGS += -v $(LIBCALICOGO_PATH):/go/src/github.com/projectcalico/libcalico-go:ro
endif
ifdef SSH_AUTH_SOCK
  EXTRA_DOCKER_ARGS += -v $(SSH_AUTH_SOCK):/ssh-agent --env SSH_AUTH_SOCK=/ssh-agent
endif

DOCKER_GO_BUILD := mkdir -p .go-pkg-cache && \
                   docker run --rm \
                              --net=host \
                              $(EXTRA_DOCKER_ARGS) \
                              -e LOCAL_USER_ID=$(MY_UID) \
                              -v $${PWD}:/go/src/github.com/tigera/calico-k8sapiserver:rw \
                              -v $${PWD}/.go-pkg-cache:/go/pkg:rw \
                              -v $${PWD}/hack/boilerplate:/go/src/k8s.io/kubernetes/hack/boilerplate:rw \
                              -w /go/src/github.com/tigera/calico-k8sapiserver \
                              $(GO_BUILD_CONTAINER)

# Update the vendored dependencies with the latest upstream versions matching
# our glide.yaml.  If there area any changes, this updates glide.lock
# as a side effect.  Unless you're adding/updating a dependency, you probably
# want to use the vendor target to install the versions from glide.lock.
.PHONY: update-vendor
update-vendor:
	mkdir -p $$HOME/.glide
	$(DOCKER_GO_BUILD) glide up --strip-vendor
	touch vendor/.up-to-date

# vendor is a shortcut for force rebuilding the go vendor directory.
.PHONY: vendor
vendor vendor/.up-to-date: glide.lock
	mkdir -p $$HOME/.glide
	$(DOCKER_GO_BUILD) glide install --strip-vendor
	touch vendor/.up-to-date

# This section contains the code generation stuff
#################################################
.generate_exes: $(BINDIR)/defaulter-gen \
                $(BINDIR)/deepcopy-gen \
                $(BINDIR)/conversion-gen \
                $(BINDIR)/client-gen \
                $(BINDIR)/lister-gen \
                $(BINDIR)/informer-gen \
                $(BINDIR)/openapi-gen
	touch $@

$(BINDIR)/defaulter-gen: 
	$(DOCKER_GO_BUILD) \
	    sh -c 'go build -o $@ $(CAPI_PKG)/vendor/k8s.io/code-generator/cmd/defaulter-gen'

$(BINDIR)/deepcopy-gen:
	$(DOCKER_GO_BUILD) \
	    sh -c 'go build -o $@ $(CAPI_PKG)/vendor/k8s.io/code-generator/cmd/deepcopy-gen'

$(BINDIR)/conversion-gen: 
	$(DOCKER_GO_BUILD) \
	    sh -c 'go build -o $@ $(CAPI_PKG)/vendor/k8s.io/code-generator/cmd/conversion-gen'

$(BINDIR)/client-gen:
	$(DOCKER_GO_BUILD) \
	    sh -c 'go build -o $@ $(CAPI_PKG)/vendor/k8s.io/code-generator/cmd/client-gen'

$(BINDIR)/lister-gen:
	$(DOCKER_GO_BUILD) \
	    sh -c 'go build -o $@ $(CAPI_PKG)/vendor/k8s.io/code-generator/cmd/lister-gen'

$(BINDIR)/informer-gen:
	$(DOCKER_GO_BUILD) \
	    sh -c 'go build -o $@ $(CAPI_PKG)/vendor/k8s.io/code-generator/cmd/informer-gen'

$(BINDIR)/openapi-gen: vendor/k8s.io/code-generator/cmd/openapi-gen
	$(DOCKER_GO_BUILD) \
	    sh -c 'go build -o $@ $(CAPI_PKG)/$^'

# Regenerate all files if the gen exes changed or any "types.go" files changed
.generate_files: .generate_exes $(TYPES_FILES)
	# Generate defaults
	$(DOCKER_GO_BUILD) \
	   sh -c '$(BINDIR)/defaulter-gen \
		--v 1 --logtostderr \
		--go-header-file "vendor/github.com/kubernetes/repo-infra/verify/boilerplate/boilerplate.go.txt" \
		--input-dirs "$(CAPI_PKG)/pkg/apis/projectcalico" \
		--input-dirs "$(CAPI_PKG)/pkg/apis/projectcalico/v3" \
	  	--extra-peer-dirs "$(CAPI_PKG)/pkg/apis/projectcalico" \
		--extra-peer-dirs "$(CAPI_PKG)/pkg/apis/projectcalico/v3" \
		--output-file-base "zz_generated.defaults"'
	# Generate deep copies
	$(DOCKER_GO_BUILD) \
	   sh -c '$(BINDIR)/deepcopy-gen \
		--v 1 --logtostderr \
		--go-header-file "vendor/github.com/kubernetes/repo-infra/verify/boilerplate/boilerplate.go.txt" \
		--input-dirs "$(CAPI_PKG)/pkg/apis/projectcalico" \
		--input-dirs "$(CAPI_PKG)/pkg/apis/projectcalico/v3" \
		--bounding-dirs "github.com/tigera/calico-k8sapiserver" \
		--output-file-base zz_generated.deepcopy'
	# Generate conversions
	$(DOCKER_GO_BUILD) \
	   sh -c '$(BINDIR)/conversion-gen \
		--v 1 --logtostderr \
		--go-header-file "vendor/github.com/kubernetes/repo-infra/verify/boilerplate/boilerplate.go.txt" \
		--input-dirs "$(CAPI_PKG)/pkg/apis/projectcalico" \
		--input-dirs "$(CAPI_PKG)/pkg/apis/projectcalico/v3" \
		--output-file-base zz_generated.conversion'
	# generate all pkg/client contents
	$(DOCKER_GO_BUILD) \
	   sh -c '$(BUILD_DIR)/update-client-gen.sh'
	# generate openapi
	$(DOCKER_GO_BUILD) \
	   sh -c '$(BINDIR)/openapi-gen \
		--v 1 --logtostderr \
		--go-header-file "vendor/github.com/kubernetes/repo-infra/verify/boilerplate/boilerplate.go.txt" \
		--input-dirs "$(CAPI_PKG)/pkg/apis/projectcalico/v3,k8s.io/api/core/v1,k8s.io/apimachinery/pkg/apis/meta/v1,k8s.io/apimachinery/pkg/version,k8s.io/apimachinery/pkg/runtime,github.com/projectcalico/libcalico-go/lib/apis/v3,github.com/projectcalico/libcalico-go/lib/apis/v1,github.com/projectcalico/libcalico-go/lib/numorstring" \
		--output-package "$(CAPI_PKG)/pkg/openapi"'
	touch $@

# This section builds the output binaries.
# Some will have dedicated targets to make it easier to type, for example
# "apiserver" instead of "$(BINDIR)/apiserver".
#########################################################################
$(BINDIR)/calico-k8sapiserver: .generate_files $(K8SAPISERVER_GO_FILES) vendor/.up-to-date
ifndef RELEASE_BUILD
	$(eval LDFLAGS:=$(RELEASE_LDFLAGS))
else
	$(eval LDFLAGS:=$(BUILD_LDFLAGS))
endif
	@echo Building k8sapiserver...
	mkdir -p bin
	$(DOCKER_GO_BUILD) \
	    sh -c 'go build -v -i -o $@ -v $(LDFLAGS) "$(CAPI_PKG)/cmd/apiserver" && \
               ( ldd $(BINDIR)/calico-k8sapiserver 2>&1 | grep -q "Not a valid dynamic program" || \
	             ( echo "Error: $(BINDIR)/calico-k8sapiserver was not statically linked"; false ) )'

# Build the tigera/cnx-apiserver docker image.
.PHONY: tigera/cnx-apiserver
tigera/cnx-apiserver: vendor/.up-to-date .generate_files \
    $(BINDIR)/calico-k8sapiserver
	rm -rf docker-image/bin
	mkdir -p docker-image/bin
	cp $(BINDIR)/calico-k8sapiserver docker-image/bin/
	docker build --pull -t tigera/cnx-apiserver$(ARCHTAG) --file ./docker-image/Dockerfile$(ARCHTAG) docker-image

.PHONY: ut
ut: run-etcd
	$(DOCKER_GO_BUILD) \
		sh -c 'ETCD_ENDPOINTS="http://127.0.0.1:2379" DATASTORE_TYPE="etcdv3" go test $(UNIT_TEST_FLAGS) \
			$(addprefix $(CAPI_PKG)/,$(TEST_DIRS))'

## Run etcd as a container (calico-etcd)
run-etcd: stop-etcd
	docker run --detach \
	--net=host \
	--entrypoint=/usr/local/bin/etcd \
	--name calico-etcd quay.io/coreos/etcd:v3.1.7 \
	--advertise-client-urls "http://$(LOCAL_IP_ENV):2379,http://127.0.0.1:2379,http://$(LOCAL_IP_ENV):4001,http://127.0.0.1:4001" \
	--listen-client-urls "http://0.0.0.0:2379,http://0.0.0.0:4001"

## Stop the etcd container (calico-etcd)
stop-etcd:
	-docker rm -f calico-etcd

.PHONY: fv
fv: run-etcd
	$(DOCKER_GO_BUILD) \
		sh -c 'ETCD_ENDPOINTS="http://127.0.0.1:2379" DATASTORE_TYPE="etcdv3" test/integration.sh'

## Run a local kubernetes master with API via hyperkube
run-kubernetes-master: run-etcd stop-kubernetes-master
	# Run a Kubernetes apiserver using Docker.
	docker run \
		--net=host --name st-apiserver \
		--detach \
		gcr.io/google_containers/hyperkube-amd64:${K8S_VERSION} \
		/hyperkube apiserver \
			--bind-address=0.0.0.0 \
			--insecure-bind-address=0.0.0.0 \
	        	--etcd-servers=http://127.0.0.1:2379 \
			--admission-control=NamespaceLifecycle,LimitRanger,DefaultStorageClass,ResourceQuota \
			--authorization-mode=RBAC \
			--service-cluster-ip-range=10.101.0.0/16 \
			--v=10 \
			--logtostderr=true

	# Wait until we can configure a cluster role binding which allows anonymous auth.
	while ! docker exec st-apiserver kubectl create clusterrolebinding anonymous-admin --clusterrole=cluster-admin --user=system:anonymous; do echo "Trying to create ClusterRoleBinding"; sleep 2; done

	# And run the controller manager.
	docker run \
		--net=host --name st-controller-manager \
		--detach \
		gcr.io/google_containers/hyperkube-amd64:${K8S_VERSION} \
		/hyperkube controller-manager \
                        --master=127.0.0.1:8080 \
                        --min-resync-period=3m \
                        --allocate-node-cidrs=true \
                        --cluster-cidr=10.10.0.0/16 \
                        --v=5

	# Create CustomResourceDefinition (CRD) for Calico resources
	# from the manifest crds.yaml
	docker run \
	    --net=host \
	    --rm \
		-v  $(CURDIR)/vendor/github.com/projectcalico/libcalico-go:/manifests \
		lachlanevenson/k8s-kubectl:${K8S_VERSION} \
		--server=http://127.0.0.1:8080 \
		apply -f manifests/test/crds.yaml

	# Create a Node in the API for the tests to use.
	docker run \
	    --net=host \
	    --rm \
		-v  $(CURDIR)/vendor/github.com/projectcalico/libcalico-go:/manifests \
		lachlanevenson/k8s-kubectl:${K8S_VERSION} \
		--server=http://127.0.0.1:8080 \
		apply -f manifests/test/mock-node.yaml

	# Create Namespaces required by namespaced Calico `NetworkPolicy`
	# tests from the manifests namespaces.yaml.
	docker run \
	    --net=host \
	    --rm \
		-v  $(CURDIR)/vendor/github.com/projectcalico/libcalico-go:/manifests \
		lachlanevenson/k8s-kubectl:${K8S_VERSION} \
		--server=http://localhost:8080 \
		apply -f manifests/test/namespaces.yaml

## Stop the local kubernetes master
stop-kubernetes-master:
	# Delete the cluster role binding.
	-docker exec st-apiserver kubectl delete clusterrolebinding anonymous-admin

	# Stop master components.
	-docker rm -f st-apiserver st-controller-manager

.PHONY: fv-kdd
fv-kdd: run-kubernetes-master
	$(DOCKER_GO_BUILD) \
		sh -c 'K8S_API_ENDPOINT="http://localhost:8080" DATASTORE_TYPE="kubernetes" test/integration.sh'

.PHONY: clean
clean: clean-bin clean-build-image clean-generated
clean-build-image:
	docker rmi -f tigera/cnx-apiserver > /dev/null 2>&1 || true

clean-generated:
	rm -f .generate_files
	find $(TOP_SRC_DIRS) -name zz_generated* -exec rm {} \;
	# rollback changes to the generated clientset directories
	# find $(TOP_SRC_DIRS) -type d -name *_generated -exec rm -rf {} \;

clean-bin:
	rm -rf $(BINDIR) \
			.generate_exes \
			docker-image/bin

.PHONY: release
release:
ifndef VERSION
	$(error VERSION is undefined - run using make release VERSION=vX.Y.Z)
endif
	git tag $(VERSION)

	# Check to make sure the tag isn't "dirty"
	if git describe --tags --dirty | grep dirty; \
	then echo current git working tree is "dirty". Make sure you do not have any uncommitted changes ;false; fi

	# Build the apiserver binaries and image
	$(MAKE) tigera/cnx-apiserver

	# Check that the version output includes the version specified.
	# Tests that the "git tag" makes it into the binaries. Main point is to catch "-dirty" builds
	# Release is currently supported on darwin / linux only.
	if ! docker run tigera/cnx-apiserver | grep 'Version:\s*$(VERSION)$$'; then \
	  echo "Reported version:" `docker run tigera/cnx-apiserver` "\nExpected version: $(VERSION)"; \
	  false; \
	else \
	  echo "Version check passed\n"; \
	fi

	# Retag images with correct version and GCR private registry
	docker tag tigera/cnx-apiserver gcr.io/tigera-dev/cnx/tigera/cnx-apiserver:$(VERSION)

	# Check that images were created recently and that the IDs of the versioned and latest images match
	@docker images --format "{{.CreatedAt}}\tID:{{.ID}}\t{{.Repository}}:{{.Tag}}" tigera/cnx-apiserver
	@docker images --format "{{.CreatedAt}}\tID:{{.ID}}\t{{.Repository}}:{{.Tag}}" gcr.io/tigera-dev/cnx/tigera/cnx-apiserver:$(VERSION)

	@echo "\nNow push the tag and images. Then create a release on Github and"
	@echo "\nAdd release notes for calico-k8sapiserver. Use this command"
	@echo "to find commit messages for this release: git log --oneline <old_release_version>...$(VERSION)"
	@echo "git push origin $(VERSION)"
	@echo "gcloud docker -- push gcr.io/tigera-dev/cnx/tigera/cnx-apiserver:$(VERSION)"

.PHONY: kubeadm
kubeadm:
	kubeadm reset
	rm -rf /var/etcd
	kubeadm init --config artifacts/misc/kubeadm.yaml

	# Wait for it to be ready
	while ! KUBECONFIG=$(KUBECONFIG_DIR) kubectl get pods; do sleep 15; done

	# Install Calico and the AAPI server
	KUBECONFIG=$(KUBECONFIG_DIR) kubectl apply -f artifacts/misc/calico.yaml
	KUBECONFIG=$(KUBECONFIG_DIR) kubectl taint nodes --all node-role.kubernetes.io/master-
	KUBECONFIG=$(KUBECONFIG_DIR) kubectl create namespace calico
	KUBECONFIG=$(KUBECONFIG_DIR) kubectl create -f artifacts/example/
	@echo "Kubeadm master created."
	@echo "To use, run the following commands:"
	@echo "sudo cp $(KUBECONFIG_DIR) \$$HOME"
	@echo "sudo chown \$$(id -u):\$$(id -g) \$$HOME/admin.conf"
	@echo "export KUBECONFIG=\$$HOME/admin.conf"
	@echo "kubectl get tiers"
