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

	NewEnricher(true).EnrichOperation(spanCtx, span, input, "PutRecords")

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

	NewEnricher(true).EnrichOperation(spanCtx, span, middleware.InitializeInput{Parameters: params}, "PutRecord")
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

func TestEnricherInjection(t *testing.T) {
	const original = `{"message":"hello"}`
	tests := []struct {
		name     string
		enricher *enricher
		injected bool
	}{
		{"zero value injects", &enricher{}, true},
		{"enabled", NewEnricher(true), true},
		{"disabled", NewEnricher(false), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			span, spanCtx := tracer.StartSpanFromContext(context.Background(), "test-span")
			params := &kinesis.PutRecordInput{
				StreamName:   aws.String("orders-stream"),
				Data:         []byte(original),
				PartitionKey: aws.String("pk-1"),
			}

			tt.enricher.EnrichOperation(spanCtx, span, middleware.InitializeInput{Parameters: params}, "PutRecord")

			assert.Equal(t, tt.injected, strings.Contains(string(params.Data), "_datadog"))
			if !tt.injected {
				assert.JSONEq(t, original, string(params.Data))
			}
		})
	}
}

// BenchmarkEnrichOperation measures one PutRecord enrichment with the injection on and off.
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
				in := middleware.InitializeInput{Parameters: &kinesis.PutRecordInput{StreamName: aws.String("bench-stream"), PartitionKey: aws.String("pk"), Data: []byte(`{"message":"hello"}`)}}
				e.EnrichOperation(ctx, span, in, "PutRecord")
			}
		})
	}
}
