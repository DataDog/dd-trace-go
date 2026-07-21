#!/usr/bin/env bash
# setup.sh — Install Specula and its dependencies for formal verification.
#
# Prerequisites:
#   - Python 3.8+
#   - Java 11+ (for TLC model checker)
#   - Maven (for building TLC)
#   - ANTHROPIC_API_KEY environment variable (for LLM-assisted translation)
#
# Usage:
#   ./scripts/setup.sh [--check-only]

set -euo pipefail

SPECULA_DIR="${SPECULA_DIR:-${HOME}/.local/share/specula}"
SPECULA_REPO="https://github.com/specula-org/Specula.git"
SPECULA_BRANCH="main"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# ---------------------------------------------------------------------------
# Prerequisite checks
# ---------------------------------------------------------------------------

check_prerequisites() {
    local ok=true

    # Python 3.8+
    if command -v python3 &>/dev/null; then
        local pyver
        pyver=$(python3 -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')
        local pymajor pyminor
        pymajor=$(echo "$pyver" | cut -d. -f1)
        pyminor=$(echo "$pyver" | cut -d. -f2)
        if [[ $pymajor -lt 3 ]] || { [[ $pymajor -eq 3 ]] && [[ $pyminor -lt 8 ]]; }; then
            error "Python 3.8+ required, found $pyver"
            ok=false
        else
            info "Python $pyver ✓"
        fi
    else
        error "python3 not found"
        ok=false
    fi

    # Java 11+
    if command -v java &>/dev/null; then
        local javaver
        javaver=$(java -version 2>&1 | head -1 | sed -E 's/.*"([0-9]+)\..*/\1/')
        if [[ $javaver -lt 11 ]]; then
            error "Java 11+ required, found version $javaver"
            ok=false
        else
            info "Java $javaver ✓"
        fi
    else
        error "java not found (JRE/JDK 11+ required for TLC)"
        ok=false
    fi

    # Maven
    if command -v mvn &>/dev/null; then
        info "Maven $(mvn --version 2>/dev/null | head -1 | awk '{print $3}') ✓"
    else
        warn "Maven not found (optional, needed only if building TLC from source)"
    fi

    # Git
    if command -v git &>/dev/null; then
        info "Git $(git --version | awk '{print $3}') ✓"
    else
        error "git not found"
        ok=false
    fi

    # ANTHROPIC_API_KEY
    if [[ -n "${ANTHROPIC_API_KEY:-}" ]]; then
        info "ANTHROPIC_API_KEY set ✓"
    else
        warn "ANTHROPIC_API_KEY not set (required for LLM-assisted translation in Step 1)"
    fi

    if [[ "$ok" = false ]]; then
        error "Prerequisite check failed. Install missing dependencies and retry."
        exit 1
    fi
}

# ---------------------------------------------------------------------------
# Install Specula
# ---------------------------------------------------------------------------

install_specula() {
    if [[ -d "$SPECULA_DIR" ]]; then
        info "Specula already installed at $SPECULA_DIR"
        info "Pulling latest changes..."
        git -C "$SPECULA_DIR" pull --ff-only origin "$SPECULA_BRANCH" || {
            warn "Pull failed; using existing checkout"
        }
    else
        info "Cloning Specula to $SPECULA_DIR..."
        mkdir -p "$(dirname "$SPECULA_DIR")"
        git clone --branch "$SPECULA_BRANCH" "$SPECULA_REPO" "$SPECULA_DIR"
    fi

    # Run Specula's own setup script if present
    if [[ -x "$SPECULA_DIR/scripts/setup.sh" ]]; then
        info "Running Specula's setup script..."
        (cd "$SPECULA_DIR" && ./scripts/setup.sh)
    elif [[ -f "$SPECULA_DIR/requirements.txt" ]]; then
        info "Installing Python dependencies..."
        python3 -m pip install --quiet -r "$SPECULA_DIR/requirements.txt"
    fi

    # Verify installation
    if [[ -x "$SPECULA_DIR/specula" ]]; then
        info "Specula installed successfully:"
        "$SPECULA_DIR/specula" --version 2>/dev/null || "$SPECULA_DIR/specula" --help 2>/dev/null | head -3
    elif [[ -f "$SPECULA_DIR/specula.py" ]]; then
        info "Specula installed (Python entry point):"
        python3 "$SPECULA_DIR/specula.py" --version 2>/dev/null || python3 "$SPECULA_DIR/specula.py" --help 2>/dev/null | head -3
    else
        warn "Specula entry point not found; check $SPECULA_DIR for manual setup"
    fi
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

main() {
    echo "=== Specula Setup for dd-trace-go Formal Verification ==="
    echo ""

    check_prerequisites

    if [[ "${1:-}" = "--check-only" ]]; then
        info "Prerequisite check passed. Use without --check-only to install."
        exit 0
    fi

    echo ""
    install_specula

    echo ""
    info "Setup complete."
    info "Specula directory: $SPECULA_DIR"
    info ""
    info "Next steps:"
    info "  1. Export ANTHROPIC_API_KEY if not set"
    info "  2. Run: ./scripts/run-span-lifecycle.sh"
    info "  3. Run: ./scripts/run-gls-context.sh"
}

main "$@"
