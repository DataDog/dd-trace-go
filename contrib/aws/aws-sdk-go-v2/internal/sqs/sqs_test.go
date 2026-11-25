// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/smithy-go/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
)

const pathwayContextKey = "dd-pathway-ctx-base64"

func TestEnrichOperation(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		input     middleware.InitializeInput
		setup     func(context.Context) *tracer.Span
		check     func(*testing.T, middleware.InitializeInput, *tracer.Span, context.Context)
	}{
		{
			name:      "SendMessage",
			operation: "SendMessage",
			input: middleware.InitializeInput{
				Parameters: &sqs.SendMessageInput{
					MessageBody: aws.String("test message"),
					QueueUrl:    aws.String("https://sqs.us-east-1.amazonaws.com/1234567890/test-queue"),
				},
			},
			setup: func(ctx context.Context) *tracer.Span {
				span, _ := tracer.StartSpanFromContext(ctx, "test-span")
				return span
			},
			check: func(t *testing.T, in middleware.InitializeInput, span *tracer.Span, spanCtx context.Context) {
				params, ok := in.Parameters.(*sqs.SendMessageInput)
				require.True(t, ok)
				require.NotNil(t, params)
				require.NotNil(t, params.MessageAttributes)
				assert.Contains(t, params.MessageAttributes, datadogKey)
				assert.NotNil(t, params.MessageAttributes[datadogKey].DataType)
				assert.Equal(t, "String", *params.MessageAttributes[datadogKey].DataType)
				assert.NotNil(t, params.MessageAttributes[datadogKey].StringValue)
				assert.NotEmpty(t, *params.MessageAttributes[datadogKey].StringValue)
				require.Equal(t, span.AsMap()["messaging.system"], "amazonsqs")

				expectedCtx, ok := tracer.SetDataStreamsCheckpointWithParams(
					spanCtx,
					options.CheckpointParams{PayloadSize: sendMessageSize(params)},
					"direction:out",
					"type:sqs",
					"topic:"+queueName(params.QueueUrl),
				)
				require.True(t, ok)
				expectedPathway, ok := datastreams.PathwayFromContext(expectedCtx)
				require.True(t, ok)
				assertInjectedPathway(t, *params.MessageAttributes[datadogKey].StringValue, expectedPathway, span)
			},
		},
		{
			name:      "SendMessageBatch",
			operation: "SendMessageBatch",
			input: middleware.InitializeInput{
				Parameters: &sqs.SendMessageBatchInput{
					QueueUrl: aws.String("https://sqs.us-east-1.amazonaws.com/1234567890/test-queue"),
					Entries: []types.SendMessageBatchRequestEntry{
						{
							Id:          aws.String("1"),
							MessageBody: aws.String("test message 1"),
						},
						{
							Id:          aws.String("2"),
							MessageBody: aws.String("test message 2"),
						},
						{
							Id:          aws.String("3"),
							MessageBody: aws.String("test message 3"),
						},
					},
				},
			},
			setup: func(ctx context.Context) *tracer.Span {
				span, _ := tracer.StartSpanFromContext(ctx, "test-span")
				return span
			},
			check: func(t *testing.T, in middleware.InitializeInput, span *tracer.Span, spanCtx context.Context) {
				params, ok := in.Parameters.(*sqs.SendMessageBatchInput)
				require.True(t, ok)
				require.NotNil(t, params)
				require.NotNil(t, params.Entries)
				require.Len(t, params.Entries, 3)

				for _, entry := range params.Entries {
					require.NotNil(t, entry.MessageAttributes)
					assert.Contains(t, entry.MessageAttributes, datadogKey)
					assert.NotNil(t, entry.MessageAttributes[datadogKey].DataType)
					assert.Equal(t, "String", *entry.MessageAttributes[datadogKey].DataType)
					assert.NotNil(t, entry.MessageAttributes[datadogKey].StringValue)
					assert.NotEmpty(t, *entry.MessageAttributes[datadogKey].StringValue)

					expectedCtx, ok := tracer.SetDataStreamsCheckpointWithParams(
						spanCtx,
						options.CheckpointParams{PayloadSize: sendMessageBatchEntrySize(&entry)},
						"direction:out",
						"type:sqs",
						"topic:"+queueName(params.QueueUrl),
					)
					require.True(t, ok)
					expectedPathway, ok := datastreams.PathwayFromContext(expectedCtx)
					require.True(t, ok)
					assertInjectedPathway(t, *entry.MessageAttributes[datadogKey].StringValue, expectedPathway, span)
				}
				require.Equal(t, span.AsMap()["messaging.system"], "amazonsqs")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			ctx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:in", "topic:upstream", "type:kafka")
			span, spanCtx := tracer.StartSpanFromContext(ctx, "test-span")

			input := NewEnricher(true).EnrichOperation(spanCtx, span, tt.input, tt.operation)

			if tt.check != nil {
				tt.check(t, input, span, spanCtx)
			}
		})
	}
}

func TestInjectTraceContext(t *testing.T) {
	tests := []struct {
		name               string
		existingAttributes int
		expectInjection    bool
	}{
		{
			name:               "Inject with no existing attributes",
			existingAttributes: 0,
			expectInjection:    true,
		},
		{
			name:               "Inject with some existing attributes",
			existingAttributes: 5,
			expectInjection:    true,
		},
		{
			name:               "No injection when at max attributes",
			existingAttributes: maxMessageAttributes,
			expectInjection:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			ctx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:in", "topic:upstream", "type:kafka")
			span, spanCtx := tracer.StartSpanFromContext(ctx, "test-span")

			messageAttributes := make(map[string]types.MessageAttributeValue)
			for i := 0; i < tt.existingAttributes; i++ {
				messageAttributes[fmt.Sprintf("attr%d", i)] = types.MessageAttributeValue{
					DataType:    aws.String("String"),
					StringValue: aws.String("value"),
				}
			}

			traceContext, err := getTraceContext(spanCtx, span, "test-queue", 42)
			assert.NoError(t, err)
			budget := traceBudget{remaining: maxMessageBytes}
			messageAttributes = budget.inject(traceContext, messageAttributes)

			if tt.expectInjection {
				assert.Contains(t, messageAttributes, datadogKey)
				assert.NotNil(t, messageAttributes[datadogKey].DataType)
				assert.Equal(t, "String", *messageAttributes[datadogKey].DataType)
				assert.NotNil(t, messageAttributes[datadogKey].StringValue)
				assert.NotEmpty(t, *messageAttributes[datadogKey].StringValue)

				carrier := tracer.TextMapCarrier{}
				err := json.Unmarshal([]byte(*messageAttributes[datadogKey].StringValue), &carrier)
				assert.NoError(t, err)

				extractedSpanContext, err := tracer.Extract(carrier)
				assert.NoError(t, err)
				assert.Equal(t, span.Context().TraceID(), extractedSpanContext.TraceID())
				assert.Equal(t, span.Context().SpanID(), extractedSpanContext.SpanID())
				assert.Contains(t, carrier, pathwayContextKey)
			} else {
				assert.NotContains(t, messageAttributes, datadogKey)
			}
		})
	}
}

func assertInjectedPathway(t *testing.T, raw string, expected datastreams.Pathway, span *tracer.Span) {
	t.Helper()

	carrier := tracer.TextMapCarrier{}
	err := json.Unmarshal([]byte(raw), &carrier)
	require.NoError(t, err)

	extractedSpanContext, err := tracer.Extract(carrier)
	require.NoError(t, err)
	assert.Equal(t, span.Context().TraceID(), extractedSpanContext.TraceID())
	assert.Equal(t, span.Context().SpanID(), extractedSpanContext.SpanID())
	assert.Contains(t, carrier, pathwayContextKey)

	pathway, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), carrier))
	require.True(t, ok)
	assert.Equal(t, expected.GetHash(), pathway.GetHash())
}

// BenchmarkEnrichOperation measures one SendMessage enrichment with the injection on and off.
func BenchmarkEnrichOperation(b *testing.B) {
	mt := mocktracer.Start()
	defer mt.Stop()
	span, ctx := tracer.StartSpanFromContext(context.Background(), "bench")
	defer span.Finish()

	for _, tc := range []struct {
		name   string
		inject bool
	}{{"on", true}, {"off", false}} {
		b.Run(tc.name, func(b *testing.B) {
			e := NewEnricher(tc.inject)
			b.ReportAllocs()
			for b.Loop() {
				in := middleware.InitializeInput{Parameters: &sqs.SendMessageInput{QueueUrl: aws.String("https://sqs.eu-west-1.amazonaws.com/123456789012/bench-queue"), MessageBody: aws.String(`{"message":"hello"}`)}}
				e.EnrichOperation(ctx, span, in, "SendMessage")
			}
		})
	}
}

func TestMessageSizeLimit(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "false")
	t.Setenv("DD_TRACE_PROPAGATION_STYLE_INJECT", "datadog")
	_, _, err := tracertest.Bootstrap(t)
	require.NoError(t, err)
	span, ctx := tracer.StartSpanFromContext(context.Background(), "produce")
	defer span.Finish()

	sendMessage := func(params *sqs.SendMessageInput) map[string]types.MessageAttributeValue {
		sent := NewEnricher(true).EnrichOperation(ctx, span, middleware.InitializeInput{Parameters: params}, "SendMessage")
		return sent.Parameters.(*sqs.SendMessageInput).MessageAttributes
	}
	sendBatch := func(inject bool, params *sqs.SendMessageBatchInput) []types.SendMessageBatchRequestEntry {
		sent := NewEnricher(inject).EnrichOperation(ctx, span, middleware.InitializeInput{Parameters: params}, "SendMessageBatch")
		return sent.Parameters.(*sqs.SendMessageBatchInput).Entries
	}

	const limit = 1 << 20
	probe := sendMessage(&sqs.SendMessageInput{MessageBody: aws.String("a")})
	require.Contains(t, probe, datadogKey)
	trace := probe[datadogKey]
	traceValueBytes := len(aws.ToString(trace.StringValue))
	require.Positive(t, traceValueBytes)
	traceSize := len(datadogKey) + len("String") + traceValueBytes
	requireTraceSize := func(t *testing.T, attrs map[string]types.MessageAttributeValue) {
		t.Helper()
		require.Equal(t, "String", aws.ToString(attrs[datadogKey].DataType))
		require.Len(t, aws.ToString(attrs[datadogKey].StringValue), traceValueBytes)
	}

	for _, tc := range []struct {
		name, suffix string
		extraBytes   int
	}{
		{"exact fit", "", 0},
		{"one byte over", "", 1},
		{"malformed body exact fit", "\xff", 0},
		{"malformed body one byte over", "\xff", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := func(otherBytes int) *string {
				padding := limit - traceSize - otherBytes - len(strings.ToValidUTF8(tc.suffix, "\uFFFD")) + tc.extraBytes
				return aws.String(strings.Repeat("b", padding) + tc.suffix)
			}
			single := sendMessage(&sqs.SendMessageInput{MessageBody: body(0)})

			const sharedSize = len("format") + len("Binary.custom") + 2
			shared := map[string]types.MessageAttributeValue{"format": {DataType: aws.String("Binary.custom"), BinaryValue: []byte{0, 1}}}
			params := &sqs.SendMessageBatchInput{Entries: []types.SendMessageBatchRequestEntry{
				{MessageBody: aws.String("a"), MessageAttributes: shared},
				{MessageBody: body(len("a") + 2*sharedSize), MessageAttributes: shared},
			}}
			sent := sendBatch(true, params)
			require.Len(t, sent, 2)
			if tc.extraBytes == 0 {
				requireTraceSize(t, single)
				requireTraceSize(t, sent[0].MessageAttributes)
			} else {
				require.Nil(t, single)
				require.NotContains(t, sent[0].MessageAttributes, datadogKey)
			}
			require.NotContains(t, sent[1].MessageAttributes, datadogKey)
			require.NotContains(t, shared, datadogKey)
			shared["format"] = types.MessageAttributeValue{DataType: aws.String("Binary.custom"), BinaryValue: []byte{1, 2}}
			require.Equal(t, shared["format"], params.Entries[1].MessageAttributes["format"])
		})
	}

	callerAttributes := func(existingTrace string) map[string]types.MessageAttributeValue {
		attrs := map[string]types.MessageAttributeValue{"user": {DataType: aws.String("String"), StringValue: aws.String("value")}}
		if existingTrace != "" {
			attrs[datadogKey] = types.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(existingTrace)}
		}
		return attrs
	}
	for _, tc := range []struct{ name, oldTrace string }{
		{"add trace", ""},
		{"replace trace", "\xff" + strings.Repeat("x", traceValueBytes-len("\uFFFD"))},
	} {
		t.Run(tc.name+" without changing caller attributes", func(t *testing.T) {
			const userSize = len("user") + len("String") + len("value")
			body := aws.String(strings.Repeat("a", limit-traceSize-userSize))
			attrs := callerAttributes(tc.oldTrace)
			original := callerAttributes(tc.oldTrace)
			single := &sqs.SendMessageInput{MessageBody: body, MessageAttributes: attrs}
			sentAttrs := sendMessage(single)
			requireTraceSize(t, sentAttrs)
			require.Equal(t, original["user"], sentAttrs["user"])
			require.Equal(t, original, single.MessageAttributes)
			require.Equal(t, original, attrs)

			batch := &sqs.SendMessageBatchInput{Entries: []types.SendMessageBatchRequestEntry{{MessageBody: body, MessageAttributes: attrs}}}
			sent := sendBatch(true, batch)
			require.Len(t, sent, 1)
			requireTraceSize(t, sent[0].MessageAttributes)
			require.Equal(t, original["user"], sent[0].MessageAttributes["user"])
			require.Equal(t, original, batch.Entries[0].MessageAttributes)
			require.Equal(t, original, attrs)
		})
	}

	t.Run("SDK expands malformed string attributes", func(t *testing.T) {
		const rawAttrSize = len("x") + len("String") + len("\xff")
		sent := sendBatch(true, &sqs.SendMessageBatchInput{Entries: []types.SendMessageBatchRequestEntry{
			{MessageBody: aws.String("a")},
			{MessageBody: aws.String(strings.Repeat("b", limit-1-traceSize-rawAttrSize)), MessageAttributes: map[string]types.MessageAttributeValue{
				"x": {DataType: aws.String("String"), StringValue: aws.String("\xff")},
			}},
		}})
		require.Len(t, sent, 2)
		require.Nil(t, sent[0].MessageAttributes)
		require.NotContains(t, sent[1].MessageAttributes, datadogKey)
	})

	for _, tc := range []struct {
		name      string
		inject    bool
		count     int
		wantTrace bool
	}{
		{"batch injection disabled", false, 10, false},
		{"maximum batch entries", true, 10, true},
		{"too many batch entries", true, 11, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := &sqs.SendMessageBatchInput{Entries: make([]types.SendMessageBatchRequestEntry, tc.count)}
			for index := range params.Entries {
				params.Entries[index].MessageBody = aws.String("a")
			}
			sent := sendBatch(tc.inject, params)
			require.Len(t, sent, tc.count)
			for _, entry := range sent {
				if tc.wantTrace {
					requireTraceSize(t, entry.MessageAttributes)
				} else {
					require.Nil(t, entry.MessageAttributes)
				}
			}
		})
	}

	t.Run("shared full attribute map", func(t *testing.T) {
		shared := make(map[string]types.MessageAttributeValue)
		for index := range 10 {
			shared[fmt.Sprintf("attr%d", index)] = types.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String("value")}
		}
		sent := sendBatch(true, &sqs.SendMessageBatchInput{Entries: []types.SendMessageBatchRequestEntry{
			{MessageBody: aws.String("a"), MessageAttributes: shared},
			{MessageBody: aws.String("b"), MessageAttributes: shared},
			{MessageBody: aws.String("c")},
		}})
		require.Len(t, sent, 3)
		require.NotContains(t, shared, datadogKey)
		require.Equal(t, shared, sent[0].MessageAttributes)
		require.Equal(t, shared, sent[1].MessageAttributes)
		requireTraceSize(t, sent[2].MessageAttributes)
	})
}
