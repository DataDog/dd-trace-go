[![Main Branch and Release Tests](https://github.com/DataDog/dd-trace-go/actions/workflows/main-branch-tests.yml/badge.svg)](https://github.com/DataDog/dd-trace-go/actions/workflows/main-branch-tests.yml)
[![System Tests](https://github.com/DataDog/dd-trace-go/actions/workflows/system-tests.yml/badge.svg)](https://github.com/DataDog/dd-trace-go/actions/workflows/system-tests.yml)
[![CodeQL](https://github.com/DataDog/dd-trace-go/actions/workflows/codeql-analysis.yml/badge.svg)](https://github.com/DataDog/dd-trace-go/actions/workflows/codeql-analysis.yml)
[![APM Parametric Tests](https://github.com/DataDog/dd-trace-go/actions/workflows/parametric-tests.yml/badge.svg)](https://github.com/DataDog/dd-trace-go/actions/workflows/parametric-tests.yml)
[![codecov](https://codecov.io/gh/DataDog/dd-trace-go/branch/v1/graph/badge.svg?token=jGG20Xhv8i)](https://codecov.io/gh/DataDog/dd-trace-go)

[![Godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://pkg.go.dev/gopkg.in/DataDog/dd-trace-go.v1)

### Datadog Client Libraries for Go

This repository contains Go packages for the client-side components of the Datadog product suite for Application Performance Monitoring, Continuous Profiling and Application Security Monitoring of Go applications.

- [Datadog Application Performance Monitoring (APM)](https://docs.datadoghq.com/tracing/): Trace requests as they flow across web servers, databases and microservices so that developers have great visiblity into bottlenecks and troublesome requests.
The package [`gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer`](https://pkg.go.dev/gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer) allows you to trace any piece of your Go code, and commonly used Go libraries can be automatically traced thanks to our out-of-the-box integrations which can be found in the package [`gopkg.in/DataDog/dd-trace-go.v1/ddtrace/contrib`](https://pkg.go.dev/gopkg.in/DataDog/dd-trace-go.v1/contrib).

- [Datadog Go Continuous Profiler](https://docs.datadoghq.com/profiler/): Continuously profile your Go apps to find CPU, memory, and synchronization bottlenecks, broken down by function name, and line number, to significantly reduce end-user latency and infrastructure costs.
The package [`gopkg.in/DataDog/dd-trace-go.v1/profiler`](https://pkg.go.dev/gopkg.in/DataDog/dd-trace-go.v1/profiler) allows you to periodically collect and send Go profiles to the Datadog API.

- [Datadog Application Security Management (ASM)](https://docs.datadoghq.com/security_platform/application_security/) provides in-app monitoring and protection against application-level attacks that aim to exploit code-level vulnerabilities, such as a Server-Side-Request-Forgery (SSRF), a SQL injection (SQLi), or Reflected Cross-Site-Scripting (XSS). ASM identifies services exposed to application attacks and leverages in-app security rules to detect and protect against threats in your application environment. ASM is not a standalone Go package and is transparently integrated into the APM tracer. You can simply enable it with [`DD_APPSEC_ENABLED=true`](https://docs.datadoghq.com/security/application_security/enabling/go).

### Installing

This module contains many packages, but most users should probably install the two packages below:

```bash
go get gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer
go get gopkg.in/DataDog/dd-trace-go.v1/profiler
```

Additionally there are many [contrib](./contrib) packages that can be installed to automatically instrument and trace commonly used Go libraries such as [net/http](https://pkg.go.dev/gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http), [gorilla/mux](https://pkg.go.dev/gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/mux) or [database/sql](https://pkg.go.dev/gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql):

```
go get gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/mux
```

If you installed more packages than you intended, you can use `go mod tidy` to remove any unused packages.

### Documentation

 - [APM Tracing API](https://pkg.go.dev/gopkg.in/DataDog/dd-trace-go.v1/ddtrace)
 - [APM Tracing Go Applications](https://docs.datadoghq.com/tracing/setup/go/)
 - [Continuous Go Profiler](https://docs.datadoghq.com/tracing/profiler/enabling/go)
 - [Application Security Monitoring](https://docs.datadoghq.com/security_platform/application_security/setup_and_configure/?code-lang=go)
 - If you are migrating from an older version of the tracer (e.g. 0.6.x) you may also find the [migration document](MIGRATING.md) we've put together helpful.

### Go Support Policy

Datadog APM for Go is built upon dependencies defined in specific versions of the host operating system, Go releases, and the Datadog Agent/API. dd-trace-go supports the two latest releases of Go, matching the [official Go policy](https://go.dev/doc/devel/release#policy). This library only officially supports [first class ports](https://go.dev/wiki/PortingPolicy) of Go.

### Contributing

Before considering contributions to the project, please take a moment to read our brief [contribution guidelines](CONTRIBUTING.md).

### Testing

Tests can be run locally using the Go toolset.

To run integration tests locally, you should set the `INTEGRATION` environment variable. The dependencies of the integration tests are best run via Docker. To get an
idea about the versions and the set-up take a look at our [docker-compose config](./docker-compose.yaml).

The best way to run the entire test suite is using the [test.sh](./test.sh) script. You'll need Docker and docker-compose installed. If this is your first time running the tests, you should run `./test.sh -t` to install any missing test tools/dependencies. Run `./test.sh --all` to run all of the integration tests through the docker-compose environment. Run `./test.sh --help` for more options.

If you're only interested in the tests for a specific integration it can be useful to spin up just the required containers via docker-compose.
For example if you're running tests that need the `mysql` database container to be up:
```shell
docker compose -f docker-compose.yaml -p dd-trace-go up -d mysql
```
