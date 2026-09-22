# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
KARTA_CHART_DIR := $(PROJECT_DIR)/charts/karta
KARTA_CRDS_DIR := $(KARTA_CHART_DIR)/crds

HELM_CHART_VERSION ?= 0.0.1

# Every Go module in the repo. An untidy manifest in a module that ships no
# binary (the examples, the e2e fixtures) still breaks a downstream `go get`.
GO_MODULES := . cli operator hack/imagelock \
	docs/examples/quickstart docs/examples/controller-runtime \
	hack/e2e/operators/nim/image

CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint
GO_LICENCE_DETECTOR ?= $(LOCALBIN)/go-licence-detector

# Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.16.5
GO_LICENCE_DETECTOR_VERSION ?= v0.10.0
# GOLANGCI_LINT_VERSION is defined in hack/tools.mk (shared with operator/Makefile).
include hack/tools.mk
PATH := $(abspath $(LOCALBIN)):$(PATH)

.PHONY: manifests
manifests: controller-gen ## Generate CRD manifests
	$(CONTROLLER_GEN) crd paths="./pkg/..." output:crd:artifacts:config=$(KARTA_CRDS_DIR)

.PHONY: generate
generate: controller-gen ## Generate DeepCopy methods
	$(CONTROLLER_GEN) object paths="./pkg/..."

.PHONY: generate-mocks
generate-mocks: ## Generate mocks using go generate
	go generate ./pkg/...

.PHONY: test
test: generate-mocks ## Run tests with mock generation
	go test ./...

lint-go: golangci-lint
	echo "Running golangci linter"
	$(GOLANGCI_LINT) run -v -c .golangci.yml
.PHONY: lint-go

fmt-go:
	go fmt ./...
.PHONY: fmt-go

vet-go:
	go vet ./...
.PHONY: vet-go

lint: fmt-go vet-go lint-go 
.PHONY: lint

.PHONY: tidy
tidy: ## Run go mod tidy in every module (rewrites go.mod and go.sum)
	@set -e; \
	for module in $(GO_MODULES); do \
		(cd $$module && go mod tidy); \
	done

.PHONY: validate
validate: tidy generate manifests generate-mocks generate-licenses
	@git diff --exit-code 

.PHONY: install-crd
install-crd: manifests ## Install CRDs into the cluster
	kubectl apply --server-side -f $(KARTA_CRDS_DIR)

.PHONY: uninstall-crd
uninstall-crd: ## Uninstall CRDs from the cluster
	kubectl delete -f $(KARTA_CRDS_DIR) --ignore-not-found

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	@[ -f "$(CONTROLLER_GEN)" ] || { \
	set -e; \
	echo "Downloading controller-gen@$(CONTROLLER_TOOLS_VERSION)" ;\
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION) ;\
	}

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	@[ -f "$(GOLANGCI_LINT)" ] || { \
	set -e; \
	echo "Downloading golangci-lint@$(GOLANGCI_LINT_VERSION)" ;\
	curl -sSfL https://golangci-lint.run/install.sh  | sh -s -- -b $(LOCALBIN) $(GOLANGCI_LINT_VERSION) ;\
	}

.PHONY: go-licence-detector
go-licence-detector: $(GO_LICENCE_DETECTOR) ## Download go-licence-detector locally if necessary.
$(GO_LICENCE_DETECTOR): $(LOCALBIN)
	@[ -f "$(GO_LICENCE_DETECTOR)" ] || { \
	set -e; \
	echo "Downloading go-licence-detector@$(GO_LICENCE_DETECTOR_VERSION)" ;\
	GOBIN=$(LOCALBIN) go install go.elastic.co/go-licence-detector@$(GO_LICENCE_DETECTOR_VERSION) ;\
	}

.PHONY: generate-licenses
generate-licenses: go-licence-detector download-dependencies ## Regenerate NOTICE and THIRD_PARTY_LICENSES from current dependencies.
	@set -eu; \
	echo "Generating NOTICE and THIRD_PARTY_LICENSES files from current dependencies using go-licence-detector"; \
	go mod download -json > $(LOCALBIN)/deps.json; \
	$(GO_LICENCE_DETECTOR) -in $(LOCALBIN)/deps.json \
		-noticeTemplate=hack/licenses/notice.tpl \
		-noticeOut=NOTICE \
		-depsTemplate=hack/licenses/third_party_licenses.tpl \
		-depsOut=THIRD_PARTY_LICENSES; \
	echo "Done"

.PHONY: download-dependencies
download-dependencies:
	go mod download

.PHONY: check
check: download-dependencies validate test

##@ Helm

.PHONY: helm-build
helm-build: ## Build the helm chart
	helm package $(KARTA_CHART_DIR) --version $(HELM_CHART_VERSION) --app-version $(HELM_CHART_VERSION)

.PHONY: helm-lint
helm-lint: ## Lint the helm chart
	helm lint $(KARTA_CHART_DIR)

.PHONY: helm-validate
helm-validate: ## Validate the helm chart renders
	helm template $(KARTA_CHART_DIR)
	helm template $(KARTA_CHART_DIR) --set global.fipsMode=only

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

# Pick which operators to install:
#   make e2e-up                          # everything
#   make e2e-up WORKLOADS="jobset lws"   # a subset - one provision, deps resolved once
.PHONY: e2e-up
e2e-up: ## Provision a kind cluster + operators (WORKLOADS="jobset kuberay" for a subset, or "all"; CLUSTER_NAME=<name> for an isolated parallel cluster)
	CLUSTER_NAME=$(CLUSTER_NAME) ./hack/e2e/up.sh $(WORKLOADS)

.PHONY: e2e-down
e2e-down: ## Tear down the e2e cluster (set CLUSTER_NAME for a named one)
	CLUSTER_NAME=$(CLUSTER_NAME) ./hack/e2e/down.sh

# The e2e shell scripts to shellcheck: the provisioner, teardown, the shared
# helpers, and every per-operator install.sh/verify.sh.
E2E_SHELL := hack/e2e/up.sh hack/e2e/down.sh \
	hack/e2e/operators/_common.sh \
	$(wildcard hack/e2e/operators/*/install.sh) \
	$(wildcard hack/e2e/operators/*/verify.sh)

.PHONY: lint-shell
lint-shell: ## shellcheck the e2e shell scripts (-x follows sourced files)
	shellcheck -x $(E2E_SHELL)
