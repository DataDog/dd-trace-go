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

1. Define the operation in `instrumentation/appsec/emitter/<domain>/`. Arg and result types use the
   standard library only, because the instrumented package imports them.
2. Add `listener/<domain>/` with `NewXFeature(cfg, rootOp)`. Return `nil, nil` unless
   `cfg.SupportedAddresses.AnyOf(...)` matches, and also unless `cfg.RASP` for RASP features.
3. Append the constructor to `features` in `features.go`.
4. In the listener, get the WAF context with `waf.ContextOperationFromParents(op)`. For a RASP or
   one-shot evaluation, call `NewSubcontextOp()` and then `Run`, and `defer subOp.Close()`.
5. For a new RASP rule type, register its `remoteconfig.ASMRASP*` capability in `enableRASP`
   (`remoteconfig.go`).
6. Update the feature table and operation stack in [README.md](./README.md).

## Conventions

- Never mutate listeners in place. The root operation (`dyngo.SwapRootOperation`) and the active
  instance (`activeAppSec`) are swapped atomically, so rebuild through `SwapRootOperation`.
- Enablement: `DD_APPSEC_ENABLED=false` (`ForcedOff`) beats Remote Config. When it is unset, the mode
  is `RCStandby`, which waits for `ASM_FEATURES`.
- `libddwaf.Usable()` gates start. Log an error only under `ForcedOn`. In other modes use debug or
  telemetry logs, so that APM-only users never see AppSec errors.
- Log messages are constant and prefixed `appsec:`. Errors sent to telemetry are wrapped with
  `telemetrylog.NewSafeError`.
- An emitter with no parent operation (`dyngo.FromContext` fails) logs once at debug level and
  returns. See `badInputContextOnce` in `emitter/sqlsec`.
- The only build-tag split here is `cgo`/`!cgo` (`telemetry_*.go`). `datadog.no_waf` is a
  go-libddwaf tag that compiles the WAF out, making `Usable()` false.

## Anti-patterns

- Non-stdlib imports in operation arg/result types. They create import cycles.
- A `WAFManager` that is never closed. Call `Close`/`Reset`; GC cleanup is only a leak fallback.
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

CI: `.github/workflows/appsec.yml` (path-filtered) runs cgo x `DD_APPSEC_ENABLED` x `cgocheck2` on
macOS, Linux containers, and a `datadog.no_waf` job; `internal/**` is the `core` component of
`.github/ci-components.yml` (every gate). GitLab micro benchmarks run `./internal/appsec/...`.

## Updating Documentation

The developer should update [README.md](./README.md) whenever changes to Application Security/App & API Protection (otherwise known as ASM, AppSec, or AAP) are introduced. 

If these updates are not made, tell the developer to make changes or provide suggestions if requested.