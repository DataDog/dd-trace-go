# Orchestrion constructs otelc cannot reproduce

Stop-and-flag cases: no otelc equivalent and no workaround. Anything with a workaround belongs in
`pattern-mapping.md`. Verified against the `OTELC_VERSION` pinned in
`.github/workflows/otelc.yml`; re-check the sources in `references.md`, since capabilities land over
time.

Every gap here is already known and tracked. Report it as a known gap, not a new finding.

## 1. Method call at a call site

`wrap_call`'s `function_call` matches only a callee qualified by a package alias (`net/http.Get`).
There is no receiver selector, and `http.DefaultClient.Do(...)` does not match either. Hooking the
method definition (`inject_hooks` with `func` + `recv`) works; a specific call site does not.

## 2. New declaration in a `main` package (`inject-declarations`)

Orchestrion injects `func init() { tracer.Start() }` this way. otelc's only equivalent is
`add_file`, which takes no selectors, so it also lands in the generated test-main compile unit.
There the copied file gets a bare `package` clause and the build fails with `syntax error:
unexpected keyword func, expected name`. `where: {file: {is_test: false}}` does not suppress it,
because `where` is ignored for file rules.

For statements at the top of an existing function use `inject_code` on `func main`, which does not
match test-main builds (`ddtrace/tracer/otelc.yaml`). For a new declaration, stop.

## 3. Negating or alternating a point selector

`all-of`, `one-of` and `not` work inside `where.file` only. At the top of `where` they load and then
fail at build time with "not yet supported". `all-of` and `one-of` have workarounds
(`pattern-mapping.md`); `not` has none.

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

## Documented in otelc, not implemented

`where.file.has_directive`, `target: test_main`, and `assign_value` on `kind: func` / `kind: type`.
Each one loads and then fails or does nothing.

## Closed since this doc was written

Confirm against the pinned version before relying on any of these; each is now in
`pattern-mapping.md`.

- **Composite-literal match.** `where: {struct_literal:}` with the `set_fields` modifier sets
  fields on `T{...}` literals written in the target package.
- **Reading the caller's context at a call site.** `wrap_call` templates expose
  `.FuncArgumentOfType` and `.CallArgument`, so an in-scope `context.Context` can be threaded into a
  call that has none.
- **Directive arguments.** `expand_directive` templates read `.DirectiveArgs` and
  `.DirectiveArg <key>`.
