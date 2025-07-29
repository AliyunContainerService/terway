
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.29.0

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

GO_BUILD_TAGS ?= privileged

REGISTRY ?= registry.cn-hangzhou.aliyuncs.com/acs
GIT_COMMIT_SHORT ?= $(shell git rev-parse --short=8 HEAD 2>/dev/null)

BUILD_PLATFORMS ?= linux/amd64,linux/arm64


# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./pkg/apis/..." output:crd:artifacts:config=pkg/apis/crds

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./pkg/apis/..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	GOOS=linux go vet --tags "$(GO_BUILD_TAGS)" ./...

.PHONY: test
test: manifests generate fmt vet envtest datapath-test## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test -race --tags "$(GO_BUILD_TAGS)" $$(go list ./... | grep -Ev '/e2e|/mocks|/generated|/apis|/examples|/tests|/rpc|/windows') -coverprofile coverage.txt

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter & yamllint
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run

.PHONY: datapath-test
datapath-test: ## Run datapath tests using the Makefile in tests/kind directory.
	make -C tests/kind datapath-test
##@ Build

.PHONY: build
build: manifests generate fmt vet build-terway build-terway-controlplane

PUSH ?= false

BUILD_ARGS = --push=$(PUSH) --build-arg GIT_VERSION=$(GIT_COMMIT_SHORT) --platform $(BUILD_PLATFORMS)
ifdef GITHUB_ACTIONS
BUILD_ARGS += --cache-from type=gha,scope=$(GHA_SCOPE) --cache-to type=gha,scope=$(GHA_SCOPE)
endif

.PHONY: build-policy
build-policy: GHA_SCOPE = policy
build-policy:
	docker buildx build $(BUILD_ARGS) -t $(REGISTRY)/terway:policy-$(GIT_COMMIT_SHORT) -f deploy/images/policy/Dockerfile .

.PHONY: build-terway
build-terway: GHA_SCOPE = terway
build-terway:
	docker buildx build $(BUILD_ARGS) -t $(REGISTRY)/terway:$(GIT_COMMIT_SHORT) -f deploy/images/terway/Dockerfile .

.PHONY: build-terway-controlplane
build-terway-controlplane: GHA_SCOPE = controlplane
build-terway-controlplane:
	docker buildx build $(BUILD_ARGS) -t $(REGISTRY)/terway-controlplane:$(GIT_COMMIT_SHORT) -f deploy/images/terway-controlplane/Dockerfile .

.PHONY: build-push
build-push: build-push-terway build-push-terway-controlplane

.PHONY: build-push-policy
build-push-policy: PUSH = true
build-push-policy: build-policy

.PHONY: build-push-terway
build-push-terway: PUSH = true
build-push-terway: build-terway

.PHONY: build-terway-controlplane
build-push-terway-controlplane: PUSH = true
build-push-terway-controlplane: build-terway-controlplane

##@ Dependencies
.PHONY: go-generate
go-generate:
	@echo "Running go generate"
	@go generate ./...

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen-$(CONTROLLER_TOOLS_VERSION)
ENVTEST ?= $(LOCALBIN)/setup-envtest-$(ENVTEST_VERSION)
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)

## Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.17.2
ENVTEST_VERSION ?= latest
GOLANGCI_LINT_VERSION ?= v2.1.6

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/cmd/golangci-lint,${GOLANGCI_LINT_VERSION})

golangci-lint-docker:
	docker run --rm -v $(shell pwd):/app -w /app golangci/golangci-lint:${GOLANGCI_LINT_VERSION} golangci-lint run -v


# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary (ideally with version)
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f $(1) ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv "$$(echo "$(1)" | sed "s/-$(3)$$//")" $(1) ;\
}
endef
