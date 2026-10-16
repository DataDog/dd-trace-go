# Orchestrion constructs otelc cannot reproduce

Stop-and-flag cases: no otelc equivalent and no workaround. Anything with a workaround belongs in
`pattern-mapping.md`. Checked against latest upstream `main`, which `OTELC_VERSION` in
`.github/workflows/otelc.yml` tracks. Capabilities land often, so re-check the sources in
`references.md` before calling something a gap.

Every gap here is already known and tracked. Report it as a known gap, not a new finding.

## 1. Reading a call's receiver at a call site

`wrap_call` templates have no variable for the receiver of the matched call. `{{ . }}` is the whole
call, and `{{ .Receiver }}` is the receiver of the *enclosing* method. An aspect that reads or
rewrites the receiver expression (`logger` in `logger.Info(...)`) cannot be reproduced.

## 2. New declaration in a `main` package (`inject-declarations`)

Orchestrion injects `func init() { tracer.Start() }` this way. otelc's only equivalent is
`add_file`, which takes no selectors, so it also lands in the generated test-main compile unit.
There the copied file gets a bare `package` clause and the build fails with `syntax error:
unexpected keyword func, expected name`. `where: {file: {is_test: false}}` does not suppress it,
because `where` is ignored for file rules. Both halves are open upstream
(https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/issues/1347 and
https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/issues/1348).

For statements at the top of an existing function use `inject_code` on `func main`, which does not
match test-main builds (`ddtrace/tracer/otelc.yaml`). For a new declaration, stop.

## 3. Negating or alternating a point selector

At the top level of `where`, `all-of`, `one-of` and `not` all load and then fail at build time with
"where `<name>` selector composition is not yet supported". They are executable only under
`where.file`, where all three do work, so they can compose file predicates but never point
selectors.

Two further limits inside `where.file`:

- A combinator owns its node. A sibling leaf predicate, or a second combinator, is rejected with
  "where.file.`<name>` cannot be combined with other predicates".
- Leaf predicates do not conjoin implicitly. Two of them fail with "where.file has multiple active
  predicates"; write an explicit `all-of` instead.

Excluding packages is not this gap: a `target:` list with `not:` entries does it
(`target: ["**", not: "<the library>/**"]`).

## 4. Matching by interface implementation

Orchestrion's `function` join point offers `result-implements`, `final-result-implements` and
`argument-implements`. otelc selectors name concrete symbols only. `FuncArgumentOfType` and
`FuncReturnOfType` match a syntactic type name, so a custom type that merely implements
`context.Context` is not matched.

## 5. Ordering between rules on the same function

Orchestrion orders advice on one join point by namespace and `order:`
(`crashtracker/orchestrion.yml` and `instrumentation/errortrace/orchestrion.yml` both set
`order: 100`). otelc has no ordering key. Modifiers in one `do:` sequence run in declaration order,
and the order across separate rules is not defined
(https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/issues/583). Stop when an aspect sets
`order:`, or when two rules land on the same function and the result depends on which runs first.

## 6. Function literals and nested functions

`func:` matches a top-level function or method by exact name, and nothing matches a function
literal. An aspect whose `function-body` join point reaches closures has no equivalent. Directive
rules also see only top-level functions, so a directive on a function literal is not expanded.

## Partly implemented, easy to misread

- **Selectors a rule kind does not read are dropped without an error.** Each rule kind decodes only
  its own fields and ignores unknown keys. Only `inject_hooks` reads `signature`,
  `signature_contains`, `result`, `last_result` and `param`. On `inject_code` the rule loads,
  ignores them, and matches every function with that name.
- **`where.file.has_directive`** works only for a directive in the file's leading comments, above
  the `package` clause. On a `var` or `func` declaration, or between declarations, it is not found,
  and the rule then matches nothing while still reporting success.
- **`assign_value` on `kind: func` or `kind: type`** is rejected when the rule loads: `kind "func"
  has no supported advice; use var or const to replace or wrap a value`. Loud, not silent. Only
  `var` and `const` carry `replace:` and `wrap:`.
- **`target: test_main`** is reserved in `docs/rules.md` but not supported. `where.file.is_test` is
  the only handle on test builds, and file rules ignore it (gap 2).
