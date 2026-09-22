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

CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint
GO_LICENCE_DETECTOR ?= $(LOCALBIN)/go-licence-detector

# Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.16.5
GOLANGCI_LINT_VERSION ?= v2.12.2
GO_LICENCE_DETECTOR_VERSION ?= v0.10.0
PATH := $(abspath $(LOCALBIN)):$(PATH)

.PHONY: manifests
manifests: controller-gen ## Generate CRD manifests
	$(CONTROLLER_GEN) crd paths="./pkg/..." output:crd:artifacts:config=$(KARTA_CRDS_DIR)

.PHONY: generate
generate: controller-gen ## Generate DeepCopy methods
	$(CONTROLLER_GEN) object paths="./..."

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
tidy: ## Run go mod tidy (rewrites go.mod and go.sum)
	go mod tidy

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
generate-licenses: tidy go-licence-detector download-dependencies ## Regenerate NOTICE and THIRD_PARTY_LICENSES from current dependencies.
	@set -eu; \
	echo "Generating NOTICE and THIRD_PARTY_LICENSES files from current dependencies using go-licence-detector"; \
	go mod download -json | $(GO_LICENCE_DETECTOR) \
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
