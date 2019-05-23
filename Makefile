# Shortcut targets
default: build

## Build binary for current platform
all: build

## Run the tests for the current platform/architecture
test: ut test-kdd test-etcd

###############################################################################
# Both native and cross architecture builds are supported.
# The target architecture is select by setting the ARCH variable.
# When ARCH is undefined it is set to the detected host architecture.
# When ARCH differs from the host architecture a crossbuild will be performed.
ARCHES=$(patsubst Dockerfile.%,%,$(wildcard Dockerfile.*))

# BUILDARCH is the host architecture
# ARCH is the target architecture
# we need to keep track of them separately
BUILDARCH ?= $(shell uname -m)
BUILDOS ?= $(shell uname -s | tr A-Z a-z)

# canonicalized names for host architecture
ifeq ($(BUILDARCH),aarch64)
        BUILDARCH=arm64
endif
ifeq ($(BUILDARCH),x86_64)
        BUILDARCH=amd64
endif

# unless otherwise set, I am building for my own architecture, i.e. not cross-compiling
ARCH ?= $(BUILDARCH)

# canonicalized names for target architecture
ifeq ($(ARCH),aarch64)
        override ARCH=arm64
endif
ifeq ($(ARCH),x86_64)
    override ARCH=amd64
endif

###############################################################################
GO_BUILD_VER?=v0.20

CALICO_BUILD = calico/go-build:$(GO_BUILD_VER)

#This is a version with known container with compatible versions of sed/grep etc. 
TOOLING_BUILD?=calico/go-build:v0.20	


CALICOCTL_VER=master
CALICOCTL_CONTAINER_NAME=gcr.io/unique-caldron-775/cnx/tigera/calicoctl:$(CALICOCTL_VER)-$(ARCH)
TYPHA_VER=master
TYPHA_CONTAINER_NAME=gcr.io/unique-caldron-775/cnx/tigera/typha:$(TYPHA_VER)-$(ARCH)
K8S_VERSION?=v1.11.3
ETCD_VER?=v3.3.7
BIRD_VER=v0.3.1
LOCAL_IP_ENV?=$(shell ip route get 8.8.8.8 | head -1 | awk '{print $$7}')

GIT_SHORT_COMMIT:=$(shell git rev-parse --short HEAD || echo '<unknown>')
GIT_DESCRIPTION:=$(shell git describe --tags || echo '<unknown>')
LDFLAGS=-ldflags "-X $(PACKAGE_NAME)/pkg/buildinfo.GitVersion=$(GIT_DESCRIPTION)"

# Ensure that the bin directory is always created
MAKE_SURE_BIN_EXIST := $(shell mkdir -p bin)

# Figure out the users UID.  This is needed to run docker containers
# as the current user and ensure that files built inside containers are
# owned by the current user.
LOCAL_USER_ID?=$(shell id -u $$USER)

PACKAGE_NAME?=github.com/kelseyhightower/confd

# All go files.
SRC_FILES:=$(shell find . -name '*.go' -not -path "./vendor/*" )

# Files to include in the Windows ZIP archive.
WINDOWS_BUILT_FILES := windows-packaging/tigera-confd.exe
# Name of the Windows release ZIP archive.
WINDOWS_ARCHIVE := dist/tigera-confd-windows-$(VERSION).zip

# Allow libcalico-go and the ssh auth sock to be mapped into the build container.
ifdef LIBCALICOGO_PATH
  EXTRA_DOCKER_ARGS += -v $(LIBCALICOGO_PATH):/go/src/github.com/projectcalico/libcalico-go:ro
endif
ifdef SSH_AUTH_SOCK
  EXTRA_DOCKER_ARGS += -v $(SSH_AUTH_SOCK):/ssh-agent --env SSH_AUTH_SOCK=/ssh-agent
endif


DOCKER_RUN := mkdir -p .go-pkg-cache && \
                   docker run --rm \
                              --net=host \
                              $(EXTRA_DOCKER_ARGS) \
                              -e LOCAL_USER_ID=$(LOCAL_USER_ID) \
                              -v $(HOME)/.glide:/home/user/.glide:rw \
                              -v $(CURDIR):/go/src/$(PACKAGE_NAME):rw \
                              -v $(CURDIR)/.go-pkg-cache:/go/pkg:rw \
                              -w /go/src/$(PACKAGE_NAME) \
                              -e GOARCH=$(ARCH)


.PHONY: clean
clean:
	rm -rf bin/*
	rm -rf tests/logs

# Always install the git hooks to prevent publishing closed source code to a non-private repo.
hooks_installed:=$(shell ./install-git-hooks)

.PHONY: install-git-hooks
## Install Git hooks
install-git-hooks:
	./install-git-hooks

###############################################################################
# Building the binary
###############################################################################
build: bin/confd
build-all: $(addprefix sub-build-,$(VALIDARCHES))
sub-build-%:
	$(MAKE) build ARCH=$*

## Create the vendor directory
vendor: glide.lock
	# Ensure that the glide cache directory exists.
	mkdir -p $(HOME)/.glide
	$(DOCKER_RUN) $(CALICO_BUILD) glide install -strip-vendor


bin/confd-$(ARCH): $(SRC_FILES) vendor
	$(DOCKER_RUN) $(CALICO_BUILD) \
	    sh -c 'go build -v -i -o $@ $(LDFLAGS) "$(PACKAGE_NAME)" && \
		( ldd bin/confd-$(ARCH) 2>&1 | grep -q -e "Not a valid dynamic program" \
			-e "not a dynamic executable" || \
	             ( echo "Error: bin/confd was not statically linked"; false ) )'

bin/confd: bin/confd-$(ARCH)
ifeq ($(ARCH),amd64)
	ln -f bin/confd-$(ARCH) bin/confd
endif

# Cross-compile confd for Windows
windows-packaging/tigera-confd.exe: $(SRC_FILES) vendor
	@echo Building confd for Windows...
	mkdir -p bin
	$(DOCKER_RUN) $(CALICO_BUILD) \
           sh -c 'GOOS=windows go build -v -o $@ -v $(LDFLAGS) "$(PACKAGE_NAME)" && \
		( ldd $@ 2>&1 | grep -q "Not a valid dynamic program" || \
		( echo "Error: $@ was not statically linked"; false ) )'



###############################################################################
# Managing the upstream library pins
###############################################################################

## Update dependency pins in glide.yaml
update-pins: update-typha-pin update-libcalico-pin 

## deprecated target alias
update-libcalico: update-pins
	$(warning !! Update update-libcalico is deprecated, use update-pins !!)

update-typha: update-pins
	$(warning !! Update update-typha is deprecated, use update-pins !!)

## Guard so we don't run this on osx because of ssh-agent to docker forwarding bug
guard-ssh-forwarding-bug:
	@if [ "$(shell uname)" = "Darwin" ]; then \
		echo "ERROR: This target requires ssh-agent to docker key forwarding and is not compatible with OSX/Mac OS"; \
		echo "$(MAKECMDGOALS)"; \
		exit 1; \
	fi;


###############################################################################
## libcalico

## Set the default LIBCALICO source for this project
LIBCALICO_PROJECT_DEFAULT=tigera/libcalico-go-private.git
LIBCALICO_GLIDE_LABEL=projectcalico/libcalico-go

## Default the LIBCALICO repo and version but allow them to be overridden (master or release-vX.Y)
## default LIBCALICO branch to the same branch name as the current checked out repo
LIBCALICO_BRANCH?=$(shell git rev-parse --abbrev-ref HEAD)
LIBCALICO_REPO?=github.com/$(LIBCALICO_PROJECT_DEFAULT)
LIBCALICO_VERSION?=$(shell git ls-remote git@github.com:$(LIBCALICO_PROJECT_DEFAULT) $(LIBCALICO_BRANCH) 2>/dev/null | cut -f 1)

## Guard to ensure LIBCALICO repo and branch are reachable
guard-git-libcalico: 
	@_scripts/functions.sh ensure_can_reach_repo_branch $(LIBCALICO_PROJECT_DEFAULT) "master" "Ensure your ssh keys are correct and that you can access github" ; 
	@_scripts/functions.sh ensure_can_reach_repo_branch $(LIBCALICO_PROJECT_DEFAULT) "$(LIBCALICO_BRANCH)" "Ensure the branch exists, or set LIBCALICO_BRANCH variable";
	@$(DOCKER_RUN) $(CALICO_BUILD) sh -c '_scripts/functions.sh ensure_can_reach_repo_branch $(LIBCALICO_PROJECT_DEFAULT) "master" "Build container error, ensure ssh-agent is forwarding the correct keys."';
	@$(DOCKER_RUN) $(CALICO_BUILD) sh -c '_scripts/functions.sh ensure_can_reach_repo_branch $(LIBCALICO_PROJECT_DEFAULT) "$(LIBCALICO_BRANCH)" "Build container error, ensure ssh-agent is forwarding the correct keys."';
	@if [ "$(strip $(LIBCALICO_VERSION))" = "" ]; then \
		echo "ERROR: LIBCALICO version could not be determined"; \
		exit 1; \
	fi;

## Update libary pin in glide.yaml
update-libcalico-pin: guard-ssh-forwarding-bug guard-git-libcalico
	@$(DOCKER_RUN) $(TOOLING_BUILD) /bin/sh -c '\
		LABEL="$(LIBCALICO_GLIDE_LABEL)" \
		REPO="$(LIBCALICO_REPO)" \
		VERSION="$(LIBCALICO_VERSION)" \
		DEFAULT_REPO="$(LIBCALICO_PROJECT_DEFAULT)" \
		BRANCH="$(LIBCALICO_BRANCH)" \
		GLIDE="glide.yaml" \
		_scripts/update-pin.sh '


###############################################################################
## typha

## Set the default TYPHA source for this project
TYPHA_PROJECT_DEFAULT=tigera/typha-private.git
TYPHA_GLIDE_LABEL=projectcalico/typha

## Default the TYPHA repo and version but allow them to be overridden (master or release-vX.Y)
## default TYPHA branch to the same branch name as the current checked out repo
TYPHA_BRANCH?=$(shell git rev-parse --abbrev-ref HEAD)
TYPHA_REPO?=github.com/$(TYPHA_PROJECT_DEFAULT)
TYPHA_VERSION?=$(shell git ls-remote git@github.com:$(TYPHA_PROJECT_DEFAULT) $(TYPHA_BRANCH) 2>/dev/null | cut -f 1)

## Guard to ensure TYPHA repo and branch are reachable
guard-git-typha: 
	@_scripts/functions.sh ensure_can_reach_repo_branch $(TYPHA_PROJECT_DEFAULT) "master" "Ensure your ssh keys are correct and that you can access github" ; 
	@_scripts/functions.sh ensure_can_reach_repo_branch $(TYPHA_PROJECT_DEFAULT) "$(TYPHA_BRANCH)" "Ensure the branch exists, or set TYPHA_BRANCH variable";
	@$(DOCKER_RUN) $(CALICO_BUILD) sh -c '_scripts/functions.sh ensure_can_reach_repo_branch $(TYPHA_PROJECT_DEFAULT) "master" "Build container error, ensure ssh-agent is forwarding the correct keys."';
	@$(DOCKER_RUN) $(CALICO_BUILD) sh -c '_scripts/functions.sh ensure_can_reach_repo_branch $(TYPHA_PROJECT_DEFAULT) "$(TYPHA_BRANCH)" "Build container error, ensure ssh-agent is forwarding the correct keys."';
	@if [ "$(strip $(TYPHA_VERSION))" = "" ]; then \
		echo "ERROR: TYPHA version could not be determined"; \
		exit 1; \
	fi;

## Update libary pin in glide.yaml
update-typha-pin: guard-ssh-forwarding-bug guard-git-typha
	@$(DOCKER_RUN) $(TOOLING_BUILD) /bin/sh -c '\
		LABEL="$(TYPHA_GLIDE_LABEL)" \
		REPO="$(TYPHA_REPO)" \
		VERSION="$(TYPHA_VERSION)" \
		DEFAULT_REPO="$(TYPHA_PROJECT_DEFAULT)" \
		BRANCH="$(TYPHA_BRANCH)" \
		GLIDE="glide.yaml" \
		_scripts/update-pin.sh '


###############################################################################
# Static checks
###############################################################################
.PHONY: static-checks
## Perform static checks on the code.
static-checks: vendor
	docker run --rm \
		-e LOCAL_USER_ID=$(LOCAL_USER_ID) \
		-v $(CURDIR):/go/src/$(PACKAGE_NAME) \
		-w /go/src/$(PACKAGE_NAME) \
		$(CALICO_BUILD) \
		gometalinter --deadline=300s --disable-all --enable=vet --enable=errcheck  --enable=goimports --vendor --exclude=vendor ./...

.PHONY: fix
## Fix static checks
fix:
	goimports -w $(SRC_FILES)

###############################################################################
# Unit Tests
###############################################################################

# Set to true when calling test-xxx to update the rendered templates instead of
# checking them.
UPDATE_EXPECTED_DATA?=false

.PHONY: test-kdd
## Run template tests against KDD
test-kdd: bin/confd bin/kubectl bin/bird bin/bird6 bin/calico-node bin/calicoctl bin/typha run-k8s-apiserver
	-git clean -fx etc/calico/confd
	docker run --rm --net=host \
		-v $(CURDIR)/tests/:/tests/ \
		-v $(CURDIR)/vendor:/vendor/ \
		-v $(CURDIR)/bin:/calico/bin/ \
		-v $(CURDIR)/etc/calico:/etc/calico/ \
		-e LOCAL_USER_ID=0 \
		-v $$SSH_AUTH_SOCK:/ssh-agent --env SSH_AUTH_SOCK=/ssh-agent \
		-e FELIX_TYPHAADDR=127.0.0.1:5473 \
		-e FELIX_TYPHAREADTIMEOUT=50 \
		-e UPDATE_EXPECTED_DATA=$(UPDATE_EXPECTED_DATA) \
		$(CALICO_BUILD) /tests/test_suite_kdd.sh || \
	{ \
	    echo; \
	    echo === confd single-shot log:; \
	    cat tests/logs/kdd/logss || true; \
	    echo; \
	    echo === confd daemon log:; \
	    cat tests/logs/kdd/logd1 || true; \
	    echo; \
	    echo === Typha log:; \
	    cat tests/logs/kdd/typha || true; \
	    echo; \
            false; \
        }
	-git clean -fx etc/calico/confd

.PHONY: test-etcd
## Run template tests against etcd
test-etcd: bin/confd bin/etcdctl bin/bird bin/bird6 bin/calico-node bin/kubectl bin/calicoctl run-etcd run-k8s-apiserver
	-git clean -fx etc/calico/confd
	docker run --rm --net=host \
		-v $(CURDIR)/tests/:/tests/ \
		-v $(CURDIR)/bin:/calico/bin/ \
		-v $(CURDIR)/etc/calico:/etc/calico/ \
		-e LOCAL_USER_ID=0 \
		-v $$SSH_AUTH_SOCK:/ssh-agent --env SSH_AUTH_SOCK=/ssh-agent \
		-e UPDATE_EXPECTED_DATA=$(UPDATE_EXPECTED_DATA) \
		$(CALICO_BUILD) /tests/test_suite_etcd.sh
	-git clean -fx etc/calico/confd

.PHONY: ut
## Run the fast set of unit tests in a container.
ut: vendor
	-mkdir -p .go-pkg-cache
	docker run --rm -t --privileged --net=host \
		-e LOCAL_USER_ID=$(LOCAL_USER_ID) \
		-v $(CURDIR):/go/src/$(PACKAGE_NAME):rw \
		-v $(CURDIR)/.go-pkg-cache:/go-cache/:rw \
		-e GOCACHE=/go-cache \
		$(CALICO_BUILD) sh -c 'cd /go/src/$(PACKAGE_NAME) && ginkgo -r --skipPackage vendor $(GINKGO_ARGS) .'

## Etcd is used by the kubernetes
# NOTE: https://quay.io/repository/coreos/etcd is available *only* for the following archs with the following tags:
# amd64: 3.2.5
# arm64: 3.2.5-arm64
# ppc64le: 3.2.5-ppc64le
# s390x is not available
COREOS_ETCD ?= quay.io/coreos/etcd:$(ETCD_VER)-$(ARCH)
ifeq ($(ARCH),amd64)
COREOS_ETCD = quay.io/coreos/etcd:$(ETCD_VER)
endif
run-etcd: stop-etcd
	docker run --detach \
	--net=host \
	--name calico-etcd $(COREOS_ETCD) \
	etcd \
	--advertise-client-urls "http://$(LOCAL_IP_ENV):2379,http://127.0.0.1:2379,http://$(LOCAL_IP_ENV):4001,http://127.0.0.1:4001" \
	--listen-client-urls "http://0.0.0.0:2379,http://0.0.0.0:4001"

## Stops calico-etcd containers
stop-etcd:
	@-docker rm -f calico-etcd

## Kubernetes apiserver used for tests
run-k8s-apiserver: stop-k8s-apiserver run-etcd
	docker run --detach --net=host \
	  --name calico-k8s-apiserver \
	gcr.io/google_containers/hyperkube-$(ARCH):$(K8S_VERSION) \
		  /hyperkube apiserver --etcd-servers=http://$(LOCAL_IP_ENV):2379 \
		  --service-cluster-ip-range=10.101.0.0/16
	# Wait until the apiserver is accepting requests.
	while ! docker exec calico-k8s-apiserver kubectl get nodes; do echo "Waiting for apiserver to come up..."; sleep 2; done

## Stop Kubernetes apiserver
stop-k8s-apiserver:
	@-docker rm -f calico-k8s-apiserver

bin/kubectl:
	curl -sSf -L --retry 5 https://storage.googleapis.com/kubernetes-release/release/$(K8S_VERSION)/bin/linux/$(ARCH)/kubectl -o $@
	chmod +x $@

bin/bird:
	curl -sSf -L --retry 5 https://github.com/projectcalico/bird/releases/download/$(BIRD_VER)/bird -o $@
	chmod +x $@

bin/bird6:
	curl -sSf -L --retry 5 https://github.com/projectcalico/bird/releases/download/$(BIRD_VER)/bird6 -o $@
	chmod +x $@

bin/calico-node:
	cp fakebinary $@
	chmod +x $@

bin/etcdctl:
	curl -sSf -L --retry 5  https://github.com/coreos/etcd/releases/download/$(ETCD_VER)/etcd-$(ETCD_VER)-linux-$(ARCH).tar.gz | tar -xz -C bin --strip-components=1 etcd-$(ETCD_VER)-linux-$(ARCH)/etcdctl

bin/calicoctl:
	-docker rm -f calico-ctl
	# Latest calicoctl binaries are stored in automated builds of calico/ctl.
	# To get them, we create (but don't start) a container from that image.
	docker pull $(CALICOCTL_CONTAINER_NAME)
	docker create --name calico-ctl $(CALICOCTL_CONTAINER_NAME)
	# Then we copy the files out of the container.  Since docker preserves
	# mtimes on its copy, check the file really did appear, then touch it
	# to make sure that downstream targets get rebuilt.
	docker cp calico-ctl:/calicoctl $@ && \
	  test -e $@ && \
	  touch $@
	-docker rm -f calico-ctl

bin/typha:
	-docker rm -f confd-typha
	docker pull $(TYPHA_CONTAINER_NAME)
	docker create --name confd-typha $(TYPHA_CONTAINER_NAME)
	# Then we copy the files out of the container.  Since docker preserves
	# mtimes on its copy, check the file really did appear, then touch it
	# to make sure that downstream targets get rebuilt.
	docker cp confd-typha:/code/calico-typha $@ && \
	  test -e $@ && \
	  touch $@
	-docker rm -f confd-typha

foss-checks: vendor
	@echo Running $@...
	@docker run --rm -v $(CURDIR):/go/src/$(PACKAGE_NAME):rw \
	  -e LOCAL_USER_ID=$(LOCAL_USER_ID) \
	  -e FOSSA_API_KEY=$(FOSSA_API_KEY) \
	  -w /go/src/$(PACKAGE_NAME) \
	  $(CALICO_BUILD) /usr/local/bin/fossa

###############################################################################
# CI
###############################################################################
.PHONY: ci
## Run what CI runs
ci: clean static-checks test

###############################################################################
# Release
###############################################################################
PREVIOUS_RELEASE=$(shell git describe --tags --abbrev=0)
GIT_VERSION?=$(shell git describe --tags --dirty)

## Tags and builds a release from start to finish.
release: release-prereqs
	$(MAKE) VERSION=$(VERSION) release-tag
	$(MAKE) VERSION=$(VERSION) release-windows-archive

## Produces a git tag for the release.
release-tag: release-prereqs release-notes
	git tag $(VERSION) -F release-notes-$(VERSION)
	@echo ""
	@echo "Now you can publish the release:"
	@echo ""
	@echo "  make VERSION=$(VERSION) release-publish"
	@echo ""

## Generates release notes based on commits in this version.
release-notes: release-prereqs
	mkdir -p dist
	echo "# Changelog" > release-notes-$(VERSION)
	sh -c "git cherry -v $(PREVIOUS_RELEASE) | cut '-d ' -f 2- | sed 's/^/- /' >> release-notes-$(VERSION)"

## Pushes a github release and release artifacts produced by `make release-build`.
release-publish: release-prereqs
	# Push the git tag.
	git push origin $(VERSION)

	@echo "Finalize the GitHub release based on the pushed tag."
	@echo ""
	@echo "  https://github.com/projectcalico/confd/releases/tag/$(VERSION)"
	@echo ""

# release-prereqs checks that the environment is configured properly to create a release.
release-prereqs:
ifndef VERSION
	$(error VERSION is undefined - run using make release VERSION=vX.Y.Z)
endif

## Produces the Windows ZIP archive for the release.
release-windows-archive $(WINDOWS_ARCHIVE): release-prereqs $(WINDOWS_BUILT_FILES)
	-rm -f $(WINDOWS_ARCHIVE)
	mkdir -p dist
	cd windows-packaging && zip -r ../$(WINDOWS_ARCHIVE) .

###############################################################################
# Developer helper scripts (not used by build or test)
###############################################################################
help:
	@echo "confd Makefile"
	@echo
	@echo "Dependencies: docker 1.12+; go 1.8+"
	@echo
	@echo "For any target, set ARCH=<target> to build for a given target."
	@echo "For example, to build for arm64:"
	@echo
	@echo "  make build ARCH=arm64"
	@echo
	@echo "Initial set-up:"
	@echo
	@echo "  make vendor  Update/install the go build dependencies."
	@echo
	@echo "Builds:"
	@echo
	@echo "  make build           Build the binary."
	@echo
	@echo "Tests:"
	@echo
	@echo "  make test                Run all tests."
	@echo "  make test-kdd            Run kdd tests."
	@echo "  make test-etcd           Run etcd tests."
	@echo
	@echo "Maintenance:"
	@echo "  make clean         Remove binary files and docker images."
	@echo "-----------------------------------------"
	@echo "ARCH (target):          $(ARCH)"
	@echo "BUILDARCH (host):       $(BUILDARCH)"
	@echo "CALICO_BUILD:     $(CALICO_BUILD)"
	@echo "-----------------------------------------"
