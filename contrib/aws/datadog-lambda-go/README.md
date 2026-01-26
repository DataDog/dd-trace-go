# Datadog Lambda Library for Go (dd-trace-go)

![build](https://github.com/DataDog/dd-trace-go/actions/workflows/main-branch-tests.yml/badge.svg)
[![Slack](https://chat.datadoghq.com/badge.svg?bg=632CA6)](https://chat.datadoghq.com/)
[![Go Reference](https://pkg.go.dev/badge/github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2.svg)](https://pkg.go.dev/github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](https://github.com/DataDog/dd-trace-go/blob/main/LICENSE)


> **IMPORTANT NOTICE**: This package replaces the deprecated https://github.com/DataDog/datadog-lambda-go repository.

Datadog Lambda Library for Go enables enhanced Lambda metrics, distributed tracing, and custom metric submission from AWS Lambda functions.

## Upgrading from Go tracer v1 to Go tracer v2
Although Go tracer v1 remains available, Datadog recommends using v2, which is the
primary supported version. See the
[migration instructions](https://docs.datadoghq.com/tracing/trace_collection/custom_instrumentation/go/migration/#migration-instructions)
for guidance on upgrading from v1 to v2.

If you are upgrading a Go AWS Lambda function that previously used the legacy
`datadog-lambda-go` repository, note that the Lambda wrapper has been migrated into
the Go tracer under `dd-trace-go`.

When using Go tracer v2, you must import the Lambda wrapper using the `/v2`
module path:

```go
import "github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2"
```

## Installation

Follow the [installation instructions](https://docs.datadoghq.com/serverless/installation/go/), and view your function's enhanced metrics, traces and logs in Datadog.

## Configurations

See the [advanced configuration options](https://docs.datadoghq.com/serverless/configuration) to tag your telemetry, capture request/response payloads, filter or scrub sensitive information from logs or traces, and more.

## Opening Issues

If you encounter a bug with this package, we want to hear about it. Before opening a new issue, search the existing issues to avoid duplicates.

When opening an issue, include the datadog-lambda-go version, `go version`, and stack trace if available. In addition, include the steps to reproduce when appropriate.

You can also open an issue for a feature request.

## Contributing

If you find an issue with this package and have a fix, please feel free to open a pull request following the [procedures](https://github.com/DataDog/dd-trace-go/blob/main/CONTRIBUTING.md).

## Community

For product feedback and questions, join the `#serverless` channel in the [Datadog community on Slack](https://chat.datadoghq.com/).

## License

Unless explicitly stated otherwise all files in this package are licensed under the Apache License Version 2.0.

This product includes software developed at Datadog (https://www.datadoghq.com/). Copyright 2021 Datadog, Inc.
