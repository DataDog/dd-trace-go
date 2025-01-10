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
	mt := mocktracer.Start()
	defer mt.Stop()

	seedBroker := sarama.NewMockBroker(t, 1)
	defer seedBroker.Close()

	leader := sarama.NewMockBroker(t, 2)
	defer leader.Close()

	metadataResponse := new(sarama.MetadataResponse)
	metadataResponse.Version = 1
	metadataResponse.AddBroker(leader.Addr(), leader.BrokerID())
	metadataResponse.AddTopicPartition("my_topic", 0, leader.BrokerID(), nil, nil, nil, sarama.ErrNoError)
	seedBroker.Returns(metadataResponse)

	prodSuccess := new(sarama.ProduceResponse)
	prodSuccess.Version = 2
	prodSuccess.AddTopicPartition("my_topic", 0, sarama.ErrNoError)
	leader.Returns(prodSuccess)

	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_11_0_0 // first version that supports headers
	cfg.Producer.Return.Successes = true

	producer, err := sarama.NewSyncProducer([]string{seedBroker.Addr()}, cfg)
	require.NoError(t, err)
	producer = WrapSyncProducer(cfg, producer, WithDataStreams())

	msg1 := &sarama.ProducerMessage{
		Topic:    "my_topic",
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test",
	}
	_, _, err = producer.SendMessage(msg1)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
	{
		s := spans[0]
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "Produce Topic my_topic", s.Tag(ext.ResourceName))
		assert.Equal(t, "kafka.produce", s.OperationName())
		assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
		assert.Equal(t, float64(0), s.Tag("offset"))
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))

		assertDSMProducerPathway(t, "my_topic", msg1)
	}
}

func TestSyncProducerSendMessages(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	seedBroker := sarama.NewMockBroker(t, 1)
	defer seedBroker.Close()
	leader := sarama.NewMockBroker(t, 2)
	defer leader.Close()

	metadataResponse := new(sarama.MetadataResponse)
	metadataResponse.Version = 1
	metadataResponse.AddBroker(leader.Addr(), leader.BrokerID())
	metadataResponse.AddTopicPartition("my_topic", 0, leader.BrokerID(), nil, nil, nil, sarama.ErrNoError)
	seedBroker.Returns(metadataResponse)

	prodSuccess := new(sarama.ProduceResponse)
	prodSuccess.Version = 2
	prodSuccess.AddTopicPartition("my_topic", 0, sarama.ErrNoError)
	leader.Returns(prodSuccess)

	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_11_0_0 // first version that supports headers
	cfg.Producer.Return.Successes = true
	cfg.Producer.Flush.Messages = 2

	producer, err := sarama.NewSyncProducer([]string{seedBroker.Addr()}, cfg)
	require.NoError(t, err)
	producer = WrapSyncProducer(cfg, producer, WithDataStreams())

	msg1 := &sarama.ProducerMessage{
		Topic:    "my_topic",
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test",
	}
	msg2 := &sarama.ProducerMessage{
		Topic:    "my_topic",
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
		assert.Equal(t, "Produce Topic my_topic", s.Tag(ext.ResourceName))
		assert.Equal(t, "kafka.produce", s.OperationName())
		assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
	}

	for _, msg := range []*sarama.ProducerMessage{msg1, msg2} {
		assertDSMProducerPathway(t, "my_topic", msg)
	}
}

func TestWrapAsyncProducer(t *testing.T) {
	// the default for producers is a fire-and-forget model that doesn't return
	// successes
	t.Run("Without Successes", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		broker := newMockBroker(t)

		cfg := sarama.NewConfig()
		cfg.Version = sarama.V0_11_0_0
		producer, err := sarama.NewAsyncProducer([]string{broker.Addr()}, cfg)
		require.NoError(t, err)
		producer = WrapAsyncProducer(nil, producer, WithDataStreams())

		msg1 := &sarama.ProducerMessage{
			Topic: "my_topic",
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
			assert.Equal(t, "Produce Topic my_topic", s.Tag(ext.ResourceName))
			assert.Equal(t, "kafka.produce", s.OperationName())

			// these tags are set in the finishProducerSpan function, but in this case it's never used, and instead we
			// automatically finish spans after being started because we don't have a way to know when they are finished.
			assert.Nil(t, s.Tag(ext.MessagingKafkaPartition))
			assert.Nil(t, s.Tag("offset"))

			assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
			assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))

			assertDSMProducerPathway(t, "my_topic", msg1)
		}
	})

	t.Run("With Successes", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		broker := newMockBroker(t)

		cfg := sarama.NewConfig()
		cfg.Version = sarama.V0_11_0_0
		cfg.Producer.Return.Successes = true

		producer, err := sarama.NewAsyncProducer([]string{broker.Addr()}, cfg)
		require.NoError(t, err)
		producer = WrapAsyncProducer(cfg, producer, WithDataStreams())

		msg1 := &sarama.ProducerMessage{
			Topic: "my_topic",
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
			assert.Equal(t, "Produce Topic my_topic", s.Tag(ext.ResourceName))
			assert.Equal(t, "kafka.produce", s.OperationName())
			assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
			assert.Equal(t, float64(0), s.Tag("offset"))
			assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
			assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))

			assertDSMProducerPathway(t, "my_topic", msg1)
		}
	})
}
