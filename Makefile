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

# checkmake is installed as a pre-built binary for simplicity and speed.
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

.PHONY: lint/action
lint/action: tools-install ## Lint GitHub Actions workflows
	$(BIN_PATH) ./scripts/lint.sh --action

.PHONY: format
format: tools-install ## Format code
	$(BIN_PATH) ./scripts/format.sh --all

.PHONY: format/go
format/go: tools-install ## Format Go code
	$(BIN_PATH) ./scripts/format.sh --go

.PHONY: format/shell
format/shell: tools-install ## install shfmt
	$(BIN_PATH) ./scripts/format.sh --shell

.PHONY: test
test: tools-install test/unit ## Run all tests (core, integration, contrib)
	$(BIN_PATH) ./scripts/test.sh --all

.PHONY: test/unit
test/unit: tools-install ## Run unit tests
	go test -v -failfast ./...

.PHONY: test/appsec
test/appsec: tools-install ## Run tests with AppSec enabled
	$(BIN_PATH) ./scripts/test.sh --appsec

.PHONY: test/contrib
test/contrib: tools-install ## Run contrib package tests
	$(BIN_PATH) ./scripts/test.sh --contrib

.PHONY: test/integration
test/integration: tools-install ## Run integration tests
	$(BIN_PATH) ./scripts/test.sh --integration

.PHONY: test-deadlock
test-deadlock: tools-install ## Run tests with deadlock detection
	BUILD_TAGS=deadlock $(BIN_PATH) ./scripts/test.sh --all

.PHONY: test-debug-deadlock
test-debug-deadlock: tools-install ## Run tests with debug and deadlock detection
	BUILD_TAGS=debug,deadlock $(BIN_PATH) ./scripts/test.sh --all

# CI-parity targets. `make test/*` uses the root docker-compose.yaml; CI uses
# .github/testservices/docker-compose.yaml. See "Reproducing
# CI locally" in CONTRIBUTING.md.
CI_TEST_RESULTS := /tmp/test-results
BUILD_TAGS ?=
CHUNK ?= 1
JOB ?= core
SERVICES ?=

# CI's test-core job starts only the agent (`docker compose up -d datadog-agent`); only
# test-contrib brings up the whole stack. Mirroring that keeps ci/run faithful and, on a
# laptop, avoids the memory pressure of 20 containers when the job needs one.
ifeq ($(JOB),core)
CI_JOB_SERVICES := datadog-agent
endif

# Five images in CI's stack publish no arm64 manifest at all (elasticsearch:2,
# elasticsearch:5, elasticsearch:6.8.13, cimg/mysql:8.0, mssql/server:2019-latest), so on
# Apple Silicon they only run under emulation. The rest do ship arm64 variants — which is
# its own problem, since CI runs amd64 and we want the same builds, not merely working
# ones. scripts/test.sh forces the platform the same way for the root stack.
ifeq ($(shell uname -s)-$(shell uname -m),Darwin-arm64)
CI_PLATFORM := DOCKER_DEFAULT_PLATFORM=linux/amd64
endif
CI_COMPOSE := $(CI_PLATFORM) COMPOSE_FILE=.github/testservices/docker-compose.yaml COMPOSE_PROJECT_NAME=dd-trace-go-ci
CI_ENV := $(CI_COMPOSE) INTEGRATION=true GOTOOLCHAIN=local GODEBUG=x509negativeserial=1 \
	TEST_RESULTS=$(CI_TEST_RESULTS) BUILD_TAGS=$(BUILD_TAGS)
REQUIRE_JQ = command -v jq > /dev/null || { echo "jq is required (brew install jq)" >&2; exit 1; }
# scripts/ci_test_core.sh (like scripts/test.sh) uses `mapfile`, a bash 4 builtin. macOS
# ships bash 3.2, so `env bash` there fails with a bare "mapfile: command not found".
REQUIRE_BASH4 = bash -c 'type mapfile' > /dev/null 2>&1 || { \
	echo "this needs bash >= 4; found $$(bash --version | head -1)" >&2; \
	echo "on macOS: brew install bash, then put its prefix ahead of /bin in PATH" >&2; \
	exit 1; }

.PHONY: ci/run
ci/run: tools-install ## Reproduce a CI job end to end (JOB=core|contrib, CHUNK=n)
	@case "$(JOB)" in \
	  core) echo "==> reproducing the test-core job" ;; \
	  contrib) echo "==> reproducing test-contrib, chunk $(CHUNK)" ;; \
	  *) echo "JOB must be 'core' or 'contrib' (got '$(JOB)')" >&2; exit 1 ;; \
	esac
	@$(MAKE) --no-print-directory ci/services SERVICES="$(CI_JOB_SERVICES)"
	@trap '$(MAKE) --no-print-directory ci/services/down' EXIT INT TERM; \
	  $(MAKE) --no-print-directory ci/$(JOB) CHUNK=$(CHUNK) BUILD_TAGS=$(BUILD_TAGS)

.PHONY: ci/services
ci/services: ## Start CI's service containers (all, or SERVICES="a b")
	@$(CI_COMPOSE) docker compose up -d --wait --wait-timeout 120 $(SERVICES) || { \
	  echo "" >&2; \
	  echo "ci/services failed. The two usual causes:" >&2; \
	  echo "  'address already in use'         -> a local service holds one of CI's ports; stop it" >&2; \
	  echo "  'does not provide the platform'  -> a cached image is the wrong arch; make ci/services/pull" >&2; \
	  exit 1; }

# An image pulled earlier without a forced platform is cached as arm64 only; `up` then
# reports "does not provide the specified platform" and will not fetch the amd64 variant
# on its own. Re-pulling with the platform forced repairs the local store.
.PHONY: ci/services/pull
ci/services/pull: ## Re-pull CI's images at CI's platform (repairs wrong-arch cache)
	$(CI_COMPOSE) docker compose pull --policy always

.PHONY: ci/services/down
ci/services/down: ## Stop CI's service containers
	$(CI_COMPOSE) docker compose down

.PHONY: ci/core
ci/core: tools-install ## Run CI's test-core entrypoint alone (services must be up)
	@$(REQUIRE_BASH4)
	@mkdir -p $(CI_TEST_RESULTS)
	$(BIN_PATH) $(CI_ENV) DD_APPSEC_WAF_TIMEOUT=1h ./scripts/ci_test_core.sh

.PHONY: ci/contrib/chunks
ci/contrib/chunks: ## List the contrib chunks CI splits test-contrib into
	@$(REQUIRE_JQ)
	@go run ./scripts/ci_contrib_matrix.go | jq -r 'to_entries[] | "CHUNK=\(.key + 1)\t\(.value)"'

.PHONY: ci/contrib
ci/contrib: tools-install ## Run one test-contrib chunk alone (services must be up)
	@mkdir -p $(CI_TEST_RESULTS)
	@$(REQUIRE_JQ)
	@chunk=$$(go run ./scripts/ci_contrib_matrix.go | jq -er ".[$$(($(CHUNK) - 1))]") || \
		{ echo "no chunk $(CHUNK); run 'make ci/contrib/chunks' for the valid range" >&2; exit 1; }; \
	$(BIN_PATH) $(CI_ENV) DD_APPSEC_WAF_TIMEOUT=1m ./scripts/ci_test_contrib.sh default "$$chunk"

.PHONY: fix-modules
fix-modules: tools-install ## Fix module dependencies and consistency
	$(BIN_PATH) ./scripts/fix_modules.sh

.PHONY: fix/go
fix/go: ## Apply go fix modernizations to Go code
	go fix ./...

.PHONY: fix/go/diff
fix/go/diff: ## Preview go fix modernizations (dry-run)
	go fix -diff ./...

.PHONY: apidiff
apidiff: tools-install ## Run semantic API diff for ddtrace/tracer against main
	$(BIN_PATH) ./scripts/apidiff.sh github.com/DataDog/dd-trace-go/v2/ddtrace/tracer

.PHONY: apidiff/incompatible
apidiff/incompatible: tools-install ## Show only breaking (incompatible) API changes for ddtrace/tracer
	$(BIN_PATH) ./scripts/apidiff.sh --incompatible-only --exit-code github.com/DataDog/dd-trace-go/v2/ddtrace/tracer

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

.PHONY: config-audit
config-audit: ## Report which DD_* configs are migrated to internal/config
	@cd scripts/configaudit && GOWORK=off go run . -root ../.. -format table
