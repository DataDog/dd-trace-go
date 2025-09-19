// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package trace

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/extension"
	"github.com/aws/aws-xray-sdk-go/v2/header"
	"github.com/aws/aws-xray-sdk-go/v2/xray"
	"github.com/stretchr/testify/assert"
)

const (
	mockXRayEntityID      = "0b11cc4230d3e09e"
	mockXRayTraceID       = "1-5ce31dc2-2c779014b90ce44db5e03875"
	convertedXRayEntityID = "797643193680388254"
	convertedXRayTraceID  = "4110911582297405557"
)

func mockLambdaXRayTraceContext(ctx context.Context, traceID, parentID string, sampled bool) context.Context {
	decision := header.NotSampled
	if sampled {
		decision = header.Sampled
	}

	traceHeader := header.Header{
		TraceID:          traceID,
		ParentID:         parentID,
		SamplingDecision: decision,
		AdditionalData:   make(TraceContext),
	}
	headerString := traceHeader.String()
	//nolint
	return context.WithValue(ctx, xray.LambdaTraceHeaderKey, headerString)
}

func mockTraceContext(traceID, parentID, samplingPriority string) context.Context {
	ctx := context.Background()
	if traceID != "" {
		ctx = context.WithValue(ctx, extension.DdTraceId, traceID)
	}
	if parentID != "" {
		ctx = context.WithValue(ctx, extension.DdParentId, parentID)
	}
	if samplingPriority != "" {
		ctx = context.WithValue(ctx, extension.DdSamplingPriority, samplingPriority)
	}
	return ctx
}

func loadRawJSON(t *testing.T, filename string) *json.RawMessage {
	bytes, err := os.ReadFile(filename)
	if err != nil {
		assert.Fail(t, "Couldn't find JSON file")
		return nil
	}
	msg := json.RawMessage{}
	err = msg.UnmarshalJSON(bytes)
	assert.NoError(t, err)
	return &msg
}
func TestGetDatadogTraceContextForTraceMetadataNonProxyEvent(t *testing.T) {
	ctx := mockLambdaXRayTraceContext(context.Background(), mockXRayTraceID, mockXRayEntityID, true)
	ev := loadRawJSON(t, "../testdata/apig-event-with-headers.json")

	headers, ok := getTraceContext(ctx, getHeadersFromEventHeaders(ctx, *ev))
	assert.True(t, ok)

	expected := TraceContext{
		traceIDHeader:          "1231452342",
		parentIDHeader:         "45678910",
		samplingPriorityHeader: "2",
	}
	assert.Equal(t, expected, headers)
}

func TestGetDatadogTraceContextForTraceMetadataWithMixedCaseHeaders(t *testing.T) {
	ctx := mockLambdaXRayTraceContext(context.Background(), mockXRayTraceID, mockXRayEntityID, true)
	ev := loadRawJSON(t, "../testdata/non-proxy-with-mixed-case-headers.json")

	headers, ok := getTraceContext(ctx, getHeadersFromEventHeaders(ctx, *ev))
	assert.True(t, ok)

	expected := TraceContext{
		traceIDHeader:          "1231452342",
		parentIDHeader:         "45678910",
		samplingPriorityHeader: "2",
	}
	assert.Equal(t, expected, headers)
}

func TestGetDatadogTraceContextForTraceMetadataWithMissingSamplingPriority(t *testing.T) {
	ctx := mockLambdaXRayTraceContext(context.Background(), mockXRayTraceID, mockXRayEntityID, true)
	ev := loadRawJSON(t, "../testdata/non-proxy-with-missing-sampling-priority.json")

	headers, ok := getTraceContext(ctx, getHeadersFromEventHeaders(ctx, *ev))
	assert.True(t, ok)

	expected := TraceContext{
		traceIDHeader:          "1231452342",
		parentIDHeader:         "45678910",
		samplingPriorityHeader: "1",
	}
	assert.Equal(t, expected, headers)
}

func TestGetDatadogTraceContextForInvalidData(t *testing.T) {
	ctx := mockLambdaXRayTraceContext(context.Background(), mockXRayTraceID, mockXRayEntityID, true)
	ev := loadRawJSON(t, "../testdata/invalid.json")

	_, ok := getTraceContext(ctx, getHeadersFromEventHeaders(ctx, *ev))
	assert.False(t, ok)
}

func TestGetDatadogTraceContextForMissingData(t *testing.T) {
	ctx := mockLambdaXRayTraceContext(context.Background(), mockXRayTraceID, mockXRayEntityID, true)
	ev := loadRawJSON(t, "../testdata/non-proxy-no-headers.json")

	_, ok := getTraceContext(ctx, getHeadersFromEventHeaders(ctx, *ev))
	assert.False(t, ok)
}

func TestGetDatadogTraceContextFromContextObject(t *testing.T) {
	testcases := []struct {
		traceID          string
		parentID         string
		samplingPriority string
		expectTC         TraceContext
		expectOk         bool
	}{
		{
			"trace",
			"parent",
			"sampling",
			TraceContext{
				"x-datadog-trace-id":          "trace",
				"x-datadog-parent-id":         "parent",
				"x-datadog-sampling-priority": "sampling",
			},
			true,
		},
		{
			"",
			"parent",
			"sampling",
			TraceContext{},
			false,
		},
		{
			"trace",
			"",
			"sampling",
			TraceContext{},
			false,
		},
		{
			"trace",
			"parent",
			"",
			TraceContext{
				"x-datadog-trace-id":          "trace",
				"x-datadog-parent-id":         "parent",
				"x-datadog-sampling-priority": "1",
			},
			true,
		},
	}

	ev := loadRawJSON(t, "../testdata/non-proxy-no-headers.json")
	for _, test := range testcases {
		t.Run(test.traceID+test.parentID+test.samplingPriority, func(t *testing.T) {
			ctx := mockTraceContext(test.traceID, test.parentID, test.samplingPriority)
			tc, ok := getTraceContext(ctx, getHeadersFromEventHeaders(ctx, *ev))
			assert.Equal(t, test.expectTC, tc)
			assert.Equal(t, test.expectOk, ok)
		})
	}
}

func TestConvertXRayTraceID(t *testing.T) {
	output, err := convertXRayTraceIDToDatadogTraceID(mockXRayTraceID)
	assert.NoError(t, err)
	assert.Equal(t, convertedXRayTraceID, output)
}

func TestConvertXRayTraceIDTooShort(t *testing.T) {
	output, err := convertXRayTraceIDToDatadogTraceID("1-5ce31dc2-5e03875")
	assert.Error(t, err)
	assert.Equal(t, "0", output)
}

func TestConvertXRayTraceIDInvalidFormat(t *testing.T) {
	output, err := convertXRayTraceIDToDatadogTraceID("1-2c779014b90ce44db5e03875")
	assert.Error(t, err)
	assert.Equal(t, "0", output)
}
func TestConvertXRayTraceIDIncorrectCharacters(t *testing.T) {
	output, err := convertXRayTraceIDToDatadogTraceID("1-5ce31dc2-c779014b90ce44db5e03875;")
	assert.Error(t, err)
	assert.Equal(t, "0", output)
}

func TestConvertXRayEntityID(t *testing.T) {
	output, err := convertXRayEntityIDToDatadogParentID(mockXRayEntityID)
	assert.NoError(t, err)
	assert.Equal(t, convertedXRayEntityID, output)
}

func TestConvertXRayEntityIDInvalidFormat(t *testing.T) {
	output, err := convertXRayEntityIDToDatadogParentID(";b11cc4230d3e09e")
	assert.Error(t, err)
	assert.Equal(t, "0", output)
}

func TestConvertXRayEntityIDTooShort(t *testing.T) {
	output, err := convertXRayEntityIDToDatadogParentID("c4230d3e09e")
	assert.Error(t, err)
	assert.Equal(t, "0", output)
}

func TestXrayTraceContextNoSegment(t *testing.T) {
	ctx := context.Background()

	_, err := convertXrayTraceContextFromLambdaContext(ctx)
	assert.Error(t, err)
}
func TestXrayTraceContextWithSegment(t *testing.T) {

	ctx := mockLambdaXRayTraceContext(context.Background(), mockXRayTraceID, mockXRayEntityID, true)

	headers, err := convertXrayTraceContextFromLambdaContext(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "2", headers[samplingPriorityHeader])
	assert.NotNil(t, headers[traceIDHeader])
	assert.NotNil(t, headers[parentIDHeader])
}

func TestContextWithRootTraceContextNoDatadogContext(t *testing.T) {
	ctx := mockLambdaXRayTraceContext(context.Background(), mockXRayTraceID, mockXRayEntityID, true)
	ev := loadRawJSON(t, "../testdata/apig-event-no-headers.json")

	newCTX, _ := contextWithRootTraceContext(ctx, *ev, false, DefaultTraceExtractor)
	traceContext, _ := newCTX.Value(traceContextKey).(TraceContext)

	expected := TraceContext{}
	assert.Equal(t, expected, traceContext)
}

func TestContextWithRootTraceContextWithDatadogContext(t *testing.T) {
	ctx := mockLambdaXRayTraceContext(context.Background(), mockXRayTraceID, mockXRayEntityID, true)
	ev := loadRawJSON(t, "../testdata/apig-event-with-headers.json")

	newCTX, _ := contextWithRootTraceContext(ctx, *ev, false, DefaultTraceExtractor)
	traceContext, _ := newCTX.Value(traceContextKey).(TraceContext)

	expected := TraceContext{
		traceIDHeader:          "1231452342",
		parentIDHeader:         "45678910",
		samplingPriorityHeader: "2",
	}
	assert.Equal(t, expected, traceContext)
}

func TestContextWithRootTraceContextMergeXrayTracesNoDatadogContext(t *testing.T) {
	ctx := mockLambdaXRayTraceContext(context.Background(), mockXRayTraceID, mockXRayEntityID, true)
	ev := loadRawJSON(t, "../testdata/apig-event-no-headers.json")

	newCTX, _ := contextWithRootTraceContext(ctx, *ev, true, DefaultTraceExtractor)
	traceContext, _ := newCTX.Value(traceContextKey).(TraceContext)

	expected := TraceContext{
		traceIDHeader:          convertedXRayTraceID,
		parentIDHeader:         convertedXRayEntityID,
		samplingPriorityHeader: "2",
	}
	assert.Equal(t, expected, traceContext)
}

func TestContextWithRootTraceContextMergeXrayTracesWithDatadogContext(t *testing.T) {
	ctx := mockLambdaXRayTraceContext(context.Background(), mockXRayTraceID, mockXRayEntityID, true)
	ev := loadRawJSON(t, "../testdata/apig-event-with-headers.json")

	newCTX, _ := contextWithRootTraceContext(ctx, *ev, true, DefaultTraceExtractor)
	traceContext, _ := newCTX.Value(traceContextKey).(TraceContext)

	expected := TraceContext{
		traceIDHeader:          "1231452342",
		parentIDHeader:         convertedXRayEntityID,
		samplingPriorityHeader: "2",
	}
	assert.Equal(t, expected, traceContext)
}
