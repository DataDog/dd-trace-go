// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package segmentio_kafka_v0

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/cenkalti/backoff/v4"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kafkatest "github.com/testcontainers/testcontainers-go/modules/kafka"
)

const (
	topicA        = "segmentio_kafka_topic_A"
	topicB        = "segmentio_kafka_topic_B"
	consumerGroup = "segmentio_kafka_group_A"
)

type TestCase struct {
	kafka *kafkatest.KafkaContainer
	addr  string
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	tc.kafka, tc.addr = containers.StartKafkaTestContainer(t, []string{topicA, topicB})
}

func (tc *TestCase) newReader(topic string) *kafka.Reader {
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:  []string{tc.addr},
		GroupID:  consumerGroup,
		Topic:    topic,
		MaxWait:  10 * time.Millisecond,
		MaxBytes: 10e6, // 10MB
	})
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	tc.produce(ctx, t)
	tc.consume(ctx, t)
}

func (tc *TestCase) produce(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	messages := []kafka.Message{
		{
			Topic: topicA,
			Key:   []byte("Key-A"),
			Value: []byte("Hello World!"),
		},
		{
			Topic: topicB,
			Key:   []byte("Key-A"),
			Value: []byte("Second message"),
		},
		{
			Topic: topicB,
			Key:   []byte("Key-A"),
			Value: []byte("Third message"),
		},
	}
	err := backoff.Retry(
		func() error {
			writer := &kafka.Writer{
				Addr:     kafka.TCP(tc.addr),
				Balancer: &kafka.LeastBytes{},
			}
			defer func() { require.NoError(t, writer.Close()) }()

			err := writer.WriteMessages(ctx, messages...)
			if !errors.Is(err, kafka.UnknownTopicOrPartition) {
				return backoff.Permanent(err)
			}
			t.Logf("failed to produce messages (retrying...): %s", err.Error())
			return err
		},
		backoff.NewExponentialBackOff(backoff.WithMaxElapsedTime(30*time.Second)),
	)
	require.NoError(t, err)
}

func (tc *TestCase) consume(_ context.Context, t *testing.T) {
	ctx := context.Background() // Diregard local trace context as it'd override the propagated one

	// We consume from separate goroutines to blur out the goroutine local storage's context weaving, more accurately
	// simulating "real-world" usage of the Kafka client.
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		readerA := tc.newReader(topicA)
		defer func() { require.NoError(t, readerA.Close()) }()
		m, err := readerA.ReadMessage(ctx)
		require.NoError(t, err)
		assert.Equal(t, "Hello World!", string(m.Value))
		assert.Equal(t, "Key-A", string(m.Key))
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		readerB := tc.newReader(topicB)
		defer func() { require.NoError(t, readerB.Close()) }()
		m, err := readerB.FetchMessage(ctx)
		require.NoError(t, err)
		assert.Equal(t, "Second message", string(m.Value))
		assert.Equal(t, "Key-A", string(m.Key))
		err = readerB.CommitMessages(ctx, m)
		require.NoError(t, err)
	}()
	wg.Wait()
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "kafka.produce",
						"type":     "queue",
						"service":  "kafka",
						"resource": "Produce Topic " + topicA,
					},
					Meta: map[string]string{
						"span.kind": "producer",
						"component": "segmentio/kafka.go.v0",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "kafka.consume",
								"type":     "queue",
								"service":  "kafka",
								"resource": "Consume Topic " + topicA,
							},
							Meta: map[string]string{
								"span.kind": "consumer",
								"component": "segmentio/kafka.go.v0",
							},
						},
					},
				},
				{
					Tags: map[string]any{
						"name":     "kafka.produce",
						"type":     "queue",
						"service":  "kafka",
						"resource": "Produce Topic " + topicB,
					},
					Meta: map[string]string{
						"span.kind": "producer",
						"component": "segmentio/kafka.go.v0",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "kafka.consume",
								"type":     "queue",
								"service":  "kafka",
								"resource": "Consume Topic " + topicB,
							},
							Meta: map[string]string{
								"span.kind": "consumer",
								"component": "segmentio/kafka.go.v0",
							},
						},
					},
				},
				{
					Tags: map[string]any{
						"name":     "kafka.produce",
						"type":     "queue",
						"service":  "kafka",
						"resource": "Produce Topic " + topicB,
					},
					Meta: map[string]string{
						"span.kind": "producer",
						"component": "segmentio/kafka.go.v0",
					},
					Children: nil,
				},
			},
		},
	}
}
