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

These links point at `main`. CI builds with the version pinned as `OTELC_VERSION` in
`.github/workflows/otelc.yml`, so check anything that reads as new against that tag.

## In this repo

The otelc foundation, already written and passing. Closer to what you are doing than the upstream
examples, because these rules target our own code and the hooks call dd-trace-go.

- `ddtrace/tracer/otelc.yaml` — every core rule in one file: build-mode flag (`assign_value`), the
  GLS storage woven into `runtime` (`add_struct_fields` + `add_file` + `inject_code`, including
  `//go:linkname`), the span GLS lifecycle, and the tracer lifecycle with why it cannot use
  `add_file`. Worth reading before writing your own rules; a rule carries its own `target:`, so one
  file can rewrite several packages.
- `ddtrace/tracer/gls_otelc_identifiers_test.go` and the one in `instrumentation/appsec/dyngo` —
  how to guard `inject_code` against a rename in the target.
- `otelc/all/otel.instrumentation.go` and
  `internal/orchestrion/_integration/otel.instrumentation.go` — rule discovery. An application
  names `otelc/all` in the tool file at its own module root; otelc reads the tool file at that
  module's root and recurses into its imports, which is how rules reach packages an application
  cannot import itself.
- `internal/apps/otelc-external-app` and `scripts/build_otelc_external_app.sh` — the guard that a
  package your rules are read from is importable by a real user. Rules must not live under
  `internal/`, because otelc blank-imports that package into the application's own main package. A
  rule's `target:` may still be internal.
- `.github/workflows/otelc.yml` — how the suites run in CI, which modules get tidied, and the
  pinned otelc version.
