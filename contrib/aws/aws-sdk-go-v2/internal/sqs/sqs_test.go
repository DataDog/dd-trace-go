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
			budget.inject(traceContext, messageAttributes)

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
	mt := mocktracer.Start()
	defer mt.Stop()
	span, ctx := tracer.StartSpanFromContext(context.Background(), "produce")
	defer span.Finish()
	enricher := NewEnricher(true)
	const limit = 1 << 20
	small := &sqs.SendMessageInput{MessageBody: aws.String("a")}
	enricher.EnrichOperation(ctx, span, middleware.InitializeInput{Parameters: small}, "SendMessage")
	require.Contains(t, small.MessageAttributes, datadogKey)
	trace := small.MessageAttributes[datadogKey]
	traceValueBytes := len(aws.ToString(trace.StringValue))
	traceSize := len(datadogKey) + len(aws.ToString(trace.DataType)) + traceValueBytes

	t.Run("single message", func(t *testing.T) {
		params := &sqs.SendMessageInput{MessageBody: aws.String(strings.Repeat("a", limit))}
		enricher.EnrichOperation(ctx, span, middleware.InitializeInput{Parameters: params}, "SendMessage")
		require.NotContains(t, params.MessageAttributes, datadogKey)
	})

	t.Run("existing trace attribute at the size limit", func(t *testing.T) {
		old := "\xff" + strings.Repeat("x", traceValueBytes-2)
		params := &sqs.SendMessageInput{
			MessageAttributes: map[string]types.MessageAttributeValue{datadogKey: {DataType: aws.String("String"), StringValue: aws.String(old)}},
			MessageBody:       aws.String(strings.Repeat("a", limit-len(datadogKey)-len("String")-len(strings.ToValidUTF8(old, "\uFFFD")))),
		}
		enricher.EnrichOperation(ctx, span, middleware.InitializeInput{Parameters: params}, "SendMessage")
		replacement := aws.ToString(params.MessageAttributes[datadogKey].StringValue)
		require.NotEmpty(t, replacement)
		require.NotEqual(t, old, replacement)
		require.LessOrEqual(t, sendMessageSize(params), int64(limit))
	})

	t.Run("batch shares the message size budget", func(t *testing.T) {
		const sharedSize = len("format") + len("Binary.custom") + 2
		sendBatch := func(secondBodyBytes int) []types.SendMessageBatchRequestEntry {
			shared := map[string]types.MessageAttributeValue{"format": {DataType: aws.String("Binary.custom"), BinaryValue: []byte{0, 1}}}
			params := &sqs.SendMessageBatchInput{Entries: []types.SendMessageBatchRequestEntry{
				{MessageBody: aws.String("a"), MessageAttributes: shared},
				{MessageBody: aws.String(strings.Repeat("b", secondBodyBytes)), MessageAttributes: shared},
			}}
			sent := enricher.EnrichOperation(ctx, span, middleware.InitializeInput{Parameters: params}, "SendMessageBatch")
			shared["format"] = types.MessageAttributeValue{DataType: aws.String("Binary.custom"), BinaryValue: []byte{1, 2}}
			require.Equal(t, shared["format"], params.Entries[1].MessageAttributes["format"])
			require.NotContains(t, shared, datadogKey)
			return sent.Parameters.(*sqs.SendMessageBatchInput).Entries
		}

		exactFit := limit - 1 - traceSize - 2*sharedSize
		entries := sendBatch(exactFit)
		require.Contains(t, entries[0].MessageAttributes, datadogKey)
		require.NotContains(t, entries[1].MessageAttributes, datadogKey)

		entries = sendBatch(exactFit + 1)
		require.NotContains(t, entries[0].MessageAttributes, datadogKey)
		require.NotContains(t, entries[1].MessageAttributes, datadogKey)

		t.Run("SDK expands malformed string attributes", func(t *testing.T) {
			const rawAttrSize = len("x") + len("String") + len("\xff")
			params := &sqs.SendMessageBatchInput{Entries: []types.SendMessageBatchRequestEntry{
				{MessageBody: aws.String("a")},
				{MessageBody: aws.String(strings.Repeat("b", limit-1-traceSize-rawAttrSize)), MessageAttributes: map[string]types.MessageAttributeValue{
					"x": {DataType: aws.String("String"), StringValue: aws.String("\xff")},
				}},
			}}
			sent := enricher.EnrichOperation(ctx, span, middleware.InitializeInput{Parameters: params}, "SendMessageBatch")
			for _, entry := range sent.Parameters.(*sqs.SendMessageBatchInput).Entries {
				require.NotContains(t, entry.MessageAttributes, datadogKey)
			}
		})
	})
}
