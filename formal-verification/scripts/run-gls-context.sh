#!/usr/bin/env bash
# run-gls-context.sh — Run Specula's 4-step pipeline for Phase 2 (GLS Push/Pop Model).
#
# This script verifies that every GLS Push has a corresponding Pop on all
# code paths — the invariant that prevents the leak addressed by the
# kakkoyun/orchestrion_gls_leak branch.
#
# Usage:
#   ./scripts/run-gls-context.sh [--dry-run] [--step N] [--help]
#
# Environment:
#   SPECULA_DIR        — Path to Specula installation (default: ~/.local/share/specula)
#   ANTHROPIC_API_KEY  — Required for Step 1 (LLM translation)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
SPECULA_DIR="${SPECULA_DIR:-${HOME}/.local/share/specula}"

SOURCE_FILE="$PROJECT_DIR/source/glscontext/gls_context.go"
CONFIG_FILE="$PROJECT_DIR/config/gls_context_config.yaml"
OUTPUT_DIR="$PROJECT_DIR/specs/gls_context"

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

Run Specula formal verification pipeline for the GLS Push/Pop model.

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

run_step1() {
    step 1 "LLM-assisted Go → TLA+ translation"
    info "Source: $SOURCE_FILE"
    info "This step translates GLS stack operations into TLA+ push/pop model"

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
    fi
}

run_step3() {
    step 3 "TLC model checking with automated fixes"
    info "Checking push/pop pairing and stack invariants"
    info "Max fix attempts: 5"

    local start_time
    start_time=$(date +%s)

    run_or_echo "$(specula_cmd) step3 \
        --input '$OUTPUT_DIR/step2_cfa.tla' \
        --config '$CONFIG_FILE' \
        --output '$OUTPUT_DIR/step3_verified.tla' \
        --max-attempts 5 \
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
    info "Step 4 skipped"
}

main() {
    echo "=== Specula Phase 2: GLS Push/Pop Formal Verification ==="
    echo ""
    echo "Model: GLS context stack push/pop pairing"
    echo "Source: source/gls_context.go"
    echo "Related: kakkoyun/orchestrion_gls_leak branch"
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
    fi
}

main "$@"
