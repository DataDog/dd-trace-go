// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package shopify_sarama

import (
	"context"
	"testing"

	"example.com/saramadep/shopify"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/shopify_sarama/internal/kafkamock"
)

// TestCaseDependency checks that producers and consumers built in a dependency
// module, not in the module being built, are traced once.
type TestCaseDependency struct{ mockBroker }

func (tc *TestCaseDependency) Run(_ context.Context, t *testing.T) {
	sp, err := shopify.NewSyncProducer(tc.addrs, tc.cfg)
	require.NoError(t, err)
	require.NoError(t, kafkamock.SendSync(sp))

	ap, err := shopify.NewAsyncProducer(tc.addrs, tc.cfg)
	require.NoError(t, err)
	require.NoError(t, kafkamock.SendAsync(ap))

	c, err := shopify.NewConsumer(tc.addrs, tc.cfg)
	require.NoError(t, err)
	require.NoError(t, kafkamock.Consume(c))
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{produceSpan(), consumeSpan()}
}
func (*TestCaseDependency) ExpectedSpanCount() int { return 3 }
