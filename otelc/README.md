# OTelc Instrumentation

`otelc` is an project in [OpenTelemetry](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation) that enables compile-time instrumentation. While the implementation for `otelc` lives under OpenTelemetry, dd-trace-go contains support for using the dd-trace-go SDK instead of OTel tracing SDKs. This means using dd-trace-go under the hood while running `otelc` and supporting Datadog features as a result.

dd-trace-go support of `otelc` lives in several directories:

1. `./otelc` -- This directory
2. [../contrib](../contrib/) -- Implementations for specific integrations
3. [../internal/otelc](../internal/otelc) -- Internal only implementations required for OTelc instrumentation to work, such as GLS.

## Contributing

To learn about which rules are supported and to contribute to the upstream project, check the [otelc project documentation](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/tree/main/docs).

When instrumenting a new contrib with `otelc`, no OpenTelemetry imports should be introduced to customer code as it would introduce bloat. Only add imports when strictly necessary, and only to `otelc` modules. 

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

Testing `otelc` is supported by the same test cases and harness as done for [Orchestrion](../internal/orchestrion/_integration/). Both `otelc` and `orchestrion` are expected to produce the same spans and traces.

### Running locally

From the root of a checkout of the [otelc project](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation), install the `otelc` binary (`make install`), then from `internal/orchestrion/_integration` in this repo run:

```console
$ otelc -rules="$(pwd)/../../.." go test ./...
```

Run a single package the same way Orchestrion tests are run individually, e.g. `otelc -rules=... go test ./gorilla_mux/...`.