// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package kafka

import (
	"context"
	"testing"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that clients are traced when confluent-kafka-go
// is imported under another name.
type TestCaseAliasedImport struct{ mockCluster }

func (tc *TestCaseAliasedImport) Run(_ context.Context, t *testing.T) {
	p, err := ckafka.NewProducer(tc.producerConfig())
	require.NoError(t, err)
	delivery := make(chan ckafka.Event, 1)
	require.NoError(t, p.Produce(mockMessage("alias"), delivery))
	requireDelivered(t, delivery)
	p.Close()

	c, err := ckafka.NewConsumer(tc.consumerConfig("alias"))
	require.NoError(t, err)
	assign(t, c)
	msg := pollMessage(t, c.Poll)
	require.Equal(t, "alias", string(msg.Key))
	require.NoError(t, c.Close())
}

func (tc *TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return trace.Traces{tc.mockTrace()}
}
func (*TestCaseAliasedImport) ExpectedSpanCount() int { return 2 }
