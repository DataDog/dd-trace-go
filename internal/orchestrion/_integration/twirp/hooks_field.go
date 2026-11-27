// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package twirp

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/twitchtv/twirp"
	"github.com/twitchtv/twirp/example"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseHooksField checks that a literal which sets its own Hooks keeps
// them and also gets the twirp span.
type TestCaseHooksField struct {
	client   example.Haberdasher
	received atomic.Int32
}

func (tc *TestCaseHooksField) Setup(_ context.Context, t *testing.T) {
	opts := &twirp.ServerOptions{
		Hooks: &twirp.ServerHooks{
			RequestReceived: func(ctx context.Context) (context.Context, error) {
				tc.received.Add(1)
				return ctx, nil
			},
		},
	}
	tc.client = serve(t, example.NewHaberdasherServer(&randomHaberdasher{}, twirp.WithServerHooks(opts.Hooks)))
}

func (tc *TestCaseHooksField) Run(ctx context.Context, t *testing.T) {
	makeHat(ctx, t, tc.client)
	require.Equal(t, int32(1), tc.received.Load(), "the literal's own hook must still run")
}

func (*TestCaseHooksField) ExpectedSpanCount() int {
	return spansPerRequest
}

func (*TestCaseHooksField) ExpectedTraces() trace.Traces {
	return expectedTraces()
}
