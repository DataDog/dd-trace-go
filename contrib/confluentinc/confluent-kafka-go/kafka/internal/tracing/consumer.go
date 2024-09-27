// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracing

import (
	"context"
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/datastreams/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/confluentinc/confluent-kafka-go/kafka"
)

type ConsumerTracer struct {
	Ctx                context.Context
	DataStreamsEnabled bool
	GroupID            string
	Prev               ddtrace.Span
	Events             chan kafka.Event
	StartSpanConfig    StartSpanConfig
	WatermarkOffsets   watermarkOffsetsFn
}

type watermarkOffsetsFn func(topic string, partition int32) (int64, int64, error)

func NewConsumerTracer(ctx context.Context, c *kafka.Consumer, dataStreamsEnabled bool, groupID string, startSpanConfig StartSpanConfig) *ConsumerTracer {
	tracer := &ConsumerTracer{
		Ctx:                ctx,
		DataStreamsEnabled: dataStreamsEnabled,
		GroupID:            groupID,
		StartSpanConfig:    startSpanConfig,
		WatermarkOffsets:   c.GetWatermarkOffsets,
	}
	tracer.traceEventsChannel(c.Events())
	return tracer
}

func (ct *ConsumerTracer) traceEventsChannel(in chan kafka.Event) {
	// in will be nil when consuming via the events channel is not enabled
	if in == nil {
		ct.Events = in
		return
	}
	out := make(chan kafka.Event, 1)
	go func() {
		defer close(out)
		for evt := range in {
			var next ddtrace.Span

			// only trace messages
			if msg, ok := evt.(*kafka.Message); ok {
				next = startConsumerSpan(ct.Ctx, msg, ct.StartSpanConfig)
				setConsumeCheckpoint(ct.DataStreamsEnabled, ct.GroupID, msg)
			} else if offset, ok := evt.(kafka.OffsetsCommitted); ok {
				commitOffsets(ct.DataStreamsEnabled, ct.GroupID, offset.Offsets, offset.Error)
				trackHighWatermark(ct.WatermarkOffsets, ct.DataStreamsEnabled, offset.Offsets)
			}

			out <- evt

			if ct.Prev != nil {
				ct.Prev.Finish()
			}
			ct.Prev = next
		}
		// finish any remaining span
		if ct.Prev != nil {
			ct.Prev.Finish()
			ct.Prev = nil
		}
	}()
	ct.Events = out
}

func (ct *ConsumerTracer) WrapPoll(p func() kafka.Event) kafka.Event {
	if ct.Prev != nil {
		ct.Prev.Finish()
		ct.Prev = nil
	}
	evt := p()
	if msg, ok := evt.(*kafka.Message); ok {
		setConsumeCheckpoint(ct.DataStreamsEnabled, ct.GroupID, msg)
		ct.Prev = ct.startSpan(msg)
	} else if offset, ok := evt.(kafka.OffsetsCommitted); ok {
		commitOffsets(ct.DataStreamsEnabled, ct.GroupID, offset.Offsets, offset.Error)
		trackHighWatermark(ct.WatermarkOffsets, ct.DataStreamsEnabled, offset.Offsets)
	}
	return evt
}

func (ct *ConsumerTracer) WrapReadMessage(rm func() (*kafka.Message, error)) (*kafka.Message, error) {
	if ct.Prev != nil {
		ct.Prev.Finish()
		ct.Prev = nil
	}
	msg, err := rm()
	if err != nil {
		return nil, err
	}
	setConsumeCheckpoint(ct.DataStreamsEnabled, ct.GroupID, msg)
	ct.Prev = ct.startSpan(msg)
	return msg, err
}

func (ct *ConsumerTracer) startSpan(msg *kafka.Message) ddtrace.Span {
	return startConsumerSpan(ct.Ctx, msg, ct.StartSpanConfig)
}

func (ct *ConsumerTracer) WrapCommit(c func() ([]kafka.TopicPartition, error)) ([]kafka.TopicPartition, error) {
	tps, err := c()
	commitOffsets(ct.DataStreamsEnabled, ct.GroupID, tps, err)
	trackHighWatermark(ct.WatermarkOffsets, ct.DataStreamsEnabled, tps)
	return tps, err
}

func (ct *ConsumerTracer) Close() {
	// we only close the previous span if consuming via the events channel is
	// not enabled, because otherwise there would be a data race from the
	// consuming goroutine.
	if ct.Events == nil && ct.Prev != nil {
		ct.Prev.Finish()
		ct.Prev = nil
	}
}

func startConsumerSpan(ctx context.Context, msg *kafka.Message, cfg StartSpanConfig) ddtrace.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(cfg.Service),
		tracer.ResourceName("Consume Topic " + *msg.TopicPartition.Topic),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.MessagingKafkaPartition, msg.TopicPartition.Partition),
		tracer.Tag("offset", msg.TopicPartition.Offset),
		tracer.Tag(ext.Component, ComponentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Measured(),
	}
	if cfg.BootstrapServers != "" {
		opts = append(opts, tracer.Tag(ext.KafkaBootstrapServers, cfg.BootstrapServers))
	}
	if cfg.TagFns != nil {
		for key, tagFn := range cfg.TagFns {
			opts = append(opts, tracer.Tag(key, tagFn(msg)))
		}
	}
	if !math.IsNaN(cfg.AnalyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.AnalyticsRate))
	}
	// kafka supports headers, so try to extract a span context
	carrier := NewMessageCarrier(msg)
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(ctx, cfg.Operation, opts...)
	// reinject the span context so consumers can pick it up
	tracer.Inject(span.Context(), carrier)
	return span
}

func setConsumeCheckpoint(dataStreamsEnabled bool, groupID string, msg *kafka.Message) {
	if !dataStreamsEnabled || msg == nil {
		return
	}
	edges := []string{"direction:in", "topic:" + *msg.TopicPartition.Topic, "type:kafka"}
	if groupID != "" {
		edges = append(edges, "group:"+groupID)
	}
	carrier := NewMessageCarrier(msg)
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(datastreams.ExtractFromBase64Carrier(context.Background(), carrier), options.CheckpointParams{PayloadSize: getMsgSize(msg)}, edges...)
	if !ok {
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
}

func getMsgSize(msg *kafka.Message) (size int64) {
	for _, header := range msg.Headers {
		size += int64(len(header.Key) + len(header.Value))
	}
	return size + int64(len(msg.Value)+len(msg.Key))
}

func commitOffsets(dataStreamsEnabled bool, groupID string, tps []kafka.TopicPartition, err error) {
	if err != nil || groupID == "" || !dataStreamsEnabled {
		return
	}
	for _, tp := range tps {
		tracer.TrackKafkaCommitOffset(groupID, *tp.Topic, tp.Partition, int64(tp.Offset))
	}
}

func trackHighWatermark(watermarkOffsets watermarkOffsetsFn, dataStreamsEnabled bool, offsets []kafka.TopicPartition) {
	if !dataStreamsEnabled {
		return
	}
	for _, tp := range offsets {
		if _, high, err := watermarkOffsets(*tp.Topic, tp.Partition); err == nil {
			tracer.TrackKafkaHighWatermarkOffset("", *tp.Topic, tp.Partition, high)
		}
	}
}
