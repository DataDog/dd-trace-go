# shellcheck shell=bash
# go-retry.sh — retry a command when its output shows Go toolchain/cache corruption.
#
# go1.26.5 (currently `stable`) intermittently corrupts its own heap mid-build (fatal errors,
# faults, SIGSEGVs, frontend ICEs — golang/go#77168) and a crash can leave partially written
# cache/module archives that later surface as zip/archive checksum errors, e.g. a
# "zip: checksum error" during `go install`/`go get`/`go build`. These are toolchain flakes, not
# real breaks, so retry a few times and let a fresh process dodge the probabilistic crash. Fail
# fast on anything else — e.g. govulncheck exit 3 (vulnerabilities found) never matches the
# signature and so is never retried. Shared by smoke-tests.yml and govulncheck*.{yml,sh}.
#
# The build cache is always rebuildable offline, so it is dropped on every retry to discard
# partial artifacts a crashed compiler left behind. The module cache is only wiped when the log
# matches an on-disk archive-corruption signature ("zip: checksum error" / "not the start of an
# archive file"): refetching modules needs network, so we don't pay that cost for a bare
# SIGSEGV/ICE that never touched the module cache.
#
# Usage:
#   source go-retry.sh
#   retry_on_corruption <cmd> [args...]                  # output streams to the caller as-is
#   retry_on_corruption_to_file <outfile> <cmd> [args...] # <cmd>'s stdout is captured to <outfile>
corruption_re='internal compiler error|zip: checksum error|not the start of an archive file|found pointer to free object|fatal error: fault|unexpected signal during runtime execution|signal SIGSEGV'
archive_corruption_re='zip: checksum error|not the start of an archive file'

_clean_caches_on_retry() {
  local log="$1"
  go clean -cache || true
  if grep -qE "$archive_corruption_re" "$log"; then
    go clean -modcache || true
  fi
}

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
    _clean_caches_on_retry "$log"
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
    _clean_caches_on_retry "$log"
    attempt=$((attempt + 1))
  done
  cat "$log" >&2
  rm -f "$log"
}
