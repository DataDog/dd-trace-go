// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestWrapConsumer(t *testing.T) {
	cfg := newIntegrationTestConfig(t)
	cfg.Version = sarama.MinVersion

	mt := mocktracer.Start()
	defer mt.Stop()

	client, err := sarama.NewClient(kafkaBrokers, cfg)
	require.NoError(t, err)
	defer client.Close()

	consumer, err := sarama.NewConsumerFromClient(client)
	require.NoError(t, err)
	defer consumer.Close()

	consumer = WrapConsumer(consumer, WithDataStreams())

	partitionConsumer, err := consumer.ConsumePartition("test-topic", 0, 0)
	require.NoError(t, err)
	msg1 := <-partitionConsumer.Messages()
	msg2 := <-partitionConsumer.Messages()
	err = partitionConsumer.Close()
	require.NoError(t, err)
	// wait for the channel to be closed
	<-partitionConsumer.Messages()
	waitForSpans(mt, 2)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	{
		s := spans[0]
		spanctx, err := tracer.Extract(NewConsumerMessageCarrier(msg1))
		assert.NoError(t, err)
		assert.Equal(t, spanctx.TraceID(), s.TraceID(),
			"span context should be injected into the consumer message headers")

		assert.Equal(t, int32(0), s.Tag(ext.MessagingKafkaPartition))
		assert.NotNil(t, s.Tag("offset"))
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "Consume Topic test-topic", s.Tag(ext.ResourceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "kafka.consume", s.OperationName())
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindConsumer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))

		assertDSMConsumerPathway(t, "test-topic", "", msg1, false)
	}
	{
		s := spans[1]
		spanctx, err := tracer.Extract(NewConsumerMessageCarrier(msg2))
		assert.NoError(t, err)
		assert.Equal(t, spanctx.TraceID(), s.TraceID(),
			"span context should be injected into the consumer message headers")

		assert.Equal(t, int32(0), s.Tag(ext.MessagingKafkaPartition))
		assert.NotNil(t, s.Tag("offset"))
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "Consume Topic test-topic", s.Tag(ext.ResourceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "kafka.consume", s.OperationName())
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindConsumer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))

		assertDSMConsumerPathway(t, "test-topic", "", msg2, false)
	}
}
