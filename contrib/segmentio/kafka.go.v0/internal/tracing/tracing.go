// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

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

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const componentName = "segmentio/kafka.go.v0"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/segmentio/kafka-go")
}

func (tr *Tracer) StartConsumeSpan(ctx context.Context, msg Message) ddtrace.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(tr.consumerServiceName),
		tracer.ResourceName("Consume Topic " + msg.GetTopic()),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.MessagingKafkaPartition, msg.GetPartition()),
		tracer.Tag("offset", msg.GetOffset()),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.KafkaBootstrapServers, tr.kafkaCfg.BootstrapServers),
		tracer.Measured(),
	}
	if !math.IsNaN(tr.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tr.analyticsRate))
	}
	// kafka supports headers, so try to extract a span context
	carrier := NewMessageCarrier(msg)
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(ctx, tr.consumerSpanName, opts...)
	// reinject the span context so consumers can pick it up
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		log.Debug("contrib/segmentio/kafka.go.v0: Failed to inject span context into carrier in reader, %v", err)
	}
	return span
}

func (tr *Tracer) StartProduceSpan(ctx context.Context, writer Writer, msg Message, spanOpts ...tracer.StartSpanOption) ddtrace.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(tr.producerServiceName),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.KafkaBootstrapServers, tr.kafkaCfg.BootstrapServers),
	}
	if writer.GetTopic() != "" {
		opts = append(opts, tracer.ResourceName("Produce Topic "+writer.GetTopic()))
	} else {
		opts = append(opts, tracer.ResourceName("Produce Topic "+msg.GetTopic()))
	}
	if !math.IsNaN(tr.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tr.analyticsRate))
	}
	opts = append(opts, spanOpts...)
	carrier := NewMessageCarrier(msg)
	span, _ := tracer.StartSpanFromContext(ctx, tr.producerSpanName, opts...)
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		log.Debug("contrib/segmentio/kafka.go.v0: Failed to inject span context into carrier in writer, %v", err)
	}
	return span
}

func (*Tracer) FinishProduceSpan(span ddtrace.Span, partition int, offset int64, err error) {
	span.SetTag(ext.MessagingKafkaPartition, partition)
	span.SetTag("offset", offset)
	span.Finish(tracer.WithError(err))
}
