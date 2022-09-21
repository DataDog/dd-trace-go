# Frequently Asked Questions (dd-trace-go)
This document contains answers to questions frequently asked by users. Just because something is listed here doesn't mean it's beyond question, so feel free to open an issue if you want to change or improve any of these things, but this should provide useful information about *how* things are and *why* they are that way.

#### Why do client integration spans not use the global service name?
Integrations that are considered *clients* (http clients, grpc clients, sql clients) do **not** use the globally-configured service name. This is by design and is a product-level decision that spans across all the languages' tracers. This is likely to segregate the time spent actually doing the work of the service from the time waiting for another service (i.e. waiting on a web server to return a response).

While there are good arguments to be made that client integrations should take the same service name as everything else in the service, that's not how the library is intended to function today. As a work-around, most integrations have a `WithServiceName` `Option` that will allow you to override the default. If the integration you are using cannot be configured the way you want, please open an issue to discuss adding as option.

See also: https://github.com/DataDog/dd-trace-go/pull/603

#### Why are client integration spans not measured?
This is primarily for 2 reasons:
1. Cost - often a traced client will speak to a traced server. If both are measured, there is duplication of measurement here, and duplication of cost for no benefit. By measuring **only** the server, we get analytics without duplication. 
2. Name conflicts - Today, metrics are calculated based on a key of the span's service name and operation name. This can cause clashes when a client and server both use the same operation name.

For example, `net/http` [server tracing](https://github.com/DataDog/dd-trace-go/blob/f86a82b0ae679be3bbd2fe3652ae17f06aabd960/contrib/internal/httputil/trace.go#L52):
```
span, ctx := tracer.StartSpanFromContext(cfg.Request.Context(), "http.request", opts...)
```

and `net/http` [client tracing](https://github.com/DataDog/dd-trace-go/blob/f86a82b0ae679be3bbd2fe3652ae17f06aabd960/contrib/net/http/roundtripper.go#L39):
```
span, ctx := tracer.StartSpanFromContext(req.Context(), "http.request", opts...)
```

This is something that users ask for from time to time, and there is work internally to resolve this. Please follow [#1006](https://github.com/DataDog/dd-trace-go/issues/1006) to track the progress.

