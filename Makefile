# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofmt
GOLINT=golangci-lint

# Build variables
BINARY_NAME=arc
BINARY_UNIX=$(BINARY_NAME)_unix
BUILD_DIR=./build
COVERAGE_DIR=./coverage

# Protobuf variables
PROTO_DIR=./api/v1
THIRD_PARTY_DIR=./third_party
PROTO_FILES=$(wildcard $(PROTO_DIR)/*.proto)

# Shared library names
SHAREDLIB_DARWIN_ARM64=signer-arm64.dylib
SHAREDLIB_LINUX_AMD64=signer-amd64.so

# Source files
MAIN_SOURCE=./cmd/$(BINARY_NAME)/main.go
SHAREDLIB_SOURCE=./sharedlib/sharedlib.go

# Git info
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_TAG=$(shell git describe --tags --abbrev=0 2>/dev/null || echo "dev")
BUILD_DATE=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Build flags
LDFLAGS=-ldflags "-X main.Version=$(GIT_TAG) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildDate=$(BUILD_DATE)"

.PHONY: all build clean test coverage lint fmt vet vendor help sharedlib-darwin sharedlib-linux sharedlib-all setup proto proto-clean proto-install server server-build server-run

# Default target
all: test build

## help: Show this help message
help:
	@echo 'Usage:'
	@echo '  make <target>'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## setup: Install development dependencies
setup:
	@echo "Installing development dependencies..."
	@$(GOCMD) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@$(GOCMD) install golang.org/x/tools/cmd/goimports@latest
	@$(GOCMD) install github.com/securego/gosec/v2/cmd/gosec@latest
	@echo "Setup complete!"

## proto-install: Install protobuf tools
proto-install:
	@echo "Installing protobuf tools..."
	@$(GOCMD) install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@$(GOCMD) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@$(GOCMD) install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
	@$(GOCMD) install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest
	@echo "Protobuf tools installed!"

## proto: Generate Go code from protobuf definitions
proto:
	@echo "Generating protobuf code..."
	@mkdir -p $(THIRD_PARTY_DIR)/googleapis/google/api
	@if [ ! -f $(THIRD_PARTY_DIR)/googleapis/google/api/annotations.proto ]; then \
		curl -L https://raw.githubusercontent.com/googleapis/googleapis/master/google/api/annotations.proto \
		-o $(THIRD_PARTY_DIR)/googleapis/google/api/annotations.proto; \
	fi
	@if [ ! -f $(THIRD_PARTY_DIR)/googleapis/google/api/http.proto ]; then \
		curl -L https://raw.githubusercontent.com/googleapis/googleapis/master/google/api/http.proto \
		-o $(THIRD_PARTY_DIR)/googleapis/google/api/http.proto; \
	fi
	@protoc \
		-I $(PROTO_DIR) \
		-I $(THIRD_PARTY_DIR)/googleapis \
		--go_out=$(PROTO_DIR) \
		--go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_DIR) \
		--go-grpc_opt=paths=source_relative \
		--grpc-gateway_out=$(PROTO_DIR) \
		--grpc-gateway_opt=paths=source_relative \
		$(PROTO_FILES)
	@echo "Protobuf code generated!"

## proto-clean: Clean generated protobuf files
proto-clean:
	@echo "Cleaning generated protobuf files..."
	@rm -f $(PROTO_DIR)/*.pb.go
	@rm -f $(PROTO_DIR)/*.pb.gw.go
	@rm -f $(BUILD_DIR)/*.swagger.json
	@rm -rf $(THIRD_PARTY_DIR)

## server-build: Build the gRPC/HTTP server
server-build: proto vendor
	@echo "Building ARC server..."
	@mkdir -p $(BUILD_DIR)
	@$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/arc-server -v ./cmd/server/

## server-run: Run the gRPC/HTTP server
server-run: server-build
	@echo "Starting ARC server..."
	@$(BUILD_DIR)/arc-server

## server: Generate protobuf, build and run server
server: proto server-build server-run

## build: Build the binary
build: vendor
	@mkdir -p $(BUILD_DIR)
	@if [ -f $(MAIN_SOURCE) ]; then \
		$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) -v $(MAIN_SOURCE); \
	else \
		echo "Warning: $(MAIN_SOURCE) not found. Skipping binary build."; \
	fi

## sharedlib-darwin: Build shared library for Darwin (macOS)
sharedlib-darwin: vendor
	@mkdir -p $(BUILD_DIR)
	@if [ -f $(SHAREDLIB_SOURCE) ]; then \
		CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 $(GOBUILD) -buildmode=c-shared -o $(BUILD_DIR)/$(SHAREDLIB_DARWIN_ARM64) $(SHAREDLIB_SOURCE); \
		echo "Built $(BUILD_DIR)/$(SHAREDLIB_DARWIN_ARM64)"; \
	else \
		echo "Error: $(SHAREDLIB_SOURCE) not found"; \
		exit 1; \
	fi

## sharedlib-linux: Build shared library for Linux
sharedlib-linux: vendor
	@mkdir -p $(BUILD_DIR)
	@if [ -f $(SHAREDLIB_SOURCE) ]; then \
		CGO_ENABLED=1 GOOS=linux GOARCH=amd64 $(GOBUILD) -buildmode=c-shared -o $(BUILD_DIR)/$(SHAREDLIB_LINUX_AMD64) $(SHAREDLIB_SOURCE); \
		echo "Built $(BUILD_DIR)/$(SHAREDLIB_LINUX_AMD64)"; \
	else \
		echo "Error: $(SHAREDLIB_SOURCE) not found"; \
		exit 1; \
	fi

## sharedlib-all: Build all shared libraries
sharedlib-all: sharedlib-darwin sharedlib-linux

## clean: Remove build artifacts
clean:
	@$(GOCLEAN)
	@rm -rf $(BUILD_DIR)
	@rm -rf $(COVERAGE_DIR)
	@rm -rf vendor/

## test: Run tests
test:
	@$(GOTEST) -v -race ./...

## test-short: Run short tests
test-short:
	@$(GOTEST) -v -short ./...

## coverage: Run tests with coverage
coverage:
	@mkdir -p $(COVERAGE_DIR)
	@$(GOTEST) -v -race -coverprofile=$(COVERAGE_DIR)/coverage.out -covermode=atomic ./...
	@$(GOCMD) tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	@echo "Coverage report generated at $(COVERAGE_DIR)/coverage.html"

## benchmark: Run benchmarks
benchmark:
	@$(GOTEST) -bench=. -benchmem ./...

## lint: Run golangci-lint
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		$(GOLINT) run ./...; \
	else \
		echo "golangci-lint not installed. Run 'make setup' first."; \
		exit 1; \
	fi

## fmt: Format code
fmt:
	@$(GOFMT) -s -w .
	@if command -v goimports >/dev/null 2>&1; then \
		goimports -w .; \
	fi

## vet: Run go vet
vet:
	@$(GOCMD) vet ./...

## security: Run security scan
security:
	@if command -v gosec >/dev/null 2>&1; then \
		gosec -fmt=json -out=$(BUILD_DIR)/security-report.json ./... || true; \
		echo "Security report generated at $(BUILD_DIR)/security-report.json"; \
	else \
		echo "gosec not installed. Run 'make setup' first."; \
	fi

## vendor: Vendor dependencies
vendor:
	@$(GOMOD) vendor
	@$(GOMOD) tidy

## deps: Download dependencies
deps:
	@$(GOMOD) download

## deps-update: Update dependencies
deps-update:
	@$(GOMOD) tidy
	@$(GOCMD) get -u ./...
	@$(GOMOD) tidy

## build-cross: Cross compile for multiple platforms
build-cross: vendor
	@mkdir -p $(BUILD_DIR)
	@if [ -f $(MAIN_SOURCE) ]; then \
		echo "Building for Linux..."; \
		GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 -v $(MAIN_SOURCE); \
		echo "Building for Darwin..."; \
		GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 -v $(MAIN_SOURCE); \
		GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 -v $(MAIN_SOURCE); \
		echo "Building for Windows..."; \
		GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe -v $(MAIN_SOURCE); \
	else \
		echo "Warning: $(MAIN_SOURCE) not found. Skipping cross compilation."; \
	fi

## run: Run the application
run: build
	@if [ -f $(BUILD_DIR)/$(BINARY_NAME) ]; then \
		$(BUILD_DIR)/$(BINARY_NAME); \
	else \
		echo "Binary not found. Build it first with 'make build'"; \
	fi

## docker-build: Build Docker image
docker-build:
	@docker build -t $(BINARY_NAME):$(GIT_TAG) .

## ci: Run CI pipeline locally
ci: vendor lint vet test

## release: Create a new release
release: clean vendor lint vet test build-cross
	@echo "Release artifacts built in $(BUILD_DIR)/"
	@ls -la $(BUILD_DIR)/

# Keep the build directory
.PRECIOUS: $(BUILD_DIR)