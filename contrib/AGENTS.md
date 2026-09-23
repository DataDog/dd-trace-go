**BEFORE writing or editing ANY code**, you MUST read [README.md](./README.md) for information about:

* Naming conventions
* Testing
* Steps for creating new contribs

## Updating Documentation

The developer should update [README.md](./README.md) with any details that must be applied to most/all contribs, for example:

1. Tags and attributes that must be applied to all spans
2. Files and/or functions that every contrib must support
3. Semantic versioning and naming

If these updates are not made, tell the developer to make changes or provide suggestions if requested.

## Span Tag Performance in Contribs

### Favor a cached `StartSpanConfig` and `WithTags` over per-operation `Tag` closures

Every `tracer.Tag(k, v)` call (and the wrappers built on it: `ServiceName`, `ResourceName`, `SpanType`, `AnalyticsRate`, `Measured`) allocates a new closure. In a span-start call issued once per request or message, calling several of these on every span rebuilds the same closures every time. There are two independent tools for this, matched to whether a tag's value is invariant for the object's lifetime or differs on every call — pick the wrong one and you either miss the optimization or pay for one that doesn't apply:

- **Tags that never change between calls** (`component`, `span.kind`, a client's configured service name, `db.system`, ...): build them **once**, when the client/hook is constructed, with `tracer.NewStartSpanConfig(...)`, and apply the result on every span with `tracer.WithStartSpanConfig(cfg)`. See [MIGRATING.md](../MIGRATING.md#newstartspanconfig-withstartspanconfig-newfinishconfig--withfinishconfig) for the mechanics.
- **Tags that differ on every call** (a resource name, a message offset, a Kafka partition, ...) cannot be hoisted into that cached config — build them where the call happens. If it's a single dynamic tag, plain `Tag(k, v)` is simplest. If a call site sets **two or more** dynamic tags, gather them into a `map[string]any` and set them in one call with `tracer.WithTags(tags)` instead of one `Tag()` call per entry. `WithTags` copies the map's entries into the span's tag map — it never retains or mutates the map you pass in, so the same map can be safely reused or discarded by the caller.
- Do **not** reach for `NewStartSpanConfig` as a substitute for `WithTags` on the per-call path. `NewStartSpanConfig` is a build-once-reuse-many tool (see its doc comment); constructing one fresh on every call adds a `StartSpanConfig` allocation on top of the same per-tag closures `WithTags` avoids, for no benefit (measured below).

**Ordering is a correctness requirement, not a style preference.** At least one `Tag`/`WithTags` call must precede `WithStartSpanConfig(cachedBase)` in the option list — never call `WithStartSpanConfig(cachedBase)` as the *first* tag-setting option:

```go
// Correct: a dynamic tag call runs before the cached base.
tracer.StartSpanFromContext(ctx, "op.name",
    tracer.WithTags(map[string]any{ext.ResourceName: name}),
    tracer.WithStartSpanConfig(cachedBase),
)
```

`WithStartSpanConfig`'s tag merge *aliases* the base config's `Tags` map onto the span's live config when that config doesn't have one yet, instead of copying it. If `WithStartSpanConfig(cachedBase)` is the first tag-setting option, the live config's tags map *is* `cachedBase`'s map, and the next `Tag`/`WithTags` call mutates the cached, shared base in place — corrupting every future span built from it. `TestWithStartSpanConfigAliasesCachedBaseWhenCalledFirst` in `ddtrace/tracer/option_test.go` reproduces this concretely.

Once a `Tag`/`WithTags` call has run first, the live tags map is a fresh, distinct map, and `WithStartSpanConfig(cachedBase)` copies the base into it instead of aliasing it. From that point on, a *further* `Tag`/`WithTags` call is both safe and useful: it can run *after* `WithStartSpanConfig(cachedBase)` to override a cached tag on key collision, which is otherwise impossible, since the cached base wins collisions during its own merge. This is how per-message custom tags (`WithConsumerCustomTag`/`WithProducerCustomTag` in `contrib/IBM/sarama` and `contrib/confluentinc/confluent-kafka-go/kafkatrace`) are guaranteed to win over the cached static tags:

```go
// Also correct: a second WithTags call after the cached base, so a
// caller-supplied custom tag overrides a same-key cached tag.
tracer.StartSpanFromContext(ctx, "op.name",
    tracer.WithTags(dynamicTags),        // non-nil tags map before the base
    tracer.WithStartSpanConfig(cachedBase),
    tracer.WithTags(customTags),         // wins over cachedBase on collision
)
```

**Measured cost** (`BenchmarkTagsVsWithTags` in `ddtrace/tracer/option_test.go`; allocs/op — the stable, hardware-independent signal, unlike ns/op which was noisy on the measuring machine). `n` is the number of dynamic tags set per call, taken from real contrib call sites: n=1 is rueidis/valkey's default case, n=2 is the same with their opt-in raw-command tag, n=4 is franz-go/kafka-go/sarama's consumer spans, n=6 is mongo-driver's `Started` handler with query capture:

| n dynamic tags | no caching at all (pre-migration) | `Tag()` × n + cached base | `WithTags` + cached base |
| -: | -: | -: | -: |
| 1 | 31 | 26 | 24 |
| 2 | 32 | 27 | 24 |
| 4 | 37 | 33 | 27 |
| 6 | 41 | 37 | 29 |

Caching the static base pays off immediately (31→26 allocs at n=1). `WithTags` saves a further 2 allocs over plain `Tag()` calls even at n=1, growing to 8 fewer allocs (22%) at n=6 — so the more dynamic tags a call site sets, the more it has to gain from batching them into one `WithTags` call.

Sample PR: <https://github.com/DataDog/dd-trace-go/pull/5007>
