// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sns

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/smithy-go/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestEnrichOperation(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		input     middleware.InitializeInput
		setup     func(context.Context) *tracer.Span
		check     func(*testing.T, middleware.InitializeInput)
	}{
		{
			name:      "Publish",
			operation: "Publish",
			input: middleware.InitializeInput{
				Parameters: &sns.PublishInput{
					Message:  aws.String("test message"),
					TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:test-topic"),
				},
			},
			setup: func(ctx context.Context) *tracer.Span {
				span, _ := tracer.StartSpanFromContext(ctx, "test-span")
				return span
			},
			check: func(t *testing.T, in middleware.InitializeInput) {
				params, ok := in.Parameters.(*sns.PublishInput)
				require.True(t, ok)
				require.NotNil(t, params)
				require.NotNil(t, params.MessageAttributes)
				assert.Contains(t, params.MessageAttributes, datadogKey)
				assert.NotNil(t, params.MessageAttributes[datadogKey].DataType)
				assert.Equal(t, "Binary", *params.MessageAttributes[datadogKey].DataType)
				assert.NotNil(t, params.MessageAttributes[datadogKey].BinaryValue)
				assert.NotEmpty(t, params.MessageAttributes[datadogKey].BinaryValue)
			},
		},
		{
			name:      "PublishBatch",
			operation: "PublishBatch",
			input: middleware.InitializeInput{
				Parameters: &sns.PublishBatchInput{
					TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:test-topic"),
					PublishBatchRequestEntries: []types.PublishBatchRequestEntry{
						{
							Id:      aws.String("1"),
							Message: aws.String("test message 1"),
						},
						{
							Id:      aws.String("2"),
							Message: aws.String("test message 2"),
						},
					},
				},
			},
			setup: func(ctx context.Context) *tracer.Span {
				span, _ := tracer.StartSpanFromContext(ctx, "test-span")
				return span
			},
			check: func(t *testing.T, in middleware.InitializeInput) {
				params, ok := in.Parameters.(*sns.PublishBatchInput)
				require.True(t, ok)
				require.NotNil(t, params)
				require.NotNil(t, params.PublishBatchRequestEntries)
				require.Len(t, params.PublishBatchRequestEntries, 2)

				for _, entry := range params.PublishBatchRequestEntries {
					require.NotNil(t, entry.MessageAttributes)
					assert.Contains(t, entry.MessageAttributes, datadogKey)
					assert.NotNil(t, entry.MessageAttributes[datadogKey].DataType)
					assert.Equal(t, "Binary", *entry.MessageAttributes[datadogKey].DataType)
					assert.NotNil(t, entry.MessageAttributes[datadogKey].BinaryValue)
					assert.NotEmpty(t, entry.MessageAttributes[datadogKey].BinaryValue)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			ctx := context.Background()
			span := tt.setup(ctx)

			EnrichOperation(span, tt.input, tt.operation)

			if tt.check != nil {
				tt.check(t, tt.input)
			}
		})
	}
}

func TestPublishSizeLimit(t *testing.T) {
	t.Run("body at limit blocks injection", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span, _ := tracer.StartSpanFromContext(context.Background(), "test-span")

		input := middleware.InitializeInput{
			Parameters: &sns.PublishInput{
				Message:  aws.String(string(make([]byte, maxMessageSizeBytes))),
				TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:test-topic"),
			},
		}

		EnrichOperation(span, input, "Publish")

		params := input.Parameters.(*sns.PublishInput)
		assert.NotContains(t, params.MessageAttributes, datadogKey)
	})

	t.Run("body plus existing attributes at limit blocks injection", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span, _ := tracer.StartSpanFromContext(context.Background(), "test-span")
		traceCtx, err := getTraceContext(span)
		require.NoError(t, err)
		ctxSize := attributeSize(datadogKey, traceCtx)

		// Existing attribute eats into budget; body fills the rest.
		existingAttr := types.MessageAttributeValue{
			DataType:    aws.String("String"),
			StringValue: aws.String("val"),
		}
		existingAttrSize := attributeSize("myattr", existingAttr)
		bodyLen := maxMessageSizeBytes - existingAttrSize - ctxSize + 1 // +1 to go over
		require.Positive(t, bodyLen)

		input := middleware.InitializeInput{
			Parameters: &sns.PublishInput{
				Message:  aws.String(string(make([]byte, bodyLen))),
				TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:test-topic"),
				MessageAttributes: map[string]types.MessageAttributeValue{
					"myattr": existingAttr,
				},
			},
		}

		EnrichOperation(span, input, "Publish")

		params := input.Parameters.(*sns.PublishInput)
		assert.NotContains(t, params.MessageAttributes, datadogKey)
	})
}

func TestPublishBatchSizeLimit(t *testing.T) {
	t.Run("partial injection when budget exhausted", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span, _ := tracer.StartSpanFromContext(context.Background(), "test-span")

		traceCtx, err := getTraceContext(span)
		require.NoError(t, err)
		ctxSize := attributeSize(datadogKey, traceCtx)

		// Layout: firstBody + secondBody + ctxSize = maxMessageSizeBytes.
		// Injecting _datadog into entry 1 fills budget exactly;
		// entry 2 would push over → skipped.
		firstBody := "x"
		secondBodyLen := maxMessageSizeBytes - len(firstBody) - ctxSize
		require.Positive(t, secondBodyLen, "test setup: secondBodyLen must be positive")

		input := middleware.InitializeInput{
			Parameters: &sns.PublishBatchInput{
				TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:test-topic"),
				PublishBatchRequestEntries: []types.PublishBatchRequestEntry{
					{Id: aws.String("1"), Message: aws.String(firstBody)},
					{Id: aws.String("2"), Message: aws.String(string(make([]byte, secondBodyLen)))},
				},
			},
		}

		EnrichOperation(span, input, "PublishBatch")

		params := input.Parameters.(*sns.PublishBatchInput)
		assert.Contains(t, params.PublishBatchRequestEntries[0].MessageAttributes, datadogKey)
		assert.NotContains(t, params.PublishBatchRequestEntries[1].MessageAttributes, datadogKey)
	})

	t.Run("full-attribute entry does not inflate running size", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span, _ := tracer.StartSpanFromContext(context.Background(), "test-span")

		traceCtx, err := getTraceContext(span)
		require.NoError(t, err)
		ctxSize := attributeSize(datadogKey, traceCtx)

		// entry[0] has max attributes so injection is skipped;
		// entry[1] has room and must still receive _datadog.
		fullAttrs := make(map[string]types.MessageAttributeValue)
		for i := range maxMessageAttributes {
			fullAttrs[fmt.Sprintf("attr%d", i)] = types.MessageAttributeValue{
				DataType:    aws.String("String"),
				StringValue: aws.String("v"),
			}
		}

		smallBody := "small"
		fullAttrsSize := sizeAttributes(fullAttrs)
		bodyLen := maxMessageSizeBytes - fullAttrsSize - len(smallBody) - ctxSize
		require.Positive(t, bodyLen, "test setup: bodyLen must leave room for one injection")

		input := middleware.InitializeInput{
			Parameters: &sns.PublishBatchInput{
				TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:test-topic"),
				PublishBatchRequestEntries: []types.PublishBatchRequestEntry{
					{Id: aws.String("1"), Message: aws.String(string(make([]byte, bodyLen))), MessageAttributes: fullAttrs},
					{Id: aws.String("2"), Message: aws.String(smallBody)},
				},
			},
		}

		EnrichOperation(span, input, "PublishBatch")

		params := input.Parameters.(*sns.PublishBatchInput)
		assert.NotContains(t, params.PublishBatchRequestEntries[0].MessageAttributes, datadogKey,
			"entry with max attributes should not get _datadog")
		assert.Contains(t, params.PublishBatchRequestEntries[1].MessageAttributes, datadogKey,
			"entry with room should still get _datadog when prior entry was skipped")
	})

	t.Run("single entry at limit blocks injection", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span, _ := tracer.StartSpanFromContext(context.Background(), "test-span")

		input := middleware.InitializeInput{
			Parameters: &sns.PublishBatchInput{
				TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:test-topic"),
				PublishBatchRequestEntries: []types.PublishBatchRequestEntry{
					{Id: aws.String("1"), Message: aws.String(string(make([]byte, maxMessageSizeBytes)))},
				},
			},
		}

		EnrichOperation(span, input, "PublishBatch")

		params := input.Parameters.(*sns.PublishBatchInput)
		assert.NotContains(t, params.PublishBatchRequestEntries[0].MessageAttributes, datadogKey)
	})
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

			span := tracer.StartSpan("test-span")

			messageAttributes := make(map[string]types.MessageAttributeValue)
			for i := 0; i < tt.existingAttributes; i++ {
				messageAttributes[fmt.Sprintf("attr%d", i)] = types.MessageAttributeValue{
					DataType:    aws.String("String"),
					StringValue: aws.String("value"),
				}
			}

			traceContext, err := getTraceContext(span)
			assert.NoError(t, err)
			injected := injectTraceContext(traceContext, messageAttributes)
			assert.Equal(t, tt.expectInjection, injected)

			if tt.expectInjection {
				assert.Contains(t, messageAttributes, datadogKey)
				assert.NotNil(t, messageAttributes[datadogKey].DataType)
				assert.Equal(t, "Binary", *messageAttributes[datadogKey].DataType)
				assert.NotNil(t, messageAttributes[datadogKey].BinaryValue)
				assert.NotEmpty(t, messageAttributes[datadogKey].BinaryValue)

				carrier := tracer.TextMapCarrier{}
				err := json.Unmarshal(messageAttributes[datadogKey].BinaryValue, &carrier)
				assert.NoError(t, err)

				extractedSpanContext, err := tracer.Extract(carrier)
				assert.NoError(t, err)
				assert.Equal(t, span.Context().TraceID(), extractedSpanContext.TraceID())
				assert.Equal(t, span.Context().SpanID(), extractedSpanContext.SpanID())
			} else {
				assert.NotContains(t, messageAttributes, datadogKey)
			}
		})
	}
}
