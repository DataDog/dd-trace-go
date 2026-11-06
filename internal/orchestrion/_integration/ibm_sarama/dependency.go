// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ibm_sarama

import (
	"context"
	"testing"

	"example.com/saramadep/ibm"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/ibm_sarama/internal/kafkamock"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that producers and consumers built in a dependency
// module, not in the module being built, are traced once.
type TestCaseDependency struct{ mockBroker }

func (tc *TestCaseDependency) Run(_ context.Context, t *testing.T) {
	sp, err := ibm.NewSyncProducer(tc.addrs, tc.cfg)
	require.NoError(t, err)
	require.NoError(t, kafkamock.SendSync(sp))

	ap, err := ibm.NewAsyncProducer(tc.addrs, tc.cfg)
	require.NoError(t, err)
	require.NoError(t, kafkamock.SendAsync(ap))

	c, err := ibm.NewConsumer(tc.addrs, tc.cfg)
	require.NoError(t, err)
	require.NoError(t, kafkamock.Consume(c))
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{produceSpan(), consumeSpan()}
}
func (*TestCaseDependency) ExpectedSpanCount() int { return 3 }
