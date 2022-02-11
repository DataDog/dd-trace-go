// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"context"
	"github.com/segmentio/kafka-go"
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// NewReader calls kafka.NewReader and wraps the resulting Consumer.
func NewReader(conf kafka.ReaderConfig, opts ...Option) *Reader {
	return WrapReader(kafka.NewReader(conf), opts...)
}

// NewWriter calls kafka.NewWriter and wraps the resulting Producer.
func NewWriter(conf kafka.WriterConfig, opts ...Option) *Writer {
	return WrapWriter(kafka.NewWriter(conf), opts...)
}

// WrapReader wraps a kafka.Reader so that any consumed events are traced.
func WrapReader(c *kafka.Reader, opts ...Option) *Reader {
	wrapped := &Reader{
		Reader: c,
		cfg:    newConfig(opts...),
	}
	log.Debug("contrib/confluentinc/confluent-kafka.go.v0/kafka: Wrapping Reader: %#v", wrapped.cfg)
	return wrapped
}

// A Reader wraps a kafka.Reader.
type Reader struct {
	*kafka.Reader
	cfg  *config
	prev ddtrace.Span
}

func (r *Reader) startSpan(ctx context.Context, msg *kafka.Message) ddtrace.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(r.cfg.consumerServiceName),
		tracer.ResourceName("Consume Topic " + msg.Topic),
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag("partition", msg.Partition),
		tracer.Tag("offset", msg.Offset),
		tracer.Measured(),
	}
	if !math.IsNaN(r.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, r.cfg.analyticsRate))
	}
	// kafka supports headers, so try to extract a span context
	carrier := messageCarrier{msg}
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, _ := tracer.StartSpanFromContext(ctx, "kafka.consume", opts...)
	// reinject the span context so consumers can pick it up
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		log.Debug("contrib/segmentio/kafka.go.v0: Failed to inject span context into carrier, %v", err)
	}
	return span
}

// Close calls the underlying Reader.Close and if polling is enabled, finishes
// any remaining span.
func (r *Reader) Close() error {
	err := r.Reader.Close()
	if r.prev != nil {
		r.prev.Finish()
		r.prev = nil
	}
	return err
}

// ReadMessage polls the consumer for a message. Message will be traced.
func (r *Reader) ReadMessage(ctx context.Context) (kafka.Message, error) {
	if r.prev != nil {
		r.prev.Finish()
		r.prev = nil
	}
	msg, err := r.Reader.ReadMessage(ctx)
	if err != nil {
		return kafka.Message{}, err
	}
	r.prev = r.startSpan(ctx, &msg)
	return msg, nil
}

// WrapWriter wraps a kafka.Writer so requests are traced.
func WrapWriter(w *kafka.Writer, opts ...Option) *Writer {
	writer := &Writer{
		Writer: w,
		cfg:    newConfig(opts...),
	}
	log.Debug("contrib/segmentio/kafka.go.v0: Wrapping Writer: %#v", writer.cfg)
	return writer
}

// Writer wraps a kafka.Writer with tracing config data
type Writer struct {
	*kafka.Writer
	cfg *config
}

func (w *Writer) startSpan(ctx context.Context, msg *kafka.Message) ddtrace.Span {
	opts := []tracer.StartSpanOption{
		tracer.ServiceName(w.cfg.producerServiceName),
		tracer.ResourceName("Produce Topic " + w.Writer.Topic),
		tracer.SpanType(ext.SpanTypeMessageProducer),
	}
	if !math.IsNaN(w.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, w.cfg.analyticsRate))
	}
	carrier := messageCarrier{msg}
	span, _ := tracer.StartSpanFromContext(ctx, "kafka.produce", opts...)
	err := tracer.Inject(span.Context(), carrier)
	log.Debug("contrib/segmentio/kafka.go.v0: Failed to inject span context into carrier, %v", err)
	return span
}

func finishSpan(span ddtrace.Span, partition int, offset int64, err error) {
	span.SetTag("partition", partition)
	span.SetTag("offset", offset)
	span.Finish(tracer.WithError(err))
}

// WriteMessages calls kafka.go.v0.Writer.WriteMessages and traces the requests.
func (w *Writer) WriteMessages(ctx context.Context, msgs ...kafka.Message) error {
	// although there's only one call made to the SyncProducer, the messages are
	// treated individually, so we create a span for each one
	spans := make([]ddtrace.Span, len(msgs))
	for i := range msgs {
		spans[i] = w.startSpan(ctx, &msgs[i])
	}
	err := w.Writer.WriteMessages(ctx, msgs...)
	for i, span := range spans {
		finishSpan(span, msgs[i].Partition, msgs[i].Offset, err)
	}
	return err
}
