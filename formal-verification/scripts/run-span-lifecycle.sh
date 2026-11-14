#!/usr/bin/env bash
# run-span-lifecycle.sh — Run Specula's 4-step pipeline for Phase 1 (Span Lifecycle Model).
#
# This script:
#   1. Translates source/span_lifecycle.go → TLA+ using LLM + RAG
#   2. Applies Control Flow Analysis to produce declarative TLA+
#   3. Runs TLC model checker with automated fix loop (up to 5 attempts)
#   4. Optionally validates traces against instrumented tests
#
# Usage:
#   ./scripts/run-span-lifecycle.sh [--dry-run] [--step N] [--help]
#
# Environment:
#   SPECULA_DIR    — Path to Specula installation (default: ~/.local/share/specula)
#   ANTHROPIC_API_KEY — Required for Step 1 (LLM translation)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
SPECULA_DIR="${SPECULA_DIR:-${HOME}/.local/share/specula}"

SOURCE_FILE="$PROJECT_DIR/source/spanlifecycle/span_lifecycle.go"
CONFIG_FILE="$PROJECT_DIR/config/span_lifecycle_config.yaml"
OUTPUT_DIR="$PROJECT_DIR/specs/span_lifecycle"

DRY_RUN=false
START_STEP=1
END_STEP=4

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }
step()  { echo -e "${BLUE}[STEP $1]${NC} $2"; }

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Run Specula formal verification pipeline for the Span Lifecycle model.

Options:
  --dry-run     Print what would be executed without running
  --step N      Run only step N (1-4)
  --from N      Start from step N (default: 1)
  --to N        End at step N (default: 4)
  --help        Show this help

Steps:
  1  LLM-assisted Go → TLA+ translation (requires ANTHROPIC_API_KEY)
  2  Control Flow Analysis (CFA) transformation
  3  TLC model checking with automated fixes
  4  Trace validation against tests (optional)

Environment:
  SPECULA_DIR        Specula installation (default: ~/.local/share/specula)
  ANTHROPIC_API_KEY  Required for Step 1
EOF
    exit 0
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run) DRY_RUN=true; shift ;;
        --step)    START_STEP="$2"; END_STEP="$2"; shift 2 ;;
        --from)    START_STEP="$2"; shift 2 ;;
        --to)      END_STEP="$2"; shift 2 ;;
        --help|-h) usage ;;
        *) error "Unknown option: $1"; usage ;;
    esac
done

# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------

validate() {
    if [[ "$DRY_RUN" = false ]] && [[ ! -d "$SPECULA_DIR" ]]; then
        error "Specula not found at $SPECULA_DIR"
        error "Run ./scripts/setup.sh first"
        exit 1
    fi

    if [[ ! -f "$SOURCE_FILE" ]]; then
        error "Source file not found: $SOURCE_FILE"
        exit 1
    fi

    if [[ $START_STEP -le 1 ]] && [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
        warn "ANTHROPIC_API_KEY not set; Step 1 will fail"
    fi

    mkdir -p "$OUTPUT_DIR"
}

# ---------------------------------------------------------------------------
# Specula command helper
# ---------------------------------------------------------------------------

specula_cmd() {
    if [[ -x "$SPECULA_DIR/specula" ]]; then
        echo "$SPECULA_DIR/specula"
    elif [[ -f "$SPECULA_DIR/specula.py" ]]; then
        echo "python3 $SPECULA_DIR/specula.py"
    elif [[ "$DRY_RUN" = true ]]; then
        echo "specula"  # placeholder for dry-run
    else
        error "Specula executable not found in $SPECULA_DIR"
        exit 1
    fi
}

run_or_echo() {
    if [[ "$DRY_RUN" = true ]]; then
        echo "  [DRY-RUN] $*"
    else
        eval "$@"
    fi
}

# ---------------------------------------------------------------------------
# Pipeline steps
# ---------------------------------------------------------------------------

run_step1() {
    step 1 "LLM-assisted Go → TLA+ translation"
    info "Source: $SOURCE_FILE"
    info "This step uses Claude to translate Go concurrency patterns into TLA+"

    local start_time
    start_time=$(date +%s)

    run_or_echo "$(specula_cmd) step1 \
        --source '$SOURCE_FILE' \
        --config '$CONFIG_FILE' \
        --output '$OUTPUT_DIR/step1_draft.tla' \
        2>&1 | tee '$OUTPUT_DIR/step1.log'"

    if [[ "$DRY_RUN" = false ]]; then
        local elapsed=$(( $(date +%s) - start_time ))
        info "Step 1 completed in ${elapsed}s"
        info "Draft TLA+ spec: $OUTPUT_DIR/step1_draft.tla"
    fi
}

run_step2() {
    step 2 "Control Flow Analysis (CFA) transformation"
    info "Transforming procedural TLA+ into declarative form"

    local start_time
    start_time=$(date +%s)

    run_or_echo "$(specula_cmd) step2 \
        --input '$OUTPUT_DIR/step1_draft.tla' \
        --config '$CONFIG_FILE' \
        --output '$OUTPUT_DIR/step2_cfa.tla' \
        2>&1 | tee '$OUTPUT_DIR/step2.log'"

    if [[ "$DRY_RUN" = false ]]; then
        local elapsed=$(( $(date +%s) - start_time ))
        info "Step 2 completed in ${elapsed}s"
        info "Declarative TLA+ spec: $OUTPUT_DIR/step2_cfa.tla"
    fi
}

run_step3() {
    step 3 "TLC model checking with automated fixes"
    info "Running TLC to find invariant violations"
    info "Max fix attempts: 5"

    local start_time attempt=1 max_attempts=5
    start_time=$(date +%s)

    run_or_echo "$(specula_cmd) step3 \
        --input '$OUTPUT_DIR/step2_cfa.tla' \
        --config '$CONFIG_FILE' \
        --output '$OUTPUT_DIR/step3_verified.tla' \
        --max-attempts $max_attempts \
        2>&1 | tee '$OUTPUT_DIR/step3.log'"

    if [[ "$DRY_RUN" = false ]]; then
        local elapsed=$(( $(date +%s) - start_time ))
        info "Step 3 completed in ${elapsed}s"

        if [[ -f "$OUTPUT_DIR/step3_verified.tla" ]]; then
            info "Verified TLA+ spec: $OUTPUT_DIR/step3_verified.tla"
        else
            warn "TLC did not produce a verified spec; check $OUTPUT_DIR/step3.log"
        fi
    fi
}

run_step4() {
    step 4 "Trace validation (optional)"
    warn "Step 4 requires instrumented test harness — skipping by default"
    warn "To enable, set step4.enabled=true in $CONFIG_FILE"

    if [[ "$DRY_RUN" = false ]]; then
        info "Step 4 skipped (not yet instrumented)"
    fi
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

main() {
    echo "=== Specula Phase 1: Span Lifecycle Formal Verification ==="
    echo ""
    echo "Model: span lifecycle with lock ordering, finish-guard, partial flush"
    echo "Source: source/span_lifecycle.go"
    echo ""

    if [[ "$DRY_RUN" = true ]]; then
        warn "DRY RUN — no commands will be executed"
        echo ""
    fi

    validate

    local overall_start
    overall_start=$(date +%s)

    for s in $(seq "$START_STEP" "$END_STEP"); do
        case "$s" in
            1) run_step1 ;;
            2) run_step2 ;;
            3) run_step3 ;;
            4) run_step4 ;;
            *) error "Invalid step: $s"; exit 1 ;;
        esac
        echo ""
    done

    if [[ "$DRY_RUN" = false ]]; then
        local total_elapsed=$(( $(date +%s) - overall_start ))
        info "Pipeline completed in ${total_elapsed}s"
        info "Output directory: $OUTPUT_DIR"
        info ""
        info "Next: review generated TLA+ specs in $OUTPUT_DIR/"
        info "See docs/05-interpreting-results.md for guidance"
    fi
}

main "$@"
