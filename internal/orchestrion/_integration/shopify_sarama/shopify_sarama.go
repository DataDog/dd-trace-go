// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package shopify_sarama

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/containers"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/Shopify/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/kafka"
)

const (
	topic     = "gotest"
	partition = int32(0)
)

type TestCase struct {
	server *kafka.KafkaContainer
	cfg    *sarama.Config
	addrs  []string
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	tc.cfg = sarama.NewConfig()
	tc.cfg.Version = sarama.V0_11_0_0
	tc.cfg.Producer.Return.Successes = true

	container, addr := containers.StartKafkaTestContainer(t)
	tc.server = container
	tc.addrs = []string{addr}
}

func produceMessage(t *testing.T, addrs []string, cfg *sarama.Config) {
	t.Helper()

	producer, err := sarama.NewSyncProducer(addrs, cfg)
	require.NoError(t, err, "failed to create producer")
	defer func() { assert.NoError(t, producer.Close(), "failed to close producer") }()

	_, _, err = producer.SendMessage(&sarama.ProducerMessage{
		Topic:     topic,
		Partition: partition,
		Value:     sarama.StringEncoder("Hello, World!"),
	})
	require.NoError(t, err, "failed to send message")
	_, _, err = producer.SendMessage(&sarama.ProducerMessage{
		Topic:     topic,
		Partition: partition,
		Value:     sarama.StringEncoder("Another message to avoid flaky tests"),
	})
	require.NoError(t, err, "failed to send message")
}

func consumeMessage(t *testing.T, addrs []string, cfg *sarama.Config) {
	t.Helper()

	consumer, err := sarama.NewConsumer(addrs, cfg)
	require.NoError(t, err, "failed to create consumer")
	defer func() { assert.NoError(t, consumer.Close(), "failed to close consumer") }()

	partitionConsumer, err := consumer.ConsumePartition(topic, partition, sarama.OffsetOldest)
	require.NoError(t, err, "failed to create partition consumer")
	defer func() { assert.NoError(t, partitionConsumer.Close(), "failed to close partition consumer") }()

	expectedMessages := []string{"Hello, World!", "Another message to avoid flaky tests"}
	for i := 0; i < len(expectedMessages); i++ {
		select {
		case msg := <-partitionConsumer.Messages():
			require.Equal(t, expectedMessages[i], string(msg.Value))
		case <-time.After(15 * time.Second):
			t.Fatal("timed out waiting for message")
		}
	}
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	produceMessage(t, tc.addrs, tc.cfg)
	consumeMessage(t, tc.addrs, tc.cfg)
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":    "kafka.produce",
				"type":    "queue",
				"service": "kafka",
			},
			Meta: map[string]string{
				"span.kind": "producer",
				"component": "Shopify/sarama",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":    "kafka.consume",
						"type":    "queue",
						"service": "kafka",
					},
					Meta: map[string]string{
						"span.kind": "consumer",
						"component": "Shopify/sarama",
					},
				},
			},
		},
	}
}
