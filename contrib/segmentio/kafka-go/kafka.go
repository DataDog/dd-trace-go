// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka // import "github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2"

import (
	"context"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2/internal/tracing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	_ "github.com/DataDog/dd-trace-go/v2/instrumentation" // Blank import to pass TestIntegrationEnabled test

	"github.com/segmentio/kafka-go"
)

// A Reader wraps a kafka.Reader.
type Reader struct {
	*kafka.Reader
	tracer     *tracing.Tracer
	prev       *tracer.Span
	closeAsync []func() // async jobs to cancel and wait for on Close
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
	cfg := tracing.KafkaConfig{}
	brokers := c.Config().Brokers
	if brokers != nil {
		cfg.BootstrapServers = strings.Join(brokers, ",")
	}
	if c.Config().GroupID != "" {
		cfg.ConsumerGroupID = c.Config().GroupID
	}
	wrapped.tracer = tracing.NewTracer(cfg, opts...)
	tracing.Logger().Debug("contrib/segmentio/kafka-go/kafka: Wrapping Reader: %#v", wrapped.tracer)
	if brokers == nil || !wrapped.tracer.DSMEnabled() {
		return wrapped
	}
	wrapped.closeAsync = append(wrapped.closeAsync, startFetchClusterID(wrapped.tracer, cfg.BootstrapServers))
	return wrapped
}

// Close calls the underlying Reader.Close and if polling is enabled, finishes
// any remaining span.
func (r *Reader) Close() error {
	for _, stop := range r.closeAsync {
		stop()
	}
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
type KafkaWriter struct {
	*kafka.Writer
	tracer     *tracing.Tracer
	closeAsync []func() // async jobs to cancel and wait for on Close
}

// NewWriter calls kafka.NewWriter and wraps the resulting Producer.
func NewWriter(conf kafka.WriterConfig, opts ...Option) *KafkaWriter {
	return WrapWriter(kafka.NewWriter(conf), opts...)
}

// WrapWriter wraps a kafka.Writer so requests are traced.
func WrapWriter(w *kafka.Writer, opts ...Option) *KafkaWriter {
	writer := &KafkaWriter{
		Writer: w,
	}
	cfg := tracing.KafkaConfig{}
	addr := w.Addr.String()
	if addr != "" {
		cfg.BootstrapServers = addr
	}
	writer.tracer = tracing.NewTracer(cfg, opts...)
	tracing.Logger().Debug("contrib/segmentio/kafka-go: Wrapping Writer: %#v", writer.tracer)
	if addr == "" || !writer.tracer.DSMEnabled() {
		return writer
	}
	writer.closeAsync = append(writer.closeAsync, startFetchClusterID(writer.tracer, cfg.BootstrapServers))
	return writer
}

func startFetchClusterID(tr *tracing.Tracer, bootstrapServers string) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		client := &kafka.Client{Addr: kafka.TCP(strings.Split(bootstrapServers, ",")...)}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		resp, err := client.Metadata(ctx, &kafka.MetadataRequest{})
		if err != nil {
			if ctx.Err() == context.Canceled {
				return
			}
			tracing.Logger().Warn("contrib/segmentio/kafka-go: failed to fetch Kafka cluster ID: %s", err)
			return
		}
		tr.SetClusterID(resp.ClusterID)
	}()
	return func() {
		cancel()
		<-done
	}
}

// Close calls the underlying Writer.Close.
func (w *KafkaWriter) Close() error {
	for _, stop := range w.closeAsync {
		stop()
	}
	return w.Writer.Close()
}

// WriteMessages calls kafka-go.Writer.WriteMessages and traces the requests.
func (w *KafkaWriter) WriteMessages(ctx context.Context, msgs ...kafka.Message) error {
	// although there's only one call made to the SyncProducer, the messages are
	// treated individually, so we create a span for each one
	spans := make([]*tracer.Span, len(msgs))
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
