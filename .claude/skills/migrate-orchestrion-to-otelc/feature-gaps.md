# Orchestrion constructs otelc cannot reproduce

Stop-and-flag cases: no otelc equivalent and no workaround. Anything with a workaround belongs in
`pattern-mapping.md`. Verified against otelc v1.0.1; re-check the sources in `references.md`, since
capabilities land over time.

Each gap has a ticket. Reference the key rather than reporting it as new.

## 1. Composite-literal (`struct-literal`) match — IDMPL-659

otelc has no rule that matches a `T{...}` literal at a use site. `assign_value` only matches a named
top-level `var`/`const`. Blocker only when there is no constructor to hook instead:
- A funnel exists (the value always flows through one constructor) → hook it and mutate the
  argument. Migratable, see `pattern-mapping.md`.
- No funnel, built in many places or generated → stop (e.g. `contrib/net/http`'s
  `Transport{}` self-instrumentation guard).
- aws-sdk-go-v2 and twitchtv/twirp look like no-funnel cases and are not: both funnel through a
  generated per-package helper (`NewFromConfig`, `newServerOpts`). Check for a generated funnel
  before concluding there is none.

## 2. Method call at a call site — IDMPL-721

`wrap_call`'s `function_call` matches only a callee qualified by a package alias (`net/http.Get`).
There is no receiver selector, and `http.DefaultClient.Do(...)` does not match either. Hooking the
method definition (`inject_hooks` with `func` + `recv`) works; a specific call site does not.

## 3. Reading the caller's context at a call site — IDMPL-660

Orchestrion's `.Function.ArgumentOfType` and `.Function.ArgumentThatImplements` find an in-scope
`context.Context` or `*http.Request` among the enclosing function's named parameters and thread it
into a call that has none (the net/http and zap client shorthands). otelc's templates expose nothing
equivalent, and hooking the definition instead sees only GLS, which does not cross goroutine
boundaries.

Upstream: issue #702 with draft PR #729, directive rule first and call/wrap sites later. Both still
open.

## 4. New declaration in a `main` package (`inject-declarations`)

Orchestrion injects `func init() { tracer.Start() }` this way. otelc's only equivalent is
`add_file`, which takes no selectors, so it also lands in the generated test-main compile unit.
There the copied file gets a bare `package` clause and the build fails with `syntax error:
unexpected keyword func, expected name`. `where: {file: {is_test: false}}` does not suppress it.

For statements at the top of an existing function use `inject_code` on `func main`, which does not
match test-main builds (`ddtrace/tracer/otelc.yaml`). For a new declaration, stop.

## 5. Negating or alternating a point selector — IDMPL-724

`all-of`, `one-of` and `not` work inside `where.file` only. At the top of `where` they load and then
fail at build time with "not yet supported". `all-of` and `one-of` have workarounds
(`pattern-mapping.md`); `not` has none.

`target: $root` substitutes for the common `not: {import-path: <the library>}` when the excluded
package is a separate module, since `$root` stops at the module boundary. The cost is that the rule
then also skips call sites inside third-party dependency modules.

## 6. Directive arguments — IDMPL-725

`expand_directive` matches a directive even when it carries arguments, and injects into the
annotated function, but its template exposes only `{{FuncName}}`; anything else fails with
`unknown template tag`. So `//dd:span foo:bar` fires, and `foo` is unreachable. otelc already
parses these (`ast.ParseDirectiveArgs`), it just does not pass them to the template.

## 7. Excluding one call site from a definition-side hook — IDMPL-720

Orchestrion honours `//orchestrion:ignore` on a call. `inject_hooks` hooks the definition, so it
fires for every caller and no selector excludes one.

## 8. Matching by interface implementation

Orchestrion's `function` join point offers `result-implements`, `final-result-implements` and
`argument-implements`. otelc selectors name concrete symbols only.

## Documented in otelc, not implemented

`where.file.has_directive`, `target: test_main`, and `assign_value` on `kind: func` / `kind: type`.
Each one loads and then fails or does nothing.
