BIN   := $(shell pwd)/bin
TOOLS := $(shell pwd)/_tools
BIN_PATH := PATH="$(abspath $(BIN)):$$PATH"

.PHONY: help
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: all
all: tools-install generate lint test ## Run complete build pipeline (tools, generate, lint, test)

.PHONY: tools-install
tools-install: ## Install development tools
	@./scripts/install_tools.sh --tools-dir $(TOOLS) --bin-dir $(BIN)

.PHONY: clean
clean: ## Clean build artifacts
	rm -rvf coverprofile.txt *.out *.test vendor core_coverage.txt gotestsum-*

.PHONY: clean-all
clean-all: clean ## Clean everything including tools and temporary files
	rm -rvf $(BIN) tmp

.PHONY: generate
generate: tools-install ## Run code generation
	$(BIN_PATH) ./scripts/generate.sh

.PHONY: lint
lint: tools-install ## Run linting checks
	$(BIN_PATH) ./scripts/lint.sh --all

.PHONY: lint-fix
lint-fix: tools-install ## Fix linting issues automatically
	$(BIN_PATH) golangci-lint run --fix ./...

.PHONY: format
format: tools-install ## Format code
	$(BIN_PATH) ./scripts/format.sh --all

.PHONY: test
test: tools-install ## Run all tests (core, integration, contrib)
	$(BIN_PATH) ./scripts/test.sh --all

.PHONY: test-appsec
test-appsec: tools-install ## Run tests with AppSec enabled
	$(BIN_PATH) ./scripts/test.sh --appsec

.PHONY: test-contrib
test-contrib: tools-install ## Run contrib package tests
	$(BIN_PATH) ./scripts/test.sh --contrib

.PHONY: test-integration
test-integration: tools-install ## Run integration tests
	$(BIN_PATH) ./scripts/test.sh --integration

.PHONY: test-deadlock
test-deadlock: tools-install ## Run tests with deadlock detection
	BUILD_TAGS=deadlock $(BIN_PATH) ./scripts/test.sh --all

.PHONY: test-debug-deadlock
test-debug-deadlock: tools-install ## Run tests with debug and deadlock detection
	BUILD_TAGS=debug,deadlock $(BIN_PATH) ./scripts/test.sh --all

.PHONY: fix-modules
fix-modules: tools-install ## Fix module dependencies and consistency
	$(BIN_PATH) ./scripts/fix_modules.sh

tmp/make-help.txt: ## Generate make help output for documentation
	@mkdir -p tmp
	@make help --no-print-directory > tmp/make-help.txt 2>&1 || true

tmp/test-help.txt: ## Generate test script help output for documentation
	@mkdir -p tmp
	@./scripts/test.sh --help > tmp/test-help.txt 2>&1 || true

.PHONY: docs
docs: tools-install tmp/make-help.txt tmp/test-help.txt ## Update embedded documentation in README files
	$(BIN_PATH) embedmd -w README.md scripts/README.md
