# v2 Migration Tool

## Features
The migration tool will help developers to upgrade your tracing code from `dd-trace-go` version v1.x to v2.0.0. By running this tool, you will be able to make quick fixes for multiple changes described in [our documentation](../../MIGRATING.md) in a best-effort basis. Some changes cannot be fully automated, please use this for your gradual code repair efforts.

## Running the Tool

Use the migration tool by running:

```
go install github.com/DataDog/dd-trace-go/tools/v2fix@latest
# In your repository's directory
v2fix .
```

In order to apply all suggested fixes, run:

```
v2fix -fix .
```

## Further Reading
For more information about migrating to `v2`, go to:

* [Migration documentation](../../MIGRATING.md)
* [Official documentation](https://docs.datadoghq.com/tracing/trace_collection/custom_instrumentation/go/migration/)
