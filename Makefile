.PHONY: build test lint fmt clean help

BINARY_NAME=gi
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_DIR=./bin
GO_LDFLAGS=-ldflags "-X main.Version=$(VERSION)"

help:
	@echo "Available targets:"
	@echo "  make build   - Build the binary"
	@echo "  make test    - Run tests"
	@echo "  make lint    - Run linter"
	@echo "  make fmt     - Format code"
	@echo "  make clean   - Clean build artifacts"

build: fmt
	@echo "Building $(BINARY_NAME) ($(VERSION))..."
	@mkdir -p $(BUILD_DIR)
	@go build $(GO_LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/gi

test:
	@echo "Running tests..."
	@go test -v -cover ./...

lint:
	@echo "Running linter..."
	@go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "Note: golangci-lint not installed, skipping"; \
	fi

fmt:
	@echo "Formatting code..."
	@go fmt ./...

clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@go clean
