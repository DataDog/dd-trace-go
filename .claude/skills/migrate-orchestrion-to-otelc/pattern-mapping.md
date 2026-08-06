# Orchestrion aspect patterns → otelc

Reproduce each aspect's behaviour and nothing more. Exact syntax and semantics come from the sources
in `references.md`.

## Advice → otelc rule

| Orchestrion advice | otelc equivalent |
|---|---|
| `wrap-expression` on a `function-call` | `wrap_call` with a `replace` template, or an `inject_hooks` after-hook when acting on the constructor's result is easier |
| `append-args` | `wrap_call` with `append_args`, plus `variadic_type` when the matched call spreads a slice |
| `replace-function` | before-hook: `SetSkipCall(true)` + call the drop-in the aspect points to + `SetReturnVal`; guard re-entrancy if the drop-in calls the target again |
| `prepend-statements` in a `function-body` | `inject_hooks` before/after hook, or `inject_code` when the code must run in-package |
| `add-struct-field` | `add_struct_fields`, exported if a hook must read it |
| `assign-value` | `assign_value`: `replace:` for a new expression, `wrap:` to keep the original as `{{ . }}` |
| `inject-declarations` (`go:linkname` to the contrib) | usually nothing: otelc links external hooks through its own trampoline. Only needed if in-package `inject_code` must reference a contrib symbol |
| `add-blank-import` | the rule's top-level `imports:` map |

## Join point → otelc selector

| Orchestrion join point | otelc |
|---|---|
| `function-body` over `function` (`name`, `receiver`) | `where: {func:, recv:}` |
| `function` with `signature` / `signature-contains` | `where:` sub-filters `signature`, `signature_contains`, `result`, `last_result`, `param` |
| `function-call` | `where: {function_call: "import/path.Func"}` |
| `struct-definition` | `where: {struct:}` |
| `declaration-of`, `value-declaration` | `where: {identifier:, kind: var\|const}` |
| `directive` (`dd:span`) | `where: {directive:}` with `expand_directive`, but the template reads only `{{FuncName}}`, not the directive's arguments (`feature-gaps.md`) |
| `import-path`, `package-name`, `package-filter` | `target:`, exact or glob; `$root` for the module being built |
| `test-main` | nothing. `target: test_main` is unsupported; `where.file.is_test` gates files inside a point-selector rule |
| `all-of` | flat keys in one `where` are an implicit conjunction |
| `one-of` | no combinator: one rule per alternative |
| `not` | see `feature-gaps.md` |

`version: <start>,<end>` binds a rule to a range of the target library. Orchestrion has no
equivalent, so use it only when the hook depends on that range.

## Porting a template to `inject_code`

`inject_code` takes a raw string (`raw:`) with no template variables:
- Spell the receiver and parameter names exactly as the target's source does.
- Results the target leaves unnamed become `_unnamedRetVal0`, `_unnamedRetVal1`, and so on.
- Imports the snippet needs come from the rule's top-level `imports:`.
- It lands at the top of the body, unless `pattern:` plus `placement: before|after` anchor it to a
  statement.

Only workable when the target is our own code. Guard the coupling with a test that parses the source
with `go/ast` and asserts the identifiers are unchanged: without it a rename fails as a compile
error inside an instrumented copy of the package instead of pointing at the yaml
(`ddtrace/tracer/gls_otelc_identifiers_test.go`).

## Reaching state the aspect read in-package

Hooks are external. They can read and write exported fields, including ones added by
`add_struct_fields`, but not the library's unexported fields. If the aspect read an unexported
field, either wrap a public accessor's return value or use `inject_code` for that piece.

## Reuse rule

Every hook calls the existing contrib entrypoint (`WrapClient`, `Middleware`, `Open`, ...). The
tracing logic stays in the contrib.
