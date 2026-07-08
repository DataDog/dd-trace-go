# SDK Error-Reporting Migration Contract

This document specifies exactly how Prompt 2 (automated call-site migration) should invoke the error-reporting API for each pattern found in the codebase.  Prompt 2 must follow these rules without deviation.

---

## 1. Background

The foundation built by Prompt 1 provides two forwarding surfaces:

| Surface | Location | Purpose |
|---|---|---|
| Auto-forward sink | `internal/log/log.go` + `internal/telemetry/log/forward.go` | Every `log.Error` call is mirrored to telemetry automatically. No call-site changes needed for existing `log.Error` uses. |
| Explicit helpers | `internal/telemetry/log/helpers.go` | For sources that bypass `log.Error`: `recover()` sites, swallowed errors. |

**Prompt 2 must NOT change existing `log.Error` or `log.Warn` calls unless the first argument is non-constant.** The auto-forward sink handles them already.

---

## 2. Constant-message rule (enforced by analyzer)

The `constantlogmsg` analyzer (in `internal/telemetry/log/analyzer/`) rejects any call where the message argument is non-constant.

**Correct** — the format string is a compile-time constant:
```go
log.Error("serialization failed: %s", err)
telemetrylog.ReportError("serialization failed", err)
```

**Wrong** — dynamic strings break dedup and risk PII:
```go
log.Error(err.Error())                         // ❌ non-constant
log.Error("failed: " + details)                // ❌ string concat
log.Error(fmt.Sprintf("error: %s", err))       // ❌ interpolated
telemetrylog.ReportError(err.Error(), err)      // ❌ same rule applies
```

---

## 3. Case types and canonical before/after

### Case A — `log.Error` with a constant format string (most common)

**No action required.** The auto-forward sink already mirrors these to telemetry. Verify the template is not in the `policyExclude` set if Error Tracking visibility is needed; if it is excluded, move it to `policyDowngrade` or `policyReport` in `internal/telemetry/log/policy.go`.

```go
// Before (no change needed)
log.Error("failed to parse trace source tag: %s", err)

// The sink calls forwardError("failed to parse trace source tag: %s", []any{err})
// which creates a telemetry record with:
//   Message:  "failed to parse trace source tag: %s"  (dedup key)
//   Attrs:    error.error_type = "..."                 (scrubbed)
//   Stack:    redacted Datadog frames only
```

### Case B — `log.Error` with a NON-constant first argument

The `constantlogmsg` analyzer will flag these. Fix by extracting a constant template and passing the dynamic value as an argument.

```go
// Before
log.Error(err.Error())          // ❌ analyzer reports this

// After — keep the existing call but fix the non-constant argument
log.Error("unexpected error: %s", err)   // ✅ first arg is now constant
```

```go
// Before
log.Error("failed: " + err.Error())      // ❌

// After
log.Error("failed: %s", err)             // ✅
```

```go
// Before
log.Error(fmt.Sprintf("operation %s failed: %v", op, err))   // ❌

// After
log.Error("operation %s failed: %v", op, err)                 // ✅
```

> Note: `%s`/`%v` etc. in the format string are fine — they stay as-is. Only the format string itself must be a literal.

### Case C — `recover()` sites (panic recovery)

Replace ad-hoc panic logging with `telemetrylog.ReportPanic`.

```go
// Before (common pattern)
if r := recover(); r != nil {
    log.Error("unexpected panic: %v", r)
}

// After
import telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"

if r := recover(); r != nil {
    log.Error("unexpected panic: %v", r)          // keep local log for visibility
    telemetrylog.ReportPanic(r, "unexpected panic in <subsystem>")
}
```

`ReportPanic` rules:
- Second argument (`msg`) MUST be a constant string.
- First argument is the raw `recover()` value; if it is an `error`, the type (not message) is attached.
- A redacted stack trace is always attached automatically.
- Obey the policy table: add the message to `policyTable` if it represents user/env noise.

### Case D — Swallowed errors (silent error branches)

Where an error is caught but not logged (intentionally or not), and the site warrants telemetry visibility, use `ReportError`.

```go
// Before
if err := doThing(); err != nil {
    // silently dropped
}

// After
if err := doThing(); err != nil {
    telemetrylog.ReportError("doThing: unexpected failure", err)
}
```

`ReportError` rules:
- First argument (`msg`) MUST be a constant string.
- `err` is scrubbed through `NewSafeError`; the error message is never sent.
- A redacted stack trace is always attached.
- Optional `telemetry.LogOption` args (e.g. `telemetry.WithTags(...)`) are appended after the stack option.

### Case E — Direct `telemetrylog.Error` calls (already migrated)

Some code (e.g. `internal/appsec/`) already calls `telemetrylog.Error` directly. These are correct and should not be changed. Verify they use constant messages.

```go
// Already correct — no action needed
telemetrylog.Error("appsec: remote activation cannot be enabled",
    slog.Any("error", telemetrylog.NewSafeError(err)))
```

---

## 4. Policy table

The policy table lives in `internal/telemetry/log/policy.go`. Prompt 2 must update it when migrating call sites:

- **Network/connectivity errors** (agent unreachable, send failures) → `policyExclude`
- **User misconfiguration** (invalid env var, bad rule syntax) → `policyDowngrade`
- **SDK defects** (serialization failures, internal invariant violations) → `policyReport` (default)

Add new entries at the bottom of the appropriate section comment.

---

## 5. What Prompt 2 must NOT do

- Do not change `log.Error` or `log.Warn` calls whose first argument is already a constant string — the auto-forward sink handles them.
- Do not use `fmt.Sprintf` or string concatenation in message arguments.
- Do not pass `err.Error()` as a message argument.
- Do not pass raw `error` values to `slog.Any` without `NewSafeError`.
- Do not add `log.Error` calls inside the forwarding/helper code itself (re-entrancy).
- Do not bypass the policy table — if a template should be silenced, add it to `policyTable`.

---

## 6. Verification checklist for Prompt 2

After each batch of migrations:

1. `go build ./...` — no compile errors.
2. `go vet ./...` — no `constantlogmsg` diagnostics.
3. `go test ./internal/log/... ./internal/telemetry/log/...` — all tests pass.
4. Confirm no PII-bearing strings appear in the `msg` field of any `telemetrylog.*` call.
