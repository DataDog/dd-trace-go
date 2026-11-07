// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package riverqueue_river

import (
	"context"
	"testing"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCase builds a client with no middleware at all: Orchestrion adds the
// tracing middleware to river.Config.Middleware.
type TestCase struct {
	base
}

func (tc *TestCase) Setup(ctx context.Context, t *testing.T) {
	client, err := river.NewClient(openDriver(t), baseConfig())
	require.NoError(t, err)
	tc.start(ctx, t, client)
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	_, err := tc.client.Insert(ctx, sortArgs{Strings: []string{"b", "a"}}, nil)
	require.NoError(t, err)
	span.Finish()
	tc.wait(t, 1)
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return insertAndWorkTrace()
}
