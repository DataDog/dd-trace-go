# Frequently Asked Questions (dd-trace-go) do not merge
This document contains answers to questions frequently asked by users. Just because something is listed here doesn't mean it's beyond question, so feel free to open an issue if you want to change or improve any of these things, but this should provide useful information about *how* things are and *why* they are that way.

#### Why does dd-trace-go use go modules in a non-standard way?
This repository currently takes an [idiosyncratic approach](https://github.com/DataDog/dd-trace-go/issues/810) to using Go modules. You may notice that the `go.mod` file does not list all dependencies of the project, but instead just lists the ones that the core logic depends on (`ddtrace/...`, `profiler/...` and a couple other packages).

As a user of the `dd-trace-go` module, this should be transparent and, if anything, make life easier for you. For those wanting to contribute, this can be confusing and annoying.

The primary reason that we do this is for compatibility. `dd-trace-go` contains many contrib packages for many many libraries, all of which are optional features. If we included all of the dd-trace-go dependencies in the `go.mod` file, users' projects would transitively include dependencies for multiple web frameworks, grpc, sql libraries, redis, and lots of other stuff. That's bothersome enough, but worse is that users may end up having versions of libraries forcibly updated, even when they're not using integrations for those libraries. If a user wanted to include `dd-trace-go` to trace their web router but was using an old version of `go-redis` or somesuch, they would be forced to update, even if they don't want to trace `go-redis` and `dd-trace-go` doesn't actually need it. The more integrations we include in `dd-trace-go`, the more likely this situation becomes when we add all dependencies into the `go.mod` file.

Another reason is that we can't. Due to some of the projects we integrate with breaking semantic versioning, it's not possible for us to include all the required dependencies and versions, since we need to depend on multiple, incompatible minor versions of some packages, namely google.golang.org/grpc (See: https://github.com/grpc/grpc-go/issues/3726)

The way forward is through submodules, allowing us to have multiple dependency sets inside `dd-trace-go` and only including what's actually necessary in a user's application. Unfortunately, a prerequisite for this is to get away from `gopkg.in`, which does not support submodules.

Please follow: [848](https://github.com/DataDog/dd-trace-go/issues/848) and [922](https://github.com/DataDog/dd-trace-go/pull/922)

Note also that the [Contribution Guidelines](https://github.com/DataDog/dd-trace-go/blob/v1/CONTRIBUTING.md#go-modules) contain information on working with this setup.


#### Why do client integration spans not use the global service name?
Integrations that are considered *clients* (http clients, grpc clients, sql clients) do **not** use the globally-configured service name. This is by design and is a product-level decision that spans across all the languages' tracers. This is likely to segregate the time spent actually doing the work of the service from the time waiting for another service (i.e. waiting on a web server to return a response).

While there are good arguments to be made that client integrations should take the same service name as everything else in the service, that's not how the library is intended to function today. As a work-around, most integrations have a `WithServiceName` `Option` that will allow you to override the default. If the integration you are using cannot be configured the way you want, please open an issue to discuss adding as option.

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

