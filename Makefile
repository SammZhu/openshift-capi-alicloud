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
	# config/crd/bases is the CRD SSOT; mirror it into the OLM bundle so the bundle
	# never drifts (verify-manifests enforces this).
	cp config/crd/bases/*.yaml bundle/manifests/
	@echo "Generated DeepCopy + CRD manifests (config/crd/bases) and synced bundle CRDs"

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

# ── Integration tests (envtest: real kube-apiserver + etcd, no cloud) ────────────
# Gated behind the `integration` build tag so plain `make test` stays asset-free
# (works air-gapped). `make test-integration` boots a real apiserver and drives
# the reconcilers against it; Alibaba cloud calls are served by pkg/client/fake.
ENVTEST_K8S_VERSION ?= 1.31.0
ENVTEST = $(shell pwd)/bin/setup-envtest
# CAPI core CRDs (Cluster/Machine) come from the cluster-api module in the cache.
CAPI_CRD_DIR := $(shell go list -m -f '{{.Dir}}' sigs.k8s.io/cluster-api)/config/crd/bases

.PHONY: setup-envtest
setup-envtest: ## Download setup-envtest + the apiserver/etcd test binaries
	@if ! [ -x "$(ENVTEST)" ]; then \
		echo "Installing setup-envtest..."; \
		GOBIN=$(shell pwd)/bin go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest; \
	fi

.PHONY: test-integration
test-integration: setup-envtest ## Run envtest integration tests (build tag: integration)
	KUBEBUILDER_ASSETS="$$('$(ENVTEST)' use $(ENVTEST_K8S_VERSION) --bin-dir '$(shell pwd)/bin' -p path)" \
	CAPI_CRD_DIR="$(CAPI_CRD_DIR)" \
	go test -tags integration -count=1 -v ./internal/controller/...

# Verify the vendored CAPI core manifest (in the sibling alibaba-openshift repo)
# applies cleanly against a real apiserver — air-gap-independent risk reduction
# before a live run. Override CORE_MANIFEST if the deploy repo is elsewhere.
CORE_MANIFEST ?= $(shell pwd)/../alibaba-openshift/custom_manifests/cluster-api-core.yaml
.PHONY: test-core-manifest
test-core-manifest: setup-envtest ## envtest: verify cluster-api-core.yaml applies cleanly
	KUBEBUILDER_ASSETS="$$('$(ENVTEST)' use $(ENVTEST_K8S_VERSION) --bin-dir '$(shell pwd)/bin' -p path)" \
	CORE_MANIFEST="$(CORE_MANIFEST)" \
	go test -tags integration -count=1 -v ./test/coremanifest/

# Local kind smoke for the clusterctl install path (G3-5) + a CAPI reconcile
# smoke (G7-2): clusterctl init this provider, render+apply the day-2 worker
# template, assert the controller/webhooks come up and the external control plane
# reconciles. Hermetic — no real Alibaba creds, no ECS. Needs kind + clusterctl
# (>=v1.11) + a container runtime (docker, else podman). KEEP_CLUSTER=1 to debug.
.PHONY: test-clusterctl-smoke
test-clusterctl-smoke: ## kind smoke: clusterctl install + external-CP reconcile (no cloud)
	@command -v kind >/dev/null 2>&1       || { echo "need 'kind' on PATH (brew install kind)"; exit 2; }
	@command -v clusterctl >/dev/null 2>&1 || { echo "need 'clusterctl' on PATH (brew install clusterctl)"; exit 2; }
	hack/kind-smoke.sh

# Friendly alias for first-time users: the SAME hermetic kind run as
# test-clusterctl-smoke, framed as a "watch it work in ~5 min, no cloud" demo.
# See docs/QUICKSTART.md. KEEP_CLUSTER=1 leaves the kind cluster up to poke at.
.PHONY: demo
demo: test-clusterctl-smoke ## 5-minute hermetic demo: install the provider on kind and watch it reconcile (no Alibaba account, no ECS)

# Assert the OLM bundle stays in sync: the CSV install-strategy Deployment matches
# the canonical controller Deployment (config/manager/deployment.yaml) field-for-
# field except the OLM-managed webhook cert volume + the image, and the CSV version
# is self-consistent across name/version/containerImage/image. Catches a half-done
# manual bundle bump or a controller change not mirrored into the CSV.
.PHONY: verify-bundle
verify-bundle: ## Verify the OLM CSV deployment + version stay in sync (G12 invariant)
	python3 hack/verify-bundle-sync.py

# Assert the whole manifest SSOT is consistent: the OLM bundle CRDs match
# config/crd/bases (run `make generate` to resync), plus verify-bundle. This is the
# drift gate for config/ being the single source consumed by ansible/OLM/clusterctl.
.PHONY: verify-manifests
verify-manifests: verify-bundle ## Verify bundle CRDs match config/crd/bases (SSOT)
	@for f in config/crd/bases/*.yaml; do \
	  b="bundle/manifests/$$(basename $$f)"; \
	  diff -q "$$f" "$$b" >/dev/null || { echo "bundle CRD drift: $$(basename $$f) — run 'make generate'"; exit 1; }; \
	done
	@echo "bundle CRDs match config/crd/bases"

# Emit the clusterctl release artifacts (components + metadata + template) into out/
# from the config/ SSOT. CAPA_IMAGE pins the controller image.
.PHONY: release
release: ## Emit clusterctl release artifacts into out/ from the config/clusterctl SSOT (cert-manager webhook certs, vanilla-k8s)
	CAPA_KUSTOMIZE_DIR=$(CURDIR)/config/clusterctl hack/gen-clusterctl-components.sh out/infrastructure-components.yaml
	cp metadata.yaml out/metadata.yaml
	cp templates/cluster-template.yaml out/cluster-template.yaml
	@echo "clusterctl release artifacts in out/"

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
