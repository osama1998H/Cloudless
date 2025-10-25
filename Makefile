# Cloudless Platform Makefile

# Variables
BINARY_NAME_COORDINATOR=coordinator
BINARY_NAME_AGENT=agent
BUILD_DIR=build
GO=go
GOFLAGS=-ldflags="-s -w"
GOTEST=$(GO) test
GOLINT=golangci-lint
PROTOC=protoc

# Version information
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT=$(shell git rev-parse HEAD 2>/dev/null || echo "unknown")

# Build flags
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT) -s -w"

# Platforms
PLATFORMS=linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: all build clean test lint proto help

## help: Show this help message
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'

## all: Build everything
all: clean proto build test

## build: Build coordinator and agent binaries
build: build-coordinator build-agent

## build-coordinator: Build the coordinator binary
build-coordinator:
	@echo "Building coordinator..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME_COORDINATOR) ./cmd/coordinator

## build-agent: Build the agent binary
build-agent:
	@echo "Building agent..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME_AGENT) ./cmd/agent

## build-linux-amd64: Cross-compile for Linux AMD64
build-linux-amd64:
	@echo "Building for Linux AMD64..."
	@mkdir -p $(BUILD_DIR)/linux-amd64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/linux-amd64/$(BINARY_NAME_COORDINATOR) ./cmd/coordinator
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/linux-amd64/$(BINARY_NAME_AGENT) ./cmd/agent

## build-linux-arm64: Cross-compile for Linux ARM64
build-linux-arm64:
	@echo "Building for Linux ARM64..."
	@mkdir -p $(BUILD_DIR)/linux-arm64
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/linux-arm64/$(BINARY_NAME_COORDINATOR) ./cmd/coordinator
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/linux-arm64/$(BINARY_NAME_AGENT) ./cmd/agent

## build-all: Build for all platforms
build-all:
	@for platform in $(PLATFORMS); do \
		GOOS=$${platform%/*} GOARCH=$${platform#*/} make build-platform; \
	done

build-platform:
	@mkdir -p $(BUILD_DIR)/$${GOOS}-$${GOARCH}
	@echo "Building for $${GOOS}/$${GOARCH}..."
	@CGO_ENABLED=0 GOOS=$${GOOS} GOARCH=$${GOARCH} $(GO) build $(LDFLAGS) \
		-o $(BUILD_DIR)/$${GOOS}-$${GOARCH}/$(BINARY_NAME_COORDINATOR) ./cmd/coordinator
	@CGO_ENABLED=0 GOOS=$${GOOS} GOARCH=$${GOARCH} $(GO) build $(LDFLAGS) \
		-o $(BUILD_DIR)/$${GOOS}-$${GOARCH}/$(BINARY_NAME_AGENT) ./cmd/agent

## clean: Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -rf coverage.out coverage.html

## test: Run all tests
test:
	@echo "Running tests..."
	$(GOTEST) -v -cover -coverprofile=coverage.out ./...

## test-race: Run tests with race detector
test-race:
	@echo "Running tests with race detector..."
	$(GOTEST) -v -race ./...

## test-integration: Run integration tests
test-integration:
	@echo "Running integration tests..."
	$(GOTEST) -v -tags=integration ./test/integration/...

## test-chaos: Run chaos tests
test-chaos:
	@echo "Running chaos tests..."
	$(GOTEST) -v -tags=chaos ./test/chaos/...

## coverage: Generate coverage report
coverage: test
	@echo "Generating coverage report..."
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

## benchmark: Run benchmarks
benchmark:
	@echo "Running benchmarks..."
	$(GOTEST) -bench=. -benchmem ./...

## lint: Run golangci-lint
lint:
	@echo "Running linter..."
	@if ! command -v $(GOLINT) &> /dev/null; then \
		echo "Installing golangci-lint..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi
	$(GOLINT) run ./...

## fmt: Format code
fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...

## vet: Run go vet
vet:
	@echo "Running go vet..."
	$(GO) vet ./...

## mod: Download and tidy modules
mod:
	@echo "Downloading dependencies..."
	$(GO) mod download
	$(GO) mod tidy

## proto: Generate protobuf/gRPC code
proto:
	@echo "Generating protobuf code..."
	@if ! command -v $(PROTOC) &> /dev/null; then \
		echo "Error: protoc is not installed. Please install protocol buffers compiler."; \
		exit 1; \
	fi
	@if [ -f pkg/api/cloudless.proto ]; then \
		$(PROTOC) --go_out=. --go_opt=paths=source_relative \
			--go-grpc_out=. --go-grpc_opt=paths=source_relative \
			pkg/api/cloudless.proto; \
	else \
		echo "Warning: pkg/api/cloudless.proto not found. Skipping proto generation."; \
	fi

## docker: Build Docker images
docker: docker-coordinator docker-agent

## docker-coordinator: Build coordinator Docker image
docker-coordinator:
	@echo "Building coordinator Docker image..."
	docker build -f deployments/docker/Dockerfile.coordinator -t cloudless/coordinator:$(VERSION) .

## docker-agent: Build agent Docker image
docker-agent:
	@echo "Building agent Docker image..."
	docker build -f deployments/docker/Dockerfile.agent -t cloudless/agent:$(VERSION) .

## docker-push: Push Docker images to registry
docker-push: docker
	@echo "Pushing Docker images..."
	docker push cloudless/coordinator:$(VERSION)
	docker push cloudless/agent:$(VERSION)

## run-coordinator: Run coordinator locally
run-coordinator: build-coordinator
	@echo "Starting coordinator..."
	./$(BUILD_DIR)/$(BINARY_NAME_COORDINATOR) --config config/coordinator.yaml

## run-agent: Run agent locally
run-agent: build-agent
	@echo "Starting agent..."
	./$(BUILD_DIR)/$(BINARY_NAME_AGENT) --config config/agent.yaml

## compose-up: Start local development cluster with Docker Compose
compose-up:
	@echo "Starting local cluster..."
	docker-compose -f deployments/docker/docker-compose.yml up -d

## compose-down: Stop local development cluster
compose-down:
	@echo "Stopping local cluster..."
	docker-compose -f deployments/docker/docker-compose.yml down

## compose-logs: Show logs from local cluster
compose-logs:
	docker-compose -f deployments/docker/docker-compose.yml logs -f

## install: Install binaries to system
install: build
	@echo "Installing binaries..."
	@mkdir -p /usr/local/bin
	@cp $(BUILD_DIR)/$(BINARY_NAME_COORDINATOR) /usr/local/bin/
	@cp $(BUILD_DIR)/$(BINARY_NAME_AGENT) /usr/local/bin/
	@echo "Installed to /usr/local/bin/"

## ci: Run CI pipeline locally
ci: clean mod lint test build

.DEFAULT_GOAL := help