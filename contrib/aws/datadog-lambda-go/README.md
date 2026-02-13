# Datadog Lambda Library for Go (dd-trace-go)

![build](https://github.com/DataDog/dd-trace-go/actions/workflows/main-branch-tests.yml/badge.svg)
[![Slack](https://chat.datadoghq.com/badge.svg?bg=632CA6)](https://chat.datadoghq.com/)
[![Go Reference](https://pkg.go.dev/badge/github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2.svg)](https://pkg.go.dev/github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](https://github.com/DataDog/dd-trace-go/blob/main/LICENSE)


> **IMPORTANT NOTICE**: This package replaces the deprecated https://github.com/DataDog/datadog-lambda-go repository.

Datadog Lambda Library for Go enables enhanced Lambda metrics, distributed tracing, and custom metric submission from AWS Lambda functions.

## Migration Guide: Upgrading from datadog-lambda-go v1 to v2

If you are upgrading from the legacy [`github.com/DataDog/datadog-lambda-go`](https://github.com/DataDog/datadog-lambda-go) repository, this guide will walk you through the migration process with code examples.

1. **Update Dependencies.** Update your `go.mod` file by replacing the old package with the new one:

    ```bash
    # Remove the old package
    go get github.com/DataDog/datadog-lambda-go@none

    # Install the new v2 package
    go get github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2
    ```

1. **Update Import Statements.** Update your `datadog-lambda-go` import path:

    **Before (v1):**
    ```go
    import (
        ddlambda "github.com/DataDog/datadog-lambda-go"
    )
    ```

    **After (v2):**
    ```go
    import (
        ddlambda "github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2"
    )
    ```

    The API is compatible between v1 and v2, so your handler code remains unchanged.

1. **(Optional) Update dd-trace-go from v1 to v2.** If you're also using `dd-trace-go` for tracing, you can optionally upgrade from v1 to v2. For details on tracer changes, see the [general dd-trace-go v1 to v2 migration guide](https://docs.datadoghq.com/tracing/trace_collection/custom_instrumentation/go/migration/).

1. **Deploy and Configure.** After updating your code, follow the [Datadog AWS Lambda Instrumentation for Go](https://docs.datadoghq.com/serverless/aws_lambda/instrumentation/go/?tab=datadogui) guide to configure the Datadog Lambda Extension and environment variables for your Lambda function.

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


