# Contribs / Integrations

## Read README.md First

**BEFORE making ANY code changes**, you MUST read [README.md](./README.md) for information about:

* Naming conventions
* Testing
* Creating new contribs

## Module Organization

All integrations are housed in this directory as a submodule. Each submodule should include the following information:

1. A main file with the name `<integration_name>.go`
2. A testing file with the name `<integration_name>_test.go`
3. `example_test.go` with an example of how to initialize and use the integration
4. [OPTIONAL, ONLY IF SPECIFICALLY REQUESTED] `orchestrion.yml` file to define auto-instrumentation behavior

Any code that might be shared across multiple contribs lives in [instrumentation](../instrumentation/).