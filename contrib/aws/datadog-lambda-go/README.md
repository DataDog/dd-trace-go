# Datadog Lambda Library for Go (dd-trace-go)

![build](https://github.com/DataDog/dd-trace-go/actions/workflows/main-branch-tests.yml/badge.svg)
[![Slack](https://chat.datadoghq.com/badge.svg?bg=632CA6)](https://chat.datadoghq.com/)
[![Go Reference](https://pkg.go.dev/badge/github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2.svg)](https://pkg.go.dev/github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](https://github.com/DataDog/dd-trace-go/blob/main/LICENSE)


> **IMPORTANT NOTICE**: This package replaces the deprecated https://github.com/DataDog/datadog-lambda-go repository.

Datadog Lambda Library for Go enables enhanced Lambda metrics, distributed tracing, and custom metric submission from AWS Lambda functions.

## Migration Guide: Upgrading from datadog-lambda-go v1 to v2

If you are upgrading from the legacy [`github.com/DataDog/datadog-lambda-go`](https://github.com/DataDog/datadog-lambda-go) repository, this guide will walk you through the migration process with code examples.

### Step 1: Update Dependencies

Update your `go.mod` file by replacing the old package with the new one:

```bash
# Remove the old package
go get github.com/DataDog/datadog-lambda-go@none

# Install the new v2 package
go get github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2
```

### Step 2: Update Import Statements

The import paths have changed. Update your imports as follows:

**Before (v1):**
```go
import (
    ddlambda "github.com/DataDog/datadog-lambda-go"
    "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
    httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
)
```

**After (v2):**
```bash
import (
    ddlambda "github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2"
    "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
    httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
)
```

### Step 3: Update Handler Code (API remains the same)

The API is compatible, so your handler code remains largely unchanged. Here's the actual integration test code from both repositories showing the migration:

**Before (v1):**
```go
// Source: datadog-lambda-go/tests/integration_tests/hello/main.go
package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/lambda"

	ddlambda "github.com/DataDog/datadog-lambda-go"
	"github.com/aws/aws-lambda-go/events"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func handleRequest(ctx context.Context, ev events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	currentSpan, _ := tracer.SpanFromContext(ctx)
	currentSpanContext := currentSpan.Context()
	fmt.Println("Current span ID: " + strconv.FormatUint(currentSpanContext.SpanID(), 10))
	fmt.Println("Current trace ID: " + strconv.FormatUint(currentSpanContext.TraceID(), 10))

	// HTTP request
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://www.datadoghq.com", nil)
	client := http.Client{}
	client = *httptrace.WrapClient(&client)
	client.Do(req)

	// Metric
	ddlambda.Distribution("hello-go.dog", 1)

	// User-defined span
	for i := 0; i < 10; i++ {
		s, _ := tracer.StartSpanFromContext(ctx, "child.span")
		time.Sleep(100 * time.Millisecond)
		s.Finish()
	}

	return events.APIGatewayProxyResponse{
		StatusCode: 200,
		Body:       "hello, dog!",
	}, nil
}

func main() {
	lambda.Start(ddlambda.WrapHandler(handleRequest, nil))
}
```

**After (v2):**
```go
// Source: dd-trace-go/contrib/aws/datadog-lambda-go/test/integration_tests/hello/main.go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/aws/aws-lambda-go/lambda"

	ddlambda "github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2"
	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/aws/aws-lambda-go/events"
)

func handleRequest(ctx context.Context, ev events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	currentSpan, _ := tracer.SpanFromContext(ctx)
	currentSpanContext := currentSpan.Context()
	slog.Info("Current span", "span_id", currentSpanContext.SpanID())
	slog.Info("Current trace", "trace_id", currentSpanContext.TraceID())

	// HTTP request
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://www.datadoghq.com", nil)
	client := http.Client{}
	client = *httptrace.WrapClient(&client)
	client.Do(req)

	// Metric
	ddlambda.Distribution("hello-go.dog", 1)

	// User-defined span
	for i := 0; i < 10; i++ {
		s, _ := tracer.StartSpanFromContext(ctx, "child.span")
		time.Sleep(100 * time.Millisecond)
		s.Finish()
	}

	return events.APIGatewayProxyResponse{
		StatusCode: 200,
		Body:       "hello, dog!",
	}, nil
}

func main() {
	lambda.Start(ddlambda.WrapHandler(handleRequest, nil))
}
```

### What Changed?

- **Import paths**: All imports now use the new module paths
- **API compatibility**: The `ddlambda` API remains the same (`WrapHandler`, `Metric`, `Distribution`, etc.)
- **Tracer API**: The tracer API is compatible between v1 and v2

### Step 4: Deploy and Configure

After updating your code, follow the [Datadog AWS Lambda Instrumentation for Go](https://docs.datadoghq.com/serverless/aws_lambda/instrumentation/go/?tab=datadogui) guide to configure the Datadog Lambda Extension and environment variables for your Lambda function.

### Additional Resources

- [General dd-trace-go v1 to v2 Migration Guide](https://docs.datadoghq.com/tracing/trace_collection/custom_instrumentation/go/migration/)

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


