// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otelc/pkg/hook/hooktest"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

const testTopic = "otelc-hooks"

// The helpers below run the hooks the way the trampolines do around the
// library calls.

func produce(p *kafka.Producer, msg *kafka.Message, deliveryChan chan kafka.Event) error {
	ictx := hooktest.NewMockHookContext(p, msg, deliveryChan)
	BeforeProducerProduce(ictx, p, msg, deliveryChan)
	err := p.Produce(msg, ictx.GetParam(2).(chan kafka.Event))
	AfterProducerProduce(ictx, err)
	return err
}

func closeProducer(p *kafka.Producer) {
	BeforeProducerClose(hooktest.NewMockHookContext(p), p)
	p.Close()
}

func poll(c *kafka.Consumer, timeoutMs int) kafka.Event {
	ictx := hooktest.NewMockHookContext(c, timeoutMs)
	BeforeConsumerPoll(ictx, c, timeoutMs)
	evt := c.Poll(timeoutMs)
	AfterConsumerPoll(ictx, evt)
	return evt
}

func closeConsumer(c *kafka.Consumer) error {
	ictx := hooktest.NewMockHookContext(c)
	BeforeConsumerClose(ictx, c)
	err := c.Close()
	AfterConsumerClose(ictx, err)
	return err
}

func newCluster(t *testing.T) *kafka.ConfigMap {
	t.Helper()
	cluster, err := kafka.NewMockCluster(1)
	require.NoError(t, err)
	t.Cleanup(cluster.Close)
	return &kafka.ConfigMap{"bootstrap.servers": cluster.BootstrapServers()}
}

func testMessage() *kafka.Message {
	topic := testTopic
	return &kafka.Message{TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: 0}, Value: []byte("value")}
}

func requireDelivered(t *testing.T, events chan kafka.Event) {
	t.Helper()
	select {
	case evt := <-events:
		msg, ok := evt.(*kafka.Message)
		require.True(t, ok)
		require.NoError(t, msg.TopicPartition.Error)
	case <-time.After(10 * time.Second):
		require.FailNow(t, "no delivery report")
	}
}

func TestHooksTraceRegisteredClients(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	conf := newCluster(t)

	p, err := kafka.NewProducer(conf)
	require.NoError(t, err)
	TraceProducer(p, conf)
	delivery := make(chan kafka.Event, 1)
	require.NoError(t, produce(p, testMessage(), delivery))
	requireDelivered(t, delivery)
	closeProducer(p)

	consumerConf := &kafka.ConfigMap{"group.id": "otelc-hooks"}
	for k, v := range *conf {
		consumerConf.SetKey(k, v)
	}
	c, err := kafka.NewConsumer(consumerConf)
	require.NoError(t, err)
	TraceConsumer(c, consumerConf)
	topic := testTopic
	require.NoError(t, c.Assign([]kafka.TopicPartition{{Topic: &topic, Partition: 0, Offset: kafka.OffsetBeginning}}))
	require.Eventually(t, func() bool {
		_, ok := poll(c, 100).(*kafka.Message)
		return ok
	}, 10*time.Second, time.Millisecond)
	require.NoError(t, closeConsumer(c))

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	assert.Equal(t, "kafka.produce", spans[0].OperationName())
	assert.Equal(t, "kafka.consume", spans[1].OperationName())
	assert.Equal(t, spans[0].SpanID(), spans[1].ParentID())
	for _, span := range spans {
		assert.Equal(t, "confluentinc/confluent-kafka-go/kafka.v2", span.Tag("component"))
		assert.NotEmpty(t, span.Tag("messaging.kafka.bootstrap.servers"))
	}
}

func TestHooksLeaveUnregisteredClientsAlone(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	conf := newCluster(t)

	p, err := kafka.NewProducer(conf)
	require.NoError(t, err)
	delivery := make(chan kafka.Event, 1)
	ictx := hooktest.NewMockHookContext(p, testMessage(), delivery)
	BeforeProducerProduce(ictx, p, testMessage(), delivery)
	assert.Nil(t, ictx.GetData())
	assert.Equal(t, delivery, ictx.GetParam(2))

	ictx = hooktest.NewMockHookContext(p)
	BeforeProducerProduceChannel(ictx, p)
	assert.Nil(t, ictx.GetData())
	p.Close()

	assert.Empty(t, mt.FinishedSpans())
}

func TestRegistryFreesClients(t *testing.T) {
	conf := newCluster(t)
	p, err := kafka.NewProducer(conf)
	require.NoError(t, err)
	TraceProducer(p, conf)
	key := weak.Make(p)
	require.NotNil(t, tracedProducer(p))
	closeProducer(p)

	p = nil
	assert.Eventually(t, func() bool {
		runtime.GC()
		_, ok := producers.Load(key)
		return !ok
	}, 10*time.Second, 10*time.Millisecond, "the registry must not keep a producer alive")
}
