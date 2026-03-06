# dd-trace-go Coding Guidelines for AI Assistants

This file provides coding guidelines for AI coding assistants working in this repository.
Please follow these rules in addition to the patterns visible in the existing codebase.

## Formatting

Always run `make format` (or `gofmt -w ./...`) before committing. CI enforces `gofmt` via
golangci-lint and will reject unformatted files. Prefer `make lint` to catch all issues before
pushing.

## Instrumentation Library Principles

### Never block in library initialization

Any I/O or external data fetching done during `New*`, `Wrap*`, or `Setup*` calls must be
asynchronous. Users do not expect their observability library to add latency to their service
startup. Use goroutines for best-effort enrichment (e.g. fetching cluster IDs, metadata).

If the async work holds resources that must be released, ensure `Close()` waits for completion
(e.g. via `sync.WaitGroup` or channel drain) before the underlying resource is closed.

```go
// Bad: blocks the caller's goroutine for up to 2s
func NewConsumer(conf *kafka.ConfigMap, opts ...Option) (*Consumer, error) {
    c, _ := kafka.NewConsumer(conf)
    clusterID := fetchClusterID(c) // synchronous network call
    ...
}

// Good: returns immediately; enrichment happens in the background
func NewConsumer(conf *kafka.ConfigMap, opts ...Option) (*Consumer, error) {
    c, _ := kafka.NewConsumer(conf)
    wrapped := WrapConsumer(c, opts...)
    wrapped.tracer.FetchClusterIDAsync(func() string { return fetchClusterID(c) })
    return wrapped, nil
}
```

### `WithX` option functions are reserved for user-facing APIs

`WithX` option functions (e.g. `WithServiceName`, `WithAnalytics`) are public API surface
intended to be called by users of the library. Do not create `WithX` options to carry
internal-only state between library layers. Use unexported struct fields, unexported setter
methods, or internal configuration types instead.

```go
// Bad: WithClusterID used internally only, not meant for users
opts = append(opts, WithClusterID(clusterID))

// Good: use an unexported setter
wrapped.tracer.SetClusterID(clusterID)
```

## API Design

### Order parameters from broadest to narrowest scope

Arrange function parameters from the broadest logical scope to the most specific. For Kafka
tracing functions the hierarchy is: `cluster` → `group` → `topic` → `partition` → `offset`.

When adding a broader-scope parameter to an existing function, place it before the narrower
ones rather than appending it at the end.

```go
// Bad: cluster appended at the end despite being the broadest scope
func TrackKafkaCommitOffsetWithCluster(group, topic string, partition int32, offset int64, cluster string)

// Good: cluster first
func TrackKafkaCommitOffsetWithCluster(cluster, group, topic string, partition int32, offset int64)
```

### Keep implementation details out of public function signatures

Parameter names in exported functions must describe the caller's logical intent, not how the
function processes the input internally. If a function normalizes, sorts, or transforms its
input, do that inside—don't encode the processing state in the parameter name.

```go
// Bad: "normalized" is an implementation detail; forces callers to know about it
func GetCachedClusterID(normalizedBootstrapServers string) (string, bool)

// Good: callers provide bootstrap servers; normalization is internal
func GetCachedClusterID(bootstrapServers string) (string, bool)
```

## Code Quality

### Avoid repeated type assertions

Perform a type assertion once and assign to a local variable when the result is used more
than once. Repeated `x.(T)` expressions on the same value are a code smell.

```go
// Bad
if bs.(string) == "" {
    return fetchFn()
}
normalized := normalize(bs.(string))

// Good
bsStr := bs.(string)
if bsStr == "" {
    return fetchFn()
}
normalized := normalize(bsStr)
```

### Extract helpers for near-identical code blocks

When two functions share the same body and differ only in how a single dependency is
constructed, extract the shared logic into a helper that accepts the already-constructed
dependency. Do not leave near-duplicate blocks in place.

```go
// Bad: fetchClusterIDFromConsumer and fetchClusterIDFromProducer are identical except
// for how the admin client is constructed.

// Good: extract shared logic
func fetchClusterIDFromAdmin(admin *kafka.AdminClient) string {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    clusterID, err := admin.ClusterID(ctx)
    if err != nil {
        instr.Logger().Warn("failed to fetch Kafka cluster ID: %s", err)
        return ""
    }
    return clusterID
}
```

### Consider input-scale assumptions for normalization algorithms

When writing key-normalization logic (sorting, hashing, joining), note the expected scale of
the input. For inputs that could be large (e.g. a Kafka `bootstrap.servers` list with many
brokers), prefer an O(n) algorithm or document the size assumption explicitly if O(n log n)
is acceptable.
