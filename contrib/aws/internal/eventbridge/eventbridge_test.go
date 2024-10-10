// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package eventbridge

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestEnrichOperation(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	ctx := context.Background()
	_, ctx = tracer.StartSpanFromContext(ctx, "test-span")

	input := middleware.InitializeInput{
		Parameters: &eventbridge.PutEventsInput{
			Entries: []types.PutEventsRequestEntry{
				{
					Detail:       aws.String(`{"key": "value"}`),
					EventBusName: aws.String("test-bus"),
				},
				{
					Detail:       aws.String(`{"another": "data"}`),
					EventBusName: aws.String("test-bus-2"),
				},
			},
		},
	}

	EnrichOperation(ctx, input, "PutEvents")

	params, ok := input.Parameters.(*eventbridge.PutEventsInput)
	require.True(t, ok)
	require.Len(t, params.Entries, 2)

	for _, entry := range params.Entries {
		var detail map[string]interface{}
		err := json.Unmarshal([]byte(*entry.Detail), &detail)
		require.NoError(t, err)

		assert.Contains(t, detail, datadogKey)
		ddData, ok := detail[datadogKey].(map[string]interface{})
		require.True(t, ok)

		assert.Contains(t, ddData, startTimeKey)
		assert.Contains(t, ddData, resourceNameKey)
		assert.Equal(t, *entry.EventBusName, ddData[resourceNameKey])
	}
}

func TestInjectTraceContext(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	ctx := context.Background()
	span, ctx := tracer.StartSpanFromContext(ctx, "test-span")

	tests := []struct {
		name     string
		entry    types.PutEventsRequestEntry
		expected func(*testing.T, *types.PutEventsRequestEntry)
	}{
		{
			name: "Inject into empty detail",
			entry: types.PutEventsRequestEntry{
				EventBusName: aws.String("test-bus"),
			},
			expected: func(t *testing.T, entry *types.PutEventsRequestEntry) {
				assert.NotNil(t, entry.Detail)
				var detail map[string]interface{}
				err := json.Unmarshal([]byte(*entry.Detail), &detail)
				require.NoError(t, err)
				assert.Contains(t, detail, datadogKey)
			},
		},
		{
			name: "Inject into existing detail",
			entry: types.PutEventsRequestEntry{
				Detail:       aws.String(`{"existing": "data"}`),
				EventBusName: aws.String("test-bus"),
			},
			expected: func(t *testing.T, entry *types.PutEventsRequestEntry) {
				var detail map[string]interface{}
				err := json.Unmarshal([]byte(*entry.Detail), &detail)
				require.NoError(t, err)
				assert.Contains(t, detail, "existing")
				assert.Equal(t, "data", detail["existing"])
				assert.Contains(t, detail, datadogKey)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			injectTraceContext(ctx, &tt.entry)
			tt.expected(t, &tt.entry)

			var detail map[string]interface{}
			err := json.Unmarshal([]byte(*tt.entry.Detail), &detail)
			require.NoError(t, err)

			ddData := detail[datadogKey].(map[string]interface{})
			assert.Contains(t, ddData, startTimeKey)
			assert.Contains(t, ddData, resourceNameKey)
			assert.Equal(t, *tt.entry.EventBusName, ddData[resourceNameKey])

			// Check that start time exists and is not empty
			startTimeStr, ok := ddData[startTimeKey].(string)
			assert.True(t, ok)
			startTime, err := strconv.ParseInt(startTimeStr, 10, 64)
			assert.NoError(t, err)
			assert.Greater(t, startTime, int64(0))

			carrier := tracer.TextMapCarrier{}
			for k, v := range ddData {
				if s, ok := v.(string); ok {
					carrier[k] = s
				}
			}

			extractedSpanContext, err := tracer.Extract(&carrier)
			assert.NoError(t, err)
			assert.Equal(t, span.Context().TraceID(), extractedSpanContext.TraceID())
			assert.Equal(t, span.Context().SpanID(), extractedSpanContext.SpanID())
		})
	}
}

func TestInjectTraceContextSizeLimit(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	ctx := context.Background()
	_, ctx = tracer.StartSpanFromContext(ctx, "test-span")

	tests := []struct {
		name     string
		entry    types.PutEventsRequestEntry
		expected func(*testing.T, *types.PutEventsRequestEntry)
	}{
		{
			name: "Do not inject when payload is too large",
			entry: types.PutEventsRequestEntry{
				Detail:       aws.String(`{"large": "` + strings.Repeat("a", maxSizeBytes-15) + `"}`),
				EventBusName: aws.String("test-bus"),
			},
			expected: func(t *testing.T, entry *types.PutEventsRequestEntry) {
				assert.GreaterOrEqual(t, len(*entry.Detail), maxSizeBytes-15)
				assert.NotContains(t, *entry.Detail, datadogKey)
				assert.True(t, strings.HasPrefix(*entry.Detail, `{"large": "`))
				assert.True(t, strings.HasSuffix(*entry.Detail, `"}`))
			},
		},
		{
			name: "Inject when payload is just under the limit",
			entry: types.PutEventsRequestEntry{
				Detail:       aws.String(`{"large": "` + strings.Repeat("a", maxSizeBytes-1000) + `"}`),
				EventBusName: aws.String("test-bus"),
			},
			expected: func(t *testing.T, entry *types.PutEventsRequestEntry) {
				assert.Less(t, len(*entry.Detail), maxSizeBytes)
				var detail map[string]interface{}
				err := json.Unmarshal([]byte(*entry.Detail), &detail)
				require.NoError(t, err)
				assert.Contains(t, detail, datadogKey)
				assert.Contains(t, detail, "large")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			injectTraceContext(ctx, &tt.entry)
			tt.expected(t, &tt.entry)
		})
	}
}
