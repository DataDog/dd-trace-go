# shellcheck shell=bash
# go-retry.sh — retry a command when its output shows Go toolchain/cache corruption.
#
# The build cache is always rebuildable offline, so it is dropped on every retry to discard
# partial artifacts a crashed compiler left behind. The module cache is wiped more selectively,
# because refetching it needs the network: a crash (SIGSEGV/ICE/fatal fault) can silently corrupt
# an on-disk module archive during extraction, with the tell-tale "zip: checksum error" only
# appearing on a *later* attempt once something finally tries to read the already-corrupted file —
# so gating the wipe purely on the current attempt's log would miss that case. Archive-corruption
# signatures always trigger a modcache wipe (self-limiting: a clean re-download stops the errors,
# so it never accumulates against the budget below). Any other corruption signature
# (SIGSEGV/ICE/fatal fault) also wipes the module cache defensively, but only up to
# GO_RETRY_MAX_MODCACHE_WIPES times per run, since most such crashes never touch GOMODCACHE and a
# false alarm shouldn't be free to trigger unbounded re-downloads.
#
# Both functions are re-entrancy-safe: if a caller wraps a function that itself calls
# retry_on_corruption/retry_on_corruption_to_file (directly or a few frames down), the inner call
# detects it's nested (via _go_retry_active) and just runs the command once, letting the
# outermost active call own all retrying and cache-clearing. Without this, a corrupted build
# retried at both levels re-runs already-succeeded sibling work on top of the inner retries, and
# each level's cache wipe compounds the other's — see the go-get-u smoke job incident where a
# single corrupted contrib module caused an 8m chunk to run 43+ minutes.
#
# Across a whole process (e.g. one retry_on_corruption call per contrib module in a loop),
# *non-archive-signature* modcache wipes are additionally capped at GO_RETRY_MAX_MODCACHE_WIPES
# (default 2): one wipe already fixes corruption for every subsequent call in the same run, so
# further independent hits from crashes that never touched the module cache don't each pay for a
# full, network-bound re-download. Archive-signature wipes are uncounted, since they're proven
# on-disk corruption rather than a defensive guess.
#
# A second, independent signature class covers *network* failures talking to the Go module proxy
# (proxy.golang.org / sum.golang.org). These are transient and must be handled differently from
# corruption: the module cache is deliberately left alone, because refetching it is exactly what
# failed. Wiping it would multiply the traffic that caused the failure in the first place.
# The dominant observed shape is an HTTP/2 stream reset ("stream error: stream ID N;
# INTERNAL_ERROR"), which is neither a 5xx nor a 404/410 -- so a status-code-only pattern misses it,
# and a comma-separated GOPROXY offers no fallback for it either (see go help goproxy: only 404/410
# fall through on ',', any error falls through on '|'). cmd/go performs no retries of its own
# (golang/go#28194), so this is the only retry layer there is.
# Network retries back off exponentially with jitter: a large matrix retrying in lockstep would
# re-create the load spike that triggered the reset. The pattern is deliberately not host-scoped
# everywhere: a bare status code or reset can land on a log line separate from the URL, and a false
# match here only costs one extra retry (bounded by RETRY_MAX_ATTEMPTS), not a wrongly-skipped cache
# wipe. Note that a failed module download can also surface later as a compile error
# (e.g. `undefined: echo`) rather than a network error, so a download failure that reaches max
# attempts may still present that way downstream.
#
# Usage:
#   source go-retry.sh
#   retry_on_corruption <cmd> [args...]                  # output streams to the caller as-is
#   retry_on_corruption_to_file <outfile> <cmd> [args...] # <cmd>'s stdout is captured to <outfile>
corruption_re='internal compiler error|zip: checksum error|not the start of an archive file|found pointer to free object|fatal error: fault|unexpected signal during runtime execution|signal SIGSEGV'
archive_corruption_re='zip: checksum error|not the start of an archive file'
# Transient module-proxy/network failures. Ordered roughly by observed frequency in this repo.
network_re='stream error.*(INTERNAL_ERROR|PROTOCOL_ERROR|REFUSED_STREAM)|(proxy|sum)\.golang\.org[^[:space:]]*: [45][0-9][0-9]|(proxy|sum)\.golang\.org.*(i/o timeout|TLS handshake|connection reset|unexpected EOF|no such host|server misbehaving)|google\.com/sorry|dial tcp.*(i/o timeout|connection refused|connection reset)|(Get|reading) "?https://(proxy|sum)\.golang\.org|INTERNAL_ERROR|502 Bad Gateway|500 Internal Server Error|429 Too Many Requests|connection reset by peer'

: "${GO_RETRY_MAX_MODCACHE_WIPES:=2}"
: "${GO_RETRY_NETWORK_BACKOFF_BASE:=5}"
_go_retry_modcache_wipes="${_go_retry_modcache_wipes:-0}"
_go_retry_active="${_go_retry_active:-0}"

_clean_caches_on_retry() {
  local log="$1"
  go clean -cache || true
  if grep -qE "$archive_corruption_re" "$log"; then
    # Proven on-disk corruption: always wipe, uncounted against the budget below.
    go clean -modcache || true
  elif [ "$_go_retry_modcache_wipes" -lt "$GO_RETRY_MAX_MODCACHE_WIPES" ]; then
    # Some other corruption signature (SIGSEGV/ICE/fatal fault): the crash may have silently
    # corrupted the module cache without an archive-error line surfacing yet, so wipe
    # defensively — but charge it against the budget since most such crashes never touch
    # GOMODCACHE at all.
    go clean -modcache || true
    _go_retry_modcache_wipes=$((_go_retry_modcache_wipes + 1))
  else
    echo "::warning::modcache wipe budget (${GO_RETRY_MAX_MODCACHE_WIPES}) already used this run; retrying without wiping it again"
  fi
}

# _go_retry_handle_failure <log> <attempt> <max>
# Decides whether the just-failed attempt is worth retrying and, if so, performs the remediation
# appropriate to *why* it failed. Returns 0 to retry, 1 to give up. Shared by both entry points so
# the two can't drift apart.
#
# Corruption is checked first: a log carrying both signatures has proven on-disk damage, and that
# needs the cache wipe regardless of whatever network noise accompanied it.
_go_retry_handle_failure() {
  local log="$1" attempt="$2" max="$3"

  if grep -qE "$corruption_re" "$log"; then
    [ "$attempt" -ge "$max" ] && return 1
    echo "::warning::Go toolchain/cache corruption signature detected (attempt ${attempt}/${max}); clearing caches and retrying"
    _clean_caches_on_retry "$log"
    return 0
  fi

  if grep -qE "$network_re" "$log"; then
    [ "$attempt" -ge "$max" ] && return 1
    # GOPROXY_FAILURE is a stable token: CI dashboards count these to size module-proxy flakiness.
    echo "::warning::GOPROXY_FAILURE transient module-proxy/network failure (attempt ${attempt}/${max}); backing off and retrying"
    # Exponential backoff with jitter, and no cache clearing: the fetch is what failed, so the
    # bytes already on disk are the one thing worth keeping.
    local delay=$((GO_RETRY_NETWORK_BACKOFF_BASE * (1 << (attempt - 1))))
    sleep "$((delay + RANDOM % (delay + 1)))"
    return 0
  fi

  return 1
}

retry_on_corruption() {
  if [ "$_go_retry_active" = "1" ]; then
    "$@"
    return
  fi
  _go_retry_active=1
  local attempt=1 max="${RETRY_MAX_ATTEMPTS:-3}"
  local log; log="$(mktemp)"
  # stdout streams straight to the caller (preserves interleaving with the job log); stderr is
  # captured for signature matching, then echoed back so nothing is lost from the logs.
  until "$@" 2>"$log"; do
    cat "$log" >&2
    if ! _go_retry_handle_failure "$log" "$attempt" "$max"; then
      rm -f "$log"; _go_retry_active=0; return 1
    fi
    attempt=$((attempt + 1))
  done
  cat "$log" >&2
  rm -f "$log"
  _go_retry_active=0
}

retry_on_corruption_to_file() {
  if [ "$_go_retry_active" = "1" ]; then
    local outfile="$1"; shift
    "$@" >"$outfile"
    return
  fi
  _go_retry_active=1
  local outfile="$1"; shift
  local attempt=1 max="${RETRY_MAX_ATTEMPTS:-3}"
  local log; log="$(mktemp)"
  # Redirecting stdout to "$outfile" here (inside the loop condition) reopens and truncates it on
  # every attempt, so a failed first attempt never leaves partial output for a retry to append to.
  until "$@" >"$outfile" 2>"$log"; do
    cat "$log" >&2
    if ! _go_retry_handle_failure "$log" "$attempt" "$max"; then
      rm -f "$log"; _go_retry_active=0; return 1
    fi
    attempt=$((attempt + 1))
  done
  cat "$log" >&2
  rm -f "$log"
  _go_retry_active=0
}
