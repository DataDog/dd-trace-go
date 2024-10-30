[![Godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://godoc.org/gopkg.in/DataDog/dd-trace-go.v1/contrib)

The purpose of these packages is to provide tracing on top of commonly used packages from the standard library as well as the 
community in a "plug-and-play" manner. This means that by simply importing the appropriate path, functions are exposed having
 the same signature as the original package. These functions return structures which embed the original return value, allowing 
them to be used as they normally would with tracing activated out of the box.

All of these libraries are supported by our [APM product](https://www.datadoghq.com/apm/).

:warning: These libraries are not built to be used with Opentracing. Opentracing integrations can be found in [their own organisation](https://github.com/opentracing-contrib/).

### Usage

First, find the library which you'd like to integrate with. The naming convention for the integration packages is:

* If the package is from the standard library (eg. `database/sql`), it will be located at the same path.
* If the package is hosted on Github (eg. `github.com/user/repo`) and has version `v2.1.0`, it will be located at the shorthand path `user/repo.v2`.
* If the package is from anywhere else (eg. `google.golang.org/grpc`) and has no stable version, it can be found under the full import path, followed by the version suffix (in this example `.v0`).
* All new integrations should be suffixed with `.vN` where `N` is the major version that is being covered. If the integration covers more than one major version, the minimum version supported should be chosen for the suffix. (ex. If the integration covers versions `2.x.x` - `4.x.x`, the suffix will be `.v2`)
* The package itself should retain its un-versioned name. For example, the integration under `user/repo.v2` stays as `package repo`, and does not become `package repo.v2`

Second, there are a few tags that should be found in all integration spans:
* The `span.kind` tag should be set in root spans with either a `client`, `server`, `producer`, or `consumer` value according to the [definitions](../ddtrace/ext/span_kind.go) found in the repository.
If the value is determined to be `internal`, then omit the tag as that is the assumed default value. Otherwise, explicitly set it with a value from above.
* The `component` tag should be set in all spans with the value equivalent to full naming convention of the integration package explained in the previous step.

Each integration comes with thorough documentation and usage examples. A good overview can be seen on our 
[godoc](https://godoc.org/gopkg.in/DataDog/dd-trace-go.v1/contrib) page.

### Instrumentation telemetry

Every integration is expected to import instrumentation telemetry to gather integration usage (more info [here](https://docs.datadoghq.com/tracing/configure_data_security/#telemetry-collection)). Instrumentation telemetry can be enabled by adding the following `init` function to the new contrib package:
```golang
func init() {
    telemetry.LoadIntegration("package/import/path")
}