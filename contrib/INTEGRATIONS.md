# Authoring an integration

This guide covers how to build a new dd-trace-go integration (a "contrib"): a package that
instruments a third-party or standard library. Instrumentation here means observing the library's
operations, such as network calls, database queries, and request handling, and producing Datadog
spans for them, without changing what the library does. For the higher level contrib overview and
the list of existing integrations, see [README.md](./README.md).

A good integration:

1. Traces the operations that matter in the target library.
2. Supports compile-time auto-instrumentation.
3. Follows the conventions below, so it behaves consistently with every other integration.

## 1. Analyze the library

Before writing code, understand what to trace and how the library lets you hook in.

- Pick the operations worth a span: network or RPC calls, database queries, message publish and
  consume, incoming request handling. Skip pure in-process helpers.
- Decide the `span.kind` for each operation: `client`, `server`, `producer`, or `consumer`. Omit the
  tag when the value would be `internal` (the default).
- Note any operation that crosses a process boundary. Those need trace context propagation, see
  [Context propagation](#8-context-propagation).
- Identify the library's public API shape, which decides the interception pattern in
  [section 3](#3-interception-patterns):
  - Does it expose a native hook, callback, observer, or middleware chain?
  - Does it return its objects through an interface, or a concrete struct?

## 2. Package path, module, and files

### Path

The integration lives at `contrib/<mirror>`, where `<mirror>` mirrors the import path of the package
being instrumented:

- Standard library: use the import path unchanged. `net/http` becomes `contrib/net/http`.
- Hosted on GitHub (`github.com/<owner>/<repo>`): drop the `github.com/` host. `github.com/gorilla/mux`
  becomes `contrib/gorilla/mux`.
- Hosted elsewhere: use the full import path. `google.golang.org/grpc` becomes
  `contrib/google.golang.org/grpc`.

### Version suffix

The suffix depends on how the instrumented library is versioned.

Libraries using Go modules:

- **v0 or v1** (the import path has no `/vN` element): no suffix. `github.com/gorilla/mux` (v1)
  becomes `contrib/gorilla/mux`.
- **v2 or higher** (the import path ends in `/vN`): add a `.vN` suffix on the last element, matching
  the major. `github.com/redis/go-redis/v9` becomes `contrib/redis/go-redis.v9`, and
  `github.com/confluentinc/confluent-kafka-go/v2/kafka` becomes
  `contrib/confluentinc/confluent-kafka-go/kafka.v2`.

Libraries not using Go modules (consumed through a pseudo-version like `v0.0.0-<date>-<hash>`): treat
as v0, no suffix. `github.com/bradfitz/gomemcache` becomes `contrib/bradfitz/gomemcache`.

The Go package name is the same as the original instrumented library, for example `package redis`.

### Module

Each integration is its own Go module. Put `go.mod` and `go.sum` at the root of the integration
directory. The module path is:

```
github.com/DataDog/dd-trace-go/contrib/<mirror>/v2
```

The trailing `/v2` is dd-trace-go's own major version. It is on every contrib module and is separate
from the `.vN` library suffix above. For example, `contrib/gorilla/mux` has module
`github.com/DataDog/dd-trace-go/contrib/gorilla/mux/v2`.

Local dependencies (`dd-trace-go/v2` and other contrib modules) live in this repo and are not fetched
from a released version, so each `go.mod` needs a `replace` directive pointing at the module's
relative path. Do not write these by hand. Run `make fix-modules` and it adds them, tidies every
module, and updates `go.work.sum`. Then register the module in the workspace with
`go work use ./contrib/<mirror>`, which `make fix-modules` does not do for you.

### Files

- Config and functional options go in `option.go`.
- Name the main file after the library package, for example `redis.go`.
- `example_test.go` holds the godoc examples, see [Testing](#10-testing).
- `orchestrion.yml` defines auto-instrumentation, see [section 9](#9-auto-instrumentation).
- Keep the number of files small. Split further only along clear boundaries, for example a client and
  a server file (`net/http`), or producer and consumer files (messaging libraries).
- Put tests next to the code they cover. Tests for `foo.go` go in `foo_test.go`.

## 3. Interception patterns

Prefer the first pattern that fits the library. The deciding factor is the library's API shape.
Patterns higher in this list are easier to maintain and cheaper to auto-instrument.

### 3.1 Native hook or observer (most preferred)

The library calls back into an object you register. The client type is untouched.

```go
func WrapClient(client redis.UniversalClient, opts ...ClientOption) {
    cfg := new(clientConfig)
    defaults(cfg)
    for _, fn := range opts {
        fn.apply(cfg)
    }
    client.AddHook(&datadogHook{...})
}
```

### 3.2 Native middleware or interceptor chain

Register one link in the library's own chain.

```go
router.Use(muxtrace.Middleware())               // gin, chi, echo, fiber
grpc.NewServer(grpc.ChainUnaryInterceptor(...))  // gRPC
```

### 3.3 Constructor replacement (fixed return type)

Use this when the library's constructor returns a type you cannot change, such as `database/sql`
always returning `*sql.DB`. Provide a drop-in function with the same signature as the original
constructor plus the integration's options, returning the same type, and attach tracing underneath.

```go
// Open has the same signature as sql.Open, with added tracing options,
// and returns the same *sql.DB.
func Open(driverName, dataSourceName string, opts ...Option) (*sql.DB, error)
```

The signature and return type are unchanged, so the calls can be replaced one-to-one for
auto-instrumentation.

### 3.4 Interface-returning wrapper

When the object flows through the library's own interface, return that interface, not a concrete type.

```go
func WrapHandler(h http.Handler, service, resource string, opts ...Option) http.Handler
func WrapSyncProducer(cfg *sarama.Config, p sarama.SyncProducer, opts ...Option) sarama.SyncProducer
```

The return type is unchanged, so it is safe for auto-instrumentation.

### 3.5 Concrete-type wrapper (last resort)

Use this only when the other patterns are not possible, either because the library does not expose
them, or because the tracing you need cannot be done through them. A struct embeds the original and
returns a concrete `*tracedX`.

```go
type Router struct {
    *mux.Router
    config *routerConfig
}
func WrapRouter(router *mux.Router, opts ...RouterOption) *Router
```

This pattern is unsafe for auto-instrumentation. The wrapper changes the return type, so
auto-instrumentation cannot swap the constructor call: returning a different type than the original
breaks any code that uses the result. When a library only supports this pattern, its
auto-instrumentation must be done on the library side, skipping the wrapper and injecting the tracing
logic directly into the library's own types.

## 4. Entrypoints and functional options

Entrypoints are the public functions that install the integration (`WrapClient`, `NewClient`,
`Middleware`, `Register`, and so on). They all take variadic functional options.

Standard shape:

```go
type clientConfig struct {
    serviceName   string
    serviceSource string
    // ...
}

// Option describes an option for this integration.
type Option interface {
    apply(*clientConfig)
}

// OptionFn is a functional option.
type OptionFn func(*clientConfig)

func (fn OptionFn) apply(cfg *clientConfig) { fn(cfg) }

// defaults sets defaults; options are applied on top.
func defaults(cfg *clientConfig) { /* ... */ }
```

Most integrations implement these two options. There are exceptions, for example a log-correlation
integration that starts no spans.

- `WithService(name string)`: sets the service name and records its source, see
  [Spans, tags, and naming](#5-spans-tags-and-naming).
- `WithCustomTag(key string, value any)`: attaches a custom tag to spans.

Add other options as the integration needs them. Common examples across existing integrations:

- `WithResourceNamer`: customize how the span resource name is derived, for example per HTTP route.
- `WithSpanOptions`: attach extra `tracer.StartSpanOption`s to every span the integration creates.
- `WithIgnoreRequest` (or a similar predicate): skip tracing for operations that match a condition.
- `WithHeaderTags`: record specific HTTP headers as span tags.

## 5. Spans, tags, and naming

### Required tags

Every span must set:

- `span.kind` (`ext.SpanKind`), unless the value is `internal`. See
  [span_kind.go](../ddtrace/ext/span_kind.go).
- `component` (`ext.Component`): the canonical package path, which is the value of the integration's
  `instrumentation.Package<Name>` constant. Pass that constant directly.

```go
tracer.Tag(ext.SpanKind, ext.SpanKindClient),
tracer.Tag(ext.Component, instrumentation.PackageRedisGoRedisV9),
```

### Service name

New integrations use the global service name (`DD_SERVICE`) and not the older naming-schema machinery.

- Register the integration in `packages.go` with no `naming` map, see
  [Register the integration](#7-register-the-integration).
- Call `instr.ServiceName(instrumentation.ComponentDefault, nil)`. With no `naming` entry, this
  returns `DD_SERVICE`.

```go
func defaults(cfg *clientConfig) {
    cfg.serviceName = instr.ServiceName(instrumentation.ComponentDefault, nil)
    cfg.serviceSource = string(instrumentation.PackageMyLib)
}
```

### Service source

Alongside the service name, the integration records where that name came from. This is stored on the
span as the `_dd.svc_src` meta tag (`ext.KeyServiceSource`), so Datadog can tell whether the service
name is the integration's default, a value set through `WithService`, or another source.
`instrumentation.ServiceNameWithSource(name, source)` returns a `tracer.StartSpanOption` that sets
both the service name and the source tag. Pass it when you create spans in the integration:

```go
span, ctx := tracer.StartSpanFromContext(ctx, "mylib.request",
    instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource),
)
```

The default source is the package name, `string(instrumentation.Package<Name>)`. `WithService`
overrides it to `instrumentation.ServiceSourceWithServiceOption` (the value `"opt.with_service"`), so
a service name set explicitly by the user is distinguishable from the default.

### Operation name

Hardcode the operation name as a string literal when you start the span. For new integrations, do not
call `instr.OperationName` and do not add `buildOpName` builders to the integration's entry in
`instrumentation/packages.go`.

```go
span, ctx := tracer.StartSpanFromContext(ctx, "mylib.request", startOpts...)
```

## 6. Choosing tag names

When you need a tag, choose in this order:

1. An existing `ext.*` constant. See [ddtrace/ext](../ddtrace/ext) and use it.
2. A concept with an OpenTelemetry semantic convention. Use the OTel name, for example `db.system`,
   `messaging.*`, `network.*`, `rpc.*`, `server.address`. See the
   [semantic conventions](https://github.com/open-telemetry/semantic-conventions/tree/main/docs).
3. A niche tag with no `ext` constant. Use a literal namespaced by the library, as existing
   integrations do for library-specific details, for example `vault.namespace` in
   `contrib/hashicorp/vault`.

When the codebase has more than one name for the same concept, prefer the OTel Semantic Conventions
name.

## 7. Register the integration

1. Every integration loads instrumentation telemetry in an `init`:

```go
var instr *instrumentation.Instrumentation

func init() {
    instr = instrumentation.Load(instrumentation.PackageMyLib)
}
```

2. Add the package to [instrumentation/packages.go](../instrumentation/packages.go): a
   `Package<Name>` constant and a `packages` map entry with `TracedPackage` and `EnvVarPrefix`. Do
   not add a `naming` map, see [Service name](#5-spans-tags-and-naming).

3. Add the module import path to `contribIntegrations` in
   [ddtrace/tracer/option.go](../ddtrace/tracer/option.go). This is how the tracer reports the
   integration as imported, for example in startup logs and integration telemetry.

## 8. Context propagation

For operations that cross a process boundary, propagate trace context over the transport. Inject the
active span's context on the sending side and extract it on the receiving side, using a carrier that
adapts the transport's key and value channel to `tracer.TextMapWriter` and `tracer.TextMapReader`.

```go
// sending side
tracer.Inject(span.Context(), carrier)

// receiving side
sctx, err := tracer.Extract(carrier)
```

Carriers for common transports:

- HTTP: `tracer.HTTPHeadersCarrier(req.Header)` over an `http.Header`.
- gRPC: a carrier over `metadata.MD`, see `MDCarrier` in the grpc contrib.
- Message queues (kafka, sarama): a carrier over the message headers, see the kafka and sarama
  contribs.

## 9. Auto-instrumentation

New integrations must support compile-time auto-instrumentation, so users get tracing without editing
their code. Today this is done with Orchestrion. Design your integration with it in mind: favor
patterns 3.1 through 3.4 over 3.5. Writing the `orchestrion.yml` file and the mandatory integration
tests is covered in [AUTO_INSTRUMENTATION.md](./AUTO_INSTRUMENTATION.md).

## 10. Testing

- Aim for about 90% coverage.
- Write table-driven tests: one test function per functionality, with a sub-test (`t.Run`) per case
  or scenario.
- Godoc examples in `example_test.go`, using Go's testable-example naming so they render in godoc:
  - Always provide a package-level example named exactly `Example`, showing typical setup and use.
  - Function and method examples (`ExampleNew`, `ExampleClient_Get`) are optional. Add one only when
    a specific function or method has a detail worth showing. Extra examples for the same target use
    a lowercase suffix, for example `ExampleNew_withService`.
  - `// Output:` only when output is deterministic (omit for examples that print IDs or other
    nondeterministic values).
- For databases and similar systems that need a running dependency, start it with the testcontainers
  helpers in [instrumentation/testutils/containers](../instrumentation/testutils/containers). Use
  them for both the contrib's own tests and the auto-instrumentation integration tests
  (`internal/orchestrion/_integration`). If the service is
  not covered yet, add a helper there, and for the `_integration` tests on CI, add an entry to
  `internal/orchestrion/_integration/ci-services.json`.

## 11. Before you open a PR

Run these in order and commit every change they produce. CI fails on a non-clean `git diff`.

1. `make fix-modules`. Adds replace directives, tidies modules, updates `go.work.sum`. Run it first,
   and remember `go work use ./contrib/<mirror>`.
2. `make generate`. Regenerates the auto-instrumentation artifacts and generated tests. Run it after
   `fix-modules`. If it changes a `go.mod`, run `make fix-modules` again.
3. `make lint` and `make format`.
4. Run your integration's unit tests, for example `go test ./contrib/net/http/...`.
5. Run the auto-instrumentation integration tests for your integration. For example, for net/http:

   ```
   cd internal/orchestrion/_integration
   go run github.com/DataDog/orchestrion go test ./net_http/...
   ```
