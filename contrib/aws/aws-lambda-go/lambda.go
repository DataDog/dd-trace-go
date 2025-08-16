// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

// Package lambda provides tracing for AWS Lambda Go runtime.
package lambda

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"

	awslambda "github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

var (
	tracerInitOnce sync.Once
	tracerStarted  bool
)

// Config holds configuration for lambda tracing.
type Config struct {
	serviceName string
}

// Option represents an option that can be passed to Start.
type Option func(*Config)

// WithServiceName sets the service name for lambda traces.
func WithServiceName(serviceName string) Option {
	return func(cfg *Config) {
		cfg.serviceName = serviceName
	}
}

// initTracer initializes the Datadog tracer if not already started
func initTracer() {
	tracerInitOnce.Do(func() {
		// Check if DD_TRACE_ENABLED is set and false
		if os.Getenv("DD_TRACE_ENABLED") == "false" {
			fmt.Println("[DD-LAMBDA] Tracing disabled via DD_TRACE_ENABLED=false")
			return
		}

		fmt.Println("[DD-LAMBDA] Initializing Datadog tracer for Lambda...")

		// Configure tracer for Lambda environment
		tracerOptions := []tracer.StartOption{
			tracer.WithService(getServiceName()),
			tracer.WithAgentAddr(""), // Use default Lambda extension endpoint
		}

		// Add debug output for environment variables
		fmt.Printf("[DD-LAMBDA] Environment variables:\n")
		fmt.Printf("  DD_TRACE_ENABLED: %s\n", os.Getenv("DD_TRACE_ENABLED"))
		fmt.Printf("  DD_SERVICE: %s\n", os.Getenv("DD_SERVICE"))
		fmt.Printf("  DD_API_KEY: %s\n", func() string {
			key := os.Getenv("DD_API_KEY")
			if len(key) > 8 {
				return key[:8] + "..." // Show first 8 chars for verification
			}
			return key
		}())
		fmt.Printf("  DD_SITE: %s\n", os.Getenv("DD_SITE"))
		fmt.Printf("  AWS_LAMBDA_FUNCTION_NAME: %s\n", os.Getenv("AWS_LAMBDA_FUNCTION_NAME"))

		// Start the tracer with options
		tracer.Start(tracerOptions...)
		tracerStarted = true

		fmt.Println("[DD-LAMBDA] Datadog tracer initialized successfully")
	})
}

// Start is a drop-in replacement for lambda.Start that adds tracing.
func Start(handler interface{}, options ...Option) {
	fmt.Println("[DD-LAMBDA] Orchestrion instrumentation: lambda.Start() called")
	// Initialize tracer if not already started
	initTracer()
	StartWithOptions(WrapHandler(handler, options...))
}

// StartWithOptions is a drop-in replacement for lambda.StartWithOptions that adds tracing.
func StartWithOptions(handler interface{}, lambdaOptions ...awslambda.Option) {
	awslambda.StartWithOptions(handler, lambdaOptions...)
}

// StartWithContext is a drop-in replacement for lambda.StartWithContext that adds tracing.
func StartWithContext(ctx context.Context, handler interface{}, options ...Option) {
	// Initialize tracer if not already started
	initTracer()
	awslambda.StartWithContext(ctx, WrapHandler(handler, options...))
}

// StartHandler is a drop-in replacement for lambda.StartHandler that adds tracing.
func StartHandler(handler awslambda.Handler, options ...Option) {
	// Initialize tracer if not already started
	initTracer()
	awslambda.StartHandler(WrapLambdaHandler(handler, options...))
}

// StartHandlerWithContext is a drop-in replacement for lambda.StartHandlerWithContext that adds tracing.
func StartHandlerWithContext(ctx context.Context, handler awslambda.Handler, options ...Option) {
	// Initialize tracer if not already started
	initTracer()
	awslambda.StartHandlerWithContext(ctx, WrapLambdaHandler(handler, options...))
}

// WrapHandler wraps a lambda handler function with tracing.
func WrapHandler(handler interface{}, options ...Option) interface{} {
	cfg := &Config{
		serviceName: getServiceName(),
	}
	for _, opt := range options {
		opt(cfg)
	}

	return wrapHandler(handler, cfg)
}

// WrapLambdaHandler wraps a lambda.Handler interface with tracing.
func WrapLambdaHandler(handler awslambda.Handler, options ...Option) awslambda.Handler {
	cfg := &Config{
		serviceName: getServiceName(),
	}
	for _, opt := range options {
		opt(cfg)
	}

	return &handlerWrapper{
		handler: handler,
		config:  cfg,
	}
}

type handlerWrapper struct {
	handler awslambda.Handler
	config  *Config
}

func (h *handlerWrapper) Invoke(ctx context.Context, payload []byte) ([]byte, error) {
	span, ctx := tracer.StartSpanFromContext(ctx, "aws.lambda.invoke",
		tracer.ServiceName(h.config.serviceName),
		tracer.ResourceName(getLambdaResourceName()),
		tracer.SpanType(ext.SpanTypeServerless),
		tracer.Tag(ext.Component, "aws-lambda-go"),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
		tracer.Tag("aws.lambda.function_name", os.Getenv("AWS_LAMBDA_FUNCTION_NAME")),
		tracer.Tag("aws.lambda.function_version", os.Getenv("AWS_LAMBDA_FUNCTION_VERSION")),
		tracer.Tag("aws.lambda.runtime", os.Getenv("AWS_EXECUTION_ENV")),
	)
	defer span.Finish()

	// Add lambda context information if available
	if lc, ok := lambdacontext.FromContext(ctx); ok {
		span.SetTag("aws.lambda.request_id", lc.AwsRequestID)
		span.SetTag("aws.lambda.invoked_function_arn", lc.InvokedFunctionArn)
		if lc.Identity.CognitoIdentityID != "" {
			span.SetTag("aws.lambda.cognito_identity_id", lc.Identity.CognitoIdentityID)
		}
		if lc.Identity.CognitoIdentityPoolID != "" {
			span.SetTag("aws.lambda.cognito_identity_pool_id", lc.Identity.CognitoIdentityPoolID)
		}
	}

	// Parse and tag the event type if possible
	if eventType := getEventType(payload); eventType != "" {
		span.SetTag("aws.lambda.event_source", eventType)
	}

	result, err := h.handler.Invoke(ctx, payload)
	if err != nil {
		span.SetTag(ext.Error, err)
		span.SetTag(ext.ErrorMsg, err.Error())
	}

	return result, err
}

func wrapHandler(handler interface{}, cfg *Config) interface{} {
	fmt.Printf("[DD-LAMBDA] Wrapping handler with service name: %s\n", cfg.serviceName)

	if handler == nil {
		return handler
	}

	handlerType := reflect.TypeOf(handler)
	if handlerType.Kind() != reflect.Func {
		fmt.Println("[DD-LAMBDA] Handler is not a function, skipping instrumentation")
		return handler
	}

	return reflect.MakeFunc(handlerType, func(args []reflect.Value) []reflect.Value {
		var ctx context.Context
		var payload interface{}

		// Extract context and payload from arguments
		argTypes := make([]reflect.Type, handlerType.NumIn())
		for i := 0; i < handlerType.NumIn(); i++ {
			argTypes[i] = handlerType.In(i)
		}

		// Determine if first argument is context
		hasContext := false
		if len(args) > 0 {
			contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
			if argTypes[0].Implements(contextType) {
				hasContext = true
				ctx = args[0].Interface().(context.Context)
			}
		}

		// Get payload if present
		if hasContext && len(args) > 1 {
			payload = args[1].Interface()
		} else if !hasContext && len(args) > 0 {
			payload = args[0].Interface()
		}

		if ctx == nil {
			ctx = context.Background()
		}

		// Start tracing span
		fmt.Printf("[DD-LAMBDA] Starting span: aws.lambda.invoke for service: %s\n", cfg.serviceName)
		span, ctx := tracer.StartSpanFromContext(ctx, "aws.lambda.invoke",
			tracer.ServiceName(cfg.serviceName),
			tracer.ResourceName(getLambdaResourceName()),
			tracer.SpanType(ext.SpanTypeServerless),
			tracer.Tag(ext.Component, "aws-lambda-go"),
			tracer.Tag(ext.SpanKind, ext.SpanKindServer),
			tracer.Tag("aws.lambda.function_name", os.Getenv("AWS_LAMBDA_FUNCTION_NAME")),
			tracer.Tag("aws.lambda.function_version", os.Getenv("AWS_LAMBDA_FUNCTION_VERSION")),
			tracer.Tag("aws.lambda.runtime", os.Getenv("AWS_EXECUTION_ENV")),
		)
		defer func() {
			fmt.Println("[DD-LAMBDA] Finishing span")
			span.Finish()
		}()

		// Add lambda context information if available
		if lc, ok := lambdacontext.FromContext(ctx); ok {
			span.SetTag("aws.lambda.request_id", lc.AwsRequestID)
			span.SetTag("aws.lambda.invoked_function_arn", lc.InvokedFunctionArn)
			if lc.Identity.CognitoIdentityID != "" {
				span.SetTag("aws.lambda.cognito_identity_id", lc.Identity.CognitoIdentityID)
			}
			if lc.Identity.CognitoIdentityPoolID != "" {
				span.SetTag("aws.lambda.cognito_identity_pool_id", lc.Identity.CognitoIdentityPoolID)
			}
		}

		// Tag event type if available
		if payload != nil {
			if eventType := getEventTypeFromPayload(payload); eventType != "" {
				span.SetTag("aws.lambda.event_source", eventType)
			}
		}

		// Update context in arguments if present
		if hasContext {
			args[0] = reflect.ValueOf(ctx)
		}

		// Call original handler
		handlerValue := reflect.ValueOf(handler)
		results := handlerValue.Call(args)

		// Check for errors in results
		if len(results) > 0 {
			if errVal := results[len(results)-1]; !errVal.IsNil() {
				if err, ok := errVal.Interface().(error); ok {
					span.SetTag(ext.Error, err)
					span.SetTag(ext.ErrorMsg, err.Error())
				}
			}
		}

		return results
	}).Interface()
}

func getServiceName() string {
	if service := os.Getenv("DD_SERVICE"); service != "" {
		return service
	}
	if functionName := os.Getenv("AWS_LAMBDA_FUNCTION_NAME"); functionName != "" {
		return functionName
	}
	return "aws-lambda"
}

func getLambdaResourceName() string {
	if functionName := os.Getenv("AWS_LAMBDA_FUNCTION_NAME"); functionName != "" {
		return functionName
	}
	return "lambda.invoke"
}

func getEventType(payload []byte) string {
	var event map[string]interface{}
	if err := json.Unmarshal(payload, &event); err != nil {
		return ""
	}

	// Try to determine event source from common patterns
	if source, ok := event["source"].(string); ok {
		return source
	}
	if eventSource, ok := event["eventSource"].(string); ok {
		return eventSource
	}
	if records, ok := event["Records"].([]interface{}); ok && len(records) > 0 {
		if record, ok := records[0].(map[string]interface{}); ok {
			if eventSource, ok := record["eventSource"].(string); ok {
				return eventSource
			}
		}
	}

	// Check for API Gateway
	if _, ok := event["requestContext"]; ok {
		if version, ok := event["version"].(string); ok {
			if version == "1.0" {
				return "aws:apigateway"
			} else if version == "2.0" {
				return "aws:apigatewayv2"
			}
		}
		return "aws:apigateway"
	}

	// Check for ALB
	if requestContext, ok := event["requestContext"].(map[string]interface{}); ok {
		if elb, ok := requestContext["elb"]; ok && elb != nil {
			return "aws:alb"
		}
	}

	return ""
}

func getEventTypeFromPayload(payload interface{}) string {
	// Use reflection to extract event source information
	value := reflect.ValueOf(payload)
	if value.Kind() == reflect.Ptr {
		value = value.Elem()
	}

	if value.Kind() != reflect.Struct {
		return ""
	}

	// Look for common event source fields
	valueType := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := valueType.Field(i)
		fieldValue := value.Field(i)

		switch strings.ToLower(field.Name) {
		case "source":
			if fieldValue.Kind() == reflect.String {
				return fieldValue.String()
			}
		case "eventsource":
			if fieldValue.Kind() == reflect.String {
				return fieldValue.String()
			}
		case "records":
			if fieldValue.Kind() == reflect.Slice && fieldValue.Len() > 0 {
				firstRecord := fieldValue.Index(0)
				if firstRecord.Kind() == reflect.Struct {
					recordType := firstRecord.Type()
					for j := 0; j < firstRecord.NumField(); j++ {
						recordField := recordType.Field(j)
						recordFieldValue := firstRecord.Field(j)
						if strings.ToLower(recordField.Name) == "eventsource" && recordFieldValue.Kind() == reflect.String {
							return recordFieldValue.String()
						}
					}
				}
			}
		}
	}

	// Check type name for known event types
	typeName := valueType.Name()
	switch {
	case strings.Contains(typeName, "APIGateway"):
		return "aws:apigateway"
	case strings.Contains(typeName, "ALB"):
		return "aws:alb"
	case strings.Contains(typeName, "S3"):
		return "aws:s3"
	case strings.Contains(typeName, "SNS"):
		return "aws:sns"
	case strings.Contains(typeName, "SQS"):
		return "aws:sqs"
	case strings.Contains(typeName, "DynamoDB"):
		return "aws:dynamodb"
	case strings.Contains(typeName, "Kinesis"):
		return "aws:kinesis"
	}

	return ""
}
