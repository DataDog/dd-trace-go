# Orchestrion constructs otelc cannot reproduce

Stop-and-flag cases: no otelc equivalent and no workaround. Anything with a workaround belongs in
`pattern-mapping.md` or `gotchas.md`. Checked against latest upstream `main`, which `OTELC_VERSION`
in `.github/workflows/otelc.yml` tracks. Capabilities land often, so re-check the sources in
`references.md` before calling something a gap.

Every gap here is already known and tracked. Report it as a known gap, not a new finding.

## 1. New declaration in a `main` package (`inject-declarations`)

Orchestrion injects `func init() { tracer.Start() }` this way. otelc's only equivalent is
`add_file`, and `where` is ignored for file rules, so `where: {file: {is_test: false}}` does not
keep the file out of the generated test-main compile unit. An `init()` added to `main` then also
runs in every test binary
(https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/issues/1347).

For statements at the top of an existing function use `inject_code` on `func main`, which does not
match test-main builds (`ddtrace/tracer/otelc.yaml`). For a new declaration, stop. In any package
other than `main`, `add_file` works (`pattern-mapping.md`).

## 2. Negating or alternating a point selector

At the top level of `where`, `all-of`, `one-of` and `not` all load and then fail at build time with
"where `<name>` selector composition is not yet supported". They work only under `where.file`, so
they can compose file predicates but never point selectors.

## 3. Ordering between rules on the same function

otelc accepts `order` only on `inject_code` and `expand_directive`
(https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/issues/583). Stop when
an aspect sets `order:` on advice that is not `prepend-statements`, or when two such rules land on
the same function and the result depends on which runs first.

## 4. Function literals and nested functions

`func:` matches a top-level function or method by exact name, and nothing matches a function
literal. An aspect whose `function-body` join point reaches closures has no equivalent
(https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/issues/1603).
