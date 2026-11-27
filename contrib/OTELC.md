# otelc

otelc ([OpenTelemetry Go compile-time instrumentation](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation))
is the second way dd-trace-go supports compile-time auto-instrumentation, next to
[Orchestrion](./ORCHESTRION.md). Like Orchestrion, it uses the Go `toolexec` mechanism to change the
code at build time, driven by rule files. New integrations must support both. This guide covers how
to add otelc support to an integration. For building the integration itself, see
[INTEGRATIONS.md](./INTEGRATIONS.md).

Within dd-trace-go, otelc lives in a few places:

- `contrib/<name>/otelc/`: each integration's rules and hooks, as its own Go module.
- [`otelc/all`](../otelc/all): blank-imports every integration's otelc module. Applications name it
  in their tool file, and otelc reads the rules of every module it imports.
- [`ddtrace/tracer/otelc.yaml`](../ddtrace/tracer/otelc.yaml): the tracer's own rules (build mode
  flag, goroutine-local storage, tracer start in `main`).
- [`.github/workflows/otelc.yml`](../.github/workflows/otelc.yml): the CI lane. `OTELC_VERSION`
  pins a commit on upstream `main`. `OTELC_REF` in
  [`.github/workflows/build-metrics.yml`](../.github/workflows/build-metrics.yml) pins the same
  version for the build metrics jobs. Bump both together.

References:

- **Rule format and every rule kind**:
  [docs/rules.md](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/docs/rules.md).
- **How hooks are linked and called, and the `HookContext` API**:
  [docs/implementation.md](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/docs/implementation.md).
- **Writing hooks**:
  [docs/instrument-guide.md](https://github.com/open-telemetry/opentelemetry-go-compile-contrib/blob/main/docs/instrument-guide.md)
  in the upstream instrumentation repo.
- **Diagnosing a build that instrumented nothing**:
  [docs/troubleshooting.md](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/docs/troubleshooting.md).
- **Existing examples**: the `contrib/*/otelc/` modules in this repo.

Check behavior against latest upstream `main`, not a release. otelc changes quickly.

## The otelc module

- Put the rules and hooks in `contrib/<name>/otelc/`, as its own module with path
  `github.com/DataDog/dd-trace-go/contrib/<name>/otelc/v2`. The hooks import
  `go.opentelemetry.io/otelc/pkg/hook`, which would otherwise land in the `go.mod` of every user of
  the contrib. Require the published `go.opentelemetry.io/otelc/pkg` tag the other hook modules use.
- The module needs at least one Go file, and it must import the contrib. A rules-only module
  blank-imports it (`_ "github.com/DataDog/dd-trace-go/contrib/<name>/v2"`).
- Name rule files `otelc.yaml` or `*.otelc.yaml`. Each rule's `path:` is this package.
- Never put rules under `internal/`. otelc imports the rule packages into the application's `main`
  package, so an internal package fails for every user. A rule's `target:` can still be internal.
- Do not add the file-level `version:` key. It rejects pseudo-versions, and `OTELC_VERSION` is one.
- Export the state that the hooks and the contrib share from the contrib package. The hook module
  cannot import `contrib/<name>/v2/internal/...`. Keep the hook layer thin, and keep the logic in
  the contrib where plain unit tests reach it.
- Comment a rule only with what a reader would not expect: why this rule kind, an odd exclusion, a
  known gap with an upstream issue link. A few lines, directly above the rule.

## Choosing a rule kind

Reuse the contrib's entry points (`WrapClient`, `Middleware`, `Open`). Do not reimplement tracing in
a rule. Use the rule kind that fits otelc best:

- **A constructor whose result the instrumentation changes** (a router that gets a middleware, a
  client that gets wrapped): a `wrap_call` rule on its call sites. The rewritten package imports
  the contrib, so Go initializes the contrib before a value built in a package-level variable
  (`var r = gin.New()`). Target `**` so it also reaches call sites in dependencies, and exclude the
  library and the contrib with `not:` entries, which also keeps the contrib's own calls from being
  traced twice. See [chi](./go-chi/chi/otelc/otelc.yaml) and [echo.v4](./labstack/echo.v4/otelc/otelc.yaml).
- **A library method that handles every request or call** (`ServeHTTP`, a driver's `Exec`):
  `inject_hooks`, with before and after hooks that call the contrib. The handlers run in the
  original call, never in the hook. See [gorilla/mux](./gorilla/mux/otelc/) and
  [database/sql](./database/sql/otelc/).
- **Code that must run inside the library package**, for example to read an unexported field:
  `inject_code`, with `add_struct_fields` and `add_file` when it needs new fields or functions. See
  [net/http](./net/http/otelc/otelc.yaml).
- **A struct literal**: `set_fields` to set or wrap one field, `wrap_literal` to wrap the whole
  literal.
- **A directive in user code**: `expand_directive`, as `//dd:span` does.

For `wrap_call` templates:

- Pass the call's arguments, or its result, to the wrapper as parameters. Never evaluate them inside
  a closure body, which runs later.
- When callers may run the call with `go` or `defer`, keep the call in the closure body and pass
  its arguments as the closure's parameters. See `fasthttp_serve_conn_func` in
  [fasthttp](./valyala/fasthttp/otelc/otelc.yaml).
- Use `{{ .CallArguments }}` to pass all of a call's arguments. A caller may pass a single call
  that returns every argument, which `{{ .CallArgument 1 }}` cannot handle.
- `{{ .CallReceiver }}` rebuilds a method call around a wrapped receiver.
- Read the caller's context with `{{ .FuncArgumentOfType "context.Context" }}` or
  `{{ .FuncArgumentThatImplements "context.Context" }}`.
- Give every rule import a private alias, such as `__dd_<name>`. Injected code uses the aliases as
  written, and the build fails when a name at the injection point or a declaration in the user's
  file has the same name as one of them.

## Avoiding double spans

A hook on a library function fires for every caller, the contrib included. When the contrib calls
the hooked function itself, the hook must not start a second span:

- Prefer a call-site rule that excludes the contrib from `target:`.
- For a hook, have the contrib mark the value the hook sees, such as the context or the request,
  and have the hook skip a marked call. Put that code in the contrib behind
  `instrumentation.OtelcEnabled()`, so plain builds pay only a bool check. See
  [redigo](./gomodule/redigo/otelc/hook.go) and [go-redis.v9](./redis/go-redis.v9/otelc_mark.go).
- Never target the contrib with an `inject_code` rule. A refactor of the contrib can make the rule
  match nothing with no error.

## Context propagation

otelc builds use the same goroutine-local storage as Orchestrion builds, woven by
`ddtrace/tracer/otelc.yaml`. Prefer `context.Context`, and see the GLS section of
[ORCHESTRION.md](./ORCHESTRION.md#context-propagation-and-goroutine-local-storage-gls) for how the
fallback works and where it stops.

## Wiring the module in

- Blank-import the module from `otelc/all/otel.instrumentation.go`.
- Add the module to `go.work`, then run `make fix-modules`. It wires local `replace` directives into
  sorted `replace (...)` blocks and tidies modules in dependency order.
- In `.github/ci-components.yml`, map both modules to the suite (a non-empty suite list for
  `contrib/<name>/otelc` registers the suite in the OTelc lane), and add `contrib/<name>/**` to the
  `otelc-integrations` component. For a suite covering code outside `contrib/` (or without a
  `*/otelc` module entry, such as `otelc-autostart` or `gls`), add the suite to
  `integration-suites.otelc-extra-suites`. CI discovers `**/otelc` modules and their suites
  automatically via `scripts/ciselect`, so `.github/workflows/otelc.yml` needs no edit.
- When a rule adds an import of the contrib, or the hooks use it, add a new `lib_<name>.go` file in
  `internal/apps/otelc-external-app` that calls `registerLibrary` from `init()` with a call that
  needs no network. The External Application Build job then checks that the
  rules module brings its contrib into an application that never imports it.

## Integration tests

The suites under [internal/orchestrion/_integration](../internal/orchestrion/_integration) run under
both tools, and must pass under both with the same spans. Write them as described in
[ORCHESTRION.md](./ORCHESTRION.md#integration-tests). For otelc, also:

- Add a test case for each call form a rule rewrites, including the library imported under an
  alias.
- For a `**` call-site rule, add a dependency module under `_integration/internal/<lib>dep/`
  (module `example.com/<lib>dep`) that calls the library, and a `TestCaseDependency` that calls it.
  Every other call site in the suites belongs to the `_integration` module, so they pass even if the
  rule never reaches a dependency.

Run a suite from `internal/orchestrion/_integration` with the otelc CLI at `OTELC_VERSION` first on
`PATH`:

```
otelc go test ./<suite>
```

A green run does not prove the rules are right, since the suites match spans by subset. Check
`.otelc-build/matched.json` for the rules that applied, and read the rewritten files under
`.otelc-build/debug/<package>/`. A hook that panics is recovered by otelc, so a missing span with
`failed to exec Before hook` on stderr means a broken hook, not a missing rule.
