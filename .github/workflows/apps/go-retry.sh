# shellcheck shell=bash
# go-retry.sh — retry a command when its output shows Go toolchain/cache corruption.
#
# Mirrors retry_on_corruption in .github/workflows/smoke-tests.yml. go1.26.5 can corrupt its
# heap mid-build and leave partial cache/module archives that resurface as zip/archive checksum
# errors; a "zip: checksum error" during install/download is the same transient class. Retry
# those; fail fast on anything else — govulncheck exit 3 (vulnerabilities found) never matches
# the signature and so is never retried.
#
# Usage:
#   source go-retry.sh
#   retry_on_corruption <cmd> [args...]                  # output streams to the caller as-is
#   retry_on_corruption_to_file <outfile> <cmd> [args...] # <cmd>'s stdout is captured to <outfile>
corruption_re='internal compiler error|zip: checksum error|not the start of an archive file|found pointer to free object|fatal error: fault|unexpected signal during runtime execution|signal SIGSEGV'

retry_on_corruption() {
  local attempt=1 max="${RETRY_MAX_ATTEMPTS:-3}"
  local log; log="$(mktemp)"
  # stdout streams straight to the caller (preserves interleaving with the job log); stderr is
  # captured for signature matching, then echoed back so nothing is lost from the logs.
  until "$@" 2>"$log"; do
    cat "$log" >&2
    if [ "$attempt" -ge "$max" ] || ! grep -qE "$corruption_re" "$log"; then
      rm -f "$log"; return 1
    fi
    echo "::warning::Go toolchain/cache corruption signature detected (attempt ${attempt}/${max}); clearing caches and retrying"
    # Build cache is rebuildable offline (drop every retry); wipe the module cache only on
    # on-disk archive corruption, since refetching it needs network.
    go clean -cache || true
    if grep -qE 'zip: checksum error|not the start of an archive file' "$log"; then
      go clean -modcache || true
    fi
    attempt=$((attempt + 1))
  done
  cat "$log" >&2
  rm -f "$log"
}

retry_on_corruption_to_file() {
  local outfile="$1"; shift
  local attempt=1 max="${RETRY_MAX_ATTEMPTS:-3}"
  local log; log="$(mktemp)"
  # Redirecting stdout to "$outfile" here (inside the loop condition) reopens and truncates it on
  # every attempt, so a failed first attempt never leaves partial output for a retry to append to.
  until "$@" >"$outfile" 2>"$log"; do
    cat "$log" >&2
    if [ "$attempt" -ge "$max" ] || ! grep -qE "$corruption_re" "$log"; then
      rm -f "$log"; return 1
    fi
    echo "::warning::Go toolchain/cache corruption signature detected (attempt ${attempt}/${max}); clearing caches and retrying"
    go clean -cache || true
    if grep -qE 'zip: checksum error|not the start of an archive file' "$log"; then
      go clean -modcache || true
    fi
    attempt=$((attempt + 1))
  done
  cat "$log" >&2
  rm -f "$log"
}
