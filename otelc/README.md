# OTelc Instrumentation

`otelc` is an project in [OpenTelemetry](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation) that enables compile-time instrumentation. While the implementation for `otelc` lives under OpenTelemetry, dd-trace-go contains support for using the dd-trace-go SDK instead of OTel tracing SDKs. This means using dd-trace-go under the hood while running `otelc` and supporting Datadog features as a result.

dd-trace-go support of `otelc` lives in several directories:

1. `./otelc` -- This directory
2. [../contrib](../contrib/) -- 

## Contributing

To learn about which rules are supported and to contribute to the upstream project, check the [otelc project documentation](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/tree/main/docs).

## Testing