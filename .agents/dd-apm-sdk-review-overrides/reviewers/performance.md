Override for `reviewers/performance.md` (in the core skill folder) — read that file first, then this.

# Performance — dd-trace-go specifics

This file starts with one confirmed pattern and should grow — add the next
one you learn from review. Do not treat it as exhaustive.

The source of truth for the pattern below is [`contrib/AGENTS.md` § "Span Tag Performance in Contribs"](../../../contrib/AGENTS.md). Apply that section as written; do not paraphrase it into a weaker rule.

## `WithStartSpanConfig` must not be the first tag-setting option

`WithStartSpanConfig`'s tag merge *aliases* the cached base's `Tags` map onto the live span config when that config does not have a tags map yet. If `WithStartSpanConfig(cachedBase)` is the first tag-setting option, the next `Tag` / `WithTags` call mutates the shared cached base in place — every future span built from it inherits the last request's dynamic tags.

This is a silent cross-request data leak, not a style nit. Treat it as **P0**.

```go
// Wrong — cachedBase is aliased, then mutated by Tag().
tracer.StartSpanFromContext(ctx, "op.name",
    tracer.WithStartSpanConfig(cachedBase),
    tracer.Tag(ext.ResourceName, name),
)

// Right — a Tag/WithTags call runs first, so the live map is a fresh copy.
tracer.StartSpanFromContext(ctx, "op.name",
    tracer.WithTags(map[string]any{ext.ResourceName: name}),
    tracer.WithStartSpanConfig(cachedBase),
)
```

`TestWithStartSpanConfigAliasesCachedBaseWhenCalledFirst` in `ddtrace/tracer/option_test.go` reproduces this.

## Cached static tags vs per-call `Tag()` closures

Every `tracer.Tag(k, v)` call (and `ServiceName`, `ResourceName`, `SpanType`, …) allocates a new closure. Tags that never change between calls (`component`, `span.kind`, a configured service name) belong in a `tracer.NewStartSpanConfig(...)` built once at construction and applied with `tracer.WithStartSpanConfig(cfg)`. Two or more dynamic tags on one call site go in one `tracer.WithTags(tags)` map, not one `Tag()` each.

Do not flag a single dynamic `Tag()` on an otherwise-correct call site.

## How to add the next rule

1. Write the pattern here in the same shape: what it looks like, why it matters, the concrete fix.
2. Add a case in `.llm-validation/suites/dd-apm-sdk-review.yaml` that would fail if this paragraph disappeared.
3. That is the whole contribution.
