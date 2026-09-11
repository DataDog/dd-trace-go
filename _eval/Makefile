GO_VERSION ?= 1.25.0
CLAUDE_CLI_VERSION ?= 2.1.231
CODEX_CLI_VERSION ?= 0.147.0

CLAUDE_IMAGE ?= dd-trace-go-agent-eval/claude:$(CLAUDE_CLI_VERSION)
CODEX_IMAGE ?= dd-trace-go-agent-eval/codex:$(CODEX_CLI_VERSION)

.PHONY: help
help: ## Show this help message
	@grep -hE '^[a-zA-Z0-9_/-]+:.*?##' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the harness
	go build ./...

.PHONY: test
test: ## Run the harness tests
	go test ./...

.PHONY: test/short
test/short: ## Run tests, skipping the ones that materialise the repo tree
	go test -short ./...

.PHONY: test/container
test/container: images ## Verify container isolation and existing agent logins
	AGENT_EVAL_DOCKER_TEST=1 go test ./internal/agenteval -run 'TestAgentContainer(Isolation|Authentication)' -count=1 -v

.PHONY: lint
lint: ## Vet and check formatting
	go vet ./...
	gofmt -l .

.PHONY: fmt
fmt: ## Format the harness
	gofmt -w .

.PHONY: suites
suites: ## List the registered task suites
	go run ./cmd/agent-eval suites

.PHONY: images
images: image/claude image/codex ## Build both isolated agent images

.PHONY: image/claude
image/claude: ## Build the isolated Claude Code image
	docker build --load --target claude --build-arg GO_VERSION=$(GO_VERSION) --build-arg CLAUDE_CLI_VERSION=$(CLAUDE_CLI_VERSION) --tag $(CLAUDE_IMAGE) --file docker/agents.Dockerfile docker

.PHONY: image/codex
image/codex: ## Build the isolated Codex image
	docker build --load --target codex --build-arg GO_VERSION=$(GO_VERSION) --build-arg CODEX_CLI_VERSION=$(CODEX_CLI_VERSION) --tag $(CODEX_IMAGE) --file docker/agents.Dockerfile docker

.PHONY: images/check
images/check: ## Print the agent CLI versions installed in both images
	docker run --rm $(CLAUDE_IMAGE) claude --version
	docker run --rm $(CODEX_IMAGE) codex --version

.PHONY: verify
verify: ## Check that every suite's mutations still apply to main and HEAD
	go run ./cmd/agent-eval verify -suite all -refs main,HEAD

.PHONY: clean
clean: ## Remove run artifacts
	rm -rf results
