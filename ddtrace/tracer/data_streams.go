// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"

	v2options "github.com/DataDog/dd-trace-go/v2/datastreams/options"
	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/datastreams/options"
)

// SetDataStreamsCheckpoint sets a consume or produce checkpoint in a Data Streams pathway.
// This enables tracking data flow & end to end latency.
// To learn more about the data streams product, see: https://docs.datadoghq.com/data_streams/go/
func SetDataStreamsCheckpoint(ctx context.Context, edgeTags ...string) (outCtx context.Context, ok bool) {
	return v2.SetDataStreamsCheckpoint(ctx, edgeTags...)
}

// SetDataStreamsCheckpointWithParams sets a consume or produce checkpoint in a Data Streams pathway.
// This enables tracking data flow & end to end latency.
// To learn more about the data streams product, see: https://docs.datadoghq.com/data_streams/go/
func SetDataStreamsCheckpointWithParams(ctx context.Context, params options.CheckpointParams, edgeTags ...string) (outCtx context.Context, ok bool) {
	ps := v2options.CheckpointParams{
		PayloadSize:     params.PayloadSize,
		ServiceOverride: params.ServiceOverride,
	}
	return v2.SetDataStreamsCheckpointWithParams(ctx, ps, edgeTags...)
}

// TrackKafkaCommitOffset should be used in the consumer, to track when it acks offset.
// if used together with TrackKafkaProduceOffset it can generate a Kafka lag in seconds metric.
func TrackKafkaCommitOffset(group, topic string, partition int32, offset int64) {
	v2.TrackKafkaCommitOffset(group, topic, partition, offset)
}

// TrackKafkaProduceOffset should be used in the producer, to track when it produces a message.
// if used together with TrackKafkaCommitOffset it can generate a Kafka lag in seconds metric.
func TrackKafkaProduceOffset(topic string, partition int32, offset int64) {
	v2.TrackKafkaProduceOffset(topic, partition, offset)
}

// TrackKafkaHighWatermarkOffset should be used in the producer, to track when it produces a message.
// if used together with TrackKafkaCommitOffset it can generate a Kafka lag in seconds metric.
func TrackKafkaHighWatermarkOffset(cluster string, topic string, partition int32, offset int64) {
	v2.TrackKafkaHighWatermarkOffset(cluster, topic, partition, offset)
}
