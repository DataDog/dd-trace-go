# Sources of truth

Consult these live; do not copy their content into the skill (it drifts). Links point at parseable
source rather than rendered docs where possible.

## Orchestrion (what you are migrating FROM)

- Join points and advice, machine-readable schema:
  [_docs/static/schema.json](https://github.com/DataDog/orchestrion/blob/main/_docs/static/schema.json)
- Join point definitions (source):
  [internal/injector/aspect/join](https://github.com/DataDog/orchestrion/tree/main/internal/injector/aspect/join)
- Advice definitions (source):
  [internal/injector/aspect/advice](https://github.com/DataDog/orchestrion/tree/main/internal/injector/aspect/advice)
- What advice templates can read (the `.` accessors, e.g. `.Function.ArgumentOfType`, `.AST`):
  [internal/injector/aspect/advice/code/dot.go](https://github.com/DataDog/orchestrion/blob/main/internal/injector/aspect/advice/code/dot.go)
- Human-readable aspect docs (markdown source):
  [_docs/content/contributing/aspects](https://github.com/DataDog/orchestrion/tree/main/_docs/content/contributing/aspects)
- The integration's own aspects: `contrib/<name>/orchestrion.yml` (the thing you are translating).

## otelc (what you are migrating TO)

- Rule format and all rule kinds:
  [docs/rules.md](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/docs/rules.md)
- Rule kinds (source of truth):
  [tool/internal/rule](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/tree/main/tool/internal/rule)
- Hook API (`HookContext`: SetParam / SetReturnVal / SetSkipCall / SetData / ...):
  [pkg/hook/context.go](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/pkg/hook/context.go)
- Adding instrumentation and import-driven selection (the tool file):
  [docs/instrument-guide.md](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/docs/instrument-guide.md)
- Building and running otelc:
  [docs/getting-started.md](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/docs/getting-started.md)
- Worked examples to copy the shape from:
  [instrumentation/](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/tree/main/instrumentation)

## In this repo

The otelc foundation, already written and passing. Closer to what you are doing than the upstream
examples, because these rules target our own code and the hooks call dd-trace-go.

- `internal/otelc/` — build-mode flag (`assign_value`) and the GLS storage woven into `runtime`
  (`add_struct_fields` + `add_file` + `inject_code`, including `//go:linkname`).
- `ddtrace/tracer/gls.otelc.yaml` — `add_struct_fields` plus four `inject_code` rules, with the
  identifier-coupling note and its guard test.
- `ddtrace/tracer/otelc.yaml` — tracer lifecycle, and why it cannot use `add_file`.
- `otel.instrumentation.go` (repo root) and `internal/orchestrion/_integration/otel.instrumentation.go`
  — how rules are discovered. An application's tool file names one dd-trace-go package; otelc then
  finds the tool file at that package's **module root** and recurses, which is what pulls in rules for
  packages an application cannot import itself.
- `.github/workflows/otelc.yml` — how the suites are run in CI.
