// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
)

// GetDataStreamsProcessor returns the processor tracking data streams stats
func (t *tracer) GetDataStreamsProcessor() *datastreams.Processor {
	return t.dataStreams
}

// SetDataStreamsCheckpoint sets a consume or produce checkpoint in a Data Streams pathway.
// This enables tracking data flow & end to end latency.
// To learn more about the data streams product, see: https://docs.datadoghq.com/data_streams/go/
func SetDataStreamsCheckpoint(ctx context.Context, edgeTags ...string) (p datastreams.Pathway, outCtx context.Context, ok bool) {
	return SetDataStreamsCheckpointWithParams(ctx, datastreams.NewCheckpointParams(), edgeTags...)
}

// SetDataStreamsCheckpointWithParams sets a consume or produce checkpoint in a Data Streams pathway.
// This enables tracking data flow & end to end latency.
// To learn more about the data streams product, see: https://docs.datadoghq.com/data_streams/go/
func SetDataStreamsCheckpointWithParams(ctx context.Context, params datastreams.CheckpointParams, edgeTags ...string) (p datastreams.Pathway, outCtx context.Context, ok bool) {
	if t, ok := internal.GetGlobalTracer().(datastreams.ProcessorContainer); ok {
		if processor := t.GetDataStreamsProcessor(); processor != nil {
			p, outCtx = processor.SetCheckpointWithParams(ctx, params, edgeTags...)
			return p, outCtx, true
		}
	}
	return datastreams.Pathway{}, ctx, false
}

// TrackKafkaCommitOffset should be used in the consumer, to track when it acks offset.
// if used together with TrackKafkaProduceOffset it can generate a Kafka lag in seconds metric.
func TrackKafkaCommitOffset(group, topic string, partition int32, offset int64) {
	if t, ok := internal.GetGlobalTracer().(datastreams.ProcessorContainer); ok {
		if p := t.GetDataStreamsProcessor(); p != nil {
			p.TrackKafkaCommitOffset(group, topic, partition, offset)
		}
	}
}

// TrackKafkaProduceOffset should be used in the producer, to track when it produces a message.
// if used together with TrackKafkaCommitOffset it can generate a Kafka lag in seconds metric.
func TrackKafkaProduceOffset(topic string, partition int32, offset int64) {
	if t, ok := internal.GetGlobalTracer().(datastreams.ProcessorContainer); ok {
		if p := t.GetDataStreamsProcessor(); p != nil {
			p.TrackKafkaProduceOffset(topic, partition, offset)
		}
	}
}
