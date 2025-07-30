BIN   := $(shell pwd)/bin
TOOLS := $(shell pwd)/_tools
BIN_PATH := PATH="$(abspath $(BIN)):$$PATH"

.PHONY: tools-install
tools-install:
	@echo Installing tools from $(TOOLS)/tools.go
	@mkdir -p $(BIN)
	@cd $(TOOLS) && GOWORK=off go mod download
	@cd $(TOOLS) && GOWORK=off GOBIN=$(BIN) go install $$(grep -E '^[[:space:]]*_[[:space:]]+".*"' tools.go | awk -F'"' '{print $$2}')

.PHONY: default
default: tools-install generate lint test

.PHONY: clean
clean:
	rm -rvf $(BIN)coverprofile.txt *.out *.test vendor

.PHONY: generate
generate: tools-install
	$(BIN_PATH) ./scripts/generate.sh

.PHONY: lint
lint: tools-install
	$(BIN_PATH) golangci-lint --version
	$(BIN_PATH) golangci-lint run ./...

.PHONY: lint-fix
lint-fix: tools-install
	$(BIN_PATH) golangci-lint run --fix ./...

.PHONY: format
format: tools-install
	$(BIN_PATH) golangci-lint fmt ./...

.PHONY: test
test: tools-install
	$(BIN_PATH) ./scripts/test.sh --all

.PHONY: test-appsec
test-appsec: tools-install
	$(BIN_PATH) ./scripts/test.sh --appsec

.PHONY: test-contrib
test-contrib: tools-install
	$(BIN_PATH) ./scripts/test.sh --contrib

.PHONY: test-integration
test-integration: tools-install
	$(BIN_PATH) ./scripts/test.sh --integration
