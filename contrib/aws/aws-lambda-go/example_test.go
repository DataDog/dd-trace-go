// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package lambda_test

import (
	"context"
	"fmt"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	lambdatrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-lambda-go/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func ExampleStart() {
	// Define your lambda handler
	handler := func(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		// Your handler logic here
		return events.APIGatewayProxyResponse{
			StatusCode: 200,
			Body:       "Hello World!",
		}, nil
	}

	// Start the lambda with automatic tracing
	// When using orchestrion, this will be automatically wrapped
	lambda.Start(handler)
}

func ExampleStart_withOptions() {
	// Start tracer (optional - orchestrion can handle this automatically)
	tracer.Start()
	defer tracer.Stop()

	// Define your lambda handler
	handler := func(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		// Your handler logic here
		return events.APIGatewayProxyResponse{
			StatusCode: 200,
			Body:       "Hello World!",
		}, nil
	}

	// Start the lambda with custom service name
	lambdatrace.Start(handler, lambdatrace.WithServiceName("my-service"))
}

func ExampleWrapHandler() {
	// You can also manually wrap handlers if needed
	handler := func(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		return events.APIGatewayProxyResponse{
			StatusCode: 200,
			Body:       "Hello World!",
		}, nil
	}

	// Wrap the handler with tracing
	wrappedHandler := lambdatrace.WrapHandler(handler, lambdatrace.WithServiceName("my-service"))

	// Start the lambda
	lambda.Start(wrappedHandler)
}

//func ExampleWrapLambdaHandler() {
//	// For lambda.Handler interface implementations
//	type customHandler struct{}
//
//	func (h *customHandler) Invoke(ctx context.Context, payload []byte) ([]byte, error) {
//		return []byte(`{"statusCode": 200, "body": "Hello World!"}`), nil
//	}
//
//	handler := &customHandler{}
//
//	// Wrap the handler interface
//	wrappedHandler := lambdatrace.WrapLambdaHandler(handler)
//
//	// Start the lambda
//	lambda.StartHandler(wrappedHandler)
//}

// Example showing that with orchestrion, existing code requires no changes
func Example_automatic() {
	handler := func(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		fmt.Printf("Processing request: %s\n", request.RequestContext.RequestID)

		return events.APIGatewayProxyResponse{
			StatusCode: 200,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: `{"message": "Hello from Lambda!"}`,
		}, nil
	}

	// With orchestrion, this call is automatically instrumented
	// No code changes needed!
	lambda.Start(handler)
}
