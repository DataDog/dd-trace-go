# Orchestrion constructs otelc cannot reproduce

Stop-and-flag cases: no otelc equivalent and no workaround. Anything with a workaround belongs in
`pattern-mapping.md`. Verified against otelc v1.0.1; re-check the sources in `references.md`, since
capabilities land over time.

## 1. Composite-literal (`struct-literal`) match

otelc has no rule that matches a `T{...}` literal at a use site. `assign_value` only matches a named
top-level `var`/`const`. Blocker only when there is no constructor to hook instead:
- A funnel exists (the value always flows through one constructor) → hook it and mutate the
  argument. Migratable, see `pattern-mapping.md`.
- No funnel, built in many places or generated → stop (e.g. aws-sdk-go-v2, twitchtv/twirp).

## 2. Method call at a call site

`wrap_call`'s `function_call` matches only a callee qualified by a package alias (`net/http.Get`).
There is no receiver selector, and `http.DefaultClient.Do(...)` does not match either. Hooking the
method definition (`inject_hooks` with `func` + `recv`) works; a specific call site does not.

## 3. Reading the caller's context at a call site

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

## 5. Negating or alternating a point selector

`all-of`, `one-of` and `not` work inside `where.file` only. At the top of `where` they load and then
fail at build time with "not yet supported". `all-of` and `one-of` have workarounds
(`pattern-mapping.md`); `not` has none.

## 6. Matching by interface implementation

Orchestrion's `function` join point offers `result-implements`, `final-result-implements` and
`argument-implements`. otelc selectors name concrete symbols only.

## Documented in otelc, not implemented

`where.file.has_directive`, `target: test_main`, and `assign_value` on `kind: func` / `kind: type`.
Each one loads and then fails or does nothing.
