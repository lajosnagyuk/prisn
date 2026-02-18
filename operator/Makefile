# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/prisn/prisn-operator:latest

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.31.0

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
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
manifests: ## Generate CRD manifests.
	@echo "CRD manifests are maintained manually in config/crd/bases/"

.PHONY: generate
generate: ## Generate code (deepcopy functions).
	@echo "DeepCopy functions are maintained manually in api/v1alpha1/zz_generated.deepcopy.go"

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: fmt vet ## Run tests.
	go test ./... -coverprofile cover.out

##@ Build

.PHONY: build
build: fmt vet ## Build operator binary.
	go build -o bin/prisn-operator ./cmd/

.PHONY: run
run: fmt vet ## Run operator from your host.
	go run ./cmd/ --metrics-bind-address=:8080 --health-probe-bind-address=:8081

.PHONY: docker-build
docker-build: ## Build docker image.
	docker build -t ${IMG} -f Dockerfile ..

.PHONY: docker-push
docker-push: ## Push docker image.
	docker push ${IMG}

##@ Deployment

.PHONY: install
install: ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	kubectl apply -k config/crd

.PHONY: uninstall
uninstall: ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	kubectl delete -k config/crd

.PHONY: deploy
deploy: ## Deploy operator to the K8s cluster specified in ~/.kube/config.
	kubectl apply -k config/default

.PHONY: undeploy
undeploy: ## Undeploy operator from the K8s cluster specified in ~/.kube/config.
	kubectl delete -k config/default

##@ Examples

.PHONY: example-app
example-app: ## Deploy example PrisnApp
	kubectl apply -f config/samples/prisnapp_sample.yaml

.PHONY: example-job
example-job: ## Deploy example PrisnJob
	kubectl apply -f config/samples/prisnjob_sample.yaml

.PHONY: example-cronjob
example-cronjob: ## Deploy example PrisnCronJob
	kubectl apply -f config/samples/prisncronjob_sample.yaml
