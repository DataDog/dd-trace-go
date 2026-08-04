# Orchestrion aspect patterns → otelc

For each aspect in the integration's `orchestrion.yml`, write the otelc rule that does the **same
thing**. Do not introduce mechanisms the aspect does not use. Take exact rule syntax from the otelc
sources in `references.md`, and exact orchestrion semantics from the orchestrion sources there.

## Advice → otelc rule

| Orchestrion advice | otelc equivalent |
|---|---|
| `wrap-expression` on a `function-call` | `wrap_call` (call-site `replace` template), or an `inject_hooks` after-hook when it is easier to act on the constructor's result |
| `append-args` | `wrap_call` with `append_args` |
| `replace-function` / `redirect-call` | before-hook: `SetSkipCall(true)` + call the same drop-in the aspect points to + `SetReturnVal`; guard re-entrancy if the drop-in calls the target again |
| `prepend-statements` in a `function-body` | `inject_hooks` before/after hook, or `inject_code` if the code must run in-package |
| `add-struct-field` | `add_struct_fields` (make the field **exported** if a hook must read it) |
| `inject-declarations` (`go:linkname` to the contrib) | usually unnecessary: the hook is an external package and otelc links it via its own trampoline. Only needed if in-package `inject_code` must reference a contrib symbol |

Join points map directly: `function-call` → `function_call`; `function-body` func/recv → `func`/`recv`;
`struct-definition` → `struct`; the `all-of`/`one-of`/`not` combinators have otelc equivalents.
Confirm names and shape against the sources.

## Porting a template to `inject_code`

Orchestrion templates name parameters positionally (`.Function.Argument 1`, `.Function.Receiver`).
otelc's `inject_code` takes a **raw string with no template variables**, so the injected code has to
spell the receiver and parameter names exactly as the target's source does.

That is fine when the target is our own code, and unusable when it is a third-party library whose
parameter names you do not control. For our own code, write the names literally and add a test that
parses the source with `go/ast` and asserts they are unchanged. A rename normally breaks the otelc
build, since the injected code then names something that does not exist, but the failure surfaces as
a compile error inside an instrumented copy of the package rather than pointing at the rule. The
guard test turns it into a plain `go test` failure that names the yaml.
(`ddtrace/tracer/gls_otelc_identifiers_test.go` is the worked example.)

## Reaching state the aspect read in-package

An otelc hook is an external package. It can read/write **exported** fields (including exported fields
added by `add_struct_fields`), but not the library's original **unexported** fields. If the
orchestrion aspect read an unexported field (its `prepend-statements` ran in-package), reproduce it
one of two ways:
- wrap a public accessor's return value (stay external), or
- use `inject_code` (raw, in-body, runs in-package) for that piece.

## Reuse rule

Every hook calls the existing contrib entrypoint (`WrapClient`, `Middleware`, `Open`, ...). The otelc
layer is glue that reproduces the orchestrion aspect; the tracing logic stays in the contrib.
