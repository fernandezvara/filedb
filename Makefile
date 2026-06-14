# Makefile for filedb

# Variables
BINARY_NAME=filedb
CMD_PATH=./cmd/filedb
BUILD_DIR=./build
GO=go
GOFLAGS=-v
VERSION?=dev
LDFLAGS=-ldflags "-X main.Version=$(VERSION)"

# Default target
.PHONY: all
all: build

# Build the CLI binary
.PHONY: build
build:
	@echo "Building $(BINARY_NAME) (version: $(VERSION))..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_PATH)

# Build for multiple platforms
.PHONY: build-all
build-all:
	@echo "Building $(BINARY_NAME) for multiple platforms (version: $(VERSION))..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 $(CMD_PATH)
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 $(CMD_PATH)
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 $(CMD_PATH)
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe $(CMD_PATH)

# Install the CLI binary to GOPATH/bin
.PHONY: install
install:
	@echo "Installing $(BINARY_NAME) (version: $(VERSION))..."
	$(GO) install $(GOFLAGS) $(LDFLAGS) $(CMD_PATH)

# Run tests
.PHONY: test
test:
	@echo "Running tests..."
	$(GO) test -v ./...

# Run tests with coverage
.PHONY: test-coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GO) test -v -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run tests with race detection
.PHONY: test-race
test-race:
	@echo "Running tests with race detection..."
	$(GO) test -v -race ./...

# Format code
.PHONY: fmt
fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...

# Run go vet
.PHONY: vet
vet:
	@echo "Running go vet..."
	$(GO) vet ./...

# Run linter (requires golangci-lint)
.PHONY: lint
lint:
	@echo "Running linter..."
	@if command -v golangci-lint > /dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Install it from https://golangci-lint.run/usage/install/"; \
	fi

# Tidy go.mod and go.sum
.PHONY: mod-tidy
mod-tidy:
	@echo "Tidying go.mod..."
	$(GO) mod tidy

# Verify dependencies
.PHONY: mod-verify
mod-verify:
	@echo "Verifying dependencies..."
	$(GO) mod verify

# Download dependencies
.PHONY: mod-download
mod-download:
	@echo "Downloading dependencies..."
	$(GO) mod download

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out coverage.html

# Clean all generated files
.PHONY: clean-all
clean-all: clean
	@echo "Cleaning all generated files..."
	@rm -f $(BINARY_NAME)

# Run the CLI from source
.PHONY: run
run:
	@echo "Running $(BINARY_NAME) from source..."
	$(GO) run $(CMD_PATH)

# Show help
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  all           - Build the CLI binary (default)"
	@echo "  build         - Build the CLI binary"
	@echo "  build-all     - Build for multiple platforms"
	@echo "  install       - Install the CLI binary to GOPATH/bin"
	@echo "  test          - Run tests"
	@echo "  test-coverage - Run tests with coverage report"
	@echo "  test-race     - Run tests with race detection"
	@echo "  fmt           - Format code"
	@echo "  vet           - Run go vet"
	@echo "  lint          - Run linter (requires golangci-lint)"
	@echo "  mod-tidy      - Tidy go.mod and go.sum"
	@echo "  mod-verify    - Verify dependencies"
	@echo "  mod-download  - Download dependencies"
	@echo "  clean         - Clean build artifacts"
	@echo "  clean-all     - Clean all generated files"
	@echo "  run           - Run the CLI from source"
	@echo "  help          - Show this help message"
