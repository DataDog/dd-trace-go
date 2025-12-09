// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"
	"log"
	"sync"
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

func TestWrapConsumerGroupHandler(t *testing.T) {
	cfg := newIntegrationTestConfig(t)
	topic := topicName(t)
	groupID := "IBM/sarama/TestWrapConsumerGroupHandler"

	mt := mocktracer.Start()
	defer mt.Stop()

	cg, err := sarama.NewConsumerGroup(kafkaBrokers, groupID, cfg)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, cg.Close())
	}()

	handler := &testConsumerGroupHandler{
		T:           t,
		ready:       make(chan bool),
		rcvMessages: make(chan *sarama.ConsumerMessage, 1),
	}
	tracedHandler := WrapConsumerGroupHandler(handler, WithDataStreams(), WithGroupID(groupID))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			// `Consume` should be called inside an infinite loop, when a
			// server-side rebalance happens, the consumer session will need to be
			// recreated to get the new claims
			if err := cg.Consume(ctx, []string{topic}, tracedHandler); err != nil {
				assert.ErrorIs(t, err, sarama.ErrClosedConsumerGroup)
				return
			}
			// check if context was cancelled, signaling that the consumer should stop
			if ctx.Err() != nil {
				return
			}
		}
	}()

	<-handler.ready // Await till the consumer has been set up
	log.Println("Sarama consumer up and running!...")

	p, err := sarama.NewSyncProducer(kafkaBrokers, cfg)
	require.NoError(t, err)
	p = WrapSyncProducer(cfg, p, WithDataStreams())
	defer func() {
		assert.NoError(t, p.Close())
	}()

	produceMsg := &sarama.ProducerMessage{
		Topic:    topic,
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test",
	}
	_, _, err = p.SendMessage(produceMsg)
	require.NoError(t, err)

	waitForSpans(mt, 2)
	cancel()
	wg.Wait()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	consumeMsg := <-handler.rcvMessages

	s0 := spans[0]
	assert.Equal(t, "kafka", s0.Tag(ext.ServiceName))
	assert.Equal(t, "queue", s0.Tag(ext.SpanType))
	assert.Equal(t, "Produce Topic "+topic, s0.Tag(ext.ResourceName))
	assert.Equal(t, "kafka.produce", s0.OperationName())
	assert.Equal(t, float64(0), s0.Tag(ext.MessagingKafkaPartition))
	assert.NotNil(t, s0.Tag("offset"))
	assert.Equal(t, "IBM/sarama", s0.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindProducer, s0.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s0.Tag(ext.MessagingSystem))
	assert.Equal(t, topic, s0.Tag("messaging.destination.name"))

	assertDSMProducerPathway(t, topic, produceMsg)

	s1 := spans[1]
	assert.Equal(t, "kafka", s1.Tag(ext.ServiceName))
	assert.Equal(t, "queue", s1.Tag(ext.SpanType))
	assert.Equal(t, "Consume Topic "+topic, s1.Tag(ext.ResourceName))
	assert.Equal(t, "kafka.consume", s1.OperationName())
	assert.Equal(t, float64(0), s1.Tag(ext.MessagingKafkaPartition))
	assert.NotNil(t, s1.Tag("offset"))
	assert.Equal(t, "IBM/sarama", s1.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindConsumer, s1.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s1.Tag(ext.MessagingSystem))
	assert.Equal(t, topic, s1.Tag("messaging.destination.name"))

	assertDSMConsumerPathway(t, topic, groupID, consumeMsg, true)

	assert.Equal(t, s0.SpanID(), s1.ParentID(), "spans are not parent-child")
}

type testConsumerGroupHandler struct {
	*testing.T
	ready       chan bool
	rcvMessages chan *sarama.ConsumerMessage
}

func (t *testConsumerGroupHandler) Setup(_ sarama.ConsumerGroupSession) error {
	// Mark the consumer as ready
	close(t.ready)
	return nil
}

func (t *testConsumerGroupHandler) Cleanup(_ sarama.ConsumerGroupSession) error {
	return nil
}

func (t *testConsumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				t.T.Log("message channel was closed")
				return nil
			}
			t.T.Logf("Message claimed: value = %s, timestamp = %v, topic = %s", string(msg.Value), msg.Timestamp, msg.Topic)
			session.MarkMessage(msg, "")
			t.rcvMessages <- msg

		// Should return when `session.Context()` is done.
		// If not, will raise `ErrRebalanceInProgress` or `read tcp <ip>:<port>: i/o timeout` when kafka rebalance. see:
		// https://github.com/IBM/sarama/issues/1192
		case <-session.Context().Done():
			return nil
		}
	}
}
