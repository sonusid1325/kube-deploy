# ============================================================================
# kube-deploy — Zero-Downtime Kubernetes Deployment Pipeline
# ============================================================================

BINARY        := kdctl
BIN_DIR       := bin
MODULE        := github.com/sonu/kube-deploy
PROTO_DIR     := proto
API_DIR       := api/v1

# Version info (injected at build time)
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS   := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)"

# Go settings
GO        := go
GOFLAGS   ?=
GOTEST    := $(GO) test $(GOFLAGS)
GOBUILD   := $(GO) build $(GOFLAGS) $(LDFLAGS)

# Proto tools
PROTOC          := protoc
PROTOC_GEN_GO   := protoc-gen-go
PROTOC_GEN_GRPC := protoc-gen-go-grpc

# Colors
GREEN  := \033[0;32m
YELLOW := \033[0;33m
CYAN   := \033[0;36m
RESET  := \033[0m

.PHONY: all build clean test test-unit test-integration test-race \
        lint vet fmt proto proto-check tidy vendor help run run-server \
        docker docker-goserver docker-goserver-v1 docker-goserver-v2 docker-goserver-v3-bad \
        install-tools cover deploy-all deploy-server deploy-goserver \
        undeploy-all undeploy-server undeploy-goserver install

# ============================================================================
# Default target
# ============================================================================

all: tidy fmt vet build test ## Build and test everything

# ============================================================================
# Build targets
# ============================================================================

build: ## Build the kdctl binary (single binary: TUI + CLI + server)
	@echo "$(CYAN)Building $(BINARY)...$(RESET)"
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $(BIN_DIR)/$(BINARY) ./cmd/kdctl
	@echo "$(GREEN)✓ Build complete: $(BIN_DIR)/$(BINARY)$(RESET)"

install: build ## Install kdctl to $GOPATH/bin
	@echo "$(CYAN)Installing $(BINARY)...$(RESET)"
	cp $(BIN_DIR)/$(BINARY) $(shell go env GOPATH)/bin/$(BINARY)
	@echo "$(GREEN)✓ Installed to $(shell go env GOPATH)/bin/$(BINARY)$(RESET)"

# ============================================================================
# Proto generation
# ============================================================================

proto: ## Generate Go code from protobuf definitions
	@echo "$(CYAN)Generating protobuf Go code...$(RESET)"
	@mkdir -p $(API_DIR)
	$(PROTOC) \
		--proto_path=$(PROTO_DIR) \
		--go_out=$(API_DIR) \
		--go_opt=paths=source_relative \
		--go-grpc_out=$(API_DIR) \
		--go-grpc_opt=paths=source_relative \
		$(PROTO_DIR)/kube_deploy.proto
	@echo "$(GREEN)✓ Proto generation complete$(RESET)"

proto-check: proto ## Check that generated proto files are up-to-date
	@echo "$(CYAN)Checking proto files are up-to-date...$(RESET)"
	@git diff --exit-code $(API_DIR)/ || \
		(echo "$(YELLOW)⚠ Proto generated files are out of date. Run 'make proto' and commit.$(RESET)" && exit 1)
	@echo "$(GREEN)✓ Proto files are up-to-date$(RESET)"

# ============================================================================
# Test targets
# ============================================================================

test: test-unit ## Run all tests
	@echo "$(GREEN)✓ All tests passed$(RESET)"

test-unit: ## Run unit tests
	@echo "$(CYAN)Running unit tests...$(RESET)"
	$(GOTEST) -v -count=1 -short ./...

test-integration: ## Run integration tests (requires Kubernetes cluster)
	@echo "$(CYAN)Running integration tests...$(RESET)"
	$(GOTEST) -v -count=1 -run Integration ./...

test-race: ## Run tests with race detector
	@echo "$(CYAN)Running tests with race detector...$(RESET)"
	$(GOTEST) -v -race -count=1 ./...

cover: ## Run tests with coverage and generate HTML report
	@echo "$(CYAN)Running tests with coverage...$(RESET)"
	@mkdir -p $(BIN_DIR)
	$(GOTEST) -coverprofile=$(BIN_DIR)/coverage.out -covermode=atomic ./...
	$(GO) tool cover -html=$(BIN_DIR)/coverage.out -o $(BIN_DIR)/coverage.html
	@echo "$(GREEN)✓ Coverage report: $(BIN_DIR)/coverage.html$(RESET)"

# ============================================================================
# Code quality
# ============================================================================

lint: ## Run golangci-lint
	@echo "$(CYAN)Running linter...$(RESET)"
	@which golangci-lint > /dev/null 2>&1 || \
		(echo "$(YELLOW)Installing golangci-lint...$(RESET)" && \
		 go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run ./...
	@echo "$(GREEN)✓ Lint passed$(RESET)"

vet: ## Run go vet
	@echo "$(CYAN)Running go vet...$(RESET)"
	$(GO) vet ./...
	@echo "$(GREEN)✓ Vet passed$(RESET)"

fmt: ## Format all Go source files
	@echo "$(CYAN)Formatting Go source files...$(RESET)"
	@gofmt -w -s .
	@echo "$(GREEN)✓ Format complete$(RESET)"

# ============================================================================
# Dependencies
# ============================================================================

tidy: ## Tidy and verify Go module dependencies
	@echo "$(CYAN)Tidying Go modules...$(RESET)"
	$(GO) mod tidy
	$(GO) mod verify
	@echo "$(GREEN)✓ Modules tidy$(RESET)"

vendor: ## Vendor Go module dependencies
	@echo "$(CYAN)Vendoring Go modules...$(RESET)"
	$(GO) mod vendor
	@echo "$(GREEN)✓ Vendor complete$(RESET)"

# ============================================================================
# Install proto tools
# ============================================================================

install-tools: ## Install required development tools
	@echo "$(CYAN)Installing development tools...$(RESET)"
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@echo "$(GREEN)✓ Tools installed$(RESET)"

# ============================================================================
# Run targets
# ============================================================================

run: build ## Build and launch the kdctl TUI (interactive mode)
	@echo "$(CYAN)Launching kdctl TUI...$(RESET)"
	./$(BIN_DIR)/$(BINARY) -n default -d goserver

run-server: build ## Build and start the gRPC server via kdctl start
	@echo "$(CYAN)Starting gRPC server via kdctl start...$(RESET)"
	./$(BIN_DIR)/$(BINARY) start --port 9090 --log-format console --log-level debug

# ============================================================================
# Docker targets
# ============================================================================

docker: ## Build the kdctl Docker image
	@echo "$(CYAN)Building kdctl Docker image...$(RESET)"
	docker build -t kdctl:latest \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-f Dockerfile .
	@echo "$(GREEN)✓ kdctl image built$(RESET)"

docker-goserver: ## Build the goserver test Docker image
	@echo "$(CYAN)Building goserver Docker image...$(RESET)"
	docker build -t goserver:latest -f deploy/goserver/Dockerfile deploy/goserver/
	@echo "$(GREEN)✓ goserver image built$(RESET)"

docker-goserver-v1: ## Build goserver v1 (healthy)
	docker build -t goserver:v1 -f deploy/goserver/Dockerfile deploy/goserver/
	@echo "$(GREEN)✓ goserver:v1 image built$(RESET)"

docker-goserver-v2: ## Build goserver v2 (healthy, new version)
	docker build -t goserver:v2 --build-arg VERSION=v2 -f deploy/goserver/Dockerfile deploy/goserver/
	@echo "$(GREEN)✓ goserver:v2 image built$(RESET)"

docker-goserver-v3-bad: ## Build goserver v3 (unhealthy — for rollback testing)
	docker build -t goserver:v3-bad --build-arg VERSION=v3-bad --build-arg FAIL_AFTER=10s -f deploy/goserver/Dockerfile deploy/goserver/
	@echo "$(GREEN)✓ goserver:v3-bad image built$(RESET)"

# ============================================================================
# Deploy to Kubernetes cluster
# ============================================================================

deploy-all: deploy-server deploy-goserver ## Deploy everything (server + goserver) to the cluster

deploy-server: ## Deploy the kube-deploy server stack to the cluster
	@echo "$(CYAN)Deploying kube-deploy server stack...$(RESET)"
	kubectl apply -f deploy/k8s/namespace.yaml
	kubectl apply -f deploy/k8s/serviceaccount.yaml
	kubectl apply -f deploy/k8s/rbac.yaml
	kubectl apply -f deploy/k8s/configmap.yaml
	kubectl apply -f deploy/k8s/service.yaml
	kubectl apply -f deploy/k8s/deployment.yaml
	@echo "$(CYAN)Waiting for kube-deploy-server rollout...$(RESET)"
	kubectl rollout status deployment/kube-deploy-server -n kube-deploy --timeout=120s || true
	@echo "$(GREEN)✓ kube-deploy server deployed$(RESET)"

deploy-goserver: ## Deploy goserver test app to the cluster
	@echo "$(CYAN)Deploying goserver to cluster...$(RESET)"
	kubectl apply -f deploy/goserver/deployment.yaml
	kubectl apply -f deploy/goserver/service.yaml
	@echo "$(CYAN)Waiting for goserver rollout...$(RESET)"
	kubectl rollout status deployment/goserver -n default --timeout=120s || true
	@echo "$(GREEN)✓ goserver deployed$(RESET)"

undeploy-all: undeploy-goserver undeploy-server ## Remove everything from the cluster

undeploy-server: ## Remove the kube-deploy server stack from the cluster
	@echo "$(CYAN)Removing kube-deploy server stack...$(RESET)"
	kubectl delete -f deploy/k8s/deployment.yaml --ignore-not-found
	kubectl delete -f deploy/k8s/service.yaml --ignore-not-found
	kubectl delete -f deploy/k8s/configmap.yaml --ignore-not-found
	kubectl delete -f deploy/k8s/rbac.yaml --ignore-not-found
	kubectl delete -f deploy/k8s/serviceaccount.yaml --ignore-not-found
	kubectl delete -f deploy/k8s/namespace.yaml --ignore-not-found
	@echo "$(GREEN)✓ kube-deploy server removed$(RESET)"

undeploy-goserver: ## Remove goserver from the cluster
	@echo "$(CYAN)Removing goserver from cluster...$(RESET)"
	kubectl delete -f deploy/goserver/service.yaml --ignore-not-found
	kubectl delete -f deploy/goserver/deployment.yaml --ignore-not-found
	@echo "$(GREEN)✓ goserver removed$(RESET)"

# ============================================================================
# Cross-compilation
# ============================================================================

.PHONY: build-all-platforms
build-all-platforms: ## Cross-compile for Linux, macOS, and Windows (amd64 + arm64)
	@echo "$(CYAN)Cross-compiling $(BINARY) for all platforms...$(RESET)"
	@mkdir -p $(BIN_DIR)
	GOOS=linux   GOARCH=amd64 $(GOBUILD) -o $(BIN_DIR)/$(BINARY)-linux-amd64     ./cmd/kdctl
	GOOS=linux   GOARCH=arm64 $(GOBUILD) -o $(BIN_DIR)/$(BINARY)-linux-arm64     ./cmd/kdctl
	GOOS=darwin  GOARCH=amd64 $(GOBUILD) -o $(BIN_DIR)/$(BINARY)-darwin-amd64    ./cmd/kdctl
	GOOS=darwin  GOARCH=arm64 $(GOBUILD) -o $(BIN_DIR)/$(BINARY)-darwin-arm64    ./cmd/kdctl
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o $(BIN_DIR)/$(BINARY)-windows-amd64.exe ./cmd/kdctl
	@echo "$(GREEN)✓ Cross-compilation complete$(RESET)"
	@ls -lh $(BIN_DIR)/$(BINARY)-*

# ============================================================================
# Cleanup
# ============================================================================

clean: ## Remove build artifacts
	@echo "$(CYAN)Cleaning build artifacts...$(RESET)"
	rm -rf $(BIN_DIR)
	rm -f $(API_DIR)/*.go
	@echo "$(GREEN)✓ Clean complete$(RESET)"

# ============================================================================
# Help
# ============================================================================

help: ## Show this help message
	@echo ""
	@echo "$(CYAN)kdctl$(RESET) — Zero-Downtime Kubernetes Deployment Pipeline"
	@echo ""
	@echo "$(YELLOW)Usage:$(RESET)"
	@echo "  make $(GREEN)<target>$(RESET)"
	@echo ""
	@echo "$(YELLOW)Targets:$(RESET)"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  $(GREEN)%-24s$(RESET) %s\n", $$1, $$2}'
	@echo ""
	@echo "$(YELLOW)kdctl subcommands:$(RESET)"
	@echo "  $(GREEN)kdctl$(RESET)                      Launch the interactive Bubble Tea TUI"
	@echo "  $(GREEN)kdctl ui -d <deploy>$(RESET)       Same as above (explicit)"
	@echo "  $(GREEN)kdctl start$(RESET)                Start the gRPC server"
	@echo "  $(GREEN)kdctl deploy$(RESET)               Deploy (non-interactive)"
	@echo "  $(GREEN)kdctl status$(RESET)               Check deployment status"
	@echo "  $(GREEN)kdctl health$(RESET)               Check deployment health"
	@echo "  $(GREEN)kdctl rollback$(RESET)             Rollback a deployment"
	@echo "  $(GREEN)kdctl history$(RESET)              Show revision history"
	@echo "  $(GREEN)kdctl version$(RESET)              Print version info"
	@echo ""
