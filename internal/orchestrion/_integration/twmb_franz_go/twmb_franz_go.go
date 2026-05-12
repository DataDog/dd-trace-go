// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package twmb_franz_go

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kafkatest "github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// The Kafka testcontainer is shared across all tests in this package (Reuse:
// true in StartKafkaTestContainer), so each TestCase* must use its own topic
// and consumer group to avoid cross-test pollution.

type TestCase struct {
	kafka *kafkatest.KafkaContainer
	addr  string
	topic string
	group string
}

func (tc *TestCase) setup(t *testing.T, topic, group string) {
	containers.SkipIfProviderIsNotHealthy(t)
	tc.topic = topic
	tc.group = group
	tc.kafka, tc.addr = containers.StartKafkaTestContainer(t, []string{topic})
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.setup(t, "twmb_franz_go_topic", "twmb_franz_go_group")
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	tc.produce(ctx, t)
	tc.consume(ctx, t)
}

func (tc *TestCase) produce(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	client, err := kgo.NewClient(
		kgo.SeedBrokers(tc.addr),
	)
	require.NoError(t, err)
	defer client.Close()

	record := &kgo.Record{
		Topic: tc.topic,
		Key:   []byte("key1"),
		Value: []byte("Hello World!"),
	}
	err = client.ProduceSync(ctx, record).FirstErr()
	require.NoError(t, err)
}

func (tc *TestCase) consume(_ context.Context, t *testing.T) {
	// Use a fresh context to avoid inheriting the parent span from produce;
	// instead the consume span should be a child of the produce span via
	// header propagation.
	ctx := context.Background()

	client, err := kgo.NewClient(
		kgo.SeedBrokers(tc.addr),
		kgo.ConsumeTopics(tc.topic),
		kgo.ConsumerGroup(tc.group),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	fetches := client.PollFetches(ctx)
	require.NoError(t, fetches.Err())

	records := fetches.Records()
	require.Len(t, records, 1)
	assert.Equal(t, "Hello World!", string(records[0].Value))
	assert.Equal(t, "key1", string(records[0].Key))

	client.Close()
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
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
						"resource": "Produce Topic " + tc.topic,
					},
					Meta: map[string]string{
						"span.kind": "producer",
						"component": "twmb/franz-go",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "kafka.consume",
								"type":     "queue",
								"service":  "kafka",
								"resource": "Consume Topic " + tc.topic,
							},
							Meta: map[string]string{
								"span.kind": "consumer",
								"component": "twmb/franz-go",
							},
						},
					},
				},
			},
		},
	}
}

// TestCaseEllipsis verifies orchestrion's append-args aspect correctly
// transforms kgo.NewClient(opts...) calls where options are spread from a
// []kgo.Opt slice. The transformation is non-trivial for this form since
// kgo.NewClient(opts..., extra) is invalid Go.
type TestCaseEllipsis struct {
	TestCase
}

func (tc *TestCaseEllipsis) Setup(_ context.Context, t *testing.T) {
	tc.setup(t, "twmb_franz_go_ellipsis_topic", "twmb_franz_go_ellipsis_group")
}

func (tc *TestCaseEllipsis) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	produceOpts := []kgo.Opt{kgo.SeedBrokers(tc.addr)}
	producer, err := kgo.NewClient(produceOpts...)
	require.NoError(t, err)
	defer producer.Close()

	record := &kgo.Record{Topic: tc.topic, Key: []byte("key1"), Value: []byte("Hello World!")}
	require.NoError(t, producer.ProduceSync(ctx, record).FirstErr())

	consumeOpts := []kgo.Opt{
		kgo.SeedBrokers(tc.addr),
		kgo.ConsumeTopics(tc.topic),
		kgo.ConsumerGroup(tc.group),
	}
	consumer, err := kgo.NewClient(consumeOpts...)
	require.NoError(t, err)

	fetchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fetches := consumer.PollFetches(fetchCtx)
	require.NoError(t, fetches.Err())

	records := fetches.Records()
	require.Len(t, records, 1)
	assert.Equal(t, "Hello World!", string(records[0].Value))

	consumer.Close()
}

// TestCaseNoArgs verifies orchestrion's append-args aspect correctly
// injects WithTracing() into kgo.NewClient() calls with no arguments.
// Brokers are configured post-creation via UpdateSeedBrokers so we can
// still exercise a real produce and observe the resulting span.
type TestCaseNoArgs struct {
	TestCase
}

func (tc *TestCaseNoArgs) Setup(_ context.Context, t *testing.T) {
	tc.setup(t, "twmb_franz_go_noargs_topic", "twmb_franz_go_noargs_group")
}

func (tc *TestCaseNoArgs) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	producer, err := kgo.NewClient()
	require.NoError(t, err)
	defer producer.Close()

	require.NoError(t, producer.UpdateSeedBrokers(tc.addr))

	record := &kgo.Record{Topic: tc.topic, Key: []byte("key1"), Value: []byte("Hello World!")}
	require.NoError(t, producer.ProduceSync(ctx, record).FirstErr())
}

func (tc *TestCaseNoArgs) ExpectedTraces() trace.Traces {
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
						"resource": "Produce Topic " + tc.topic,
					},
					Meta: map[string]string{
						"span.kind": "producer",
						"component": "twmb/franz-go",
					},
				},
			},
		},
	}
}
