# Frequently Asked Questions (dd-trace-go)
This document contains answers to questions frequently asked by users. Just because something is listed here doesn't mean it's beyond question, so feel free to open an issue if you want to change or improve any of these things, but this should provide useful information about *how* things are and *why* they are that way.

## How can I reduce the size of my instrumented binaries?
You can use the tags below to reduce the size of your binaries (note that some tags disable features):
- `datadog.no_waf`: forces AAP's WAF to be disabled, so Application Security Monitoring features can't be activated if you specify this build tag.
- `grpcnotrace` ([only for gRPC users](https://github.com/grpc/grpc-go/pull/6954)): disables gRPC's built-in `golang.org/x/net/trace` debug tracing endpoints (avoids the `reflect.MethodByName` dependency).
- `nomsgpack` ([only for Gin users](https://github.com/gin-gonic/gin/blob/master/docs/doc.md#build-without-msgpack-rendering-feature)): disables msgpack binding/rendering support in Gin; msgpack-based request/response handling will not be available (dd-trace-go's msgpack usage is unaffected).

## Why do client integration spans not use the global service name?
Integrations that are considered *clients* (http clients, grpc clients, sql clients) do **not** use the globally-configured service name by default. This is by design and is a product-level decision that spans across all the languages' tracers. This is likely to segregate the time spent actually doing the work of the service from the time waiting for another service (i.e. waiting on a web server to return a response). If you want client spans to use the global service name, enable either `DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED=true`, or start the tracer with `tracer.WithGlobalServiceName(true)`.

While there are good arguments to be made that client integrations should take the same service name as everything else in the service, that's not how the library is intended to function today. As a work-around, most integrations have a `WithService` `Option` that will allow you to override the default. If the integration you are using cannot be configured the way you want, please open an issue to discuss adding as option.

See also: https://github.com/DataDog/dd-trace-go/pull/603

## Why are client integration spans not measured?
This is primarily for 2 reasons (most client integrations are not measured by default):
1. Cost - often a traced client will speak to a traced server. If both are measured, there is duplication of measurement here, and duplication of cost for no benefit. By measuring **only** the server, we get analytics without duplication. 
2. Name conflicts - Today, metrics are calculated based on a key of the span's service name and operation name. This can cause clashes when a client and server both use the same operation name.

Some client-like integrations choose to set `tracer.Measured` explicitly (for example, certain messaging consumers), but the default posture is to avoid measuring client calls unless you opt in.

For example, `net/http` [server tracing](https://github.com/DataDog/dd-trace-go/blob/927b5dbf037e267cb3330c93a8d8580c4889bb9c/httptrace/httptrace.go#L136-L143):
```
span, ctx := tracer.StartSpanFromContext(requestContext, instr.OperationName(instrumentation.ComponentServer, nil), nopts...)
```

and `net/http` [client tracing](https://github.com/DataDog/dd-trace-go/blob/927b5dbf037e267cb3330c93a8d8580c4889bb9c/contrib/net/http/internal/wrap/roundtrip.go#L121-L123):
```
span, ctx := tracer.StartSpanFromContext(req.Context(), spanName, opts...)
```
