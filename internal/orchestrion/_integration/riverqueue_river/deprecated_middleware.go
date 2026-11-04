// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package riverqueue_river

import (
	"context"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDeprecatedMiddleware uses the deprecated WorkerMiddleware field.
// River rejects a config that sets both it and Middleware, so the tracing
// middleware must be added to the deprecated fields instead.
type TestCaseDeprecatedMiddleware struct {
	base
}

func (tc *TestCaseDeprecatedMiddleware) Setup(ctx context.Context, t *testing.T) {
	config := baseConfig()
	config.WorkerMiddleware = []rivertype.WorkerMiddleware{
		river.WorkerMiddlewareFunc(func(ctx context.Context, _ *rivertype.JobRow, doInner func(context.Context) error) error {
			return doInner(ctx)
		}),
	}
	client, err := river.NewClient(openDriver(t), config)
	require.NoError(t, err)
	tc.start(ctx, t, client)
}

func (tc *TestCaseDeprecatedMiddleware) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	_, err := tc.client.Insert(ctx, sortArgs{Strings: []string{"b", "a"}}, nil)
	require.NoError(t, err)
	span.Finish()
	tc.wait(t, 1)
}

func (*TestCaseDeprecatedMiddleware) ExpectedTraces() trace.Traces {
	return insertAndWorkTrace()
}
