---
name: apm-integrations
description: |
  dd-trace-go integration (contrib) development guide. Use when creating, reviewing, or
  debugging a contrib integration or its Orchestrion auto-instrumentation. Covers package
  path and module registration, interception patterns, functional options, span tags and
  naming, orchestrion.yml aspects, and the _integration test suite.
  Triggers: "contrib", "integration", "new integration", "instrument <library>", "trace
  <library>", "orchestrion", "orchestrion.yml", "auto-instrumentation", "join point",
  "aspect", "wrap-expression", "replace-function", "add-struct-field", "_integration
  test", "TestCase", "instrumentation.Load", "contribIntegrations", "packages.go",
  "PackageInfo", "make fix-modules", "span.kind", "component tag", "WithService".
---

# dd-trace-go APM integrations

An integration ("contrib") instruments a third-party or standard library so users get Datadog
spans without writing them by hand. Each one is a nested Go module under `/contrib`.

Two guides hold the detail. Read the section you need rather than the whole file, unless you are
building a new integration from scratch, in which case read the first one end to end.

- [`/contrib/INTEGRATIONS.md`](/contrib/INTEGRATIONS.md) is the authoring guide.
- [`/contrib/ORCHESTRION.md`](/contrib/ORCHESTRION.md) is the auto-instrumentation guide.

## Workflow for a new integration

1. **Analyze the library.** Pick which operations get a span, decide each one's `span.kind`, note
   which cross a process boundary, and find how the library lets you hook in.
   [§1](/contrib/INTEGRATIONS.md#1-analyze-the-library)
2. **Place the package.** `contrib/<path>` mirroring the instrumented import path, with the version
   suffix rules below. [§2](/contrib/INTEGRATIONS.md#2-package-path-module-and-files)
3. **Pick an interception pattern**, highest in the list that fits.
   [§3](/contrib/INTEGRATIONS.md#3-interception-patterns)
4. **Write the entrypoints** as variadic functional options, with at least `WithService` and
   `WithCustomTag`. [§4](/contrib/INTEGRATIONS.md#4-entrypoints-and-functional-options)
5. **Set tags and names.** [§5](/contrib/INTEGRATIONS.md#5-spans-tags-and-naming), and
   [§6](/contrib/INTEGRATIONS.md#6-choosing-tag-names) for choosing a tag name.
6. **Register it** in three places: `instrumentation.Load` in an `init`, `instrumentation/packages.go`,
   and `contribIntegrations`. [§7](/contrib/INTEGRATIONS.md#7-register-the-integration)
7. **Propagate context** across process boundaries with `tracer.Inject` and `tracer.Extract`.
   [§8](/contrib/INTEGRATIONS.md#8-context-propagation)
8. **Add Orchestrion support**: an `orchestrion.yml`, plus test cases under
   `internal/orchestrion/_integration`. Both are mandatory.
   [ORCHESTRION.md](/contrib/ORCHESTRION.md)
9. **Test.** Table-driven, about 90% coverage, and a package-level `Example` in `example_test.go`.
   [§10](/contrib/INTEGRATIONS.md#10-testing)
10. **Run the pre-PR sequence** below.

## Rules that are easy to get wrong

State these before reaching for the guides. Each links to the reasoning.

- **Interception pattern order.** Native hook or observer, then native middleware or interceptor,
  then constructor replacement with an unchanged signature, then interface-returning wrapper, then
  concrete-type wrapper. Only the last one changes the return type, which makes it unsafe for
  auto-instrumentation and a last resort.
  [§3](/contrib/INTEGRATIONS.md#3-interception-patterns)
- **The `component` tag needs a `string()` cast.** `tracer.Tag(ext.Component, string(instrumentation.PackageX))`.
  The tracer type-asserts this value to `string`, so passing the typed constant leaves the span
  attributed to `manual`. [§5](/contrib/INTEGRATIONS.md#required-tags)
- **No naming schema for new integrations.** Leave `naming` unset in `packages.go`, do not call
  `instr.OperationName`, and hardcode operation names as string literals. `instr.ServiceName` with
  no `naming` entry returns the global `DD_SERVICE`, which is what you want.
  [§5](/contrib/INTEGRATIONS.md#service-name)
- **`contribIntegrations` takes the traced package's import path**, the same value as
  `TracedPackage`, not the contrib module path.
  [§7](/contrib/INTEGRATIONS.md#7-register-the-integration)
- **`.vN` goes on the element that carried `/vN`**, and any subpackage after it stays unsuffixed.
  `go.mongodb.org/mongo-driver/v2/mongo` becomes `contrib/go.mongodb.org/mongo-driver.v2/mongo`.
  The module's own trailing `/v2` is dd-trace-go's major version and is unrelated.
  [§2](/contrib/INTEGRATIONS.md#version-suffix)
- **The Go package name stays unversioned.** `contrib/redis/go-redis.v9` is `package redis`.
  [§2](/contrib/INTEGRATIONS.md#version-suffix)
- **`go work use ./contrib/<path>` runs before `make fix-modules`.** The tidy step fails on a module
  that is not in the workspace yet. [§2](/contrib/INTEGRATIONS.md#module)
- **Never put `tracer.WithStartSpanConfig(cachedBase)` first** in an option list. It aliases the
  cached base's tag map instead of copying it, so the next `Tag`/`WithTags` call mutates the shared
  base. At least one tag-setting option must precede it.
  [§5](/contrib/INTEGRATIONS.md#tag-performance)
- **Exclude the library's own packages from a join point** with `not`. Orchestrion does not skip the
  library you are instrumenting, and libraries call their own constructors internally, so an
  unguarded aspect produces an import cycle.
  [Avoiding circular imports](/contrib/ORCHESTRION.md#avoiding-circular-imports)
- **`_integration` test apps must not import the contrib package.** Auto-instrumentation is what
  should add it, and that is the thing under test.
  [Integration tests](/contrib/ORCHESTRION.md#integration-tests)
- **One `_integration` file per calling convention.** Function literal, interface, closure, global
  convenience function, explicit construction, value versus pointer config. Aspects match how code
  is written, so an untested convention can silently fail to weave.
  [Integration tests](/contrib/ORCHESTRION.md#integration-tests)
- **A span that lives only on the GLS does not cross a goroutine boundary.** Pass the explicit
  `context.Context` into the goroutine.
  [Limitation](/contrib/ORCHESTRION.md#limitation-goroutine-boundaries)

## Before opening a PR

Run in order and commit everything they produce. CI fails on a non-clean `git diff`.

1. `go work use ./contrib/<path>` for a new module, then `make fix-modules`.
2. `make generate`. Also required after adding a `TestCase` or editing any `orchestrion.yml`. If it
   changes a `go.mod`, run `make fix-modules` again.
3. `make lint` and `make format`.
4. The integration's own tests, for example `go test ./contrib/net/http/...`.
5. The auto-instrumentation tests:

   ```
   cd internal/orchestrion/_integration
   go run github.com/DataDog/orchestrion go test ./<package>/...
   ```

Full checklist: [§11](/contrib/INTEGRATIONS.md#11-before-you-open-a-pr).
