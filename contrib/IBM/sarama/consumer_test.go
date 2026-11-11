// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"fmt"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/IBM/sarama/mocks"
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
	waitForSpans(t, mt, 2, 5*time.Second)

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
		WithConsumerCustomTag(
			"messaging.kafka.key",
			func(msg *sarama.ConsumerMessage) any {
				return string(msg.Key)
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
	waitForSpans(t, mt, 1, 5*time.Second)

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

// TestWrapConsumerSharesConfigWithPartitionConsumers checks that partition
// consumers created via a wrapped consumer use the consumer's config: they
// report the cluster ID fetched by the consumer and start no fetch of their
// own. It needs no broker.
func TestWrapConsumerSharesConfigWithPartitionConsumers(t *testing.T) {
	const topic = "test-topic"
	// Nothing listens on these addresses, so the consumer's cluster ID fetch
	// fails quickly and the test sets the cluster ID itself.
	brokers := []string{"127.0.0.1:1", "127.0.0.1:2"}

	mt := mocktracer.Start()
	defer mt.Stop()

	mc := mocks.NewConsumer(t, nil)
	mc.ExpectConsumePartition(topic, 0, sarama.OffsetOldest).YieldMessage(&sarama.ConsumerMessage{Topic: topic})
	mc.ExpectConsumePartition(topic, 1, sarama.OffsetOldest).YieldMessage(&sarama.ConsumerMessage{Topic: topic, Partition: 1})

	c := WrapConsumer(mc, WithDataStreams(), WithBrokers(brokers))
	wc := c.(*consumer)
	require.Len(t, wc.closeAsync, 1, "the consumer fetches the cluster ID")
	wc.cfg.SetClusterID("test-cluster-id")

	for partition := range int32(2) {
		pc, err := c.ConsumePartition(topic, partition, sarama.OffsetOldest)
		require.NoError(t, err)
		wpc := pc.(*partitionConsumer)
		assert.Empty(t, wpc.closeAsync, "the partition consumer must not fetch the cluster ID again")
		assert.Same(t, wc.cfg, wpc.dispatcher.(*wrappedDispatcher).cfg)

		<-pc.Messages()
		require.NoError(t, pc.Close())
		// wait for the channel to be closed
		<-pc.Messages()
	}
	require.NoError(t, c.Close())
	waitForSpans(t, mt, 2, 5*time.Second)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	for _, s := range spans {
		assert.Equal(t, "test-cluster-id", s.Tag(ext.MessagingKafkaClusterID))
		assert.Equal(t, "127.0.0.1:1,127.0.0.1:2", s.Tag(ext.KafkaBootstrapServers))
	}
}

func TestWrapPartitionConsumerBootstrapServers(t *testing.T) {
	for _, tt := range []struct {
		name     string
		opts     []Option
		expected any
	}{
		{name: "without brokers", expected: nil},
		{name: "with brokers", opts: []Option{WithBrokers([]string{"broker-1:9092", "broker-2:9092"})}, expected: "broker-1:9092,broker-2:9092"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const topic = "test-topic"

			mt := mocktracer.Start()
			defer mt.Stop()

			mc := mocks.NewConsumer(t, nil)
			mc.ExpectConsumePartition(topic, 0, sarama.OffsetOldest).YieldMessage(&sarama.ConsumerMessage{Topic: topic})
			pc, err := mc.ConsumePartition(topic, 0, sarama.OffsetOldest)
			require.NoError(t, err)

			// Data streams is disabled, so no cluster ID fetch is started.
			wpc := WrapPartitionConsumer(pc, tt.opts...)
			assert.Empty(t, wpc.(*partitionConsumer).closeAsync)
			<-wpc.Messages()
			require.NoError(t, wpc.Close())
			<-wpc.Messages()
			waitForSpans(t, mt, 1, 5*time.Second)

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			assert.Equal(t, tt.expected, spans[0].Tag(ext.KafkaBootstrapServers))
			assert.Nil(t, spans[0].Tag(ext.MessagingKafkaClusterID))
		})
	}
}
