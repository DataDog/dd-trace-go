// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package lambda

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

func TestWrapHandler(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	handler := func(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		return events.APIGatewayProxyResponse{
			StatusCode: 200,
			Body:       "Hello World!",
		}, nil
	}

	wrappedHandler := WrapHandler(handler)
	require.NotNil(t, wrappedHandler)

	// Test that the wrapped handler is callable
	ctx := context.Background()
	request := events.APIGatewayProxyRequest{
		HTTPMethod: "GET",
		Path:       "/test",
	}

	response, err := wrappedHandler.(func(context.Context, events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error))(ctx, request)
	require.NoError(t, err)
	assert.Equal(t, 200, response.StatusCode)
	assert.Equal(t, "Hello World!", response.Body)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	span := spans[0]
	assert.Equal(t, "aws.lambda.invoke", span.OperationName())
	assert.Equal(t, "aws-lambda-go", span.Tag("component"))
	assert.Equal(t, "server", span.Tag("span.kind"))
}

//func TestWrapLambdaHandler(t *testing.T) {
//	mt := mocktracer.Start()
//	defer mt.Stop()
//
//	type customHandler struct{}
//
//	func (h *customHandler) Invoke(ctx context.Context, payload []byte) ([]byte, error) {
//		return []byte(`{"statusCode": 200, "body": "Hello World!"}`), nil
//	}
//
//	handler := &customHandler{}
//	wrappedHandler := WrapLambdaHandler(handler)
//	require.NotNil(t, wrappedHandler)
//
//	ctx := context.Background()
//	payload := []byte(`{"httpMethod": "GET", "path": "/test"}`)
//
//	result, err := wrappedHandler.Invoke(ctx, payload)
//	require.NoError(t, err)
//	assert.Contains(t, string(result), "Hello World!")
//
//	spans := mt.FinishedSpans()
//	require.Len(t, spans, 1)
//
//	span := spans[0]
//	assert.Equal(t, "aws.lambda.invoke", span.OperationName())
//	assert.Equal(t, "aws-lambda-go", span.Tag("component"))
//	assert.Equal(t, "server", span.Tag("span.kind"))
//}

func TestWrapHandlerWithOptions(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	handler := func(ctx context.Context) (string, error) {
		return "Hello World!", nil
	}

	wrappedHandler := WrapHandler(handler, WithServiceName("test-service"))
	require.NotNil(t, wrappedHandler)

	ctx := context.Background()
	result, err := wrappedHandler.(func(context.Context) (string, error))(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Hello World!", result)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	span := spans[0]
	assert.Equal(t, "aws.lambda.invoke", span.OperationName())
	assert.Equal(t, "test-service", span.Tag("service.name"))
}

func TestGetEventType(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		expected string
	}{
		{
			name:     "API Gateway",
			payload:  `{"requestContext": {"elb": null}, "version": "1.0"}`,
			expected: "aws:apigateway",
		},
		{
			name:     "ALB",
			payload:  `{"requestContext": {"elb": {"targetGroupArn": "arn:aws:elasticloadbalancing:..."}}}`,
			expected: "aws:alb",
		},
		{
			name:     "S3 Event",
			payload:  `{"Records": [{"eventSource": "aws:s3"}]}`,
			expected: "aws:s3",
		},
		{
			name:     "Direct Source",
			payload:  `{"source": "aws:sns"}`,
			expected: "aws:sns",
		},
		{
			name:     "Unknown",
			payload:  `{"someField": "someValue"}`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getEventType([]byte(tt.payload))
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetEventTypeFromPayload(t *testing.T) {
	// Test with API Gateway event
	apiGwEvent := events.APIGatewayProxyRequest{
		HTTPMethod: "GET",
		Path:       "/test",
	}

	eventType := getEventTypeFromPayload(apiGwEvent)
	assert.Equal(t, "aws:apigateway", eventType)

	// Test with S3 event
	s3Event := events.S3Event{
		Records: []events.S3EventRecord{
			{
				EventSource: "aws:s3",
			},
		},
	}

	eventType = getEventTypeFromPayload(s3Event)
	assert.Equal(t, "aws:s3", eventType)
}

func TestServiceNameConfiguration(t *testing.T) {
	// Test default service name
	assert.Equal(t, "aws-lambda", getServiceName())

	// Test with environment variable
	t.Setenv("DD_SERVICE", "test-service")
	assert.Equal(t, "test-service", getServiceName())

	// Test with lambda function name
	t.Setenv("DD_SERVICE", "")
	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "my-function")
	assert.Equal(t, "my-function", getServiceName())
}
