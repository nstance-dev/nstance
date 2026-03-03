# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

YAML_HEADER := \# Nstance <https://nstance.dev>\n\# Copyright 2026 Nadrama Pty Ltd\n\# SPDX-License-Identifier: Apache-2.0

CURRENT := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))

BUILD_VERSION=$(shell git describe --tags --dirty --always)
BUILD_DATE=$(shell date -u '+%Y-%m-%dT%H:%M:%S')
COMMIT_HASH=$(shell git rev-parse --short HEAD)
COMMIT_DATE=$(shell git log -1 --format=%cd --date=format:'%Y-%m-%dT%H:%M:%S')
COMMIT_BRANCH=$(shell git rev-parse --abbrev-ref HEAD)

BUILDVARS_PKG=github.com/nstance-dev/nstance/internal/buildvars

BINARYDIR=$(CURRENT)bin
BINARIES=nstance-server nstance-agent nstance-operator nstance-admin

# Cross-compilation settings for SQLite support
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
CGO_ENABLED=1
EXTRA_LD_FLAGS=
ifeq ($(GOOS),linux)
	BUILD_TAGS=linux
	EXTRA_LD_FLAGS=-extldflags -static
	ifeq ($(GOARCH),amd64)
		CC=x86_64-linux-musl-gcc
		CXX=x86_64-linux-musl-g++
	else ifeq ($(GOARCH),arm64)
		CC=aarch64-linux-musl-gcc
		CXX=aarch64-linux-musl-g++
	endif
else ifeq ($(GOOS),darwin)
	ifeq ($(GOARCH),amd64)
		BUILD_TAGS=darwin amd64
		CC=clang
		CXX=clang++
	else ifeq ($(GOARCH),arm64)
		BUILD_TAGS=darwin arm64
		CC=clang
		CXX=clang++
	endif
endif

.PHONY: help check git-hooks proto fmt lint test e2e-admin e2e-k8s dev-tmux dev-tmux-k8s dev-operator dev-proxmox kind-create kind-provision kind-delete clean-dev build clean-build $(BINARIES) manifests tag images image-operator image-agent image-server image-admin publish publish-operator publish-agent publish-server publish-admin

help:
	@echo "Available Makefile commands:"
	@echo "  check       - Check dependencies are installed"
	@echo "  git-hooks   - Install git pre-commit hook"
	@echo "  proto       - Generate protobuf code"
	@echo "  fmt         - Format code"
	@echo "  lint        - Run golangci-lint"
	@echo "  test        - Run tests"
	@echo "  e2e-admin   - Run e2e test for admin CLI (requires dev-tmux running)"
	@echo "  e2e-k8s     - Run e2e test for K8s resource flow (requires dev-tmux-k8s running)"
	@echo "  dev-tmux    - Start dev environment with s3 + server only (for admin CLI testing)"
	@echo "  dev-tmux-k8s - Start dev environment with s3 + server + k8s + operator (for K8s testing)"
	@echo "  dev-operator - Run operator with air against KUBECONFIG (for Kind/real K8s)"
	@echo "  dev-proxmox - Start dev environment with Proxmox provider"
	@echo "  kind-create  - Create a Kind cluster for dev"
	@echo "  kind-provision - Install cert-manager, CAPI, and Nstance CRDs into Kind cluster"
	@echo "  kind-delete  - Delete the Kind dev cluster"
	@echo "  clean-dev   - Clean development environment"
	@echo "  build       - Build all binaries"
	@echo "  nstance-server"
	@echo "  nstance-agent"
	@echo "  nstance-operator"
	@echo "  clean-build - Remove build artifacts"
	@echo "  manifests   - Generate Kubernetes manifests"
	@echo "  tag         - Tag the current commit with a version"
	@echo "  images      - Build Docker images"
	@echo "  image-operator"
	@echo "  image-agent"
	@echo "  image-server"
	@echo "  image-admin"
	@echo "  publish     - Publish Docker images"
	@echo "  publish-operator"
	@echo "  publish-agent"
	@echo "  publish-server"
	@echo "  publish-admin"

check:
	@command -v go >/dev/null 2>&1 || { echo "Error: go is not installed. Please install it first."; exit 1; }
	@command -v tmux >/dev/null 2>&1 || { echo "Error: tmux is not installed. Please install it first."; exit 1; }
	@command -v air >/dev/null 2>&1 || { echo "Error: air is not installed. Please install it first."; exit 1; }
	@command -v overmind >/dev/null 2>&1 || { echo "Error: overmind is not installed. Please install it first."; exit 1; }
	@echo "All dependencies are installed. Available Makefile commands:"

git-hooks:
	@echo "Installing git hooks..."
	@mkdir -p $(CURRENT).git/hooks
	@cp $(CURRENT)scripts/pre-commit $(CURRENT).git/hooks/pre-commit
	@chmod +x $(CURRENT).git/hooks/pre-commit
	@echo "Git hooks installed!"

proto:
	protoc -I=$(CURRENT) \
	       --go_out=$(CURRENT)internal \
	       --go-grpc_out=$(CURRENT)internal \
	       --go_opt=paths=source_relative \
	       --go-grpc_opt=paths=source_relative \
	       $(CURRENT)proto/*.proto

fmt:
	@echo "Formatting code..."
	go fmt ./...

lint:
	@echo "Running linter..."
	golangci-lint run

test:
	@echo "Running tests..."
	go test -v -race -coverprofile=coverage.out ./...

e2e-admin:
	@$(CURRENT)scripts/test-e2e-admin.sh

e2e-k8s:
	@$(CURRENT)scripts/test-e2e-k8s.sh

dev-tmux:
	@$(CURRENT)scripts/check-dev-ports.sh
	@trap 'tmux list-sessions -F "#{session_name}" 2>/dev/null | grep "^nstance-.*-agents$$" | xargs -I{} tmux kill-session -t {} 2>/dev/null || true' EXIT; cd $(CURRENT) && OVERMIND_FORMATION=s3=1,server=2,k8s=0,operator=0 overmind start

dev-tmux-k8s:
	@$(CURRENT)scripts/check-dev-ports.sh
	@trap 'tmux list-sessions -F "#{session_name}" 2>/dev/null | grep "^nstance-.*-agents$$" | xargs -I{} tmux kill-session -t {} 2>/dev/null || true' EXIT; cd $(CURRENT) && OVERMIND_FORMATION=s3=1,server=2,k8s=1,operator=1 overmind start

dev-operator:
	@if [ ! -f "$(CURRENT)temp/operator/kubeconfig" ]; then \
		echo "Error: temp/operator/kubeconfig does not exist"; \
		echo "  See docs/DEV.md 'Using with a Real Kubernetes Cluster' for setup instructions"; \
		exit 1; \
	fi
	@if [ ! -f "$(CURRENT)temp/operator/config.yaml" ]; then \
		echo "Creating default temp/operator/config.yaml..."; \
		mkdir -p $(CURRENT)temp/operator; \
		printf 'cluster_id: example-cluster\ntenant: default\nshards:\n  dev-1:\n    registration_addr: "127.0.0.1:8992"\n    operator_addr: "127.0.0.1:8993"\n  dev-2:\n    registration_addr: "127.0.0.1:9002"\n    operator_addr: "127.0.0.1:9003"\n' > $(CURRENT)temp/operator/config.yaml; \
	fi
	@mkdir -p $(CURRENT)temp/logs
	cd $(CURRENT) && KUBECONFIG=$(CURRENT)temp/operator/kubeconfig NSTANCE_NAMESPACE=$${NSTANCE_NAMESPACE:-default} air -c scripts/air/operator.toml 2>&1 | tee temp/logs/operator.log

dev-proxmox:
	@if [ -z "$$PROXMOX_API_URL" ]; then \
		echo "Error: PROXMOX_API_URL is not set"; \
		echo "  export PROXMOX_API_URL='https://proxmox.example.com:8006/api2/json'"; \
		exit 1; \
	fi
	@if [ -z "$$PROXMOX_TOKEN_ID" ]; then \
		echo "Error: PROXMOX_TOKEN_ID is not set"; \
		echo "  export PROXMOX_TOKEN_ID='nstance@pve!nstance-token'"; \
		exit 1; \
	fi
	@if [ -z "$$PROXMOX_TOKEN_SECRET" ]; then \
		echo "Error: PROXMOX_TOKEN_SECRET is not set"; \
		echo "  export PROXMOX_TOKEN_SECRET='xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx'"; \
		exit 1; \
	fi
	@$(CURRENT)scripts/check-dev-ports.sh
	@trap 'tmux list-sessions -F "#{session_name}" 2>/dev/null | grep "^nstance-.*-agents$$" | xargs -I{} tmux kill-session -t {} 2>/dev/null || true' EXIT; cd $(CURRENT) && NSTANCE_DEV_CONFIG=examples/config-proxmox.jsonc OVERMIND_FORMATION=s3=1,server=1,k8s=1,operator=1 overmind start

KIND_CLUSTER_NAME ?= nstance-dev
KIND_CONFIG = $(CURRENT)temp/kind-config.yaml

kind-create:
	@command -v kind >/dev/null 2>&1 || { echo "Error: kind is not installed. See https://kind.sigs.k8s.io/"; exit 1; }
	@mkdir -p $(CURRENT)temp
	@if [ ! -f "$(KIND_CONFIG)" ]; then \
		printf 'kind: Cluster\napiVersion: kind.x-k8s.io/v1alpha4\nnodes:\n- role: control-plane\n' > $(KIND_CONFIG); \
		echo "Created $(KIND_CONFIG)"; \
	fi
	@if kind get clusters 2>/dev/null | grep -q "^$(KIND_CLUSTER_NAME)$$"; then \
		echo "Kind cluster '$(KIND_CLUSTER_NAME)' already exists."; \
	else \
		kind create cluster --name $(KIND_CLUSTER_NAME) --config $(KIND_CONFIG); \
		echo "Kind cluster '$(KIND_CLUSTER_NAME)' created."; \
	fi
	@echo "Context set to kind-$(KIND_CLUSTER_NAME)."

kind-provision:
	@test -f "$(BINARYDIR)/nstance-admin" || $(MAKE) nstance-admin
	@if [ ! -f "$(CURRENT)temp/dev-s3/cluster/ca.crt" ]; then \
		echo "Error: temp/dev-s3/cluster/ca.crt does not exist."; \
		echo "  Start the nstance-server first: make clean-dev && make dev-tmux"; \
		exit 1; \
	fi
	@echo "Installing cert-manager..."
	kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml
	kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=120s
	@echo "Installing Cluster API core components..."
	@CAPI_VERSION=$$(curl -sL https://api.github.com/repos/kubernetes-sigs/cluster-api/releases/latest | jq -r .tag_name) && \
		curl -sL "https://github.com/kubernetes-sigs/cluster-api/releases/download/$$CAPI_VERSION/core-components.yaml" \
		| sed -E 's/\$$\{[A-Za-z_][A-Za-z0-9_]*:=([^}]*)\}/\1/g' \
		| kubectl apply --server-side --force-conflicts -f -
	kubectl wait --for=condition=Available deployment --all -n capi-system --timeout=120s
	@echo "Installing Nstance CRDs..."
	kubectl apply -k $(CURRENT)config/crd
	@echo "Creating CAPI ServiceAccount and RBAC..."
	kubectl apply -f $(CURRENT)config/rbac/capi-workload.yaml
	@echo "Creating cluster CA ConfigMap..."
	kubectl create configmap nstance-cluster-ca \
		--from-file=ca.crt=$(CURRENT)temp/dev-s3/cluster/ca.crt \
		--dry-run=client -o yaml | kubectl apply -f -
	@echo "Exporting Kind kubeconfig..."
	@mkdir -p $(CURRENT)temp/operator
	kind get kubeconfig --name $(KIND_CLUSTER_NAME) > $(CURRENT)temp/operator/kubeconfig
	@echo "Creating operator config..."
	@printf 'cluster_id: example-cluster\ntenant: default\nshards:\n  dev-1:\n    registration_addr: "127.0.0.1:8992"\n    operator_addr: "127.0.0.1:8993"\n  dev-2:\n    registration_addr: "127.0.0.1:9002"\n    operator_addr: "127.0.0.1:9003"\n' > $(CURRENT)temp/operator/config.yaml
	@echo "Generating registration nonce..."
	@NONCE_JWT=$$(AWS_ACCESS_KEY_ID=dev \
		AWS_SECRET_ACCESS_KEY=dev \
		AWS_ENDPOINT_URL=http://localhost:8989 \
		AWS_S3_USE_PATH_STYLE=true \
		NSTANCE_ENCRYPTION_KEY=thisisatest32bytekey123456789012 \
		$(BINARYDIR)/nstance-admin cluster nonce \
		--cluster-id example-cluster \
		--storage-bucket dev \
		--key-provider env \
		--output -) && \
	kubectl create secret generic nstance-operator-nonce \
		--from-literal=nonce.jwt="$$NONCE_JWT" \
		--dry-run=client -o yaml | kubectl apply -f -
	@echo "Kind cluster provisioned and ready. Run 'make dev-operator' to start the operator."

kind-delete:
	@if kind get clusters 2>/dev/null | grep -q "^$(KIND_CLUSTER_NAME)$$"; then \
		kind delete cluster --name $(KIND_CLUSTER_NAME); \
	else \
		echo "Kind cluster '$(KIND_CLUSTER_NAME)' does not exist, nothing to delete."; \
	fi

clean-dev:
	tmux list-sessions -F "#{session_name}" 2>/dev/null | grep "^nstance-.*-agents$$" | xargs -I{} tmux kill-session -t {} 2>/dev/null || true
	pkill -f dev-k8s || true
	pkill -f dev-s3 || true
	pkill -f nstance-operator || true
	pkill -f nstance-server || true
	pkill -f nstance-admin || true
	pkill -f overmind || true
	rm -f $(CURRENT).overmind.sock
	rm -rf $(CURRENT)temp
	mkdir -p $(CURRENT)temp/dev-s3

build: $(BINARIES)

$(BINARIES):
	mkdir -p $(BINARYDIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) \
	CGO_ENABLED=$(if $(findstring server,$@),1,0) CC=$(CC) CXX=$(CXX) \
	go build $(if $(BUILD_TAGS),-tags "$(BUILD_TAGS)") \
		-o $(BINARYDIR)/$@ \
		-trimpath \
		-ldflags "$(EXTRA_LD_FLAGS) \
		-X $(BUILDVARS_PKG).buildVersion=$(BUILD_VERSION) \
		-X $(BUILDVARS_PKG).buildDate=$(BUILD_DATE) \
		-X $(BUILDVARS_PKG).commitHash=$(COMMIT_HASH) \
		-X $(BUILDVARS_PKG).commitDate=$(COMMIT_DATE) \
		-X $(BUILDVARS_PKG).commitBranch=$(COMMIT_BRANCH) \
		" $(CURRENT)cmd/$@
	printf "%s" "$(BUILD_VERSION)-$(COMMIT_HASH)" > $(BINARYDIR)/nstance-version.txt

clean-build:
	rm -rf $(BINARYDIR)

manifests:
	cd $(CURRENT) && controller-gen object:headerFile="config/boilerplate.go.txt" paths="./api/..."
	cd $(CURRENT) && controller-gen crd:crdVersions=v1 paths="./api/..." output:crd:artifacts:config=config/crd/bases
	cd $(CURRENT) && controller-gen rbac:roleName=nstance-operator-role paths="./internal/operator/controller/..." output:rbac:artifacts:config=config/rbac
	@echo "Syncing CRDs to Helm chart..."
	@rm -f $(CURRENT)deploy/helm/crds/*.yaml
	@cd $(CURRENT) && kustomize build config/crd/ | awk ' \
		/^---$$/ || /^apiVersion:/ { \
			if (name != "" && doc != "") { \
				f = "deploy/helm/crds/" name ".yaml"; \
				printf "$(YAML_HEADER)\n\n---\n%s", doc > f; \
				close(f); \
				} \
				doc = ""; name = ""; \
				if (/^apiVersion:/) { doc = $$0 "\n" } \
				next; \
				} \
				/^  name: / && name == "" { name = $$2 } \
				{ doc = doc $$0 "\n" } \
				END { \
				if (name != "" && doc != "") { \
				f = "deploy/helm/crds/" name ".yaml"; \
				printf "$(YAML_HEADER)\n\n---\n%s", doc > f; \
				close(f); \
			} \
		}'

tag:
	@if [ -z "$(VERSION)" ]; then \
		echo "Usage: make tag VERSION=v1.0.0"; \
		exit 1; \
	fi; \
	VERSION_NUM=$$(echo "$(VERSION)" | sed 's/^v//'); \
	echo "Updating Helm chart to version $$VERSION_NUM..."; \
	sed -i.bak "s/^version:.*/version: $$VERSION_NUM/" $(CURRENT)deploy/helm/Chart.yaml; \
	sed -i.bak "s/^appVersion:.*/appVersion: \"$$VERSION_NUM\"/" $(CURRENT)deploy/helm/Chart.yaml; \
	rm -f $(CURRENT)deploy/helm/Chart.yaml.bak; \
	git -C $(CURRENT) add deploy/helm/Chart.yaml; \
	git -C $(CURRENT) commit -m "Updated version to $(VERSION)"; \
	git -C $(CURRENT) tag -a $(VERSION) -m "$(VERSION)"; \
	echo "Tagged $(VERSION) and updated Helm chart to $$VERSION_NUM"; \
	echo ""; \
	echo "To push the tag, run:"; \
	echo "  git push origin $(VERSION)"

images: image-operator image-agent image-server image-admin

image-operator:
	docker build -f $(CURRENT)images/operator/Containerfile \
		--build-arg VERSION=$(BUILD_VERSION) \
		--build-arg COMMIT_HASH=$(COMMIT_HASH) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t ghcr.io/nstance-dev/nstance-operator:$(BUILD_VERSION) \
		-t ghcr.io/nstance-dev/nstance-operator:latest \
		$(CURRENT)

image-agent:
	docker build -f $(CURRENT)images/agent/Containerfile \
		--build-arg VERSION=$(BUILD_VERSION) \
		--build-arg COMMIT_HASH=$(COMMIT_HASH) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t ghcr.io/nstance-dev/nstance-agent:$(BUILD_VERSION) \
		-t ghcr.io/nstance-dev/nstance-agent:latest \
		$(CURRENT)

image-server:
	docker build -f $(CURRENT)images/server/Containerfile \
		--build-arg VERSION=$(BUILD_VERSION) \
		--build-arg COMMIT_HASH=$(COMMIT_HASH) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t ghcr.io/nstance-dev/nstance-server:$(BUILD_VERSION) \
		-t ghcr.io/nstance-dev/nstance-server:latest \
		$(CURRENT)

image-admin:
	docker build -f $(CURRENT)images/admin/Containerfile \
		--build-arg VERSION=$(BUILD_VERSION) \
		--build-arg COMMIT_HASH=$(COMMIT_HASH) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t ghcr.io/nstance-dev/nstance-admin:$(BUILD_VERSION) \
		-t ghcr.io/nstance-dev/nstance-admin:latest \
		$(CURRENT)

publish: publish-operator publish-agent publish-server publish-admin

publish-operator:
	docker buildx build --platform linux/amd64,linux/arm64 \
		-f $(CURRENT)images/operator/Containerfile \
		--build-arg VERSION=$(BUILD_VERSION) \
		--build-arg COMMIT_HASH=$(COMMIT_HASH) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t ghcr.io/nstance-dev/nstance-operator:$(BUILD_VERSION) \
		-t ghcr.io/nstance-dev/nstance-operator:latest \
		--push \
		$(CURRENT)

publish-agent:
	docker buildx build --platform linux/amd64,linux/arm64 \
		-f $(CURRENT)images/agent/Containerfile \
		--build-arg VERSION=$(BUILD_VERSION) \
		--build-arg COMMIT_HASH=$(COMMIT_HASH) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t ghcr.io/nstance-dev/nstance-agent:$(BUILD_VERSION) \
		-t ghcr.io/nstance-dev/nstance-agent:latest \
		--push \
		$(CURRENT)

publish-server:
	docker buildx build --platform linux/amd64,linux/arm64 \
		-f $(CURRENT)images/server/Containerfile \
		--build-arg VERSION=$(BUILD_VERSION) \
		--build-arg COMMIT_HASH=$(COMMIT_HASH) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t ghcr.io/nstance-dev/nstance-server:$(BUILD_VERSION) \
		-t ghcr.io/nstance-dev/nstance-server:latest \
		--push \
		$(CURRENT)

publish-admin:
	docker buildx build --platform linux/amd64,linux/arm64 \
		-f $(CURRENT)images/admin/Containerfile \
		--build-arg VERSION=$(BUILD_VERSION) \
		--build-arg COMMIT_HASH=$(COMMIT_HASH) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t ghcr.io/nstance-dev/nstance-admin:$(BUILD_VERSION) \
		-t ghcr.io/nstance-dev/nstance-admin:latest \
		--push \
		$(CURRENT)
