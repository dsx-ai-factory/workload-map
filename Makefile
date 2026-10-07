# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

PROJECT_DIR := $(shell dirname "$(abspath $(lastword $(MAKEFILE_LIST)))")

# One output directory for build artifacts and downloaded tools.
LOCALBIN ?= $(PROJECT_DIR)/bin

# Version stamping. CI overrides VERSION with the scheme in push-artifacts.yaml
# (tag v1.2.3 -> 1.2.3, main -> 0.0.0-main-<sha>).
VERSION ?= $(shell git describe --tags --always --dirty --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || echo "0.0.0-main")
# Freeze the fallback once so image variables do not rerun it per recipe.
VERSION := $(VERSION)

VERSION_PKG := github.com/dsx-ai-factory/workload-map/pkg/version
GO_LDFLAGS  := -X $(VERSION_PKG).version=$(VERSION)
LDFLAGS     := -ldflags "$(GO_LDFLAGS)"

# The component inventory every aggregate fans out over.
COMPONENTS := lib cli operator karta-wasm release-helper

# Every Go module in the repo. The tidy target fans out over this rather than
# COMPONENTS because an untidy manifest in a module that ships no
# binary (the examples, the e2e fixtures) still breaks a downstream `go get`.
GO_MODULES := . cli karta-wasm operator test/e2e hack/imagelock \
	docs/examples/quickstart docs/examples/controller-runtime \
	hack/e2e/operators/nim/image

# cli and operator pin a root version the proxy cannot serve until the release
# tags are pushed. modules-check-cli and modules-check-operator tidy them
# out of tree instead.
TIDY_MODULES := $(filter-out cli operator,$(GO_MODULES))

KARTA_CHART_DIR := $(PROJECT_DIR)/charts/karta
KARTA_CRDS_DIR := $(KARTA_CHART_DIR)/crds

HELM_CHART_VERSION ?= 0.0.1

# Tool versions. Override on the command line, e.g.
#   make lint GOLANGCI_LINT_VERSION=v2.13.0
GOLANGCI_LINT_VERSION       ?= v2.12.2
CONTROLLER_TOOLS_VERSION    ?= v0.16.5
GO_LICENCE_DETECTOR_VERSION ?= v0.10.0
ENVTEST_VERSION             ?= release-0.23
GORELEASER_VERSION          ?= v2.18.1
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
GORELEASER          ?= $(LOCALBIN)/goreleaser-$(GORELEASER_VERSION)

GOLANGCI_LINT_FLAGS ?= $(if $(VERBOSE),-v)

# Container image settings (defaults for ghcr.io/dsx-ai-factory/workload-map OSS publishing).
# Override any component from the command line, e.g.:
#   make operator-image IMAGE_TAG=v1.2.3
#   make operator-image IMAGE_REGISTRY=ghcr.io/myorg/karta
IMAGE_REGISTRY ?= ghcr.io/dsx-ai-factory/workload-map
IMAGE_NAME     ?= karta-operator
IMAGE_TAG      ?= $(VERSION)
IMAGE_REPOSITORY ?= $(IMAGE_REGISTRY)/$(IMAGE_NAME)

CONTAINER_TOOL ?= docker
BUILD_ARGS     ?=
DIST_DIR       ?= $(PROJECT_DIR)/dist
# Where make install puts kli. make install-local uses $(HOME)/.local/bin instead.
PREFIX         ?= /usr/local
BINDIR         ?= $(PREFIX)/bin
RELEASE_HELPER_DIR := $(PROJECT_DIR)/hack/release
ROOT_MODULE         := github.com/dsx-ai-factory/workload-map

# Architectures for build-operator-all and operator-image-buildx-push.
PLATFORMS ?= linux/amd64 linux/arm64

empty :=
space := $(empty) $(empty)
comma := ,
PLATFORMS_CSV = $(subst $(space),$(comma),$(PLATFORMS))

.DEFAULT_GOAL := help

##@ Aggregates

.PHONY: fmt
fmt: $(addprefix fmt-,$(COMPONENTS)) ## Format every component (rewrites files)

.PHONY: fmt-check
fmt-check: $(addprefix fmt-check-,$(COMPONENTS)) ## Check formatting without modifying files

.PHONY: vet
vet: $(addprefix vet-,$(COMPONENTS)) ## go vet every component

.PHONY: lint
lint: $(addprefix lint-,$(COMPONENTS)) ## Lint every component (read-only, never rewrites files)

.PHONY: test
test: $(addprefix test-,$(COMPONENTS)) ## Test every component

.PHONY: check
check: $(addprefix check-,$(COMPONENTS)) ## Everything CI runs for Go. Run this before pushing

.PHONY: build
build: build-cli build-operator ## Build every binary into bin/ (the library has no artifact)

# Keeps the pinned tools and envtest assets in bin/; clean-all removes them too.
.PHONY: clean
clean: ## Remove build outputs, coverage profiles, and release and plugin artifacts
	rm -f "$(LOCALBIN)/kli" "$(LOCALBIN)/karta-operator" "$(LOCALBIN)/karta-operator-amd64" "$(LOCALBIN)/karta-operator-arm64"
	rm -f "$(LOCALBIN)"/*-tidy.mod "$(LOCALBIN)"/*-tidy.sum "$(LOCALBIN)"/*-license.mod "$(LOCALBIN)"/*-license.sum "$(LOCALBIN)"/*deps.json
	rm -f operator/cover-unit.out operator/cover-integration.out
	rm -f karta-wasm/karta.wasm karta-wasm/wasm_exec.js
	rm -f hack/imagelock/imagelock hack/release/release
	rm -f "$(PROJECT_DIR)"/karta-*.tgz
	rm -rf "$(DIST_DIR)" "$(IMAGE_LOCK_OUT_DIR)" headlamp-plugin/dist "$(PROJECT_DIR)/.artifacts"

# setup-envtest leaves its asset directories read-only, which blocks rm -rf without the chmod.
.PHONY: clean-all
clean-all: clean ## Run clean, then also remove bin/ (tools, envtest assets) and headlamp-plugin/node_modules
	[ ! -d "$(LOCALBIN)" ] || chmod -R u+w "$(LOCALBIN)"
	rm -rf "$(LOCALBIN)" headlamp-plugin/node_modules

.PHONY: install
install: build-cli ## Install kli into BINDIR (default /usr/local/bin) and load its completion from your bash or zsh rc file
	install -d "$(BINDIR)"
	install -m 0755 "$(LOCALBIN)/kli" "$(BINDIR)/kli"
	hack/kli-completion.sh install "$(LOCALBIN)/kli" "$(BINDIR)/kli"
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; *) echo "warning: $(BINDIR) is not on PATH" >&2 ;; esac

.PHONY: uninstall
uninstall: ## Remove kli from BINDIR and stop loading its completion from your bash or zsh rc file
	rm -f "$(BINDIR)/kli"
	hack/kli-completion.sh uninstall

.PHONY: install-local
install-local: ## Run install into $HOME/.local/bin, which needs no root
	$(MAKE) install PREFIX="$(HOME)/.local"

.PHONY: uninstall-local
uninstall-local: ## Run uninstall against $HOME/.local/bin
	$(MAKE) uninstall PREFIX="$(HOME)/.local"

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
	cd test/e2e && GOWORK=off go test ./recorder/...

.PHONY: check-lib
check-lib: fmt-check-lib vet-lib lint-lib validate verify-recordings test-lib test-replay ## Full library presubmit

##@ Karta WASM (karta-wasm module)

.PHONY: fmt-karta-wasm
fmt-karta-wasm: ## Format the karta-wasm module
	GOWORK=off go -C karta-wasm fmt ./...

.PHONY: fmt-check-karta-wasm
fmt-check-karta-wasm: ## Check karta-wasm formatting without modifying files
	@set -e; \
	unformatted="$$(gofmt -l karta-wasm)"; \
	test -z "$$unformatted" || { echo "go fmt required:"; echo "$$unformatted"; exit 1; }

.PHONY: vet-karta-wasm
vet-karta-wasm: ## go vet the karta-wasm module (host and js builds)
	GOWORK=off go -C karta-wasm vet ./...
	cd karta-wasm && GOWORK=off GOOS=js GOARCH=wasm go vet ./...

.PHONY: lint-karta-wasm
lint-karta-wasm: golangci-lint ## Lint the karta-wasm module
	cd karta-wasm && GOWORK=off $(GOLANGCI_LINT) run $(GOLANGCI_LINT_FLAGS) -c $(PROJECT_DIR)/.golangci.yml

.PHONY: test-karta-wasm
test-karta-wasm: ## Run the karta-wasm module tests on the host
	GOWORK=off go -C karta-wasm test ./...

.PHONY: check-karta-wasm
check-karta-wasm: fmt-check-karta-wasm vet-karta-wasm lint-karta-wasm test-karta-wasm ## Full karta-wasm presubmit

##@ Release helper (nested module)

.PHONY: fmt-release-helper
fmt-release-helper: ## Format the release helper module
	cd hack/release && GOWORK=off go fmt ./...

.PHONY: fmt-check-release-helper
fmt-check-release-helper: ## Check release helper formatting without modifying files
	@set -e; \
	cd hack/release; \
	dirs="$$(GOWORK=off go list -f '{{.Dir}}' ./...)"; \
	unformatted="$$(gofmt -l $$dirs)"; \
	test -z "$$unformatted" || { echo "go fmt required:"; echo "$$unformatted"; exit 1; }

.PHONY: vet-release-helper
vet-release-helper: ## go vet the release helper module
	cd hack/release && GOWORK=off go vet ./...

.PHONY: lint-release-helper
lint-release-helper: golangci-lint ## Lint the release helper module
	cd hack/release && GOWORK=off $(GOLANGCI_LINT) run $(GOLANGCI_LINT_FLAGS) -c $(PROJECT_DIR)/.golangci.yml

.PHONY: test-release-helper
test-release-helper: ## Test the release helper module
	cd hack/release && GOWORK=off go test ./...

.PHONY: modules-check-release-helper
modules-check-release-helper: ## Verify release helper module metadata is tidy and complete
	cd $(RELEASE_HELPER_DIR) && GOWORK=off go mod tidy -diff
	cd $(RELEASE_HELPER_DIR) && GOWORK=off go mod verify

.PHONY: check-release-helper
check-release-helper: fmt-check-release-helper vet-release-helper lint-release-helper modules-check-release-helper test-release-helper ## Full release helper presubmit

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
build-cli: goreleaser $(LOCALBIN) ## Build the karta CLI binary into bin/
	VERSION=$(VERSION) $(GORELEASER) build --snapshot --clean --single-target --id karta --output $(LOCALBIN)/kli

.PHONY: cli-verify-version
cli-verify-version: build-cli ## Assert the CLI binary reports the stamped version
	@set -e; \
	out="$$($(LOCALBIN)/kli --version)"; \
	echo "$$out"; \
	[ "$$out" = "$(VERSION)" ] || { \
		echo "version mismatch: got '$$out', want '$(VERSION)'" >&2; exit 1; }

.PHONY: cli-completion-install
cli-completion-install: build-cli ## Build kli and load its completion (of the installed kli if present) from your bash or zsh rc file
	hack/kli-completion.sh install "$(LOCALBIN)/kli" "$(BINDIR)/kli"

.PHONY: cli-completion-install-local
cli-completion-install-local: ## Run cli-completion-install, preferring the kli in $HOME/.local/bin
	$(MAKE) cli-completion-install PREFIX="$(HOME)/.local"

.PHONY: cli-completion-uninstall
cli-completion-uninstall: ## Stop loading kli completion from your bash or zsh rc file
	hack/kli-completion.sh uninstall

# go mod tidy ignores the workspace, so it cannot resolve the unpublished root
# pin. Tidy a copy carrying a local replace; its go.sum must travel with it.
.PHONY: modules-check-cli modules-check-operator
modules-check-cli: MODULE := cli
modules-check-operator: MODULE := operator
modules-check-cli modules-check-operator: $(LOCALBIN) ## Verify the module's third-party requirements are tidy
	@set -e; \
	cp $(MODULE)/go.mod $(LOCALBIN)/$(MODULE)-tidy.mod; \
	cp $(MODULE)/go.sum $(LOCALBIN)/$(MODULE)-tidy.sum; \
	cd $(MODULE); \
	GOWORK=off go mod edit -modfile=$(LOCALBIN)/$(MODULE)-tidy.mod \
		-replace github.com/dsx-ai-factory/workload-map=../; \
	GOWORK=off go mod tidy -diff -modfile=$(LOCALBIN)/$(MODULE)-tidy.mod

.PHONY: check-cli
check-cli: fmt-check-cli vet-cli lint-cli test-cli modules-check-cli cli-verify-version ## Full CLI presubmit

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
check-operator: fmt-check-operator vet-operator lint-operator modules-check-operator build-operator-e2e test-operator operator-version-smoke ## Full operator presubmit

.PHONY: build-operator
build-operator: $(LOCALBIN) ## Build the karta-operator binary for the host OS/arch
	cd operator && go build -trimpath $(LDFLAGS) -o $(LOCALBIN)/karta-operator ./cmd

.PHONY: operator-version-smoke
operator-version-smoke: build-operator ## Verify the operator reports the stamped version
	@set -e; \
	out="$$($(LOCALBIN)/karta-operator --version)"; \
	echo "$$out"; \
	[ "$$out" = "$(VERSION)" ] || { \
		echo "version mismatch: got '$$out', want '$(VERSION)'" >&2; exit 1; }

.PHONY: build-operator-linux-amd64
build-operator-linux-amd64: $(LOCALBIN) ## Cross-compile the operator for linux/amd64
	cd operator && GOOS=linux GOARCH=amd64 go build -trimpath $(LDFLAGS) -o $(LOCALBIN)/karta-operator-amd64 ./cmd

.PHONY: build-operator-linux-arm64
build-operator-linux-arm64: $(LOCALBIN) ## Cross-compile the operator for linux/arm64
	cd operator && GOOS=linux GOARCH=arm64 go build -trimpath $(LDFLAGS) -o $(LOCALBIN)/karta-operator-arm64 ./cmd

.PHONY: build-operator-all
build-operator-all: build-operator-linux-amd64 build-operator-linux-arm64 ## Cross-compile the operator for all supported platforms

# The operator image builds on the Go its go.mod asks for; the golang image does
# not fetch a newer toolchain (GOTOOLCHAIN=local), so a stale pin fails the build.
OPERATOR_GO_VERSION := $(shell awk '/^go /{print $$2; exit}' operator/go.mod)

.PHONY: operator-image
operator-image: ## Build the operator image for the host arch
	$(CONTAINER_TOOL) build $(BUILD_ARGS) \
		--build-arg TARGETARCH=$(shell go env GOARCH) \
		--build-arg GO_VERSION=$(OPERATOR_GO_VERSION) \
		--build-arg GO_LDFLAGS="$(GO_LDFLAGS)" \
		--tag $(IMAGE_REPOSITORY):$(IMAGE_TAG) \
		-f operator/Dockerfile \
		.

.PHONY: operator-image-push
operator-image-push: ## Push the operator image
	$(CONTAINER_TOOL) push $(IMAGE_REPOSITORY):$(IMAGE_TAG)

.PHONY: operator-image-buildx-push
operator-image-buildx-push: ## Build and push a multi-arch operator image via BuildKit (requires Docker)
	@[ "$(CONTAINER_TOOL)" = "docker" ] || { echo "Error: operator-image-buildx-push requires CONTAINER_TOOL=docker (got '$(CONTAINER_TOOL)')" >&2; exit 1; }
	$(CONTAINER_TOOL) buildx build $(BUILD_ARGS) \
		--platform $(PLATFORMS_CSV) \
		--build-arg GO_VERSION=$(OPERATOR_GO_VERSION) \
		--build-arg GO_LDFLAGS="$(GO_LDFLAGS)" \
		--provenance=false --sbom=false \
		--tag $(IMAGE_REPOSITORY):$(IMAGE_TAG) \
		-f operator/Dockerfile \
		--push \
		.

##@ Release

.PHONY: goreleaser-check
goreleaser-check: goreleaser ## Validate the GoReleaser configuration
	$(GORELEASER) check

.PHONY: release-build
release-build: build-cli build-operator ## Build both executable definitions for the current runner architecture

.PHONY: release-snapshot
release-snapshot: goreleaser release-validate ## Build the complete release locally without publishing
	VERSION=$(VERSION) $(GORELEASER) release --snapshot --clean

.PHONY: release-verify
release-verify: ## Verify the CLI archives and stamped executable versions in dist/
	cd $(RELEASE_HELPER_DIR) && GOWORK=off go run . verify-artifacts --dist $(DIST_DIR) --version $(VERSION)

# Not part of `check`: main carries the released pin until the bump lands, so
# gating pushes on it would redden main. Pull-request only, and needs the tags.
.PHONY: pin-check
pin-check: ## Check the synchronized pin is consistent and still unreleased
	cd $(RELEASE_HELPER_DIR) && GOWORK=off go run . validate-version --root $(PROJECT_DIR)

.PHONY: pin-bump
pin-bump: ## Set the synchronized pin in both modules and go.work to VERSION
	@set -eu; \
	printf '%s\n' "$(VERSION)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$$' || { \
		echo "VERSION must be X.Y.Z without a leading v, for example make pin-bump VERSION=0.3.0" >&2; \
		echo "got '$(VERSION)', which is the default derived from git describe" >&2; exit 1; }; \
	old="$$(sed -n 's|^replace $(subst .,\.,$(ROOT_MODULE)) \(v[^ ]*\) => \.|\1|p' $(PROJECT_DIR)/go.work)"; \
	[ -n "$$old" ] || { echo "no $(ROOT_MODULE) replace found in go.work" >&2; exit 1; }; \
	cd $(PROJECT_DIR); \
	GOWORK=off go mod edit -require=$(ROOT_MODULE)@v$(VERSION) cli/go.mod; \
	GOWORK=off go mod edit -require=$(ROOT_MODULE)@v$(VERSION) operator/go.mod; \
	go work edit -dropreplace=$(ROOT_MODULE)@$$old -replace=$(ROOT_MODULE)@v$(VERSION)=. go.work; \
	echo "pin $$old -> v$(VERSION) in cli/go.mod, operator/go.mod and go.work"

.PHONY: release-validate
release-validate: ## Validate the synchronized module requirements for VERSION
	cd $(RELEASE_HELPER_DIR) && GOWORK=off go run . validate-version --root $(PROJECT_DIR) --version $(VERSION)

.PHONY: release
release: goreleaser ## Publish a guarded root-tag release
	@set -eu; \
	[ -n "$${GITHUB_TOKEN:-}" ] || { echo "GITHUB_TOKEN is required" >&2; exit 1; }; \
	status="$$(git status --porcelain)"; \
	[ -z "$$status" ] || { echo "the working tree must be clean" >&2; exit 1; }; \
	tag="$$(git describe --tags --exact-match --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true)"; \
	printf '%s\n' "$$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "current ref is not a root vX.Y.Z tag" >&2; exit 1; }; \
	[ "$${tag#v}" = "$(VERSION)" ] || { echo "VERSION $(VERSION) must be $${tag#v} (the tag without the leading v)" >&2; exit 1; }; \
	(cd $(RELEASE_HELPER_DIR) && GOWORK=off go run . validate-version --root $(PROJECT_DIR) --version $(VERSION) --require-tags); \
	GORELEASER_CURRENT_TAG="$$tag" VERSION=$(VERSION) $(GORELEASER) release --clean

##@ Headlamp plugin

.PHONY: karta-wasm
karta-wasm: ## Build the WASM engine module (used by the Headlamp plugin)
	cd karta-wasm && GOWORK=off GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o karta.wasm .
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
generate-licenses: tidy go-licence-detector ## Regenerate NOTICE and THIRD_PARTY_LICENSES from current dependencies
	@set -eu; \
	echo "Generating NOTICE and THIRD_PARTY_LICENSES files from current dependencies using go-licence-detector"; \
	GOWORK=off go mod download -json > $(LOCALBIN)/root-deps.json; \
	cp cli/go.mod $(LOCALBIN)/cli-license.mod; \
	cp cli/go.sum $(LOCALBIN)/cli-license.sum; \
	GOWORK=off go mod edit -modfile=$(LOCALBIN)/cli-license.mod -droprequire=github.com/dsx-ai-factory/workload-map; \
	GOWORK=off go mod download -modfile=$(LOCALBIN)/cli-license.mod -json > $(LOCALBIN)/cli-deps.json; \
	(cd karta-wasm && GOWORK=off go mod download -json) > $(LOCALBIN)/karta-wasm-deps.json; \
	python3 hack/merge-go-deps.py $(LOCALBIN)/root-deps.json $(LOCALBIN)/cli-deps.json $(LOCALBIN)/karta-wasm-deps.json > $(LOCALBIN)/deps.json; \
	$(GO_LICENCE_DETECTOR) -in $(LOCALBIN)/deps.json \
		-noticeTemplate=hack/licenses/notice.tpl \
		-noticeOut=NOTICE \
		-depsTemplate=hack/licenses/third_party_licenses.tpl \
		-depsOut=THIRD_PARTY_LICENSES; \
	echo "Done"

.PHONY: tidy
tidy: ## Run go mod tidy in every module except cli and operator (rewrites go.mod and go.sum)
	@set -e; \
	for module in $(TIDY_MODULES); do \
		(cd $$module && go mod tidy); \
	done

.PHONY: validate
# status is captured into a variable rather than tested inline, so that a git
# that cannot run is a hard error. Inline, a failing git yields empty output and
# the emptiness check passes, reporting success having verified nothing.
validate: tidy lib-generate lib-manifests lib-generate-mocks generate-licenses generate-samples ## Fail if any generated or module manifest is stale or untracked
	@set -e; \
	status="$$(git status --porcelain)"; \
	test -z "$$status" || { echo "$$status"; \
		echo "generated files or module manifests are stale or untracked; run the generators and commit"; exit 1; }

.PHONY: download-dependencies
download-dependencies: ## Pre-warm the module cache for the library module
	GOWORK=off go mod download

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
	cd hack/imagelock && GOWORK=off go run . \
		--chart $(KARTA_CHART_DIR) \
		--version $(VERSION) \
		$(foreach p,$(IMAGE_LOCK_PLATFORMS),--platform $(p)) \
		$(foreach s,$(IMAGE_LOCK_SET),--set $(s)) \
		--out-dir $(IMAGE_LOCK_OUT_DIR)

.PHONY: image-lock-verify
image-lock-verify: ## Fail if the chart renders a container image the lock generator does not classify
	cd hack/imagelock && GOWORK=off go run . --chart $(KARTA_CHART_DIR) --verify-only

.PHONY: image-lock-test
image-lock-test: ## Run the image-lock generator unit tests
	cd hack/imagelock && GOWORK=off go test ./...

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
	cd test/e2e && GOWORK=off go build ./...
	cd test/e2e && GOWORK=off go test ./replay_tests/...

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
	cd test/e2e && GOWORK=off go test -count=1 ./recorder
	cd test/e2e && GOWORK=off CLUSTER_NAME=$(CLUSTER_NAME) $(E2E_KUBECONFIG) go test -count=1 -v -timeout $(E2E_TIMEOUT) ./flows $(if $(E2E_FOCUS)$(E2E_LABELS),-args $(if $(E2E_FOCUS),-ginkgo.focus="$(E2E_FOCUS)") $(if $(E2E_LABELS),-ginkgo.label-filter="$(E2E_LABELS)"))

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

.PHONY: goreleaser
goreleaser: $(GORELEASER) ## Download the pinned GoReleaser OSS binary locally
$(GORELEASER): | $(LOCALBIN)
	@set -e; \
	echo "Downloading goreleaser@$(GORELEASER_VERSION)"; \
	GOTOOLCHAIN=auto GOBIN=$(LOCALBIN) go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION); \
	mv $(LOCALBIN)/goreleaser $@

##@ Help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
	/^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-28s\033[0m %s\n", $$1, $$2 } \
	/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)
