# dd-trace-go Coding Guidelines for AI Assistants

This file provides coding guidelines for AI coding assistants working in this repository.
Please follow these rules in addition to the patterns visible in the existing codebase.

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

### Public functions must encapsulate their preconditions

If a function requires its input to be in a particular form (normalized, sorted, trimmed),
do that transformation inside the function—do not externalize it as a caller obligation
encoded in the parameter name. A public function signature should describe what the caller
provides, not what the function needs internally.

```go
// Bad: "normalized" is a precondition pushed onto the caller; forces callers to know
// about and perform normalization before calling, and leaks the implementation detail
func GetCachedClusterID(normalizedBootstrapServers string) (string, bool)

// Good: the function accepts raw input and normalizes internally
func GetCachedClusterID(bootstrapServers string) (string, bool) {
    key := normalizeBootstrapServers(bootstrapServers)
    ...
}
```

## Code Quality

### Inject dependencies instead of duplicating bodies

When two functions share the same body and differ only in how a single dependency is
constructed, do not duplicate the body. Instead, extract the shared logic into a helper
that accepts the already-constructed dependency, and call it from both sites.

```go
// Bad: fetchClusterIDFromConsumer and fetchClusterIDFromProducer are identical
// except for how the admin client is constructed — the body is duplicated.

// Good: extract the shared body; each caller constructs and passes its own admin client
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

func fetchClusterIDFromConsumer(c *kafka.Consumer) string {
    admin, err := kafka.NewAdminClientFromConsumer(c)
    if err != nil { ... }
    defer admin.Close()
    return fetchClusterIDFromAdmin(admin)
}
```
