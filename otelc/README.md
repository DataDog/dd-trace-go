# OTelc Instrumentation

`otelc` is an project in [OpenTelemetry](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation) that enables compile-time instrumentation. While the implementation for `otelc` lives under OpenTelemetry, dd-trace-go contains support for using the dd-trace-go SDK instead of OTel tracing SDKs. This means using dd-trace-go under the hood while running `otelc` and supporting Datadog features as a result.

dd-trace-go support of `otelc` lives in several directories:

1. `./otelc` -- This directory
2. [../contrib](../contrib/) -- 

## Contributing

To learn about which rules are supported and to contribute to the upstream project, check the [otelc project documentation](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/tree/main/docs).

### Template variables

Rules that use `expand_directive` (e.g. `span_directive` in [../ddtrace/tracer/otelc.yaml](../ddtrace/tracer/otelc.yaml)) render their `template:` string against the matched function using these `text/template` variables:

- `{{.FuncName}}` -- the matched function's name.
- `{{.FuncArgument N}}` -- the identifier of the Nth (0-indexed) parameter, excluding the receiver.
- `{{.FuncArgumentCount}}` -- the number of parameters, excluding the receiver.
- `{{.FuncArgumentOfType "type.Name"}}` -- the identifier of the first parameter matching `type.Name` (e.g. `"context.Context"`, `"*net/http.Request"`), or `""` if none match.
- `{{.FuncReturn N}}` -- the identifier of the Nth (0-indexed) return value.
- `{{.FuncReturnCount}}` -- the number of return values.
- `{{.FuncReturnOfType "type.Name"}}` -- the identifier of the first return value matching `type.Name` (e.g. `"error"`), or `""` if none match.
- `{{.DirectiveArg "key"}}` -- the value of the first `key:value` argument on the matched directive comment, or `""` if absent.
- `{{range .DirectiveArgs}}{{.Key}}={{.Value}}{{end}}` -- iterates all `key:value` arguments on the matched directive comment.

For more information, refer to the [otelc documentation on rules](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/blob/main/docs/rules.md).

## Testing