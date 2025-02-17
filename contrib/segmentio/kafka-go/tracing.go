// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"context"
	"math"

	"github.com/segmentio/kafka-go"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const componentName = "segmentio/kafka-go"

func (tr *Tracer) StartConsumeSpan(ctx context.Context, msg Message) *tracer.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(tr.cfg.consumerServiceName),
		tracer.ResourceName("Consume Topic " + msg.GetTopic()),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.MessagingKafkaPartition, msg.GetPartition()),
		tracer.Tag("offset", msg.GetOffset()),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemKafka),
		tracer.Tag(ext.MessagingDestinationName, msg.GetTopic()),
		tracer.Measured(),
	}
	if tr.kafkaCfg.BootstrapServers != "" {
		opts = append(opts, tracer.Tag(ext.KafkaBootstrapServers, tr.kafkaCfg.BootstrapServers))
	}
	if !math.IsNaN(tr.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tr.cfg.analyticsRate))
	}
	// kafka supports headers, so try to extract a span context
	carrier := NewMessageCarrier(msg)
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(ctx, tr.cfg.consumerSpanName, opts...)
	// reinject the span context so consumers can pick it up
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		instr.Logger().Debug("contrib/segmentio/kafka-go: Failed to inject span context into carrier in reader, %v", err)
	}
	return span
}

func (tr *Tracer) StartProduceSpan(ctx context.Context, writer Writer, msg Message, spanOpts ...tracer.StartSpanOption) *tracer.Span {
	topic := writer.GetTopic()
	if topic == "" {
		topic = msg.GetTopic()
	}
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(tr.cfg.producerServiceName),
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
	if !math.IsNaN(tr.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tr.cfg.analyticsRate))
	}
	opts = append(opts, spanOpts...)
	carrier := NewMessageCarrier(msg)
	span, _ := tracer.StartSpanFromContext(ctx, tr.cfg.producerSpanName, opts...)
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		instr.Logger().Debug("contrib/segmentio/kafka-go: Failed to inject span context into carrier in writer, %v", err)
	}
	return span
}

func (*Tracer) FinishProduceSpan(span *tracer.Span, partition int, offset int64, err error) {
	span.SetTag(ext.MessagingKafkaPartition, partition)
	span.SetTag("offset", offset)
	span.Finish(tracer.WithError(err))
}

type wMessage struct {
	*kafka.Message
}

func wrapMessage(msg *kafka.Message) Message {
	if msg == nil {
		return nil
	}
	return &wMessage{msg}
}

func (w *wMessage) GetValue() []byte {
	return w.Value
}

func (w *wMessage) GetKey() []byte {
	return w.Key
}

func (w *wMessage) GetHeaders() []Header {
	hs := make([]Header, 0, len(w.Headers))
	for _, h := range w.Headers {
		hs = append(hs, wrapHeader(h))
	}
	return hs
}

func (w *wMessage) SetHeaders(headers []Header) {
	hs := make([]kafka.Header, 0, len(headers))
	for _, h := range headers {
		hs = append(hs, kafka.Header{
			Key:   h.GetKey(),
			Value: h.GetValue(),
		})
	}
	w.Message.Headers = hs
}

func (w *wMessage) GetTopic() string {
	return w.Topic
}

func (w *wMessage) GetPartition() int {
	return w.Partition
}

func (w *wMessage) GetOffset() int64 {
	return w.Offset
}

type wHeader struct {
	kafka.Header
}

func wrapHeader(h kafka.Header) Header {
	return &wHeader{h}
}

func (w wHeader) GetKey() string {
	return w.Key
}

func (w wHeader) GetValue() []byte {
	return w.Value
}

type wWriter struct {
	*kafka.Writer
}

func (w *wWriter) GetTopic() string {
	return w.Topic
}

func wrapTracingWriter(w *kafka.Writer) Writer {
	return &wWriter{w}
}
