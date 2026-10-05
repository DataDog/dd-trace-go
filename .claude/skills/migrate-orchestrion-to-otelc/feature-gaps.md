# Orchestrion constructs otelc cannot reproduce

Stop-and-flag cases: no otelc equivalent and no workaround. Anything with a workaround belongs in
`pattern-mapping.md`. Verified by running the `OTELC_VERSION` pinned in
`.github/workflows/otelc.yml`; re-check the sources in `references.md`, since capabilities land over
time, and upstream `main` is normally ahead of the pin.

Every gap here is already known and tracked. Report it as a known gap, not a new finding.

## 1. Method call at a call site

`wrap_call`'s `function_call` parses as `import/path.FunctionName` and rejects anything else, so it
matches only a callee qualified by a package alias (`net/http.Get`). There is no receiver selector,
and `http.DefaultClient.Do(...)` does not match either. Hooking the method definition
(`inject_hooks` with `func` + `recv`) works; a specific call site does not.

## 2. New declaration in a `main` package (`inject-declarations`)

Orchestrion injects `func init() { tracer.Start() }` this way. otelc's only equivalent is
`add_file`, which takes no selectors, so it also lands in the generated test-main compile unit.
There the copied file gets a bare `package` clause and the build fails with `syntax error:
unexpected keyword func, expected name`. `where: {file: {is_test: false}}` does not suppress it,
because `where` is ignored for file rules. Both halves are open upstream
(open-telemetry/opentelemetry-go-compile-instrumentation#1347 and #1348).

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

`target: $root` substitutes for the common `not: {import-path: <the library>}` when the excluded
package is a separate module, since `$root` stops at the module boundary. The cost is that the rule
then also skips call sites inside third-party dependency modules.

## 4. Excluding one call site from a definition-side hook

Orchestrion honours `//orchestrion:ignore` on a call. `inject_hooks` hooks the definition, so it
fires for every caller and no selector excludes one.

## 5. Matching by interface implementation

Orchestrion's `function` join point offers `result-implements`, `final-result-implements` and
`argument-implements`. otelc selectors name concrete symbols only. `FuncArgumentOfType` and
`FuncReturnOfType` match a syntactic type name, so a custom type that merely implements
`context.Context` is not matched.

## Partly implemented, easy to misread

- **`where.file.has_directive`** works, but only for a directive sitting above the `package` clause.
  The same directive on a `var` or `func` declaration, or floating between declarations, is not
  found, and the rule then matches nothing while still reporting success. It is absent from
  `docs/rules.md`, so treat the behaviour above as the specification.
- **`assign_value` on `kind: func` or `kind: type`** is rejected when the rule loads: `kind "func"
  has no supported advice; use var or const to replace or wrap a value`. Loud, not silent. Only
  `var` and `const` carry `replace:` and `wrap:`.
- **`target: test_main`** does not exist and is not documented. `where.file.is_test` is the only
  handle on test builds, and file rules ignore it (gap 2).

## Closed since this doc was written

Each is now in `pattern-mapping.md`, and each was re-confirmed against the pinned version.

- **Composite-literal match.** `where: {struct_literal:}` with the `set_fields` modifier sets
  fields on `T{...}` literals written in the target package.
- **Reading the caller's context at a call site.** `wrap_call` templates expose
  `.FuncArgumentOfType` and `.CallArgument`, so an in-scope `context.Context` can be threaded into a
  call that has none. `.FuncReturnOfType` is not available to `wrap_call` at the pinned version,
  only to `inject_code` and `expand_directive`.
- **Directive arguments.** `expand_directive` templates read `.DirectiveArgs` and
  `.DirectiveArg <key>`.
