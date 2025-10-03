.PHONY: help test test-up test-down test-clean build clean deps fmt lint

# Default target
.DEFAULT_GOAL := help

# Variables
BINARY_NAME := pgqueue
BUILD_DIR := bin

## Show available commands
help:
	@echo "PgTask - PostgreSQL Task Queue (aligned with pgqueuer)"
	@echo ""
	@echo "Available commands:"
	@echo "  test        - Run all tests (unit + integration)"
	@echo "  test-unit   - Run unit tests only (no database required)"
	@echo "  test-integration - Run integration tests (requires database)"
	@echo "  test-up     - Start PostgreSQL test database only"
	@echo "  test-down   - Stop PostgreSQL test database"
	@echo "  test-clean  - Clean up test environment"
	@echo "  build       - Build the CLI binary"
	@echo "  clean       - Clean build artifacts"
	@echo "  deps        - Download dependencies"
	@echo "  fmt         - Format code"
	@echo "  lint        - Run linter"

# Build the binary
.PHONY: build
build:
	@echo "Building $(BINARY_NAME)..."
	@go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/pgqueue
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

# Build for Linux
.PHONY: build-linux
build-linux:
	@echo "Building $(BINARY_NAME) for Linux..."
	@GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-linux ./cmd/pgqueue

## Start PostgreSQL test database (schema installed automatically via CLI)
test-up: build-linux
	@echo "Starting PostgreSQL test database with schema..."
	@docker-compose -f test/docker/docker-compose.test.yml up --force-recreate --build -d postgres
	@echo "Waiting for PostgreSQL to be ready..."
	@sleep 5
	@echo "Test environment ready! (Schema installed using pgqueue CLI)"

## Run unit tests only (no database required)
test-unit:
	@echo "Running unit tests..."
	@go test -v ./pkg/...

## Run integration tests (requires database)
test-integration: test-up
	@echo "Running integration tests..."
	@go test -v ./test/integration/...

## Run all tests (unit + integration)
test: test-unit test-integration

## Stop PostgreSQL test database
test-down:
	@echo "Stopping PostgreSQL..."
	@docker-compose -f test/docker/docker-compose.test.yml down

## Clean up test environment
test-clean: test-down
	@echo "Cleaning up test environment..."
	@docker-compose -f test/docker/docker-compose.test.yml down -v

## Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)/
	@go clean -testcache

## Download dependencies
deps:
	@echo "Downloading dependencies..."
	@go mod download
	@go mod tidy

## Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...
	@goimports -w . 2>/dev/null || echo "goimports not installed (optional)"

## Run linter
lint:
	@echo "Running linter..."
	@golangci-lint run 2>/dev/null || echo "golangci-lint not installed (optional)"