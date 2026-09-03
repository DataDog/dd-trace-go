# telemetry-errors: dogfooding the SDK error-reporting API

This app exists to close one gap: adopting `ReportError`/`ReportPanic`/`LogAndReportError`/
`LogAndReportPanic` (see [`internal/README.md`](../../README.md)) at a call site is easy to get wrong in
ways no unit test catches, because `telemetrytest.RecordClient` discards `LogOption`s and record attrs —
it can see that `Log` was called, not what was actually sent. Before this app existed, confirming that an
adopted call site produces a correct wire payload and actually lands in the product meant a manual,
multi-minute loop: bring up `docker-compose` with an agent sidecar (`network_mode: host`, which does not
work on Docker Desktop for macOS), wait out a telemetry flush interval that starts at 60s, and hope the
process didn't exit before that flush happened — it doesn't flush on `Stop()` (dd-trace-go#5249). Then
open the UI and look.

This document describes a process that turns that loop into three fast, mostly-automatable tiers, and
the (human or agent) workflow for running it against a diff that touches these call sites.

## The three tiers

| Tier | Needs | Proves |
|---|---|---|
| **0 — wire shape** | nothing (no credentials, no network) | The payload the SDK *would* send is correct: message, error type, level, count, stack trace quality |
| **1 — real intake** | a Datadog API key | The production telemetry intake accepts the payload |
| **2 — product landing** | read access to the org that received it | The report is queryable and attributable in the product |

### Tier 0 — wire shape, offline

Use the payload-dump mode the CI Visibility / test-optimization path already provides — it is a general
switch, not CI-Visibility-specific, and it short-circuits before any HTTP call:

```sh
export DD_TELEMETRY_HEARTBEAT_INTERVAL=2      # collapses the flush interval to ~2s (see "Why 2s" below)
export DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES=1
export TEST_UNDECLARED_OUTPUTS_DIR=$(mktemp -d)

go run ./telemetry-errors -http localhost:8080 &
APP_PID=$!
sleep 1   # wait for "Listening on:"
curl -s localhost:8080/decision-maker
sleep 3   # wait past one flush tick
kill $APP_PID

ls "$TEST_UNDECLARED_OUTPUTS_DIR"/payloads/telemetry/
```

**Both env vars are required.** Setting one without the other makes every flush fail silently — nothing
is sent anywhere and no file is written. Each file is one JSON-encoded `transport.Body`; find the one
whose `request_type` is `logs` (or `message-batch` wrapping a `logs` payload) and inspect its
`payload.logs[].message`, `.stack_trace`, `.count`, and `.level`.

#### Why `DD_TELEMETRY_HEARTBEAT_INTERVAL=2`

The telemetry client's flush interval is adaptive between 15s and 60s, starting at the 60s end. Setting
`DD_TELEMETRY_HEARTBEAT_INTERVAL` lower forces the flush interval down to match it (the client keeps
`FlushInterval.Max <= HeartbeatInterval`), so a 2s heartbeat gets you a ~2s first flush instead of waiting
up to 60s. This sidesteps dd-trace-go#5249 (`tracer.Stop()` doesn't flush) entirely, rather than working
around it — the run simply survives long enough for a normal flush tick to fire before the process exits.

#### Automated checks, per trigger endpoint

- **`message_match`** — observed message starts with the trigger's declared constant
- **`error_type_present`** — `error.error_type=<type>` present and non-empty
- **`level == ERROR` and `count >= 1`**
- **`stack_points_at_call_site`** — the topmost non-telemetry-package frame is the declaring function,
  not telemetry client plumbing
- **`no_replay_frames`** — the stack trace contains no frames from the global-client replay path. A
  report made before `telemetry.StartApp` is queued and only reported once the client swaps in; its
  stack trace is then captured at *replay* time, pointing at telemetry internals instead of the actual
  bug (dd-trace-go#5250 documents the ordering issue that causes this). A trigger that fails this check
  needs to move later in the tracer's startup, not just be re-run.
- **`customer_frames_redacted`** — the harness app's own `main.*` frames read `REDACTED` in the stack
  trace, confirming that the redaction step that scrubs customer code before it ever leaves the process
  is actually working for this call site

A trigger failing any of these is a real defect in the adoption, not a flake — investigate before
re-running.

### Tier 1 — real intake

**Do not rely on "no local agent" being true.** The tracer's telemetry client tries an agent proxy
*before* falling back to the direct (agentless) intake, and it will happily use whatever agent answers
on the default port — including one you forgot was running, or one authenticated with a completely
different API key than the one you intend to test with. On a Datadog engineer's own machine this is the
common case, not the exception: a local agent for day-to-day development is often already running.
Confirmed empirically: with an unrelated local agent reachable on the default port, every "agentless"
send in this tier looked identical to success (2xx, non-zero bytes flushed, no error) while landing in
whatever org that agent's own key belongs to — never in the org actually being verified. That failure
mode produces no error anywhere in the chain; it has to be ruled out structurally, not detected.

The robust approach is to run an **isolated, verified agent** rather than trust ambient agentlessness:

```sh
dd-auth -- bash -c '
  docker run -d --name dogfood-agent \
    -e DD_API_KEY="$DD_API_KEY" -e DD_SITE=datadoghq.com \
    -e DD_APM_NON_LOCAL_TRAFFIC=true -e DD_HOSTNAME=dogfood-agent \
    -p 18126:8126 datadog/agent:latest
'
# wait for it to answer, e.g.: until curl -sf http://localhost:18126/info >/dev/null; do sleep 2; done

# confirm the key it is actually forwarding with (must NOT match any pre-existing local agent's key):
docker exec dogfood-agent agent status | grep -A2 "API Key ending"

export DD_TELEMETRY_HEARTBEAT_INTERVAL=2
export DD_TRACE_AGENT_URL=http://localhost:18126
go run ./telemetry-errors -http localhost:8080 &
APP_PID=$!
sleep 1
curl -s localhost:8080/decision-maker
sleep 8
kill $APP_PID

docker rm -f dogfood-agent
```

A non-default host port (`18126`, not `8126`) avoids colliding with anything already bound to the
default port — with `network_mode: host` (as used elsewhere in this directory) that collision would
either fail loudly (port already in use) or, worse, silently bind to whichever process won the race.
The explicit `docker exec ... agent status` key check is the actual guard: if its suffix matches a key
you didn't just mint, stop and investigate before trusting anything this tier reports.

Datadog employees should prefer `dd-auth` over setting `DD_API_KEY` manually, exactly as recommended in
[`internal/apps/README.md`](../README.md). Never pass `dd-auth`'s `--output` or `-v` flags in a
scripted or logged context — both print live credentials (`-v`'s own help says "will print secrets!").
The wrapper form above keeps the key confined to the child process's environment.

If you skip the container and rely on the direct intake instead, first confirm nothing is listening on
the default agent port at all (e.g. `lsof -iTCP:8126 -sTCP:LISTEN`) — otherwise this tier's "success"
tells you nothing about the org you think you're testing against.

`dd-auth`'s default target is Datadog's own production org, so this tier and tier 2 below read from the
same place the tracer just wrote to — no separate staging round-trip needed. Confirm the intake accepted
the payload by exit code / absence of a `WriterStatusCodeError` in the app's stderr; a non-2xx response
surfaces there. Absence of an error is necessary but not sufficient — it does not by itself prove which
org received the payload, per the above.

### Tier 2 — product landing

This is a query against the org's log/event search for the unique service name this run used, scoped to
records created after the run started (`run_started_at`, not a fuzzy time window — a stable `DD_SERVICE`
reused across runs would otherwise match a previous run's records). The exact index and field names are
intentionally not enumerated here (see "Scoping the query" below); they belong in whatever internal
runbook backs this process for the querying team, not in a public repository.

**Do not assert "a new product-side issue appeared".** For some languages, error-grouping produces very
coarse buckets — many structurally different errors can collapse into a single grouped issue. Whether
grouping is fine-grained enough to be a useful assertion is language- and product-version-dependent;
check empirically before relying on it, and treat it as a judgment call (see below), not a pass/fail gate.

**A tier-1 pass alone does not prove which org received the payload — run tier 2 for real.** Confirmed
the hard way: several reports from the same trigger, each independently confirmed accepted by the
intake (tier 1 — a successful flush with a non-zero byte count and no transport error), never became
findable by tier 2 at all. The cause was not the product: it was silent local-agent interception (see
"Do not rely on 'no local agent' being true" under Tier 1) — every one of those "accepted" sends had
gone to a different org than the one being searched, with no error anywhere to reveal it. Once run
through a verified, isolated agent, the identical payload became searchable within a couple of minutes.
The general lesson stands independent of that specific cause: a 2xx and a non-zero byte count only
prove the client-side send succeeded, never which org received it — always confirm tier 2 directly.

## Judged, not asserted (tier 2)

Some things about a landed report are a matter of engineering judgment, not a boolean check:

- Is the message distinct enough to stand alone? Only the **first** error type seen for a given
  `(message, level, tags)` combination per flush window is ever transmitted — attrs are not part of the
  dedup key. A message generic enough to be produced by two different bugs will silently hide the second.
- Is the reported error type actually informative, or a generic wrapper type (e.g. `*errors.errorString`
  from an `fmt.Errorf("...: %w", err)` wrap) that tells a maintainer nothing beyond "something failed
  here"? If the type is generic, the message and stack trace need to carry the real signal.
- Does the product-side stack trace, as rendered, actually point a maintainer at the code — or did
  something in symbolication or redaction obscure the real frame?

Record these as findings to act on, separately from the automated tier-0/tier-1 pass/fail result.

## Scoping the query

Whatever runs the tier-2 query — human or agent — should follow these rules regardless of which org or
index it targets:

- **Scope every query to the org and service that sent the report.** The index tracer telemetry lands in
  is typically a shared, cross-customer feed; an unscoped query can pull back other customers' data.
- **Request only the specific fields needed to confirm the report**, never a wildcard field selection —
  a shared telemetry feed can carry commercially sensitive account metadata on every record.
- **Give the run's identity an attributable, excludable shape** (e.g. a fixed prefix plus a stable
  per-machine identifier for `DD_SERVICE`/`DD_ENV`), so whoever owns that feed can filter this traffic
  out or trace it back to an owner.

## Agent-led workflow

Given a diff that adds or changes `ReportError`/`ReportPanic`/`LogAndReport*` call sites, the process to
run — by an agent or a person — is:

1. **Enumerate** the new/changed call sites from the diff.
2. **Classify** each by reachability:
   - `http-triggerable` — reachable from an inbound request (the `parseDecisionMaker` example below)
   - `fault-injectable` — reachable by feeding a component malformed input it processes on its own
     (e.g. a remote-config poll response)
   - `not-triggerable` — reachable only from in-process conditions this harness can't induce from the
     outside
   Also check each site against the four-point policy in [`internal/README.md`](../../README.md#telemetry)
   ("our defect, swallowed, not per-span, fires after telemetry has started").
3. **Require a trigger** in this app for every `http-triggerable` and `fault-injectable` site. A site
   with no trigger is a reported gap in this harness's coverage, not a silent pass — say so explicitly.
4. **Run tier 0**, then **tier 1**, and read the automated checks above.
5. **Run tier 2** and apply the judged checks above.
6. **Report** the outcome — e.g. update the originating PR's test plan with what was actually verified,
   and file separately any product-side finding that isn't specific to this one PR (a grouping gap, a
   redaction gap, etc.).

## Current coverage

HTTP-endpoint triggers in this app:

| Endpoint | Call site | Reachability |
|---|---|---|
| `/decision-maker` | `parseDecisionMaker` (`ddtrace/tracer/propagating_tags.go`) | `http-triggerable` — malformed `_dd.p.dm` value on an inbound `x-datadog-tags` header |

Non-endpoint triggers — these fire on a background cadence once the process is configured correctly,
with no HTTP call needed. See "Running this app" below for exact commands:

| Mechanism | Call site | Reachability |
|---|---|---|
| Fault-injecting reverse proxy in front of a real agent, returning malformed JSON for the remote-config poll endpoint | `updateState` "could not parse the json response body" (`internal/remoteconfig/remoteconfig.go`) | `fault-injectable` — confirmed working end-to-end (tiers 0/1/2) |
| Linux container with a custom seccomp profile blocking the `memfd_create` syscall, run with `--network host` (see "Known gaps") | `storeConfig`'s two sites (`ddtrace/tracer/tracer.go`) — both fire together, since the second one's own internal fallback also fails under Docker Desktop's Linux VM kernel | `fault-injectable`, Linux-only (both sites are no-ops on macOS/Windows) — confirmed at all three tiers |

Not practically triggerable from outside the process, given what each depends on:

| Call site | Why |
|---|---|
| `remoteconfig.go` `newUpdateRequest` erroring | Only fails on an already-corrupted internal repository state, not reachable via a crafted network response |
| `remoteconfig.go` `http.NewRequest` erroring | Only fails on a malformed agent URL, which tracer startup validates before this point is ever reached |
| `remoteconfig.go` "could not read the response body" | Needs a raw truncated-connection response (200 announced, then abrupt close mid-body) — plausible but not yet implemented here |

## Known gaps

- **Coarse error grouping** (see "Do not assert 'a new product-side issue appeared'" above): confirmed
  for at least one other language's telemetry error feed, grouping was coarse enough that a large volume
  of structurally different errors collapsed into a single grouped issue. Whether this affects Go's feed
  specifically was not established from this app alone.
- **Resolved — container-originated telemetry needs `network_mode: host`, not bridge networking with
  published ports.** Sending from a container over bridge networking (`-p hostport:containerport`, or a
  custom bridge network with container-name resolution) reproducibly failed to land at tier 2 — 2xx,
  non-zero bytes flushed, no error anywhere, yet never searchable — while native-process telemetry
  through the identical agent landed reliably every time. Enabling the agent's own debug logging pinned
  the mechanism precisely: a bridge-networked connection triggers an internal origin-resolution attempt
  (the agent tries to resolve the sender's identity via its cgroup, through an internal call to its own
  tag-resolution component) that fails outright for a throwaway container the agent has no way to
  recognize; this attempt is never made at all for a same-network-namespace connection. Confirmed the
  fix directly: re-running the exact same trigger with the sending container on `network_mode: host`
  (matching the topology this repo's own `docker-compose.yml` already uses) made the resolution attempt
  disappear entirely, and the payload landed within seconds. **Use `network_mode: host` for any
  container-based trigger** — it is both the fix and the existing convention, not a new requirement.

**Not a gap, but the pitfall that cost the most time building this process:** local-agent interception
(see "Do not rely on 'no local agent' being true" under Tier 1). It presents identically to a genuine
product-side "accepted but unsearchable" gap — 2xx, non-zero bytes flushed, no error anywhere — right up
until you verify which agent actually handled the send. Two things ruled out simpler explanations first
and are worth naming so they aren't re-litigated: search-indexing lag (ruled out — an unrelated query
resolved within ~2 minutes in the same session) and the tracer's own build version (ruled out via a
controlled rebuild with a released-looking version string). Neither was the cause. The actual cause was
found only by inspecting the intercepting agent's own reported destination and key.

## Running this app

This is a manual-only harness — deliberately not wired into
[`/.github/workflows/test-apps.cue`](../../../.github/workflows/test-apps.cue), so it never runs in CI
and nothing here can flake a build. See [`internal/apps/README.md`](../README.md) for the general
test-apps conventions (env vars, CI cost model, how to add an app). Run it directly with `go run` as
shown above, or through the shared harness:

```sh
export DD_API_KEY=<API KEY>
cd internal/apps
docker-compose run --build scenario 'telemetry-errors/v1$'
```

The `docker-compose` path additionally needs a reachable agent container and does not benefit from the
`DD_TELEMETRY_HEARTBEAT_INTERVAL` flush-speedup shown above unless that env var is passed through with
`-e`; prefer the direct `go run` invocations above for the fast local loop.

### Running the remote-config JSON-parse trigger

Build and run [`rcfaultproxy`](./rcfaultproxy), a fault-injecting reverse proxy, in front of a real,
verified agent from Tier 1 above. It intercepts only the remote-config poll endpoint; every other
request (notably `/info`, needed for the tracer to detect remote-config support at all, and
`/telemetry/proxy/*`) passes through untouched:

```sh
go build -o /tmp/rcfaultproxy ./telemetry-errors/rcfaultproxy
/tmp/rcfaultproxy -listen :20000 -upstream http://localhost:18126 &

export DD_TELEMETRY_HEARTBEAT_INTERVAL=2
export DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS=1   # optional, speeds up the first poll
export DD_TRACE_AGENT_URL=http://localhost:20000
go run ./telemetry-errors -http localhost:8080
# no curl needed — remote-config polls automatically once the tracer starts
```

### Running the memfd/OTel-process-context trigger (Linux only)

`storeConfig`'s two sites are no-ops on macOS/Windows (`internal/inmemoryfile.go` vs
`internal/inmemoryfilelinux.go`) — they only do real work, and can only fail, on Linux. Both fire
unconditionally at tracer startup, so no HTTP call is needed here either; letting the process run for a
few seconds is enough. To force the failure deterministically rather than relying on an already-broken
environment, block the underlying syscall with a minimal custom seccomp profile:

```sh
cat > no-memfd-seccomp.json <<'EOF'
{
  "defaultAction": "SCMP_ACT_ALLOW",
  "architectures": ["SCMP_ARCH_X86_64", "SCMP_ARCH_AARCH64"],
  "syscalls": [{"names": ["memfd_create"], "action": "SCMP_ACT_ERRNO"}]
}
EOF

CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/telemetry-errors-linux ./telemetry-errors

docker run --rm --network host --security-opt seccomp=./no-memfd-seccomp.json \
  -e DD_TRACE_AGENT_URL=http://localhost:18126 \
  -e DD_TELEMETRY_HEARTBEAT_INTERVAL=2 \
  -v /tmp/telemetry-errors-linux:/telemetry-errors-linux:ro \
  --entrypoint /telemetry-errors-linux \
  alpine:latest -http 127.0.0.1:8080
```

**Use `--network host`** for tier-2-verifiable runs — see "Known gaps" above; bridge networking with a
published port (`-p hostport:8080`) is fine for tiers 0/1 only, where landing doesn't matter.
