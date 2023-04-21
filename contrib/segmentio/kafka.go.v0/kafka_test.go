// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"context"
	"os"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

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
	testMessages = []kafka.Message{
		{
			Key:   []byte("key1"),
			Value: []byte("value1"),
		},
	}
)

func skipIntegrationTest(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
	}
}

/*
to setup the integration test locally run:
	docker-compose -f local_testing.yaml up
*/

type readerOpFn func(t *testing.T, r *Reader)

func genIntegrationTestSpans(t *testing.T, writerOp func(t *testing.T, w *Writer), readerOp readerOpFn, writerOpts []Option, readerOpts []Option) []mocktracer.Span {
	skipIntegrationTest(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	kw := &kafka.Writer{
		Addr:         kafka.TCP("localhost:9092"),
		Topic:        testTopic,
		RequiredAcks: kafka.RequireOne,
	}
	w := WrapWriter(kw, writerOpts...)
	writerOp(t, w)
	err := w.Close()
	require.NoError(t, err)

	r := NewReader(kafka.ReaderConfig{
		Brokers: []string{"localhost:9092"},
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
	return spans
}

func TestReadMessageFunctional(t *testing.T) {
	spans := genIntegrationTestSpans(
		t,
		func(t *testing.T, w *Writer) {
			err := w.WriteMessages(context.Background(), testMessages...)
			require.NoError(t, err, "Expected to write message to topic")
		},
		func(t *testing.T, r *Reader) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			readMsg, err := r.ReadMessage(ctx)
			require.NoError(t, err, "Expected to consume message")
			assert.Equal(t, testMessages[0].Value, readMsg.Value, "Values should be equal")

			err = r.CommitMessages(context.Background(), readMsg)
			assert.NoError(t, err, "Expected CommitMessages to not return an error")
		},
		[]Option{WithAnalyticsRate(0.1)},
		[]Option{},
	)

	// producer span
	s0 := spans[0]
	assert.Equal(t, "kafka.produce", s0.OperationName())
	assert.Equal(t, "kafka", s0.Tag(ext.ServiceName))
	assert.Equal(t, "Produce Topic "+testTopic, s0.Tag(ext.ResourceName))
	assert.Equal(t, 0.1, s0.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s0.Tag(ext.SpanType))
	assert.Equal(t, 0, s0.Tag(ext.MessagingKafkaPartition))
	assert.Equal(t, "segmentio/kafka.go.v0", s0.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindProducer, s0.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s0.Tag(ext.MessagingSystem))

	// consumer span
	s1 := spans[1]
	assert.Equal(t, "kafka.consume", s1.OperationName())
	assert.Equal(t, "kafka", s1.Tag(ext.ServiceName))
	assert.Equal(t, "Consume Topic "+testTopic, s1.Tag(ext.ResourceName))
	assert.Equal(t, nil, s1.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s1.Tag(ext.SpanType))
	assert.Equal(t, 0, s1.Tag(ext.MessagingKafkaPartition))
	assert.Equal(t, "segmentio/kafka.go.v0", s1.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindConsumer, s1.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s1.Tag(ext.MessagingSystem))
}

func TestFetchMessageFunctional(t *testing.T) {
	spans := genIntegrationTestSpans(
		t,
		func(t *testing.T, w *Writer) {
			err := w.WriteMessages(context.Background(), testMessages...)
			require.NoError(t, err, "Expected to write message to topic")
		},
		func(t *testing.T, r *Reader) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			readMsg, err := r.FetchMessage(ctx)
			require.NoError(t, err, "Expected to consume message")
			assert.Equal(t, testMessages[0].Value, readMsg.Value, "Values should be equal")

			err = r.CommitMessages(context.Background(), readMsg)
			assert.NoError(t, err, "Expected CommitMessages to not return an error")
		},
		[]Option{WithAnalyticsRate(0.1)},
		[]Option{},
	)

	// producer span
	s0 := spans[0]
	assert.Equal(t, "kafka.produce", s0.OperationName())
	assert.Equal(t, "kafka", s0.Tag(ext.ServiceName))
	assert.Equal(t, "Produce Topic "+testTopic, s0.Tag(ext.ResourceName))
	assert.Equal(t, 0.1, s0.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s0.Tag(ext.SpanType))
	assert.Equal(t, 0, s0.Tag(ext.MessagingKafkaPartition))
	assert.Equal(t, "segmentio/kafka.go.v0", s0.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindProducer, s0.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s0.Tag(ext.MessagingSystem))

	// consumer span
	s1 := spans[1]
	assert.Equal(t, "kafka.consume", s1.OperationName())
	assert.Equal(t, "kafka", s1.Tag(ext.ServiceName))
	assert.Equal(t, "Consume Topic "+testTopic, s1.Tag(ext.ResourceName))
	assert.Equal(t, nil, s1.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s1.Tag(ext.SpanType))
	assert.Equal(t, 0, s1.Tag(ext.MessagingKafkaPartition))
	assert.Equal(t, "segmentio/kafka.go.v0", s1.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindConsumer, s1.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s1.Tag(ext.MessagingSystem))
}

func TestNamingSchema(t *testing.T) {
	genSpans := func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []Option
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		return genIntegrationTestSpans(
			t,
			func(t *testing.T, w *Writer) {
				err := w.WriteMessages(context.Background(), testMessages...)
				require.NoError(t, err, "Expected to write message to topic")
			},
			func(t *testing.T, r *Reader) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				readMsg, err := r.FetchMessage(ctx)
				require.NoError(t, err, "Expected to consume message")
				assert.Equal(t, testMessages[0].Value, readMsg.Value, "Values should be equal")

				err = r.CommitMessages(context.Background(), readMsg)
				assert.NoError(t, err, "Expected CommitMessages to not return an error")
			},
			opts,
			opts,
		)
	}
	namingschematest.NewKafkaTest(genSpans)(t)
}
