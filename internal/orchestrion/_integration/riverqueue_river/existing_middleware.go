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

// countingMiddleware is an application middleware. It checks that the woven
// tracing middleware runs outermost, so the work span is already active.
type countingMiddleware struct {
	river.MiddlewareDefaults
	sawSpan chan bool
}

func (m *countingMiddleware) Work(ctx context.Context, _ *rivertype.JobRow, doInner func(context.Context) error) error {
	_, ok := tracer.SpanFromContext(ctx)
	m.sawSpan <- ok
	return doInner(ctx)
}

// TestCaseExistingMiddleware builds a client whose config already lists a
// middleware, and is shared: the caller's slice and config must be left as is.
type TestCaseExistingMiddleware struct {
	base
	mw     *countingMiddleware
	config *river.Config
}

func (tc *TestCaseExistingMiddleware) Setup(ctx context.Context, t *testing.T) {
	tc.mw = &countingMiddleware{sawSpan: make(chan bool, 1)}
	tc.config = baseConfig()
	tc.config.Middleware = make([]rivertype.Middleware, 1, 4)
	tc.config.Middleware[0] = tc.mw

	client, err := river.NewClient(openDriver(t), tc.config)
	require.NoError(t, err)
	require.Len(t, tc.config.Middleware, 1, "the caller's config must not be modified")
	require.Nil(t, tc.config.Middleware[:2][1], "the caller's spare capacity must not be written to")
	tc.start(ctx, t, client)
}

func (tc *TestCaseExistingMiddleware) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	_, err := tc.client.Insert(ctx, sortArgs{Strings: []string{"b", "a"}}, nil)
	require.NoError(t, err)
	span.Finish()
	tc.wait(t, 1)
	require.True(t, <-tc.mw.sawSpan, "the tracing middleware must run before application middleware")
}

func (*TestCaseExistingMiddleware) ExpectedTraces() trace.Traces {
	return insertAndWorkTrace()
}
