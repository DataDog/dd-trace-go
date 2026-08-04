# Orchestrion constructs otelc cannot reproduce

Only things with no otelc equivalent and no workaround, the cases where you STOP and do not migrate
the aspect (flag it, and file an upstream otelc ask if warranted). Anything with a workaround belongs
in `pattern-mapping.md`. Re-verify against the otelc sources in `references.md`; capabilities land
over time.

## 1. Composite-literal (struct-literal) match, with no constructor funnel

Orchestrion can match a `T{...}` literal at a use site (`struct-literal` join point) and rewrite it.
otelc has no rule that matches a composite literal. This is only a real blocker when there is no
constructor to hook instead:
- A funnel exists (the value always flows through one constructor) → hook that constructor and mutate
  the argument. Migratable; put it in `pattern-mapping.md`.
- No funnel (built in many places or in generated code) → not reproducible. Stop.
  (e.g. aws-sdk-go-v2, twitchtv/twirp)

## 2. Matching a method call at a call site

Orchestrion's `method-call` join point matches a call to a method on a receiver, e.g.
`logger.Info(...)`. otelc's call-site rule (`wrap_call` / `function_call`) matches only
package-qualified function calls like `net/http.Get`, not `receiver.Method(...)`. You can hook the
method *definition* (`inject_hooks` func+recv), but not a specific method call site. If the aspect
needs the call site, stop.

## 3. Reading the caller's context at a call site (passing the outer context)

Some aspects read the enclosing function's arguments by type at a call site, e.g. find an in-scope
`context.Context` / `*http.Request` and thread it into a call that otherwise has no context (the
net/http and zap client shorthands). otelc's rule templates do not expose this today. Hooking the
definition instead cannot see the caller's scope and falls back to GLS, which does not cross
goroutine boundaries, so it is not equivalent. Stop and flag.

Tracked upstream in otelc (issue #702, draft PR #729), which is adding template variables like
`FuncArgumentOfType`, starting with the directive rule; the call/wrap-site rules needed here are a
later follow-up. Not available in otelc yet, so re-check those before relying on it.

## 4. Adding a declaration to a `main` package (`inject-declarations` into `main`)

Orchestrion can add a new top-level declaration to the application's `main` package, which is how it
injects `func init() { tracer.Start() }`. otelc's only equivalent is `add_file`, and targeting `main`
with it currently breaks `go test` for that module: the rule is also applied to the generated
test-main compile unit, where the copied file is written with a bare `package` and no name, so the
build fails with `syntax error: unexpected keyword func, expected name`. File rules take no
selectors, so `where: {file: {is_test: false}}` does not suppress it.

If you only need statements at the top of an existing function, use `inject_code` on `func main`,
which does not match test-main builds. If you genuinely need a new declaration, stop.
(Verified against otelc v1.0.1. This is why the tracer starts at the top of `main` rather than from
an init, so anything traced from a package init is lost; see `ddtrace/tracer/otelc.yaml`.)
