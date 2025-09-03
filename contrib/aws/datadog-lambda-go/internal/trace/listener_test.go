/*
 * Unless explicitly stated otherwise all files in this repository are licensed
 * under the Apache License Version 2.0.
 *
 * This product includes software developed at Datadog (https://www.datadoghq.com/).
 * Copyright 2021 Datadog, Inc.
 */

package trace

import (
	"context"
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/contrib/aws/datadog-lambda-go/internal/extension"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/stretchr/testify/assert"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

func TestSeparateVersionFromFunctionArnWithVersion(t *testing.T) {
	inputArn := "arn:aws:lambda:us-east-1:123456789012:function:my-function:9"

	arnWithoutVersion, functionVersion := separateVersionFromFunctionArn(inputArn)

	expectedArnWithoutVersion := "arn:aws:lambda:us-east-1:123456789012:function:my-function"
	expectedFunctionVersion := "9"
	assert.Equal(t, expectedArnWithoutVersion, arnWithoutVersion)
	assert.Equal(t, expectedFunctionVersion, functionVersion)
}

func TestSeparateVersionFromFunctionArnWithoutVersion(t *testing.T) {
	inputArn := "arn:aws:lambda:us-east-1:123456789012:function:my-function"

	arnWithoutVersion, functionVersion := separateVersionFromFunctionArn(inputArn)

	expectedArnWithoutVersion := "arn:aws:lambda:us-east-1:123456789012:function:my-function"
	expectedFunctionVersion := "$LATEST"
	assert.Equal(t, expectedArnWithoutVersion, arnWithoutVersion)
	assert.Equal(t, expectedFunctionVersion, functionVersion)
}

func TestSeparateVersionFromFunctionArnEmptyString(t *testing.T) {
	inputArn := ""

	arnWithoutVersion, functionVersion := separateVersionFromFunctionArn(inputArn)
	assert.Empty(t, arnWithoutVersion)
	assert.Empty(t, functionVersion)
}

var traceContextFromXray = TraceContext{
	traceIDHeader:  "1231452342",
	parentIDHeader: "45678910",
}

var traceContextFromEvent = TraceContext{
	traceIDHeader:  "1231452342",
	parentIDHeader: "45678910",
}

var mockLambdaContext = lambdacontext.LambdaContext{
	AwsRequestID:       "abcdefgh-1234-5678-1234-abcdefghijkl",
	InvokedFunctionArn: "arn:aws:lambda:us-east-1:123456789012:function:MyFunction:11",
}

func TestStartFunctionExecutionSpanFromXrayWithMergeEnabled(t *testing.T) {
	ctx := context.Background()

	lambdacontext.FunctionName = "MockFunctionName"
	ctx = lambdacontext.NewContext(ctx, &mockLambdaContext)
	ctx = context.WithValue(ctx, traceContextKey, traceContextFromXray)
	//nolint
	ctx = context.WithValue(ctx, "cold_start", true)

	mt := mocktracer.Start()
	defer mt.Stop()

	span, ctx := startFunctionExecutionSpan(ctx, true, false)
	span.Finish()
	finishedSpan := mt.FinishedSpans()[0]

	assert.Equal(t, "aws.lambda", finishedSpan.OperationName())

	assert.Equal(t, true, finishedSpan.Tag("cold_start"))
	// We expect the function ARN to be lowercased, and the version removed
	assert.Equal(t, "arn:aws:lambda:us-east-1:123456789012:function:myfunction", finishedSpan.Tag("function_arn"))
	assert.Equal(t, "11", finishedSpan.Tag("function_version"))
	assert.Equal(t, "abcdefgh-1234-5678-1234-abcdefghijkl", finishedSpan.Tag("request_id"))
	assert.Equal(t, "MockFunctionName", finishedSpan.Tag("resource.name"))
	assert.Equal(t, "MockFunctionName", finishedSpan.Tag("resource_names"))
	assert.Equal(t, "mockfunctionname", finishedSpan.Tag("functionname"))
	assert.Equal(t, "serverless", finishedSpan.Tag("span.type"))
	assert.Equal(t, "xray", finishedSpan.Tag("_dd.parent_source"))
	assert.Equal(t, fmt.Sprint(span.Context().SpanID()), ctx.Value(extension.DdSpanId).(string))
}

func TestStartFunctionExecutionSpanFromXrayWithMergeDisabled(t *testing.T) {
	ctx := context.Background()

	lambdacontext.FunctionName = "MockFunctionName"
	ctx = lambdacontext.NewContext(ctx, &mockLambdaContext)
	ctx = context.WithValue(ctx, traceContextKey, traceContextFromXray)
	//nolint
	ctx = context.WithValue(ctx, "cold_start", true)

	mt := mocktracer.Start()
	defer mt.Stop()

	span, ctx := startFunctionExecutionSpan(ctx, false, false)
	span.Finish()
	finishedSpan := mt.FinishedSpans()[0]

	assert.Equal(t, nil, finishedSpan.Tag("_dd.parent_source"))
	assert.Equal(t, fmt.Sprint(span.Context().SpanID()), ctx.Value(extension.DdSpanId).(string))
}

func TestStartFunctionExecutionSpanFromEventWithMergeEnabled(t *testing.T) {
	ctx := context.Background()

	lambdacontext.FunctionName = "MockFunctionName"
	ctx = lambdacontext.NewContext(ctx, &mockLambdaContext)
	ctx = context.WithValue(ctx, traceContextKey, traceContextFromEvent)
	//nolint
	ctx = context.WithValue(ctx, "cold_start", true)

	mt := mocktracer.Start()
	defer mt.Stop()

	span, ctx := startFunctionExecutionSpan(ctx, true, false)
	span.Finish()
	finishedSpan := mt.FinishedSpans()[0]

	assert.Equal(t, "xray", finishedSpan.Tag("_dd.parent_source"))
	assert.Equal(t, fmt.Sprint(span.Context().SpanID()), ctx.Value(extension.DdSpanId).(string))
}

func TestStartFunctionExecutionSpanFromEventWithMergeDisabled(t *testing.T) {
	ctx := context.Background()

	lambdacontext.FunctionName = "MockFunctionName"
	ctx = lambdacontext.NewContext(ctx, &mockLambdaContext)
	ctx = context.WithValue(ctx, traceContextKey, traceContextFromEvent)
	//nolint
	ctx = context.WithValue(ctx, "cold_start", true)

	mt := mocktracer.Start()
	defer mt.Stop()

	span, ctx := startFunctionExecutionSpan(ctx, false, false)
	span.Finish()
	finishedSpan := mt.FinishedSpans()[0]

	assert.Equal(t, nil, finishedSpan.Tag("_dd.parent_source"))
	assert.Equal(t, fmt.Sprint(span.Context().SpanID()), ctx.Value(extension.DdSpanId).(string))
}

func TestStartFunctionExecutionSpanWithExtension(t *testing.T) {
	ctx := context.Background()

	lambdacontext.FunctionName = "MockFunctionName"
	ctx = lambdacontext.NewContext(ctx, &mockLambdaContext)
	ctx = context.WithValue(ctx, traceContextKey, traceContextFromEvent)
	//nolint
	ctx = context.WithValue(ctx, "cold_start", true)

	mt := mocktracer.Start()
	defer mt.Stop()

	span, ctx := startFunctionExecutionSpan(ctx, false, true)
	span.Finish()
	finishedSpan := mt.FinishedSpans()[0]

	assert.Equal(t, string(extension.DdSeverlessSpan), finishedSpan.Tag("resource.name"))
	assert.Equal(t, fmt.Sprint(span.Context().SpanID()), ctx.Value(extension.DdSpanId).(string))
}
