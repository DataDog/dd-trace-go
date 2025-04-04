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

You can locally run this test suite using the following commands:
```console
$ go run github.com/DataDog/orchestrion go test ./...
```

[1]: https://github.com/DataDog/orchestrion
[2]: ./orchestrion.tool.go
[3]: https://golang.testcontainers.org/
[4]: https://github.com/abiosoft/colima
[5]: https://pypi.org/project/ddapm-test-agent/
[6]: ../../../.github/workflows/orchestrion.yml
