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

const (
	topic         = "twmb_franz_go_topic"
	consumerGroup = "twmb_franz_go_group"
)

type TestCase struct {
	kafka *kafkatest.KafkaContainer
	addr  string
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)
	tc.kafka, tc.addr = containers.StartKafkaTestContainer(t, []string{topic})
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
		Topic: topic,
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
		kgo.ConsumeTopics(topic),
		kgo.ConsumerGroup(consumerGroup),
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
						"resource": "Produce Topic " + topic,
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
								"resource": "Consume Topic " + topic,
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
