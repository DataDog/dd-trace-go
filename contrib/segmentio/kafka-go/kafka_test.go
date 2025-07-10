// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2/internal/tracing"
	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	kafka "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testGroupID       = "gosegtest"
	testTopic         = "gosegtest"
	testReaderMaxWait = 10 * time.Millisecond
)

var (
	// add some dummy values to broker/addr to test bootstrap servers.
	kafkaBrokers = []string{"localhost:9092", "localhost:9093", "localhost:9094"}
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		log.Println("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
		os.Exit(0)
	}
	cleanup := createTopic()
	exitCode := m.Run()
	cleanup()
	os.Exit(exitCode)
}

func testWriter() *kafka.Writer {
	return &kafka.Writer{
		Addr:         kafka.TCP(kafkaBrokers...),
		Topic:        testTopic,
		RequiredAcks: kafka.RequireOne,
		Balancer:     &kafka.LeastBytes{},
	}
}

func testReader() *kafka.Reader {
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:  kafkaBrokers,
		GroupID:  testGroupID,
		Topic:    testTopic,
		MaxWait:  testReaderMaxWait,
		MaxBytes: 10e6, // 10MB
	})
}

func createTopic() func() {
	conn, err := kafka.Dial("tcp", "localhost:9092")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		log.Fatal(err)
	}
	controllerConn, err := kafka.Dial("tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	if err != nil {
		log.Fatal(err)
	}
	if err := controllerConn.DeleteTopics(testTopic); err != nil && !errors.Is(err, kafka.UnknownTopicOrPartition) {
		log.Fatalf("failed to delete topic: %v", err)
	}
	topicConfigs := []kafka.TopicConfig{
		{
			Topic:             testTopic,
			NumPartitions:     1,
			ReplicationFactor: 1,
		},
	}
	if err := controllerConn.CreateTopics(topicConfigs...); err != nil {
		log.Fatal(err)
	}
	if err := ensureTopicReady(); err != nil {
		log.Fatal(err)
	}
	return func() {
		if err := controllerConn.DeleteTopics(testTopic); err != nil {
			log.Printf("failed to delete topic: %v", err)
		}
		if err := controllerConn.Close(); err != nil {
			log.Printf("failed to close controller connection: %v", err)
		}
	}
}

func ensureTopicReady() error {
	const (
		maxRetries = 10
		retryDelay = 100 * time.Millisecond
	)
	writer := testWriter()
	defer writer.Close()
	reader := testReader()
	defer reader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var (
		retryCount int
		err        error
	)
	for retryCount < maxRetries {
		err = writer.WriteMessages(ctx, kafka.Message{Key: []byte("some-key"), Value: []byte("some-value")})
		if err == nil {
			break
		}
		// This error happens sometimes with brand-new topics, as there is a delay between when the topic is created
		// on the broker, and when the topic can actually be written to.
		if errors.Is(err, kafka.UnknownTopicOrPartition) {
			retryCount++
			log.Printf("topic not ready yet, retrying produce in %s (retryCount: %d)\n", retryDelay, retryCount)
			time.Sleep(retryDelay)
		}
	}
	if err != nil {
		return fmt.Errorf("timeout waiting for topic to be ready: %w", err)
	}
	// read the message to ensure we don't pollute tests
	_, err = reader.ReadMessage(ctx)
	if err != nil {
		return err
	}
	return nil
}

type readerOpFn func(t *testing.T, r *Reader)

func genIntegrationTestSpans(t *testing.T, mt mocktracer.Tracer, writerOp func(t *testing.T, w *KafkaWriter), readerOp readerOpFn, writerOpts []Option, readerOpts []Option) ([]*mocktracer.Span, []kafka.Message) {
	writtenMessages := []kafka.Message{}

	kw := testWriter()
	kw.Completion = func(messages []kafka.Message, _ error) {
		writtenMessages = append(writtenMessages, messages...)
	}
	w := WrapWriter(kw, writerOpts...)
	writerOp(t, w)
	err := w.Close()
	require.NoError(t, err)

	r := WrapReader(testReader(), readerOpts...)
	readerOp(t, r)
	err = r.Close()
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	// they should be linked via headers
	assert.Equal(t, spans[0].TraceID(), spans[1].TraceID(), "Trace IDs should match")
	return spans, writtenMessages
}

func TestReadMessageFunctional(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	var (
		messagesToWrite = []kafka.Message{
			{
				Key:   []byte("key1"),
				Value: []byte("value1"),
			},
		}
		readMessages []kafka.Message
	)

	spans, writtenMessages := genIntegrationTestSpans(
		t,
		mt,
		func(t *testing.T, w *KafkaWriter) {
			err := w.WriteMessages(context.Background(), messagesToWrite...)
			require.NoError(t, err, "Expected to write message to topic")
		},
		func(t *testing.T, r *Reader) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			readMsg, err := r.ReadMessage(ctx)
			require.NoError(t, err, "Expected to consume message")
			assert.Equal(t, messagesToWrite[0].Value, readMsg.Value, "Values should be equal")

			readMessages = append(readMessages, readMsg)
			err = r.CommitMessages(context.Background(), readMsg)
			assert.NoError(t, err, "Expected CommitMessages to not return an error")
		},
		[]Option{WithAnalyticsRate(0.1), WithDataStreams()},
		[]Option{WithDataStreams()},
	)

	require.Len(t, writtenMessages, len(messagesToWrite))
	require.Len(t, readMessages, len(messagesToWrite))

	// producer span
	s0 := spans[0]
	assert.Equal(t, "kafka.produce", s0.OperationName())
	assert.Equal(t, "kafka", s0.Tag(ext.ServiceName))
	assert.Equal(t, "Produce Topic "+testTopic, s0.Tag(ext.ResourceName))
	assert.Equal(t, 0.1, s0.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s0.Tag(ext.SpanType))
	assert.Equal(t, float64(0), s0.Tag(ext.MessagingKafkaPartition))
	assert.Equal(t, "segmentio/kafka.go.v0", s0.Tag(ext.Component))
	assert.Equal(t, "segmentio/kafka.go.v0", s0.Integration())
	assert.Equal(t, ext.SpanKindProducer, s0.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s0.Tag(ext.MessagingSystem))
	assert.Equal(t, "localhost:9092,localhost:9093,localhost:9094", s0.Tag(ext.KafkaBootstrapServers))
	assert.Equal(t, testTopic, s0.Tag("messaging.destination.name"))

	p, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), tracing.NewMessageCarrier(wrapMessage(&writtenMessages[0]))))
	assert.True(t, ok)
	expectedCtx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "topic:"+testTopic, "type:kafka")
	expected, _ := datastreams.PathwayFromContext(expectedCtx)
	assert.NotEqual(t, expected.GetHash(), 0)
	assert.Equal(t, expected.GetHash(), p.GetHash())

	// consumer span
	s1 := spans[1]
	assert.Equal(t, "kafka.consume", s1.OperationName())
	assert.Equal(t, "kafka", s1.Tag(ext.ServiceName))
	assert.Equal(t, "Consume Topic "+testTopic, s1.Tag(ext.ResourceName))
	assert.Equal(t, nil, s1.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s1.Tag(ext.SpanType))
	assert.Equal(t, float64(0), s1.Tag(ext.MessagingKafkaPartition))
	assert.Equal(t, "segmentio/kafka.go.v0", s1.Tag(ext.Component))
	assert.Equal(t, "segmentio/kafka.go.v0", s1.Integration())
	assert.Equal(t, ext.SpanKindConsumer, s1.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s1.Tag(ext.MessagingSystem))
	assert.Equal(t, "localhost:9092,localhost:9093,localhost:9094", s1.Tag(ext.KafkaBootstrapServers))
	assert.Equal(t, testTopic, s1.Tag("messaging.destination.name"))

	// context propagation
	assert.Equal(t, s0.SpanID(), s1.ParentID(), "consume span should be child of the produce span")
	assert.Equal(t, s0.TraceID(), s1.TraceID(), "spans should have the same trace id")

	p, ok = datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), tracing.NewMessageCarrier(wrapMessage(&readMessages[0]))))
	assert.True(t, ok)
	expectedCtx, _ = tracer.SetDataStreamsCheckpoint(
		datastreams.ExtractFromBase64Carrier(context.Background(), tracing.NewMessageCarrier(wrapMessage(&writtenMessages[0]))),
		"direction:in", "topic:"+testTopic, "type:kafka", "group:"+testGroupID,
	)
	expected, _ = datastreams.PathwayFromContext(expectedCtx)
	assert.NotEqual(t, expected.GetHash(), 0)
	assert.Equal(t, expected.GetHash(), p.GetHash())
}

func TestFetchMessageFunctional(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	var (
		messagesToWrite = []kafka.Message{
			{
				Key:   []byte("key1"),
				Value: []byte("value1"),
			},
		}
		readMessages []kafka.Message
	)

	spans, writtenMessages := genIntegrationTestSpans(
		t,
		mt,
		func(t *testing.T, w *KafkaWriter) {
			err := w.WriteMessages(context.Background(), messagesToWrite...)
			require.NoError(t, err, "Expected to write message to topic")
		},
		func(t *testing.T, r *Reader) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			readMsg, err := r.FetchMessage(ctx)
			require.NoError(t, err, "Expected to consume message")
			assert.Equal(t, messagesToWrite[0].Value, readMsg.Value, "Values should be equal")

			readMessages = append(readMessages, readMsg)
			err = r.CommitMessages(context.Background(), readMsg)
			assert.NoError(t, err, "Expected CommitMessages to not return an error")
		},
		[]Option{WithAnalyticsRate(0.1), WithDataStreams()},
		[]Option{WithDataStreams()},
	)

	// producer span
	s0 := spans[0]
	assert.Equal(t, "kafka.produce", s0.OperationName())
	assert.Equal(t, "kafka", s0.Tag(ext.ServiceName))
	assert.Equal(t, "Produce Topic "+testTopic, s0.Tag(ext.ResourceName))
	assert.Equal(t, 0.1, s0.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s0.Tag(ext.SpanType))
	assert.Equal(t, float64(0), s0.Tag(ext.MessagingKafkaPartition))
	assert.Equal(t, "segmentio/kafka.go.v0", s0.Tag(ext.Component))
	assert.Equal(t, "segmentio/kafka.go.v0", s0.Integration())
	assert.Equal(t, ext.SpanKindProducer, s0.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s0.Tag(ext.MessagingSystem))
	assert.Equal(t, "localhost:9092,localhost:9093,localhost:9094", s0.Tag(ext.KafkaBootstrapServers))
	assert.Equal(t, testTopic, s0.Tag("messaging.destination.name"))

	p, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), tracing.NewMessageCarrier(wrapMessage(&writtenMessages[0]))))
	assert.True(t, ok)
	expectedCtx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "topic:"+testTopic, "type:kafka")
	expected, _ := datastreams.PathwayFromContext(expectedCtx)
	assert.NotEqual(t, expected.GetHash(), 0)
	assert.Equal(t, expected.GetHash(), p.GetHash())

	// consumer span
	s1 := spans[1]
	assert.Equal(t, "kafka.consume", s1.OperationName())
	assert.Equal(t, "kafka", s1.Tag(ext.ServiceName))
	assert.Equal(t, "Consume Topic "+testTopic, s1.Tag(ext.ResourceName))
	assert.Equal(t, nil, s1.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s1.Tag(ext.SpanType))
	assert.Equal(t, float64(0), s1.Tag(ext.MessagingKafkaPartition))
	assert.Equal(t, "segmentio/kafka.go.v0", s1.Tag(ext.Component))
	assert.Equal(t, "segmentio/kafka.go.v0", s1.Integration())
	assert.Equal(t, ext.SpanKindConsumer, s1.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s1.Tag(ext.MessagingSystem))
	assert.Equal(t, "localhost:9092,localhost:9093,localhost:9094", s1.Tag(ext.KafkaBootstrapServers))
	assert.Equal(t, testTopic, s1.Tag("messaging.destination.name"))

	// context propagation
	assert.Equal(t, s0.SpanID(), s1.ParentID(), "consume span should be child of the produce span")

	p, ok = datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), tracing.NewMessageCarrier(wrapMessage(&readMessages[0]))))
	assert.True(t, ok)
	expectedCtx, _ = tracer.SetDataStreamsCheckpoint(
		datastreams.ExtractFromBase64Carrier(context.Background(), tracing.NewMessageCarrier(wrapMessage(&writtenMessages[0]))),
		"direction:in", "topic:"+testTopic, "type:kafka", "group:"+testGroupID,
	)
	expected, _ = datastreams.PathwayFromContext(expectedCtx)
	assert.NotEqual(t, expected.GetHash(), 0)
	assert.Equal(t, expected.GetHash(), p.GetHash())
}

func TestProduceMultipleMessages(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	messages := []kafka.Message{
		{
			Key:   []byte("key1"),
			Value: []byte("value1"),
		},
		{
			Key:   []byte("key2"),
			Value: []byte("value2"),
		},
		{
			Key:   []byte("key3"),
			Value: []byte("value3"),
		},
	}

	writer := WrapWriter(testWriter())
	reader := WrapReader(testReader())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := writer.WriteMessages(ctx, messages...)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	curMsg := 0
	for curMsg < len(messages) {
		readMsg, err := reader.ReadMessage(ctx)
		require.NoError(t, err)
		require.Equal(t, string(messages[curMsg].Key), string(readMsg.Key))
		require.Equal(t, string(messages[curMsg].Value), string(readMsg.Value))
		curMsg++
	}
	require.NoError(t, reader.Close())

	spans := mt.FinishedSpans()
	require.Len(t, spans, 6)

	produceSpans := spans[0:3]
	consumeSpans := spans[3:6]
	for i := 0; i < 3; i++ {
		ps := produceSpans[i]
		cs := consumeSpans[i]

		assert.Equal(t, "kafka.produce", ps.OperationName(), "wrong produce span name")
		assert.Equal(t, "kafka.consume", cs.OperationName(), "wrong consume span name")
		assert.Equal(t, cs.ParentID(), ps.SpanID(), "consume span should be child of a produce span")
		assert.Equal(t, uint64(0), ps.ParentID(), "produce span should not be child of any span")
		assert.Equal(t, cs.TraceID(), ps.TraceID(), "spans should be part of the same trace")
	}
}

// benchSpan is a package-level variable used to prevent compiler optimisations in the benchmarks below.
var benchSpan *tracer.Span

func BenchmarkReaderStartSpan(b *testing.B) {
	ctx := context.Background()
	kafkaCfg := tracing.KafkaConfig{
		BootstrapServers: "localhost:9092,localhost:9093,localhost:9094",
		ConsumerGroupID:  testGroupID,
	}
	tr := tracing.NewTracer(kafkaCfg)
	msg := kafka.Message{
		Key:   []byte("key1"),
		Value: []byte("value1"),
	}
	var result *tracer.Span

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		result = tr.StartConsumeSpan(ctx, wrapMessage(&msg))
	}
	benchSpan = result
}

func BenchmarkWriterStartSpan(b *testing.B) {
	ctx := context.Background()
	kafkaCfg := tracing.KafkaConfig{
		BootstrapServers: "localhost:9092,localhost:9093,localhost:9094",
		ConsumerGroupID:  testGroupID,
	}
	tr := tracing.NewTracer(kafkaCfg)
	kw := &kafka.Writer{
		Addr:         kafka.TCP("localhost:9092", "localhost:9093", "localhost:9094"),
		Topic:        testTopic,
		RequiredAcks: kafka.RequireOne,
	}
	msg := kafka.Message{
		Key:   []byte("key1"),
		Value: []byte("value1"),
	}
	var result *tracer.Span

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		result = tr.StartProduceSpan(ctx, wrapTracingWriter(kw), wrapMessage(&msg))
	}
	benchSpan = result
}
