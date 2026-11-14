// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ibm_sarama

import (
	"context"
	"testing"

	kafka "github.com/IBM/sarama"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/ibm_sarama/internal/kafkamock"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a producer and a consumer are traced when
// sarama is imported under another name.
type TestCaseAliasedImport struct{ mockBroker }

// Run also passes a nil config, which sarama replaces with its own.
func (tc *TestCaseAliasedImport) Run(_ context.Context, t *testing.T) {
	p, err := kafka.NewSyncProducer(tc.addrs, nil)
	require.NoError(t, err)
	require.NoError(t, kafkamock.SendSync(p))

	c, err := kafka.NewConsumerFromClient(tc.newClient(t))
	require.NoError(t, err)
	require.NoError(t, kafkamock.Consume(c))
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return trace.Traces{produceSpan(), consumeSpan()}
}
func (*TestCaseAliasedImport) ExpectedSpanCount() int { return 2 }
