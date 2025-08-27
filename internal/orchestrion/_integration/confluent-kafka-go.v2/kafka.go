// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package kafka

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/stretchr/testify/require"
	kafkatest "github.com/testcontainers/testcontainers-go/modules/kafka"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

var (
	topic         = "confluent_kafka_v2_default_test"
	consumerGroup = "confluent_kafka_v2_default_test"
	partition     = int32(0)
)

type TestCase struct {
	container *kafkatest.KafkaContainer
	addr      []string
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)
	container, addr := containers.StartKafkaTestContainer(t, []string{topic})
	tc.container = container
	tc.addr = []string{addr}
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	tc.produceMessage(t)
	tc.consumeMessage(ctx, t)
}

func (tc *TestCase) kafkaBootstrapServers() string {
	return strings.Join(tc.addr, ",")
}

func (tc *TestCase) produceMessage(t *testing.T) {
	t.Helper()

	cfg := &kafka.ConfigMap{
		"bootstrap.servers":   tc.kafkaBootstrapServers(),
		"go.delivery.reports": true,
	}
	delivery := make(chan kafka.Event, 1)

	producer, err := kafka.NewProducer(cfg)
	require.NoError(t, err, "failed to create producer")
	defer func() {
		<-delivery
		producer.Close()
	}()

	err = producer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &topic,
			Partition: partition,
		},
		Key:   []byte("key2"),
		Value: []byte("value2"),
	}, delivery)
	require.NoError(t, err, "failed to send message")
}

func (tc *TestCase) consumeMessage(_ context.Context, t *testing.T) {
	t.Helper()

	cfg := &kafka.ConfigMap{
		"group.id":                 consumerGroup,
		"bootstrap.servers":        tc.kafkaBootstrapServers(),
		"fetch.wait.max.ms":        500,
		"socket.timeout.ms":        1500,
		"session.timeout.ms":       1500,
		"enable.auto.offset.store": false,
	}
	c, err := kafka.NewConsumer(cfg)
	require.NoError(t, err, "failed to create consumer")
	defer c.Close()

	err = c.Assign([]kafka.TopicPartition{
		{Topic: &topic, Partition: 0},
	})
	require.NoError(t, err)

	m, err := backoff.RetryWithData(
		func() (*kafka.Message, error) { return c.ReadMessage(3 * time.Second) },
		backoff.NewExponentialBackOff(),
	)
	require.NoError(t, err)

	_, err = c.CommitMessage(m)
	require.NoError(t, err)

	require.Equal(t, "key2", string(m.Key))
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "kafka.produce",
				"type":     "queue",
				"service":  "kafka",
				"resource": "Produce Topic " + topic,
			},
			Meta: map[string]string{
				"span.kind":        "producer",
				"component":        "confluentinc/confluent-kafka-go/kafka.v2",
				"messaging.system": "kafka",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "kafka.consume",
						"type":     "queue",
						"service":  "kafka",
						"resource": "Consume Topic " + topic,
					},
					Meta: map[string]string{
						"span.kind":                         "consumer",
						"component":                         "confluentinc/confluent-kafka-go/kafka.v2",
						"messaging.system":                  "kafka",
						"messaging.kafka.bootstrap.servers": "localhost",
					},
				},
			},
		},
	}
}
