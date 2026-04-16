# Contribs / Integrations

## Read README.md First

**BEFORE making ANY code changes**, you MUST read [README.md](./README.md) for information about:

* Naming conventions
* Testing
* Steps for creating new contribs

### Key Take-aways

1. All integration spans should include the `span.kind` (unless it is internal, in which case it can be removed) and `component` tags. 
2. Always create an `init` function to call `instrumentation.Load(instrumentation.PkgContribName)` once per package.
3. All integrations should be included in [packages](../instrumentation/packages.go).
4. Remind the user to update [public documentation](https://github.com/DataDog/documentation/blob/master/content/en/tracing/trace_collection/compatibility/go.md) when creating new integrations.

## Module Organization

All integrations are housed in this directory as a submodule. Each submodule should include the following information:

1. A main file with the name `<integration_name>.go`
2. A testing file with the name `<integration_name>_test.go`
3. `example_test.go` with an example of how to initialize and use the integration for godocs
4. [OPTIONAL, ONLY IF SPECIFICALLY REQUESTED] `orchestrion.yml` file to define auto-instrumentation behavior

Any code that might be shared across multiple contribs lives in [instrumentation](../instrumentation/).