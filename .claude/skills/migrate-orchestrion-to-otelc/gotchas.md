# otelc gotchas and workarounds

otelc limits that have a workaround, and mistakes that fail late or silently. Things with no
workaround are in `feature-gaps.md`.

## Hooks

- For a method, `GetParam` and `SetParam` count the receiver as parameter 0, so the first argument
  is 1. A wrong index fails at run time only, when the type assertion in the trampoline panics.
- On a generic target function, `GetParam`, `SetParam`, `GetReturnVal` and `SetReturnVal` panic
  for a parameter or result whose type uses a type parameter. Read the hook's own typed
  parameters, or use `inject_code`.
- `SetReturnVal` has no effect in a Before hook. In a before-only rule it does nothing, and when
  the rule also has an After hook it panics, which also cancels `SetSkipCall`
  (https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/issues/1608). Pair
  before and after hooks and pass the value through `SetData`/`GetData`.
- `inject_hooks` gives no init-ordering guarantee. The trampoline creates no import edge, so a
  function that other packages call from their own `init()` can run the hook before the hook
  module and the contrib are initialized. Queue those calls in package variables with no
  initializer and replay them from the hook module's `init()`
  (`contrib/database/sql/otelc/hooks.go`).
- A hook that builds state lazily must not keep a failed build. When a call comes before the
  contrib's `init`, the build panics and the trampoline recovers it. `sync.Once` and
  `sync.OnceValue` would then never retry, or replay the panic on every call, and the object stays
  untraced. Store the state only after the build returns, for example with `LoadOrStore`.
- Every hook call allocates: the trampoline builds a `HookContext` holding pointers to the
  parameters, and it escapes. On a hot path in our own code, prefer `inject_code`. On a library's
  per-request method, a hook that reuses the contrib is usually worth it, but benchmark it under
  otelc against the alternative before deciding.

## Rules

- `function_call` and `method_call` only match qualified calls (`pkg.Func()`). Calls inside the
  library itself, and calls through a dot import, are never matched, so a constructor that calls
  another one internally is not wrapped twice.
- Injected code uses a rule's `imports:` aliases as written. When the file already imports the
  path under another alias, otelc imports it again under the rule's alias. The build fails when a
  parameter, result or local at the injection point, or a declaration in the file, has the same
  name as an alias the code uses, so give every rule import a private alias such as
  `__dd_tracer`.
- `target: $root` never matches `package main`, whose compile-time import path is the literal
  string `main`. Only a `main` entry reaches it, so a rule for the root module and its main package
  lists both (`target: [$root, main]`). It also needs a main app test (see "Package main and
  dependency modules" in `SKILL.md`).
- Each rule kind decodes only its own fields and ignores unknown keys, so a selector the kind does
  not read is dropped without an error. `inject_hooks` reads `signature`, `signature_contains`,
  `result`, `last_result`, `param` and the `*_implements` filters, and `inject_code` reads the
  same ones except the `*_implements` filters. On any other kind the rule matches every function
  or call the other selectors allow.
- `where.file.has_directive` finds a directive only in the file's leading comments, above the
  `package` clause. On a `var` or `func` declaration, or between declarations, it is not found,
  and the rule then matches nothing while still reporting success.
- In `where.file`, one node takes one predicate. Two leaf predicates fail with "where.file has
  multiple active predicates", and a combinator next to another predicate fails with
  "where.file.`<name>` cannot be combined with other predicates". Write an explicit `all-of`.

## Green runs with nothing instrumented

- A hook that panics is swallowed by otelc's generated `recover()`. Build and tests stay green, and
  the only sign is a `failed to exec Before hook <name>` line on stderr. Suspect this whenever
  spans go missing without an error.
- `.otelc-build/` is gitignored here. If you validate from outside this repo, ignore it there too.
  otelc's troubleshooting doc covers `--debug`, `--stats` and `otelc cleanup`.
