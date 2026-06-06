# Makefile for openshift-capi-alicloud
# Cluster API Infrastructure Provider for Alibaba Cloud

# ── Variables ─────────────────────────────────────────────────────────────────
REGISTRY    ?= quay.io/samzhu
IMAGE_NAME  ?= openshift-capi-alicloud
IMAGE_TAG   ?= latest
IMAGE       := $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

MODULE      := github.com/SammZhu/openshift-capi-alicloud
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LD_FLAGS    := -X $(MODULE)/pkg/version.Raw=$(VERSION) -extldflags "-static"

GOARCH      ?= $(shell go env GOARCH)
GOOS        ?= $(shell go env GOOS)
CGO_ENABLED ?= 0

# Prefer podman, fall back to docker
ENGINE := $(shell command -v /opt/podman/bin/podman 2>/dev/null || command -v podman 2>/dev/null || command -v docker 2>/dev/null)

CONTROLLER_GEN_VERSION := v0.21.0
CONTROLLER_GEN         := $(shell pwd)/bin/controller-gen

# ── Default target ─────────────────────────────────────────────────────────────
.PHONY: all
all: fmt vet build test ## Run fmt, vet, build, and test

# ── Build ──────────────────────────────────────────────────────────────────────
.PHONY: build
build: bin/manager-linux-amd64 ## Cross-compile manager binary for Linux amd64

bin/manager-linux-amd64: $(shell find . -name '*.go' -not -path './vendor/*')
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-ldflags "$(LD_FLAGS)" \
		-o bin/manager-linux-amd64 \
		./cmd/main.go
	@echo "Built: bin/manager-linux-amd64 ($(shell ls -lh bin/manager-linux-amd64 | awk '{print $$5}'))"

# ── Container image ────────────────────────────────────────────────────────────
.PHONY: image
image: build ## Build container image
	$(ENGINE) build -t $(IMAGE) .
	@echo "Image: $(IMAGE)"

.PHONY: push
push: image ## Build and push container image
	$(ENGINE) push $(IMAGE)
	@echo "Pushed: $(IMAGE)"

# ── Code generation ────────────────────────────────────────────────────────────
.PHONY: generate
generate: controller-gen ## Regenerate DeepCopy and CRD manifests
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."
	# allowDangerousTypes: AlibabaCloudMachineSpec.spotPriceLimit is a *float64
	# (Alibaba prices its spot ceiling as a float; the SDK's SpotPriceLimit is a
	# float-backed string). controller-gen otherwise refuses to emit floats.
	$(CONTROLLER_GEN) crd:allowDangerousTypes=true paths="./api/..." output:crd:dir=config/crd/bases
	@echo "Generated DeepCopy and CRD manifests"

.PHONY: controller-gen
controller-gen: ## Download controller-gen if not present
	@if ! [ -x $(CONTROLLER_GEN) ]; then \
		echo "Downloading controller-gen $(CONTROLLER_GEN_VERSION)..."; \
		GOBIN=$(shell pwd)/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION); \
	fi

# ── Testing ────────────────────────────────────────────────────────────────────
.PHONY: test
test: ## Run unit tests
	go test -v -count=1 ./...

.PHONY: test-race
test-race: ## Run unit tests with race detector
	CGO_ENABLED=1 go test -race -count=1 ./...

# ── Code quality ───────────────────────────────────────────────────────────────
.PHONY: fmt
fmt: ## Run go fmt
	go fmt ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

# ── Utilities ──────────────────────────────────────────────────────────────────
.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin/

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z/_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
