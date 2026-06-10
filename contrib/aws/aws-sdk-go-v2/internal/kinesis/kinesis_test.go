// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kinesis

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"
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
		Parameters: &kinesis.PutRecordsInput{
			StreamName: aws.String("orders-stream"),
			Records: []types.PutRecordsRequestEntry{
				{
					Data:         []byte(`{"message":"one"}`),
					PartitionKey: aws.String("pk-1"),
				},
				{
					Data:         []byte(`{"message":"two"}`),
					PartitionKey: aws.String("pk-2"),
				},
			},
		},
	}

	params, ok := input.Parameters.(*kinesis.PutRecordsInput)
	require.True(t, ok)

	expectedPathway, ok := expectedKinesisPathway(spanCtx, "orders-stream", int64(putRecordsEntrySize(&params.Records[0])))
	require.True(t, ok)

	EnrichOperation(spanCtx, span, input, "PutRecords", true)

	for _, record := range params.Records {
		var payload map[string]interface{}
		err := json.Unmarshal(record.Data, &payload)
		require.NoError(t, err)

		ddData, ok := payload[datadogKey].(map[string]interface{})
		require.True(t, ok)
		assert.Contains(t, ddData, "x-datadog-trace-id")
		assert.Contains(t, ddData, "x-datadog-parent-id")
		assert.Contains(t, ddData, pathwayContextKey)

		carrier := tracer.TextMapCarrier{}
		for k, v := range ddData {
			if s, ok := v.(string); ok {
				carrier[k] = s
			}
		}

		pathway, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), carrier))
		require.True(t, ok)
		assert.Equal(t, expectedPathway.GetHash(), pathway.GetHash())
	}
}

func TestEnrichOperationDSMDisabled(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	baseCtx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:in", "topic:upstream", "type:kafka")
	span, spanCtx := tracer.StartSpanFromContext(baseCtx, "test-span")

	input := middleware.InitializeInput{
		Parameters: &kinesis.PutRecordInput{
			StreamName:   aws.String("my-stream"),
			Data:         []byte(`{"message":"hello"}`),
			PartitionKey: aws.String("pk"),
		},
	}

	EnrichOperation(spanCtx, span, input, "PutRecord", false)

	params := input.Parameters.(*kinesis.PutRecordInput)
	var payload map[string]interface{}
	err := json.Unmarshal(params.Data, &payload)
	require.NoError(t, err)
	ddData, ok := payload[datadogKey].(map[string]interface{})
	require.True(t, ok)
	assert.NotContains(t, ddData, pathwayContextKey)
}

func TestInjectTraceContextSkipsMalformedData(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	baseCtx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:in", "topic:upstream", "type:kafka")
	span, spanCtx := tracer.StartSpanFromContext(baseCtx, "test-span")

	params := &kinesis.PutRecordInput{
		StreamName:   aws.String("orders-stream"),
		Data:         []byte("not-json"),
		PartitionKey: aws.String("pk-1"),
	}

	EnrichOperation(spanCtx, span, middleware.InitializeInput{Parameters: params}, "PutRecord", true)
	assert.Equal(t, []byte("not-json"), params.Data)
}

func TestInjectTraceContextSizeLimit(t *testing.T) {
	carrier := tracer.TextMapCarrier{
		"x-datadog-trace-id":  "12345",
		"x-datadog-parent-id": "67890",
	}

	partitionKey := aws.String("pk")
	data := []byte(`{"large":"` + strings.Repeat("a", maxRecordSizeBytes) + `"}`)
	assert.Equal(t, data, injectTraceContext(carrier, data, partitionKey))
}

func expectedKinesisPathway(ctx context.Context, stream string, payloadSize int64) (datastreams.Pathway, bool) {
	expectedCtx, ok := tracer.SetDataStreamsCheckpointWithParams(
		ctx,
		options.CheckpointParams{PayloadSize: payloadSize},
		"direction:out",
		"type:kinesis",
		"topic:"+stream,
	)
	if !ok {
		return nil, false
	}
	pathway, ok := datastreams.PathwayFromContext(expectedCtx)
	return pathway, ok
}
