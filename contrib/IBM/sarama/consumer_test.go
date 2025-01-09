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
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestWrapConsumer(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	broker := sarama.NewMockBroker(t, 0)
	defer broker.Close()

	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).
			SetLeader("test-topic", 0, broker.BrokerID()),
		"OffsetRequest": sarama.NewMockOffsetResponse(t).
			SetOffset("test-topic", 0, sarama.OffsetOldest, 0).
			SetOffset("test-topic", 0, sarama.OffsetNewest, 1),
		"FetchRequest": sarama.NewMockFetchResponse(t, 1).
			SetMessage("test-topic", 0, 0, sarama.StringEncoder("hello")).
			SetMessage("test-topic", 0, 1, sarama.StringEncoder("world")),
	})
	cfg := sarama.NewConfig()
	cfg.Version = sarama.MinVersion

	client, err := sarama.NewClient([]string{broker.Addr()}, cfg)
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

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	{
		s := spans[0]
		spanctx, err := tracer.Extract(NewConsumerMessageCarrier(msg1))
		assert.NoError(t, err)
		assert.Equal(t, spanctx.TraceIDLower(), s.TraceID(),
			"span context should be injected into the consumer message headers")

		assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
		assert.Equal(t, float64(0), s.Tag("offset"))
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
		assert.Equal(t, spanctx.TraceIDLower(), s.TraceID(),
			"span context should be injected into the consumer message headers")

		assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
		assert.Equal(t, float64(1), s.Tag("offset"))
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
