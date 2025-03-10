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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/smithy-go/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			injectTraceContext(traceContext, messageAttributes)

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
