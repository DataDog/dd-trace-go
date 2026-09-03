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

Same app, no dump mode, no local agent — the telemetry client falls back to the direct (agentless)
intake when the agent proxy is unreachable and an API key is present:

```sh
export DD_TELEMETRY_HEARTBEAT_INTERVAL=2
dd-auth -- bash -c '
  go run ./telemetry-errors -http localhost:8080 &
  APP_PID=$!
  sleep 1
  curl -s localhost:8080/decision-maker
  sleep 3
  kill $APP_PID
'
```

Datadog employees should prefer `dd-auth` over setting `DD_API_KEY` manually, exactly as recommended in
[`internal/apps/README.md`](../README.md). Never pass `dd-auth`'s `--output` or `-v` flags in a
scripted or logged context — both print live credentials (`-v`'s own help says "will print secrets!").
The wrapper form above keeps the key confined to the child process's environment.

`dd-auth`'s default target is Datadog's own production org, so this tier and tier 2 below read from the
same place the tracer just wrote to — no separate staging round-trip needed. Confirm the intake accepted
the payload by exit code / absence of a `WriterStatusCodeError` in the app's stderr; a non-2xx response
surfaces there.

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

**A 2xx from the intake does not guarantee a searchable record.** Confirmed empirically against a real
org: three separate reports from the same trigger, each independently confirmed accepted by the intake
(tier 1 — a successful flush with a non-zero byte count and no transport error), never became findable
by tier 2 within a 25-minute window — not by exact tag match, not by an unscoped full-text search on the
literal message. That absence is itself evidence, not just an unlucky timing window: an unrelated,
unscoped full-text search performed minutes later found an unrelated record from within that same
window, ruling out plain indexing lag as the explanation. So tier 1 passing is **not sufficient** to
conclude tier 2 will pass — run tier 2 for real before treating a `ReportError` adoption as confirmed
end-to-end, and if it comes up empty after a generous wait, treat that as a genuine product-side finding
to file (see "Known gaps" below), not a harness bug to retry past.

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

Endpoints in this app and the call site each one exercises:

| Endpoint | Call site | Reachability |
|---|---|---|
| `/decision-maker` | `parseDecisionMaker` (`ddtrace/tracer/propagating_tags.go`) | `http-triggerable` — malformed `_dd.p.dm` value on an inbound `x-datadog-tags` header |

Adopted call sites with no trigger yet in this app (reported gaps, not silent passes):

| Call site | Reachability | Why untriggered |
|---|---|---|
| `tracer.go` `storeConfig` (x2) | `not-triggerable` | Fires on internal config marshaling failure; not reachable from outside the process |
| `remoteconfig.go` `updateState` (x4) | `fault-injectable` | Reachable via a malformed remote-config poll response; no trigger endpoint implemented yet |

## Known gaps

- **Intake acceptance vs. searchability** (see "A 2xx from the intake does not guarantee a searchable
  record" above): a report can be confirmed accepted by tier 1 and still never surface at tier 2, with
  no error returned to the sender either way. Root cause unconfirmed — worth re-testing against a
  released tracer build rather than a development version, since every comparison record found during
  scoping for this process carried a released version string.
- **Coarse error grouping** (see "Do not assert 'a new product-side issue appeared'" above): confirmed
  for at least one other language's telemetry error feed, grouping was coarse enough that a large volume
  of structurally different errors collapsed into a single grouped issue. Whether this affects Go's feed
  specifically was not established from this app alone.

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
