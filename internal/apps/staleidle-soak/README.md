# staleidle-soak — APMS-19533 end-to-end verification harness

This harness exists to verify, against a real Datadog Agent over a real UDS
socket, that the [APMS-19533](https://datadoghq.atlassian.net/browse/APMS-19533)
fix in `ddtrace/tracer/transport.go` recovers from the customer's failure mode:
the trace-agent silently drops an idle keep-alive UDS connection, and the
tracer's next request on that pooled connection fails with `write: broken pipe`
or `read: connection reset by peer`.

The fix is covered by a unit test (`TestUDSTransportRecoversFromStaleIdleConn`
in `ddtrace/tracer/transport_test.go`) that runs against a synthetic
close-every-conn server. This harness is the next-layer-down check: same fix,
but driven against the customer's exact agent version (`datadog/agent:7.77.1`)
with the customer's exact transport (UDS) under stress and over time.

## What it spins up

```
┌──────────────┐      ┌────────────────┐      ┌────────────────────┐
│ load driver  │ ───▶ │ staleidle-     │ ───▶ │ datadog-agent      │
│ (this app's  │      │   proxy        │      │ :7.77.1 in docker  │
│ main.go,     │      │ (B.2/B.3 only) │      │ DD_APM_RECEIVER_   │
│ on host)     │      │                │      │   SOCKET=/var/...  │
└──────────────┘      └────────────────┘      └────────────────────┘
       │                                              │
       └──── UDS at /tmp/staleidle-uds/apm.socket ────┘
                  (or /tmp/staleidle-proxy/apm.socket for B.2/B.3)
```

- **`compose.yml`** — pins `datadog/agent:7.77.1` (the customer's version),
  receiver socket bind-mounted to the host so the host-side driver can
  connect to it, no real Datadog account needed (the receiver layer is what
  we're exercising — shipping downstream is irrelevant).
- **`main.go`** — load driver: creates spans concurrently, uses
  `tracer.WithUDS(...)` to point at the agent's socket, captures both
  tracer-side statsd metrics (via `tracer.WithDogstatsdAddr` pointed at a
  local UDP listener) and tracer-side error log lines (via
  `tracer.WithLogger`). Emits one JSON line of results.
- **`proxy/main.go`** — staleidle-proxy: a UDS pass-through that injects the
  failure mode. After each upstream HTTP response, it tears down both ends of
  the connection abruptly so the tracer's persistConn goes back to the idle
  pool looking healthy but is actually dead. The next reuse from the pool
  surfaces the customer's exact error shape.
- **`soak.sh`** — orchestrator: brings up the agent, optionally starts the
  proxy, builds and runs the driver, tears everything down, dumps a JSON
  result.

## Scenarios

| | What | Duration | Closest customer-side analog |
|---|---|---|---|
| **B.1** | Tracer ↔ Agent direct, steady load | 60s | Healthy steady state |
| **B.2** | Tracer ↔ proxy ↔ Agent. Proxy closes each conn after 1 upstream response | 60s | Agent silently killing idle conns under backpressure |
| **B.3** | Same as B.2 but longer | 300s (5 min) | Sustained pressure over time |
| **B.4** | Tracer ↔ proxy ↔ Agent. Proxy (`--mode hang`) accepts the request but never responds | 60s | Hung / unresponsive agent |

**B.4 is a boundary scenario, not a pass/fail test.** It produces `context
deadline exceeded` (HTTP client timeout) rather than a connection teardown.
The fix intentionally does **not** retry timeouts — retrying a request against
a wedged agent only stacks load — so B.4 drops traces on *both* baseline and
patched. It exists to document the edge of what the fix covers: connection
teardown (B.2/B.3) is recovered; agent unresponsiveness is not, and is a
distinct agent-side concern. The customer's reported errors (APMS-19533) are
all teardown, none are timeouts.

## Running locally

```sh
# from internal/apps/staleidle-soak/

# B.1 + B.2 + B.3 (+ optional B.4 boundary) against the current worktree's tracer
bash soak.sh B.1 patched
bash soak.sh B.2 patched
bash soak.sh B.3 patched
bash soak.sh B.4 patched   # boundary: hung-agent timeouts (see Scenarios)

# Results land in ./results/<scenario>_<label>.json
jq . results/B.2_patched.json
```

For A/B comparison against the pre-fix baseline:

```sh
git worktree add -f /tmp/baseline-worktree 633e55f821fc45d2d6a866a4c1961d831da19a84
cp -r internal/apps/staleidle-soak /tmp/baseline-worktree/internal/apps/

cd /tmp/baseline-worktree/internal/apps/staleidle-soak
bash soak.sh B.1 baseline
bash soak.sh B.2 baseline
bash soak.sh B.3 baseline

# Diff
diff <(jq -S '{spans_created, flush_traces, traces_dropped, api_errors, lost_trace_log_count, send_stats_err_log_count}' /tmp/baseline-worktree/internal/apps/staleidle-soak/results/B.2_baseline.json) \
     <(jq -S '{spans_created, flush_traces, traces_dropped, api_errors, lost_trace_log_count, send_stats_err_log_count}' .../staleidle-soak/results/B.2_patched.json)
```

## Pass criteria

For each scenario, `patched` must satisfy all of:

- `api_errors == 0` (in B.2/B.3 baseline this is `>> 0`)
- `lost_trace_log_count == 0`
- `send_stats_err_log_count == 0`
- `traces_dropped == 0`
- `spans_created == flush_traces` (no payload loss)

If any of the above fail with the proxy injection in place, the fix has a gap.
The driver dumps `_all_counters` (full statsd snapshot) and `all_error_logs`
(every ERROR-level log line) into the JSON so the operator has enough signal
to debug.

## Why both Layer A (unit test) and this harness?

Layer A is fast, deterministic, and catches the bug in CI. But it runs against
a Go-stdlib HTTP server with hand-rolled close behavior — not the real agent.
This harness is the answer to "what if `datadog/agent:7.77.1` does something
weirder than my synthetic server simulates?" It runs the same fix against the
real receiver code path the customer hits, plus exercises sustained load over
minutes (Layer A is sub-second).

If both Layer A and this harness are green, the next layer is asking the
customer to canary the build (Layer C).

## Cost and overhead

A full A/B run (3 scenarios × 2 branches) takes ~15 minutes wall clock. The
docker-compose agent uses ~50 MB of memory and one CPU core. The driver itself
is single-process Go and is negligible.

No real Datadog account is touched.
