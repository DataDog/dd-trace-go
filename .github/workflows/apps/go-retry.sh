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
# Both caches are wiped on every retry, unconditionally: a crash that corrupts an on-disk module
# archive can itself surface as an unrelated symptom (e.g. a SIGSEGV mid-extraction) on the
# attempt that causes the corruption, with the tell-tale "zip: checksum error" only appearing on
# a later attempt once something finally tries to read the already-corrupted file. Gating the
# modcache wipe on the current attempt's log matching an archive-error string missed that case —
# module extraction crashed silently by the affected package in one job, then failed all
# remaining retries because none of them cleared the modcache.
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
    # Both caches are rebuildable from network/source, so wipe them unconditionally on every
    # retry rather than gating the modcache wipe on this attempt's log (see file header).
    go clean -cache || true
    go clean -modcache || true
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
    go clean -modcache || true
    attempt=$((attempt + 1))
  done
  cat "$log" >&2
  rm -f "$log"
}
