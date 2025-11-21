BIN   := $(shell pwd)/bin
TOOLS := $(shell pwd)/_tools
BIN_PATH := PATH="$(abspath $(BIN)):$$PATH"

.PHONY: help
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[A-Za-z0-9_./-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: all
all: tools-install generate lint test ## Run complete build pipeline (tools, generate, lint, test)

.PHONY: tools-install
tools-install: tools-install/checkmake ## Install development tools
	@./scripts/install_tools.sh --tools-dir $(TOOLS) --bin-dir $(BIN)

# checkmake is installed as a pre-built binary rather than via go install
# because it requires Go 1.25+, which would force an upgrade of our _tools module.
# We keep the _tools module on Go 1.24.0 to match our main project requirements.
# For platforms without pre-built binaries, we fall back to building from source.
.PHONY: tools-install/checkmake
tools-install/checkmake: ## Install checkmake binary for Makefile linting
	@mkdir -p $(BIN)
	@if [ ! -f $(BIN)/checkmake ]; then \
		echo "Installing checkmake..."; \
		CHECKMAKE_VERSION=0.2.2; \
		OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
		ARCH=$$(uname -m); \
		if [ "$$ARCH" = "x86_64" ]; then ARCH="amd64"; fi; \
		if [ "$$ARCH" = "aarch64" ]; then ARCH="arm64"; fi; \
		BINARY="checkmake-$$CHECKMAKE_VERSION.$$OS.$$ARCH"; \
		if curl -sSfL -o $(BIN)/checkmake "https://github.com/checkmake/checkmake/releases/download/$$CHECKMAKE_VERSION/$$BINARY" 2>/dev/null; then \
			chmod +x $(BIN)/checkmake; \
			echo "checkmake $$CHECKMAKE_VERSION installed from pre-built binary"; \
		else \
			echo "Pre-built binary not available for $$OS/$$ARCH, building from source..."; \
			GOBIN=$(abspath $(BIN)) go install github.com/checkmake/checkmake/cmd/checkmake@latest; \
			echo "checkmake installed from source"; \
		fi; \
	fi

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

.PHONY: lint/go
lint/go: tools-install ## Run Go linting checks
	$(BIN_PATH) ./scripts/lint.sh --go

.PHONY: lint/go/fix
lint/go/fix: tools-install ## Fix linting issues automatically
	$(BIN_PATH) golangci-lint run --fix ./...

.PHONY: lint/shell
lint/shell: tools-install ## Run shell script linting checks
	$(BIN_PATH) ./scripts/lint.sh --shell

.PHONY: lint/misc
lint/misc: tools-install ## Run miscellaneous linting checks (copyright, Makefiles)
	$(BIN_PATH) ./scripts/lint.sh --misc

.PHONY: format
format: tools-install ## Format code
	$(BIN_PATH) ./scripts/format.sh --all

.PHONY: format/shell
format/shell: tools-install ## install shfmt
	$(BIN_PATH) ./scripts/format.sh --shell

.PHONY: test
test: tools-install ## Run all tests (core, integration, contrib)
	$(BIN_PATH) ./scripts/test.sh --all

.PHONY: test-appsec
test/appsec: tools-install ## Run tests with AppSec enabled
	$(BIN_PATH) ./scripts/test.sh --appsec

.PHONY: test-contrib
test/contrib: tools-install ## Run contrib package tests
	$(BIN_PATH) ./scripts/test.sh --contrib

.PHONY: test-integration
test/integration: tools-install ## Run integration tests
	$(BIN_PATH) ./scripts/test.sh --integration

.PHONY: fix-modules
fix-modules: tools-install ## Fix module dependencies and consistency
	$(BIN_PATH) ./scripts/fix_modules.sh

.PHONY: tmp/make-help.txt
tmp/make-help.txt:
	@mkdir -p tmp
	@make help --no-print-directory > tmp/make-help.txt 2>&1 || true

.PHONY: tmp/test-help.txt
tmp/test-help.txt:
	@mkdir -p tmp
	@./scripts/test.sh --help > tmp/test-help.txt 2>&1 || true

.PHONY: docs
docs: tools-install tmp/make-help.txt tmp/test-help.txt ## Generate and Update embedded documentation in README files
	$(BIN_PATH) embedmd -w README.md scripts/README.md

ORCHESTRION_VERSION := latest
ORCHESTRION_DIRS := internal/orchestrion/_integration orchestrion/all

.PHONY: upgrade/orchestrion
upgrade/orchestrion: ## Upgrade Orchestrion and fix modules
	$(BIN_PATH) ORCHESTRION_VERSION=$(ORCHESTRION_VERSION) ORCHESTRION_DIRS="$(ORCHESTRION_DIRS)" ./scripts/upgrade_orchestrion.sh
