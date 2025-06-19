// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"testing"
	"time"

	segmentiotracer "github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testGroupID       = "segmentio-kafka-namingschematest"
	testTopic         = "segmentio-kafka-namingschematest"
	testReaderMaxWait = 10 * time.Millisecond
)

var (
	kafkaBrokers = []string{"localhost:9092", "localhost:9093", "localhost:9094"}
)

type readerOpFn func(t *testing.T, r *segmentiotracer.Reader)

func genIntegrationTestSpans(t *testing.T, mt mocktracer.Tracer, writerOp func(t *testing.T, w *segmentiotracer.KafkaWriter), readerOp readerOpFn, writerOpts []segmentiotracer.Option, readerOpts []segmentiotracer.Option) ([]*mocktracer.Span, []kafka.Message) {
	_ = createTopic(t)
	writtenMessages := []kafka.Message{}

	// add some dummy values to broker/addr to test bootstrap servers.
	kw := &kafka.Writer{
		Addr:         kafka.TCP(kafkaBrokers...),
		Topic:        testTopic,
		RequiredAcks: kafka.RequireOne,
		Completion: func(messages []kafka.Message, err error) {
			writtenMessages = append(writtenMessages, messages...)
		},
	}
	w := segmentiotracer.WrapWriter(kw, writerOpts...)
	writerOp(t, w)
	err := w.Close()
	require.NoError(t, err)

	r := segmentiotracer.NewReader(kafka.ReaderConfig{
		Brokers: kafkaBrokers,
		GroupID: testGroupID,
		Topic:   testTopic,
		MaxWait: testReaderMaxWait,
	}, readerOpts...)
	readerOp(t, r)
	err = r.Close()
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	// they should be linked via headers
	assert.Equal(t, spans[0].TraceID(), spans[1].TraceID(), "Trace IDs should match")
	return spans, writtenMessages
}

func segmentioKafkaGoGenSpans() harness.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []segmentiotracer.Option
		if serviceOverride != "" {
			opts = append(opts, segmentiotracer.WithService(serviceOverride))
		}

		mt := mocktracer.Start()
		defer mt.Stop()

		messagesToWrite := []kafka.Message{
			{
				Key:   []byte("key1"),
				Value: []byte("value1"),
			},
		}

		spans, _ := genIntegrationTestSpans(
			t,
			mt,
			func(t *testing.T, w *segmentiotracer.KafkaWriter) {
				err := w.WriteMessages(context.Background(), messagesToWrite...)
				require.NoError(t, err, "Expected to write message to topic")
			},
			func(t *testing.T, r *segmentiotracer.Reader) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				readMsg, err := r.FetchMessage(ctx)
				require.NoError(t, err, "Expected to consume message")
				assert.Equal(t, messagesToWrite[0].Value, readMsg.Value, "Values should be equal")

				err = r.CommitMessages(context.Background(), readMsg)
				assert.NoError(t, err, "Expected CommitMessages to not return an error")
			},
			opts,
			opts,
		)
		return spans
	}
}

var segmentioKafkaGo = harness.TestCase{
	Name:     instrumentation.PackageSegmentioKafkaGo,
	GenSpans: segmentioKafkaGoGenSpans(),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        harness.RepeatString("kafka", 2),
		DDService:       []string{"kafka", harness.TestDDService},
		ServiceOverride: harness.RepeatString(harness.TestServiceOverride, 2),
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "kafka.produce", spans[0].OperationName())
		assert.Equal(t, "kafka.consume", spans[1].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "kafka.send", spans[0].OperationName())
		assert.Equal(t, "kafka.process", spans[1].OperationName())
	},
}

func createTopic(t *testing.T) func() {
	conn, err := kafka.Dial("tcp", "localhost:9092")
	require.NoError(t, err)

	defer conn.Close()

	controller, err := conn.Controller()
	require.NoError(t, err)

	controllerConn, err := kafka.Dial("tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	require.NoError(t, err)

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
	err = controllerConn.CreateTopics(topicConfigs...)
	require.NoError(t, err)

	err = ensureTopicReady()
	require.NoError(t, err)

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
		MaxWait:  10 * time.Millisecond,
		MaxBytes: 10e6, // 10MB
	})
}
