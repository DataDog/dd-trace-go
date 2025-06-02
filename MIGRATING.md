# Migration Guide

This document outlines migrating from an older version of the Datadog tracer (v1.x.x) to v2.

Datadog's v2 version of the Go tracer provides a significant refactor of our API, moving away from interfaces to provide flexibility in future works, isolating our integrations to prevent false-positives from security scanners, and enforcing proper library patterns to prevent misuse. This update is the result of continuous feedback from customers, the community, as well as our extensive internal usage, introducing better maintainability, simplified APIs, and unlocking performance benefits.

As is common and recommended in the Go community, the best way to approach migrating to this new API is by using the [gradual code repair](https://talks.golang.org/2016/refactor.article) method. We have done the same internally and it has worked just great! For this exact reason we have provided a new, [semver](https://semver.org/) friendly import path to help with using both tracers in parallel, without conflict, for the duration of the migration. This new path is `github.com/DataDog/dd-trace-go/v2`.

We have also provided a new migration tool to help with the most essential changes made in v2, which you can read about [here](./tools/v2fix/README.md).

Our [godoc page](https://pkg.go.dev/github.com/DataDog/dd-trace-go/v2/ddtrace) should be helpful during this process. We also have the [official documentation](https://docs.datadoghq.com/tracing/setup/go/), which contains a couple of examples.

This document will further outline some _before_ and _after_ examples.

## Importing

In v2, we have moved away from using gopkg.in in our import URLs in favor of github.com. To import the tracer library, you would have before:

```go
import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
```

Becomes:

```go
import "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
```

It is important to note that when using our contrib libraries, import URLs may be impacted differently. This will be covered in the next section:

### Independent Contrib Packages

This version upgrade comes with a large overhaul of what was previously one single package that held all of our integrations. In v2, we introduce independent packages for each of our contribs, which will prevent false-positives in security scanners that were caused by indirect dependencies. As a result, importing contribs will also change. Before:

```go
import "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
```
Becomes:

```go
import "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
```

If you are unsure of which import URL to use, please refer to our [godoc](https://pkg.go.dev/github.com/DataDog/dd-trace-go/v2/contrib), which will include example code for each contrib.

## Spans

`Span` and `SpanContext` are now represented as a struct rather than an interface, which means that references to these types must use a pointer. They have also been moved to live within the `tracer` package, so they must be accessed using `tracer.Span` rather than `ddtrace.Span`. Before:

```go
var sp ddtrace.Span = tracer.StartSpan("opname")
var ctx ddtrace.SpanContext = sp.Context()
```

Becomes:

```go
var sp *tracer.Span = tracer.StartSpan("opname")
var ctx *tracer.SpanContext = sp.Context()
```

### Deprecated ddtrace interfaces

All the interfaces in `ddtrace` have been removed in favor of struct types, except for `SpanContext`. The new types have moved into `ddtrace/tracer`.

### Deprecated constants and options

The following constants and functions have been removed:

* `ddtrace/ext.AppTypeWeb`
* `ddtrace/ext.CassandraQuery`
* `ddtrace/ext.CassandraBatch`
* `ddtrace/tracer.WithPrioritySampling`; priority sampling is enabled by default.
* `ddtrace/tracer.WithHTTPRoundTripper`; use `WithHTTPClient` instead.

### StartChild

Child spans can be started with StartChild rather than ChildOf. Before:

```go
import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

func main() {
  tracer.Start()
	defer tracer.Stop()

	parent := tracer.StartSpan("op").Context()
	child := tracer.StartSpan("op", tracer.ChildOf(parent))
}
```

Becomes:

```go
import "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

func main() {
  tracer.Start()
	defer tracer.Stop()

	parent := tracer.StartSpan("op")
	child := parent.StartChild("op")
}
```

## Trace IDs

Rather than a `uint64`, trace IDs are now represented as a `string`. This change will allow support for 128-bit trace IDs. Old behavior may still be accessed by using the new `TraceIDLower()` method, though switching to 128-bit IDs is recommended. Before:

```go
sp := tracer.StartSpan("opname")
fmt.Printf("traceID: %d\n", sp.Context().TraceID())
```

Becomes:

```go
sp := tracer.StartSpan("opname")
fmt.Printf("traceID: %s\n", sp.Context().TraceID()) //recommended for using 128-bit IDs
fmt.Printf("traceID: %d\n", sp.Context().TraceIDLower()) // for maintaining old behavior with 64-bit IDs
```

## Span Links API

`Span.AddSpanLink` has been renamed to `Span.AddLink`.

## WithService

The previously deprecated `tracer.WithServiceName()` has been fully removed and replaced with the method `tracer.WithService()`. If you would like to specify a service name upon starting the tracer, you would have before:

```go
tracer.Start(tracer.WithServiceName("service"))
```

After:

```go
tracer.Start(tracer.WithService("service"))
```

## NewStartSpanConfig, WithStartSpanConfig & WithFinishConfig

These functional options for `ddtrace/tracer.Tracer.StartSpan` and `ddtrace/tracer.Span.Finish` reduces the number of calls (in functional option form) in hot loops by giving the freedom to prepare a common span configuration in hot paths.

Before:

```go
var err error
span := tracer.StartSpan(
	"operation",
	ChildOf(parent.Context()),
	Measured(),
	ResourceName("resource"),
	ServiceName(service),
	SpanType(ext.SpanTypeWeb),
	Tag("key", "value"),
)
defer span.Finish(tracer.NoDebugStack())
```

After:

```go
cfg := tracer.NewStartSpanConfig(
	tracer.ChildOf(parent.Context()),
	tracer.Measured(),
	tracer.ResourceName("resource"),
	tracer.ServiceName(service),
	tracer.SpanType(ext.SpanTypeWeb),
	tracer.Tag("key", "value"),
)
finishCfg := &FinishConfig{
	NoDebugStack: true,
}
// [...]
// Reuse the configuration in your hot path:
span := tracer.StartSpan("operation", tracer.WithStartSpanConfig(cfg))
defer span.Finish(tracer.WithFinishConfig(finishCfg))
```

## Sampling API simplified

The following functions have been removed in favour of `SpanSamplingRules` and `TraceSamplingRules`:

* `NameRule`
* `NameServiceRule`
* `RateRule`
* `ServiceRule`
* `SpanNameServiceMPSRule`
* `SpanNameServiceRule`
* `SpanTagsResourceRule`
* `TagsResourceRule`

Also, `ext.SamplingPriority` tag is deprecated. Use `ext.ManualKeep` and `ext.ManualDrop` instead.

## Contrib API

A support package to create contribs without depending on internal packages is available in `instrumentation`. Please refer to [`instrumentation` godoc page](https://pkg.go.dev/github.com/DataDog/dd-trace-go/v2/instrumentation) and existing contribs for more detail.

## Updated User Monitoring SDK for Appsec

`appsec` package offers a new API for user monitoring; essentially deprecating login success & failure event functions, replacing them with versions that accept a `login` field, which is to be used by user monitoring rules (ATO monitoring & protection). Before:

```go
appsec.TrackUserLoginSuccessEvent(...)
appsec.TrackUserLoginFailureEvent(...)
```

Becomes:

```go
appsec.TrackUserLoginSuccess(...)
appsec.TrackUserLoginFailure(...)
```

## API Security sampling

The API Security sampler now takes decisions specific to a given endpoint (method + route + response status code) instead of using a simplistic sampling rate. This allows for improved coverage and accuracy of schema extraction as part of API Security.

## Opentracing deprecation

`opentracer` is in "Maintenance" mode and limited support was offered in `v1`. We recommend to use OpenTelemetry or ddtrace/tracer directly. For additional details, please see our [Support Policy](https://github.com/DataDog/dd-trace-go?tab=readme-ov-file#go-support-policy).

## Further reading 

* package level documentation of the [`tracer` package](https://pkg.go.dev/github.com/DataDog/dd-trace-go/v2/ddtrace/tracer) for a better overview.
* [official documentation](https://docs.datadoghq.com/tracing/setup/go/)
