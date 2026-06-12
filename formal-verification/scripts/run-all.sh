#!/usr/bin/env bash
# run-all.sh — Run both Specula formal verification phases sequentially.
#
# Usage:
#   ./scripts/run-all.sh [--dry-run] [--help]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

info() { echo -e "${GREEN}[INFO]${NC} $*"; }

ARGS=("$@")

for arg in "$@"; do
    if [[ "$arg" = "--help" ]] || [[ "$arg" = "-h" ]]; then
        cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Run all Specula formal verification phases.

Options:
  --dry-run     Print what would be executed without running
  --help        Show this help

This runs:
  Phase 1: Span Lifecycle Model (lock ordering, finish-guard, partial flush)
  Phase 2: GLS Push/Pop Model (push/pop pairing, stack invariants)
EOF
        exit 0
    fi
done

main() {
    echo "============================================================"
    echo "  Specula Formal Verification — dd-trace-go"
    echo "============================================================"
    echo ""

    local overall_start
    overall_start=$(date +%s)

    echo -e "${BLUE}━━━ Phase 1: Span Lifecycle ━━━${NC}"
    echo ""
    "$SCRIPT_DIR/run-span-lifecycle.sh" "${ARGS[@]}"

    echo ""
    echo -e "${BLUE}━━━ Phase 2: GLS Push/Pop ━━━${NC}"
    echo ""
    "$SCRIPT_DIR/run-gls-context.sh" "${ARGS[@]}"

    echo ""
    echo "============================================================"
    local total_elapsed=$(( $(date +%s) - overall_start ))
    info "All phases completed in ${total_elapsed}s"
    info "Results in: formal-verification/specs/"
    echo "============================================================"
}

main
