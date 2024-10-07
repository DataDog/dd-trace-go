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
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// ExtractSpanContext retrieves the SpanContext from a kafka.Message
func ExtractSpanContext(msg kafka.Message) (ddtrace.SpanContext, error) {
	return tracer.Extract(tracing.MessageCarrier{Message: tracingMessage(&msg)})
}

// A Reader wraps a kafka.Reader.
type Reader struct {
	*kafka.Reader
	cfg      *tracing.Config
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
		Reader:   c,
		cfg:      tracing.NewConfig(opts...),
		kafkaCfg: &tracing.KafkaConfig{},
	}
	if c.Config().Brokers != nil {
		wrapped.kafkaCfg.BootstrapServers = strings.Join(c.Config().Brokers, ",")
	}
	if c.Config().GroupID != "" {
		wrapped.kafkaCfg.ConsumerGroupID = c.Config().GroupID
	}
	log.Debug("contrib/segmentio/kafka-go.v0/kafka: Wrapping Reader: %#v", wrapped.cfg)
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
	tMsg := tracingMessage(&msg)
	r.prev = tracing.StartConsumeSpan(ctx, r.cfg, r.kafkaCfg, tMsg)
	tracing.SetConsumeDSMCheckpoint(r.cfg, r.kafkaCfg, tMsg)
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
	tMsg := tracingMessage(&msg)
	r.prev = tracing.StartConsumeSpan(ctx, r.cfg, r.kafkaCfg, tMsg)
	tracing.SetConsumeDSMCheckpoint(r.cfg, r.kafkaCfg, tMsg)
	return msg, nil
}

// Writer wraps a kafka.Writer with tracing config data
type Writer struct {
	*kafka.Writer
	cfg      *tracing.Config
	kafkaCfg *tracing.KafkaConfig
}

// NewWriter calls kafka.NewWriter and wraps the resulting Producer.
func NewWriter(conf kafka.WriterConfig, opts ...Option) *Writer {
	return WrapWriter(kafka.NewWriter(conf), opts...)
}

// WrapWriter wraps a kafka.Writer so requests are traced.
func WrapWriter(w *kafka.Writer, opts ...Option) *Writer {
	writer := &Writer{
		Writer:   w,
		cfg:      tracing.NewConfig(opts...),
		kafkaCfg: &tracing.KafkaConfig{},
	}
	if w.Addr.String() != "" {
		writer.kafkaCfg.BootstrapServers = w.Addr.String()
	}
	log.Debug("contrib/segmentio/kafka.go.v0: Wrapping Writer: %#v", writer.cfg)
	return writer
}

// WriteMessages calls kafka.go.v0.Writer.WriteMessages and traces the requests.
func (w *Writer) WriteMessages(ctx context.Context, msgs ...kafka.Message) error {
	// although there's only one call made to the SyncProducer, the messages are
	// treated individually, so we create a span for each one
	spans := make([]ddtrace.Span, len(msgs))
	for i := range msgs {
		tMsg := tracingMessage(&msgs[i])
		tWriter := tracingWriter(w.Writer)
		spans[i] = tracing.StartProduceSpan(ctx, w.cfg, w.kafkaCfg, tWriter, tMsg)
		tracing.SetProduceDSMCheckpoint(w.cfg, tMsg, tWriter)
	}
	err := w.Writer.WriteMessages(ctx, msgs...)
	for i, span := range spans {
		tracing.FinishProduceSpan(span, msgs[i].Partition, msgs[i].Offset, err)
	}
	return err
}

func tracingMessage(msg *kafka.Message) *tracing.KafkaMessage {
	setHeaders := func(newHeaders []tracing.KafkaHeader) {
		hs := make([]kafka.Header, 0, len(newHeaders))
		for _, h := range newHeaders {
			hs = append(hs, kafka.Header{
				Key:   h.Key,
				Value: h.Value,
			})
		}
		msg.Headers = hs
	}
	return &tracing.KafkaMessage{
		Topic:      msg.Topic,
		Partition:  msg.Partition,
		Offset:     msg.Offset,
		Headers:    tracingKafkaHeaders(msg.Headers),
		SetHeaders: setHeaders,
		Value:      msg.Value,
		Key:        msg.Key,
	}
}

func tracingKafkaHeaders(headers []kafka.Header) []tracing.KafkaHeader {
	hs := make([]tracing.KafkaHeader, 0, len(headers))
	for _, h := range headers {
		hs = append(hs, tracing.KafkaHeader{
			Key:   h.Key,
			Value: h.Value,
		})
	}
	return hs
}

func tracingWriter(w *kafka.Writer) *tracing.KafkaWriter {
	return &tracing.KafkaWriter{
		Topic: w.Topic,
	}
}
