// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"errors"
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

func TestHandleUnknownError(t *testing.T) {
	t.Run("Unknown Error", func(t *testing.T) {
		cfg := new(config)
		defaults(cfg)
		cfg.headerInjectionEnabled = true

		err := sarama.ErrUnknown
		handleUnknownError(cfg, err)
		assert.False(t, cfg.headerInjectionEnabled)
	})

	t.Run("Other Error", func(t *testing.T) {
		cfg := new(config)
		defaults(cfg)
		cfg.headerInjectionEnabled = true

		err := sarama.ErrBrokerNotFound
		handleUnknownError(cfg, err)
		assert.True(t, cfg.headerInjectionEnabled)
	})
}

func TestSyncProducerSendMessageErrorHandling(t *testing.T) {
	t.Run("Unknown Error", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Create a mock producer that returns ErrUnknown
		mockProducer := &mockSyncProducer{
			sendMessageFunc: func(msg *sarama.ProducerMessage) (int32, int64, error) {
				return 0, 0, sarama.ErrUnknown
			},
		}

		cfg := new(config)
		defaults(cfg)
		cfg.dataStreamsEnabled = true
		cfg.headerInjectionEnabled = true

		producer := &syncProducer{
			SyncProducer: mockProducer,
			version:      sarama.V0_11_0_0,
			cfg:          cfg,
		}

		msg := &sarama.ProducerMessage{
			Topic: "test-topic",
			Value: sarama.StringEncoder("test"),
		}

		_, _, err := producer.SendMessage(msg)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, sarama.ErrUnknown))
		assert.False(t, cfg.headerInjectionEnabled, "header injection should be disabled after UNKNOWN_SERVER_ERROR")

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		s := spans[0]
		assert.Equal(t, "kafka.produce", s.OperationName())
		assert.Equal(t, "Produce Topic test-topic", s.Tag(ext.ResourceName))
	})

	t.Run("Success", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Create a mock producer that returns success
		mockProducer := &mockSyncProducer{
			sendMessageFunc: func(msg *sarama.ProducerMessage) (int32, int64, error) {
				return 1, 42, nil
			},
		}

		cfg := new(config)
		defaults(cfg)
		cfg.dataStreamsEnabled = true
		cfg.headerInjectionEnabled = true

		producer := &syncProducer{
			SyncProducer: mockProducer,
			version:      sarama.V0_11_0_0,
			cfg:          cfg,
		}

		msg := &sarama.ProducerMessage{
			Topic: "test-topic",
			Value: sarama.StringEncoder("test"),
		}

		partition, offset, err := producer.SendMessage(msg)
		assert.NoError(t, err)
		assert.Equal(t, int32(1), partition)
		assert.Equal(t, int64(42), offset)
		assert.True(t, cfg.headerInjectionEnabled, "header injection should remain enabled after successful send")

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		s := spans[0]
		assert.Equal(t, "kafka.produce", s.OperationName())
		assert.Equal(t, "Produce Topic test-topic", s.Tag(ext.ResourceName))
		assert.Equal(t, float64(1), s.Tag(ext.MessagingKafkaPartition))
	})
}

// mockSyncProducer implements sarama.SyncProducer for testing
type mockSyncProducer struct {
	sarama.SyncProducer
	sendMessageFunc func(*sarama.ProducerMessage) (int32, int64, error)
}

func (m *mockSyncProducer) SendMessage(msg *sarama.ProducerMessage) (int32, int64, error) {
	return m.sendMessageFunc(msg)
}

func (m *mockSyncProducer) SendMessages(msgs []*sarama.ProducerMessage) error {
	return nil
}

func (m *mockSyncProducer) Close() error {
	return nil
}
