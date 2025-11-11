[![Godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://pkg.go.dev/github.com/DataDog/dd-trace-go/v2/contrib)

The purpose of these packages is to provide tracing on top of commonly used packages from the standard library as well as the
community in a "plug-and-play" manner. This means that by simply importing the appropriate path, functions are exposed having
 the same signature as the original package. These functions return structures that embed the original return value, allowing
them to be used as they normally would with tracing activated out of the box.

All of these libraries are supported by our [APM product](https://www.datadoghq.com/apm/).

### Usage

First, find the library which you'd like to integrate with. The naming convention for the integration packages has two things to take into consideration:

* Name of the package being instrumented:
  * If the package is from the standard library (eg. `database/sql`), it will be located at the same path.
  * If the package is hosted on Github (eg. `github.com/user/repo`), it will be located at the shorthand path `user/repo`.
  * If the package is from anywhere else (eg. `google.golang.org/grpc`), it can be found under the full import path.
* Version of the package being instrumented:
  * If the package is from the standard library (eg. `database/sql`), it won't have a version suffix.
  * If the package has no major version released, it won't have a version suffix.
  * If the package has a major version released, and:
    * The integration works with all versions (including v0), it won't have a version suffix.
    * The integration works with a specific major version, it will have a version suffix (in this example `.vN`) where N is the major version that is being covered. If the integration covers more than one major version, the minimum version supported should be chosen for the suffix. (ex. If the integration covers versions 2.x.x - 4.x.x, the suffix will be .v2).

Important: the package itself should retain its un-versioned name. For example, the integration under `user/repo.v2` stays as `package repo`, and does not become `package repo.v2`.

All of these packages must be imported using an import URL following the schema `github.com/DataDog/dd-trace-go/contrib/<package path>/v2`.

Second, there are a few tags that should be found in all integration spans:

* The `span.kind` tag should be set in root spans with either a `client`, `server`, `producer`, or `consumer` value according to the [definitions](../ddtrace/ext/span_kind.go) found in the repository.
If the value is determined to be `internal`, then omit the tag as that is the assumed default value. Otherwise, explicitly set it with a value from above.
* The `component` tag should be set in all spans with the value equivalent to full naming convention of the integration package explained in the previous step.

Third, some guidelines to follow on naming functions:

* Use `WithService` instead of `WithServiceName` when setting the service name.

Each integration comes with a thorough documentation and usage examples. A good overview can be seen on our [godoc](https://pkg.go.dev/github.com/DataDog/dd-trace-go/v2/contrib) page.

### Tests

Write tests for your new integration and include them in a file ``<name>_test.go``. This will cause your new tests to be automatically run by the Continuous Integration system on new pull requests.

### Instrumentation telemetry

Every integration is expected to import instrumentation telemetry to gather integration usage (more info [here](https://docs.datadoghq.com/tracing/configure_data_security/#telemetry-collection)). Instrumentation telemetry can be enabled by adding the following `init` function to the new contrib package:

```golang
func init() {
    instrumentation.Load(instrumentation.PkgContribName)
}
```

Then, ensure that:

* [Packages](../instrumentation/packages.go) is updated with the following information:
  * A new constant with a matching package name (eg. `PackageNetHTTP` for `net/http`).
  * Relevant package information in the `packages` map.
* The `go.mod` file in your new submodule is in sync with the rest of the contrib folder.
* `contribIntegrations` in [option.go](../ddtrace/tracer/option.go) contains your new package.
* A corresponding PR is opened in [Datadog/documentation](https://github.com/DataDog/documentation) to update our list of [compatible integrations](https://github.com/DataDog/documentation/blob/master/content/en/tracing/trace_collection/compatibility/go.md).

### Version pinning

We aim to keep all integrated packages to their minimum working version without known vulnerabilities (based on reported CVEs). As integrated packages have different versioning policies regarding breaking changes,
there is no guarantee that previously pinned versions will work with next `dd-trace-go` versions.

### Deprecation

Integrations can be deprecated if all the following conditions are true:

* The integrated package is deprecated or archived (no longer maintained).
* A vulnerability is reported in the latest available version as CVE.
