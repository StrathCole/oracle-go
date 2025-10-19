.PHONY: build install test lint fmt clean help

# Build variables
BINARY_NAME=oracle-go
BUILD_DIR=build
CMD_DIR=cmd/oracle-go
GO=go
GOFUMPT=gofumpt
GOLANGCI_LINT=golangci-lint
MARKDOWNLINT=markdownlint-cli

# Build flags
LDFLAGS=-ldflags "-s -w"

## help: Display this help message
help:
	@echo "Available targets:"
	@grep -E '^##' $(MAKEFILE_LIST) | sed 's/## /  /'

## build: Build the oracle-go binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./$(CMD_DIR)

## install: Install oracle-go to $GOPATH/bin
install:
	@echo "Installing $(BINARY_NAME)..."
	$(GO) install $(LDFLAGS) ./$(CMD_DIR)

## test: Run all tests
test:
	@echo "Running tests..."
	$(GO) test -v -race -coverprofile=coverage.out ./...

## test-coverage: Run tests with coverage report
test-coverage: test
	@echo "Generating coverage report..."
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## lint: Run all linters (Go + Markdown)
lint: lint-go lint-md

## lint-go: Run Go linters with golangci-lint
lint-go:
	@echo "Running Go linters..."
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { \
		echo "golangci-lint not found. Install: https://golangci-lint.run/usage/install/"; \
		exit 1; \
	}
	$(GOLANGCI_LINT) run --timeout=5m

## lint-md: Run Markdown linter
lint-md:
	@echo "Running Markdown linter..."
	@command -v markdownlint >/dev/null 2>&1 || { \
		echo "markdownlint not found. Install: npm install -g markdownlint-cli"; \
		exit 1; \
	}
	markdownlint '**/*.md' --ignore node_modules --ignore build

## fmt: Format code with gofumpt and goimports
fmt:
	@echo "Formatting Go code..."
	@command -v $(GOFUMPT) >/dev/null 2>&1 || { \
		echo "gofumpt not found. Install: go install mvdan.cc/gofumpt@latest"; \
		exit 1; \
	}
	@find . -name '*.go' -not -path "./vendor/*" -not -path "./.git/*" | xargs $(GOFUMPT) -w -extra
	@echo "Running goimports..."
	@command -v goimports >/dev/null 2>&1 || { \
		echo "goimports not found. Install: go install golang.org/x/tools/cmd/goimports@latest"; \
		exit 1; \
	}
	@find . -name '*.go' -not -path "./vendor/*" -not -path "./.git/*" | xargs goimports -w -local github.com/StrathCole/oracle-go

## fmt-check: Check if code is formatted
fmt-check:
	@echo "Checking code formatting..."
	@test -z "$$($(GOFUMPT) -l -extra . | tee /dev/stderr)"

## clean: Remove build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out coverage.html

## deps: Download and verify dependencies
deps:
	@echo "Downloading dependencies..."
	$(GO) mod download
	$(GO) mod verify

## tidy: Tidy and verify dependencies
tidy:
	@echo "Tidying dependencies..."
	$(GO) mod tidy

## run-server: Run oracle in server-only mode
run-server: build
	@echo "Starting oracle in server mode..."
	$(BUILD_DIR)/$(BINARY_NAME) --server

## run-feeder: Run oracle in feeder-only mode
run-feeder: build
	@echo "Starting oracle in feeder mode..."
	$(BUILD_DIR)/$(BINARY_NAME) --feeder

## run: Run oracle in both mode (default)
run: build
	@echo "Starting oracle in both mode..."
	$(BUILD_DIR)/$(BINARY_NAME)

## check: Run tests and linters
check: test lint

## ci: Run all CI checks (format check, lint, test)
ci: fmt-check lint test
	@echo "✅ All CI checks passed!"

.DEFAULT_GOAL := help
