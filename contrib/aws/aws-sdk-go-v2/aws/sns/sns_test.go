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
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type testCarrier struct {
	m map[string]string
}

func (c *testCarrier) Set(key, val string) {
	c.m[key] = val
}

func (c *testCarrier) ForeachKey(handler func(key, val string) error) error {
	for k, v := range c.m {
		if err := handler(k, v); err != nil {
			return err
		}
	}
	return nil
}

func TestEnrichOperation(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		input     middleware.InitializeInput
		setup     func(context.Context) context.Context
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
			setup: func(ctx context.Context) context.Context {
				_, ctx = tracer.StartSpanFromContext(ctx, "test-span")
				return ctx
			},
			check: func(t *testing.T, in middleware.InitializeInput) {
				params, ok := in.Parameters.(*sns.PublishInput)
				require.True(t, ok)
				require.NotNil(t, params)
				require.NotNil(t, params.MessageAttributes)
				assert.Contains(t, params.MessageAttributes, datadogKey)
				assert.NotNil(t, params.MessageAttributes[datadogKey].DataType)
				assert.Equal(t, "String", *params.MessageAttributes[datadogKey].DataType)
				assert.NotNil(t, params.MessageAttributes[datadogKey].StringValue)
				assert.NotEmpty(t, *params.MessageAttributes[datadogKey].StringValue)
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
			setup: func(ctx context.Context) context.Context {
				_, ctx = tracer.StartSpanFromContext(ctx, "test-span")
				return ctx
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
					assert.Equal(t, "String", *entry.MessageAttributes[datadogKey].DataType)
					assert.NotNil(t, entry.MessageAttributes[datadogKey].StringValue)
					assert.NotEmpty(t, *entry.MessageAttributes[datadogKey].StringValue)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			ctx := context.Background()
			if tt.setup != nil {
				ctx = tt.setup(ctx)
			}

			EnrichOperation(ctx, tt.input, tt.operation)

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

			ctx := context.Background()
			span, ctx := tracer.StartSpanFromContext(ctx, "test-span")

			messageAttributes := make(map[string]types.MessageAttributeValue)
			for i := 0; i < tt.existingAttributes; i++ {
				messageAttributes[fmt.Sprintf("attr%d", i)] = types.MessageAttributeValue{
					DataType:    aws.String("String"),
					StringValue: aws.String("value"),
				}
			}

			injectTraceContext(ctx, messageAttributes)

			if tt.expectInjection {
				assert.Contains(t, messageAttributes, datadogKey)
				assert.NotNil(t, messageAttributes[datadogKey].DataType)
				assert.Equal(t, "String", *messageAttributes[datadogKey].DataType)
				assert.NotNil(t, messageAttributes[datadogKey].StringValue)
				assert.NotEmpty(t, *messageAttributes[datadogKey].StringValue)

				var carrier testCarrier
				carrier.m = make(map[string]string)
				err := json.Unmarshal([]byte(*messageAttributes[datadogKey].StringValue), &carrier.m)
				assert.NoError(t, err)

				extractedSpanContext, err := tracer.Extract(&carrier)
				assert.NoError(t, err)
				assert.Equal(t, span.Context().TraceID(), extractedSpanContext.TraceID())
				assert.Equal(t, span.Context().SpanID(), extractedSpanContext.SpanID())
			} else {
				assert.NotContains(t, messageAttributes, datadogKey)
			}
		})
	}
}

func TestMessageCarrier(t *testing.T) {
	carrier := make(messageCarrier)

	carrier.Set("key1", "value1")
	carrier.Set("key2", "value2")

	assert.Equal(t, "value1", carrier["key1"])
	assert.Equal(t, "value2", carrier["key2"])
}