[![CircleCI](https://circleci.com/gh/DataDog/dd-trace-go/tree/v1.svg?style=svg)](https://circleci.com/gh/DataDog/dd-trace-go/tree/v1)
[![Godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://godoc.org/gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace)
[![codecov](https://codecov.io/gh/DataDog/dd-trace-go/branch/v1/graph/badge.svg?token=jGG20Xhv8i)](https://codecov.io/gh/DataDog/dd-trace-go)

### Installing

This module contains many packages, but most users should probably install the two packages below:

```bash
go get gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer
go get gopkg.in/CodapeWild/dd-trace-go.v1/profiler
```

Additionally there are many [contrib](./contrib) packages that can be installed as needed like this:

```
go get gopkg.in/CodapeWild/dd-trace-go.v1/contrib/gorilla/mux
```

If you installed more packages than you intended, you can use `go mod tidy` to remove any unused packages.

Requires:

- Go >= 1.12
- Datadog's Trace Agent >= 5.21.1

### Documentation

The API is documented on [godoc](https://godoc.org/gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace) as well as Datadog's official documentation for [Tracing Go Applications](https://docs.datadoghq.com/tracing/setup/go/) and the [Continuous Go Profiler](https://docs.datadoghq.com/tracing/profiler/enabling/go). If you are migrating from an older version of the tracer (e.g. 0.6.x) you may also find the [migration document](https://github.com/DataDog/dd-trace-go/blob/v1/MIGRATING.md) we've put together helpful.

### Contributing

Before considering contributions to the project, please take a moment to read our brief [contribution guidelines](https://github.com/DataDog/dd-trace-go/blob/v1/CONTRIBUTING.md).

### Testing

Tests can be run locally using the Go toolset. The grpc.v12 integration will fail (and this is normal), because it covers for deprecated methods. In the CI environment
we vendor this version of the library inside the integration. Under normal circumstances this is not something that we want to do, because users using this integration
might be running versions different from the vendored one, creating hard to debug conflicts.

To run integration tests locally, you should set the `INTEGRATION` environment variable. The dependencies of the integration tests are best run via Docker. To get an
idea about the versions and the set-up take a look at our [CI config](https://github.com/DataDog/dd-trace-go/blob/v1/.circleci/config.yml).

The best way to run the entire test suite is using the [CircleCI CLI](https://circleci.com/docs/2.0/local-jobs/). Simply run `circleci build`
in the repository root. Note that you might have to increase the resources dedicated to Docker to around 4GB.
