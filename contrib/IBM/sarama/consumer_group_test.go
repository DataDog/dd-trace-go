// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"
	"log"
	"net"
	"sync"
	"testing"
	"time"

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

	// Wait for the message to be received by the consumer before cancelling.
	consumeMsg := <-handler.rcvMessages

	// The consumer only finishes spans when the next message is received and when the consumer shuts down.
	// To make sure the last span is finished, we need to cancel the context to make sure this last span is finished.
	cancel()
	wg.Wait()

	waitForSpans(t, mt, 2, 5*time.Second)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

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

type cleanupRecorder struct {
	sarama.ConsumerGroupHandler
	cleanups int
}

func (h *cleanupRecorder) Cleanup(sarama.ConsumerGroupSession) error {
	h.cleanups++
	return nil
}

// TestConsumerGroupHandlerCleanupCancelsClusterIDFetch checks that Cleanup
// cancels the cluster ID fetch the handler started, without waiting for it,
// and calls the wrapped handler's Cleanup.
func TestConsumerGroupHandlerCleanupCancelsClusterIDFetch(t *testing.T) {
	t.Run("cancels the fetch", func(t *testing.T) {
		inner := &cleanupRecorder{}
		h := WrapConsumerGroupHandler(inner).(*consumerGroupHandler)
		cancels := 0
		h.cancelFetch = func() { cancels++ }

		require.NoError(t, h.Cleanup(nil))
		assert.Equal(t, 1, cancels)
		assert.Equal(t, 1, inner.cleanups)
	})
	t.Run("does not wait for a stalled broker", func(t *testing.T) {
		// The listener accepts connections and never answers, like a stalled
		// broker.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer ln.Close()
		accepted := make(chan struct{}, 1)
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				select {
				case accepted <- struct{}{}:
				default:
				}
			}
		}()

		inner := &cleanupRecorder{}
		h := WrapConsumerGroupHandler(inner, WithDataStreams(), WithBrokers([]string{ln.Addr().String()}))
		select {
		case <-accepted:
		case <-time.After(5 * time.Second):
			t.Fatal("the cluster ID fetch did not connect to the broker")
		}
		// Let the fetch send its metadata request and wait for the answer.
		time.Sleep(100 * time.Millisecond)
		start := time.Now()
		require.NoError(t, h.Cleanup(nil))
		assert.Less(t, time.Since(start), 5*time.Second, "Cleanup must not wait for the broker's network timeout")
		assert.Equal(t, 1, inner.cleanups)
	})
}
