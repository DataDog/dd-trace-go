// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package eventbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
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
	mt := mocktracer.Start()
	defer mt.Stop()

	baseCtx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:in", "topic:upstream", "type:kafka")
	span, spanCtx := tracer.StartSpanFromContext(baseCtx, "test-span")

	input := middleware.InitializeInput{
		Parameters: &eventbridge.PutEventsInput{
			Entries: []types.PutEventsRequestEntry{
				{
					Detail:       aws.String(`{"@123": "value", "_foo": "bar"}`),
					EventBusName: aws.String("test-bus"),
				},
				{
					Detail:       aws.String(`{"@123": "data", "_foo": "bar"}`),
					EventBusName: aws.String("test-bus-2"),
				},
			},
		},
	}

	params, ok := input.Parameters.(*eventbridge.PutEventsInput)
	require.True(t, ok)
	expectedPathways := make([]datastreams.Pathway, len(params.Entries))
	for i := range params.Entries {
		expectedCtx, ok := tracer.SetDataStreamsCheckpointWithParams(
			spanCtx,
			options.CheckpointParams{PayloadSize: payloadSize(&params.Entries[i])},
			eventBridgeEdgeTags(&params.Entries[i])...,
		)
		require.True(t, ok)
		expectedPathways[i], ok = datastreams.PathwayFromContext(expectedCtx)
		require.True(t, ok)
	}

	EnrichOperation(spanCtx, span, input, "PutEvents")

	require.Len(t, params.Entries, 2)

	for i, entry := range params.Entries {
		var detail map[string]interface{}
		err := json.Unmarshal([]byte(*entry.Detail), &detail)
		require.NoError(t, err)

		assert.Contains(t, detail, "@123") // make sure user data still exists
		assert.Contains(t, detail, "_foo")
		assert.Contains(t, detail, datadogKey)
		ddData, ok := detail[datadogKey].(map[string]interface{})
		require.True(t, ok)

		assert.Contains(t, ddData, startTimeKey)
		assert.Contains(t, ddData, resourceNameKey)
		assert.Contains(t, ddData, pathwayContextKey)
		assert.Equal(t, *entry.EventBusName, ddData[resourceNameKey])

		carrier := tracer.TextMapCarrier{}
		for k, v := range ddData {
			if s, ok := v.(string); ok {
				carrier[k] = s
			}
		}

		pathway, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), carrier))
		require.True(t, ok)
		assert.Equal(t, expectedPathways[i].GetHash(), pathway.GetHash())
	}
}

func TestInjectTraceContext(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	baseCtx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:in", "topic:upstream", "type:kafka")
	span, spanCtx := tracer.StartSpanFromContext(baseCtx, "test-span")

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
			expectedCtx, ok := tracer.SetDataStreamsCheckpointWithParams(
				spanCtx,
				options.CheckpointParams{PayloadSize: payloadSize(&tt.entry)},
				eventBridgeEdgeTags(&tt.entry)...,
			)
			require.True(t, ok)
			expectedPathway, ok := datastreams.PathwayFromContext(expectedCtx)
			require.True(t, ok)

			carrier, err := getTraceContext(spanCtx, span, &tt.entry, 123456789)
			require.NoError(t, err)

			injectTraceContext(carrier, &tt.entry)
			tt.expected(t, &tt.entry)

			var detail map[string]interface{}
			err = json.Unmarshal([]byte(*tt.entry.Detail), &detail)
			require.NoError(t, err)

			ddData := detail[datadogKey].(map[string]interface{})
			assert.Contains(t, ddData, startTimeKey)
			assert.Contains(t, ddData, resourceNameKey)
			assert.Contains(t, ddData, pathwayContextKey)
			assert.Equal(t, *tt.entry.EventBusName, ddData[resourceNameKey])

			// Check that start time exists and is not empty
			startTime, ok := ddData[startTimeKey]
			assert.True(t, ok)
			assert.Equal(t, startTime, "123456789")

			extractedCarrier := tracer.TextMapCarrier{}
			for k, v := range ddData {
				if s, ok := v.(string); ok {
					extractedCarrier[k] = s
				}
			}

			extractedSpanContext, err := tracer.Extract(&extractedCarrier)
			assert.NoError(t, err)
			assert.Equal(t, span.Context().TraceIDLower(), extractedSpanContext.TraceIDLower())
			assert.Equal(t, span.Context().SpanID(), extractedSpanContext.SpanID())

			pathway, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), extractedCarrier))
			require.True(t, ok)
			assert.Equal(t, expectedPathway.GetHash(), pathway.GetHash())
		})
	}
}

func TestInjectTraceContextSizeLimit(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	baseTraceContext := tracer.TextMapCarrier{
		"x-datadog-trace-id":      "12345",
		"x-datadog-parent-id":     "67890",
		"x-datadog-start-time":    "123456789",
		"x-datadog-resource-name": "test-bus",
	}

	tests := []struct {
		name     string
		entry    types.PutEventsRequestEntry
		expected func(*testing.T, *types.PutEventsRequestEntry)
	}{
		{
			name: "Do not inject when payload is too large",
			entry: types.PutEventsRequestEntry{
				Detail:       aws.String(`{"large": "` + strings.Repeat("a", maxSizeBytes-50) + `"}`),
				EventBusName: aws.String("test-bus"),
			},
			expected: func(t *testing.T, entry *types.PutEventsRequestEntry) {
				assert.GreaterOrEqual(t, len(*entry.Detail), maxSizeBytes-50)
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
			injectTraceContext(baseTraceContext, &tt.entry)
			tt.expected(t, &tt.entry)
		})
	}
}

func TestEventBridgeEdgeTags(t *testing.T) {
	entry := &types.PutEventsRequestEntry{
		EventBusName: aws.String("orders-bus"),
		DetailType:   aws.String("order.created"),
	}

	assert.Equal(t,
		[]string{"direction:out", "exchange:orders-bus", "topic:order.created", "type:eventbridge"},
		eventBridgeEdgeTags(entry),
	)
}
