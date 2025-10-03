// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"fmt"
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestWrapConsumer(t *testing.T) {
	cfg := newIntegrationTestConfig(t)
	cfg.Version = sarama.MinVersion
	topic := topicName(t)

	mt := mocktracer.Start()
	defer mt.Stop()

	client, err := sarama.NewClient(kafkaBrokers, cfg)
	require.NoError(t, err)
	defer client.Close()

	consumer, err := sarama.NewConsumerFromClient(client)
	require.NoError(t, err)
	consumer = WrapConsumer(consumer, WithDataStreams())
	defer consumer.Close()

	partitionConsumer, err := consumer.ConsumePartition(topic, 0, 0)
	require.NoError(t, err)
	defer partitionConsumer.Close()

	p, err := sarama.NewSyncProducer(kafkaBrokers, cfg)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, p.Close())
	}()

	for i := 1; i <= 2; i++ {
		produceMsg := &sarama.ProducerMessage{
			Topic:    topic,
			Value:    sarama.StringEncoder(fmt.Sprintf("test %d", i)),
			Metadata: fmt.Sprintf("test %d", i),
		}
		_, _, err = p.SendMessage(produceMsg)
		require.NoError(t, err)
	}

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
		assert.Equal(t, spanctx.TraceIDLower(), s.TraceID(),
			"span context should be injected into the consumer message headers")

		assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
		assert.NotNil(t, s.Tag("offset"))
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "Consume Topic "+topic, s.Tag(ext.ResourceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "kafka.consume", s.OperationName())
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindConsumer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
		assert.Equal(t, topic, s.Tag("messaging.destination.name"))

		assertDSMConsumerPathway(t, topic, "", msg1, false)
	}
	{
		s := spans[1]
		spanctx, err := tracer.Extract(NewConsumerMessageCarrier(msg2))
		assert.NoError(t, err)
		assert.Equal(t, spanctx.TraceIDLower(), s.TraceID(),
			"span context should be injected into the consumer message headers")

		assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
		assert.NotNil(t, s.Tag("offset"))
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "Consume Topic "+topic, s.Tag(ext.ResourceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "kafka.consume", s.OperationName())
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindConsumer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
		assert.Equal(t, topic, s.Tag("messaging.destination.name"))

		assertDSMConsumerPathway(t, topic, "", msg2, false)
	}
}

func TestWrapConsumerWithCustomConsumerSpanOptions(t *testing.T) {
	cfg := newIntegrationTestConfig(t)
	cfg.Version = sarama.MinVersion
	topic := topicName(t)

	mt := mocktracer.Start()
	defer mt.Stop()

	client, err := sarama.NewClient(kafkaBrokers, cfg)
	require.NoError(t, err)
	defer client.Close()

	consumer, err := sarama.NewConsumerFromClient(client)
	require.NoError(t, err)
	consumer = WrapConsumer(
		consumer,
		WithDataStreams(),
		WithCustomConsumerSpanOptions(
			func(msg *sarama.ConsumerMessage) []tracer.StartSpanOption {
				return []tracer.StartSpanOption{
					tracer.Tag("messaging.kafka.key", string(msg.Key)),
				}
			},
		),
	)
	defer consumer.Close()

	partitionConsumer, err := consumer.ConsumePartition(topic, 0, 0)
	require.NoError(t, err)
	defer partitionConsumer.Close()

	p, err := sarama.NewSyncProducer(kafkaBrokers, cfg)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, p.Close())
	}()

	produceMsg := &sarama.ProducerMessage{
		Topic:    topic,
		Key:      sarama.StringEncoder("test key"),
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test 1",
	}
	_, _, err = p.SendMessage(produceMsg)
	require.NoError(t, err)

	msg1 := <-partitionConsumer.Messages()
	err = partitionConsumer.Close()
	require.NoError(t, err)
	// wait for the channel to be closed
	<-partitionConsumer.Messages()
	waitForSpans(mt, 1)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	s := spans[0]
	spanctx, err := tracer.Extract(NewConsumerMessageCarrier(msg1))
	assert.NoError(t, err)
	assert.Equal(t, spanctx.TraceIDLower(), s.TraceID(),
		"span context should be injected into the consumer message headers")

	assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
	assert.NotNil(t, s.Tag("offset"))
	assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
	assert.Equal(t, "Consume Topic "+topic, s.Tag(ext.ResourceName))
	assert.Equal(t, "queue", s.Tag(ext.SpanType))
	assert.Equal(t, "kafka.consume", s.OperationName())
	assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindConsumer, s.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
	assert.Equal(t, topic, s.Tag("messaging.destination.name"))
	assert.Equal(t, "test key", s.Tag("messaging.kafka.key"))

	assertDSMConsumerPathway(t, topic, "", msg1, false)
}
