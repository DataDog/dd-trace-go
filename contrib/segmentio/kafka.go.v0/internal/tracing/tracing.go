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

// KafkaConfig holds information from the kafka config for span tags.
type KafkaConfig struct {
	BootstrapServers string
	ConsumerGroupID  string
}

type KafkaHeader struct {
	Key   string
	Value []byte
}

type KafkaWriter struct {
	Topic string
}

type KafkaMessage struct {
	Topic      string
	Partition  int
	Offset     int64
	Headers    []KafkaHeader
	SetHeaders func([]KafkaHeader)
	Value      []byte
	Key        []byte
}

func StartConsumeSpan(ctx context.Context, cfg *Config, kafkaCfg *KafkaConfig, msg *KafkaMessage) ddtrace.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(cfg.consumerServiceName),
		tracer.ResourceName("Consume Topic " + msg.Topic),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.MessagingKafkaPartition, msg.Partition),
		tracer.Tag("offset", msg.Offset),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.KafkaBootstrapServers, kafkaCfg.BootstrapServers),
		tracer.Measured(),
	}
	if !math.IsNaN(cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	// kafka supports headers, so try to extract a span context
	carrier := MessageCarrier{msg}
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(ctx, cfg.consumerSpanName, opts...)
	// reinject the span context so consumers can pick it up
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		log.Debug("contrib/segmentio/kafka.go.v0: Failed to inject span context into carrier in reader, %v", err)
	}
	return span
}

func StartProduceSpan(ctx context.Context, cfg *Config, kafkaCfg *KafkaConfig, writer *KafkaWriter, msg *KafkaMessage) ddtrace.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(cfg.producerServiceName),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.KafkaBootstrapServers, kafkaCfg.BootstrapServers),
	}
	if writer.Topic != "" {
		opts = append(opts, tracer.ResourceName("Produce Topic "+writer.Topic))
	} else {
		opts = append(opts, tracer.ResourceName("Produce Topic "+msg.Topic))
	}
	if !math.IsNaN(cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	carrier := MessageCarrier{msg}
	span, _ := tracer.StartSpanFromContext(ctx, cfg.producerSpanName, opts...)
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		log.Debug("contrib/segmentio/kafka.go.v0: Failed to inject span context into carrier in writer, %v", err)
	}
	return span
}

func FinishProduceSpan(span ddtrace.Span, partition int, offset int64, err error) {
	span.SetTag(ext.MessagingKafkaPartition, partition)
	span.SetTag("offset", offset)
	span.Finish(tracer.WithError(err))
}
