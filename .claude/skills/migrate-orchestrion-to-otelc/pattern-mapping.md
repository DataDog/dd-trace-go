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
| `wrap-expression` on a `struct-literal` | `where: {struct_literal:}` with `set_fields`: `value:` to set a field, `wrap:` to wrap what the literal already assigns |
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
| `struct-literal` | `where: {struct_literal: "import/path.TypeName"}`, matching literals written in the `target:` package |
| `declaration-of`, `value-declaration` | `where: {identifier:, kind: var\|const}` |
| `directive` (`dd:span`) | `where: {directive:}` with `expand_directive`; the template reads `.DirectiveArgs` / `.DirectiveArg <key>` for the directive's own arguments |
| `import-path`, `package-name`, `package-filter` | `target:`, exact or glob; `$root` for the module being built |
| `test-main` | nothing. `target: test_main` is unsupported; `where.file.is_test` gates files inside a point-selector rule |
| `all-of` | flat keys in one `where` are an implicit conjunction |
| `one-of` | no combinator: one rule per alternative |
| `not` | see `feature-gaps.md` |

`version: <start>,<end>` binds a rule to a range of the target library. Orchestrion has no
equivalent, so use it only when the hook depends on that range.

## Porting a template to `inject_code`

`inject_code` takes a raw string (`raw:`) rendered as a `text/template`, so the snippet reads the
matched function instead of naming its identifiers:
- `.FuncArgument N` for a parameter, `.Receiver` for a method's receiver, `.FuncReturn N` for a
  result, and `.FuncArgumentOfType` / `.FuncReturnOfType` to pick one by type rather than position.
- Parameters and results the target leaves unnamed, or names `_`, are given a synthetic name the
  first time the template reads them.
- Imports the snippet needs come from the rule's top-level `imports:`.
- It lands at the top of the body, unless `pattern:` plus `placement: before|after` anchor it to a
  statement.

Resolve every identifier through a template variable. A literal name survives only until someone
renames it in the target, and then the build fails as a compile error inside an instrumented copy of
the package, which points nowhere near the yaml. `ddtrace/tracer/otelc.yaml` does this throughout.

Only workable when the target is our own code.

## Reaching state the aspect read in-package

Hooks are external. They can read and write exported fields, including ones added by
`add_struct_fields`, but not the library's unexported fields. If the aspect read an unexported
field, either wrap a public accessor's return value or use `inject_code` for that piece.

## Reuse rule

Every hook calls the existing contrib entrypoint (`WrapClient`, `Middleware`, `Open`, ...). The
tracing logic stays in the contrib.
