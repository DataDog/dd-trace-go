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

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

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

func generateIntegrationTestSpans(t *testing.T, writerOp func(t *testing.T, w *Writer), readerOp func(t *testing.T, r *Reader), writerOpts []Option, readerOpts []Option) (mocktracer.Span, mocktracer.Span) {
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

	producerSpan, consumerSpan := spans[0], spans[1]

	// they should be linked via headers
	assert.Equal(t, producerSpan.TraceID(), consumerSpan.TraceID(), "Trace IDs should match")
	return producerSpan, consumerSpan
}

func TestReadMessageFunctional(t *testing.T) {
	skipIntegrationTest(t)

	s0, s1 := generateIntegrationTestSpans(
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
	skipIntegrationTest(t)

	s0, s1 := generateIntegrationTestSpans(
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
	skipIntegrationTest(t)

	createSpans := func(t *testing.T, opts ...Option) (mocktracer.Span, mocktracer.Span) {
		return generateIntegrationTestSpans(
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

	testCases := []struct {
		name                      string
		schemaVersion             namingschema.Version
		serviceNameOverride       string
		ddService                 string
		wantProducerServiceName   string
		wantConsumerServiceName   string
		wantProducerOperationName string
		wantConsumerOperationName string
	}{
		{
			name:                      "schema v0",
			schemaVersion:             namingschema.SchemaV0,
			serviceNameOverride:       "",
			ddService:                 "",
			wantProducerServiceName:   "kafka",
			wantConsumerServiceName:   "kafka",
			wantProducerOperationName: "kafka.produce",
			wantConsumerOperationName: "kafka.consume",
		},
		{
			name:                      "schema v0 with DD_SERVICE",
			schemaVersion:             namingschema.SchemaV0,
			serviceNameOverride:       "",
			ddService:                 "dd-service",
			wantProducerServiceName:   "kafka",
			wantConsumerServiceName:   "dd-service",
			wantProducerOperationName: "kafka.produce",
			wantConsumerOperationName: "kafka.consume",
		},
		{
			name:                      "schema v0 with service override",
			schemaVersion:             namingschema.SchemaV0,
			serviceNameOverride:       "service-override",
			ddService:                 "dd-service",
			wantProducerServiceName:   "service-override",
			wantConsumerServiceName:   "service-override",
			wantProducerOperationName: "kafka.produce",
			wantConsumerOperationName: "kafka.consume",
		},
		{
			name:                      "schema v1",
			schemaVersion:             namingschema.SchemaV1,
			serviceNameOverride:       "",
			ddService:                 "",
			wantProducerServiceName:   "kafka",
			wantConsumerServiceName:   "kafka",
			wantProducerOperationName: "kafka.send",
			wantConsumerOperationName: "kafka.process",
		},
		{
			name:                      "schema v1 with DD_SERVICE",
			schemaVersion:             namingschema.SchemaV1,
			serviceNameOverride:       "",
			ddService:                 "dd-service",
			wantProducerServiceName:   "dd-service",
			wantConsumerServiceName:   "dd-service",
			wantProducerOperationName: "kafka.send",
			wantConsumerOperationName: "kafka.process",
		},
		{
			name:                      "schema v1 with service override",
			schemaVersion:             namingschema.SchemaV1,
			serviceNameOverride:       "service-override",
			ddService:                 "dd-service",
			wantProducerServiceName:   "service-override",
			wantConsumerServiceName:   "service-override",
			wantProducerOperationName: "kafka.send",
			wantConsumerOperationName: "kafka.process",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			version := namingschema.GetVersion()
			defer namingschema.SetVersion(version)
			namingschema.SetVersion(tc.schemaVersion)

			if tc.ddService != "" {
				svc := globalconfig.ServiceName()
				defer globalconfig.SetServiceName(svc)
				globalconfig.SetServiceName(tc.ddService)
			}

			var opts []Option
			if tc.serviceNameOverride != "" {
				opts = append(opts, WithServiceName(tc.serviceNameOverride))
			}

			producerSpan, consumerSpan := createSpans(t, opts...)
			assert.Equal(t, tc.wantProducerServiceName, producerSpan.Tag(ext.ServiceName))
			assert.Equal(t, tc.wantConsumerServiceName, consumerSpan.Tag(ext.ServiceName))

			assert.Equal(t, tc.wantProducerOperationName, producerSpan.OperationName())
			assert.Equal(t, tc.wantConsumerOperationName, consumerSpan.OperationName())
		})
	}
}
