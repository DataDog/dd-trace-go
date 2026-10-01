# Makefile for dd-trace-go formal verification with Specula
#
# Usage:
#   make setup          — Install Specula and check prerequisites
#   make run-phase1     — Run Phase 1 via Specula (span lifecycle model)
#   make run-phase2     — Run Phase 2 via Specula (GLS push/pop model)
#   make run-all        — Run both Specula phases
#   make tlc            — Run TLC directly on hand-written TLA+ specs
#   make tlc-phase1     — Run TLC on Phase 1 only
#   make tlc-phase2     — Run TLC on Phase 2 only
#   make validate       — Verify extracted source files compile
#   make clean          — Remove generated logs (preserves specs)
#
# Options:
#   DRY_RUN=--dry-run   — Print Specula commands without executing

.PHONY: setup run-phase1 run-phase2 run-all tlc tlc-phase1 tlc-phase2 validate clean help

SCRIPTS := scripts
DRY_RUN ?=

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

setup: ## Install Specula and check prerequisites
	@chmod +x $(SCRIPTS)/*.sh
	$(SCRIPTS)/setup.sh

run-phase1: ## Run Phase 1 via Specula pipeline
	$(SCRIPTS)/run-span-lifecycle.sh $(DRY_RUN)

run-phase2: ## Run Phase 2 via Specula pipeline
	$(SCRIPTS)/run-gls-context.sh $(DRY_RUN)

run-all: ## Run all Specula verification phases
	$(SCRIPTS)/run-all.sh $(DRY_RUN)

tlc: ## Run TLC on all hand-written TLA+ specs
	$(SCRIPTS)/run-tlc.sh --all

tlc-phase1: ## Run TLC on Phase 1 (span lifecycle)
	$(SCRIPTS)/run-tlc.sh --phase1

tlc-phase2: ## Run TLC on Phase 2 (GLS push/pop)
	$(SCRIPTS)/run-tlc.sh --phase2

validate: ## Verify extracted source files compile
	@echo "Checking source files..."
	@cd source && GOWORK=off go build ./...
	@cd source && GOWORK=off go vet ./...
	@echo "Source files compile and pass go vet."

clean: ## Remove generated logs (preserves hand-written specs)
	rm -rf specs/span_lifecycle/*.log specs/span_lifecycle/states/
	rm -rf specs/gls_context/*.log specs/gls_context/states/
	@echo "Cleaned generated files."
