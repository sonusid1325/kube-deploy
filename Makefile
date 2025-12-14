# ============================================================================
# kube-deploy — Zero-Downtime Kubernetes Deployment Pipeline
# ============================================================================

BINARY_SERVER := kube-deploy-server
BINARY_CLI    := kdctl
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

.PHONY: all build build-server build-cli clean test test-unit test-integration test-race \
        lint vet fmt proto proto-check tidy vendor help run-server docker-goserver \
        install-tools cover

# ============================================================================
# Default target
# ============================================================================

all: tidy fmt vet build test ## Build and test everything

# ============================================================================
# Build targets
# ============================================================================

build: build-server build-cli ## Build both server and CLI binaries
	@echo "$(GREEN)✓ Build complete$(RESET)"

build-server: ## Build the kube-deploy-server binary
	@echo "$(CYAN)Building $(BINARY_SERVER)...$(RESET)"
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $(BIN_DIR)/$(BINARY_SERVER) ./cmd/kube-deploy-server

build-cli: ## Build the kdctl CLI binary
	@echo "$(CYAN)Building $(BINARY_CLI)...$(RESET)"
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $(BIN_DIR)/$(BINARY_CLI) ./cmd/kdctl

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

run-server: build-server ## Build and run the kube-deploy-server
	@echo "$(CYAN)Starting kube-deploy-server...$(RESET)"
	./$(BIN_DIR)/$(BINARY_SERVER) --port 9090 --log-format console --log-level debug

# ============================================================================
# Docker targets (goserver test app)
# ============================================================================

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
# Deploy goserver to cluster (for testing)
# ============================================================================

deploy-goserver: ## Deploy goserver to the current Kubernetes cluster
	@echo "$(CYAN)Deploying goserver to cluster...$(RESET)"
	kubectl apply -f deploy/goserver/deployment.yaml
	kubectl apply -f deploy/goserver/service.yaml
	@echo "$(GREEN)✓ goserver deployed$(RESET)"

undeploy-goserver: ## Remove goserver from the current Kubernetes cluster
	@echo "$(CYAN)Removing goserver from cluster...$(RESET)"
	kubectl delete -f deploy/goserver/service.yaml --ignore-not-found
	kubectl delete -f deploy/goserver/deployment.yaml --ignore-not-found
	@echo "$(GREEN)✓ goserver removed$(RESET)"

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
	@echo "$(CYAN)kube-deploy$(RESET) — Zero-Downtime Kubernetes Deployment Pipeline"
	@echo ""
	@echo "$(YELLOW)Usage:$(RESET)"
	@echo "  make $(GREEN)<target>$(RESET)"
	@echo ""
	@echo "$(YELLOW)Targets:$(RESET)"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  $(GREEN)%-20s$(RESET) %s\n", $$1, $$2}'
	@echo ""
