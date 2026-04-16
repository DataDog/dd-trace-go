## 2026-04-16

### Remaining work to pass system-tests

Source for all items below: `~/go/src/github.com/DataDog/system-tests/`

---

**1. Remove `missing_feature` markers from `manifests/golang.yml`**

File: `manifests/golang.yml` lines 1334–1337:
```
tests/test_library_conf.py::Test_ExtractBehavior_Default: missing_feature (baggage should be implemented and conflicting trace contexts should generate span link in v1.71.0)
tests/test_library_conf.py::Test_ExtractBehavior_Ignore: missing_feature (extract behavior not implemented)
tests/test_library_conf.py::Test_ExtractBehavior_Restart: missing_feature (extract behavior not implemented)
tests/test_library_conf.py::Test_ExtractBehavior_Restart_With_Extract_First: missing_feature (extract behavior not implemented)
```
These markers skip the tests for Go. They need to be removed in a PR to the system-tests
repo once the implementation is confirmed working.

---

**2. Outbound injection through `/make_distant_call` needs smoke-testing**

All four test classes call `/make_distant_call?url=http://weblog:7777/` and assert on
the *outbound* headers that the weblog sends to the downstream service
(`data["request_headers"][...]`). Source: `tests/test_library_conf.py`:

- `restart` (line 610–612): outbound `x-datadog-trace-id` must differ from `"1"`;
  `_dd.p.tid=1111111111111111` must NOT be in outbound tags; `key1=value1` must be
  in outbound `baggage`
- `ignore` (line 767–769): outbound `x-datadog-trace-id` != incoming; outbound
  `baggage` header must be absent entirely
- `restart+extract_first` (line 860–862): same as restart

This tests injection, not just extraction. The Go weblog `/make_distant_call` endpoint
exists and was confirmed by the MCP, but these assertions have not been run against the
actual system-test harness yet. Need CI to confirm.

---

**3. `restart` — same trace ID, different span IDs (`test_multiple_tracecontexts_with_overrides`)**

Source: `tests/test_library_conf.py` lines 667–718, `Test_ExtractBehavior_Restart`.

This test sends DD and W3C headers sharing the same trace ID (`1111111111111111...0001`)
but with *different* span IDs (DD=`1`, W3C=`0x1234567890123456`). The assertion at
line 708:
```python
assert int(link["spanID"]) == 1311768467284833366  # 0x1234567890123456
```
The span link's `spanID` is expected to be the **W3C span ID**, not the DD span ID.
This is the `overrideDatadogParentID` code path. The current `restart` implementation
builds the span link from `incomingCtx` which is whatever `extractIncomingSpanContext`
returns — need to verify that `overrideDatadogParentID` runs before `Extract()` applies
the restart behavior, and that the resulting `incomingCtx.SpanID()` is the W3C span ID.
Not yet verified with a unit test.

---

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
- `restart/extract-first`: extraction stops after the Datadog propagator (no conflicting
  W3C span link), tracestate is empty (Datadog headers carry no W3C tracestate), Flags=1.
  Baggage is propagated — see fix below.
- `ignore`: returns `nil, nil` — asserted as `sctx == nil, err == nil`

### Bug fix: baggage lost with extract-first

`extractIncomingSpanContext` returns immediately when `onlyExtractFirst=true` (after the
first non-baggage propagator succeeds), so the baggage propagator never runs inside the
loop. This caused baggage to be nil in `restart+extract_first` mode — violating the RFC.

**Fix** (`ddtrace/tracer/textmap.go`, `Extract()`): after `extractIncomingSpanContext`
returns and `onlyExtractFirst` is set, iterate `p.extractors` looking for the baggage
propagator and run it explicitly, merging results into `incomingCtx`. This is isolated to
`Extract()` so `extractIncomingSpanContext` is unchanged. All existing extract-first tests
still pass.

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
