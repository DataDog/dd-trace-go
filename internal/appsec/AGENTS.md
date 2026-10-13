**BEFORE writing or editing ANY code**, you MUST read [README.md](./README.md) (dyngo, operations,
features, block-outcome telemetry). This file only adds what the README does not cover.

## Overview

App & API Protection (AppSec/ASM/AAP) runtime. It connects instrumentation events to the go-libddwaf
WAF, keeps WAF rules in sync with Remote Config, and swaps listeners atomically on every config change.

## Where to look

| Task | Location | Notes |
|------|----------|-------|
| Start/stop, enablement modes | `appsec.go` | Called by `ddtrace/tracer/tracer.go` and `instrumentation/testutils` |
| Register a feature | `features.go` | `features` slice, rebuilt by `SwapRootOperation` on each config change |
| Feature contract | `listener/feature.go` | `nil, nil` = inactive; any error aborts the whole swap |
| Listeners | `listener/<domain>/` | `listener/sqlsec/sql.go` is the smallest template |
| WAF context, subcontext, metrics | `emitter/waf/` | `metrics.go` owns `waf.requests` and `rasp.rule.*` |
| Remote Config | `remoteconfig.go` | `ASM_FEATURES` activation, rule products, RASP capabilities |
| Env config, WAF handle | `config/` | `DD_APPSEC_*` keys in `config.go` and `internal_config.go`; handle in `wafmanager.go` |
| API Security sampling | `apisec/` | Lock-free LRU in `apisec/internal/timed` |
| Body parsing | `body/json/` | `unsafe.go` reflects into jsoniter internals and panics on layout drift |
| Operation types (emitters) | `instrumentation/appsec/emitter/` | Public: contrib imports these |
| WAF address names | `instrumentation/appsec/emitter/waf/addresses/` | Constants plus `NewAddressesBuilder()` |
| dyngo | `instrumentation/appsec/dyngo/` | Public; Orchestrion GLS hook in `gls.otelc.yaml` |
| User-event emitter | `emitter/usersec/` | Internal: used only by the public `appsec/` SDK and `listener/usersec` |

## Adding a feature

1. Define the operation where its emitters live. If contrib or other instrumentation emits it, put it
   in `instrumentation/appsec/emitter/<domain>/`. If only the public `appsec/` SDK emits it, put it in
   `internal/appsec/emitter/<domain>/` (like `usersec`), so that it does not become supported API.
   Arg and result types use the standard library only, because the instrumented package imports them.
2. Add `listener/<domain>/` with `NewXFeature(cfg, rootOp)`. Return `nil, nil` only when the feature
   would do nothing. A WAF-backed feature checks `cfg.SupportedAddresses.AnyOf(...)`, and RASP
   features also check `cfg.RASP`. A feature with non-WAF behavior activates anyway:
   `trace.NewAppsecSpanTransport` always activates, and `httpsec.NewHTTPSecFeature` falls back to
   `HeaderExtractionFeature` when no HTTP address is supported.
3. Append the constructor to `features` in `features.go`.
4. In the listener, get the WAF context with `waf.ContextOperationFromParents(op)`.
   - One-shot evaluation (SQLi, LFI, CMDi): the listener calls `NewSubcontextOp()` and then `Run`,
     and defers `subOp.Close()`. See `listener/sqlsec`.
   - Multi-stage evaluation (SSRF covers both the request and the response): the subcontext belongs
     to the operation. The emitter creates it before starting the operation and embeds it, the start
     and finish listeners both call `Run` on it, and the operation's `Finish` closes it. See
     `ProtectRoundTrip` in `instrumentation/appsec/emitter/httpsec/roundtripper.go`.
5. For a new RASP rule type, register its `remoteconfig.ASMRASP*` capability in `enableRASP`
   (`remoteconfig.go`), behind the same availability check as its emitter. For example, LFI and CMDi
   are registered only when `orchestrion.Enabled()`, so Remote Config never sees a capability the
   tracer cannot provide.
6. Update the feature table and operation stack in [README.md](./README.md).

## Conventions

- Never mutate listeners in place. The root operation (`dyngo.SwapRootOperation`) and the active
  instance (`activeAppSec`) are swapped atomically, so rebuild through `SwapRootOperation`.
- Enablement: `DD_APPSEC_ENABLED=false` (`ForcedOff`) beats Remote Config. When it is unset, the mode
  is `RCStandby`, which waits for `ASM_FEATURES`.
- `libddwaf.Usable()` gates start. Log an error only under `ForcedOn`. In other modes use debug or
  telemetry logs, so that APM-only users never see AppSec errors.
- New log messages are constant and prefixed `appsec:` (most existing call sites follow this). Errors
  sent to telemetry are wrapped with `telemetrylog.NewSafeError`. Never reword an existing telemetry
  log template (e.g. `"failed to build WAF handle"`) just to add the prefix: its text is the
  deduplication key.
- No parent operation (`dyngo.FromContext` or `FindOperation` fails):
  - Helper emitters that evaluate and return only an error log once at debug level and return nil.
    Examples are `sqlsec.ProtectSQLOperation` and `httpsec.ProtectRoundTrip`.
  - Emitters that hand an operation back to the caller still return a usable operation with a nil
    parent, so the caller's Start/Finish pairing holds. `graphqlsec.Start{Execution,Resolve}Operation`
    log at debug level. `usersec.StartUserLoginOperation` logs an error once, because the user called
    the SDK explicitly.
- The only build-tag split here is `cgo`/`!cgo` (`telemetry_*.go`). `datadog.no_waf` is a
  go-libddwaf tag that compiles the WAF out, making `Usable()` false.

## Anti-patterns

- Non-stdlib imports in operation arg/result types. They create import cycles.
- Discarding a `WAFManager` without `Close()`. Only `Close()` releases the native builder and cancels
  the GC cleanup. `Reset()` only restores the default rules on a manager that will be reused. That
  happens on Remote Config deactivation: `handleASMFeatures` calls `appsec.stop()` and keeps the
  instance in case AppSec is activated again.
- WAF telemetry assertions without `testutils.StartTelemetryRecorder` before AppSec starts.

## Testing

- `instrumentation/testutils.StartAppSec(t)` forces `ForcedOn` and skips when the WAF is unusable.
- Rule fixtures are `testdata/*.json`. Select one with `t.Setenv("DD_APPSEC_RULES", "testdata/...")`.

```bash
go test -race ./internal/appsec/... ./instrumentation/appsec/... ./appsec/...
CGO_ENABLED=0 go test ./internal/appsec/...
DD_APPSEC_ENABLED=false go test ./internal/appsec/...
go test -tags datadog.no_waf ./internal/appsec/...
```

CI: `.github/workflows/appsec.yml` is path-filtered and has four test jobs:
- `macos`: `CGO_ENABLED` x `DD_APPSEC_ENABLED` x `GOEXPERIMENT` (default and `cgocheck2`). It gates:
  any failure fails the job.
- `golang-linux-container`: `CGO_ENABLED` x `DD_APPSEC_ENABLED` on Debian and Alpine images, on amd64
  and arm64. It does not run `cgocheck2`. Root-module failures gate, but contrib submodule failures
  are only logged.
- `disabled`: `DD_APPSEC_ENABLED` x `-tags datadog.no_waf` on Linux, macOS, and Windows. It does not
  gate: failures are echoed and the step still succeeds, so read its logs. Do not rely on its status.
- `test-app-smoke-tests`: runs the smoke-test workflow of `DataDog/appsec-go-test-app` end to end
  against the PR head. It is skipped on fork PRs.

In `.github/ci-components.yml`, non-Markdown `internal/appsec` paths fall through to `core`, which
runs every gate. `*.md` files, including this one and `README.md`, match the earlier `docs` component
and run only `static-docs`. GitLab micro benchmarks run `./internal/appsec/...`.

## Updating Documentation

The developer should update [README.md](./README.md) whenever changes to Application Security/App & API Protection (otherwise known as ASM, AppSec, or AAP) are introduced. 

If these updates are not made, tell the developer to make changes or provide suggestions if requested.