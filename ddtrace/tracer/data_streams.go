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

// SetDataStreamsCheckpoint sets a consume or produce checkpoint in a Data Streams pathway.
// This enables tracking data flow & end to end latency.
// To learn more about the data streams product, see: https://docs.datadoghq.com/data_streams/go/
func SetDataStreamsCheckpoint(ctx context.Context, edgeTags ...string) (p datastreams.Pathway, outCtx context.Context, ok bool) {
	if t, ok := internal.GetGlobalTracer().(*tracer); ok {
		if t.dataStreams != nil {
			p, outCtx = t.dataStreams.SetCheckpoint(ctx, edgeTags...)
			return p, outCtx, true
		}
	}
	return datastreams.Pathway{}, ctx, false
}

// TrackKafkaCommitOffset should be used in the consumer, to track when it acks offset.
// if used together with TrackKafkaProduceOffset it can generate a Kafka lag in seconds metric.
func TrackKafkaCommitOffset(group, topic string, partition int32, offset int64) {
	if t, ok := internal.GetGlobalTracer().(*tracer); ok {
		if t.dataStreams != nil {
			t.dataStreams.TrackKafkaCommitOffset(group, topic, partition, offset)
		}
	}
}

// TrackKafkaProduceOffset should be used in the producer, to track when it produces a message.
// if used together with TrackKafkaCommitOffset it can generate a Kafka lag in seconds metric.
func TrackKafkaProduceOffset(topic string, partition int32, offset int64) {
	if t, ok := internal.GetGlobalTracer().(*tracer); ok {
		if t.dataStreams != nil {
			t.dataStreams.TrackKafkaProduceOffset(topic, partition, offset)
		}
	}
}
func TrackPayload(topic string, partition int32, offset int64, payload []byte) {
	if t, ok := internal.GetGlobalTracer().(*tracer); ok {
		if t.dataStreams != nil {
			t.dataStreams.TrackPayload(topic, partition, offset, payload)
		}
	}
}
