# Auto-instrumentation

New integrations must support compile-time auto-instrumentation, so a user gets tracing without
editing their code. Today this is delivered with Orchestrion, described below. This guide covers how
to add auto-instrumentation to an integration. For building the integration itself, see
[INTEGRATIONS.md](./INTEGRATIONS.md).

## Orchestrion

Orchestrion rewrites the Go AST at build time using an `orchestrion.yml` file in the integration.

For references and guides, check the following:

- **JSON schema**: the machine-readable list of every join point and advice, and what editors
  validate `orchestrion.yml` against. Published at
  [datadoghq.dev/orchestrion/schema.json](https://datadoghq.dev/orchestrion/schema.json), with the
  source in the Orchestrion repo at `internal/injector/config/schema.json`. Prefer this when you need
  the exact, current set of options.
- **Contributor guide**: human-readable explanations of each join point and advice, and when to use
  them, at
  [datadoghq.dev/orchestrion/contributing/aspects](https://datadoghq.dev/orchestrion/contributing/aspects/).
- **Existing examples**: the `contrib/*/orchestrion.yml` files in this repo.

### orchestrion.yml

An `orchestrion.yml` contains aspects. Each aspect is a join point (where to act) and advice (what to
inject).

Advice templates are rendered with Go `text/template`, where `.` gives access to the matched code and
its surroundings:

- `{{ . }}`: the matched node itself. For `wrap-expression`, this is the expression being wrapped.
- `{{ .AST }}`: the matched syntax node. Which fields exist depends on the node type. For a call
  match (`function-call`) the node is a call expression, so `{{ .AST.Fun }}` and
  `{{ index .AST.Args 0 }}` (first argument) are available. Orchestrion uses the
  [github.com/dave/dst](https://pkg.go.dev/github.com/dave/dst) library, whose node types mirror the
  standard library [go/ast](https://pkg.go.dev/go/ast); use those references for the full set of
  nodes and fields.
- `{{ .Function }}`: the function enclosing the matched node, available when the match is inside a
  function body (for example a `function-body` join point). Its accessors return an error when they
  do not apply: `{{ .Function.Receiver }}` fails if the function is not a method,
  `{{ .Function.Argument 0 }}` fails if there is no such argument. Also `Name`, `Result`,
  `ArgumentOfType`, `ResultOfType`.
- `{{ .DirectiveArgs "name" }}`: the arguments of a matching directive comment.

For the full set of join points and advice and when to use each, see the schema and contributor guide
above. For patterns to copy, read the existing `contrib/*/orchestrion.yml` files.

### Common patterns

Read the referenced `orchestrion.yml` files for the full aspect code.

- **Wrap a constructor call.** For a hook, middleware, or interface wrapper, wrap the call
  (`wrap-expression`) so it still returns the same value. See
  [redis.v9](./redis/go-redis.v9/orchestrion.yml), [gin](./gin-gonic/gin/orchestrion.yml),
  [chi.v5](./go-chi/chi.v5/orchestrion.yml).
- **Replace a constructor.** When you provide a drop-in with the same signature and return type, swap
  the call one-to-one (`replace-function`). See [database/sql](./database/sql/orchestrion.yml)
  (`sql.Open`, `sql.OpenDB`) and [pgx.v5](./jackc/pgx.v5/orchestrion.yml).
- **Grab the surrounding context.** Some calls take no `context.Context` (for example `http.Get`,
  `http.Post`). An aspect can pull a context from the enclosing function with
  `.Function.ArgumentOfType "context.Context"` (or from a `*http.Request` via its `Context()`) and
  thread it into the traced call, so context propagates even where the original call has no context
  argument. See the `Get|Head|Post|PostForm` aspect in [net/http](./net/http/orchestrion.client.yml).
- **Modify library code.** Instead of wrapping from the outside, inject tracing into the library's
  own functions and types (`add-struct-field`, `function-body`, `prepend-statements`). Choose this
  when the library has no hook and cannot be wrapped safely (the concrete-wrapper case in
  [INTEGRATIONS.md](./INTEGRATIONS.md) section 3.5), or simply when modifying the library is easier
  and more complete than instrumenting from the outside. `net/http` chose this: instrumenting the
  library's own request handling was simpler than enumerating every way an `http.Server` or handler
  can be built.

  The injected library-side code needs to call your tracing logic, which lives in a helper package.
  There are two ways to reach it without an import cycle:
  - **Shared package (the clean approach).** Write the helper so it does not import the library, using
    its own adapter interfaces, and have the injected code import it normally. No cycle, and the
    package is reusable across contribs. See
    [kafkatrace](./confluentinc/confluent-kafka-go/kafkatrace), shared by the
    [kafka](./confluentinc/confluent-kafka-go/kafka/orchestrion.yml) and
    [kafka.v2](./confluentinc/confluent-kafka-go/kafka.v2/orchestrion.yml) contribs.
  - **`go:linkname` (when that is not possible).** If the helper must import the library you are
    injecting into, a normal import back would cycle. Reference the symbol with `go:linkname`
    (`inject-declarations` with `links:`) instead, which links at build time with no import. See
    [net/http](./net/http/orchestrion.client.yml), whose helper uses `*http.Request` and therefore
    imports `net/http`.

  Rule of thumb: if the helper can be written without importing the library, use a shared package and
  a normal import. Use `go:linkname` only when the helper must import the library.

### Avoiding circular imports

Orchestrion weaves aspects into every package it compiles. Its only automatic exclusions
([internal/toolexec/aspect/specialcase.go](https://github.com/DataDog/orchestrion/blob/main/internal/toolexec/aspect/specialcase.go))
are Orchestrion's own packages and dd-trace-go itself. It does not skip the library you are
instrumenting, including the package where the matched function is declared. So you must guard
against the cycle yourself.

This matters because libraries call their own constructors internally. `chi.NewRouter` returns
`chi.NewMux()`, so an aspect matching `chi.NewMux` also matches that call inside package `chi`.
Weaving there makes chi import your contrib, and your contrib imports chi, so the build fails with an
import cycle.

Guard against it by excluding the library's own packages from the join point with `not`. See
[chi](./go-chi/chi/orchestrion.yml) (excludes `github.com/go-chi/chi` and its `middleware`
subpackage) and the `Get|Head|Post|PostForm` aspect in [net/http](./net/http/orchestrion.client.yml)
(excludes `net/http`). The other cycle case, where injected code calls a helper whose package would
import back, is handled with `go:linkname` (see the modify-library-code pattern above).

## Context propagation and goroutine-local storage (GLS)

Auto-instrumentation links spans into a trace by propagating the active span through the call chain.
There are two mechanisms, and understanding them shapes how you instrument a library.

- **`context.Context` (preferred).** When code threads a `context.Context`, the tracer carries the
  active span in it, and `tracer.StartSpanFromContext` makes the new span a child of the one found in
  the context. Whenever the library gives you a context, propagate through it.
- **Goroutine-local storage (GLS), a fallback.** Many call sites do not pass a `context.Context`. For
  those, Orchestrion uses GLS to forward the active span implicitly within a goroutine.

### What GLS is

GLS is a per-goroutine storage slot. When a build is compiled with Orchestrion, it weaves a field
(`__dd_gls_v2`) onto the Go runtime's goroutine struct (`runtime.g`) and reaches it through
`go:linkname` (see `internal/orchestrion/gls.orchestrion.yml`). The tracer keeps a stack of context
values there, such as the active span. It is active only under Orchestrion. In a normal build the GLS
is inert and everything falls back to plain `context.Context`.

### What it does

- Starting a span pushes it onto the current goroutine's GLS (woven into the tracer's
  `ContextWithSpan`).
- `tracer.SpanFromContext` reads the GLS when the explicit context chain has no active span (woven
  into `SpanFromContext`). This lets a span created upstream be found downstream even across call
  sites that do not pass a context, which is what makes auto-instrumentation work without editing
  user code.
- Explicit context wins: a span found in the `context.Context` chain takes priority, and the GLS is
  only the fallback.

GLS is a tracer-internal mechanism (see the caveat in `gls.orchestrion.yml`) and not a public API.
Integration authors normally do not touch it directly: use `context.Context` and the tracer's
context-aware helpers, and the tracer plus Orchestrion manage the GLS for you.

### Limitation: goroutine boundaries

The GLS is per-goroutine, so a value on it does not follow work onto a new goroutine. If a span lives
only on the GLS (no explicit context carries it) and the code starts a goroutine, a span created
there is not linked to it:

```go
parent, ctx := tracer.StartSpanFromContext(ctx, "op") // parent is on this goroutine's GLS
go func() {
    // New goroutine: the GLS does not carry parent. With no context holding the
    // span, this starts a new, disconnected trace.
    child, _ := tracer.StartSpanFromContext(context.Background(), "child")
    child.Finish()
}()
```

Pass the explicit `context.Context` across the goroutine to keep the link:
`tracer.StartSpanFromContext(ctx, "child")`. When instrumenting a library that hands work to other
goroutines, rely on context-based propagation, not the GLS.

For propagating context across process boundaries (distributed tracing), see the context propagation
section in [INTEGRATIONS.md](./INTEGRATIONS.md).

## Integration tests

Auto-instrumentation is verified by the tests under
[internal/orchestrion/_integration](../internal/orchestrion/_integration). See its
[README](../internal/orchestrion/_integration/README.md) for how to write and run these tests.

- A test is a `TestCase` with `Setup`, `Run`, and `ExpectedTraces`.
- Name the base case `TestCase`. For several cases in one package, keep the `TestCase` prefix, for
  example `TestCaseSubrouter`. The generator registers every type whose name starts with `TestCase`.
  Put each distinct calling pattern in its own test case.
- The application code must not use the integration (contrib) package. Auto-instrumentation is what
  should add it, and that is what the test verifies. The code may import dd-trace-go's tracer to
  create a manual root span for the test.
- `ExpectedTraces` is a partial match, not a full one: it checks that the spans you list appear and
  ignores the rest. Make the assertions as complete as you can, asserting every span the trace should
  contain.
- Write one file per distinct calling convention the library supports: function literal, interface,
  closure, global convenience function, explicit construction, value versus pointer config. This
  exercises the join-point matchers and catches build breaks at compile time.

New `TestCase` types are not run by the test runner until the generated test files are regenerated.
After adding a `TestCase` or editing any `orchestrion.yml`, run `make generate`, which regenerates the
generated tests and `orchestrion/all`. CI fails if these are out of date.
