[![Godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://pkg.go.dev/github.com/DataDog/dd-trace-go/v2/contrib)

These packages are Datadog's integrations for commonly used standard-library and community packages.
Each one instruments a package in a "plug-and-play" manner: import the matching path and use the
exposed functions, which mirror the original package's API and add tracing out of the box.

These integrations are supported by our [APM product](https://www.datadoghq.com/apm/).

### Using an integration

Each integration is a nested Go module, imported with the schema
`github.com/DataDog/dd-trace-go/contrib/<package path>/v2`, where `<package path>` mirrors the
instrumented package (for example `net/http`, `gorilla/mux`, `google.golang.org/grpc`).

For the list of supported integrations and their versions, see the
[Go compatibility docs](https://docs.datadoghq.com/tracing/trace_collection/compatibility/go/?tab=v2#integrations)
and the [godoc reference](https://pkg.go.dev/github.com/DataDog/dd-trace-go/v2/contrib).

### Contributing a new integration

See [INTEGRATIONS.md](./INTEGRATIONS.md) for the full authoring guide, and
[ORCHESTRION.md](./ORCHESTRION.md) for auto-instrumentation.

### Version pinning

We aim to keep all integrated packages to their minimum working version without known vulnerabilities
(based on reported CVEs). As integrated packages have different versioning policies regarding breaking
changes, there is no guarantee that previously pinned versions will work with next `dd-trace-go`
versions.

### Deprecation

Integrations can be deprecated if all the following conditions are true:

* The integrated package is deprecated or archived (no longer maintained).
* A vulnerability is reported in the latest available version as CVE.
