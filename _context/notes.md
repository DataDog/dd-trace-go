## 2026-04-15

### Status

Core implementation of `DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT` is complete and verified.
Remaining work: unit tests in `ddtrace/tracer/textmap_test.go`.

**Done:**
- `continue` (default): existing behavior, no changes needed
- `restart`: verified via echotrace — fresh trace-id, no parent, one span link with
  `reason=propagation_behavior_extract` and `context_headers=datadog`, baggage propagated
- `ignore`: verified via echotrace — fresh trace-id, no parent, no span links, baggage
  dropped. Returns `nil, nil` (not an error — same as receiving no headers at all)
- Telemetry: `trace_propagation_behavior_extract` now reported at startup alongside the
  existing propagation style keys
- `supported_configurations.json`: fixed type/default, removed accidental `DD_TEST_*`
  entries, regenerated `.gen.go`

**Next:** ~~unit tests in `ddtrace/tracer/textmap_test.go`~~ done — see unit tests section below

---


### Why telemetry?

Other config values in `chainedPropagator` (injection/extraction style names) are already
reported at startup via `startTelemetry()` in `telemetry.go`. The key
`"trace_propagation_behavior_extract"` follows the same pattern so the backend can observe
which mode customers are using. This is the RFC's "Telemetry Key:
DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT" requirement.

See `ddtrace/tracer/telemetry.go`, the `if chained, ok := c.propagator.(*chainedPropagator)`
block where `trace_propagation_style_inject` and `trace_propagation_style_extract` are
already reported.

### nil, nil on ignore — real-life behavior and rationale

**How a user uses it:** Set `DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT=ignore` on a service.
Every incoming HTTP request — regardless of what trace headers the caller sends — produces
a fresh root span. No parent relationship, no span links, no baggage. The service starts
its own independent trace as if it had never received distributed tracing headers. This is
the option for a service that doesn't want to be adopted into an upstream org's trace.

**Call path through echotrace/httptrace:**
1. `echotrace.Middleware` → `httptrace.StartRequestSpan`
2. `StartRequestSpan` calls `tracer.Extract(HTTPHeadersCarrier(r.Header))` →
   returns `nil, nil`
3. Condition `extractErr == nil && parentCtx != nil` is false — `ChildOf` is never set,
   span starts as a root ✓
4. `parentCtx.ForeachBaggageItem(...)` is called unconditionally but `ForeachBaggageItem`
   has a nil-receiver guard (`if c == nil { return }`) — safe ✓

**Why `nil, nil` and not `nil, ErrSpanContextNotFound`:** returning an error would trigger
misleading debug log "failed to extract span context" in `StartSpanFromPropagatedContext`,
implying something went wrong. `nil, nil` is the clean signal: no context, no problem.
Verified via `TestPropagationBehaviorExtract/ignore` in echotrace.

### Unit tests (`ddtrace/tracer/textmap_test.go::TestPropagationBehaviorExtract`)

5 sub-tests covering the RFC's 4 configurations, all passing:

- `continue/same-trace-id`: trace continued from DD context, no span links, baggage propagated
- `continue/different-trace-ids`: trace continued from DD context, one `terminated_context`
  span link for the conflicting W3C context, baggage propagated
- `restart`: zero trace-id (`baggageOnly=true`), one `propagation_behavior_extract` span link
  pointing at the DD context. Tracestate is `dd=s:1;p:<spanID>` (Datadog propagator enriches
  it with the parent span ID sub-key). Flags=1 (priority > 0). Baggage propagated.
- `restart/extract-first`: same as restart but extraction stops after the Datadog propagator,
  so tracestate is empty (Datadog headers carry no W3C tracestate). **Baggage is nil** — this
  is a pre-existing limitation of `DD_TRACE_PROPAGATION_EXTRACT_FIRST`: it returns before the
  baggage propagator runs, so baggage is lost regardless of the restart mode. Documented in
  the test comment.
- `ignore`: returns `nil, nil` — asserted as `sctx == nil, err == nil`

### extractFirst + restart interaction

Covered explicitly in the RFC test matrix (configuration 3:
`DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT=restart` + `DD_TRACE_PROPAGATION_EXTRACT_FIRST=true`).
`extractIncomingSpanContext` returns after the first successful extraction, then `Extract()`
applies the restart behavior to that single context — so exactly one span link is created
and no conflicting-trace span links appear. Verified against .NET implementation.

### supported_configurations.json

`DD_TEST_BOOL_ENV`, `DD_TEST_FLOAT_ENV`, `DD_TEST_INT_ENV`, `DD_TEST_STRING_ENV` were
accidentally added to the JSON. Removed. They still appear in `.gen.go` because the
generator also scans Go source for `internal.*Env()` calls — that is correct behavior.
`DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT` fixed to `type: "String", default: "continue"`.
