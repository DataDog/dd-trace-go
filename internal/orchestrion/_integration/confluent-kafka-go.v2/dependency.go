// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package kafka

import (
	"context"
	"testing"

	"example.com/confluentkafkadep/kafkav2"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that clients created in a dependency module, not
// in the module being built, are traced once.
type TestCaseDependency struct{ mockCluster }

func (tc *TestCaseDependency) Run(_ context.Context, t *testing.T) {
	p, err := kafkav2.NewProducer(tc.producerConfig())
	require.NoError(t, err)
	delivery := make(chan kafka.Event, 1)
	require.NoError(t, p.Produce(mockMessage("dependency"), delivery))
	requireDelivered(t, delivery)
	p.Close()

	c, err := kafkav2.NewConsumer(tc.consumerConfig("dependency"))
	require.NoError(t, err)
	assign(t, c)
	msg := pollMessage(t, c.Poll)
	require.Equal(t, "dependency", string(msg.Key))
	require.NoError(t, c.Close())
}

func (tc *TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{tc.mockTrace()}
}
func (*TestCaseDependency) ExpectedSpanCount() int { return 2 }
