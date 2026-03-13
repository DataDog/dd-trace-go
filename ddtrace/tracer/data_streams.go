// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"time"

	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	idatastreams "github.com/DataDog/dd-trace-go/v2/internal/datastreams"
)

// dataStreamsContainer is an object that contains a data streams processor.
type dataStreamsContainer interface {
	GetDataStreamsProcessor() *idatastreams.Processor
}

// GetDataStreamsProcessor returns the processor tracking data streams stats
func (t *tracer) GetDataStreamsProcessor() *idatastreams.Processor {
	return t.dataStreams
}

// SetDataStreamsCheckpoint sets a consume or produce checkpoint in a Data Streams pathway.
// This enables tracking data flow & end to end latency.
// To learn more about the data streams product, see: https://docs.datadoghq.com/data_streams/go/
func SetDataStreamsCheckpoint(ctx context.Context, edgeTags ...string) (outCtx context.Context, ok bool) {
	return SetDataStreamsCheckpointWithParams(ctx, options.CheckpointParams{}, edgeTags...)
}

// SetDataStreamsCheckpointWithParams sets a consume or produce checkpoint in a Data Streams pathway.
// This enables tracking data flow & end to end latency.
// To learn more about the data streams product, see: https://docs.datadoghq.com/data_streams/go/
func SetDataStreamsCheckpointWithParams(ctx context.Context, params options.CheckpointParams, edgeTags ...string) (outCtx context.Context, ok bool) {
	if t, ok := getGlobalTracer().(dataStreamsContainer); ok {
		if processor := t.GetDataStreamsProcessor(); processor != nil {
			outCtx = processor.SetCheckpointWithParams(ctx, params, edgeTags...)
			return outCtx, true
		}
	}
	return ctx, false
}

// TrackKafkaCommitOffset should be used in the consumer, to track when it acks offset.
// if used together with TrackKafkaProduceOffset it can generate a Kafka lag in seconds metric.
func TrackKafkaCommitOffset(group, topic string, partition int32, offset int64) {
	TrackKafkaCommitOffsetWithCluster("", group, topic, partition, offset)
}

// TrackKafkaCommitOffsetWithCluster is like TrackKafkaCommitOffset but also associates the offset with a Kafka cluster ID.
func TrackKafkaCommitOffsetWithCluster(cluster, group, topic string, partition int32, offset int64) {
	if t, ok := getGlobalTracer().(dataStreamsContainer); ok {
		if p := t.GetDataStreamsProcessor(); p != nil {
			p.TrackKafkaCommitOffsetWithCluster(cluster, group, topic, partition, offset)
		}
	}
}

// TrackKafkaProduceOffset should be used in the producer, to track when it produces a message.
// if used together with TrackKafkaCommitOffset it can generate a Kafka lag in seconds metric.
func TrackKafkaProduceOffset(topic string, partition int32, offset int64) {
	TrackKafkaProduceOffsetWithCluster("", topic, partition, offset)
}

// TrackKafkaProduceOffsetWithCluster is like TrackKafkaProduceOffset but also associates the offset with a Kafka cluster ID.
func TrackKafkaProduceOffsetWithCluster(cluster string, topic string, partition int32, offset int64) {
	if t, ok := getGlobalTracer().(dataStreamsContainer); ok {
		if p := t.GetDataStreamsProcessor(); p != nil {
			p.TrackKafkaProduceOffsetWithCluster(cluster, topic, partition, offset)
		}
	}
}

// TrackKafkaHighWatermarkOffset should be used in the producer, to track when it produces a message.
// if used together with TrackKafkaCommitOffset it can generate a Kafka lag in seconds metric.
func TrackKafkaHighWatermarkOffset(cluster string, topic string, partition int32, offset int64) {
	if t, ok := getGlobalTracer().(dataStreamsContainer); ok {
		if p := t.GetDataStreamsProcessor(); p != nil {
			p.TrackKafkaHighWatermarkOffset(cluster, topic, partition, offset)
		}
	}
}

// TrackDataStreamsTransaction records a manual transaction checkpoint observation
// for Data Streams Monitoring. The active span in ctx (if any) is tagged with the
// transaction ID and checkpoint name for correlation in the Datadog UI.
// Pass context.Background() if you do not need span tagging.
//
// transactionID is an application-defined identifier for the transaction (e.g. a
// message ID or correlation ID). IDs longer than 255 bytes are silently truncated.
//
// checkpointName is a stable label for the processing stage (e.g. "ingested",
// "processed", "delivered"). A maximum of 254 unique checkpoint names are supported
// per processor lifetime; additional names beyond this limit are silently dropped.
func TrackDataStreamsTransaction(ctx context.Context, transactionID, checkpointName string) {
	TrackDataStreamsTransactionAt(ctx, transactionID, checkpointName, time.Now())
}

// TrackDataStreamsTransactionAt is like TrackDataStreamsTransaction but records the
// observation at time t instead of the current time. Use this when the transaction
// timestamp is already known (e.g. embedded in a message header).
func TrackDataStreamsTransactionAt(ctx context.Context, transactionID, checkpointName string, t time.Time) {
	tagActiveSpan(ctx, transactionID, checkpointName)
	if tr, ok := getGlobalTracer().(dataStreamsContainer); ok {
		if p := tr.GetDataStreamsProcessor(); p != nil {
			p.TrackTransactionAt(transactionID, checkpointName, t)
		}
	}
}

// tagActiveSpan sets the DSM transaction tags on the span stored in ctx, if any.
// If ctx is nil or contains no span, this is a no-op.
func tagActiveSpan(ctx context.Context, transactionID, checkpointName string) {
	if ctx == nil {
		return
	}
	if span, ok := SpanFromContext(ctx); ok {
		span.SetTag(ext.DSMTransactionID, transactionID)
		span.SetTag(ext.DSMTransactionCheckpoint, checkpointName)
	}
}
