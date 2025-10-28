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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

func TestSyncProducer(t *testing.T) {
	cfg := newIntegrationTestConfig(t)
	topic := topicName(t)

	mt := mocktracer.Start()
	defer mt.Stop()

	producer, err := sarama.NewSyncProducer(kafkaBrokers, cfg)
	require.NoError(t, err)
	producer = WrapSyncProducer(cfg, producer, WithDataStreams())
	defer func() {
		assert.NoError(t, producer.Close())
	}()

	msg1 := &sarama.ProducerMessage{
		Topic:    topic,
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test",
	}
	_, _, err = producer.SendMessage(msg1)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	{
		s := spans[0]
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "Produce Topic "+topic, s.Tag(ext.ResourceName))
		assert.Equal(t, "kafka.produce", s.OperationName())
		assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
		assert.NotNil(t, s.Tag("offset"))
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
		assert.Equal(t, topic, s.Tag("messaging.destination.name"))

		assertDSMProducerPathway(t, topic, msg1)
	}
}

func TestSyncProducerSendMessages(t *testing.T) {
	cfg := newIntegrationTestConfig(t)
	topic := topicName(t)

	mt := mocktracer.Start()
	defer mt.Stop()

	producer, err := sarama.NewSyncProducer(kafkaBrokers, cfg)
	require.NoError(t, err)
	producer = WrapSyncProducer(cfg, producer, WithDataStreams())
	defer func() {
		assert.NoError(t, producer.Close())
	}()

	msg1 := &sarama.ProducerMessage{
		Topic:    topic,
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test",
	}
	msg2 := &sarama.ProducerMessage{
		Topic:    topic,
		Value:    sarama.StringEncoder("test 2"),
		Metadata: "test",
	}
	err = producer.SendMessages([]*sarama.ProducerMessage{msg1, msg2})
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	for _, s := range spans {
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "Produce Topic "+topic, s.Tag(ext.ResourceName))
		assert.Equal(t, "kafka.produce", s.OperationName())
		assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
		assert.Equal(t, topic, s.Tag("messaging.destination.name"))
	}

	for _, msg := range []*sarama.ProducerMessage{msg1, msg2} {
		assertDSMProducerPathway(t, topic, msg)
	}
}

func TestSyncProducerWithCustomSpanOptions(t *testing.T) {
	cfg := newIntegrationTestConfig(t)
	topic := topicName(t)

	mt := mocktracer.Start()
	defer mt.Stop()

	producer, err := sarama.NewSyncProducer(kafkaBrokers, cfg)
	require.NoError(t, err)
	producer = WrapSyncProducer(
		cfg,
		producer,
		WithDataStreams(),
		WithProducerCustomTag(
			"kafka.messaging.key",
			func(msg *sarama.ProducerMessage) any {
				key, err := msg.Key.Encode()
				assert.NoError(t, err)

				return key
			},
		),
	)
	defer func() {
		assert.NoError(t, producer.Close())
	}()

	msg1 := &sarama.ProducerMessage{
		Topic:    topic,
		Key:      sarama.StringEncoder("test key"),
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test",
	}
	_, _, err = producer.SendMessage(msg1)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	{
		s := spans[0]
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "Produce Topic "+topic, s.Tag(ext.ResourceName))
		assert.Equal(t, "kafka.produce", s.OperationName())
		assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
		assert.NotNil(t, s.Tag("offset"))
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
		assert.Equal(t, topic, s.Tag("messaging.destination.name"))
		assert.Equal(t, "test key", s.Tag("messaging.kafka.key"))

		assertDSMProducerPathway(t, topic, msg1)
	}
}

func TestWrapAsyncProducer(t *testing.T) {
	// the default for producers is a fire-and-forget model that doesn't return
	// successes
	t.Run("Without Successes", func(t *testing.T) {
		cfg := newIntegrationTestConfig(t)
		cfg.Producer.Return.Successes = false
		topic := topicName(t)

		mt := mocktracer.Start()
		defer mt.Stop()

		producer, err := sarama.NewAsyncProducer(kafkaBrokers, cfg)
		require.NoError(t, err)
		producer = WrapAsyncProducer(cfg, producer, WithDataStreams())
		defer func() {
			assert.NoError(t, producer.Close())
		}()

		msg1 := &sarama.ProducerMessage{
			Topic: topic,
			Value: sarama.StringEncoder("test 1"),
		}
		producer.Input() <- msg1

		waitForSpans(mt, 1)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		{
			s := spans[0]
			assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
			assert.Equal(t, "queue", s.Tag(ext.SpanType))
			assert.Equal(t, "Produce Topic "+topic, s.Tag(ext.ResourceName))
			assert.Equal(t, "kafka.produce", s.OperationName())

			// these tags are set in the finishProducerSpan function, but in this case it's never used, and instead we
			// automatically finish spans after being started because we don't have a way to know when they are finished.
			assert.Nil(t, s.Tag(ext.MessagingKafkaPartition))
			assert.Nil(t, s.Tag("offset"))

			assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
			assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
			assert.Equal(t, topic, s.Tag("messaging.destination.name"))

			assertDSMProducerPathway(t, topic, msg1)
		}
	})

	t.Run("With Successes", func(t *testing.T) {
		cfg := newIntegrationTestConfig(t)
		cfg.Producer.Return.Successes = true
		topic := topicName(t)

		mt := mocktracer.Start()
		defer mt.Stop()

		producer, err := sarama.NewAsyncProducer(kafkaBrokers, cfg)
		require.NoError(t, err)
		producer = WrapAsyncProducer(cfg, producer, WithDataStreams())
		defer func() {
			assert.NoError(t, producer.Close())
		}()

		msg1 := &sarama.ProducerMessage{
			Topic: topic,
			Value: sarama.StringEncoder("test 1"),
		}
		producer.Input() <- msg1
		<-producer.Successes()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		{
			s := spans[0]
			assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
			assert.Equal(t, "queue", s.Tag(ext.SpanType))
			assert.Equal(t, "Produce Topic "+topic, s.Tag(ext.ResourceName))
			assert.Equal(t, "kafka.produce", s.OperationName())
			assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
			assert.NotNil(t, s.Tag("offset"))
			assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
			assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
			assert.Equal(t, topic, s.Tag("messaging.destination.name"))

			assertDSMProducerPathway(t, topic, msg1)
		}
	})
}
