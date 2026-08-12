#!/usr/bin/env bash
# run-tlc.sh — Run TLC model checker directly on hand-written TLA+ specs.
#
# This script runs TLC on the specs in formal-verification/specs/ without
# requiring Specula. It uses the TLA+ Toolbox's tla2tools.jar.
#
# Usage:
#   ./scripts/run-tlc.sh [--phase1] [--phase2] [--all] [--help]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Find Java
if [[ -x "/opt/homebrew/opt/openjdk/libexec/openjdk.jdk/Contents/Home/bin/java" ]]; then
    JAVA="/opt/homebrew/opt/openjdk/libexec/openjdk.jdk/Contents/Home/bin/java"
elif command -v java &>/dev/null; then
    JAVA="java"
else
    echo "ERROR: Java not found. Install OpenJDK 11+." >&2
    exit 1
fi

# Find tla2tools.jar
TLA2TOOLS="${TLA2TOOLS:-}"
if [[ -z "$TLA2TOOLS" ]]; then
    for candidate in \
        "/Applications/TLA+ Toolbox.app/Contents/Eclipse/tla2tools.jar" \
        "$HOME/tla2tools.jar" \
        "/usr/local/lib/tla2tools.jar"; do
        if [[ -f "$candidate" ]]; then
            TLA2TOOLS="$candidate"
            break
        fi
    done
fi

if [[ -z "$TLA2TOOLS" ]] || [[ ! -f "$TLA2TOOLS" ]]; then
    echo "ERROR: tla2tools.jar not found. Set TLA2TOOLS=/path/to/tla2tools.jar" >&2
    exit 1
fi

GREEN='\033[0;32m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { echo -e "${GREEN}[PASS]${NC} $*"; }
fail()  { echo -e "${RED}[FAIL]${NC} $*"; }
phase() { echo -e "${BLUE}━━━ $* ━━━${NC}"; }

TLC_OPTS="-XX:+UseParallelGC"
TLC_ARGS="-workers 4 -deadlock"

run_phase1() {
    phase "Phase 1: Span Lifecycle"
    local spec_dir="$PROJECT_DIR/specs/span_lifecycle"
    local start_time
    start_time=$(date +%s)

    if "$JAVA" $TLC_OPTS -cp "$TLA2TOOLS" tlc2.TLC $TLC_ARGS \
        -config "$spec_dir/SpanLifecycle.cfg" \
        "$spec_dir/SpanLifecycle.tla" 2>&1; then
        local elapsed=$(( $(date +%s) - start_time ))
        info "Phase 1 passed in ${elapsed}s"
        return 0
    else
        local elapsed=$(( $(date +%s) - start_time ))
        fail "Phase 1 FAILED in ${elapsed}s"
        return 1
    fi
}

run_phase2() {
    phase "Phase 2: GLS Push/Pop"
    local spec_dir="$PROJECT_DIR/specs/gls_context"
    local start_time
    start_time=$(date +%s)

    if "$JAVA" $TLC_OPTS -cp "$TLA2TOOLS" tlc2.TLC $TLC_ARGS \
        -config "$spec_dir/GLSContext.cfg" \
        "$spec_dir/GLSContext.tla" 2>&1; then
        local elapsed=$(( $(date +%s) - start_time ))
        info "Phase 2 passed in ${elapsed}s"
        return 0
    else
        local elapsed=$(( $(date +%s) - start_time ))
        fail "Phase 2 FAILED in ${elapsed}s"
        return 1
    fi
}

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Run TLC model checker on formal verification specs.

Options:
  --phase1    Run Phase 1 only (span lifecycle)
  --phase2    Run Phase 2 only (GLS push/pop)
  --all       Run all phases (default)
  --help      Show this help

Environment:
  TLA2TOOLS   Path to tla2tools.jar (auto-detected from TLA+ Toolbox)
EOF
    exit 0
}

main() {
    local run_p1=false run_p2=false

    if [[ $# -eq 0 ]]; then
        run_p1=true
        run_p2=true
    fi

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --phase1)  run_p1=true; shift ;;
            --phase2)  run_p2=true; shift ;;
            --all)     run_p1=true; run_p2=true; shift ;;
            --help|-h) usage ;;
            *)         echo "Unknown option: $1"; usage ;;
        esac
    done

    echo "============================================================"
    echo "  TLC Model Checker — dd-trace-go Formal Verification"
    echo "============================================================"
    echo "Java: $JAVA"
    echo "TLA+ Tools: $TLA2TOOLS"
    echo ""

    local overall_start failures=0
    overall_start=$(date +%s)

    if [[ "$run_p1" = true ]]; then
        run_phase1 || failures=$((failures + 1))
        echo ""
    fi

    if [[ "$run_p2" = true ]]; then
        run_phase2 || failures=$((failures + 1))
        echo ""
    fi

    local total_elapsed=$(( $(date +%s) - overall_start ))
    echo "============================================================"
    if [[ $failures -eq 0 ]]; then
        info "All phases passed in ${total_elapsed}s"
    else
        fail "$failures phase(s) failed in ${total_elapsed}s"
    fi
    echo "============================================================"

    exit "$failures"
}

main "$@"
