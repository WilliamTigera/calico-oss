PACKAGE_NAME    ?= github.com/tigera/es-proxy
GO_BUILD_VER    ?= v0.38
GIT_USE_SSH      = true
LIBCALICO_REPO   = github.com/tigera/libcalico-go-private
FELIX_REPO       = github.com/tigera/felix-private
TYPHA_REPO       = github.com/tigera/typha-private
LOCAL_CHECKS     = mod-download

build: es-proxy

##############################################################################
# Download and include Makefile.common before anything else
#   Additions to EXTRA_DOCKER_ARGS need to happen before the include since
#   that variable is evaluated when we declare DOCKER_RUN and siblings.
##############################################################################
MAKE_BRANCH?=$(GO_BUILD_VER)
MAKE_REPO?=https://raw.githubusercontent.com/projectcalico/go-build/$(MAKE_BRANCH)

Makefile.common: Makefile.common.$(MAKE_BRANCH)
	cp "$<" "$@"
Makefile.common.$(MAKE_BRANCH):
	# Clean up any files downloaded from other branches so they don't accumulate.
	rm -f Makefile.common.*
	curl --fail $(MAKE_REPO)/Makefile.common -o "$@"

EXTRA_DOCKER_ARGS += -e GOPRIVATE=github.com/tigera/*
# Allow local libcalico-go to be mapped into the build container.
ifdef LIBCALICOGO_PATH
EXTRA_DOCKER_ARGS += -v $(LIBCALICOGO_PATH):/go/src/github.com/projectcalico/libcalico-go:ro
endif
# SSH_AUTH_DIR doesn't work with MacOS but we can optionally volume mount keys
ifdef SSH_AUTH_DIR
EXTRA_DOCKER_ARGS += --tmpfs /home/user -v $(SSH_AUTH_DIR):/home/user/.ssh:ro
endif

include Makefile.common

###############################################################################
# we want to be able to run the same recipe on multiple targets keyed on the image name
# to do that, we would use the entire image name, e.g. calico/node:abcdefg, as the stem, or '%', in the target
# however, make does **not** allow the usage of invalid filename characters - like / and : - in a stem, and thus errors out
# to get around that, we "escape" those characters by converting all : to --- and all / to ___ , so that we can use them
# in the target, we then unescape them back
escapefs = $(subst :,---,$(subst /,___,$(1)))
unescapefs = $(subst ---,:,$(subst ___,/,$(1)))

###############################################################################
# Define some constants
BUILD_IMAGE?=tigera/es-proxy
PUSH_IMAGES?=gcr.io/unique-caldron-775/cnx/$(BUILD_IMAGE)
RELEASE_IMAGES?=quay.io/$(BUILD_IMAGE)

ETCD_VERSION?=v3.3.7
# If building on amd64 omit the arch in the container name.  Fixme!
ETCD_IMAGE?=quay.io/coreos/etcd:$(ETCD_VERSION)

K8S_VERSION?=v1.11.3
HYPERKUBE_IMAGE?=gcr.io/google_containers/hyperkube-$(ARCH):$(K8S_VERSION)

ELASTICSEARCH_VERSION?=7.3.2
ELASTICSEARCH_IMAGE?=docker.elastic.co/elasticsearch/elasticsearch:$(ELASTICSEARCH_VERSION)

K8S_VERSION    = v1.11.0
BINDIR        ?= bin
BUILD_DIR     ?= build
TOP_SRC_DIRS   = pkg
SRC_DIRS       = $(shell sh -c "find $(TOP_SRC_DIRS) -name \\*.go \
                   -exec dirname {} \\; | sort | uniq")
TEST_DIRS     ?= $(shell sh -c "find $(TOP_SRC_DIRS) -name \\*_test.go \
                   -exec dirname {} \\; | sort | uniq")
GO_FILES       = $(shell sh -c "find pkg cmd -name \\*.go")
ifeq ($(shell uname -s),Darwin)
STAT           = stat -f '%c %N'
else
STAT           = stat -c '%Y %n'
endif
ifdef UNIT_TESTS
UNIT_TEST_FLAGS=-run $(UNIT_TESTS) -v
endif

ES_PROXY_VERSION?=$(shell git describe --tags --dirty --always)
ES_PROXY_BUILD_DATE?=$(shell date -u +'%FT%T%z')
ES_PROXY_GIT_COMMIT?=$(shell git rev-parse --short HEAD)
ES_PROXY_GIT_TAG?=$(shell git describe --tags)

VERSION_FLAGS=-X $(PACKAGE_NAME)/pkg/handler.VERSION=$(ES_PROXY_VERSION) \
	-X $(PACKAGE_NAME)/pkg/handler.BUILD_DATE=$(ES_PROXY_BUILD_DATE) \
	-X $(PACKAGE_NAME)/pkg/handler.GIT_TAG=$(ES_PROXY_GIT_TAG) \
	-X $(PACKAGE_NAME)/pkg/handler.GIT_COMMIT=$(ES_PROXY_GIT_COMMIT) \
	-X main.VERSION=$(ES_PROXY_VERSION)
BUILD_LDFLAGS=-ldflags "$(VERSION_FLAGS)"
RELEASE_LDFLAGS=-ldflags "$(VERSION_FLAGS) -s -w"

###############################################################################
# This section builds the output binaries.
# Some will have dedicated targets to make it easier to type, for example
# "es-proxy" instead of "$(BINDIR)/es-proxy".
es-proxy: $(BINDIR)/es-proxy

$(BINDIR)/es-proxy: $(BINDIR)/es-proxy-amd64
	$(DOCKER_GO_BUILD) \
		sh -c 'cd $(BINDIR) && ln -s -T es-proxy-$(ARCH) es-proxy'

$(BINDIR)/es-proxy-$(ARCH): $(GO_FILES)
ifndef RELEASE_BUILD
	$(eval LDFLAGS:=$(RELEASE_LDFLAGS))
else
	$(eval LDFLAGS:=$(BUILD_LDFLAGS))
endif
	@echo Building es-proxy...
	# configure git to use ssh instead of https so that go mod can pull private libraries.
	# note this will require the user have their SSH agent running and configured with valid private keys
	# but the Makefile logic here will load the local SSH agent into the container automatically.
	mkdir -p .go-build-cache && \
	$(DOCKER_GO_BUILD) \
		sh -c 'git config --global url.ssh://git@github.com.insteadOf https://github.com && \
			go build -o $@ -v $(LDFLAGS) "$(PACKAGE_NAME)/cmd/server" && \
				( ldd $(BINDIR)/es-proxy-$(ARCH) 2>&1 | \
	                grep -q -e "Not a valid dynamic program" -e "not a dynamic executable" || \
				( echo "Error: $(BINDIR)/es-proxy-$(ARCH) was not statically linked"; false ) )'

# Build the docker image.
.PHONY: $(BUILD_IMAGE) $(BUILD_IMAGE)-$(ARCH)

# by default, build the image for the target architecture
.PHONY: image-all
image-all: $(addprefix sub-image-,$(ARCHES))
sub-image-%:
	$(MAKE) image ARCH=$*

image: $(BUILD_IMAGE)
$(BUILD_IMAGE): $(BUILD_IMAGE)-$(ARCH)
$(BUILD_IMAGE)-$(ARCH): $(BINDIR)/es-proxy-$(ARCH)
	docker build --pull -t $(BUILD_IMAGE):latest-$(ARCH) --file ./Dockerfile.$(ARCH) .
ifeq ($(ARCH),amd64)
	docker tag $(BUILD_IMAGE):latest-$(ARCH) $(BUILD_IMAGE):latest
endif

imagetag:
ifndef IMAGETAG
	$(error IMAGETAG is undefined - run using make <target> IMAGETAG=X.Y.Z)
endif

## push one arch
push: imagetag $(addprefix sub-single-push-,$(call escapefs,$(PUSH_IMAGES)))

sub-single-push-%:
	docker push $(call unescapefs,$*:$(IMAGETAG)-$(ARCH))

## push all arches
push-all: imagetag $(addprefix sub-push-,$(ARCHES))
sub-push-%:
	$(MAKE) push ARCH=$* IMAGETAG=$(IMAGETAG)

## push multi-arch manifest where supported
push-manifests: imagetag  $(addprefix sub-manifest-,$(call escapefs,$(PUSH_MANIFEST_IMAGES)))
sub-manifest-%:
	# Docker login to hub.docker.com required before running this target as we are using $(DOCKER_CONFIG) holds the docker login credentials
	# path to credentials based on manifest-tool's requirements here https://github.com/estesp/manifest-tool#sample-usage
	docker run -t --entrypoint /bin/sh -v $(DOCKER_CONFIG):/root/.docker/config.json $(CALICO_BUILD) -c "/usr/bin/manifest-tool push from-args --platforms $(call join_platforms,$(ARCHES)) --template $(call unescapefs,$*:$(IMAGETAG))-ARCH --target $(call unescapefs,$*:$(IMAGETAG))"

## push default amd64 arch where multi-arch manifest is not supported
push-non-manifests: imagetag $(addprefix sub-non-manifest-,$(call escapefs,$(PUSH_NONMANIFEST_IMAGES)))
sub-non-manifest-%:
ifeq ($(ARCH),amd64)
	docker push $(call unescapefs,$*:$(IMAGETAG))
else
	$(NOECHO) $(NOOP)
endif

## tag images of one arch for all supported registries
tag-images: imagetag $(addprefix sub-single-tag-images-arch-,$(call escapefs,$(PUSH_IMAGES))) $(addprefix sub-single-tag-images-non-manifest-,$(call escapefs,$(PUSH_NONMANIFEST_IMAGES)))

sub-single-tag-images-arch-%:
	docker tag $(BUILD_IMAGE):latest-$(ARCH) $(call unescapefs,$*:$(IMAGETAG)-$(ARCH))

# because some still do not support multi-arch manifest
sub-single-tag-images-non-manifest-%:
ifeq ($(ARCH),amd64)
	docker tag $(BUILD_IMAGE):latest-$(ARCH) $(call unescapefs,$*:$(IMAGETAG))
else
	$(NOECHO) $(NOOP)
endif

## tag images of all archs
tag-images-all: imagetag $(addprefix sub-tag-images-,$(ARCHES))
sub-tag-images-%:
	$(MAKE) tag-images ARCH=$* IMAGETAG=$(IMAGETAG)

##########################################################################
# Testing
##########################################################################
report-dir:
	mkdir -p report

.PHONY: ut
ut: report-dir
	$(DOCKER_GO_BUILD) \
		sh -c 'git config --global url.ssh://git@github.com.insteadOf https://github.com && \
			go test $(UNIT_TEST_FLAGS) \
			$(addprefix $(PACKAGE_NAME)/,$(TEST_DIRS))'

.PHONY: fv
fv: signpost image report-dir run-k8s-apiserver
	$(MAKE) fv-no-setup

## Developer friendly target to only run fvs and skip other
## setup steps.
.PHONY: fv-no-setup
fv-no-setup:
	PACKAGE_ROOT=$(CURDIR) \
		       GO_BUILD_IMAGE=$(CALICO_BUILD) \
		       PACKAGE_NAME=$(PACKAGE_NAME) \
		       GINKGO_ARGS='$(GINKGO_ARGS)' \
		       FV_ELASTICSEARCH_IMAGE=$(ELASTICSEARCH_IMAGE) \
		       GOMOD_CACHE=$(GOMOD_CACHE) \
		       ./test/run_test.sh

.PHONY: clean
clean: clean-bin clean-build-image
clean-build-image:
	-docker rmi -f $(BUILD_IMAGE) > /dev/null 2>&1

clean-bin:
	-rm -rf $(BINDIR) bin


.PHONY: signpost
signpost:
	@echo "------------------------------------------------------------------------------"

###############################################################################
# Static checks
###############################################################################
# See .golangci.yml for golangci-lint config
LINT_ARGS +=

###############################################################################
# CI/CD
###############################################################################
.PHONY: ci cd

## run CI cycle - build, test, etc.
## Run UTs and only if they pass build image and continue along.
## Building the image is required for fvs.
ci: clean image-all static-checks ut fv

.PHONY: undo-go-sum check-dirty
## Avoid unplanned go.sum updates
undo-go-sum:
	@if (git status --porcelain go.sum | grep -o 'go.sum'); then \
	  echo "Undoing go.sum update..."; \
	  git checkout -- go.sum; \
	fi

## Check if generated image is dirty
check-dirty: undo-go-sum
	@if (git describe --tags --dirty | grep -c dirty >/dev/null); then \
	  echo "Generated image is dirty:"; \
	  git status --porcelain; \
	  false; \
	fi

## Deploys images to registry
cd: check-dirty
ifndef CONFIRM
	$(error CONFIRM is undefined - run using make <target> CONFIRM=true)
endif
ifndef BRANCH_NAME
	$(error BRANCH_NAME is undefined - run using make <target> BRANCH_NAME=var or set an environment variable)
endif
	$(MAKE) tag-images-all push-all push-manifests push-non-manifests IMAGETAG=$(BRANCH_NAME) EXCLUDEARCH="$(EXCLUDEARCH)"
	$(MAKE) tag-images-all push-all push-manifests push-non-manifests IMAGETAG=$(shell git describe --tags --dirty --always --long) EXCLUDEARCH="$(EXCLUDEARCH)"

###############################################################################
# Release
###############################################################################
PREVIOUS_RELEASE=$(shell git describe --tags --abbrev=0)

## Tags and builds a release from start to finish.
release: release-prereqs
	$(MAKE) VERSION=$(VERSION) release-tag
	$(MAKE) VERSION=$(VERSION) release-build
	$(MAKE) VERSION=$(VERSION) release-verify

	@echo ""
	@echo "Release build complete. Next, push the produced images."
	@echo ""
	@echo "  make VERSION=$(VERSION) release-publish"
	@echo ""

## Produces a git tag for the release.
release-tag: release-prereqs release-notes
	git tag $(VERSION) -F release-notes-$(VERSION)
	@echo ""
	@echo "Now you can build the release:"
	@echo ""
	@echo "  make VERSION=$(VERSION) release-build"
	@echo ""

## Produces a clean build of release artifacts at the specified version.
release-build: release-prereqs clean
# Check that the correct code is checked out.
ifneq ($(VERSION), $(GIT_VERSION))
	$(error Attempt to build $(VERSION) from $(GIT_VERSION))
endif

	$(MAKE) image-all
	$(MAKE) tag-images-all IMAGETAG=$(VERSION)
	# Generate the `latest` images.
	$(MAKE) tag-images-all IMAGETAG=latest

## Verifies the release artifacts produces by `make release-build` are correct.
release-verify: release-prereqs
	# Check the reported version is correct for each release artifact.
	if ! docker run $(PUSH_IMAGES):$(VERSION)-$(ARCH) --version | grep '^$(VERSION)$$'; then \
	  echo "Reported version:" `docker run $(PUSH_IMAGES):$(VERSION)-$(ARCH) --version` "\nExpected version: $(VERSION)"; \
	  false; \
	else \
	  echo "Version check passed\n"; \
	fi

## Generates release notes based on commits in this version.
release-notes: release-prereqs
	mkdir -p dist
	echo "# Changelog" > release-notes-$(VERSION)
	sh -c "git cherry -v $(PREVIOUS_RELEASE) | cut '-d ' -f 2- | sed 's/^/- /' >> release-notes-$(VERSION)"

## Pushes a github release and release artifacts produced by `make release-build`.
release-publish: release-prereqs
	# Push the git tag.
	git push origin $(VERSION)

	# Push images.
	$(MAKE) push-all push-manifests push-non-manifests IMAGETAG=$(VERSION)

	@echo "Finalize the GitHub release based on the pushed tag."
	@echo ""
	@echo "  https://$(PACKAGE_NAME)/releases/tag/$(VERSION)"
	@echo ""
	@echo "If this is the latest stable release, then run the following to push 'latest' images."
	@echo ""
	@echo "  make VERSION=$(VERSION) release-publish-latest"
	@echo ""

# WARNING: Only run this target if this release is the latest stable release. Do NOT
# run this target for alpha / beta / release candidate builds, or patches to earlier Calico versions.
## Pushes `latest` release images. WARNING: Only run this for latest stable releases.
release-publish-latest: release-prereqs
	$(MAKE) push-all push-manifests push-non-manifests IMAGETAG=latest

# release-prereqs checks that the environment is configured properly to create a release.
release-prereqs:
ifndef VERSION
	$(error VERSION is undefined - run using make release VERSION=vX.Y.Z)
endif
ifdef LOCAL_BUILD
	$(error LOCAL_BUILD must not be set for a release)
endif

###############################################################################
# Update pins
###############################################################################
# Guard so we don't run this on osx because of ssh-agent to docker forwarding bug
guard-ssh-forwarding-bug:
	@if [ "$(shell uname)" = "Darwin" ]; then \
		echo "ERROR: This target requires ssh-agent to docker key forwarding and is not compatible with OSX/Mac OS"; \
		echo "$(MAKECMDGOALS)"; \
		exit 1; \
	fi;

APISERVER_BRANCH=$(PIN_BRANCH)
APISERVER_REPO=github.com/tigera/apiserver
COMPLIANCE_BRANCH?=$(PIN_BRANCH)
COMPLIANCE_REPO?=github.com/tigera/compliance
LMA_BRANCH?=$(PIN_BRANCH)
LMA_REPO?=github.com/tigera/lma

replace-apiserver-pin:
	$(call update_replace_pin,$(APISERVER_REPO),$(APISERVER_REPO),$(APISERVER_BRANCH))

replace-compliance-pin:
	$(call update_replace_pin,$(COMPLIANCE_REPO),$(COMPLIANCE_REPO),$(COMPLIANCE_BRANCH))

replace-lma-pin:
	$(call update_replace_pin,$(LMA_REPO),$(LMA_REPO),$(LMA_BRANCH))

## Update dependency pins
update-pins: guard-ssh-forwarding-bug replace-libcalico-pin replace-typha-pin replace-felix-pin replace-apiserver-pin replace-compliance-pin replace-lma-pin

###############################################################################
# Utilities
###############################################################################
LOCAL_IP_ENV?=$(shell ip route get 8.8.8.8 | head -1 | awk '{print $$7}')

# etcd is used by the FVs
.PHONY: run-etcd
run-etcd: stop-etcd
	@-docker rm -f calico-etcd
	docker run --detach \
		--net=host \
		--name calico-etcd $(ETCD_IMAGE) \
		etcd \
		--advertise-client-urls "http://$(LOCAL_IP_ENV):2379,http://127.0.0.1:2379" \
		--listen-client-urls "http://0.0.0.0:2379"

stop-etcd:
	@-docker rm -f calico-etcd


# Kubernetes apiserver used for FVs
.PHONY: run-k8s-apiserver
run-k8s-apiserver: stop-k8s-apiserver run-etcd
	docker run \
		--net=host --name st-apiserver \
		-v  $(CURDIR)/test:/test\
		--detach \
		${HYPERKUBE_IMAGE} \
		/hyperkube apiserver \
			--bind-address=0.0.0.0 \
			--insecure-bind-address=0.0.0.0 \
			--etcd-servers=http://127.0.0.1:2379 \
			--admission-control=NamespaceLifecycle,LimitRanger,DefaultStorageClass,ResourceQuota \
			--authorization-mode=RBAC \
			--service-cluster-ip-range=10.101.0.0/16 \
			--v=10 \
			--token-auth-file=/test/token_auth.csv \
			--basic-auth-file=/test/basic_auth.csv \
			--anonymous-auth=true \
			--logtostderr=true

	# Wait until we can configure a cluster role binding which allows anonymous auth.
	while ! docker exec st-apiserver kubectl create \
		clusterrolebinding anonymous-admin \
		--clusterrole=cluster-admin \
		--user=system:anonymous; \
		do echo "Trying to create ClusterRoleBinding"; \
		sleep 1; \
		done

	test/setup_k8s_auth.sh

# Stop Kubernetes apiserver
stop-k8s-apiserver:
	@-docker rm -f st-apiserver

###############################################################################
# Utils
###############################################################################
# this is not a linked target, available for convenience.
.PHONY: tidy
## 'tidy' go modules.
tidy:
	$(DOCKER_RUN) $(CALICO_BUILD) sh -c '$(GIT_CONFIG_SSH) go mod tidy'
