---
name: apm-integrations
description: |
  dd-trace-go integration (contrib) development guide. Use when creating, reviewing, or
  debugging a contrib integration or its Orchestrion auto-instrumentation.
  Triggers: "add a new integration", "instrument a library", "contrib", "contrib module",
  "orchestrion", "orchestrion.yml", "auto-instrumentation", "join point",
  "wrap-expression", "_integration test".
---

# dd-trace-go APM integrations

An integration ("contrib") instruments a library so users get spans without writing them by
hand. Each is a nested Go module under `/contrib`.

- [`/contrib/INTEGRATIONS.md`](/contrib/INTEGRATIONS.md), the authoring guide.
- [`/contrib/ORCHESTRION.md`](/contrib/ORCHESTRION.md), the auto-instrumentation guide.

Every rule lives in those guides, not here. Open the linked section, not the whole file. Read
INTEGRATIONS.md end to end only when building a new integration from scratch.

## Workflow

1. Analyze the library: which operations get a span, each one's `span.kind`, which cross a
   process boundary, how it lets you hook in.
   [§1](/contrib/INTEGRATIONS.md#1-analyze-the-library)
2. Place the package and module.
   [§2](/contrib/INTEGRATIONS.md#2-package-path-module-and-files)
3. Pick the highest interception pattern that fits.
   [§3](/contrib/INTEGRATIONS.md#3-interception-patterns)
4. Write entrypoints taking variadic functional options.
   [§4](/contrib/INTEGRATIONS.md#4-entrypoints-and-functional-options)
5. Set tags, service name and operation name.
   [§5](/contrib/INTEGRATIONS.md#5-spans-tags-and-naming),
   [§6](/contrib/INTEGRATIONS.md#6-choosing-tag-names)
6. Register in three places: `instrumentation.Load` in an `init`, `instrumentation/packages.go`,
   and `contribIntegrations`.
   [§7](/contrib/INTEGRATIONS.md#7-register-the-integration)
7. Propagate context across process boundaries.
   [§8](/contrib/INTEGRATIONS.md#8-context-propagation)
8. Add `orchestrion.yml` and `internal/orchestrion/_integration` tests. Both mandatory.
   [orchestrion.yml](/contrib/ORCHESTRION.md#orchestrionyml),
   [Integration tests](/contrib/ORCHESTRION.md#integration-tests)
9. Write tests and a package-level `Example`.
   [§10](/contrib/INTEGRATIONS.md#10-testing)
10. Run the pre-PR command sequence and commit everything it produces.
    [§11](/contrib/INTEGRATIONS.md#11-before-you-open-a-pr)
