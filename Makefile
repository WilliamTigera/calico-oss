.PHONY: all test

default: all
all: test
test: ut

BUILD_VER?=latest
BUILD_IMAGE:=tigera/calicoq
REGISTRY_PREFIX?=gcr.io/unique-caldron-775/cnx/
PACKAGE_NAME?=github.com/tigera/calicoq
LOCAL_USER_ID?=$(shell id -u $$USER)
BINARY:=bin/calicoq

GO_BUILD_VER?=latest
GO_BUILD?=calico/go-build:$(GO_BUILD_VER)
# Specific version for fossa license checks
FOSSA_GO_BUILD_VER?=v0.18
FOSSA_GO_BUILD?=calico/go-build:$(FOSSA_GO_BUILD_VER)

CALICOQ_VERSION?=$(shell git describe --tags --dirty --always)
CALICOQ_BUILD_DATE?=$(shell date -u +'%FT%T%z')
CALICOQ_GIT_REVISION?=$(shell git rev-parse --short HEAD)
CALICOQ_GIT_DESCRIPTION?=$(shell git describe --tags)

VERSION_FLAGS=-X $(PACKAGE_NAME)/calicoq/commands.VERSION=$(CALICOQ_VERSION) \
	-X $(PACKAGE_NAME)/calicoq/commands.BUILD_DATE=$(CALICOQ_BUILD_DATE) \
	-X $(PACKAGE_NAME)/calicoq/commands.GIT_DESCRIPTION=$(CALICOQ_GIT_DESCRIPTION) \
	-X $(PACKAGE_NAME)/calicoq/commands.GIT_REVISION=$(CALICOQ_GIT_REVISION)
BUILD_LDFLAGS=-ldflags "$(VERSION_FLAGS)"
RELEASE_LDFLAGS=-ldflags "$(VERSION_FLAGS) -s -w"

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
                              -e LOCAL_USER_ID=$(LOCAL_USER_ID) \
                              -v $${PWD}:/go/src/$(PACKAGE_NAME):rw \
                              -v $${PWD}/.go-pkg-cache:/go/pkg:rw \
                              -w /go/src/$(PACKAGE_NAME) \
                              $(GO_BUILD)

# Always install the git hooks to prevent publishing closed source code to a non-private repo.
hooks_installed:=$(shell ./install-git-hooks)

.PHONY: install-git-hooks
## Install Git hooks
install-git-hooks:
	./install-git-hooks

.PHONY: vendor
vendor vendor/.up-to-date: glide.lock
	mkdir -p $$HOME/.glide
	$(DOCKER_GO_BUILD) glide install --strip-vendor
	touch vendor/.up-to-date

.PHONY: update-vendor
update-vendor:
	mkdir -p $$HOME/.glide
	$(DOCKER_GO_BUILD) glide up --strip-vendor
	touch vendor/.up-to-date

foss-checks: vendor
	@echo Running $@...
	@docker run --rm -v $(CURDIR):/go/src/$(PACKAGE_NAME):rw \
	  -e LOCAL_USER_ID=$(LOCAL_USER_ID) \
	  -e FOSSA_API_KEY=$(FOSSA_API_KEY) \
	  -w /go/src/$(PACKAGE_NAME) \
	  $(FOSSA_GO_BUILD) /usr/local/bin/fossa

.PHONY: ut
ut: bin/calicoq
	ginkgo -cover -r --skipPackage vendor calicoq/*

	@echo
	@echo '+==============+'
	@echo '| All coverage |'
	@echo '+==============+'
	@echo
	@find ./calicoq/ -iname '*.coverprofile' | xargs -I _ go tool cover -func=_

	@echo
	@echo '+==================+'
	@echo '| Missing coverage |'
	@echo '+==================+'
	@echo
	@find ./calicoq/ -iname '*.coverprofile' | xargs -I _ go tool cover -func=_ | grep -v '100.0%'

.PHONY: ut-containerized
ut-containerized: bin/calicoq
	docker run --rm -t \
		-v $(CURDIR):/go/src/$(PACKAGE_NAME) \
		-w /go/src/$(PACKAGE_NAME) \
		-e LOCAL_USER_ID=$(LOCAL_USER_ID) \
		$(GO_BUILD) \
		sh -c 'make ut'

.PHONY: fv
fv: bin/calicoq
	CALICOQ=`pwd`/$^ fv/run-test

.PHONY: fv-containerized
fv-containerized: build-image run-etcd
	docker run --net=host --privileged \
		--rm -t \
		--entrypoint '/bin/sh' \
		-v $(CURDIR):/code/$(PACKAGE_NAME) \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-w /code/$(PACKAGE_NAME) \
		$(GO_BUILD) \
		-c 'CALICOQ=`pwd`/$(BINARY) fv/run-test'

.PHONY: st
st: bin/calicoq
	CALICOQ=`pwd`/$^ st/run-test

.PHONY: st-containerized
st-containerized: build-image
	docker run --net=host --privileged \
		--rm -t \
		--entrypoint '/bin/sh' \
		-v $(CURDIR):/code/$(PACKAGE_NAME) \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-w /code/$(PACKAGE_NAME) \
		$(GO_BUILD) \
		-c 'CALICOQ=`pwd`/$(BINARY) st/run-test'

.PHONY: scale-test
scale-test: bin/calicoq
	CALICOQ=`pwd`/$^ scale-test/run-test

.PHONY: scale-test-containerized
scale-test-containerized: build-image
	docker run --net=host --privileged \
		--rm -t \
		--entrypoint '/bin/sh' \
		-v $(CURDIR):/code/$(PACKAGE_NAME) \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-w /code/$(PACKAGE_NAME) \
		$(GO_BUILD) \
		-c 'CALICOQ=`pwd`/$(BINARY) scale-test/run-test'

# Build image for containerized testing
.PHONY: build-image
build-image: bin/calicoq
	docker build -t $(BUILD_IMAGE):$(BUILD_VER) `pwd`

# Clean up image from containerized testing
.PHONY: clean-image
clean-image:
	docker rmi -f $(shell docker images -a | grep $(BUILD_IMAGE) | awk '{print $$3}' | awk '!a[$$0]++')

# All calicoq Go source files.
CALICOQ_GO_FILES:=$(shell find calicoq -type f -name '*.go' -print)

bin/calicoq:
	$(MAKE) binary-containerized

.PHONY: binary-containerized
binary-containerized: $(CALICOQ_GO_FILES)
ifndef RELEASE_BUILD
	$(eval LDFLAGS:=$(RELEASE_LDFLAGS))
else
	$(eval LDFLAGS:=$(BUILD_LDFLAGS))
endif
	mkdir -p bin
	mkdir -p $(HOME)/.glide
	# vendor in a container first
	docker run --rm \
		-v $(CURDIR):/go/src/$(PACKAGE_NAME):rw \
		-v $$SSH_AUTH_SOCK:/ssh-agent --env SSH_AUTH_SOCK=/ssh-agent \
		-v $(HOME)/.glide:/home/user/.glide:rw \
		-e LOCAL_USER_ID=$(LOCAL_USER_ID) \
		-w /go/src/$(PACKAGE_NAME) \
		calico/go-build \
		sh -c 'glide install --strip-vendor'
	# Generate the protobuf bindings for Felix
	# Cannot do this together with vendoring since docker permissions in go-build are not perfect
	$(MAKE) vendor/github.com/projectcalico/felix/proto/felixbackend.pb.go
	# Create the binary
	docker run --rm -t \
		-v $(CURDIR):/go/src/$(PACKAGE_NAME) \
		-v $$SSH_AUTH_SOCK:/ssh-agent --env SSH_AUTH_SOCK=/ssh-agent \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-e LOCAL_USER_ID=$(LOCAL_USER_ID) \
		-w /go/src/$(PACKAGE_NAME) \
		calico/go-build \
		sh -c 'go build $(LDFLAGS) -o "$(BINARY)" "./calicoq/calicoq.go"'

.PHONY: binary
binary: vendor vendor/github.com/projectcalico/felix/proto/felixbackend.pb.go $(CALICOQ_GO_FILES)
	mkdir -p bin
	go build $(BUILD_LDFLAGS) -o "$(BINARY)" "./calicoq/calicoq.go"

.PHONY: release
release: clean clean-release release/calicoq

release/calicoq: $(CALICOQ_GO_FILES) clean
ifndef VERSION
	$(error VERSION is undefined - run using make release VERSION=v.X.Y.Z)
endif
	git tag $(VERSION)

	# Check to make sure the tag isn't "dirty"
	if git describe --tags --dirty | grep dirty; \
	then echo current git working tree is "dirty". Make sure you do not have any uncommitted changes ;false; fi

	# Build the calicoq binaries and image
	$(MAKE) binary-containerized RELEASE_BUILD=1
	$(MAKE) build-image

	# Make the release directory and move over the relevant files
	mkdir -p release
	mv $(BINARY) release/calicoq-$(CALICOQ_GIT_DESCRIPTION)
	ln -f release/calicoq-$(CALICOQ_GIT_DESCRIPTION) release/calicoq

	# Check that the version output includes the version specified.
	# Tests that the "git tag" makes it into the binaries. Main point is to catch "-dirty" builds
	# Release is currently supported on darwin / linux only.
	if ! docker run $(BUILD_IMAGE) version | grep 'Version:\s*$(VERSION)$$'; then \
	  echo "Reported version:" `docker run $(BUILD_IMAGE) version` "\nExpected version: $(VERSION)"; \
	  false; \
	else \
	  echo "Version check passed\n"; \
	fi

	# Retag images with correct version and registry prefix
	docker tag $(BUILD_IMAGE) $(REGISTRY_PREFIX)$(BUILD_IMAGE):$(VERSION)

	# Check that images were created recently and that the IDs of the versioned and latest images match
	@docker images --format "{{.CreatedAt}}\tID:{{.ID}}\t{{.Repository}}:{{.Tag}}" $(BUILD_IMAGE)
	@docker images --format "{{.CreatedAt}}\tID:{{.ID}}\t{{.Repository}}:{{.Tag}}" $(REGISTRY_PREFIX)$(BUILD_IMAGE):$(VERSION)

	@echo "\nNow push the tag and images."
	@echo "git push origin $(VERSION)"
	@echo "gcloud auth configure-docker"
	@echo "docker push $(REGISTRY_PREFIX)$(BUILD_IMAGE):$(VERSION)"
	@echo "\nIf this release version is the newest stable release, also tag and push the"
	@echo "images with the 'latest' tag"
	@echo "docker tag $(BUILD_IMAGE) $(REGISTRY_PREFIX)$(BUILD_IMAGE):latest"
	@echo "docker push $(REGISTRY_PREFIX)$(BUILD_IMAGE):latest"

.PHONY: compress-release
compressed-release: release/calicoq
	# Requires "upx" to be in your PATH.
	# Compress the executable with upx.  We get 4:1 compression with '-8'; the
	# more agressive --best gives a 0.5% improvement but takes several minutes.
	upx -8 release/calicoq-$(CALICOQ_GIT_DESCRIPTION)
	ln -f release/calicoq-$(CALICOQ_GIT_DESCRIPTION) release/calicoq

# Generate the protobuf bindings for Felix.
vendor/github.com/projectcalico/felix/proto/felixbackend.pb.go: vendor/github.com/projectcalico/felix/proto/felixbackend.proto
	docker run --rm -v `pwd`/vendor/github.com/projectcalico/felix/proto:/src:rw \
	              calico/protoc \
	              --gogofaster_out=. \
	              felixbackend.proto

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

.PHONY: clean-release
clean-release:
	-rm -rf release

.PHONY: clean
clean:
	-rm -f *.created
	find . -name '*.pyc' -exec rm -f {} +
	-rm -rf build bin release vendor
	-docker rm -f calico-build
	-docker rmi calico/build
	-docker rmi $(BUILD_IMAGE) -f
	-docker rmi calico/go-build -f
