// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func genTestSpans(t *testing.T, serviceOverride string) []mocktracer.Span {
	var opts []Option
	if serviceOverride != "" {
		opts = append(opts, WithServiceName(serviceOverride))
	}
	mt := mocktracer.Start()
	defer mt.Stop()

	broker := sarama.NewMockBroker(t, 1)
	defer broker.Close()

	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).
			SetLeader("test-topic", 0, broker.BrokerID()),
		"OffsetRequest": sarama.NewMockOffsetResponse(t).
			SetOffset("test-topic", 0, sarama.OffsetOldest, 0).
			SetOffset("test-topic", 0, sarama.OffsetNewest, 1),
		"FetchRequest": sarama.NewMockFetchResponse(t, 1).
			SetMessage("test-topic", 0, 0, sarama.StringEncoder("hello")),
		"ProduceRequest": sarama.NewMockProduceResponse(t).
			SetError("test-topic", 0, sarama.ErrNoError),
	})
	cfg := sarama.NewConfig()
	cfg.Version = sarama.MinVersion
	cfg.Producer.Return.Successes = true
	cfg.Producer.Flush.Messages = 1

	producer, err := sarama.NewSyncProducer([]string{broker.Addr()}, cfg)
	require.NoError(t, err)
	producer = WrapSyncProducer(cfg, producer, opts...)

	c, err := sarama.NewConsumer([]string{broker.Addr()}, cfg)
	require.NoError(t, err)
	defer func(c sarama.Consumer) {
		err := c.Close()
		require.NoError(t, err)
	}(c)
	c = WrapConsumer(c, opts...)

	msg1 := &sarama.ProducerMessage{
		Topic:    "test-topic",
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test",
	}
	_, _, err = producer.SendMessage(msg1)
	require.NoError(t, err)

	pc, err := c.ConsumePartition("test-topic", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = <-pc.Messages()
	err = pc.Close()
	require.NoError(t, err)
	// wait for the channel to be closed
	<-pc.Messages()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	return spans
}

func TestConsumer(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	consumer, err := sarama.NewConsumerFromClient(client)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()

	consumer = WrapConsumer(consumer)

	partitionConsumer, err := consumer.ConsumePartition("test-topic", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	msg1 := <-partitionConsumer.Messages()
	msg2 := <-partitionConsumer.Messages()
	partitionConsumer.Close()
	// wait for the channel to be closed
	<-partitionConsumer.Messages()

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2)
	{
		s := spans[0]
		spanctx, err := tracer.Extract(NewConsumerMessageCarrier(msg1))
		assert.NoError(t, err)
		assert.Equal(t, spanctx.TraceID(), s.TraceID(),
			"span context should be injected into the consumer message headers")

		assert.Equal(t, int32(0), s.Tag(ext.MessagingKafkaPartition))
		assert.Equal(t, int64(0), s.Tag("offset"))
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "Consume Topic test-topic", s.Tag(ext.ResourceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "kafka.consume", s.OperationName())
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindConsumer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
	}
	{
		s := spans[1]
		spanctx, err := tracer.Extract(NewConsumerMessageCarrier(msg2))
		assert.NoError(t, err)
		assert.Equal(t, spanctx.TraceID(), s.TraceID(),
			"span context should be injected into the consumer message headers")

		assert.Equal(t, int32(0), s.Tag(ext.MessagingKafkaPartition))
		assert.Equal(t, int64(1), s.Tag("offset"))
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "Consume Topic test-topic", s.Tag(ext.ResourceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "kafka.consume", s.OperationName())
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindConsumer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
	}
}

func TestSyncProducer(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	seedBroker := sarama.NewMockBroker(t, 1)
	defer seedBroker.Close()

	leader := sarama.NewMockBroker(t, 2)
	defer leader.Close()

	metadataResponse := new(sarama.MetadataResponse)
	metadataResponse.AddBroker(leader.Addr(), leader.BrokerID())
	metadataResponse.AddTopicPartition("my_topic", 0, leader.BrokerID(), nil, nil, nil, sarama.ErrNoError)
	seedBroker.Returns(metadataResponse)

	prodSuccess := new(sarama.ProduceResponse)
	prodSuccess.AddTopicPartition("my_topic", 0, sarama.ErrNoError)
	leader.Returns(prodSuccess)

	cfg := sarama.NewConfig()
	cfg.Version = sarama.MinVersion
	cfg.Producer.Return.Successes = true

	producer, err := sarama.NewSyncProducer([]string{seedBroker.Addr()}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	producer = WrapSyncProducer(cfg, producer)

	msg1 := &sarama.ProducerMessage{
		Topic:    "my_topic",
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test",
	}
	producer.SendMessage(msg1)

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
	{
		s := spans[0]
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "Produce Topic my_topic", s.Tag(ext.ResourceName))
		assert.Equal(t, "kafka.produce", s.OperationName())
		assert.Equal(t, int32(0), s.Tag(ext.MessagingKafkaPartition))
		assert.Equal(t, int64(0), s.Tag("offset"))
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
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
	metadataResponse.AddBroker(leader.Addr(), leader.BrokerID())
	metadataResponse.AddTopicPartition("my_topic", 0, leader.BrokerID(), nil, nil, nil, sarama.ErrNoError)
	seedBroker.Returns(metadataResponse)

	prodSuccess := new(sarama.ProduceResponse)
	prodSuccess.AddTopicPartition("my_topic", 0, sarama.ErrNoError)
	leader.Returns(prodSuccess)

	cfg := sarama.NewConfig()
	cfg.Version = sarama.MinVersion
	cfg.Producer.Return.Successes = true
	cfg.Producer.Flush.Messages = 2

	producer, err := sarama.NewSyncProducer([]string{seedBroker.Addr()}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	producer = WrapSyncProducer(cfg, producer)

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
	producer.SendMessages([]*sarama.ProducerMessage{msg1, msg2})
	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2)
	for _, s := range spans {
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "Produce Topic my_topic", s.Tag(ext.ResourceName))
		assert.Equal(t, "kafka.produce", s.OperationName())
		assert.Equal(t, int32(0), s.Tag(ext.MessagingKafkaPartition))
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
	}
}

func TestAsyncProducer(t *testing.T) {
	// the default for producers is a fire-and-forget model that doesn't return
	// successes
	t.Run("Without Successes", func(t *testing.T) {
		t.Skip("Skipping test because sarama.MockBroker doesn't work with versions >= sarama.V0_11_0_0 " +
			"https://github.com/IBM/sarama/issues/1665")
		mt := mocktracer.Start()
		defer mt.Stop()

		broker := newMockBroker(t)

		cfg := sarama.NewConfig()
		cfg.Version = sarama.V0_11_0_0
		producer, err := sarama.NewAsyncProducer([]string{broker.Addr()}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		producer = WrapAsyncProducer(nil, producer)

		msg1 := &sarama.ProducerMessage{
			Topic: "my_topic",
			Value: sarama.StringEncoder("test 1"),
		}
		producer.Input() <- msg1

		waitForSpans(mt, 1)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		{
			s := spans[0]
			assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
			assert.Equal(t, "queue", s.Tag(ext.SpanType))
			assert.Equal(t, "Produce Topic my_topic", s.Tag(ext.ResourceName))
			assert.Equal(t, "kafka.produce", s.OperationName())
			assert.Equal(t, int32(0), s.Tag(ext.MessagingKafkaPartition))
			assert.Equal(t, int64(0), s.Tag("offset"))
			assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
			assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
		}
	})

	t.Run("With Successes", func(t *testing.T) {
		t.Skip("Skipping test because sarama.MockBroker doesn't work with versions >= sarama.V0_11_0_0 " +
			"https://github.com/IBM/sarama/issues/1665")
		mt := mocktracer.Start()
		defer mt.Stop()

		broker := newMockBroker(t)

		cfg := sarama.NewConfig()
		cfg.Version = sarama.V0_11_0_0
		cfg.Producer.Return.Successes = true

		producer, err := sarama.NewAsyncProducer([]string{broker.Addr()}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		producer = WrapAsyncProducer(cfg, producer)

		msg1 := &sarama.ProducerMessage{
			Topic: "my_topic",
			Value: sarama.StringEncoder("test 1"),
		}
		producer.Input() <- msg1
		<-producer.Successes()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		{
			s := spans[0]
			assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
			assert.Equal(t, "queue", s.Tag(ext.SpanType))
			assert.Equal(t, "Produce Topic my_topic", s.Tag(ext.ResourceName))
			assert.Equal(t, "kafka.produce", s.OperationName())
			assert.Equal(t, int32(0), s.Tag(ext.MessagingKafkaPartition))
			assert.Equal(t, int64(0), s.Tag("offset"))
			assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
			assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
		}
	})
}

func TestNamingSchema(t *testing.T) {
	namingschematest.NewKafkaTest(genTestSpans)(t)
}

func newMockBroker(t *testing.T) *sarama.MockBroker {
	broker := sarama.NewMockBroker(t, 1)

	metadataResponse := new(sarama.MetadataResponse)
	metadataResponse.AddBroker(broker.Addr(), broker.BrokerID())
	metadataResponse.AddTopicPartition("my_topic", 0, broker.BrokerID(), nil, nil, nil, sarama.ErrNoError)
	broker.Returns(metadataResponse)

	prodSuccess := new(sarama.ProduceResponse)
	prodSuccess.AddTopicPartition("my_topic", 0, sarama.ErrNoError)
	for i := 0; i < 10; i++ {
		broker.Returns(prodSuccess)
	}
	return broker
}

// waitForSpans polls the mock tracer until the expected number of spans
// appear
func waitForSpans(mt mocktracer.Tracer, sz int) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	for len(mt.FinishedSpans()) < sz {
		select {
		case <-ctx.Done():
			return
		default:
		}
		time.Sleep(time.Millisecond * 100)
	}
}
