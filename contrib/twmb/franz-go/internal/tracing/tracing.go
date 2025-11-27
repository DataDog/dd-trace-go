// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package tracing contains tracing logic for the twmb/franz-go instrumentation.
//
// WARNING: this package SHOULD NOT import twmb/franz-go.
//
// The motivation of this package is to support orchestrion, which cannot use the main package because it imports
// the twmb/franz-go package, and since orchestrion modifies the library code itself,
// this would cause an import cycle.
package tracing

import (
	"context"
	"math"

	// NOTE: Think of it as external constants.
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const componentName = "twmb/franz-go"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageTwmbFranzGo)
}

type Tracer struct {
	consumerServiceName string
	producerServiceName string
	consumerSpanName    string
	producerSpanName    string
	analyticsRate       float64
	dataStreamsEnabled  bool
	kafkaCfg            KafkaConfig
}

func (tr *Tracer) StartConsumeSpan(ctx context.Context, r Record) *tracer.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(tr.consumerServiceName),
		tracer.ResourceName("Consume Topic " + r.GetTopic()),
		// ???: What is ext?
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.MessagingKafkaPartition, r.GetPartition()),
		tracer.Tag("offset", r.GetOffset()),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.MessagingDestinationName, r.GetTopic()),
		tracer.Measured(),
	}
	if tr.kafkaCfg.BootstrapServers != "" {
		opts = append(opts, tracer.Tag(ext.KafkaBootstrapServers, tr.kafkaCfg.BootstrapServers))
	}
	if !math.IsNaN(tr.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tr.analyticsRate))
	}

	// Kafka supports headers, so we try to extract a span context from them
	carrier := NewKafkaHeadersCarrier(r)
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(ctx, tr.consumerSpanName, opts...)

	// We reinject the span context so consumers can pick it up
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		instr.Logger().Debug("contrib/twmb/franz-go: Failed to inject span context into carrier in reader, %s", err.Error())
	}
	return span
}

func (tr *Tracer) StartProduceSpan(ctx context.Context, writer Writer, r Record, spanOpts ...tracer.StartSpanOption) *tracer.Span {
	topic := writer.GetTopic()
	if topic == "" {
		topic = r.GetTopic()
	}
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(tr.producerServiceName),
		tracer.ResourceName("Produce Topic " + topic),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.MessagingDestinationName, topic),
	}
	if tr.kafkaCfg.BootstrapServers != "" {
		opts = append(opts, tracer.Tag(ext.KafkaBootstrapServers, tr.kafkaCfg.BootstrapServers))
	}
	if !math.IsNaN(tr.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tr.analyticsRate))
	}
	opts = append(opts, spanOpts...)
	carrier := NewKafkaHeadersCarrier(r)
	span, _ := tracer.StartSpanFromContext(ctx, tr.producerSpanName, opts...)
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		instr.Logger().Debug("contrib/twmb/franz-go: Failed to inject span context into carrier in writer, %s", err.Error())
	}
	return span
}

func (*Tracer) FinishProduceSpan(span *tracer.Span, partition int, offset int64, err error) {
	span.SetTag(ext.MessagingKafkaPartition, partition)
	span.SetTag("offset", offset)
	span.Finish(tracer.WithError(err))
}
