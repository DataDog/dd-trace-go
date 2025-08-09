# AWS Lambda Go Orchestrion Integration

This package provides automatic instrumentation for AWS Lambda Go functions using Datadog's Orchestrion compile-time code generation. With Orchestrion, your existing Lambda functions get tracing capabilities without requiring any code changes.

## How It Works

### Traditional Manual Instrumentation

Previously, you needed to manually wrap your Lambda handlers:

```go
package main

import (
    "context"
    
    "github.com/aws/aws-lambda-go/lambda"
    lambdatrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-lambda-go/v2"
    "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func handler(ctx context.Context, event interface{}) (interface{}, error) {
    // Your handler logic
    return "Hello World!", nil
}

func main() {
    tracer.Start()
    defer tracer.Stop()
    
    // Manual wrapping required
    lambdatrace.Start(handler, lambdatrace.WithServiceName("my-service"))
}
```

### Automatic Orchestrion Instrumentation

With Orchestrion, the same result requires **no code changes**:

```go
package main

import (
    "context"
    
    "github.com/aws/aws-lambda-go/lambda"
)

func handler(ctx context.Context, event interface{}) (interface{}, error) {
    // Your handler logic - no changes needed!
    return "Hello World!", nil
}

func main() {
    // This call is automatically instrumented by orchestrion
    lambda.Start(handler)
}
```

Simply add an `orchestrion.tool.go` file:

```go
//go:build tools

package tools

import (
    _ "github.com/DataDog/orchestrion"
    _ "github.com/DataDog/dd-trace-go/contrib/aws/aws-lambda-go/v2" // integration
)
```

## Architecture

### Orchestrion Configuration

The `orchestrion.yml` file defines how AWS Lambda calls are intercepted:

```yaml
aspects:
  - id: lambda.Start
    join-point:
      function-call: github.com/aws/aws-lambda-go/lambda.Start
    advice:
      - wrap-expression:
          imports:
            lambdatrace: github.com/DataDog/dd-trace-go/contrib/aws/aws-lambda-go/v2
          template: |-
            lambdatrace.Start({{ index .AST.Args 0 }})
```

This configuration tells Orchestrion to:
1. **Join Point**: Intercept calls to `lambda.Start`
2. **Advice**: Replace them with calls to our instrumented `lambdatrace.Start`

### Code Generation Process

When you build with Orchestrion:

1. **AST Analysis**: Orchestrion scans your code for `lambda.Start()` calls
2. **Code Injection**: Replaces calls with our wrapped versions
3. **Import Management**: Automatically adds necessary imports
4. **Compilation**: Builds the instrumented binary

### Join Points Covered

The integration handles all AWS Lambda entry points:

- `lambda.Start(handler)`
- `lambda.StartWithOptions(handler, options...)`
- `lambda.StartWithContext(ctx, handler)` (deprecated)
- `lambda.StartHandler(handler)` (deprecated)
- `lambda.StartHandlerWithContext(ctx, handler)` (deprecated)

## Features

### Automatic Tracing

Every Lambda invocation automatically creates spans with:

- **Operation Name**: `aws.lambda.invoke`
- **Service Name**: Configurable (defaults to function name)
- **Resource Name**: Lambda function name
- **Span Type**: `serverless`
- **Component**: `aws-lambda-go`

### Lambda Context Integration

Automatically extracts and tags Lambda context information:

```go
span.SetTag("aws.lambda.request_id", lc.AwsRequestID)
span.SetTag("aws.lambda.invoked_function_arn", lc.InvokedFunctionArn)
span.SetTag("aws.lambda.function_name", os.Getenv("AWS_LAMBDA_FUNCTION_NAME"))
span.SetTag("aws.lambda.function_version", os.Getenv("AWS_LAMBDA_FUNCTION_VERSION"))
```

### Event Source Detection

Automatically detects and tags the Lambda event source:

- API Gateway: `aws:apigateway`
- Application Load Balancer: `aws:alb`
- S3 Events: `aws:s3`
- SNS Events: `aws:sns`
- SQS Events: `aws:sqs`
- DynamoDB Streams: `aws:dynamodb`

### Error Handling

Automatically captures and tags errors:

```go
if err != nil {
    span.SetTag(ext.Error, err)
    span.SetTag(ext.ErrorMsg, err.Error())
}
```

## Configuration

### Service Name

Service name can be configured via:

1. **Environment Variable**: `DD_SERVICE=my-service`
2. **Function Name**: Uses `AWS_LAMBDA_FUNCTION_NAME` if `DD_SERVICE` not set
3. **Default**: Falls back to `"aws-lambda"`

### Manual Configuration (Optional)

If you need manual control, you can still use the wrapper functions:

```go
import lambdatrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-lambda-go/v2"

func main() {
    lambdatrace.Start(handler, lambdatrace.WithServiceName("custom-service"))
}
```

## Comparison: Before vs After

### Before (Manual Instrumentation)

**Required Code Changes:**
- Import tracing libraries
- Wrap handlers explicitly
- Manage tracer lifecycle
- Handle configuration manually

**Example:**
```go
// Multiple imports required
import (
    "github.com/aws/aws-lambda-go/lambda"
    lambdatrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-lambda-go/v2"
    "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func main() {
    tracer.Start()                                    // Manual setup
    defer tracer.Stop()                               // Manual cleanup
    lambdatrace.Start(handler, options...)            // Manual wrapping
}
```

### After (Orchestrion)

**Required Code Changes:**
- **None!** 

**Setup:**
1. Add `orchestrion.tool.go` file
2. Build with Orchestrion
3. Deploy as normal

**Example:**
```go
// No extra imports
import "github.com/aws/aws-lambda-go/lambda"

func main() {
    lambda.Start(handler)  // Automatically instrumented
}
```

## Migration Guide

### From datadog-lambda-go

If you're currently using `datadog-lambda-go` wrapper pattern:

**Old Code:**
```go
import "github.com/DataDog/datadog-lambda-go/ddlambda"

func main() {
    lambda.Start(ddlambda.WrapFunction(handler, &ddlambda.Config{
        DDTraceEnabled: true,
    }))
}
```

**New Code (Orchestrion):**
```go
import "github.com/aws/aws-lambda-go/lambda"

func main() {
    lambda.Start(handler)  // That's it!
}
```

### From Manual dd-trace-go

**Old Code:**
```go
import lambdatrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-lambda-go/v2"

func main() {
    lambdatrace.Start(handler)
}
```

**New Code (Orchestrion):**
```go
import "github.com/aws/aws-lambda-go/lambda"

func main() {
    lambda.Start(handler)  // Automatically becomes lambdatrace.Start(handler)
}
```

## Building and Deployment

### With Orchestrion

```bash
# Build with orchestrion
orchestrion go build -o bootstrap main.go

# Or use go build after setting up orchestrion as a toolexec
export GOOS=linux GOARCH=amd64
go build -toolexec="orchestrion toolexec" -o bootstrap main.go
```

### Environment Variables

Set these in your Lambda environment:

```bash
DD_TRACE_ENABLED=true
DD_SERVICE=your-service-name
DD_API_KEY=your-datadog-api-key
```

## Examples

See the test directories for complete examples:

- `hello-world-orchestrion/`: Shows automatic instrumentation
- `hello-world-manual/`: Shows manual instrumentation for comparison

## Benefits of Orchestrion Approach

1. **Zero Code Changes**: Existing Lambda functions work without modification
2. **Consistent Instrumentation**: All Lambda functions get the same tracing automatically  
3. **Reduced Maintenance**: No need to remember to wrap handlers
4. **Migration Safety**: Can be adopted gradually without breaking existing code
5. **Performance**: Compile-time instrumentation has zero runtime overhead for setup