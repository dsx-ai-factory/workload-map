# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

# One output directory for build artifacts and downloaded tools.
LOCALBIN ?= $(PROJECT_DIR)/bin

# Version stamping. CI overrides VERSION with the scheme in push-artifacts.yaml
# (tag v1.2.3 -> 1.2.3, main -> 0.0.0-main-<sha>).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-main")

VERSION_PKG := github.com/dsx-ai-factory/workload-map/pkg/version
GO_LDFLAGS  := -X $(VERSION_PKG).version=$(VERSION)
LDFLAGS     := -ldflags "$(GO_LDFLAGS)"

# The component inventory every aggregate fans out over.
PRIMARY_COMPONENTS := lib cli operator karta-wasm

KARTA_CHART_DIR := $(PROJECT_DIR)/charts/karta
KARTA_CRDS_DIR := $(KARTA_CHART_DIR)/crds

HELM_CHART_VERSION ?= 0.0.1

# Tool versions. Override on the command line, e.g.
#   make lint GOLANGCI_LINT_VERSION=v2.13.0
GOLANGCI_LINT_VERSION       ?= v2.12.2
CONTROLLER_TOOLS_VERSION    ?= v0.16.5
GO_LICENCE_DETECTOR_VERSION ?= v0.10.0
ENVTEST_VERSION             ?= release-0.23
# Kubernetes control-plane (apiserver, etcd, kubectl) version envtest downloads.
ENVTEST_K8S_VERSION         ?= 1.31.0

# Tool binaries, all installed into the single LOCALBIN. The version is part of
# the filename, and therefore part of make's target name, so bumping a version
# above asks for a path that does not exist yet and the tool is reinstalled. An
# unversioned name lets an old binary satisfy a new pin without a word.
CONTROLLER_GEN      ?= $(LOCALBIN)/controller-gen-$(CONTROLLER_TOOLS_VERSION)
GOLANGCI_LINT       ?= $(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
GO_LICENCE_DETECTOR ?= $(LOCALBIN)/go-licence-detector-$(GO_LICENCE_DETECTOR_VERSION)
ENVTEST             ?= $(LOCALBIN)/setup-envtest-$(ENVTEST_VERSION)

GOLANGCI_LINT_FLAGS ?= $(if $(VERBOSE),-v)

# Container image settings (defaults for ghcr.io/dsx-ai-factory/workload-map OSS publishing).
# Override any component from the command line, e.g.:
#   make operator-image IMAGE_TAG=v1.2.3
#   make operator-image IMAGE_REGISTRY=ghcr.io/myorg/karta
IMAGE_REGISTRY ?= ghcr.io/dsx-ai-factory/workload-map
IMAGE_NAME     ?= karta-operator
IMAGE_TAG      ?= $(VERSION)
IMAGE          ?= $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

CONTAINER_TOOL ?= docker
BUILD_ARGS     ?=

# Architectures for build-operator-all and operator-image-buildx-push.
PLATFORMS ?= linux/amd64 linux/arm64

empty :=
space := $(empty) $(empty)
comma := ,
PLATFORMS_CSV = $(subst $(space),$(comma),$(PLATFORMS))

.DEFAULT_GOAL := help

##@ Aggregates

.PHONY: fmt
fmt: $(addprefix fmt-,$(PRIMARY_COMPONENTS)) ## Format every component (rewrites files)

.PHONY: fmt-check
fmt-check: $(addprefix fmt-check-,$(PRIMARY_COMPONENTS)) ## Check formatting without modifying files

.PHONY: vet
vet: $(addprefix vet-,$(PRIMARY_COMPONENTS)) ## go vet every component

.PHONY: lint
lint: $(addprefix lint-,$(PRIMARY_COMPONENTS)) ## Lint every component (read-only, never rewrites files)

.PHONY: test
test: $(addprefix test-,$(PRIMARY_COMPONENTS)) ## Test every component

.PHONY: check
check: $(addprefix check-,$(PRIMARY_COMPONENTS)) ## Everything CI runs for Go. Run this before pushing

.PHONY: build
build: build-cli build-operator ## Build every binary into bin/ (the library has no artifact)

##@ Library (root module)

.PHONY: fmt-lib
fmt-lib: ## Format the library module
	go fmt ./...

.PHONY: fmt-check-lib
fmt-check-lib: ## Check library formatting without modifying files
	@set -e; \
	dirs="$$(go list -f '{{.Dir}}' ./...)"; \
	unformatted="$$(gofmt -l $$dirs)"; \
	test -z "$$unformatted" || { echo "go fmt required:"; echo "$$unformatted"; exit 1; }

.PHONY: vet-lib
vet-lib: ## go vet the library module
	go vet ./...

.PHONY: lint-lib
lint-lib: golangci-lint ## Lint the library module (set VERBOSE=1 for -v)
	$(GOLANGCI_LINT) run $(GOLANGCI_LINT_FLAGS) -c $(PROJECT_DIR)/.golangci.yml

.PHONY: test-lib
test-lib: lib-generate-mocks ## Run the library tests, plus the offline e2e recorder tests
	go test ./...
	cd test/e2e && go test ./recorder/...

.PHONY: check-lib
check-lib: fmt-check-lib vet-lib lint-lib validate verify-recordings test-lib test-replay ## Full library presubmit

##@ Karta WASM (karta-wasm module)

.PHONY: fmt-karta-wasm
fmt-karta-wasm: ## Format the karta-wasm module
	go -C karta-wasm fmt ./...

.PHONY: fmt-check-karta-wasm
fmt-check-karta-wasm: ## Check karta-wasm formatting without modifying files
	@set -e; \
	unformatted="$$(gofmt -l karta-wasm)"; \
	test -z "$$unformatted" || { echo "go fmt required:"; echo "$$unformatted"; exit 1; }

.PHONY: vet-karta-wasm
vet-karta-wasm: ## go vet the karta-wasm module (host and js builds)
	go -C karta-wasm vet ./...
	cd karta-wasm && GOOS=js GOARCH=wasm go vet ./...

.PHONY: lint-karta-wasm
lint-karta-wasm: golangci-lint ## Lint the karta-wasm module
	cd karta-wasm && $(GOLANGCI_LINT) run $(GOLANGCI_LINT_FLAGS) -c $(PROJECT_DIR)/.golangci.yml

.PHONY: test-karta-wasm
test-karta-wasm: ## Run the karta-wasm module tests on the host
	go -C karta-wasm test ./...

.PHONY: check-karta-wasm
check-karta-wasm: fmt-check-karta-wasm vet-karta-wasm lint-karta-wasm test-karta-wasm ## Full karta-wasm presubmit

##@ CLI

.PHONY: fmt-cli
fmt-cli: ## Format the CLI module
	cd cli && go fmt ./...

.PHONY: fmt-check-cli
fmt-check-cli: ## Check CLI formatting without modifying files
	@set -e; \
	cd cli; \
	dirs="$$(go list -f '{{.Dir}}' ./...)"; \
	unformatted="$$(gofmt -l $$dirs)"; \
	test -z "$$unformatted" || { echo "go fmt required:"; echo "$$unformatted"; exit 1; }

.PHONY: vet-cli
vet-cli: ## go vet the CLI module
	cd cli && go vet ./...

.PHONY: lint-cli
lint-cli: golangci-lint ## Lint the CLI module
	cd cli && $(GOLANGCI_LINT) run $(GOLANGCI_LINT_FLAGS) -c $(PROJECT_DIR)/.golangci.yml

.PHONY: test-cli
test-cli: ## Run the CLI unit tests
	cd cli && go test ./...

.PHONY: build-cli
build-cli: $(LOCALBIN) ## Build the karta CLI binary into bin/
	cd cli && go build -trimpath $(LDFLAGS) -o $(LOCALBIN)/karta .

.PHONY: cli-verify-version
cli-verify-version: build-cli ## Assert the CLI binary reports the stamped version
	@set -e; \
	out="$$($(LOCALBIN)/karta --version)"; \
	echo "$$out"; \
	[ "$$out" = "$(VERSION)" ] || { \
		echo "version mismatch: got '$$out', want '$(VERSION)'" >&2; exit 1; }

.PHONY: check-cli
check-cli: fmt-check-cli vet-cli lint-cli test-cli cli-verify-version ## Full CLI presubmit

##@ Operator

.PHONY: fmt-operator
fmt-operator: ## Format the operator module
	cd operator && go fmt ./...

.PHONY: fmt-check-operator
fmt-check-operator: ## Check operator formatting without modifying files
	@set -e; \
	cd operator; \
	dirs="$$(go list -f '{{.Dir}}' ./...)"; \
	unformatted="$$(gofmt -l $$dirs)"; \
	test -z "$$unformatted" || { echo "go fmt required:"; echo "$$unformatted"; exit 1; }

.PHONY: vet-operator
vet-operator: ## go vet the operator module
	cd operator && go vet ./...

.PHONY: lint-operator
lint-operator: golangci-lint ## Lint the operator module
	cd operator && $(GOLANGCI_LINT) run $(GOLANGCI_LINT_FLAGS) -c $(PROJECT_DIR)/.golangci.yml

.PHONY: test-operator-unit
test-operator-unit: ## Run the operator unit tests (no envtest binaries required)
	cd operator && go test -coverprofile=cover-unit.out ./pkg/... ./cmd/...

.PHONY: test-operator-integration
test-operator-integration: envtest ## Run the operator envtest suite (downloads control-plane binaries)
	cd operator && KUBEBUILDER_ASSETS="$$($(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
		go test -coverprofile=cover-integration.out ./test/integration/...

.PHONY: test-operator
test-operator: test-operator-unit test-operator-integration ## Run the operator unit and envtest suites

.PHONY: check-operator
check-operator: fmt-check-operator vet-operator lint-operator build-operator-e2e test-operator ## Full operator presubmit

.PHONY: build-operator
build-operator: $(LOCALBIN) ## Build the karta-operator binary for the host OS/arch
	cd operator && go build -trimpath $(LDFLAGS) -o $(LOCALBIN)/karta-operator ./cmd

.PHONY: build-operator-linux-amd64
build-operator-linux-amd64: $(LOCALBIN) ## Cross-compile the operator for linux/amd64
	cd operator && GOOS=linux GOARCH=amd64 go build -trimpath $(LDFLAGS) -o $(LOCALBIN)/karta-operator-amd64 ./cmd

.PHONY: build-operator-linux-arm64
build-operator-linux-arm64: $(LOCALBIN) ## Cross-compile the operator for linux/arm64
	cd operator && GOOS=linux GOARCH=arm64 go build -trimpath $(LDFLAGS) -o $(LOCALBIN)/karta-operator-arm64 ./cmd

.PHONY: build-operator-all
build-operator-all: build-operator-linux-amd64 build-operator-linux-arm64 ## Cross-compile the operator for all supported platforms

# The image targets pass GO_LDFLAGS in, so the symbol path lives only here.
# The build context is the repository root, because the replace directive in
# operator/go.mod resolves against the local pkg/ tree.

.PHONY: operator-image
operator-image: ## Build the operator image for the host arch
	$(CONTAINER_TOOL) build $(BUILD_ARGS) \
		--build-arg TARGETARCH=$(shell go env GOARCH) \
		--build-arg GO_LDFLAGS="$(GO_LDFLAGS)" \
		--tag $(IMAGE) \
		-f operator/Dockerfile \
		.

.PHONY: operator-image-push
operator-image-push: ## Push the operator image
	$(CONTAINER_TOOL) push $(IMAGE)

.PHONY: operator-image-buildx-push
operator-image-buildx-push: ## Build and push a multi-arch operator image with an SBOM attestation via BuildKit (requires Docker)
	@[ "$(CONTAINER_TOOL)" = "docker" ] || { echo "Error: operator-image-buildx-push requires CONTAINER_TOOL=docker (got '$(CONTAINER_TOOL)')" >&2; exit 1; }
	$(CONTAINER_TOOL) buildx build $(BUILD_ARGS) \
		--platform $(PLATFORMS_CSV) \
		--build-arg GO_LDFLAGS="$(GO_LDFLAGS)" \
		--tag $(IMAGE) \
		--attest type=sbom \
		-f operator/Dockerfile \
		--push \
		.

##@ Headlamp plugin

.PHONY: karta-wasm
karta-wasm: ## Build the WASM engine module (used by the Headlamp plugin)
	cd karta-wasm && GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o karta.wasm .
	rm -f karta-wasm/wasm_exec.js
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" karta-wasm/wasm_exec.js

.PHONY: headlamp-plugin-build
headlamp-plugin-build: karta-wasm ## Build the Headlamp plugin (requires Node.js >= 22)
	npm --prefix headlamp-plugin ci
	npm --prefix headlamp-plugin run lint
	npm --prefix headlamp-plugin run tsc
	npm --prefix headlamp-plugin run test
	npm --prefix headlamp-plugin run build

##@ Code generation

.PHONY: lib-manifests
lib-manifests: controller-gen ## Generate CRD manifests
	$(CONTROLLER_GEN) crd paths="./pkg/..." output:crd:artifacts:config=$(KARTA_CRDS_DIR)

.PHONY: lib-generate
lib-generate: controller-gen ## Generate DeepCopy methods
	$(CONTROLLER_GEN) object paths="./pkg/..."

.PHONY: lib-generate-mocks
lib-generate-mocks: ## Generate mocks using go generate
	go generate ./pkg/...

.PHONY: generate-samples
generate-samples: ## Regenerate docs/catalog/ from pkg/catalog
	go run ./hack/gen-samples

.PHONY: generate-licenses
generate-licenses: go-licence-detector ## Regenerate NOTICE and THIRD_PARTY_LICENSES from current dependencies
	@set -eu; \
	echo "Generating NOTICE and THIRD_PARTY_LICENSES files from current dependencies using go-licence-detector"; \
	go mod download -json > $(LOCALBIN)/root-deps.json; \
	(cd cli && go mod download -json) > $(LOCALBIN)/cli-deps.json; \
	(cd karta-wasm && go mod download -json) > $(LOCALBIN)/karta-wasm-deps.json; \
	python3 hack/merge-go-deps.py $(LOCALBIN)/root-deps.json $(LOCALBIN)/cli-deps.json $(LOCALBIN)/karta-wasm-deps.json > $(LOCALBIN)/deps.json; \
	$(GO_LICENCE_DETECTOR) -in $(LOCALBIN)/deps.json \
		-noticeTemplate=hack/licenses/notice.tpl \
		-noticeOut=NOTICE \
		-depsTemplate=hack/licenses/third_party_licenses.tpl \
		-depsOut=THIRD_PARTY_LICENSES; \
	echo "Done"

.PHONY: validate
# status is captured into a variable rather than tested inline, so that a git
# that cannot run is a hard error. Inline, a failing git yields empty output and
# the emptiness check passes, reporting success having verified nothing.
validate: lib-generate lib-manifests lib-generate-mocks generate-licenses generate-samples ## Fail if any generated file is stale or untracked
	@set -e; \
	status="$$(git status --porcelain)"; \
	test -z "$$status" || { echo "$$status"; \
		echo "generated files are stale or untracked; run the generators and commit"; exit 1; }

.PHONY: download-dependencies
download-dependencies: ## Pre-warm the module cache for the library module
	go mod download

##@ CRDs

.PHONY: install-crd
install-crd: lib-manifests ## Install CRDs into the cluster
	kubectl apply --server-side -f $(KARTA_CRDS_DIR)

.PHONY: uninstall-crd
uninstall-crd: ## Uninstall CRDs from the cluster
	kubectl delete -f $(KARTA_CRDS_DIR) --ignore-not-found

##@ Helm

.PHONY: helm-build
helm-build: ## Build the helm chart
	helm package $(KARTA_CHART_DIR) --version $(HELM_CHART_VERSION) --app-version $(HELM_CHART_VERSION)

.PHONY: helm-lint
helm-lint: ## Lint the helm chart
	helm lint $(KARTA_CHART_DIR)

# The crd-upgrader ships the CRDs in a ConfigMap, capped at ~1 MiB per etcd
# object; fail here if a growing CRD would breach it, not on a user's upgrade.
CRD_CONFIGMAP_MAX_BYTES ?= 1000000

.PHONY: helm-validate
helm-validate: ## Validate the chart renders and the CRD ConfigMap fits in etcd
	helm template $(KARTA_CHART_DIR)
	helm template $(KARTA_CHART_DIR) --set global.fipsMode=only
	@set -e; tmp=$$(mktemp); trap 'rm -f "$$tmp"' EXIT; helm template $(KARTA_CHART_DIR) -s templates/hooks/pre/crd-upgrader-configmap.yaml > "$$tmp"; s=$$(wc -c < "$$tmp"); echo "crd-upgrader ConfigMap: $$s bytes (max $(CRD_CONFIGMAP_MAX_BYTES))"; test $$s -le $(CRD_CONFIGMAP_MAX_BYTES) || { echo "error: CRD ConfigMap is $$s bytes, over the $(CRD_CONFIGMAP_MAX_BYTES) limit; it must fit in a single ~1 MiB etcd object"; exit 1; }

##@ Air-gap

IMAGE_LOCK_OUT_DIR ?= $(PROJECT_DIR)/dist
IMAGE_LOCK_PLATFORMS ?= linux/amd64 linux/arm64

.PHONY: image-lock
image-lock: ## Generate the per-platform ImageLock for a release (VERSION=vX.Y.Z)
	cd hack/imagelock && go run . \
		--chart $(KARTA_CHART_DIR) \
		--version $(VERSION) \
		$(foreach p,$(IMAGE_LOCK_PLATFORMS),--platform $(p)) \
		$(foreach s,$(IMAGE_LOCK_SET),--set $(s)) \
		--out-dir $(IMAGE_LOCK_OUT_DIR)

.PHONY: image-lock-verify
image-lock-verify: ## Fail if the chart renders a container image the lock generator does not classify
	cd hack/imagelock && go run . --chart $(KARTA_CHART_DIR) --verify-only

.PHONY: image-lock-test
image-lock-test: ## Run the image-lock generator unit tests
	cd hack/imagelock && go test ./...

##@ E2E

# Cluster name for the e2e targets. Override to run isolated clusters in parallel,
# e.g. make e2e-up CLUSTER_NAME=shard-a WORKLOADS="jobset kuberay"
CLUSTER_NAME ?= karta-e2e
# record-e2e records whatever cluster kubectl currently points at - a kind cluster from
# e2e-up or any cluster of your own (it only needs Karta installed). An explicit KUBECONFIG
# wins; otherwise a named kind cluster (CLUSTER_NAME=...) switches to its own kubeconfig file,
# matching hack/e2e/up.sh, so parallel clusters do not race on the shared current-context.
ifneq ($(strip $(KUBECONFIG)),)
E2E_KUBECONFIG := KUBECONFIG=$(KUBECONFIG)
else ifneq ($(CLUSTER_NAME),karta-e2e)
E2E_KUBECONFIG := KUBECONFIG=$(HOME)/.kube/kind-$(CLUSTER_NAME).kubeconfig
endif
# Overall go-test timeout for the online suite; override for a quick subset, e.g. E2E_TIMEOUT=10m.
E2E_TIMEOUT ?= 30m

# Select cases by operator with the same WORKLOADS list as e2e-up: record-e2e WORKLOADS="batch-job"
# records just the batch-job case (a comma is OR in ginkgo label filters). E2E_LABELS overrides it
# with a raw ginkgo label expression.
E2E_LABELS ?= $(subst $(space),$(comma),$(strip $(filter-out all,$(WORKLOADS))))

# FLOW="scaled" narrows a record to one flow by name (focuses the spec with that title);
# without WORKLOADS it matches that flow name across all workload types.
ifneq ($(strip $(FLOW)),)
E2E_FOCUS := $(strip $(FLOW))
endif

.PHONY: test-replay
test-replay: ## Replay the recorded fixtures through Karta offline (no cluster)
	cd test/e2e && go build ./...
	cd test/e2e && go test ./replay_tests/...

# Defaults and accepted values live in hack/e2e/global.env.
KARTA_WEBHOOK_MODE ?= auto
CERT_MANAGER ?= auto

# Pick which operators to install:
#   make e2e-up                          # everything
#   make e2e-up WORKLOADS="jobset lws"   # a subset - one provision, deps resolved once
#   make e2e-up WORKLOADS=none           # base only, no workload operators
.PHONY: e2e-up
e2e-up: ## Provision a kind cluster + operators (WORKLOADS=<list>|all|none; KARTA_WEBHOOK_MODE=auto|cert-manager|disabled; CLUSTER_NAME for a parallel cluster)
	CLUSTER_NAME=$(CLUSTER_NAME) \
	KARTA_WEBHOOK_MODE=$(KARTA_WEBHOOK_MODE) \
	CERT_MANAGER=$(CERT_MANAGER) \
	./hack/e2e/up.sh $(WORKLOADS)

# Overall go-test timeout for the operator suite. Separate from E2E_TIMEOUT, which
# caps the much longer workload-recording run.
E2E_OPERATOR_TIMEOUT ?= 15m

# Deliberately absent from check-operator: it needs a cluster, and check must not.
.PHONY: test-operator-e2e
# The e2e package is behind a build tag, so vet, golangci-lint and the unit tests all
# skip it. Compiling with no spec selected type checks it without needing a cluster.
.PHONY: build-operator-e2e
build-operator-e2e: ## Compile the operator e2e suite without running it (no cluster needed)
	cd operator && go test -tags e2e -run '^$$' ./test/e2e/...

test-operator-e2e: ## Run the operator e2e against the current cluster (KARTA_WEBHOOK_MODE must match the one e2e-up installed; CLUSTER_NAME for a named one)
	CLUSTER_NAME=$(CLUSTER_NAME) KARTA_WEBHOOK_MODE=$(KARTA_WEBHOOK_MODE) $(E2E_KUBECONFIG) ./hack/e2e/karta-operator/test.sh

.PHONY: e2e-down
e2e-down: ## Tear down the e2e cluster (set CLUSTER_NAME for a named one)
	CLUSTER_NAME=$(CLUSTER_NAME) ./hack/e2e/down.sh

# The recorder's own unit tests run first: they take a second, and a broken recorder
# should fail here rather than minutes into a live cluster run.
.PHONY: record-e2e
record-e2e: ## Record the fixtures against the current cluster - kind from e2e-up or your own (WORKLOADS="pod" a subset, FLOW="running" one flow, CLUSTER_NAME for a named kind cluster)
	@if [ "$(strip $(WORKLOADS))" = "none" ]; then \
		echo "record-e2e: WORKLOADS=none selects no cases"; exit 2; fi
	cd test/e2e && go test -count=1 ./recorder
	cd test/e2e && CLUSTER_NAME=$(CLUSTER_NAME) $(E2E_KUBECONFIG) go test -count=1 -v -timeout $(E2E_TIMEOUT) ./flows $(if $(E2E_FOCUS)$(E2E_LABELS),-args $(if $(E2E_FOCUS),-ginkgo.focus="$(E2E_FOCUS)") $(if $(E2E_LABELS),-ginkgo.label-filter="$(E2E_LABELS)"))

.PHONY: verify-recordings
verify-recordings: ## Fail if any recorded fixture ended with succeeded false (re-record it clean before pushing)
	@test -d test/e2e/recorded_data || { echo "no test/e2e/recorded_data dir"; exit 1; }
	@bad=$$(grep -rlE '^  succeeded: false' test/e2e/recorded_data --include='*.yaml'); \
	if [ -n "$$bad" ]; then echo "recordings that did not succeed:"; echo "$$bad"; exit 1; fi; \
	echo "all recordings succeeded"

# The e2e shell scripts to shellcheck: the provisioner, teardown, the karta-operator
# scripts, the shared helpers, and every per-operator install.sh/verify.sh.
E2E_SHELL := hack/e2e/up.sh hack/e2e/down.sh \
	$(wildcard hack/e2e/karta-operator/*.sh) \
	hack/e2e/operators/_common.sh \
	$(wildcard hack/e2e/operators/*/install.sh) \
	$(wildcard hack/e2e/operators/*/verify.sh)

.PHONY: lint-shell
lint-shell: ## shellcheck the e2e shell scripts (-x follows sourced files)
	shellcheck -x $(E2E_SHELL)

##@ Tools

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): | $(LOCALBIN)
	@set -e; \
	echo "Downloading controller-gen@$(CONTROLLER_TOOLS_VERSION)"; \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION); \
	mv $(LOCALBIN)/controller-gen $@

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): | $(LOCALBIN)
	@set -e; \
	echo "Downloading golangci-lint@$(GOLANGCI_LINT_VERSION)"; \
	tmp=$$(mktemp); \
	trap 'rm -f "$$tmp"' EXIT; \
	curl -sSfL --proto '=https' --proto-redir '=https' --tlsv1.2 https://golangci-lint.run/install.sh -o "$$tmp"; \
	sh "$$tmp" -b $(LOCALBIN) $(GOLANGCI_LINT_VERSION); \
	mv $(LOCALBIN)/golangci-lint $@

.PHONY: go-licence-detector
go-licence-detector: $(GO_LICENCE_DETECTOR) ## Download go-licence-detector locally if necessary.
$(GO_LICENCE_DETECTOR): | $(LOCALBIN)
	@set -e; \
	echo "Downloading go-licence-detector@$(GO_LICENCE_DETECTOR_VERSION)"; \
	GOBIN=$(LOCALBIN) go install go.elastic.co/go-licence-detector@$(GO_LICENCE_DETECTOR_VERSION); \
	mv $(LOCALBIN)/go-licence-detector $@

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): | $(LOCALBIN)
	@set -e; \
	echo "Downloading setup-envtest@$(ENVTEST_VERSION)"; \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION); \
	mv $(LOCALBIN)/setup-envtest $@

##@ Help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
	/^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-28s\033[0m %s\n", $$1, $$2 } \
	/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)
