// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka // import "github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2"

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2/internal/tracing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	_ "github.com/DataDog/dd-trace-go/v2/instrumentation" // Blank import to pass TestIntegrationEnabled test

	"github.com/segmentio/kafka-go"
)

var (
	clusterIDCache   = make(map[string]string)
	clusterIDCacheMu sync.Mutex
)

// A Reader wraps a kafka.Reader.
type Reader struct {
	*kafka.Reader
	tracer *tracing.Tracer
	prev   *tracer.Span
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
	if c.Config().Brokers != nil {
		bootstrapServersString := strings.Join(c.Config().Brokers, ",")
		cfg.BootstrapServers = bootstrapServersString
		if clusterID := fetchClusterID(bootstrapServersString, c.Config().Brokers); clusterID != "" {
			cfg.ClusterID = clusterID
		}
	}
	if c.Config().GroupID != "" {
		cfg.ConsumerGroupID = c.Config().GroupID
	}
	wrapped.tracer = tracing.NewTracer(cfg, opts...)
	tracing.Logger().Debug("contrib/segmentio/kafka-go/kafka: Wrapping Reader: %#v", wrapped.tracer)
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
type KafkaWriter struct {
	*kafka.Writer
	tracer *tracing.Tracer
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
	if w.Addr.String() != "" {
		bootstrapServersString := w.Addr.String()
		cfg.BootstrapServers = bootstrapServersString
		if clusterID := fetchClusterID(bootstrapServersString, parseAddrs(w.Addr)); clusterID != "" {
			cfg.ClusterID = clusterID
		}
	}
	writer.tracer = tracing.NewTracer(cfg, opts...)
	tracing.Logger().Debug("contrib/segmentio/kafka-go: Wrapping Writer: %#v", writer.tracer)
	return writer
}

// fetchClusterID returns the Kafka cluster ID for the given bootstrap servers,
// using a cache keyed by bootstrapServersString to avoid repeated metadata requests.
func fetchClusterID(bootstrapServersString string, addrs []string) string {
	if len(addrs) == 0 {
		return ""
	}
	clusterIDCacheMu.Lock()
	if id, ok := clusterIDCache[bootstrapServersString]; ok {
		clusterIDCacheMu.Unlock()
		return id
	}
	clusterIDCacheMu.Unlock()

	client := &kafka.Client{
		Addr: kafka.TCP(addrs...),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := client.Metadata(ctx, &kafka.MetadataRequest{})
	if err != nil {
		tracing.Logger().Warn("contrib/segmentio/kafka-go: failed to fetch Kafka cluster ID: %s", err)
		return ""
	}

	clusterIDCacheMu.Lock()
	clusterIDCache[bootstrapServersString] = resp.ClusterID
	clusterIDCacheMu.Unlock()
	return resp.ClusterID
}

// parseAddrs extracts individual host:port strings from a net.Addr (which may be a multi-address).
func parseAddrs(addr net.Addr) []string {
	return strings.Split(addr.String(), ",")
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
