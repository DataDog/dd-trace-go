// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/segmentio/kafka.go.v0"

import (
	"context"
	"strings"

	"github.com/segmentio/kafka-go"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/segmentio/kafka.go.v0/internal/tracing"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// A Reader wraps a kafka.Reader.
type Reader struct {
	*kafka.Reader
	tracer   *tracing.Tracer
	kafkaCfg *tracing.KafkaConfig
	prev     ddtrace.Span
}

// NewReader calls kafka.NewReader and wraps the resulting Consumer.
func NewReader(conf kafka.ReaderConfig, opts ...Option) *Reader {
	return WrapReader(kafka.NewReader(conf), opts...)
}

// WrapReader wraps a kafka.Reader so that any consumed events are traced.
func WrapReader(c *kafka.Reader, opts ...Option) *Reader {
	wrapped := &Reader{
		Reader: c,
	}
	kafkaCfg := tracing.KafkaConfig{}
	if c.Config().Brokers != nil {
		kafkaCfg.BootstrapServers = strings.Join(c.Config().Brokers, ",")
	}
	if c.Config().GroupID != "" {
		kafkaCfg.ConsumerGroupID = c.Config().GroupID
	}
	wrapped.tracer = tracing.NewTracer(kafkaCfg, opts...)
	log.Debug("contrib/segmentio/kafka-go.v0/kafka: Wrapping Reader: %#v", wrapped.tracer)
	return wrapped
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
	tMsg := wrapMessage(&msg)
	r.prev = r.tracer.StartConsumeSpan(ctx, tMsg)
	r.tracer.SetConsumeDSMCheckpoint(tMsg)
	return msg, nil
}

// FetchMessage reads and returns the next message from the reader. Message will be traced.
func (r *Reader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	if r.prev != nil {
		r.prev.Finish()
		r.prev = nil
	}
	msg, err := r.Reader.FetchMessage(ctx)
	if err != nil {
		return msg, err
	}
	tMsg := wrapMessage(&msg)
	r.prev = r.tracer.StartConsumeSpan(ctx, tMsg)
	r.tracer.SetConsumeDSMCheckpoint(tMsg)
	return msg, nil
}

// Writer wraps a kafka.Writer with tracing config data
type Writer struct {
	*kafka.Writer
	tracer *tracing.Tracer
}

// NewWriter calls kafka.NewWriter and wraps the resulting Producer.
func NewWriter(conf kafka.WriterConfig, opts ...Option) *Writer {
	return WrapWriter(kafka.NewWriter(conf), opts...)
}

// WrapWriter wraps a kafka.Writer so requests are traced.
func WrapWriter(w *kafka.Writer, opts ...Option) *Writer {
	writer := &Writer{
		Writer: w,
	}
	kafkaCfg := tracing.KafkaConfig{}
	if w.Addr.String() != "" {
		kafkaCfg.BootstrapServers = w.Addr.String()
	}
	writer.tracer = tracing.NewTracer(kafkaCfg, opts...)
	log.Debug("contrib/segmentio/kafka.go.v0: Wrapping Writer: %#v", writer.tracer)
	return writer
}

// WriteMessages calls kafka.go.v0.Writer.WriteMessages and traces the requests.
func (w *Writer) WriteMessages(ctx context.Context, msgs ...kafka.Message) error {
	// although there's only one call made to the SyncProducer, the messages are
	// treated individually, so we create a span for each one
	spans := make([]ddtrace.Span, len(msgs))
	for i := range msgs {
		tMsg := wrapMessage(&msgs[i])
		tWriter := wrapTracingWriter(w.Writer)
		spans[i] = w.tracer.StartProduceSpan(ctx, tWriter, tMsg)
		w.tracer.SetProduceDSMCheckpoint(tMsg, tWriter)
	}
	err := w.Writer.WriteMessages(ctx, msgs...)
	for i, span := range spans {
		w.tracer.FinishProduceSpan(span, msgs[i].Partition, msgs[i].Offset, err)
	}
	return err
}
