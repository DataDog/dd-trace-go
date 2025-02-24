# v2 Migration Tool

## Features
The migration tool will allow a simplified process to upgrade your tracing code from `dd-trace-go` version 1.6x to v2.0. By running this tool, you will be able to make quick fixes for:

* Changing import URLs from `dd-trace-go.v1` to `dd-trace-go/v2`.
* Importing and using the certain types from `ddtrace/tracer` rather than from `ddtrace`.
* Calling `Span` and `SpanContext` using pointers.
* Replacing `WithServiceName()`, which is no longer supported, with `WithService()` calls.
* Using `TraceIDLower()` to get an `uint64` Trace ID instead of `TraceID()`.

For other necessary fixes, refer to [our documentation](../../MIGRATING.md) for more information.

## Running the Tool

Use the migration tool by running:

```
go install github.com/DataDog/dd-trace-go/tools/v2check
# In your repository's directory
v2check .
```

## Further Reading
For more information about migrating to `v2`, go to:

* [Migration documentation](../../MIGRATING.md)
* [Official documentation](https://docs.datadoghq.com/tracing/setup/go/)