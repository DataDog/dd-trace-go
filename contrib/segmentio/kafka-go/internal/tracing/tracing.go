// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package tracing contains tracing logic for the segmentio/kafka-go.v0 instrumentation.
//
// WARNING: this package SHOULD NOT import segmentio/kafka-go.
//
// The motivation of this package is to support orchestrion, which cannot use the main package because it imports
// the segmentio/kafka-go package, and since orchestrion modifies the library code itself,
// this would cause an import cycle.
package tracing

import (
	"context"
	"math"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const componentName = "segmentio/kafka.go.v0"

// newConsumerSpanConfig builds the base StartSpanConfig holding the tags
// that stay constant for every message consumed through a Tracer with the
// given config, so per-message calls don't need to rebuild them.
func newConsumerSpanConfig(tr *Tracer) *tracer.StartSpanConfig {
	opts := []tracer.StartSpanOption{
		instrumentation.ServiceNameWithSource(tr.consumerServiceName, tr.serviceSource),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Measured(),
	}
	if tr.kafkaCfg.BootstrapServers != "" {
		opts = append(opts, tracer.Tag(ext.KafkaBootstrapServers, tr.kafkaCfg.BootstrapServers))
	}
	if !math.IsNaN(tr.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tr.analyticsRate))
	}
	return tracer.NewStartSpanConfig(opts...)
}

func (tr *Tracer) StartConsumeSpan(ctx context.Context, msg Message) *tracer.Span {
	// Partition, offset, topic, and the Kafka cluster ID (fetched
	// asynchronously in the background after the Tracer is created, see
	// startFetchClusterID in the calling kafka-go package) are genuinely
	// per-message/mutable, so they stay dynamic instead of moving into the
	// static consumerSpanCfg base.
	tags := map[string]any{
		ext.ResourceName:             "Consume Topic " + msg.GetTopic(),
		ext.MessagingKafkaPartition:  msg.GetPartition(),
		"offset":                     msg.GetOffset(),
		ext.MessagingDestinationName: msg.GetTopic(),
	}
	if tr.ClusterID() != "" {
		tags[ext.MessagingKafkaClusterID] = tr.ClusterID()
	}
	opts := []tracer.StartSpanOption{
		tracer.WithTags(tags),
		tracer.WithStartSpanConfig(tr.consumerSpanCfg),
	}
	// kafka supports headers, so try to extract a span context
	carrier := NewMessageCarrier(msg)
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(ctx, tr.consumerSpanName, opts...)
	// reinject the span context so consumers can pick it up
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		instr.Logger().Debug("contrib/segmentio/kafka-go: Failed to inject span context into carrier in reader, %s", err.Error())
	}
	return span
}

// newProducerSpanConfig builds the base StartSpanConfig holding the tags
// that stay constant for every message produced through a Tracer with the
// given config, so per-message calls don't need to rebuild them.
func newProducerSpanConfig(tr *Tracer) *tracer.StartSpanConfig {
	opts := []tracer.StartSpanOption{
		instrumentation.ServiceNameWithSource(tr.producerServiceName, tr.serviceSource),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
	}
	if tr.kafkaCfg.BootstrapServers != "" {
		opts = append(opts, tracer.Tag(ext.KafkaBootstrapServers, tr.kafkaCfg.BootstrapServers))
	}
	if !math.IsNaN(tr.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tr.analyticsRate))
	}
	return tracer.NewStartSpanConfig(opts...)
}

func (tr *Tracer) StartProduceSpan(ctx context.Context, writer Writer, msg Message, spanOpts ...tracer.StartSpanOption) *tracer.Span {
	topic := writer.GetTopic()
	if topic == "" {
		topic = msg.GetTopic()
	}
	// Topic and the Kafka cluster ID (fetched asynchronously in the
	// background after the Tracer is created, see startFetchClusterID in the
	// calling kafka-go package) are genuinely per-message/mutable, so they
	// stay dynamic instead of moving into the static producerSpanCfg base.
	tags := map[string]any{
		ext.ResourceName:             "Produce Topic " + topic,
		ext.MessagingDestinationName: topic,
	}
	if tr.ClusterID() != "" {
		tags[ext.MessagingKafkaClusterID] = tr.ClusterID()
	}
	opts := []tracer.StartSpanOption{
		tracer.WithTags(tags),
		tracer.WithStartSpanConfig(tr.producerSpanCfg),
	}
	// spanOpts is caller-supplied and must run last so it can override any
	// tag above, exactly as it did in the pre-migration option list.
	opts = append(opts, spanOpts...)
	carrier := NewMessageCarrier(msg)
	span, _ := tracer.StartSpanFromContext(ctx, tr.producerSpanName, opts...)
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		instr.Logger().Debug("contrib/segmentio/kafka-go: Failed to inject span context into carrier in writer, %s", err.Error())
	}
	return span
}

func (*Tracer) FinishProduceSpan(span *tracer.Span, partition int, offset int64, err error) {
	span.SetTag(ext.MessagingKafkaPartition, partition)
	span.SetTag("offset", offset)
	span.Finish(tracer.WithError(err))
}
