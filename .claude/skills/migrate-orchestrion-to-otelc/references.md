# Sources of truth

Consult these live; do not copy their content into the skill. Links point at parseable source rather
than rendered docs where possible.

## Orchestrion (what you are migrating FROM)

- Join points and advice, machine-readable schema:
  [_docs/static/schema.json](https://github.com/DataDog/orchestrion/blob/main/_docs/static/schema.json)
- Join point definitions:
  [internal/injector/aspect/join](https://github.com/DataDog/orchestrion/tree/main/internal/injector/aspect/join)
- Advice definitions:
  [internal/injector/aspect/advice](https://github.com/DataDog/orchestrion/tree/main/internal/injector/aspect/advice)
- What advice templates can read (the `.` accessors):
  [internal/injector/aspect/advice/code/dot.go](https://github.com/DataDog/orchestrion/blob/main/internal/injector/aspect/advice/code/dot.go)
- Human-readable aspect docs:
  [_docs/content/contributing/aspects](https://github.com/DataDog/orchestrion/tree/main/_docs/content/contributing/aspects)
- The integration's own aspects: `contrib/<name>/orchestrion.yml`.

## otelc (what you are migrating TO)

- Rule format and all rule kinds:
  [docs/rules.md](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/docs/rules.md)
- Rule kinds in source:
  [tool/internal/rule](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/tree/main/tool/internal/rule)
- Hook API (`HookContext`):
  [pkg/hook/context.go](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/pkg/hook/context.go)
- Hook authoring, the tool file, and hook limitations:
  [docs/instrument-guide.md](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/docs/instrument-guide.md)
- Building and running otelc:
  [docs/getting-started.md](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/docs/getting-started.md)
- Diagnosing a build that instrumented nothing, and the `.otelc-build` layout:
  [docs/troubleshooting.md](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/docs/troubleshooting.md)
- Runtime configuration, including the per-instrumentation enable/disable env vars:
  [docs/configuration.md](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/docs/configuration.md)
- Worked examples:
  [instrumentation/](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/tree/main/instrumentation)

`OTELC_VERSION` in `.github/workflows/otelc.yml` pins a commit on upstream `main`, never a release.
Check otelc behaviour against latest upstream `main` and ignore tags. When a port needs something
newer than the pin, bump `OTELC_VERSION` to the latest `main` commit and re-pin
`go.opentelemetry.io/otelc/pkg` to match (see "The otelc pin" in `SKILL.md`).

`docs/` is not in the module zip, so the links above are the only copy. To settle what the tool
does, read `tool/internal/` in a clone of upstream at latest `main`. The module cache copy under
`go.opentelemetry.io/otelc@<OTELC_VERSION>/` shows the pin, which can be older.

## In this repo

Closer to what you are doing than the upstream examples, because these rules target our own code
and the hooks call dd-trace-go.

- `ddtrace/tracer/otelc.yaml`: every core rule in one file. The build-mode flag (`assign_value`),
  the GLS storage woven into `runtime` (`add_struct_fields` + `add_file` + `inject_code`), the span
  GLS lifecycle, and the tracer lifecycle with why it cannot use `add_file`. Also the worked example
  of resolving every injected identifier through `.FuncArgument` and `.Receiver`. A rule carries
  its own `target:`, so one file can rewrite several packages.
- `instrumentation/appsec/dyngo/gls.otelc.yaml`: the same rules against plain functions, so every
  identifier resolves through `.FuncArgument`.
- `contrib/*/otelc/`: integrations already ported. `contrib/net/http/otelc` combines hooks,
  `inject_code`, `wrap_call`, `add_struct_fields` and `set_fields`.
- `otelc/all/otel.instrumentation.go` and
  `internal/orchestrion/_integration/otel.instrumentation.go`: rule discovery. An application names
  `otelc/all` in the tool file at its module root, and otelc recurses into its imports, which is how
  rules reach packages the application does not import itself.
- `internal/apps/otelc-external-app` and `scripts/build_otelc_external_app.sh`: the guard that the
  packages your rules are read from are importable by a real user.
- `.github/workflows/otelc.yml`: how the suites run in CI, `OTELC_MODULES`, and `OTELC_VERSION`.
