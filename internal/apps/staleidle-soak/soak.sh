#!/usr/bin/env bash
# APMS-19533 stale-idle UDS soak orchestrator.
#
# Usage:
#   ./soak.sh <scenario> <label>
#
# Scenarios:
#   B.1  steady-state, 60s, no stale-conn injection
#   B.2  60s, stale-conn proxy in front of the agent (close after each response)
#   B.3  300s sustained soak, same proxy
#
# Label is free-form (typically "patched" or "baseline") and is written into
# the result JSON so the A/B diff can find both halves.
#
# Outputs:
#   results/<scenario>_<label>.json    driver JSON (single line)
#   results/<scenario>_<label>.agent.log  agent stdout/stderr for the run

set -euo pipefail

# Some Datadog dev environments set OTEL_* env vars (Claude Code workspaces,
# certain CI runners). The tracer inside the driver respects those by
# switching to OTLP export mode, which silently sends traces to a non-existent
# endpoint and invalidates the run. Unset them here as belt-and-suspenders;
# main.go does the same.
unset OTEL_TRACES_EXPORTER OTEL_METRICS_EXPORTER OTEL_LOGS_EXPORTER
unset OTEL_EXPORTER_OTLP_ENDPOINT OTEL_EXPORTER_OTLP_PROTOCOL
unset OTEL_EXPORTER_OTLP_TRACES_ENDPOINT OTEL_EXPORTER_OTLP_METRICS_ENDPOINT OTEL_EXPORTER_OTLP_LOGS_ENDPOINT
unset OTEL_METRICS_INCLUDE_VERSION OTEL_METRICS_TEMPORALITY_PREFERENCE
unset OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE

SCENARIO="${1:?usage: ./soak.sh <scenario> <label>}"
LABEL="${2:?usage: ./soak.sh <scenario> <label>}"

PROXY_MODE="close"
case "$SCENARIO" in
  # B.1: steady-state direct-to-agent UDS load. Happy-path sanity for the fix.
  B.1) DURATION_S=60;  USE_STALE_PROXY=0; CLOSE_AFTER_RESP=0 ;;
  # B.2: tracer talks to the staleidle-proxy, which abruptly closes both ends
  # of the UDS connection after each upstream HTTP response. The result for
  # the tracer is: its persistConn goes back to the idle pool looking healthy,
  # but the next flush hits a torn-down conn and surfaces EPIPE / ECONNRESET
  # — exactly the customer's failure mode (APMS-19533).
  B.2) DURATION_S=60;  USE_STALE_PROXY=1; CLOSE_AFTER_RESP=1 ;;
  # B.3: same injector, longer soak. Catches retry stacking / metric drift
  # over time.
  B.3) DURATION_S=300; USE_STALE_PROXY=1; CLOSE_AFTER_RESP=1 ;;
  # B.4: BOUNDARY scenario. The proxy accepts the request but never responds,
  # simulating a hung/overloaded agent. The tracer times out
  # (`context deadline exceeded`) — a NON-teardown error that the fix
  # deliberately does NOT retry. This characterizes the edge of what the fix
  # covers; we expect BOTH baseline and patched to drop here, confirming the
  # fix targets connection-teardown specifically (the customer's observed
  # error family) and not agent unresponsiveness.
  B.4) DURATION_S=60;  USE_STALE_PROXY=1; CLOSE_AFTER_RESP=0; PROXY_MODE="hang" ;;
  *) echo "unknown scenario: $SCENARIO (want B.1 / B.2 / B.3 / B.4)" >&2; exit 2 ;;
esac

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$HERE/../../.." && pwd)"
RESULTS_DIR="$HERE/results"
UDS_DIR="${SOAK_UDS_DIR:-/tmp/staleidle-uds}"
AGENT_SOCKET="$UDS_DIR/apm.socket"
# When the stale-conn proxy is enabled the tracer connects through socat,
# which forwards to the real agent UDS but closes any conn idle longer than
# PROXY_IDLE_MS ms. The tracer's view of "the agent" is then the proxy
# socket, not the agent socket directly.
PROXY_DIR="${SOAK_PROXY_DIR:-/tmp/staleidle-proxy}"
PROXY_SOCKET="$PROXY_DIR/apm.socket"
DRIVER_BIN="${TMPDIR:-/tmp}/staleidle-soak-driver-$$"
PROXY_BIN="${TMPDIR:-/tmp}/staleidle-proxy-$$"

if [ "$USE_STALE_PROXY" = "1" ]; then
  TRACER_SOCKET="$PROXY_SOCKET"
else
  TRACER_SOCKET="$AGENT_SOCKET"
fi

JSON_OUT="$RESULTS_DIR/${SCENARIO}_${LABEL}.json"
AGENT_LOG="$RESULTS_DIR/${SCENARIO}_${LABEL}.agent.log"

mkdir -p "$RESULTS_DIR"
: > "$AGENT_LOG"

cleanup() {
  if [ -n "${PROXY_PID:-}" ]; then
    echo "[soak] stopping stale-conn proxy..."
    kill "$PROXY_PID" 2>/dev/null || true
    wait "$PROXY_PID" 2>/dev/null || true
  fi
  echo "[soak] tearing down agent..."
  ( cd "$HERE" && SOAK_UDS_DIR="$UDS_DIR" docker compose -f compose.yml down --remove-orphans 2>&1 | sed 's/^/[soak] /' ) || true
  rm -f "$DRIVER_BIN" "$PROXY_BIN"
  rm -rf "$UDS_DIR" "$PROXY_DIR"
}
trap cleanup EXIT

echo "[soak] scenario=$SCENARIO label=$LABEL duration=${DURATION_S}s stale_proxy=${USE_STALE_PROXY} close_after_resp=${CLOSE_AFTER_RESP}"
echo "[soak] agent uds dir: $UDS_DIR"
[ "$USE_STALE_PROXY" = "1" ] && echo "[soak] proxy uds dir: $PROXY_DIR"

# Fresh UDS directories each run so leftover sockets don't cause races.
rm -rf "$UDS_DIR" "$PROXY_DIR"
mkdir -p "$UDS_DIR" "$PROXY_DIR"
chmod 777 "$UDS_DIR" "$PROXY_DIR"

echo "[soak] bringing up agent..."
( cd "$HERE" && SOAK_UDS_DIR="$UDS_DIR" docker compose -f compose.yml up -d 2>&1 | sed 's/^/[soak] /' )

echo "[soak] waiting for agent healthy..."
deadline=$(( $(date +%s) + 90 ))
until status=$(docker inspect staleidle-soak-agent --format '{{.State.Health.Status}}' 2>/dev/null); [ "$status" = "healthy" ]; do
  [ "$(date +%s)" -lt "$deadline" ] || { echo "[soak] agent never became healthy"; docker logs --tail 100 staleidle-soak-agent; exit 1; }
  sleep 2
done
echo "[soak] agent healthy at $(date -u +%H:%M:%S)"

echo "[soak] streaming agent logs to $AGENT_LOG"
( docker logs -f --since 0s staleidle-soak-agent >> "$AGENT_LOG" 2>&1 ) &
AGENT_LOG_PID=$!

echo "[soak] building driver (uses tracer from this worktree's go.mod)..."
( cd "$REPO_ROOT/internal/apps" && go build -o "$DRIVER_BIN" ./staleidle-soak )

if [ "$USE_STALE_PROXY" = "1" ]; then
  echo "[soak] building stale-conn proxy..."
  ( cd "$REPO_ROOT/internal/apps" && go build -o "$PROXY_BIN" ./staleidle-soak/proxy )
  echo "[soak] starting stale-conn proxy: $PROXY_SOCKET -> $AGENT_SOCKET (mode=${PROXY_MODE}, close after ${CLOSE_AFTER_RESP} response)"
  "$PROXY_BIN" \
    --listen "$PROXY_SOCKET" \
    --target "$AGENT_SOCKET" \
    --mode "$PROXY_MODE" \
    --close-after-resp "$CLOSE_AFTER_RESP" \
    >>"$AGENT_LOG" 2>&1 &
  PROXY_PID=$!
  for _ in $(seq 1 50); do
    [ -S "$PROXY_SOCKET" ] && break
    sleep 0.1
  done
  [ -S "$PROXY_SOCKET" ] || { echo "[soak] proxy socket never appeared"; exit 1; }
  echo "[soak] proxy ready at $PROXY_SOCKET"
fi

echo "[soak] running driver for ${DURATION_S}s (tracer points at $TRACER_SOCKET)..."
"$DRIVER_BIN" \
  --uds-path "$TRACER_SOCKET" \
  --duration "${DURATION_S}s" \
  --concurrency 50 \
  --spans-per-sec 100 \
  --label "$LABEL" \
  > "$JSON_OUT"

# Stop the log streamer.
kill "$AGENT_LOG_PID" 2>/dev/null || true
wait "$AGENT_LOG_PID" 2>/dev/null || true

echo "[soak] done. result:"
jq . "$JSON_OUT"
