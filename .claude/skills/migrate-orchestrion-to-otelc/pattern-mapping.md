# Orchestrion aspect patterns → otelc

Reproduce each aspect's behaviour and nothing more. Exact syntax and semantics come from the sources
in `references.md`.

## Advice → otelc rule

| Orchestrion advice | otelc equivalent |
|---|---|
| `wrap-expression` on a `function-call` | `wrap_call` with a `replace` template, or an `inject_hooks` after-hook when acting on the constructor's result is easier. The template reads the enclosing function with `.FuncArgumentOfType` / `.FuncReturnOfType` and the call with `.CallArgument N`, so an in-scope `context.Context` can be passed into the call |
| `append-args` | `wrap_call` with `append_args`, plus `variadic_type` when the matched call spreads a slice |
| `replace-function` | paired before/after hooks: the before-hook calls the drop-in, keeps its result with `SetData` and calls `SetSkipCall(true)`; the after-hook writes it with `SetReturnVal`. Guard re-entrancy if the drop-in calls the target again |
| `prepend-statements` in a `function-body` | `inject_hooks`, or `inject_code` when the code must run in-package |
| `add-struct-field` | `add_struct_fields`. A hook cannot read the new field (see below) |
| `wrap-expression` on a `struct-literal` | `where: {struct_literal:}` with `set_fields`: `value:` to set a field, `wrap:` to wrap what the literal already assigns |
| `assign-value` | `assign_value`: `replace:` for a new expression, `wrap:` to keep the original as `{{ . }}` |
| `inject-declarations` (`go:linkname` to the contrib) | usually nothing, since otelc links hooks through its own trampoline. Only needed if `inject_code` must reference a contrib symbol |
| `add-blank-import` | the rule's top-level `imports:` map |

## Join point → otelc selector

| Orchestrion join point | otelc |
|---|---|
| `function-body` over `function` (`name`, `receiver`) | `where: {func:, recv:}` |
| `function` with `signature` / `signature-contains` | `where:` filters `signature`, `signature_contains`, `result`, `last_result`, `param`. Only `inject_hooks` reads them; other rule kinds drop them silently |
| `function-call` | `where: {function_call: "import/path.Func"}` |
| `function-call` on a method | `where: {method_call: "import/path.*Type.Method"}`, without `*` for a value receiver. Never matches a call through an interface-typed receiver |
| `struct-definition` | `where: {struct:}` |
| `struct-literal` | `where: {struct_literal: "import/path.TypeName"}`, matching literals written in the `target:` package |
| `declaration-of`, `value-declaration` | `where: {identifier:, kind: var\|const}` |
| `directive` (`dd:span`) | `where: {directive:}` with `expand_directive`. The template reads the directive's arguments with `.DirectiveArgs` / `.DirectiveArg <key>` |
| `import-path`, `package-name`, `package-filter` | `target:`, exact or glob; `$root` for the module being built |
| `test-main` | nothing; see `feature-gaps.md` |
| `all-of` | flat keys in one `where` are an implicit conjunction |
| `one-of` | one rule per alternative |
| `not` | a `target:` list with `not:` entries when it excludes packages; otherwise see `feature-gaps.md` |

`//orchestrion:ignore` in source maps to `//otelc:ignore`, on a function, a file, or the
statement holding a call. otelc does not read the orchestrion directive, so a fixture that uses it
needs both. The `docs/configuration.md` section on source directives lists the limits.

`version: <start>,<end>` binds a rule to a range of the target library. Orchestrion has no
equivalent, so use it only when the hook depends on that range.

## Porting a template to `inject_code`

`inject_code` takes a raw string (`raw:`) rendered as a `text/template`, so the snippet reads the
matched function instead of naming its identifiers:
- `.FuncArgument N` for a parameter, `.Receiver` for a method's receiver, `.FuncReturn N` for a
  result, and `.FuncArgumentOfType` / `.FuncReturnOfType` to pick one by type rather than position.
- Parameters and results the target leaves unnamed, or names `_`, get a synthetic name the first
  time the template reads them.
- Imports the snippet needs come from the rule's top-level `imports:`.
- It lands at the top of the body, unless `pattern:` plus `placement: before|after` anchor it to a
  statement.

Resolve every identifier through a template variable, as `ddtrace/tracer/otelc.yaml` does. A
literal name breaks when the target renames it, and the compile error then points inside an
instrumented copy of the package, nowhere near the yaml.

The snippet compiles inside the target package, so the target module's `go` directive caps its
language version. Against an old dependency, avoid `any` and generic calls.

## Reaching state the aspect read in-package

Hooks compile against the unmodified library. They can read and write its exported fields, but not
its unexported ones, and not fields added by `add_struct_fields`. For either, wrap a public
accessor's return value, or read the field in-package with `inject_code`
(`transport_round_trip_tracer_internal` in `contrib/net/http/otelc/otelc.yaml`).
