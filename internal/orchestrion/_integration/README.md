## Orchestrion Integration Tests

This package contains integration tests that are executed using [`orchestrion`][1]. They are not
expected to pass unless they are built with [`orchestrion`][1].

The test binaries are built with all compile-time integrations activated (see
[`orchestrion.tool.go`][2]).

This test suite is run in CI as part of the [orchestrion.yml][6] workflow.

### Prerequisites

#### Docker

This test suite uses [`testcontainers`][3] to provide endpoints for certain tests (Redis, Cassandra,
etc...). These are currently un-supported by Windows and macOS runners on GitHub Actions, and are
omitted when the `githubci` build tag is present.

<details>
<summary>
ℹ️ Running on macOS with <tt>colima</tt>
</summary>

Running the test suite locally on a macOS host that uses [`colima`][4] as a container engine may
require executing the following commands so that [`testcontainers`][3] correctly leverages it:

```console
$ export DOCKER_HOST=$(docker context inspect "$(docker context show)" -f "{{ .Endpoints.docker.Host }}")
$ export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE='/var/run/docker.sock'
```

</details>

### Running the test suite

First, `cd` into the _integration directory: `cd internal/orchestrion/_integration`. From there, you can locally run this test suite using the following commands:
```console
$ go run github.com/DataDog/orchestrion go test ./...
```
Run a specific integration test (for example, gorilla_mux):
```console
$ go run github.com/DataDog/orchestrion go test ./gorilla_mux/...
```

[1]: https://github.com/DataDog/orchestrion
[2]: ./orchestrion.tool.go
[3]: https://golang.testcontainers.org/
[4]: https://github.com/abiosoft/colima
[5]: https://pypi.org/project/ddapm-test-agent/
[6]: ../../../.github/workflows/orchestrion.yml

### Adding new tests

To add a new integration test, follow these steps:

1. **Create a test case structure**: Implement a new struct that satisfies the [`harness.TestCase`](./internal/harness/harness.go) interface. If adding to an existing package that already has a `TestCase`, use a descriptive name like `TestCaseSomething` to avoid naming conflicts.

   Write one file per distinct calling convention the library supports (function literal vs
   interface, closure, global convenience function vs explicit construction, value vs pointer
   config, and so on), each with its own `TestCase`. Auto-instrumentation matches on how code is
   written, not just which library is called, so a pattern with no dedicated test case can build
   fine in one form and silently fail to weave, or outright fail to compile, in another. One file
   per calling convention exercises every join-point matcher and catches build breaks at compile
   time. See [`contrib/ORCHESTRION.md`](../../../contrib/ORCHESTRION.md#integration-tests) and, for
   example, `net_http`'s `issue_400.go` and `global_functions.go` in this package.

2. **Implement the required methods**: Ensure your test case implements all three methods defined by the `harness.TestCase` interface:
   - **`Setup`**: Prepare everything needed for the test, such as starting services (e.g., database servers) or setting up test data. The tracer is not yet started during setup.
   - **`Run`**: Perform the actions that should generate trace data from the instrumented code. This executes after the tracer is started and should assert on expected post-conditions.
   - **`ExpectedTraces`**: Return the set of traces that the test expects to be produced. Each trace returned will be matched against the actual traces received by the mock agent.

3. **Generate test files**: After creating your test case, regenerate the `generated_test.go` files to include your new test in the suite:

```console
$ go generate ./...
```

This command will automatically discover and register your new test case with the integration test suite.
